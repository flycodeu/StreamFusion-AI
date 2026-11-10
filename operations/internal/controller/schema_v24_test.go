package controller

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// Build the old schema from its real migrations, including the Git plan's job
// foreign key. Lowering a current user_version would miss that migration risk.
func schemaTwentyThreeFixture(t *testing.T) schemaTenFixture {
	t.Helper()
	f := schemaSixteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, step := range []struct {
		version int
		sql     string
	}{{17, schemaV17}, {18, schemaV18}, {19, schemaV19}, {20, schemaV20}, {21, schemaV21}, {22, schemaV22}, {23, schemaV23}} {
		if _, err = db.Exec(step.sql); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", step.version, hashToken(step.sql), "historical", Now()); err != nil {
			t.Fatal(err)
		}
	}
	var job string
	if err = db.QueryRow("SELECT id FROM jobs ORDER BY id LIMIT 1").Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO git_repository_plans VALUES(?,?,?,?,' { \"preserve\" : \"git plan bytes\" } ','SUBMITTED',?,?,?)", uuid.NewString(), f.admin, f.host, uuid.NewString(), Now(), Now(), job); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSchemaTwentyFourPreservesJobsGitPlanReferencesAndUnresolvedLocks(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "rollback"}[fail], func(t *testing.T) {
			f := schemaTwentyThreeFixture(t)
			db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			before := schemaSeventeenHistoricalRows(t, db)
			var plan, job string
			if err = db.QueryRow("SELECT snapshot_json,job_id FROM git_repository_plans").Scan(&plan, &job); err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err = db.Exec("CREATE TRIGGER reject_management_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=24 BEGIN SELECT RAISE(ABORT,'fixture migration failure'); END"); err != nil {
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
			} else {
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer s.Close()
				db = s.DB
			}
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				defer db.Close()
			}
			if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, db)) {
				t.Fatal("historical requests, receipts or locks changed")
			}
			var afterPlan, afterJob string
			if err = db.QueryRow("SELECT snapshot_json,job_id FROM git_repository_plans").Scan(&afterPlan, &afterJob); err != nil || afterPlan != plan || afterJob != job {
				t.Fatal("Git plan or job reference changed", err)
			}
			var version, invalid int
			db.QueryRow("PRAGMA user_version").Scan(&version)
			db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&invalid)
			want := 24
			if fail {
				want = 23
			}
			if version != want || invalid != 0 {
				t.Fatal(version, invalid)
			}
			backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-24-*.tar.gz"))
			if len(backups) != 1 {
				t.Fatal("migration backup missing", backups)
			}
		})
	}
}
