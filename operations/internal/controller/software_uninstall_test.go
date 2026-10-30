package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestSoftwareUninstallRetainsHistoryReconcilesAndPreservesDataDefault(t *testing.T) {
	s, b := setup(t)
	b.login()
	var uninstallCalls atomic.Int32
	var oldExecutor, unknown atomic.Bool
	var mu sync.Mutex
	results := map[string]deploy.Result{}
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { raw, _ := json.Marshal(v); return raw, true }
		if req.Action == "CAPABILITIES" {
			uninstallVersion := 1
			if oldExecutor.Load() {
				uninstallVersion = 0
			}
			return encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "platform": "linux/amd64", "dockerEngineAvailable": true, "softwareLifecycleVersion": 1, "softwareUninstallVersion": uninstallVersion, "serviceInstallVersion": serviceinstall.CapabilityVersion, "serviceCatalogHash": serviceinstall.CatalogHash()})
		}
		if req.Uninstallation == nil {
			return nil, false
		}
		mu.Lock()
		defer mu.Unlock()
		if req.Action == "SOFTWARE_UNINSTALL" {
			uninstallCalls.Add(1)
			target, _ := s.readTarget(req.TargetID)
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_UNINSTALLED", ObservedAt: Now(), Uninstallation: &deploy.SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: req.Uninstallation.DeleteData, DataRemoved: req.Uninstallation.DeleteData, DataRoot: serviceinstall.InstallRoot(target.InstallRoot, req.Software.ID, req.Uninstallation.Installation.Config), ObservedAt: Now()}}
			results[req.JobID] = result
			if unknown.Load() {
				result.Status = "UNKNOWN"
			}
			return encode(result)
		}
		result := results[req.JobID]
		if req.Action == "ACK" && req.ResultHash == deploy.Digest(result) {
			result.Ack = true
		}
		return encode(result)
	})
	plan := nginxPlan(t, b, host, 18198)
	accepted := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "install-before-uninstall"}, 202))
	installJob, id := accepted["jobId"].(string), accepted["softwareInstanceId"].(string)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", installJob).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processSoftware(context.Background(), installJob, raw)
	i, err := s.readSoftware(id)
	if err != nil {
		t.Fatal(err)
	}
	detail := dataOf(t, b.call("GET", "/software-instances/"+id, nil, 200))
	paths, ok := detail["installationPaths"].(map[string]any)
	target, err := s.readTarget(host)
	if err != nil {
		t.Fatal(err)
	}
	root := serviceinstall.InstallRoot(target.InstallRoot, id, i.Installation.Config)
	if !ok || paths["root"] != root || paths["data"] != root+"/data" || paths["config"] != root+"/config" || paths["logs"] != root+"/logs" {
		t.Fatal("software detail must expose exact owned uninstall paths", paths)
	}
	input := map[string]any{"expectedVersion": i.Version, "action": "UNINSTALL", "idempotencyKey": "uninstall-preserve-default", "confirmed": true}
	oldExecutor.Store(true)
	if body := b.call("POST", "/software-instances/"+id+"/actions", input, 409).Body.String(); !strings.Contains(body, "EXECUTOR_SOFTWARE_UNINSTALL_UNSUPPORTED") {
		t.Fatal(body)
	}
	oldExecutor.Store(false)
	input["deleteData"], input["confirmationName"] = true, "wrong name"
	b.call("POST", "/software-instances/"+id+"/actions", input, 409)
	delete(input, "deleteData")
	delete(input, "confirmationName")
	uninstall := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", input, 202))
	job := uninstall["jobId"].(string)
	if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.Uninstallation == nil || req.Uninstallation.DeleteData || strings.Contains(raw, "deleteData") {
		t.Fatal(raw)
	}
	unknown.Store(true)
	b.h.processSoftware(context.Background(), job, raw)
	var status string
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	if status != "UNKNOWN" {
		t.Fatal(status)
	}
	mu.Lock()
	forged := results[job]
	evidence := *forged.Uninstallation
	forged.Uninstallation = &evidence
	mu.Unlock()
	forged.Uninstallation.IdentityHash = strings.Repeat("f", 64)
	if err = b.h.commitSoftwareResult(job, req, forged); err == nil {
		t.Fatal("forged uninstall receipt accepted")
	}
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	i, err = s.readSoftware(id)
	if err != nil || i.ManagementMode != "RELEASED" || i.ActualState != "REMOVED" || i.InstallationStatus != "UNINSTALLED" || i.Installation == nil || i.InstallationEvidence == nil {
		t.Fatalf("%+v %v", i, err)
	}
	oldExecutor.Store(true)
	if replay := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", input, 200)); replay["jobId"] != job {
		t.Fatal(replay)
	}
	if uninstallCalls.Load() != 1 {
		t.Fatal("uninstall replayed", uninstallCalls.Load())
	}
	var history, locks int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE id IN (?,?)", installJob, job).Scan(&history)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if history != 2 || locks != 0 {
		t.Fatal(history, locks)
	}
	input["deleteData"], input["confirmationName"] = true, i.Name
	b.call("POST", "/software-instances/"+id+"/actions", input, 409)
	oldExecutor.Store(false)
	unknown.Store(false)
	secondPlan := nginxPlan(t, b, host, 18199)
	second := dataOf(t, b.call("POST", "/software-install-plans/"+secondPlan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "install-delete-data"}, 202))
	secondJob, secondID := second["jobId"].(string), second["softwareInstanceId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", secondJob).Scan(&raw)
	b.h.processSoftware(context.Background(), secondJob, raw)
	secondInstance, err := s.readSoftware(secondID)
	if err != nil {
		t.Fatal(err)
	}
	remove := dataOf(t, b.call("POST", "/software-instances/"+secondID+"/actions", map[string]any{"expectedVersion": secondInstance.Version, "action": "UNINSTALL", "idempotencyKey": "delete-data-confirmed", "confirmed": true, "deleteData": true, "confirmationName": secondInstance.Name}, 202))
	removeJob := remove["jobId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", removeJob).Scan(&raw)
	if deploy.StrictJSON([]byte(raw), &req) != nil || !req.Uninstallation.DeleteData {
		t.Fatal(raw)
	}
	b.h.processSoftware(context.Background(), removeJob, raw)
	mu.Lock()
	deleted := results[removeJob]
	mu.Unlock()
	if deleted.Uninstallation == nil || !deleted.Uninstallation.DataRemoved || !deleted.Uninstallation.DeleteData {
		t.Fatal("explicit deletion not frozen", deleted)
	}
}

