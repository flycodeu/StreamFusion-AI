package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func registerTestSoftware(t *testing.T, b *browser, host, kind, locator, typ string) map[string]any {
	t.Helper()
	name := locator
	if len(name) > 40 {
		name = name[:40]
	}
	return dataOf(t, b.call("POST", "/software-instances", map[string]string{"name": "test " + name, "hostId": host, "runtimeKind": kind, "locator": locator, "softwareType": typ, "softwareVersion": "declared-version"}, 201))
}

func unitReadResponse(command string) ([]byte, bool) {
	switch {
	case strings.Contains(command, "'/usr/bin/systemctl'"):
		return []byte("Id=zlm.service\nLoadState=loaded\nActiveState=active\nSubState=running\nResult=success\nFragmentPath=/etc/systemd/system/zlm.service\nDropInPaths=\nTransient=no\nNeedDaemonReload=no\n"), true
	case strings.Contains(command, "'/usr/bin/stat'"):
		return []byte("0:755:directory:4096\n0:755:directory:4096\n0:755:directory:4096\n0:755:directory:4096\n0:644:regular file:1024\n"), true
	case strings.Contains(command, "'/usr/bin/readlink'"):
		return []byte("/etc/systemd/system/zlm.service\n"), true
	case strings.Contains(command, "'/usr/bin/sha256sum'"):
		return []byte(strings.Repeat("a", 64) + "  /etc/systemd/system/zlm.service\n"), true
	case strings.Contains(command, "'/usr/bin/journalctl'"):
		return []byte("service running token=secret-example rtsp://user:password@camera/live\n"), true
	case strings.Contains(command, "'/usr/bin/java'"):
		return []byte("openjdk version 21.0.6\n"), true
	}
	return nil, false
}

func TestSoftwareReadOnlySSHWithoutExecutorAndIdentityGuards(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, unitReadResponse)
	host := f.create(t, b, "readonly host")
	hostID := host["id"].(string)
	i := registerTestSoftware(t, b, hostID, "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	if i["actualState"] != "UNKNOWN" || i["freshness"] != "UNOBSERVED" || i["dataOwnership"] != "EXTERNAL" {
		t.Fatal(i)
	}
	b.call("POST", "/software-instances", map[string]string{"name": "duplicate", "hostId": hostID, "runtimeKind": "SYSTEMD", "locator": "zlm.service", "softwareType": "ZLM"}, 409)
	checked := dataOf(t, b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200))
	obs := checked["observation"].(map[string]any)
	if checked["actualState"] != "RUNNING" || checked["managementMode"] != "OBSERVE" || obs["identityHash"] == "" || obs["version"] != "" || checked["softwareVersion"] != "declared-version" {
		t.Fatal(checked)
	}
	logs := b.call("GET", "/software-instances/"+id+"/logs", nil, 200).Body.String()
	if strings.Contains(logs, "secret-example") || strings.Contains(logs, "user:password") || !strings.Contains(logs, "REDACTED") {
		t.Fatal(logs)
	}
	b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": checked["version"].(string), "action": "STOP", "idempotencyKey": "unmanaged-stop"}, 409)
	b.call("POST", "/software-instances/"+id+"/adopt", map[string]any{"expectedVersion": checked["version"], "identityHash": obs["identityHash"], "confirmed": false, "idempotencyKey": "unconfirmed-adopt"}, 409)
	refused := b.call("POST", "/software-instances/"+id+"/adopt", map[string]any{"expectedVersion": checked["version"], "identityHash": obs["identityHash"], "confirmed": true, "idempotencyKey": "no-executor-adopt"}, 409)
	if !strings.Contains(refused.Body.String(), "EXECUTOR_MISSING") {
		t.Fatal(refused.Body.String())
	}
	var locks int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks").Scan(&locks)
	if locks != 0 {
		t.Fatal("known preflight failure retained lock", locks)
	}
	b.call("DELETE", "/hosts/"+hostID+"?expectedVersion=1", nil, 409)
	in := f.connection()
	in["name"] = "readonly host"
	in["expectedVersion"] = "1"
	in["installRoot"] = "/opt/another-target"
	b.call("PATCH", "/hosts/"+hostID, in, 409)
	tool := registerTestSoftware(t, b, hostID, "TOOL", "java", "JAVA")
	tool = dataOf(t, b.call("POST", "/software-instances/"+tool["id"].(string)+"/observe", map[string]any{}, 200))
	if tool["actualState"] != "INSTALLED" || !strings.Contains(tool["observation"].(map[string]any)["version"].(string), "21.0.6") {
		t.Fatal(tool)
	}
	b.call("POST", "/software-instances", map[string]string{"name": "invalid tool", "hostId": hostID, "runtimeKind": "TOOL", "locator": "ffmpeg", "softwareType": "JAVA"}, 400)
	b.call("POST", "/software-instances", map[string]string{"name": "protected", "hostId": hostID, "runtimeKind": "SYSTEMD", "locator": "sshd.service", "softwareType": "OTHER"}, 400)
	b.call("POST", "/software-instances/"+id+"/unmanage", map[string]string{"expectedVersion": checked["version"].(string), "idempotencyKey": "release-observe"}, 200)
	b.call("POST", "/software-instances/"+id+"/unmanage", map[string]string{"expectedVersion": checked["version"].(string), "idempotencyKey": "release-observe"}, 200)
	old, e := s.readSoftware(id)
	if e != nil || old.ManagementMode != "RELEASED" {
		t.Fatal(old, e)
	}
	registerTestSoftware(t, b, hostID, "SYSTEMD", "zlm.service", "ZLM")
}

