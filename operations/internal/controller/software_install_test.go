package controller

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type installFixture struct {
	InstallCount         atomic.Int32
	ConfigurationCount   atomic.Int32
	ConfigurationFailure atomic.Bool
	NoAck                atomic.Bool
	Unknown              atomic.Bool
	HealthFailure        atomic.Bool
	RejectPreflight      atomic.Bool
	OldExecutor          atomic.Bool
	NoDocker             atomic.Bool
	mu                   sync.Mutex
	Results              map[string]deploy.Result
}

func installSSHFixture(t *testing.T, s *Store, b *browser, extensions ...func(deploy.Request) ([]byte, bool)) (string, *installFixture) {
	t.Helper()
	fixture := &installFixture{Results: map[string]deploy.Result{}}
	handler := func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { raw, _ := json.Marshal(v); return raw, true }
		if req.Action == "CAPABILITIES" {
			version := serviceinstall.CapabilityVersion
			if fixture.OldExecutor.Load() {
				version = 0
			}
			return encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "platform": "linux/amd64", "dockerAvailable": false, "dockerEngineAvailable": !fixture.NoDocker.Load(), "softwareLifecycleVersion": 1, "serviceInstallVersion": version, "serviceCatalogHash": serviceinstall.CatalogHash()})
		}
		if req.Reconfiguration != nil {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			change := req.Reconfiguration
			frozen := change.Previous
			frozen.Config = change.Config
			if req.Action == "SOFTWARE_CONFIGURE_CHECK" {
				root := serviceinstall.InstallRoot("/opt/plugin-test", frozen.SoftwareID, frozen.Config)
				return encode(installPreflightFixture(frozen, root, true))
			}
			if req.Action == "PROGRESS" {
				return encode(deploy.Result{Phase: "CONFIGURATION_VERIFY"})
			}
			if req.Action == "SOFTWARE_INSTALL_LOGS" {
				return encode(map[string]any{"text": "configuration verified", "limited": false})
			}
			if req.Action == "SOFTWARE_CONFIGURE" {
				fixture.ConfigurationCount.Add(1)
				frozen.BindingRevision++
				result := installReceiptFixture(req, frozen, "c")
				if fixture.ConfigurationFailure.Load() {
					result = installReceiptFixture(req, change.Previous, "b")
					result.Status = "FAILED"
					result.ErrorCode = "CONFIGURATION_ROLLED_BACK"
				}
				fixture.Results[req.JobID] = result
				if fixture.Unknown.Load() {
					result.Status = "UNKNOWN"
				}
				return encode(result)
			}
			result := fixture.Results[req.JobID]
			if req.Action == "ACK" && !fixture.NoAck.Load() && req.ResultHash == deploy.Digest(result) {
				result.Ack = true
			}
			return encode(result)
		}
		if req.Software != nil {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if req.Action == "SOFTWARE_OBSERVE" || req.Action == "SOFTWARE_LOGS" {
				state := "RUNNING"
				if req.Software.Kind == "TOOL" {
					state = "INSTALLED"
				}
				return encode(deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: state, Version: "observed version", ObservedAt: Now(), Log: "bounded target log"})
			}
			state := "STOPPED"
			if req.Action == "SOFTWARE_START" {
				state = "RUNNING"
			}
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_VERIFIED", ObservedAt: Now(), Software: &deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: state, ObservedAt: Now()}}
			if req.Action == "ACK" {
				result = fixture.Results[req.JobID]
				if !fixture.NoAck.Load() && req.ResultHash == deploy.Digest(result) {
					result.Ack = true
				}
			} else {
				fixture.Results[req.JobID] = result
			}
			return encode(result)
		}
		if req.Installation == nil {
			return nil, false
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		install := req.Installation
		root := serviceinstall.InstallRoot("/opt/plugin-test", install.SoftwareID, install.Config)
		switch req.Action {
		case "SOFTWARE_INSTALL_CHECK":
			return encode(installPreflightFixture(*install, root, !fixture.RejectPreflight.Load()))
		case "SOFTWARE_INSTALL_LOGS":
			return encode(map[string]any{"text": "verified install step\n", "limited": false, "source": "TARGET_JOB"})
		case "PROGRESS":
			return encode(deploy.Result{Phase: "INSTALL_HEALTH"})
		case "SOFTWARE_INSTALL":
			if req.SoftwareOperation != "SOFTWARE_INSTALL" {
				t.Error("installation operation missing")
			}
			fixture.InstallCount.Add(1)
			result := installReceiptFixture(req, *install, "b")
			if fixture.HealthFailure.Load() {
				result.Status, result.ErrorCode, result.Installation.Health = "FAILED", "INSTALL_HEALTH_FAILED", "FAILED"
			}
			fixture.Results[req.JobID] = result
			if fixture.Unknown.Load() {
				result.Status = "UNKNOWN"
			}
			return encode(result)
		case "STATUS", "ACK":
			result := fixture.Results[req.JobID]
			if req.Action == "ACK" && !fixture.NoAck.Load() && req.ResultHash == deploy.Digest(result) {
				result.Ack = true
			}
			return encode(result)
		}
		return nil, false
	}
	id := pluginSSHFixture(t, s, b, true, append(extensions, handler)...)
	return id, fixture
}

