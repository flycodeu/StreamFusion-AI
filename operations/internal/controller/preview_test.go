package controller

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaThreePreviewMigrationPreservesArtifactsAndBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	_ = os.Mkdir(dir, 0700)
	db, err := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for index, source := range []string{schema, schemaV2, schemaV3} {
		if _, err = db.Exec(source); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", index+1, hashToken(source), Version, Now()); err != nil {
			t.Fatal(err)
		}
	}
	id, artifact := uuid.NewString(), uuid.NewString()
	_, err = db.Exec("INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'existing',?)", id, Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO artifacts VALUES(?,'legacy',?,1,'sha256:old',?)", artifact, hashToken("a"), Now())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec("PRAGMA user_version=3")
	_ = db.Close()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var recipe, image string
	if err = store.DB.QueryRow("SELECT recipe,image_id FROM artifacts WHERE id=?", artifact).Scan(&recipe, &image); err != nil || recipe != "java-compose-v1" || image != "sha256:old" || store.controllerID() != id {
		t.Fatal(recipe, image, err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "before-schema-4-*.tar.gz"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	archive, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		header, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if header.Name == "manifest.json" {
			var manifest Manifest
			if json.NewDecoder(tr).Decode(&manifest) != nil || manifest.SchemaVersion != 3 {
				t.Fatal("backup is not schema 3")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("backup manifest missing")
	}
	key := filepath.Join(t.TempDir(), "key")
	_ = os.WriteFile(key, store.Key, 0600)
	dest := filepath.Join(t.TempDir(), "restored")
	if err = Restore(files[0], key, dest); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", filepath.Join(dest, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var version int
	_ = restored.QueryRow("PRAGMA user_version").Scan(&version)
	if version != SchemaVersion {
		t.Fatal("restore did not apply current migration", version)
	}
}
