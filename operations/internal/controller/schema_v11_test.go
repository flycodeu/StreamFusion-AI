package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Build the historical schema directly. Opening a current database and lowering
// user_version would leave new columns behind and would not test an upgrade.
type schemaTenFixture struct {
	dir, controller, admin, host, credential, software string
	key                                                []byte
	secrets                                            map[string][]byte
	rows                                               map[string]string
}

var schemaTenQueries = map[string]string{
	"controller":  "SELECT * FROM controller_state ORDER BY id",
	"admins":      "SELECT * FROM admins ORDER BY id",
	"targets":     "SELECT * FROM targets ORDER BY id",
	"credentials": "SELECT * FROM credential_refs ORDER BY id,revision",
	"materials":   "SELECT id,actor_id,name,definition_json,sha256,byte_size,status,image_id,platform,error_code,idempotency_key,request_hash,created_at,updated_at FROM installation_materials ORDER BY id",
	"plans":       "SELECT * FROM software_install_plans ORDER BY id",
	"planSecrets": "SELECT * FROM software_install_credentials ORDER BY plan_id,credential_id,credential_revision",
	"software":    "SELECT id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,desired_state,actual_state,observation_json,observed_at,identity_hash,binding_revision,version,last_job_id,error_code,data_ownership,created_at,updated_at,attempt_json,observation_revision,observation_identity,install_plan_id,installation_json,installation_status FROM software_instances ORDER BY id",
	"jobs":        "SELECT id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at,software_plan_id FROM jobs ORDER BY id",
	"locks":       "SELECT * FROM operation_locks ORDER BY resource_key",
	"migrations":  "SELECT * FROM schema_migrations WHERE version<=10 ORDER BY version",
}

func schemaTenRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	result := make(map[string]string, len(schemaTenQueries))
	for name, query := range schemaTenQueries {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var values [][]any
		for rows.Next() {
			value := make([]any, len(columns))
			addresses := make([]any, len(columns))
			for i := range value {
				addresses[i] = &value[i]
			}
			if err = rows.Scan(addresses...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			values = append(values, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = string(encoded)
	}
	return result
}

func newSchemaTenFixture(t *testing.T) schemaTenFixture {
	t.Helper()
	f := schemaTenFixture{
		dir: t.TempDir(), controller: uuid.NewString(), admin: uuid.NewString(),
		host: uuid.NewString(), credential: uuid.NewString(), software: uuid.NewString(),
		key: make([]byte, 32), secrets: map[string][]byte{},
	}
	if _, err := rand.Read(f.key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "master.key"), f.key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.dir, "secrets"), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for index, source := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV8, schemaV9, schemaV10} {
		if _, err = db.Exec(source); err != nil {
			t.Fatalf("historical schema %d: %v", index+1, err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", index+1, hashToken(source), "historical-release", Now()); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	created := "2026-10-20T11:10:09.000000000Z"
	insert("INSERT INTO controller_state(id,controller_id,name,version,created_at) VALUES(1,?,'historical controller',8,?)", f.controller, created)
	insert("INSERT INTO admins(id,username,password_hash,session_generation,created_at) VALUES(?,'old-admin',?,4,?)", f.admin, []byte("historical-password-hash"), created)
	for revision := 1; revision <= 2; revision++ {
		name := fmt.Sprintf("%s-%d.enc", f.credential, revision)
		sealed, err := seal(f.key, f.credential, revision, []byte(fmt.Sprintf("test-only-install-secret-%d", revision)))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(f.dir, "secrets", name), sealed, 0600); err != nil {
			t.Fatal(err)
		}
		f.secrets[name] = sealed
		var revoked any
		if revision == 1 {
			revoked = "2026-10-20T12:00:00.000000000Z"
		}
		insert("INSERT INTO credential_refs(id,revision,name,kind,material,created_at,revoked_at) VALUES(?,?,'installation-account','PASSWORD',?,?,?)", f.credential, revision, name, created, revoked)
	}
	insert("INSERT INTO targets(id,name,install_root,kind,address,ssh_user,host_fingerprint,credential_id,credential_revision,version,created_at) VALUES(?,'historical host','/opt/old-host','SSH','192.0.2.50','ops','SHA256:old',?,2,7,?)", f.host, f.credential, created)
	material, completedPlan, unknownPlan := uuid.NewString(), uuid.NewString(), uuid.NewString()
	insert("INSERT INTO installation_materials(id,actor_id,name,definition_json,sha256,byte_size,status,image_id,platform,idempotency_key,request_hash,created_at,updated_at) VALUES(?,?,'redis-offline.tar',?, ?,12345,'AVAILABLE','sha256:old-image','linux/amd64','material-key','material-request',?,?)", material, f.admin, `{"key":"redis","version":"7.4","source":"OFFLINE_ARCHIVE"}`, strings.Repeat("a", 64), created, created)
	for _, plan := range []string{completedPlan, unknownPlan} {
		insert("INSERT INTO software_install_plans(id,actor_id,target_id,material_id,snapshot_json,status,expires_at,created_at) VALUES(?,?,?,?,?,'SUBMITTED','2026-10-20T11:15:09.000000000Z',?)", plan, f.admin, f.host, material, `{"hostRevision":7,"source":"OFFLINE_ARCHIVE","credentialRevision":1}`, created)
		insert("INSERT INTO software_install_credentials(plan_id,credential_id,credential_revision) VALUES(?,?,1)", plan, f.credential)
	}
	completed, unknown, failed := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, job := range []struct {
		id, status, phase, plan, code string
		ack                           int
	}{
		{completed, "SUCCEEDED", "COMPLETE", completedPlan, "", 0},
		{unknown, "UNKNOWN", "START_CONTAINER", unknownPlan, "EXECUTOR_TIMEOUT", 1},
	} {
		insert("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at,software_plan_id) VALUES(?,?,'SOFTWARE_INSTALL',?,?,?,?,?,?,?,?,?,'historical-trace',?,?,?)", job.id, f.admin, f.host, "install-"+job.id, "input-"+job.id, job.status, job.phase, `{"action":"SOFTWARE_INSTALL","inputHash":"frozen-input"}`, `{"resultHash":"historical-result","software":{"locator":"old-container"}}`, job.code, job.ack, created, created, job.plan)
	}
	insert("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_STOP',?,'failed-key','failed-request','FAILED','NOT_STARTED','{}',?,'NOT_STARTED','failed-trace',?,?)", failed, f.admin, f.host, `{"status":"FAILED","reason":"NOT_STARTED"}`, created, created)
	insert("INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,desired_state,actual_state,observation_json,observed_at,identity_hash,binding_revision,version,last_job_id,data_ownership,created_at,updated_at,attempt_json,observation_revision,observation_identity,install_plan_id,installation_json,installation_status) VALUES(?,'historical redis',?,'REDIS','7.4','DOCKER','old-container','MANAGED','RUNNING','RUNNING',?,?,'old-identity',4,7,?,'OPS',?,?,?,9,'old-observation',?,?,'READY')", f.software, f.host, `{"state":"RUNNING","version":"7.4","ports":[16379]}`, created, completed, created, created, `{"code":"SSH_UNAVAILABLE","observedAt":"2026-10-20T12:01:00Z"}`, completedPlan, `{"recipeKey":"redis","port":16379,"dataPath":"/opt/old-host/redis/data"}`)
	insert("INSERT INTO operation_locks(resource_key,job_id) VALUES(?,?)", "target:"+f.host, unknown)
	insert("INSERT INTO operation_locks(resource_key,job_id) VALUES(?,?)", "software:"+f.software, unknown)
	assertSchemaElevenForeignKeys(t, db)
	f.rows = schemaTenRows(t, db)
	return f
}

func assertSchemaElevenForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration left broken foreign keys")
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaTenUpgradePreservesInstallationsReceiptsLocksAndCredentials(t *testing.T) {
	f := newSchemaTenFixture(t)
	s, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	afterRows := schemaTenRows(t, s.DB)
	for name, before := range f.rows {
		if after := afterRows[name]; after != before {
			t.Errorf("historical %s changed during migration", name)
		}
	}
	var version, foreignKeys, runtimeDefaults, configurationDefaults, checksumCount int
	for query, target := range map[string]*int{
		"PRAGMA user_version": &version,
		"PRAGMA foreign_keys": &foreignKeys,
		"SELECT count(*) FROM installation_materials WHERE runtime_mode='DOCKER'":                                   &runtimeDefaults,
		"SELECT count(*) FROM software_instances WHERE configuration_job_id='' AND installation_evidence_json='{}'": &configurationDefaults,
		"SELECT count(*) FROM schema_migrations":                                                                    &checksumCount,
	} {
		if err = s.DB.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if version != SchemaVersion || foreignKeys != 1 || runtimeDefaults != 1 || configurationDefaults != 1 || checksumCount != SchemaVersion || s.controllerID() != f.controller {
		t.Fatalf("wrong migrated schema state: version=%d foreignKeys=%d runtime=%d configuration=%d checksums=%d", version, foreignKeys, runtimeDefaults, configurationDefaults, checksumCount)
	}
	var checksum string
	if err = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=11").Scan(&checksum); err != nil || checksum != hashToken(schemaV11) {
		t.Fatal("schema11 checksum missing or incorrect", err)
	}
	key, err := os.ReadFile(filepath.Join(f.dir, "master.key"))
	if err != nil || !bytes.Equal(key, f.key) || !bytes.Equal(s.Key, f.key) {
		t.Fatal("migration replaced the existing master key", err)
	}
	for name, before := range f.secrets {
		after, err := os.ReadFile(s.credentialFile(name))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("migration modified encrypted credential material", err)
		}
	}
	if err = s.verifySecrets(); err != nil {
		t.Fatal("historical credential revisions no longer decrypt", err)
	}
	assertSchemaElevenForeignKeys(t, s.DB)
	assertSchemaElevenNewRelations(t, s, f)
	backups, err := filepath.Glob(filepath.Join(f.dir, "before-schema-*.tar.gz"))
	if err != nil || len(backups) != 4 || !strings.HasPrefix(filepath.Base(backups[0]), "before-schema-11-") || !strings.HasPrefix(filepath.Base(backups[1]), "before-schema-12-") || !strings.HasPrefix(filepath.Base(backups[2]), "before-schema-13-") || !strings.HasPrefix(filepath.Base(backups[3]), "before-schema-14-") {
		t.Fatal("upgrade must preserve historical backups and add the current migration backup", backups, err)
	}
	assertSchemaTenBackup(t, backups[0], f)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.dir)
	if err != nil {
		t.Fatal("upgraded database did not reopen", err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(f.rows, schemaTenRows(t, reopened.DB)) {
		t.Fatal("reopening changed historical rows")
	}
	afterBackups, err := filepath.Glob(filepath.Join(f.dir, "before-schema-*.tar.gz"))
	if err != nil || !reflect.DeepEqual(backups, afterBackups) {
		t.Fatal("opening current schema unexpectedly reran migration", err)
	}
}

func assertSchemaElevenNewRelations(t *testing.T, s *Store, f schemaTenFixture) {
	t.Helper()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	plan := uuid.NewString()
	if _, err = tx.Exec("INSERT INTO software_configuration_plans(id,actor_id,target_id,software_id,snapshot_json,expires_at,created_at) VALUES(?,?,?,?,'{}',?,?)", plan, f.admin, f.host, f.software, Now(), Now()); err != nil {
		t.Fatal("new configuration plan is unavailable", err)
	}
	if _, err = tx.Exec("INSERT INTO software_configuration_credentials(plan_id,credential_id,credential_revision) VALUES(?,?,2)", plan, f.credential); err != nil {
		t.Fatal("new configuration plan lost existing credential revision", err)
	}
	if _, err = tx.Exec("INSERT INTO software_configuration_credentials(plan_id,credential_id,credential_revision) VALUES(?,?,99)", plan, f.credential); err == nil {
		t.Fatal("new credential reference accepted a nonexistent revision")
	}
	if _, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at,configuration_plan_id) VALUES(?,?,'SOFTWARE_CONFIGURE',?,'new-configuration-key','new-hash','QUEUED','PREFLIGHT','{}','new-trace',?,?,?)", uuid.NewString(), f.admin, f.host, Now(), Now(), plan); err != nil {
		t.Fatal("migrated jobs table rejected configuration jobs", err)
	}
	if _, err = tx.Exec("INSERT INTO operation_locks(resource_key,job_id) VALUES('orphan-lock','missing-job')"); err == nil {
		t.Fatal("migrated operation locks lost their job foreign key")
	}
}

