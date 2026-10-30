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