// This preflight is controlled protocol evidence. It reports the complete wire
// shape but does not inspect sockets, install packages, or run a real service.
func installPreflightFixture(i serviceinstall.Request, root string, ready bool) serviceinstall.Preflight {
	preflight := serviceinstall.Preflight{Ready: ready, Root: root, DataDirectory: root + "/data", ConfigDirectory: root + "/config", Checks: []serviceinstall.Check{{Key: "port", Status: "PASS", Message: "protocol fixture: ports accepted"}}, PortMappings: serviceinstall.PortMappings(i.Definition, i.Config)}
	if i.Config.Mode() == "NATIVE" {
		preflight.BinaryDirectory = path.Dir(root + "/" + serviceinstall.NativeBinaryRelative(i.Definition))
		preflight.LogsDirectory = root + "/logs"
	}
	return preflight
}

func nginxPlan(t *testing.T, b *browser, host string, port int) map[string]any {
	t.Helper()
	d, _ := serviceinstall.Find("nginx", "1.30.6")
	return dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "site", BindAddress: "127.0.0.1", HostPort: port, MemoryMiB: 256}, "source": map[string]string{"type": "REGISTRY"}}, 200))
}

func TestServiceInstallPlanSubmitIdentityAckAndReplay(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := installSSHFixture(t, s, b)
	var catalog struct{ Data []serviceinstall.Definition }
	if err := json.Unmarshal(b.call("GET", "/software-recipes", nil, 200).Body.Bytes(), &catalog); err != nil || len(catalog.Data) != len(serviceinstall.Catalog()) {
		t.Fatal(catalog, err)
	}
	p := nginxPlan(t, b, host, 18088)
	planID := p["id"].(string)
	if _, exists := p["softwareId"]; exists {
		t.Fatal("private pending identity leaked", p)
	}
	if p["paths"].(map[string]any)["data"] == "" || p["portMappings"].([]any)[0].(map[string]any)["hostPort"] != float64(18088) {
		t.Fatal(p)
	}
	response := dataOf(t, b.call("POST", "/software-install-plans/"+planID+"/submit", map[string]string{"idempotencyKey": "install-once-1"}, 202))
	job, id := response["jobId"].(string), response["softwareInstanceId"].(string)
	i, err := s.readSoftware(id)
	if err != nil || i.InstallationStatus != "INSTALLING" || i.ManagementMode != "OBSERVE" {
		t.Fatal(i, err)
	}
	b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 409)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	f.NoAck.Store(true)
	b.h.processSoftware(context.Background(), job, raw)
	i, err = s.readSoftware(id)
	if err != nil || i.InstallationStatus != "READY" || i.ManagementMode != "MANAGED" || i.Locator != strings.Repeat("b", 64) || softwareRequest(i).Ownership == nil {
		t.Fatal(i, err)
	}
	var locks int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if locks != 2 {
		t.Fatal("ack lock lost", locks)
	}
	if dataOf(t, b.call("POST", "/software-install-plans/"+planID+"/submit", map[string]string{"idempotencyKey": "install-once-1"}, 200))["jobId"] != job {
		t.Fatal("duplicate changed job")
	}
	f.NoAck.Store(false)
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if locks != 0 || f.InstallCount.Load() != 1 {
		t.Fatal(locks, f.InstallCount.Load())
	}
	if !strings.Contains(b.call("GET", "/jobs/"+job+"/logs", nil, 200).Body.String(), "verified install step") {
		t.Fatal("missing target logs")
	}
	f.OldExecutor.Store(true)
	if dataOf(t, b.call("POST", "/software-install-plans/"+planID+"/submit", map[string]string{"idempotencyKey": "install-once-1"}, 200))["jobId"] != job {
		t.Fatal("replay depended on target")
	}
}