func TestSoftwareUninstallRejectsAdoptedExternalAndMissingConfirmation(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := pluginSSHFixture(t, s, b, true)
	i := registerTestSoftware(t, b, host, "SYSTEMD", "redis.service", "REDIS")
	id := i["id"].(string)
	if _, err := s.DB.Exec("UPDATE software_instances SET management_mode='MANAGED',binding_revision=1,identity_hash=? WHERE id=?", strings.Repeat("a", 64), id); err != nil {
		t.Fatal(err)
	}
	body := b.call("POST", "/software-instances/"+id+"/actions", map[string]any{"expectedVersion": "1", "action": "UNINSTALL", "idempotencyKey": "external-uninstall", "confirmed": true}, 409).Body.String()
	if !strings.Contains(body, "SOFTWARE_UNINSTALL_NOT_OWNED") {
		t.Fatal(body)
	}
}

func TestSchemaThirteenUpgradePreservesUnknownJobsLocksAndReceipts(t *testing.T) {
	f := newSchemaTenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []struct {
		version int
		source  string
	}{{11, schemaV11}, {12, schemaV12}, {13, schemaV13}} {
		if _, err = db.Exec(migration.source); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", migration.version, hashToken(migration.source), "historical", Now()); err != nil {
			t.Fatal(err)
		}
	}
	before := schemaTenRows(t, db)
	db.Close()
	s, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for key, value := range before {
		if schemaTenRows(t, s.DB)[key] != value {
			t.Fatal("history changed:", key)
		}
	}
	var version int
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 14 {
		t.Fatal(version)
	}
	backups, err := filepath.Glob(filepath.Join(f.dir, "before-schema-14-*.tar.gz"))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	assertSchemaElevenForeignKeys(t, s.DB)
}

func TestDockerEngineDependenciesIncludeStoppedAndObservedSoftware(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := pluginSSHFixture(t, s, b, true)
	i := registerTestSoftware(t, b, host, "DOCKER", strings.Repeat("b", 64), "REDIS")
	if _, err := s.DB.Exec("UPDATE software_instances SET actual_state='STOPPED' WHERE id=?", i["id"]); err != nil {
		t.Fatal(err)
	}
	count, err := softwareDockerEngineDependencies(s.DB, host, "engine")
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = s.DB.Exec("UPDATE software_instances SET management_mode='RELEASED' WHERE id=?", i["id"]); err != nil {
		t.Fatal(err)
	}
	count, err = softwareDockerEngineDependencies(s.DB, host, "engine")
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
