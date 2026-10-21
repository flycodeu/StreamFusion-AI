package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func observationTick(t *testing.T, h *HTTP) {
	t.Helper()
	var workers sync.WaitGroup
	h.scheduleObservations(context.Background(), &workers)
	workers.Wait()
	h.finishObservationBatches()
}

func TestObservationBatchOverOneChunkAndBoundedRange(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, unitReadResponse)
	host := f.create(t, b, "chunk-host")
	hid := host["id"].(string)
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 105; n++ {
		id := uuid.NewString()
		if _, e = tx.Exec("INSERT INTO software_instances(id,name,host_id,software_type,runtime_kind,locator,created_at,updated_at) VALUES(?,? ,?,'OTHER','SYSTEMD',?,?,?)", id, id, hid, "test-"+id+".service", Now(), Now()); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	bid := submitObservation(t, b, hid, "more-than-one-chunk")
	observationTick(t, b.h)
	batch := observationBatchOf(t, b, bid)
	if len(batch.Items) != 105 {
		t.Fatal(len(batch.Items))
	}
	pending := 0
	for _, item := range batch.Items {
		if item.Status == "QUEUED" {
			pending++
		}
	}
	if pending != 5 {
		t.Fatal("batch did not respect 100 item chunk", pending)
	}
	observationTick(t, b.h)
	batch = observationBatchOf(t, b, bid)
	if batch.Status != "PARTIAL" {
		t.Fatal(batch.Status)
	}
	for _, item := range batch.Items {
		if item.Status != "FAILED" {
			t.Fatal(item)
		}
	}
}
func TestObservationRevocationDuringProbeCannotPublish(t *testing.T) {
	s, b := setup(t)
	b.login()
	revoke := false
	f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
		if revoke && strings.Contains(command, "'/usr/bin/java'") {
			s.DB.Exec("UPDATE admins SET session_generation=session_generation+1")
		}
		return unitReadResponse(command)
	})
	host := f.create(t, b, "revocation-race")
	hid := host["id"].(string)
	i := registerTestSoftware(t, b, hid, "TOOL", "java", "JAVA")
	id := i["id"].(string)
	bid := submitObservation(t, b, hid, "revoke-while-running")
	revoke = true
	observationTick(t, b.h)
	current, _ := s.readSoftware(id)
	if current.Observation.State != "UNKNOWN" {
		t.Fatal("revoked batch published observation", current)
	}
	var code string
	s.DB.QueryRow("SELECT error_code FROM observation_batch_items WHERE batch_id=?", bid).Scan(&code)
	if code != "AUTH_REVOKED" {
		t.Fatal(code)
	}
}
func TestObservationSchema8UpgradeRetainsRecordsAndBacksUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	controller := uuid.NewString()
	db, e := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	for index, source := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV8} {
		if _, e = db.Exec(source); e != nil {
			db.Close()
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", index+1, hashToken(source), Version, Now()); e != nil {
			db.Close()
			t.Fatal(e)
		}
	}
	if _, e = db.Exec("INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'schema8',?)", controller, Now()); e != nil {
		db.Close()
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.controllerID() != controller {
		t.Fatal("controller identity changed")
	}
	backups, e := filepath.Glob(filepath.Join(dir, "before-schema-9-*.tar.gz"))
	if e != nil || len(backups) != 1 {
		t.Fatal(backups, e)
	}
	var version int
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if version != SchemaVersion {
		t.Fatal(version)
	}
	rows, e := s.DB.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("invalid foreign key after migration")
	}
}
func observationBatchOf(t *testing.T, b *browser, id string) observationBatch {
	t.Helper()
	var body struct{ Data observationBatch }
	if e := json.Unmarshal(b.call("GET", "/observation-batches/"+id, nil, 200).Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	return body.Data
}
func submitObservation(t *testing.T, b *browser, host, key string) string {
	t.Helper()
	d := dataOf(t, b.call("POST", "/observation-batches", observationBatchInput{Kind: "SOFTWARE", HostID: host, Key: key}, 202))
	return d["id"].(string)
}

func TestObservationBatchFilterDeduplicateAndResume(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, unitReadResponse)
	host := f.create(t, b, "observe-a")
	hid := host["id"].(string)
	i := registerTestSoftware(t, b, hid, "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	registerTestSoftware(t, b, hid, "TOOL", "java", "JAVA")
	input := observationBatchInput{Kind: "SOFTWARE", HostID: hid, Query: "zlm", Key: "batch-filter-01"}
	bid := dataOf(t, b.call("POST", "/observation-batches", input, 202))["id"].(string)
	if dataOf(t, b.call("POST", "/observation-batches", input, 200))["id"] != bid {
		t.Fatal("idempotency replay changed batch")
	}
	input.Key = "batch-filter-02"
	if dataOf(t, b.call("POST", "/observation-batches", input, 200))["id"] != bid {
		t.Fatal("same scope not merged")
	}
	input.Query = "java"
	b.call("POST", "/observation-batches", input, 409)
	before := observationBatchOf(t, b, bid)
	if len(before.Items) != 1 || before.Items[0].ObjectID != id {
		t.Fatal(before)
	}
	observationTick(t, b.h)
	after := observationBatchOf(t, b, bid)
	if after.Status != "SUCCEEDED" || after.Items[0].Status != "SUCCEEDED" {
		t.Fatal(after)
	}
	software, e := s.readSoftware(id)
	if e != nil || software.ActualState != "RUNNING" || software.Version != "1" || software.ObservationRevision < 1 {
		t.Fatal(software, e)
	}
	// Persistence belongs to the store, independent of HTTP/component lifetime.
	restarted, e := NewHTTP(s, "http://127.0.0.1:19090", http.NotFoundHandler(), b.h.logger)
	if e != nil {
		t.Fatal(e)
	}
	if current, e := restarted.store.readSoftware(id); e != nil || current.Observation.State != "RUNNING" {
		t.Fatal(current, e)
	}
}
func TestObservationBatchesCancelConfigChangeAndRevocation(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, unitReadResponse)
	host := f.create(t, b, "observe-cancel")
	hid := host["id"].(string)
	i := registerTestSoftware(t, b, hid, "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	bid := submitObservation(t, b, hid, "cancel-observation")
	b.call("POST", "/observation-batches/"+bid+"/cancel", map[string]any{}, 200)
	if batch := observationBatchOf(t, b, bid); batch.Status != "CANCELLED" || batch.Items[0].Status != "CANCELLED" {
		t.Fatal(batch)
	}
	bid = submitObservation(t, b, hid, "changed-observation")
	s.DB.Exec("UPDATE software_instances SET version=version+1 WHERE id=?", id)
	observationTick(t, b.h)
	if batch := observationBatchOf(t, b, bid); batch.Items[0].ErrorCode != "CONFIG_CHANGED" || batch.Items[0].Status != "SKIPPED" {
		t.Fatal(batch)
	}
	bid = submitObservation(t, b, hid, "revoked-observation")
	s.DB.Exec("UPDATE admins SET session_generation=session_generation+1")
	observationTick(t, b.h)
	var code string
	s.DB.QueryRow("SELECT error_code FROM observation_batch_items WHERE batch_id=?", bid).Scan(&code)
	if code != "AUTH_REVOKED" {
		t.Fatal(code)
	}
}

func TestObservationChineseFilterAndExpiredBatchReceipts(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, unitReadResponse)
	host := f.create(t, b, "filter-host")
	hid := host["id"].(string)
	i := registerTestSoftware(t, b, hid, "SYSTEMD", "custom.service", "OTHER")
	registerTestSoftware(t, b, hid, "TOOL", "java", "JAVA")
	input := observationBatchInput{Kind: "SOFTWARE", HostID: hid, Query: "其他服务", Key: "chinese-filter-01"}
	id := dataOf(t, b.call("POST", "/observation-batches", input, 202))["id"].(string)
	batch := observationBatchOf(t, b, id)
	if len(batch.Items) != 1 || batch.Items[0].ObjectID != i["id"] {
		t.Fatal(batch)
	}
	b.call("POST", "/observation-batches/"+id+"/cancel", map[string]any{}, 200)
	if _, e := s.DB.Exec("UPDATE observation_batches SET created_at=? WHERE id=?", time.Now().Add(-8*24*time.Hour).UTC().Format(time.RFC3339Nano), id); e != nil {
		t.Fatal(e)
	}
	input.Key = "chinese-filter-02"
	current := dataOf(t, b.call("POST", "/observation-batches", input, 202))["id"].(string)
	var retained int
	if e := s.DB.QueryRow("SELECT count(*) FROM observation_batch_requests WHERE batch_id=?", id).Scan(&retained); e != nil || retained != 0 {
		t.Fatal("expired completed batch receipts not pruned", retained, e)
	}
	if _, e := s.DB.Exec("UPDATE observation_batches SET actor_generation=actor_generation-1 WHERE id=?", current); e != nil {
		t.Fatal(e)
	}
	input.Key = "chinese-filter-03"
	if next := dataOf(t, b.call("POST", "/observation-batches", input, 202))["id"].(string); next == current {
		t.Fatal("new request merged into a revoked generation")
	}
}
func TestObservationsContinueWithoutBrowserAndPause(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, func(cmd string) ([]byte, bool) {
		if cmd == hostMetricsCommand {
			return []byte(hostMetricFixture), true
		}
		return unitReadResponse(cmd)
	})
	host := f.create(t, b, "background-host")
	hid := host["id"].(string)
	i := registerTestSoftware(t, b, hid, "TOOL", "java", "JAVA")
	// No browser endpoint initiates either probe.
	observationTick(t, b.h)
	target, _ := s.readTarget(hid)
	resource, e := s.readHostObservation(target)
	if e != nil || resource.Confirmed == nil || resource.Confirmed.CPUCores != 8 {
		t.Fatal(resource, e)
	}
	software, e := s.readSoftware(i["id"].(string))
	if e != nil || software.Observation.State != "INSTALLED" {
		t.Fatal(software, e)
	}
	commands := f.Commands.Load()
	b.call("GET", "/host-observations", nil, 200)
	if f.Commands.Load() != commands {
		t.Fatal("GET performed a remote probe")
	}
	b.call("PATCH", "/hosts/"+hid+"/monitoring", map[string]any{"enabled": false, "expectedVersion": "1"}, 200)
	observationTick(t, b.h)
	if f.Commands.Load() != commands {
		t.Fatal("paused host probed")
	}
	// Manual work is permitted while automatic monitoring is paused.
	batch := submitObservation(t, b, hid, "manual-while-paused")
	observationTick(t, b.h)
	if v := observationBatchOf(t, b, batch); v.Status != "SUCCEEDED" {
		t.Fatal(v)
	}
	resource, e = s.readHostObservation(target)
	if e != nil || resource.Enabled || resource.Confirmed == nil {
		t.Fatal(resource, e)
	}
}
func TestObservationFailureRetentionHostIdentityAndUnknownLock(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, unitReadResponse)
	host := f.create(t, b, "retain-host")
	hid := host["id"].(string)
	i := registerTestSoftware(t, b, hid, "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200)
	before, _ := s.readSoftware(id)
	target, _ := s.readTarget(hid)
	oldTime := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	_, e := s.DB.Exec("UPDATE software_instances SET observed_at=?,observation_json=json_set(observation_json,'$.observedAt',?) WHERE id=?", oldTime, oldTime, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.h.saveSoftwareObservation(target, before, failedSoftwareObservation(before, "SSH_TIMEOUT"), "test-timeout"); e != nil {
		t.Fatal(e)
	}
	after, _ := s.readSoftware(id)
	if after.Freshness != "STALE" || after.Observation.State != "RUNNING" || after.Observation.ObservedAt != oldTime || after.Attempt.Code != "SSH_TIMEOUT" {
		t.Fatal(after)
	}
	var actor string
	s.DB.QueryRow("SELECT id FROM admins LIMIT 1").Scan(&actor)
	job := uuid.NewString()
	_, e = s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_START',?,'unknown-observation','hash','UNKNOWN','UNKNOWN','{}','test',?,?)", job, actor, hid, Now(), Now())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO operation_locks VALUES(?,?)", "software:"+id, job)
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("UPDATE software_instances SET actual_state='UNKNOWN',last_job_id=? WHERE id=?", job, id)
	after, _ = s.readSoftware(id)
	good := softwareObservation{SoftwareObservation: &deploy.SoftwareObservation{State: "RUNNING", ObservedAt: Now(), Kind: "SYSTEMD", Locator: "zlm.service"}}
	if e = b.h.saveSoftwareObservation(target, after, good, "test-locked"); e != nil {
		t.Fatal(e)
	}
	after, _ = s.readSoftware(id)
	var locks int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if after.ActualState != "UNKNOWN" || after.Observation.State != "RUNNING" || locks != 1 {
		t.Fatal(after, locks)
	}
	s.DB.Exec("UPDATE targets SET ssh_user='changed-user' WHERE id=?", hid)
	after, _ = s.readSoftware(id)
	if after.Freshness != "UNOBSERVED" || after.Attempt.Code != "CONFIG_CHANGED" {
		t.Fatal(after)
	}
	if e = b.h.saveSoftwareObservation(target, before, good, "old-host"); e == nil || !strings.Contains(e.Error(), "HOST_CHANGED") {
		t.Fatal(e)
	}
}
func TestObservationSnapshotSurvivesStoreReopen(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "local observation", "kind": "LOCAL"}, 201))
	target, _ := s.readTarget(host["id"].(string))
	m, _ := parseHostMetrics(hostMetricFixture)
	if e := b.h.saveHostObservation(target, m, "persist"); e != nil {
		t.Fatal(e)
	}
	dir := s.Dir
	s.Close()
	reopened, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	o, e := reopened.readHostObservation(target)
	if e != nil || o.Confirmed == nil || o.Confirmed.CPUCores != 8 || o.Attempt.TraceID != "persist" {
		t.Fatal(o, e)
	}
}