func TestServiceInstallUnknownHealthFailureAndCancel(t *testing.T) {
	for _, mode := range []string{"unknown", "failed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			host, f := installSSHFixture(t, s, b)
			p := nginxPlan(t, b, host, 18089)
			response := dataOf(t, b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "install-" + mode + "-1"}, 202))
			job, id := response["jobId"].(string), response["softwareInstanceId"].(string)
			if mode == "cancel" {
				b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 200)
				i, _ := s.readSoftware(id)
				if i.InstallationStatus != "CANCELLED" || f.InstallCount.Load() != 0 {
					t.Fatal(i)
				}
				return
			}
			f.Unknown.Store(mode == "unknown")
			f.HealthFailure.Store(mode == "failed")
			var raw string
			s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
			b.h.processSoftware(context.Background(), job, raw)
			i, _ := s.readSoftware(id)
			if mode == "failed" {
				if i.InstallationStatus != "FAILED" || i.ManagementMode != "MANAGED" || softwareRequest(i).Ownership == nil {
					t.Fatal("failed service lost concrete identity", i)
				}
				stopped := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": i.Version, "action": "STOP", "idempotencyKey": "failed-service-stop"}, 202))
				var stopRaw string
				stopID := stopped["jobId"].(string)
				s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", stopID).Scan(&stopRaw)
				b.h.processSoftware(context.Background(), stopID, stopRaw)
				i, _ = s.readSoftware(id)
				if i.ActualState != "STOPPED" || i.InstallationStatus != "FAILED" {
					t.Fatal("failed service could not safely stop", i)
				}
				return
			}
			if i.InstallationStatus != "UNKNOWN" {
				t.Fatal(i)
			}
			b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 409)
			b.call("POST", "/jobs/"+job+"/recover", map[string]any{}, 200)
			i, _ = s.readSoftware(id)
			if i.InstallationStatus != "READY" || f.InstallCount.Load() != 1 {
				t.Fatal(i, f.InstallCount.Load())
			}
		})
	}
}

func TestInstallationCredentialReferencesAndSecretFreeSnapshots(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, _ := installSSHFixture(t, s, b)
	secret := "a-Strong-Redis-Password-123"
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "redis password", "kind": "PASSWORD", "secret": secret}, 201))
	d, _ := serviceinstall.Find("redis", "7.4.11")
	request := map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "cache", BindAddress: "127.0.0.1", HostPort: 16379, MemoryMiB: 256, CredentialID: credential["id"].(string), CredentialRevision: credential["revision"].(string)}, "source": map[string]string{"type": "REGISTRY"}}
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", request, 200))
	planID := plan["id"].(string)
	if strings.Contains(jsonText(plan), secret) {
		t.Fatal("secret leaked to response")
	}
	b.call("POST", "/credentials/"+credential["id"].(string)+"/revisions/1/revoke", map[string]any{}, 409)
	accepted := dataOf(t, b.call("POST", "/software-install-plans/"+planID+"/submit", map[string]string{"idempotencyKey": "secret-free-install"}, 202))
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", accepted["jobId"]).Scan(&raw)
	if strings.Contains(raw, secret) || strings.Contains(raw, `"secrets"`) {
		t.Fatal("secret entered durable intent", raw)
	}
	b.call("POST", "/jobs/"+accepted["jobId"].(string)+"/cancel", map[string]any{}, 200)
	b.call("POST", "/credentials/"+credential["id"].(string)+"/revisions/1/revoke", map[string]any{}, 200)
}

