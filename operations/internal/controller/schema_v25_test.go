package controller

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Old-schema tests remove only the additive application tables before replaying
// their historical migration. Real upgrade coverage below builds schema 24.
const dropApplicationFixtureTables = `DROP TABLE IF EXISTS application_deployments;
DROP TABLE IF EXISTS application_environments;
DROP TABLE IF EXISTS application_modules;
DROP TABLE IF EXISTS application_repositories;
DROP TABLE IF EXISTS application_projects;`

func schemaTwentyFourFixture(t *testing.T) schemaTenFixture {
	t.Helper()
	f := schemaTwentyThreeFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(schemaV24); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schema_migrations VALUES(24,?,?,?)", hashToken(schemaV24), "historical", Now()); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSchemaTwentyFivePreservesHistoricalRowsAndRollsBackAtomically(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "rollback"}[fail], func(t *testing.T) {
			f := schemaTwentyFourFixture(t)
			db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			before := schemaSeventeenHistoricalRows(t, db)
			var plan, job, oldChecksum string
			if err = db.QueryRow("SELECT snapshot_json,job_id FROM git_repository_plans").Scan(&plan, &job); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=24").Scan(&oldChecksum); err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile(filepath.Join(f.dir, "master.key"))
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err = db.Exec("CREATE TRIGGER reject_application_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=25 BEGIN SELECT RAISE(ABORT,'fixture migration failure'); END"); err != nil {
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
			}
			if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, db)) {
				t.Fatal("historical requests, receipts, software or unresolved locks changed")
			}
			var gotPlan, gotJob, gotChecksum string
			if err = db.QueryRow("SELECT snapshot_json,job_id FROM git_repository_plans").Scan(&gotPlan, &gotJob); err != nil || gotPlan != plan || gotJob != job {
				t.Fatal("historical Git plan changed", err)
			}
			if err = db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=24").Scan(&gotChecksum); err != nil || gotChecksum != oldChecksum {
				t.Fatal("historical migration checksum changed", err)
			}
			afterKey, err := os.ReadFile(filepath.Join(f.dir, "master.key"))
			if err != nil || !bytes.Equal(key, afterKey) {
				t.Fatal("credential master key changed", err)
			}
			var version, invalid, applicationTables int
			if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&invalid); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name LIKE 'application_%'").Scan(&applicationTables); err != nil {
				t.Fatal(err)
			}
			wantVersion, wantTables := SchemaVersion, 5
			if fail {
				wantVersion, wantTables = 24, 0
			}
			if version != wantVersion || invalid != 0 || applicationTables != wantTables {
				t.Fatalf("version=%d foreignKeys=%d applicationTables=%d", version, invalid, applicationTables)
			}
			backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-25-*.tar.gz"))
			if len(backups) != 1 {
				t.Fatal("application migration backup missing", backups)
			}
		})
	}
}
