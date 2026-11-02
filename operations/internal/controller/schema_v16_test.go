package controller

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func schemaFifteenFixture(t *testing.T) schemaTenFixture {
	f := newSchemaTenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, m := range []struct {
		v int
		s string
	}{{11, schemaV11}, {12, schemaV12}, {13, schemaV13}, {14, schemaV14}, {15, schemaV15}} {
		if _, err = db.Exec(m.s); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", m.v, hashToken(m.s), "historical", Now()); err != nil {
			t.Fatal(err)
		}
	}
	env, plan, job := uuid.NewString(), uuid.NewString(), uuid.NewString()
	statements := []struct {
		q    string
		args []any
	}{
		{"INSERT INTO node_environments(id,host_id,name,account,account_uid,root_path,manager_type,management_mode,desired_default_software_id,created_at,updated_at) VALUES(?,?,'node-history','ubuntu',1000,'/home/ubuntu/node','NVM','MANAGED',?,?,?)", []any{env, f.host, f.software, Now(), Now()}},
		{"UPDATE software_instances SET node_environment_id=? WHERE id=?", []any{env, f.software}},
		{"INSERT INTO node_environment_plans(id,actor_id,host_id,environment_id,action,snapshot_json,status,expires_at,created_at) VALUES(?,?,?,?,'SET_DEFAULT',?,'SUBMITTED',?,?)", []any{plan, f.admin, f.host, env, ` { "keep" : "original plan bytes" } `, Now(), Now()}},
		{"INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,result_json,target_ack_pending,trace_id,created_at,updated_at,node_environment_plan_id) VALUES(?,?,'NODE_ENVIRONMENT_CHANGE',?,'node-history','request-hash','UNKNOWN','NODE_CHECK',?,?,1,'trace-node',?,?,?)", []any{job, f.admin, f.host, ` { "keep" : "original input bytes" } `, ` { "keep" : "original receipt bytes" } `, Now(), Now(), plan}},
		{"INSERT INTO operation_locks VALUES(?,?)", []any{"node-environment:" + env, job}},
	}
	for _, s := range statements {
		if _, err = db.Exec(s.q, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func schemaSixteenRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	out := schemaTenRows(t, db)
	for _, table := range []string{"jobs", "operation_locks", "node_environments", "node_environment_plans"} {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		var all [][]any
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			all = append(all, values)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
		out[table] = hashToken(jsonText(all))
	}
	return out
}

func TestSchemaSixteenPreservesOriginalJobsReferencesAndLocks(t *testing.T) {
	f := schemaFifteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	before := schemaSixteenRows(t, db)
	db.Close()
	s, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if after := schemaSixteenRows(t, s.DB); !reflect.DeepEqual(before, after) {
		for k, v := range before {
			if after[k] != v {
				t.Error("historical bytes changed", k)
			}
		}
	}
	var version int
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if version != SchemaVersion {
		t.Fatal(version)
	}
	backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-16-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatal("missing migration backup", backups)
	}
	assertSchemaElevenForeignKeys(t, s.DB)
	if _, err = s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_DOCKER_ACCESS',?,'new-access','new-request','QUEUED','PREFLIGHT','{}','trace',?,?)", uuid.NewString(), f.admin, f.host, Now(), Now()); err != nil {
		t.Fatal("new job kind not accepted", err)
	}
	for name, data := range f.secrets {
		raw, err := os.ReadFile(filepath.Join(f.dir, "secrets", name))
		if err != nil || string(raw) != string(data) {
			t.Fatal("credential bytes changed", name, err)
		}
	}
}

func TestSchemaSixteenFailureRollsBackWithoutDroppingHistory(t *testing.T) {
	f := schemaFifteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	// Make a historical row fail the new table's constraints during INSERT,
	// after CREATE TABLE has executed, to verify transactional rollback.
	if _, err = db.Exec("PRAGMA ignore_check_constraints=ON; UPDATE jobs SET kind='UNSUPPORTED_HISTORY' WHERE id=(SELECT id FROM jobs LIMIT 1); PRAGMA ignore_check_constraints=OFF"); err != nil {
		t.Fatal(err)
	}
	before := schemaSixteenRows(t, db)
	db.Close()
	if s, err := Open(f.dir); err == nil {
		s.Close()
		t.Fatal("invalid historical row migrated")
	}
	db, err = sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !reflect.DeepEqual(before, schemaSixteenRows(t, db)) {
		t.Fatal("failed migration changed history")
	}
	var version, leftovers int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('jobs_v16','operation_locks_v16')").Scan(&leftovers)
	if version != 15 || leftovers != 0 {
		t.Fatal("failed migration not rolled back", version, leftovers)
	}
	backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-16-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatal("failed migration did not retain backup")
	}
}
