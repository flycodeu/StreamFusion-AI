package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func materialCheckFixture(t *testing.T) (string, serviceinstall.MaterialLibrary) {
	t.Helper()
	dir := t.TempDir()
	content := []byte("transport fixture; not an official vendor package")
	digest := sha256.Sum256(content)
	d, err := serviceinstall.Find("java", "8")
	if err != nil {
		t.Fatal(err)
	}
	library := serviceinstall.MaterialLibrary{SchemaVersion: 1, Materials: []serviceinstall.LibraryMaterialFile{{RecipeKey: d.Key, RecipeVersion: d.Version, RecipeRevision: d.Revision, RuntimeMode: "NATIVE", Name: "JDK 8", File: "jdk package.tar.gz", SHA256: hex.EncodeToString(digest[:]), ByteSize: int64(len(content))}}}
	if err = os.WriteFile(filepath.Join(dir, library.Materials[0].File), content, 0600); err != nil {
		t.Fatal(err)
	}
	writeMaterialCheckFixture(t, dir, library)
	return dir, library
}

func writeMaterialCheckFixture(t *testing.T, dir string, library serviceinstall.MaterialLibrary) {
	t.Helper()
	raw, err := json.Marshal(library)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "library.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMaterialLibraryCheckIsReadOnlyAndDoesNotClaimVendorValidation(t *testing.T) {
	dir, library := materialCheckFixture(t)
	before, _ := os.ReadDir(dir)
	var output bytes.Buffer
	if err := checkMaterialLibrary([]string{"--material-library", dir}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "library.json\njdk package.tar.gz\n" {
		t.Fatal(output.String())
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatal("read-only check wrote files")
	}
	d, _ := library.Materials[0].Definition()
	if _, err := serviceinstall.ValidateMaterial(filepath.Join(dir, library.Materials[0].File), library.Materials[0].SHA256, library.Materials[0].ByteSize, d, "NATIVE"); err == nil {
		t.Fatal("transport fixture must still fail pinned vendor validation")
	}
	for _, args := range [][]string{nil, {"--state", filepath.Join(dir, "new-state")}, {"--material-library", dir, "unexpected"}} {
		if err := checkMaterialLibrary(args, &output); err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "new-state")); !os.IsNotExist(err) {
		t.Fatal("checker created state")
	}
}

func TestMaterialLibraryCheckRejectsUnsafeOrChangedManifest(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*serviceinstall.MaterialLibrary)
	}{
		{"revision", func(m *serviceinstall.MaterialLibrary) { m.Materials[0].RecipeRevision = "changed" }},
		{"hash", func(m *serviceinstall.MaterialLibrary) { m.Materials[0].SHA256 = strings.Repeat("0", 64) }},
		{"size", func(m *serviceinstall.MaterialLibrary) { m.Materials[0].ByteSize++ }},
		{"duplicate", func(m *serviceinstall.MaterialLibrary) { m.Materials = append(m.Materials, m.Materials[0]) }},
		{"manifest-alias", func(m *serviceinstall.MaterialLibrary) { m.Materials[0].File = "LIBRARY.JSON" }},
		{"trailing-dot", func(m *serviceinstall.MaterialLibrary) { m.Materials[0].File = "package." }},
		{"newline", func(m *serviceinstall.MaterialLibrary) { m.Materials[0].File = "package\nname" }},
		{"path", func(m *serviceinstall.MaterialLibrary) { m.Materials[0].File = "../package" }},
		{"null-materials", func(m *serviceinstall.MaterialLibrary) { m.Materials = nil }},
		{"too-many", func(m *serviceinstall.MaterialLibrary) { m.Materials = make([]serviceinstall.LibraryMaterialFile, 101) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir, library := materialCheckFixture(t)
			test.edit(&library)
			writeMaterialCheckFixture(t, dir, library)
			var output bytes.Buffer
			if err := checkMaterialLibrary([]string{"--material-library", dir}, &output); err == nil || output.Len() != 0 {
				t.Fatal("invalid manifest returned successful or partial output", err, output.String())
			}
		})
	}
	for _, raw := range []string{`{"schemaVersion":1,"schemaVersion":1,"materials":[]}`, `{"SchemaVersion":1,"materials":[]}`, `{"schemaVersion":1,"materials":[],"extra":true}`, `{"schemaVersion":1,"materials":[]} {}`} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "library.json"), []byte(raw), 0600)
		if err := checkMaterialLibrary([]string{"--material-library", dir}, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid JSON accepted", raw)
		}
	}
}

func TestMaterialLibraryCheckRejectsSourceAndAncestorLinks(t *testing.T) {
	dir, library := materialCheckFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := checkMaterialLibrary([]string{"--material-library", alias}, &bytes.Buffer{}); err == nil {
		t.Fatal("directory link accepted")
	}
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	writeMaterialCheckFixture(t, nested, serviceinstall.MaterialLibrary{SchemaVersion: 1, Materials: []serviceinstall.LibraryMaterialFile{}})
	if err := checkMaterialLibrary([]string{"--material-library", filepath.Join(alias, "nested")}, &bytes.Buffer{}); err == nil {
		t.Fatal("ancestor link accepted")
	}
	file := filepath.Join(dir, library.Materials[0].File)
	source := filepath.Join(t.TempDir(), "original")
	if err := os.Rename(file, source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, file); err != nil {
		t.Fatal(err)
	}
	if err := checkMaterialLibrary([]string{"--material-library", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("source link accepted")
	}
}
