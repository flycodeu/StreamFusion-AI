package controller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func writeLibraryManifest(t *testing.T, root string, entries []serviceinstall.LibraryMaterialFile) {
	t.Helper()
	raw, err := json.Marshal(serviceinstall.MaterialLibrary{SchemaVersion: 1, Materials: entries})
	if err == nil {
		err = os.WriteFile(filepath.Join(root, "library.json"), raw, 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func invalidLibraryEntry() serviceinstall.LibraryMaterialFile {
	d, _ := serviceinstall.Find("java", "8")
	return serviceinstall.LibraryMaterialFile{RecipeKey: d.Key, RecipeVersion: d.Version, RecipeRevision: d.Revision, RuntimeMode: "NATIVE", Name: "Java 8", File: "not-vendor.tar.gz", SHA256: hashToken("not vendor bytes"), ByteSize: int64(len("not vendor bytes"))}
}

func TestMaterialLibraryRejectsUntrustedBytesWithoutPublishing(t *testing.T) {
	s, _ := setup(t)
	dir := t.TempDir()
	entry := invalidLibraryEntry()
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry})
	if err := os.WriteFile(filepath.Join(dir, entry.File), []byte("not vendor bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "NATIVE_VENDOR_MATERIAL_MISMATCH") {
		t.Fatal("local hash alone trusted a package", err)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM installation_materials").Scan(&count)
	if count != 0 {
		t.Fatal("invalid package published a record")
	}
	files, _ := os.ReadDir(filepath.Join(s.Dir, "installation-materials"))
	if len(files) != 0 {
		t.Fatal("invalid package left a private partial", files)
	}
}

func TestMaterialLibraryManifestBoundsAndExistingQuota(t *testing.T) {
	s, b := setup(t)
	b.login()
	entry := invalidLibraryEntry()
	for _, bad := range []string{
		`{"schemaVersion":2,"materials":[]}`,
		`{"schemaVersion":1,"materials":[],"extra":true}`,
		`{"schemaVersion":1,"materials":[]} {}`,
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "library.json"), []byte(bad), 0600)
		if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); err == nil {
			t.Fatal("invalid manifest accepted", bad)
		}
	}
	dir := t.TempDir()
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry, entry})
	if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); err == nil || err.Error() != "MATERIAL_LIBRARY_DUPLICATE_ENTRY" {
		t.Fatal(err)
	}
	for n := 0; n < 8; n++ {
		materialCreate(t, b, serviceinstall.ArchiveLimit, "library-quota-"+string(rune('a'+n)))
	}
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry})
	if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "INSTALL_MATERIAL_QUOTA") {
		t.Fatal("existing quota bypassed", err)
	}
}

func TestMaterialSourcesAreReadableAndOriginsAreHonest(t *testing.T) {
	s, b := setup(t)
	b.login()
	id := materialCreate(t, b, 10, "material-readable-origin")
	m, err := s.readInstallationMaterial(id)
	if err != nil || m.Origin.Kind != "UPLOADED" || m.Origin.Verification != "PENDING" || m.RecipeName != "Nginx" {
		t.Fatal(m, err)
	}
	if err = os.Remove(s.materialOriginPath(id)); err != nil {
		t.Fatal(err)
	}
	m, _ = s.readInstallationMaterial(id)
	if m.Origin.Kind != "UNKNOWN" {
		t.Fatal("historical origin invented", m.Origin)
	}
	redis, _ := serviceinstall.Find("redis", "7.0.15-1ubuntu0.24.04.5")
	sources := materialSources(redis, "NATIVE")
	if len(sources) != 4 || sources[0].Role != "DEPENDENCY" || sources[3].Role != "MAIN" || sources[3].Publisher != "Ubuntu Archive / Canonical" || sources[3].Packaging != "ISOLATED_EXTRACTION" {
		t.Fatal(sources)
	}
}

// Set this variable to the packer's real output directory to exercise official
// bytes without making ordinary unit tests download vendor binaries.
func TestMaterialLibraryOfficialSidecarImportReuseDeleteAndRepair(t *testing.T) {
	dir := os.Getenv("SF_OPS_TEST_MATERIAL_LIBRARY")
	if dir == "" {
		t.Skip("set SF_OPS_TEST_MATERIAL_LIBRARY to an official packaged sidecar")
	}
	s, b := setup(t)
	b.login()
	first, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || first.Imported == 0 || len(first.Items) == 0 {
		t.Fatal(first, err)
	}
	again, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || again.Imported != 0 || again.Reused != first.Imported {
		t.Fatal(again, err)
	}
	material, err := s.readInstallationMaterial(first.Items[0].ID)
	if err != nil || !material.Available || material.Origin.Kind != "BUNDLED" || material.Origin.Verification != "RECIPE_SHA256" {
		t.Fatal(material, err)
	}
	if err = os.Remove(s.installationMaterialPath(material.ID, false)); err != nil {
		t.Fatal(err)
	}
	repaired, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || repaired.Imported != 1 {
		t.Fatal(repaired, err)
	}
	b.call("DELETE", "/installation-materials/"+material.ID, nil, 200)
	afterDelete, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || afterDelete.Skipped != 1 || afterDelete.Imported != 0 {
		t.Fatal("startup restored an explicitly deleted bundle", afterDelete, err)
	}
	var audits int
	s.DB.QueryRow("SELECT count(*) FROM audit WHERE action='INSTALL_MATERIAL_LIBRARY_IMPORTED' AND actor='LOCAL_STARTUP'").Scan(&audits)
	if audits != first.Imported+1 {
		t.Fatal("missing local import audit", audits)
	}
}
