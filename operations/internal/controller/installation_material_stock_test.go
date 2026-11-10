package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func stockYtlabelLibraryEntry(t *testing.T) serviceinstall.LibraryMaterialFile {
	t.Helper()
	d, err := ShippedInstallRecipe("custom-ytlabel-py312-native-offline", "4.0-python312")
	if err != nil {
		t.Fatal(err)
	}
	content := "material identity fixture, never executed"
	return serviceinstall.LibraryMaterialFile{RecipeKey: d.Key, RecipeVersion: d.Version, RecipeRevision: d.Revision, RuntimeMode: "NATIVE", Name: d.Name, File: "ytlabel.tar.gz", SHA256: hashToken(content), ByteSize: int64(len(content))}
}

func TestMaterialLibraryStockUpgradeKeepsOldSnapshotAndMatchesNewPublication(t *testing.T) {
	dir := t.TempDir()
	entry := stockYtlabelLibraryEntry(t)
	if err := os.WriteFile(filepath.Join(dir, entry.File), []byte("material identity fixture, never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	checkStockMaterialUpgrade(t, dir, entry)
}

func checkStockMaterialUpgrade(t *testing.T, dir string, entry serviceinstall.LibraryMaterialFile) {
	t.Helper()
	s, _ := setup(t)
	stock, err := entry.Definition(ShippedInstallRecipe)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.libraryMaterialDefinition(entry)
	if err != nil || !sameMaterialDefinition(fresh, stock) || fresh.Revision != "1" {
		t.Fatal("fresh state cannot use its shipped manifest", err)
	}
	previous := previousWebDefaultDefinition(stock)
	if _, err = s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=1 WHERE recipe_key=?", jsonText(previous), jsonText(previous), stock.Key); err != nil {
		t.Fatal(err)
	}
	writeLibraryManifest(t, dir, []serviceinstall.LibraryMaterialFile{entry})
	first, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || first.Imported != 1 {
		t.Fatal("original stock material was not imported", first, err)
	}
	old, err := s.readInstallationMaterial(first.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.seedSoftwareManagementPresets(); err != nil {
		t.Fatal(err)
	}
	current, err := s.findInstallRecipe(stock.Key, stock.Version)
	if err != nil || current.Revision != "2" || current.DefaultBindAddress != "0.0.0.0" {
		t.Fatal("stock template did not receive the actual new publication", err)
	}
	updated, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || updated.Imported != 1 || updated.Items[0].ID == old.ID {
		t.Fatal("new stock publication did not receive a separate material", updated, err)
	}
	material, err := s.readInstallationMaterial(updated.Items[0].ID)
	if err != nil || !material.Available || material.RecipeRevision != "2" || !sameMaterialDefinition(material.Definition, current) || material.SHA256 != entry.SHA256 || material.ByteSize != entry.ByteSize {
		t.Fatal("new material cannot be selected for the current recipe", err)
	}
	unchanged, err := s.readInstallationMaterial(old.ID)
	if err != nil || jsonText(unchanged) != jsonText(old) {
		t.Fatal("historical material was rewritten", err)
	}
	again, err := s.ImportInstallationMaterialLibrary(context.Background(), dir)
	if err != nil || again.Reused != 1 || again.Imported != 0 || again.Items[0].ID != material.ID {
		t.Fatal("new stock material was not reused", again, err)
	}
	t.Logf("old=%s revision=%s new=%s revision=%s sha256=%s bytes=%d", old.ID, old.RecipeRevision, material.ID, material.RecipeRevision, material.SHA256, material.ByteSize)
}

func TestMaterialLibraryStockFallbackRejectsEditsWithdrawalAndUnknownRevision(t *testing.T) {
	s, _ := setup(t)
	entry := stockYtlabelLibraryEntry(t)
	stock, _ := entry.Definition(ShippedInstallRecipe)
	for _, test := range []struct {
		name   string
		change func(*serviceinstall.Definition)
	}{
		{"script", func(d *serviceinstall.Definition) { d.Commands.Steps[0].Script += "\nprintf edited\n" }},
		{"bind", func(d *serviceinstall.Definition) { d.DefaultBindAddress = "192.0.2.8" }},
		{"management", func(d *serviceinstall.Definition) { d.Management.Operations[0].Steps[0].TimeoutSeconds++ }},
		{"withdrawn", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var current serviceinstall.Definition
			if err := json.Unmarshal([]byte(jsonText(stock)), &current); err != nil {
				t.Fatal(err)
			}
			current.Revision = "2"
			published := ""
			if test.change != nil {
				test.change(&current)
				published = jsonText(current)
			}
			if _, err := s.DB.Exec("UPDATE command_templates SET published_json=?,revision=2 WHERE recipe_key=?", published, stock.Key); err != nil {
				t.Fatal(err)
			}
			if _, err := s.libraryMaterialDefinition(entry); !errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
				t.Fatal("stock fallback accepted a changed or withdrawn publication", err)
			}
		})
	}
	stock.Revision = "2"
	if _, err := s.DB.Exec("UPDATE command_templates SET published_json=?,revision=2 WHERE recipe_key=?", jsonText(stock), stock.Key); err != nil {
		t.Fatal(err)
	}
	entry.RecipeRevision = "99"
	if _, err := s.libraryMaterialDefinition(entry); !errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
		t.Fatal("stock fallback accepted an unknown manifest revision", err)
	}
}

func TestMaterialLibraryStockUpgradeUsesRealShippedArchive(t *testing.T) {
	source := os.Getenv("SF_OPS_TEST_MATERIAL_LIBRARY")
	if source == "" {
		t.Skip("set SF_OPS_TEST_MATERIAL_LIBRARY to verify shipped archive bytes")
	}
	raw, err := os.ReadFile(filepath.Join(source, "library.json"))
	var library serviceinstall.MaterialLibrary
	if err != nil || json.Unmarshal(raw, &library) != nil {
		t.Fatal("cannot read real material library", err)
	}
	for _, entry := range library.Materials {
		if entry.RecipeKey != "custom-ytlabel-py312-native-offline" {
			continue
		}
		dir := t.TempDir()
		if err = serviceinstall.CopyLibraryMaterial(context.Background(), source, filepath.Join(dir, entry.File), entry, ShippedInstallRecipe); err != nil {
			t.Fatal(err)
		}
		checkStockMaterialUpgrade(t, dir, entry)
		return
	}
	t.Fatal("real library has no shipped ytlabel archive")
}