func TestSoftwareObservationFailureDoesNotClaimStoppedOrFreshOldState(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	host := f.create(t, b, "unreadable")
	i := registerTestSoftware(t, b, host["id"].(string), "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	if _, e := s.DB.Exec("UPDATE software_instances SET actual_state='RUNNING',identity_hash=?,observed_at=?,observation_json=? WHERE id=?", strings.Repeat("a", 64), Now(), jsonText(softwareObservation{SoftwareObservation: &deploy.SoftwareObservation{State: "RUNNING", ObservedAt: Now()}}), id); e != nil {
		t.Fatal(e)
	}
	checked := dataOf(t, b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200))
	if checked["actualState"] != "UNKNOWN" || checked["errorCode"] == "" || checked["observation"].(map[string]any)["state"] != "RUNNING" {
		t.Fatal(checked)
	}
}

func TestSoftwareDockerNamesResolveToOneFrozenIdentity(t *testing.T) {
	s, b := setup(t)
	b.login()
	containerID := strings.Repeat("b", 64)
	response := func(command string) ([]byte, bool) {
		if strings.Contains(command, "'/usr/bin/docker'") {
			return []byte(`{"id":"` + containerID + `","name":"/redis","imageId":"sha256:` + strings.Repeat("c", 64) + `","image":"redis:7.4","labels":{},"state":"running"}`), true
		}
		return nil, false
	}
	f := hostSSHServer(t, s, b, false, false, response)
	host := f.create(t, b, "docker readonly")
	hid := host["id"].(string)
	i := registerTestSoftware(t, b, hid, "DOCKER", "redis", "REDIS")
	id := i["id"].(string)
	actual := dataOf(t, b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200))
	if actual["locator"] != "redis" || actual["observation"].(map[string]any)["locator"] != containerID || actual["version"] != "1" {
		t.Fatal(actual)
	}
	second := registerTestSoftware(t, b, hid, "DOCKER", "another-name", "REDIS")
	b.call("POST", "/software-instances/"+second["id"].(string)+"/observe", map[string]any{}, 200)
	if _, e := s.DB.Exec("UPDATE software_instances SET identity_hash=?,management_mode='MANAGED' WHERE id=?", strings.Repeat("d", 64), id); e != nil {
		t.Fatal(e)
	}
	actual = dataOf(t, b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200))
	if actual["latestAttempt"].(map[string]any)["code"] != "SOFTWARE_IDENTITY_DRIFT" || actual["locator"] != "redis" {
		t.Fatal(actual)
	}
}

