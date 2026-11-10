package controller

import (
	"context"
	"encoding/json"
	"errors"
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

func TestMaterialLibraryCustomRecipesRequireCurrentPublicationAndReuseSnapshots(t *testing.T) {
	s, b := setup(t)
	b.login()
	d := testCommandDefinition()
	d.SourceModes = []string{"ARCHIVE"}
	saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
	endpoint := "/command-templates/" + saved["id"].(string)
	dir := t.TempDir()
	content := []byte("custom material bytes; never executed by an import")
	entry := serviceinstall.LibraryMaterialFile{RecipeKey: d.Key, RecipeVersion: d.Version, RecipeRevision: "1", RuntimeMode: "NATIVE", Name: "离线演示", File: "custom.tar.gz", SHA256: hashToken(string(content)), ByteSize: int64(len(content))}
	if err := os.WriteFile(filepath.Join(dir, entry.File), content, 0600); err != nil {
		t.Fatal(err)
	}
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry})
	if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); !errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
		t.Fatal("unpublished draft accepted", err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	injected := strings.Replace(string(manifest), `"recipeKey":`, `"definition":{"commands":{"steps":[{"script":"arbitrary script"}]}},"recipeKey":`, 1)
	if err := os.WriteFile(filepath.Join(dir, "library.json"), []byte(injected), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); err == nil || err.Error() != "MATERIAL_LIBRARY_MANIFEST_INVALID" {
		t.Fatal("manifest supplied its own executable definition", err)
	}
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry})
	published := dataOf(t, b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200))
	first, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || first.Imported != 1 {
		t.Fatal("published custom recipe was not imported", first, err)
	}
	frozen, err := s.readInstallationMaterial(first.Items[0].ID)
	if err != nil || frozen.Definition.Commands == nil || !frozen.Available || frozen.Origin.Kind != "BUNDLED" {
		t.Fatal("custom snapshot lost its published commands", frozen, err)
	}
	again, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || again.Reused != 1 || again.Imported != 0 || again.Items[0].ID != frozen.ID {
		t.Fatal("custom material was not reused", again, err)
	}
	for _, mutate := range []func(*serviceinstall.LibraryMaterialFile){
		func(m *serviceinstall.LibraryMaterialFile) { m.RecipeRevision = "2" },
		func(m *serviceinstall.LibraryMaterialFile) { m.RuntimeMode = "DOCKER" },
		func(m *serviceinstall.LibraryMaterialFile) { m.File = "../outside.tar.gz" },
	} {
		invalid := entry
		mutate(&invalid)
		writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{invalid})
		if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); err == nil {
			t.Fatal("invalid custom material accepted", invalid)
		}
	}
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry})
	withdrawn := dataOf(t, b.call("DELETE", endpoint, map[string]any{"expectedVersion": published["version"]}, 200))
	if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); !errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
		t.Fatal("withdrawn custom recipe accepted", err)
	}
	b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": withdrawn["version"]}, 200)
	if _, err := s.ImportInstallationMaterialLibrary(context.Background(), dir); !errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
		t.Fatal("old revision accepted after republication", err)
	}
	entry.RecipeRevision = "2"
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry})
	updated, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || updated.Imported != 1 || updated.Items[0].ID == frozen.ID {
		t.Fatal("new publication did not receive its own snapshot", updated, err)
	}
	old, err := s.readInstallationMaterial(frozen.ID)
	if err != nil || jsonText(old.Definition) != jsonText(frozen.Definition) || old.SHA256 != frozen.SHA256 || old.CreatedAt != frozen.CreatedAt {
		t.Fatal("existing material history changed", err)
	}
}

func TestMaterialLibraryBuiltinRecipeRemainsAvailableAfterWithdrawal(t *testing.T) {
	s, _ := setup(t)
	entry := invalidLibraryEntry()
	if _, err := s.DB.Exec("UPDATE command_templates SET published_json='' WHERE recipe_key=?", commandTemplateKey(entry.RecipeKey, entry.RecipeVersion)); err != nil {
		t.Fatal(err)
	}
	actual, err := entry.Definition(s.libraryInstallRecipe)
	expected, _ := serviceinstall.Find(entry.RecipeKey, entry.RecipeVersion)
	if err != nil || serviceinstall.Digest(actual) != serviceinstall.Digest(expected) {
		t.Fatal("withdrawal changed the compiled vendor material definition", err)
	}
}
