package serviceinstall

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func libraryFixture(content []byte) LibraryMaterialFile {
	d, _ := Find("java", "8")
	return LibraryMaterialFile{RecipeKey: d.Key, RecipeVersion: d.Version, RecipeRevision: d.Revision, RuntimeMode: "NATIVE", Name: "JDK 8", File: "jdk8.tar.gz", SHA256: rawHash(content), ByteSize: int64(len(content))}
}

func TestLibraryMaterialSnapshotAndCancellation(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	content := []byte("snapshot test, not an official package")
	in := libraryFixture(content)
	if err := os.WriteFile(filepath.Join(root, in.File), content, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(target, "snapshot")
	if err := CopyLibraryMaterial(context.Background(), root, out, in); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); string(data) != string(content) {
		t.Fatal("snapshot changed input bytes")
	}
	if CopyLibraryMaterial(context.Background(), root, out, in) == nil {
		t.Fatal("existing output overwritten")
	}
	if _, err := ValidateMaterial(out, in.SHA256, in.ByteSize, mustFindLibraryRecipe(t), "NATIVE"); err == nil {
		t.Fatal("a matching local hash bypassed the pinned vendor hash")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := filepath.Join(target, "canceled")
	if CopyLibraryMaterial(ctx, root, canceled, in) == nil {
		t.Fatal("canceled copy succeeded")
	}
	if _, err := os.Stat(canceled); !os.IsNotExist(err) {
		t.Fatal("canceled copy left an output")
	}
}

func mustFindLibraryRecipe(t *testing.T) Definition {
	t.Helper()
	d, err := Find("java", "8")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLibraryMaterialRejectsHashSizeLinkAndUnsafePaths(t *testing.T) {
	root := t.TempDir()
	content := []byte("local bytes")
	base := libraryFixture(content)
	if err := os.WriteFile(filepath.Join(root, base.File), content, 0600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../jdk.tar", "C:drive.tar", "a\\b", "a/b", ".", "..", "a\x00b", ""} {
		in := base
		in.File = bad
		if _, err := in.Definition(); err == nil {
			t.Fatal("unsafe path accepted", bad)
		}
	}
	for _, mutate := range []func(*LibraryMaterialFile){func(m *LibraryMaterialFile) { m.SHA256 = strings.Repeat("0", 64) }, func(m *LibraryMaterialFile) { m.ByteSize++ }, func(m *LibraryMaterialFile) { m.RecipeRevision = "replacement" }, func(m *LibraryMaterialFile) { m.RuntimeMode = "DOCKER" }} {
		in := base
		mutate(&in)
		if CopyLibraryMaterial(context.Background(), root, filepath.Join(t.TempDir(), "out"), in) == nil {
			t.Fatal("mutated manifest accepted", in)
		}
	}
	link := filepath.Join(root, "link.tar.gz")
	if err := os.Symlink(filepath.Join(root, base.File), link); err == nil {
		in := base
		in.File = filepath.Base(link)
		if CopyLibraryMaterial(context.Background(), root, filepath.Join(t.TempDir(), "link"), in) == nil {
			t.Fatal("symbolic link accepted")
		}
	}
}

func TestBundledRecipesAreAdditionalFrozenOfficialBytes(t *testing.T) {
	for _, old := range append(install03Definitions(), additionalDefinitions()...) {
		current, err := Find(old.Key, old.Version)
		before, _ := json.Marshal(old)
		after, _ := json.Marshal(current)
		if err != nil || string(before) != string(after) {
			t.Fatal("existing recipe identity changed", old.Key, old.Version)
		}
	}
	for _, d := range bundledDefinitions() {
		if err := ValidateDefinition(d); err != nil {
			t.Fatal(err)
		}
		if len(d.RuntimeModes) != 1 || d.RuntimeModes[0] != "NATIVE" || d.Native == nil {
			t.Fatal("bundled distribution package advertised an invented image", d.Key)
		}
		for _, part := range d.Native.Parts {
			if !strings.HasPrefix(part.URL, "https://") || !sha.MatchString(part.SHA256) || part.ByteSize < 1 || !LibraryFileName(part.Name) {
				t.Fatal("part is not frozen", part)
			}
		}
	}
}

func TestLibraryMaterialUsesTrustedResolverAndKeepsBuiltinMaterialIdentity(t *testing.T) {
	content := []byte("published custom archive")
	d := CommandExamples()[0]
	d.Key, d.Revision, d.SourceModes = "custom-library", "3", []string{"ARCHIVE"}
	m := libraryFixture(content)
	m.RecipeKey, m.RecipeVersion, m.RecipeRevision = d.Key, d.Version, d.Revision
	resolve := func(key, version string) (Definition, error) { return d, nil }
	if _, err := m.Definition(); err == nil {
		t.Fatal("custom recipe resolved without a trusted resolver")
	}
	if actual, err := m.Definition(resolve); err != nil || Digest(actual) != Digest(d) {
		t.Fatal("published custom definition changed", err)
	}
	root, output := t.TempDir(), filepath.Join(t.TempDir(), "snapshot")
	if err := os.WriteFile(filepath.Join(root, m.File), content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CopyLibraryMaterial(context.Background(), root, output, m, resolve); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*LibraryMaterialFile){
		func(m *LibraryMaterialFile) { m.RecipeKey = "custom-other" },
		func(m *LibraryMaterialFile) { m.RecipeVersion = "missing" },
		func(m *LibraryMaterialFile) { m.RecipeRevision = "2" },
		func(m *LibraryMaterialFile) { m.RuntimeMode = "DOCKER" },
		func(m *LibraryMaterialFile) { m.File = "../outside" },
	} {
		invalid := m
		mutate(&invalid)
		if _, err := invalid.Definition(resolve); err == nil {
			t.Fatal("resolver bypassed manifest validation", invalid)
		}
	}
	for _, builtin := range BuiltinCommandCatalog() {
		if builtin.Key == "java" && builtin.Version == "8" {
			builtin.Commands.Revision++
			builtin.Commands.Steps[0].Script += "\nprintf edited\n"
			entry := libraryFixture(content)
			got, err := entry.Definition(func(string, string) (Definition, error) { return builtin, nil })
			if err != nil || Digest(got) != Digest(mustFindLibraryRecipe(t)) {
				t.Fatal("command revision changed builtin material identity", err)
			}
			return
		}
	}
	t.Fatal("builtin Java fixture missing")
}
