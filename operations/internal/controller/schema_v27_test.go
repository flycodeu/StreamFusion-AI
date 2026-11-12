package controller

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaTwentySevenPreservesPendingJobsAndManagementReferences(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "rollback"}[fail], func(t *testing.T) {
			f := schemaTwentyFourFixture(t)
			db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range []struct {
				version int
				sql     string
			}{{25, schemaV25}, {26, schemaV26}} {
				if _, err = db.Exec(step.sql); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", step.version, hashToken(step.sql), "historical", Now()); err != nil {
					t.Fatal(err)
				}
			}
			var software, job string
			if err = db.QueryRow("SELECT id FROM software_instances ORDER BY id LIMIT 1").Scan(&software); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT id FROM jobs ORDER BY id LIMIT 1").Scan(&job); err != nil {
				t.Fatal(err)
			}
			template := uuid.NewString()
			if _, err = db.Exec("INSERT INTO command_templates(id,recipe_key,draft_json,updated_at) VALUES(?,'migration-policy','{}',?)", template, Now()); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("INSERT INTO software_management_state(software_id,template_id,template_revision,definition_json,last_job_id,updated_at) VALUES(?,?,1,' { \"preserve\" : true } ',?,?)", software, template, job, Now()); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("INSERT INTO software_management_credentials SELECT ?,id,revision FROM credential_refs WHERE id=?", software, f.credential); err != nil {
				t.Fatal(err)
			}
			var credentialReferences int
			if err = db.QueryRow("SELECT count(*) FROM software_management_credentials WHERE software_id=?", software).Scan(&credentialReferences); err != nil || credentialReferences == 0 {
				t.Fatal("credential fixture missing", err)
			}
			before := schemaSeventeenHistoricalRows(t, db)
			var managementBefore, gitBefore string
			if err = db.QueryRow("SELECT software_id||template_id||definition_json||last_job_id FROM software_management_state WHERE software_id=?", software).Scan(&managementBefore); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT snapshot_json||job_id FROM git_repository_plans").Scan(&gitBefore); err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err = db.Exec("CREATE TRIGGER reject_runtime_policy_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=27 BEGIN SELECT RAISE(ABORT,'fixture failure'); END"); err != nil {
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
				t.Fatal("historical jobs, receipts, identities or locks changed")
			}
			var managementAfter, gitAfter string
			if err = db.QueryRow("SELECT software_id||template_id||definition_json||last_job_id FROM software_management_state WHERE software_id=?", software).Scan(&managementAfter); err != nil || managementAfter != managementBefore {
				t.Fatal("management reference changed", err)
			}
			if err = db.QueryRow("SELECT snapshot_json||job_id FROM git_repository_plans").Scan(&gitAfter); err != nil || gitAfter != gitBefore {
				t.Fatal("Git plan changed", err)
			}
			var keptReferences int
			if err = db.QueryRow("SELECT count(*) FROM software_management_credentials WHERE software_id=? AND credential_id=?", software, f.credential).Scan(&keptReferences); err != nil || keptReferences != credentialReferences {
				t.Fatal("management credential references changed", err)
			}
			var version, invalid int
			if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&invalid); err != nil {
				t.Fatal(err)
			}
			want := 27
			if fail {
				want = 26
			}
			if version != want || invalid != 0 {
				t.Fatal(version, invalid)
			}
			backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-27-*.tar.gz"))
			if len(backups) != 1 {
				t.Fatal("migration backup missing", backups)
			}
		})
	}
}