func assertSchemaTenBackup(t *testing.T, path string, fixture schemaTenFixture) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	contents := map[string][]byte{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 4<<20 {
			t.Fatal("unexpected backup entry")
		}
		if _, exists := contents[header.Name]; exists {
			t.Fatal("duplicate backup entry")
		}
		data, err := io.ReadAll(io.LimitReader(tr, 4<<20))
		if err != nil || int64(len(data)) != header.Size {
			t.Fatal("damaged backup entry", err)
		}
		contents[header.Name] = data
	}
	var manifest Manifest
	if err = json.Unmarshal(contents["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 10 || manifest.ControllerID != fixture.controller || manifest.KeyFingerprint != hashToken(string(fixture.key)) || manifest.CreatedAt == "" || len(manifest.Files) != len(fixture.secrets)+1 || len(contents) != len(manifest.Files)+1 {
		t.Fatal("backup does not describe the pre-upgrade database")
	}
	for name, digest := range manifest.Files {
		data, exists := contents[name]
		if !exists || hashToken(string(data)) != digest {
			t.Fatal("backup file failed manifest digest verification")
		}
		if name != "controller.sqlite" && !bytes.Equal(data, fixture.secrets[strings.TrimPrefix(name, "secrets/")]) {
			t.Fatal("backup contains unexpected or modified credential material")
		}
	}
	if _, exists := contents["master.key"]; exists {
		t.Fatal("backup must not contain the master key")
	}
	snapshot := filepath.Join(t.TempDir(), "schema10.sqlite")
	if err = os.WriteFile(snapshot, contents["controller.sqlite"], 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, newMigrations int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 10 {
		t.Fatal("backup captured post-upgrade schema", version, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version>10").Scan(&newMigrations); err != nil || newMigrations != 0 {
		t.Fatal("backup contains later migration receipts", err)
	}
	if !reflect.DeepEqual(fixture.rows, schemaTenRows(t, db)) {
		t.Fatal("pre-upgrade backup did not preserve historical rows")
	}
	assertSchemaElevenForeignKeys(t, db)
}

func TestSchemaTenUpgradeRejectsHistoricalChecksumMismatch(t *testing.T) {
	f := newSchemaTenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE schema_migrations SET checksum='tampered' WHERE version=10"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(f.dir); err == nil {
		s.Close()
		t.Fatal("upgrade accepted a tampered historical migration")
	} else if !strings.HasPrefix(err.Error(), "SCHEMA_MISMATCH:") {
		t.Fatal("unexpected checksum failure", err)
	}
	db, err = sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 10 {
		t.Fatal("checksum failure advanced the schema", version, err)
	}
	backups, err := filepath.Glob(filepath.Join(f.dir, "before-schema-11-*.tar.gz"))
	if err != nil || len(backups) != 0 {
		t.Fatal("checksum rejection should precede schema11 backup and mutation", err)
	}
}