func TestSoftwareJobsFreezeBindingRevisionAndRequireAck(t *testing.T) {
	s, b := setup(t)
	b.login()
	results := map[string]deploy.Result{}
	ackAllowed := true
	unknown := false
	hid := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		if req.Software == nil {
			return nil, false
		}
		res := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_VERIFIED", ObservedAt: Now()}
		state := "RUNNING"
		if req.Action == "SOFTWARE_STOP" {
			state = "STOPPED"
		}
		res.Software = &deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: state, ObservedAt: Now()}
		if req.Action == "STATUS" || req.Action == "ACK" {
			res = results[req.JobID]
			if req.Action == "ACK" && ackAllowed && deploy.Digest(res) == req.ResultHash {
				res.Ack = true
			}
		}
		if unknown && req.Action == "SOFTWARE_START" {
			res.Status = "UNKNOWN"
		}
		if strings.HasPrefix(req.Action, "SOFTWARE_") {
			results[req.JobID] = res
		}
		raw, _ := json.Marshal(res)
		return raw, true
	})
	i := registerTestSoftware(t, b, hid, "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	identity := strings.Repeat("e", 64)
	if _, e := s.DB.Exec("UPDATE software_instances SET actual_state='RUNNING',identity_hash=?,observed_at=? WHERE id=?", identity, Now(), id); e != nil {
		t.Fatal(e)
	}
	adopt := map[string]any{"expectedVersion": "1", "identityHash": identity, "confirmed": true, "idempotencyKey": "adopt-software-1"}
	accepted := dataOf(t, b.call("POST", "/software-instances/"+id+"/adopt", adopt, 202))
	job := accepted["jobId"].(string)
	if dataOf(t, b.call("POST", "/software-instances/"+id+"/adopt", adopt, 200))["jobId"] != job {
		t.Fatal("idempotency failed")
	}
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	current, e := s.readSoftware(id)
	if e != nil || current.ManagementMode != "MANAGED" || current.BindingRevision != 1 {
		t.Fatal(current, e)
	}
	ackAllowed = false
	stop := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": current.Version, "action": "STOP", "idempotencyKey": "stop-software-1"}, 202))
	job = stop["jobId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	current, e = s.readSoftware(id)
	if e != nil || current.ActualState != "STOPPED" || current.BindingRevision != 1 {
		t.Fatal(current, e)
	}
	b.call("POST", "/software-instances/"+id+"/unmanage", map[string]string{"expectedVersion": current.Version, "idempotencyKey": "unmanage-locked"}, 409)
	var locks int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if locks != 2 {
		t.Fatal("unacked lock lost", locks)
	}
	ackAllowed = true
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	b.call("GET", "/jobs/"+job+"/logs", nil, 200)
	unknown = true
	start := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": current.Version, "action": "START", "idempotencyKey": "start-unknown-1"}, 202))
	job = start["jobId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	current, _ = s.readSoftware(id)
	if current.ActualState != "UNKNOWN" {
		t.Fatal(current)
	}
	b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 409)
	message := b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 409).Body.String()
	if !strings.Contains(message, "SOFTWARE_RECONCILE_REQUIRED") || strings.Contains(message, "冻结基线恢复") {
		t.Fatal(message)
	}
}

func TestSoftwareDockerEngineWithoutComposeAndHostPreflightCAS(t *testing.T) {
	s, b := setup(t)
	b.login()
	engine, changeHost := true, false
	hid := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		if req.Action != "CAPABILITIES" {
			return nil, false
		}
		if changeHost {
			if _, e := s.DB.Exec("UPDATE targets SET version=version+1 WHERE id=?", req.TargetID); e != nil {
				t.Error(e)
			}
		}
		raw, _ := json.Marshal(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "dockerAvailable": false, "dockerEngineAvailable": engine, "softwareLifecycleVersion": 1, "platform": "linux/amd64"})
		return raw, true
	})
	i := registerTestSoftware(t, b, hid, "DOCKER", strings.Repeat("b", 64), "REDIS")
	id := i["id"].(string)
	identity := strings.Repeat("e", 64)
	if _, e := s.DB.Exec("UPDATE software_instances SET actual_state='RUNNING',identity_hash=?,observed_at=? WHERE id=?", identity, Now(), id); e != nil {
		t.Fatal(e)
	}
	in := map[string]any{"expectedVersion": "1", "identityHash": identity, "confirmed": true, "idempotencyKey": "docker-host-cas"}
	changeHost = true
	refused := b.call("POST", "/software-instances/"+id+"/adopt", in, 409)
	if !strings.Contains(refused.Body.String(), "HOST_CHANGED") {
		t.Fatal(refused.Body.String())
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 0 {
		t.Fatal("host change created job", count)
	}
	changeHost = false
	in["idempotencyKey"] = "docker-engine-only"
	accepted := dataOf(t, b.call("POST", "/software-instances/"+id+"/adopt", in, 202))
	b.call("POST", "/jobs/"+accepted["jobId"].(string)+"/cancel", map[string]any{}, 200)
	if _, e := s.DB.Exec("UPDATE software_instances SET management_mode='MANAGED',binding_revision=1,error_code='',version=version+1 WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
	current, e := s.readSoftware(id)
	if e != nil {
		t.Fatal(e)
	}
	engine = false
	b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": current.Version, "action": "STOP", "idempotencyKey": "no-engine-stop"}, 409)
	b.call("POST", "/software-instances/"+id+"/unmanage", map[string]string{"expectedVersion": current.Version, "idempotencyKey": "unmanage-no-engine"}, 202)
}

func TestSoftwareRejectsOldExecutorWithSameVersionBeforeCreatingJob(t *testing.T) {
	s, b := setup(t)
	b.login()
	var lifecycleVersion atomic.Int64
	hid := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		if req.Action != "CAPABILITIES" {
			return nil, false
		}
		// F01 executors share the release number but do not understand software
		// lifecycle requests. The explicit capability is the compatibility gate.
		capability := map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "dockerAvailable": true, "platform": "linux/amd64"}
		if lifecycleVersion.Load() > 0 {
			capability["softwareLifecycleVersion"] = lifecycleVersion.Load()
		}
		raw, _ := json.Marshal(capability)
		return raw, true
	})
	i := registerTestSoftware(t, b, hid, "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	identity := strings.Repeat("e", 64)
	if _, e := s.DB.Exec("UPDATE software_instances SET actual_state='RUNNING',identity_hash=?,observed_at=? WHERE id=?", identity, Now(), id); e != nil {
		t.Fatal(e)
	}
	refused := b.call("POST", "/software-instances/"+id+"/adopt", map[string]any{"expectedVersion": "1", "identityHash": identity, "confirmed": true, "idempotencyKey": "old-executor-gate"}, 409)
	if !strings.Contains(refused.Body.String(), "EXECUTOR_SOFTWARE_UNSUPPORTED") {
		t.Fatal(refused.Body.String())
	}
	lifecycleVersion.Store(2)
	refused = b.call("POST", "/software-instances/"+id+"/adopt", map[string]any{"expectedVersion": "1", "identityHash": identity, "confirmed": true, "idempotencyKey": "unknown-executor-gate"}, 409)
	if !strings.Contains(refused.Body.String(), "EXECUTOR_SOFTWARE_UNSUPPORTED") {
		t.Fatal(refused.Body.String())
	}
	var jobs, locks int
	if e := s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); e != nil {
		t.Fatal(e)
	}
	if e := s.DB.QueryRow("SELECT count(*) FROM operation_locks").Scan(&locks); e != nil {
		t.Fatal(e)
	}
	if jobs != 0 || locks != 0 {
		t.Fatal("old executor created unknown work", jobs, locks)
	}
}

