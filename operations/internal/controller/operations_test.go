package controller

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestSchemaOneUpgradeKeepsIdentityAndRestorableBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	os.Mkdir(dir, 0700)
	db, e := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(schema); e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	_, e = db.Exec("INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'original',?)", id, Now())
	if e == nil {
		_, e = db.Exec("INSERT INTO schema_migrations VALUES(1,?,'0.1.0',?)", hashToken(schema), Now())
	}
	if e == nil {
		_, e = db.Exec("PRAGMA user_version=1")
	}
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if s.controllerID() != id {
		t.Fatal("identity changed")
	}
	var checksum string
	if e = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum); e != nil || checksum != hashToken(schema) {
		t.Fatal(e)
	}
	files, e := filepath.Glob(filepath.Join(dir, "before-schema-2-*.tar.gz"))
	if e != nil || len(files) != 1 {
		t.Fatal(files, e)
	}
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, s.Key, 0600)
	s.Close()
	dest := filepath.Join(t.TempDir(), "restored")
	if e = Restore(files[0], key, dest); e != nil {
		t.Fatal(e)
	}
	restored, e := Open(dest)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if restored.controllerID() != id {
		t.Fatal("backup identity mismatch")
	}
}
func TestTargetsDraftValidationAndImmutableBindings(t *testing.T) {
	s, b := setup(t)
	b.login()
	tgt := dataOf(t, b.call("POST", "/hosts", map[string]any{"kind": "LOCAL", "name": "local", "installRoot": "/opt/test-target"}, 201))["id"].(string)
	b.call("POST", "/hosts", map[string]any{"kind": "LOCAL", "name": "second", "installRoot": "/opt/second-target"}, 409)
	config := deploy.Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/actuator/health", HealthSeconds: 10, DataCompatibility: "v1", BindingIDs: []string{}}
	in := map[string]any{"name": "java", "targetId": tgt, "config": config}
	id := dataOf(t, b.call("POST", "/instances", in, 200))["id"].(string)
	i, e := s.readInstance(id)
	if e != nil || i.Current != "" || i.Generation != "0" {
		t.Fatal(i, e)
	}
	in["version"] = "1"
	config.Profile = "test"
	in["config"] = config
	b.call("PATCH", "/instances/"+id, in, 200)
	b.call("PATCH", "/instances/"+id, in, 409)
	i, _ = s.readInstance(id)
	if i.Config.Profile != "test" || i.Generation != "0" {
		t.Fatal("draft changed applied state", i)
	}
	b.call("POST", "/bindings", deploy.Binding{Kind: "MYSQL", Host: "localhost", Port: 3306, Database: "test", Username: "test"}, 201)
	var fk int
	if e = s.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk); e != nil || fk != 1 {
		t.Fatal("foreign keys disabled")
	}
}
func TestResultCommitOnlyOnceAndProtectsNewDraft(t *testing.T) {
	s, b := setup(t)
	b.login()
	tid := dataOf(t, b.call("POST", "/hosts", map[string]any{"kind": "LOCAL", "name": "local", "installRoot": "/opt/test-target"}, 201))["id"].(string)
	conf := deploy.Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/actuator/health", HealthSeconds: 10, DataCompatibility: "v1"}
	iid := dataOf(t, b.call("POST", "/instances", map[string]any{"name": "java", "targetId": tid, "config": conf}, 200))["id"].(string)
	aid, rid, pid, jid := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	rel := deploy.Release{ID: rid, InstanceID: iid, ArtifactID: aid, ImageID: "sha256:abc", Config: conf}
	p := plan{ID: pid, ExpectedGeneration: "0", Candidate: rel}
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: s.controllerID(), TargetID: tid, JobID: jid, InputHash: hashToken("input"), Candidate: &rel}
	raw, _ := json.Marshal(req)
	_, e := s.DB.Exec("INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?, 'a', ?,1,?,?)", aid, hashToken("archive"), rel.ImageID, Now())
	if e == nil {
		_, e = s.DB.Exec("INSERT INTO releases VALUES(?,?,?,?,?)", rid, iid, aid, jsonText(rel), Now())
	}
	if e == nil {
		_, e = s.DB.Exec("INSERT INTO operation_plans VALUES(?,?,?,?,?)", pid, iid, jsonText(p), Now(), Now())
	}
	if e == nil {
		_, e = s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,instance_id,plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'PUBLISH',?,?,?,?,?,?,'RUNNING','TARGET_EXECUTION',?,'trace',?,?)", jid, b.currentAdmin(), tid, iid, pid, rid, "idem-test", hashToken("req"), string(raw), Now(), Now())
	}
	if e != nil {
		t.Fatal(e)
	}
	updated := conf
	updated.Profile = "draft-new"
	b.call("PATCH", "/instances/"+iid, map[string]any{"name": "java", "targetId": tid, "version": "1", "config": updated}, 200)
	result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: tid, JobID: jid, InputHash: req.InputHash, Status: "SUCCEEDED", ReleaseID: rid, ImageID: rel.ImageID, ConfigHash: deploy.Digest(conf), Health: "APPLICATION_READY"}
	if e = b.h.commitResult(jid, req, result); e != nil {
		t.Fatal(e)
	}
	if e = b.h.commitResult(jid, req, result); e != nil {
		t.Fatal(e)
	}
	i, e := s.readInstance(iid)
	if e != nil || i.Generation != "1" || i.Config.Profile != "draft-new" || i.Current != rid {
		t.Fatal(i, e)
	}
	if dataOf(t, b.call("GET", "/jobs/"+jid, nil, 200))["status"] != "SUCCEEDED" {
		t.Fatal("job detail differs from committed result")
	}
	b.call("GET", "/plans/"+pid, nil, 200)
	b.call("GET", "/hosts/"+tid, nil, 200)
	b.call("GET", "/instances/"+iid, nil, 200)
	w := b.call("POST", "/plans/"+pid+"/submit", map[string]string{"idempotencyKey": "different-submit-key"}, 409)
	var envelope Envelope
	json.Unmarshal(w.Body.Bytes(), &envelope)
	if envelope.Code != "PLAN_ALREADY_ACCEPTED" {
		t.Fatal("unexpected duplicate plan error", envelope.Code)
	}
	b.call("GET", "/jobs/"+uuid.NewString(), nil, 404)
}
func (b *browser) currentAdmin() string {
	var id string
	b.h.store.DB.QueryRow("SELECT id FROM admins LIMIT 1").Scan(&id)
	return id
}