func TestInstallationMaterialQuotaAndLiveReferences(t *testing.T) {
	s, b := setup(t)
	b.login()
	for n := 0; n < 8; n++ {
		materialCreate(t, b, serviceinstall.ArchiveLimit, "quota-entry-"+string(rune('a'+n)))
	}
	d, _ := serviceinstall.Find("nginx", "1.30.6")
	b.call("POST", "/installation-materials", map[string]any{"recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "name": "too much", "sha256": hashToken("more"), "byteSize": 1, "idempotencyKey": "quota-extra-1"}, 409)
	id := uuid.NewString()
	now := Now()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "material-ref", "kind": "LOCAL"}, 201))["id"].(string)
	var material string
	s.DB.QueryRow("SELECT id FROM installation_materials LIMIT 1").Scan(&material)
	_, err := s.DB.Exec("INSERT INTO software_install_plans(id,actor_id,target_id,material_id,snapshot_json,status,expires_at,created_at) VALUES(?,?,?,?,?,'SUBMITTED',?,?)", id, b.currentAdmin(), host, material, "{}", now, now)
	if err != nil {
		t.Fatal(err)
	}
	software := uuid.NewString()
	_, err = s.DB.Exec("INSERT INTO software_instances(id,name,host_id,software_type,runtime_kind,locator,management_mode,install_plan_id,installation_status,created_at,updated_at) VALUES(?,'failed real service',?,'NGINX','DOCKER',?,'MANAGED',?,'FAILED',?,?)", software, host, strings.Repeat("a", 64), id, now, now)
	if err != nil {
		t.Fatal(err)
	}
	b.call("DELETE", "/installation-materials/"+material, nil, 409)
	s.DB.Exec("UPDATE software_instances SET management_mode='RELEASED' WHERE id=?", software)
	b.call("DELETE", "/installation-materials/"+material, nil, 200)
}

func TestServiceInstallPlanRejectsStaleHostExecutorAndPort(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := installSSHFixture(t, s, b)
	p := nginxPlan(t, b, host, 18090)
	d, _ := serviceinstall.Find("nginx", "1.30.6")
	in := map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "other", BindAddress: "127.0.0.1", HostPort: 18090, MemoryMiB: 256}, "source": map[string]string{"type": "REGISTRY"}}
	newPlan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", in, 200))
	if newPlan["id"] == p["id"] {
		t.Fatal("replacement did not freeze new plan")
	}
	if !strings.Contains(b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "superseded-plan"}, 409).Body.String(), "INSTALL_PLAN_STALE") {
		t.Fatal("superseded plan still usable")
	}
	p = newPlan
	f.RejectPreflight.Store(true)
	b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "changed-port-1"}, 409)
	f.RejectPreflight.Store(false)
	f.OldExecutor.Store(true)
	in["config"] = serviceinstall.Config{Name: "other", BindAddress: "127.0.0.1", HostPort: 18091, MemoryMiB: 256}
	if !strings.Contains(b.call("POST", "/hosts/"+host+"/software-install-plans", in, 409).Body.String(), "EXECUTOR_UPGRADE_REQUIRED") {
		t.Fatal("old executor not blocked")
	}
	f.OldExecutor.Store(false)
	b.call("PATCH", "/hosts/"+host+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": "1"}, 200)
	b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "stale-host-1"}, 409)
	var jobs int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='SOFTWARE_INSTALL'").Scan(&jobs)
	if jobs != 0 {
		t.Fatal("failed preflight queued side effect", jobs)
	}
}

type installDeadlineRecorder struct {
	*httptest.ResponseRecorder
	DeadlineError error
}