func TestSoftwareQueuedCancelAndMaintenance(t *testing.T) {
	s, b := setup(t)
	b.login()
	hid := pluginSSHFixture(t, s, b, true)
	i := registerTestSoftware(t, b, hid, "SYSTEMD", "zlm.service", "ZLM")
	id := i["id"].(string)
	identity := strings.Repeat("e", 64)
	s.DB.Exec("UPDATE software_instances SET actual_state='RUNNING',identity_hash=?,observed_at=? WHERE id=?", identity, Now(), id)
	accepted := dataOf(t, b.call("POST", "/software-instances/"+id+"/adopt", map[string]any{"expectedVersion": "1", "identityHash": identity, "confirmed": true, "idempotencyKey": "adopt-queued-1"}, 202))
	job := accepted["jobId"].(string)
	b.call("PATCH", "/hosts/"+hid+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": "1"}, 200)
	var raw, status string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	if status != "QUEUED" {
		t.Fatal(status)
	}
	b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 200)
	var locks int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks").Scan(&locks)
	if locks != 0 {
		t.Fatal(locks)
	}
	current, _ := s.readSoftware(id)
	if current.ManagementMode != "OBSERVE" || current.ErrorCode != "JOB_CANCELLED" {
		t.Fatal(current)
	}
	b.call("POST", "/software-instances/"+id+"/unmanage", map[string]string{"expectedVersion": current.Version, "idempotencyKey": "maintenance-release"}, 409)
}

func TestSchemaSevenUpgradePreservesHistoryAndLocks(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	for index, source := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7} {
		if _, e = db.Exec(source); e != nil {
			t.Fatal(index, e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", index+1, hashToken(source), Version, Now()); e != nil {
			t.Fatal(e)
		}
	}
	cid, admin, hid, job := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{{"INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'existing',?)", []any{cid, Now()}}, {"INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,'admin',?,?)", []any{admin, []byte("old"), Now()}}, {"INSERT INTO targets(id,name,install_root,created_at) VALUES(?,'host','/opt/old',?)", []any{hid, Now()}}, {"INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'PUBLISH',?,'history-key','hash','UNKNOWN','HEALTH','{}','historic-trace',?,?)", []any{job, admin, hid, Now(), Now()}}, {"INSERT INTO operation_locks VALUES(?,?)", []any{"target:" + hid, job}}} {
		if _, e = db.Exec(q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var got, trace string
	if e = s.DB.QueryRow("SELECT l.job_id,j.trace_id FROM operation_locks l JOIN jobs j ON j.id=l.job_id").Scan(&got, &trace); e != nil || got != job || trace != "historic-trace" || s.controllerID() != cid {
		t.Fatal(got, trace, e)
	}
	backups, e := filepath.Glob(filepath.Join(dir, "before-schema-8-*.tar.gz"))
	if e != nil || len(backups) != 1 {
		t.Fatal(backups, e)
	}
	var checksum string
	s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=7").Scan(&checksum)
	if checksum != hashToken(schemaV7) {
		t.Fatal("old checksum changed")
	}
	rows, e := s.DB.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign keys broken")
	}
}
