package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/controller"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestMaterialCatalogPrintsCompiledRecipesWithoutState(t *testing.T) {
	var output bytes.Buffer
	if err := writeMaterialCatalog(nil, &output); err != nil {
		t.Fatal(err)
	}
	var catalog []serviceinstall.Definition
	if err := json.Unmarshal(output.Bytes(), &catalog); err != nil || len(catalog) != len(serviceinstall.Catalog()) {
		t.Fatal(catalog, err)
	}
	if err := writeMaterialCatalog([]string{"unexpected"}, &output); err == nil {
		t.Fatal("positional argument accepted")
	}
	if err := run([]string{"import-materials", "--state", filepath.Join(t.TempDir(), "state")}); err == nil || !strings.Contains(err.Error(), "MATERIAL_LIBRARY_REQUIRED") {
		t.Fatal(err)
	}
}

func TestMaterialLibraryStartupMissingDefaultAndExplicitFailures(t *testing.T) {
	s, err := controller.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var output bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing")
	if err = importMaterialLibrary(s, "", false, filepath.Join(missing, "streamfusion-ops"), &output); err != nil || output.Len() != 0 {
		t.Fatal("missing default must skip", err, output.String())
	}
	if err = importMaterialLibrary(s, missing, true, "", &output); err == nil || !strings.Contains(err.Error(), "MATERIAL_LIBRARY_DIRECTORY_INVALID") {
		t.Fatal(err)
	}
	output.Reset()
	release := t.TempDir()
	if err = os.Mkdir(filepath.Join(release, "materials"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = importMaterialLibrary(s, "", false, filepath.Join(release, "streamfusion-ops"), &output); err != nil || output.Len() != 0 {
		t.Fatal("source materials without library.json must skip", err)
	}
}

func TestMaterialLibraryImportRequiresAdminAndReportsSummary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "library.json"), []byte(`{"schemaVersion":1,"materials":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	s, err := controller.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = importMaterialLibrary(s, dir, true, "", &output); err == nil || !strings.Contains(err.Error(), "MATERIAL_LIBRARY_ADMIN_REQUIRED") {
		t.Fatal(err)
	}
	if err = s.InitAdmin("admin", "test-password-123"); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = importMaterialLibrary(s, dir, true, "", &output); err != nil {
		t.Fatal(err)
	}
	var summary controller.MaterialLibraryImportResult
	if err = json.Unmarshal(output.Bytes(), &summary); err != nil || summary.Imported != 0 || summary.Reused != 0 || summary.Items == nil {
		t.Fatal(output.String(), err)
	}
	s.Close()
	if err = run([]string{"import-materials", "--state", state, "--material-library", dir}); err != nil {
		t.Fatal(err)
	}
}

func TestMaterialLibraryStartupFindsAdjacentMaterials(t *testing.T) {
	release := t.TempDir()
	materials := filepath.Join(release, "materials")
	if err := os.Mkdir(materials, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(materials, "library.json"), []byte(`{"schemaVersion":1,"materials":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := controller.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitAdmin("admin", "test-password-123"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = importMaterialLibrary(s, "", false, filepath.Join(release, "streamfusion-ops"), &output); err != nil || !strings.Contains(output.String(), `"imported":0`) {
		t.Fatal(output.String(), err)
	}
}
