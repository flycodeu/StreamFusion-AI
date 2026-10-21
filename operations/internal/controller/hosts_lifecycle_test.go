package controller

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestHostPasswordAuthenticationAndLatestRevision(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "host password", "kind": "PASSWORD", "secret": "test-ssh-password"}, 201))
	f.CredentialID = credential["id"].(string)
	b.call("POST", "/credentials/"+f.CredentialID+"/revisions", map[string]string{"name": "host password", "kind": "PASSWORD", "secret": "test-ssh-password", "expectedRevision": "1"}, 201)
	host := f.create(t, b, "password-host")
	if host["credentialRevision"] != "2" {
		t.Fatal("latest revision not frozen", host)
	}
	checked := b.call("POST", "/hosts/"+host["id"].(string)+"/checks", nil, 200)
	if strings.Contains(checked.Body.String(), "test-ssh-password") {
		t.Fatal("password leaked")
	}
	caps := dataOf(t, checked)["capabilities"].(map[string]any)
	if caps["reachability"].(map[string]any)["status"] != "AVAILABLE" {
		t.Fatal(caps)
	}
}

func TestHostMaintenanceQueuesAndFrozenPlan(t *testing.T) {
	s, b := setup(t)
	b.login()
	hostID := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "maintenance", "kind": "LOCAL"}, 201))["id"].(string)
	instanceID, planID := uuid.NewString(), uuid.NewString()
	if _, e := s.DB.Exec("INSERT INTO instances(id,name,target_id,draft_json,created_at) VALUES(?,'draft',?,'{}',?)", instanceID, hostID, Now()); e != nil {
		t.Fatal(e)
	}
	p := plan{ID: planID, Kind: "PUBLISH", TargetID: hostID, InstanceID: instanceID, ExpectedVersion: "1", ExpectedGeneration: "0", HostVersion: "1", ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)}
	if _, e := s.DB.Exec("INSERT INTO operation_plans VALUES(?,?,?,?,?)", planID, instanceID, jsonText(p), p.ExpiresAt, Now()); e != nil {
		t.Fatal(e)
	}
	b.call("PATCH", "/hosts/"+hostID+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": "1"}, 200)
	b.call("POST", "/plans/"+planID+"/submit", map[string]string{"idempotencyKey": "maintenance-test"}, 409)
	b.call("PATCH", "/hosts/"+hostID+"/maintenance", map[string]any{"maintenance": false, "expectedVersion": "2"}, 200)
	response := b.call("POST", "/plans/"+planID+"/submit", map[string]string{"idempotencyKey": "maintenance-test"}, 409)
	if !strings.Contains(response.Body.String(), "HOST_CHANGED") {
		t.Fatal(response.Body.String())
	}
	b.call("PATCH", "/hosts/"+hostID+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": "3"}, 200)
	jobA, jobB, hostB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e := s.DB.Exec("INSERT INTO targets(id,name,kind,install_root,created_at) VALUES(?,'available-host','SSH','/opt/available',?)", hostB, Now()); e != nil {
		t.Fatal(e)
	}
	for _, row := range []struct{ id, host, created string }{{jobA, hostID, "2026-10-20T00:00:00Z"}, {jobB, hostB, "2026-10-20T00:01:00Z"}} {
		if _, e := s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'PUBLISH',?,?,?,'QUEUED','PREFLIGHT','{}','host-test',?,?)", row.id, b.currentAdmin(), row.host, row.id, hashToken(row.id), row.created, Now()); e != nil {
			t.Fatal(e)
		}
	}
	b.h.processRelease(context.Background(), jobA, jsonText(deploy.Request{TargetID: hostID, Candidate: &deploy.Release{}}))
	var status string
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", jobA).Scan(&status)
	if status != "QUEUED" {
		t.Fatal(status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); b.h.RunOperations(ctx) }()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", jobB).Scan(&status)
		if status != "QUEUED" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if status == "QUEUED" {
		t.Fatal("maintenance job starved another host")
	}
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", jobA).Scan(&status)
	if status != "QUEUED" {
		t.Fatal("maintenance job was executed", status)
	}
	b.call("PATCH", "/hosts/"+hostID, map[string]any{"name": "renamed", "kind": "LOCAL", "expectedVersion": "4"}, 409)
}