func (w installDeadlineRecorder) SetReadDeadline(time.Time) error  { return w.DeadlineError }
func (w installDeadlineRecorder) SetWriteDeadline(time.Time) error { return w.DeadlineError }
func installBytes(b *browser, method, path string, body []byte, want int, deadlineErr error) *httptest.ResponseRecorder {
	b.t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:19090"+api+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Origin", "http://127.0.0.1:19090")
	r.Header.Set("X-CSRF-Token", b.csrf)
	r.AddCookie(b.cookie)
	w := httptest.NewRecorder()
	b.h.ServeHTTP(installDeadlineRecorder{w, deadlineErr}, r)
	if w.Code != want {
		b.t.Fatalf("%s %s status=%d wanted=%d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}

func materialCreate(t *testing.T, b *browser, size int64, key string) string {
	t.Helper()
	d, _ := serviceinstall.Find("nginx", "1.30.6")
	return dataOf(t, b.call("POST", "/installation-materials", map[string]any{"recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "name": "nginx.tar", "sha256": hashToken("bad archive"), "byteSize": size, "idempotencyKey": key}, 201))["id"].(string)
}

func TestInstallationMaterialBoundedUploadValidationMissingAndDelete(t *testing.T) {
	s, b := setup(t)
	b.login()
	id := materialCreate(t, b, int64(len("bad archive")), "material-upload-1")
	installBytes(b, "PUT", "/installation-materials/"+id+"/content", []byte("bad archive"), 500, http.ErrNotSupported)
	installBytes(b, "PUT", "/installation-materials/"+id+"/content", []byte("short"), 400, nil)
	installBytes(b, "PUT", "/installation-materials/"+id+"/content", []byte("bad archive"), 200, nil)
	installBytes(b, "POST", "/installation-materials/"+id+"/confirm", nil, 400, nil)
	m, _ := s.readInstallationMaterial(id)
	if m.Status != "FAILED" || m.Available {
		t.Fatal(m)
	}
	installBytes(b, "PUT", "/installation-materials/"+id+"/content", []byte("bad archive"), 200, nil)
	if _, err := s.DB.Exec("UPDATE installation_materials SET status='AVAILABLE' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	m, _ = s.readInstallationMaterial(id)
	if m.Status != "MISSING" || m.Available {
		t.Fatal(m)
	}
	installBytes(b, "PUT", "/installation-materials/"+id+"/content", []byte("bad archive"), 409, nil)
	b.call("DELETE", "/installation-materials/"+id, nil, 200)
	if _, err := os.Stat(s.installationMaterialPath(id, true)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial not cleaned", err)
	}
	idA := materialCreate(t, b, 1, "material-busy-a")
	idB := materialCreate(t, b, 1, "material-busy-b")
	idC := materialCreate(t, b, 1, "material-busy-c")
	s.DB.Exec("UPDATE installation_materials SET status='UPLOADING' WHERE id IN (?,?)", idA, idB)
	installBytes(b, "PUT", "/installation-materials/"+idC+"/content", []byte("x"), 429, nil)
	s.recoverInstallationMaterials()
	m, _ = s.readInstallationMaterial(idA)
	if m.Status != "FAILED" || m.ErrorCode != "UPLOAD_INTERRUPTED" {
		t.Fatal(m)
	}
	b.call("GET", "/installation-materials?recipeKey=nginx", nil, 200)
}

func TestSchemaNineUpgradePreservesSoftwareJobsLocksAndObservation(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for index, source := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV8, schemaV9} {
		if _, err = db.Exec(source); err != nil {
			t.Fatal(index, err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", index+1, hashToken(source), Version, Now()); err != nil {
			t.Fatal(err)
		}
	}
	controller, admin, host, job, software := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'old',?)", []any{controller, Now()}},
		{"INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,'admin',?,?)", []any{admin, []byte("old-hash"), Now()}},
		{"INSERT INTO targets(id,name,install_root,created_at) VALUES(?,'old','/opt/old-target',?)", []any{host, Now()}},
		{"INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_STOP',?,'old-key','hash','UNKNOWN','STOP','{}','old-trace',?,?)", []any{job, admin, host, Now(), Now()}},
		{"INSERT INTO operation_locks VALUES(?,?)", []any{"target:" + host, job}},
		{"INSERT INTO software_instances(id,name,host_id,software_type,runtime_kind,locator,management_mode,identity_hash,binding_revision,observation_json,observation_revision,created_at,updated_at) VALUES(?,'redis',?,'REDIS','SYSTEMD','redis.service','MANAGED','identity',4,?,7,?,?)", []any{software, host, `{"state":"RUNNING","version":"7.4"}`, Now(), Now()}},
		{"INSERT INTO host_observation_snapshots(host_id,identity,confirmed_json,attempt_json,revision) VALUES(?,'old-host','{}','{}',9)", []any{host}},
	} {
		if _, err = db.Exec(q.query, q.args...); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var status, lock, raw string
	var binding, observation int
	if err = s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status); err != nil || status != "UNKNOWN" {
		t.Fatal(status, err)
	}
	if err = s.DB.QueryRow("SELECT job_id FROM operation_locks WHERE resource_key=?", "target:"+host).Scan(&lock); err != nil || lock != job {
		t.Fatal(lock, err)
	}
	if err = s.DB.QueryRow("SELECT binding_revision,observation_json,observation_revision FROM software_instances WHERE id=?", software).Scan(&binding, &raw, &observation); err != nil || binding != 4 || observation != 7 || !strings.Contains(raw, "RUNNING") {
		t.Fatal(binding, raw, observation, err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "before-schema-10-*.tar.gz"))
	if len(backups) != 1 || s.controllerID() != controller {
		t.Fatal(backups)
	}
	rows, err := s.DB.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("broken foreign keys")
	}
}
