package controller

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaTwentySixAddsCodesAndPreservesProjectHistory(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "rollback"}[fail], func(t *testing.T) {
			f := schemaTwentyFourFixture(t)
			db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(schemaV25); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("INSERT INTO schema_migrations VALUES(25,?,?,?)", hashToken(schemaV25), "historical", Now()); err != nil {
				t.Fatal(err)
			}
			p := projectDraft28("迁移前项目")
			p.ID, p.Version, p.CreatedAt, p.UpdatedAt, p.Archived = uuid.NewString(), "7", Now(), Now(), true
			if err = normalizeApplicationProject(&p); err != nil {
				t.Fatal(err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec("INSERT INTO application_projects(id,name,description,version,archived,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", p.ID, p.Name, p.Description, p.Version, p.Archived, p.CreatedAt, p.UpdatedAt); err != nil {
				t.Fatal(err)
			}
			if err = saveApplicationChildren(tx, p); err != nil {
				t.Fatal(err)
			}
			otherID := uuid.NewString()
			if _, err = tx.Exec("INSERT INTO application_projects(id,name,created_at,updated_at) VALUES(?,?,?,?)", otherID, "另一个旧项目", Now(), Now()); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			before := schemaSeventeenHistoricalRows(t, db)
			var moduleJSON, deploymentJSON string
			if err = db.QueryRow("SELECT config_json FROM application_modules WHERE id=?", p.Modules[0].ID).Scan(&moduleJSON); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT config_json FROM application_deployments WHERE id=?", p.Environments[0].Deployments[0].ID).Scan(&deploymentJSON); err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile(filepath.Join(f.dir, "master.key"))
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err = db.Exec("CREATE TRIGGER reject_project_code_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=26 BEGIN SELECT RAISE(ABORT,'fixture code migration failure'); END"); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			s, openErr := Open(f.dir)
			if fail {
				if openErr == nil {
					s.Close()
					t.Fatal("failed migration accepted")
				}
				db, err = sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			} else {
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer s.Close()
				db = s.DB
				p.Code = "project-" + p.ID
				loaded, err := readApplicationProject(db, p.ID)
				if err != nil || !reflect.DeepEqual(loaded, p) {
					t.Fatalf("project or child references changed: err=%v got=%+v want=%+v", err, loaded, p)
				}
				var otherCode string
				if err = db.QueryRow("SELECT code FROM application_projects WHERE id=?", otherID).Scan(&otherCode); err != nil || otherCode != "project-"+otherID {
					t.Fatal("legacy project code is not stable and unique", otherCode, err)
				}
				if _, err = db.Exec("UPDATE application_projects SET code=upper(?) WHERE id=?", p.Code, otherID); err == nil {
					t.Fatal("database accepted duplicate codes")
				}
			}
			if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, db)) {
				t.Fatal("historical requests, receipts, software or unresolved locks changed")
			}
			var gotModule, gotDeployment, checksum, name, version string
			var archived bool
			if err = db.QueryRow("SELECT config_json FROM application_modules WHERE id=?", p.Modules[0].ID).Scan(&gotModule); err != nil || gotModule != moduleJSON {
				t.Fatal("module JSON changed", err)
			}
			if err = db.QueryRow("SELECT config_json FROM application_deployments WHERE id=?", p.Environments[0].Deployments[0].ID).Scan(&gotDeployment); err != nil || gotDeployment != deploymentJSON {
				t.Fatal("deployment JSON changed", err)
			}
			if err = db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=25").Scan(&checksum); err != nil || checksum != hashToken(schemaV25) {
				t.Fatal("old migration checksum changed", err)
			}
			if err = db.QueryRow("SELECT name,CAST(version AS TEXT),archived FROM application_projects WHERE id=?", p.ID).Scan(&name, &version, &archived); err != nil || name != p.Name || version != "7" || !archived {
				t.Fatal("project metadata changed", err)
			}
			afterKey, err := os.ReadFile(filepath.Join(f.dir, "master.key"))
			if err != nil || !bytes.Equal(key, afterKey) {
				t.Fatal("credential key changed", err)
			}
			var gotVersion, invalid, codeColumns int
			if err = db.QueryRow("PRAGMA user_version").Scan(&gotVersion); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&invalid); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('application_projects') WHERE name='code'").Scan(&codeColumns); err != nil {
				t.Fatal(err)
			}
			wantVersion, wantColumns := SchemaVersion, 1
			if fail {
				wantVersion, wantColumns = 25, 0
			}
			if gotVersion != wantVersion || invalid != 0 || codeColumns != wantColumns {
				t.Fatalf("version=%d foreignKeys=%d codeColumns=%d", gotVersion, invalid, codeColumns)
			}
			backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-26-*.tar.gz"))
			if len(backups) != 1 {
				t.Fatal("code migration backup missing", backups)
			}
		})
	}
}