func TestSchemaSixHostMigrationPreservesIdentityAndJobs(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	for index, source := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6} {
		if _, e = db.Exec(source); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", index+1, hashToken(source), Version, Now()); e != nil {
			t.Fatal(e)
		}
	}
	controllerID, adminID, hostID, jobID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'legacy',?)", []any{controllerID, Now()}},
		{"INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,'admin','unchanged',?)", []any{adminID, Now()}},
		{"INSERT INTO targets(id,name,install_root,version,created_at) VALUES(?,'legacy','/opt/legacy',5,?)", []any{hostID, Now()}},
		{"INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'PUBLISH',?,'legacy','hash','UNKNOWN','HEALTH','{}','trace',?,?)", []any{jobID, adminID, hostID, Now(), Now()}},
		{"INSERT INTO operation_locks VALUES(?,?)", []any{"target:" + hostID, jobID}},
	} {
		if _, e = db.Exec(statement.sql, statement.args...); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	host, e := s.readTarget(hostID)
	if e != nil {
		t.Fatal(e)
	}
	if host.Version != "5" || host.Maintenance || host.Capabilities.Reachability.Status != "UNKNOWN" || s.controllerID() != controllerID {
		t.Fatal(host)
	}
	var status, lock string
	if e = s.DB.QueryRow("SELECT j.status,l.job_id FROM jobs j JOIN operation_locks l ON l.job_id=j.id WHERE j.id=?", jobID).Scan(&status, &lock); e != nil || status != "UNKNOWN" || lock != jobID {
		t.Fatal(status, lock, e)
	}
	backups, e := filepath.Glob(filepath.Join(dir, "before-schema-7-*.tar.gz"))
	if e != nil || len(backups) != 1 {
		t.Fatal(backups, e)
	}
	rows, e := s.DB.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign keys invalid")
	}
}

func TestHostLegacyEndpointAndDeployedIdentityGuards(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	host := f.create(t, b, "legacy-main")
	id := host["id"].(string)
	if _, e := s.DB.Exec("INSERT INTO targets(id,name,install_root,kind,address,ssh_port,ssh_user,host_fingerprint,credential_id,credential_revision,created_at) VALUES(?,'legacy-second','/opt/legacy-second','SSH','127.0.0.1',?,'ops',?,?,1,?)", uuid.NewString(), f.Port, f.Fingerprint, f.CredentialID, Now()); e != nil {
		t.Fatal(e)
	}
	in := f.connection()
	in["name"] = "renamed legacy"
	in["kind"] = "SSH"
	in["expectedVersion"] = "1"
	b.call("PATCH", "/hosts/"+id, in, 200)
	instanceID, artifactID := uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO instances(id,name,target_id,draft_json,created_at) VALUES(?,'deployed',?,'{}',?)", []any{instanceID, id, Now()}},
		{"INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?,'artifact',?,1,'image',?)", []any{artifactID, hashToken("artifact"), Now()}},
		{"INSERT INTO releases(id,instance_id,artifact_id,snapshot_json,created_at) VALUES(?,?,?,'{}',?)", []any{uuid.NewString(), instanceID, artifactID, Now()}},
	} {
		if _, e := s.DB.Exec(q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	in["expectedVersion"] = "2"
	in["installRoot"] = "/opt/relocated"
	response := b.call("PATCH", "/hosts/"+id, in, 409)
	if !strings.Contains(response.Body.String(), "HOST_IDENTITY_IN_USE") {
		t.Fatal(response.Body.String())
	}
}

func TestHostChecksHaveBoundedConcurrency(t *testing.T) {
	_, b := setup(t)
	b.login()
	for n := 0; n < cap(b.h.hostCheckSlots); n++ {
		b.h.hostCheckSlots <- struct{}{}
	}
	b.call("POST", "/hosts/fingerprint", map[string]any{}, 429)
	for len(b.h.hostCheckSlots) > 0 {
		<-b.h.hostCheckSlots
	}
}
