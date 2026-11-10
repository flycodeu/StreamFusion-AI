package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/controller"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestDefaultMaterialLibrarySkipsUnavailableTemplatesButExplicitImportFails(t *testing.T) {
	s, err := controller.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InitAdmin("admin", "test-password-123"); err != nil {
		t.Fatal(err)
	}
	release := t.TempDir()
	dir := filepath.Join(release, "materials")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := controller.ShippedInstallRecipe("custom-ytlabel-native-offline", "4.0")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("archive fixture; import never executes these bytes")
	hash := sha256.Sum256(content)
	entry := serviceinstall.LibraryMaterialFile{RecipeKey: d.Key, RecipeVersion: d.Version, RecipeRevision: d.Revision, RuntimeMode: "NATIVE", Name: "离线演示", File: "offline.tar.gz", SHA256: hex.EncodeToString(hash[:]), ByteSize: int64(len(content))}
	if err := os.WriteFile(filepath.Join(dir, entry.File), content, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := func(entries ...serviceinstall.LibraryMaterialFile) {
		t.Helper()
		raw, err := json.Marshal(serviceinstall.MaterialLibrary{SchemaVersion: 1, Materials: entries})
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "library.json"), raw, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	manifest(entry)
	var output bytes.Buffer
	if err := importMaterialLibrary(s, dir, true, "", &output); err != nil {
		t.Fatal("initial custom import failed", err)
	}
	var original string
	if err := s.DB.QueryRow("SELECT definition_json FROM installation_materials").Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"withdrawn", "edited-publication"} {
		t.Run(state, func(t *testing.T) {
			published := ""
			if state == "edited-publication" {
				d.Revision = "2"
				// An unchanged stock republication can reuse the archive. An
				// administrator's changed command needs its own matching manifest.
				d.Commands.Steps[0].Script += "\nprintf 'administrator changed this command'\n"
				raw, _ := json.Marshal(d)
				published = string(raw)
			}
			if _, err := s.DB.Exec("UPDATE command_templates SET published_json=? WHERE recipe_key=?", published, d.Key); err != nil {
				t.Fatal(err)
			}
			output.Reset()
			if err := importMaterialLibrary(s, dir, true, "", &output); !errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
				t.Fatal("explicit import accepted an unavailable template", err)
			}
			output.Reset()
			if err := importMaterialLibrary(s, "", false, filepath.Join(release, "streamfusion-ops"), &output); err != nil || !strings.Contains(output.String(), "已保留现有材料") {
				t.Fatal("default sidecar prevented startup", err, output.String())
			}
			var count int
			var frozen string
			if err := s.DB.QueryRow("SELECT count(*),definition_json FROM installation_materials").Scan(&count, &frozen); err != nil || count != 1 || frozen != original {
				t.Fatal("unavailable template changed material history", count, err)
			}
		})
	}
	for _, bad := range []string{"path", "hash"} {
		invalid := entry
		if bad == "path" {
			invalid.File = "../unsafe.tar.gz"
		} else {
			invalid.SHA256 = "invalid"
		}
		// A stale first entry must not hide a malformed later entry.
		manifest(entry, invalid)
		if err := importMaterialLibrary(s, "", false, filepath.Join(release, "streamfusion-ops"), &output); err == nil || errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
			t.Fatal("default startup ignored malformed material metadata", bad, err)
		}
	}
	manifest(entry)
	if _, err := s.DB.Exec("UPDATE command_templates SET published_json=? WHERE recipe_key=?", "invalid JSON", d.Key); err != nil {
		t.Fatal(err)
	}
	if err := importMaterialLibrary(s, "", false, filepath.Join(release, "streamfusion-ops"), &output); err == nil || errors.Is(err, serviceinstall.ErrMaterialLibraryTemplateUnavailable) {
		t.Fatal("default startup ignored a damaged template", err)
	}
}
