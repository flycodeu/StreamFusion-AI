package controller

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type managementFixture struct {
	mu             sync.Mutex
	results        map[string]deploy.Result
	calls          atomic.Int32
	noAck          atomic.Bool
	unknown        atomic.Bool
	unapplied      atomic.Bool
	noPortIdentity atomic.Bool
	checkBlocked   atomic.Bool
}

// Transport fixtures validate controller plans and durable state only. They do
// not install software, write configuration files or run target commands.
func readyManagementFixture(t *testing.T) (*Store, *browser, string, commandTemplate, *managementFixture) {
	t.Helper()
	f := &managementFixture{results: map[string]deploy.Result{}}
	var store *Store
	extension := func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { b, _ := json.Marshal(v); return b, true }
		if req.Action == "CAPABILITIES" {
			return encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "platform": "linux/amd64", "dockerEngineAvailable": true, "softwareLifecycleVersion": deploy.SoftwareLifecycleVersion, "runtimePolicyVersion": 1, "softwareManagementVersion": deploy.SoftwareManagementVersion, "serviceInstallVersion": serviceinstall.CapabilityVersion, "nativeInstallVersion": 1, "serviceCatalogHash": serviceinstall.CatalogHash(), "commandRecipeVersion": deploy.CommandRecipeVersion, "commandTrustFingerprint": deploy.CommandPublicKeyFingerprint(store.commandPublicKey())})
		}
		if req.Action == "SOFTWARE_INSTALL_LOGS" {
			return encode(map[string]any{"text": "$ fixture command\nverified\n", "source": "COMMAND_LOG"})
		}
		if req.Management == nil && req.Software != nil {
			f.mu.Lock()
			defer f.mu.Unlock()
			if newSoftwareLifecycleAction(req.Action) {
				state := "RUNNING"
				if req.Action == "SOFTWARE_PAUSE" {
					state = "PAUSED"
				}
				result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_VERIFIED", ObservedAt: Now(), Software: &deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: state, ObservedAt: Now()}, Lifecycle: &deploy.SoftwareLifecycleEvidence{Action: req.Action, Before: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), After: Now(), ObservedAt: Now()}}
				result.RuntimePolicy = runtimePolicyReceiptFixture(req)
				result.Software.RuntimePolicy = result.RuntimePolicy
				f.results[req.JobID] = result
				return encode(result)
			}
			if result, exists := f.results[req.JobID]; exists && (req.Action == "STATUS" || req.Action == "ACK") {
				result.Ack = req.Action == "ACK" && !f.noAck.Load() && req.ResultHash == deploy.Digest(result)
				return encode(result)
			}
		}
		if req.Management == nil {
			return nil, false
		}
		if req.CommandSignature == "" {
			t.Error("management request was not signed")
		}
		m := req.Management
		if req.Action == "SOFTWARE_OPERATION_CHECK" {
			resources, err := m.ResolveResources(req.Software.Locator)
			if err != nil {
				t.Error(err)
			}
			for index := range resources {
				resources[index].IdentityHash = deploy.Digest("fixture-before:" + resources[index].Value)
				if resources[index].Type == "PORT" && f.noPortIdentity.Load() {
					resources[index].IdentityHash = ""
				}
			}
			return encode(serviceinstall.ManagementPreflight{Ready: !f.checkBlocked.Load(), Checks: []serviceinstall.Check{{Key: "fixture", Status: "PASS"}}, Resources: resources, ResourceHash: deploy.Digest(resources)})
		}
		if req.Action == "PROGRESS" {
			return encode(deploy.Result{Phase: "MANAGEMENT_VERIFY"})
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if req.Action == "SOFTWARE_OPERATION" {
			f.calls.Add(1)
			op, _ := m.Definition.ManagementOperation(m.OperationKey)
			resources, _ := m.ResolveResources(req.Software.Locator)
			for index := range resources {
				resources[index].IdentityHash = deploy.Digest("fixture-after:" + resources[index].Value)
			}
			if op.ReadOnly {
				state, _ := store.readSoftwareManagement(m.SoftwareID)
				resources = state.Resources
			}
			fact := &deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: "RUNNING", ObservedAt: Now()}
			evidence := &serviceinstall.ManagementEvidence{SoftwareID: m.SoftwareID, OperationKey: m.OperationKey, DefinitionHash: deploy.Digest(m.Definition), ParametersHash: m.ParameterHash(), ResourceHash: m.ResourceHash, ReadOnly: op.ReadOnly, Output: "fixture operation completed", Resources: resources, ObservedAt: Now()}
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_OPERATION_COMPLETED", Software: fact, Management: evidence, ObservedAt: Now()}
			if f.unapplied.Load() {
				result.Status, result.Phase, result.ErrorCode = "FAILED", "OPERATION_NOT_APPLIED", "MANAGEMENT_RESOURCES_CHANGED"
				result.Management, result.Software = nil, nil
			}
			f.results[req.JobID] = result
			if f.unknown.Load() {
				result.Status, result.Management = "UNKNOWN", nil
			}
			return encode(result)
		}
		result := f.results[req.JobID]
		if req.Action == "ACK" && !f.noAck.Load() && req.ResultHash == deploy.Digest(result) {
			result.Ack = true
		}
		return encode(result)
	}
	// Store initialization precedes every request handled by the SSH fixture.
	s, b := setup(t)
	store = s
	b.login()
	host, _ := installSSHFixture(t, s, b, extension)
	plan := nginxPlan(t, b, host, 18191)
	receipt := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "management-fixture-install"}, 202))
	job, id := receipt["jobId"].(string), receipt["softwareInstanceId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	i, _ := s.readSoftware(id)
	if !managementInstance(i) {
		t.Fatal("fixture installation not confirmed")
	}
	template, err := scanCommandTemplate(s.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE recipe_key=?", commandTemplateKey(i.Installation.Definition.Key, i.Installation.Definition.Version)))
	if err != nil {
		t.Fatal(err)
	}
	d := *template.Published
	d.Management = &serviceinstall.ManagementDefinition{Parameters: []serviceinstall.ParameterDefinition{{Key: "VALUE", Label: "配置值", Type: "TEXT", Default: "initial"}, {Key: "TOKEN", Label: "管理凭据", Type: "CREDENTIAL"}}, Resources: []serviceinstall.ResourceDefinition{{Key: "config", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/app.conf"}}, Operations: []serviceinstall.ManagementOperation{{Key: "apply", Name: "应用配置", Parameters: []serviceinstall.ParameterDefinition{{Key: "ONCE", Label: "单次输入", Type: "TEXT"}}, Steps: []serviceinstall.CommandStep{{Name: "应用", Script: `printf '%s' "$OPS_PARAM_VALUE"`, RunAs: "ROOT", TimeoutSeconds: 5}}, VerifySteps: []serviceinstall.CommandStep{{Name: "核验", Script: "true", RunAs: "ROOT", TimeoutSeconds: 5}}, ResultFormat: "TEXT"}}}
	template.Revision++
	d.Commands.Revision = int64(template.Revision)
	template.Published = &d
	if err := d.ValidateCommands(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE command_templates SET published_json=?,draft_json=?,revision=?,version=version+1 WHERE id=?", jsonText(d), jsonText(d), template.Revision, template.ID); err != nil {
		t.Fatal(err)
	}
	return s, b, id, template, f
}

func managementPlan(t *testing.T, s *Store, b *browser, id string, template commandTemplate, refs map[string]serviceinstall.CredentialReference) map[string]any {
	t.Helper()
	i, _ := s.readSoftware(id)
	state, _ := s.readSoftwareManagement(id)
	return dataOf(t, b.call("POST", "/software-instances/"+id+"/operation-plans", map[string]any{"expectedVersion": i.Version, "expectedManagementVersion": state.Version, "templateId": template.ID, "templateRevision": template.Revision, "operationKey": "apply", "parameters": map[string]string{"VALUE": "saved"}, "credentialRefs": refs, "operationParameters": map[string]string{"ONCE": "one-request-only"}}, 200))
}

func TestSoftwareManagementPlanReceiptHistoryAndExplicitTemplateAdoption(t *testing.T) {
	s, b, id, template, fixture := readyManagementFixture(t)
	before, _ := s.readSoftware(id)
	installation := jsonText(before.Installation)
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "management-token", "kind": "TOKEN", "secret": "Management-Secret-Only-Once"}, 201))
	refs := map[string]serviceinstall.CredentialReference{"TOKEN": {ID: credential["id"].(string), Revision: credential["revision"].(string)}}
	view := dataOf(t, b.call("GET", "/software-instances/"+id+"/management", nil, 200))
	if view["stateVersion"] != "0" || view["templateId"] != template.ID {
		t.Fatal(view)
	}
	plan := managementPlan(t, s, b, id, template, refs)
	endpoint := "/software-operation-plans/" + plan["id"].(string) + "/submit"
	receipt := dataOf(t, b.call("POST", endpoint, map[string]string{"idempotencyKey": "management-once"}, 202))
	job := receipt["jobId"].(string)
	if replay := dataOf(t, b.call("POST", endpoint, map[string]string{"idempotencyKey": "management-once"}, 200)); replay["jobId"] != job {
		t.Fatal("duplicate created another operation", replay)
	}
	fixture.noAck.Store(true)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	if strings.Contains(raw, "Management-Secret-Only-Once") {
		t.Fatal("secret persisted in job input")
	}
	b.h.processSoftwareOperation(context.Background(), job, raw)
	state, err := s.readSoftwareManagement(id)
	after, _ := s.readSoftware(id)
	if err != nil || state.Version != "1" || state.Parameters["VALUE"] != "saved" || state.Parameters["ONCE"] != "" || state.CredentialRefs["TOKEN"] != refs["TOKEN"] || jsonText(after.Installation) != installation || after.BindingRevision != before.BindingRevision {
		t.Fatal("management state or installation history changed incorrectly", state, err)
	}
	var status string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "SUCCEEDED" || locks != 2 || fixture.calls.Load() != 1 {
		t.Fatal(status, locks, fixture.calls.Load())
	}
	b.call("POST", "/credentials/"+refs["TOKEN"].ID+"/revisions/1/revoke", map[string]any{}, 409)
	fixture.noAck.Store(false)
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if locks != 0 || fixture.calls.Load() != 1 {
		t.Fatal("ACK replayed the operation", locks, fixture.calls.Load())
	}
	newTemplate := template
	var next serviceinstall.Definition
	if err := deploy.StrictJSON([]byte(jsonText(template.Published)), &next); err != nil {
		t.Fatal(err)
	}
	newTemplate.Revision++
	next.Management.Operations[0].Name = "新版配置操作"
	next.Commands.Revision = int64(newTemplate.Revision)
	newTemplate.Published = &next
	if _, err := s.DB.Exec("UPDATE command_templates SET published_json=?,draft_json=?,revision=? WHERE id=?", jsonText(next), jsonText(next), newTemplate.Revision, newTemplate.ID); err != nil {
		t.Fatal(err)
	}
	view = dataOf(t, b.call("GET", "/software-instances/"+id+"/management", nil, 200))
	if view["templateRevision"] != float64(template.Revision) || view["availableTemplate"] == nil || view["lastJobId"] != job {
		t.Fatal("publication replaced current management binding", view)
	}
	oldPlan := managementPlan(t, s, b, id, template, refs)
	if oldPlan["operation"].(map[string]any)["name"] != "应用配置" {
		t.Fatal("old bound revision stopped working")
	}
	newPlan := managementPlan(t, s, b, id, newTemplate, refs)
	if newPlan["operation"].(map[string]any)["name"] != "新版配置操作" {
		t.Fatal("explicit adoption did not select new definition")
	}
	state, _ = s.readSoftwareManagement(id)
	if state.TemplateRevision != template.Revision {
		t.Fatal("a draft already changed current management")
	}
}

func TestSoftwareManagementUnknownKeepsOriginalJobAndLocks(t *testing.T) {
	s, b, id, template, fixture := readyManagementFixture(t)
	plan := managementPlan(t, s, b, id, template, nil)
	endpoint := "/software-operation-plans/" + plan["id"].(string) + "/submit"
	receipt := dataOf(t, b.call("POST", endpoint, map[string]string{"idempotencyKey": "unknown-management"}, 202))
	job := receipt["jobId"].(string)
	fixture.unknown.Store(true)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftwareOperation(context.Background(), job, raw)
	var status string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	state, _ := s.readSoftwareManagement(id)
	if status != "UNKNOWN" || locks != 2 || state.Version != "0" {
		t.Fatal(status, locks, state.Version)
	}
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if locks != 0 || fixture.calls.Load() != 1 {
		t.Fatal("reconciliation repeated side effects", locks, fixture.calls.Load())
	}
}

func TestSoftwareManagementRejectsDifferentTemplateAndConflictingResources(t *testing.T) {
	s, b, id, template, _ := readyManagementFixture(t)
	i, _ := s.readSoftware(id)
	in := map[string]any{"expectedVersion": i.Version, "expectedManagementVersion": "0", "templateId": uuid.NewString(), "templateRevision": template.Revision, "operationKey": "apply"}
	b.call("POST", "/software-instances/"+id+"/operation-plans", in, 409)
	in["templateId"] = template.ID
	in["parameters"] = map[string]string{"UNDECLARED": "hidden"}
	b.call("POST", "/software-instances/"+id+"/operation-plans", in, 400)
	in["parameters"] = map[string]string{}
	in["credentialRefs"] = map[string]serviceinstall.CredentialReference{"TOKEN": {ID: uuid.NewString(), Revision: "1"}}
	b.call("POST", "/software-instances/"+id+"/operation-plans", in, 409)
	other := uuid.NewString()
	if _, err := s.DB.Exec(`INSERT INTO software_instances(id,name,host_id,software_type,runtime_kind,locator,created_at,updated_at) VALUES(?,'other',?,'CUSTOM','SYSTEMD','other.service',?,?)`, other, i.HostID, Now(), Now()); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = reserveManagementResources(tx, i.HostID, id, "", []serviceinstall.ResolvedResource{{Type: "UNIT", Value: "other.service"}}); err == nil {
		t.Fatal("another instance's systemd unit was not protected")
	}
}

func TestSoftwareLifecycleControlsRequireEvidenceAndKeepRealLogs(t *testing.T) {
	s, b, id, _, fixture := readyManagementFixture(t)
	i, _ := s.readSoftware(id)
	installation, binding := jsonText(i.Installation), i.BindingRevision
	for _, action := range []string{"RESTART", "PAUSE", "UNPAUSE"} {
		i, _ = s.readSoftware(id)
		in := map[string]string{"expectedVersion": i.Version, "action": action, "idempotencyKey": "lifecycle-" + action}
		endpoint := "/software-instances/" + id + "/actions"
		receipt := dataOf(t, b.call("POST", endpoint, in, 202))
		job := receipt["jobId"].(string)
		if replay := dataOf(t, b.call("POST", endpoint, in, 200)); replay["jobId"] != job {
			t.Fatal("control replay created another job")
		}
		var raw string
		s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
		b.h.processSoftware(context.Background(), job, raw)
		var request deploy.Request
		json.Unmarshal([]byte(raw), &request)
		if request.ExecutionLogVersion != 1 {
			t.Fatal("new control job lacks real-log marker")
		}
		fixture.mu.Lock()
		result := fixture.results[job]
		fixture.mu.Unlock()
		bad := result
		bad.Lifecycle = nil
		if b.h.commitSoftwareResult(job, request, bad) == nil {
			t.Fatal("lifecycle success accepted without action evidence")
		}
		if action == "RESTART" {
			bad = result
			bad.RuntimePolicy = nil
			if b.h.commitSoftwareResult(job, request, bad) == nil {
				t.Fatal("restart accepted without restored runtime policy")
			}
			bad = result
			copyEvidence := *result.Lifecycle
			copyEvidence.After = copyEvidence.Before
			bad.Lifecycle = &copyEvidence
			if b.h.commitSoftwareResult(job, request, bad) == nil {
				t.Fatal("restart accepted unchanged invocation")
			}
		}
		logs := dataOf(t, b.call("GET", "/jobs/"+job+"/logs", nil, 200))
		if logs["source"] == "JOB_RECEIPT" || !strings.Contains(logs["text"].(string), "$ fixture command") {
			t.Fatal("control output was replaced by receipt", logs)
		}
		i, _ = s.readSoftware(id)
		if jsonText(i.Installation) != installation || i.BindingRevision != binding {
			t.Fatal("control rewrote installation snapshot")
		}
	}
	i, _ = s.readSoftware(id)
	b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": i.Version, "action": "RELOAD", "idempotencyKey": "docker-cannot-reload"}, 409)
}

func TestInstallReservationsRespectPendingManagementPorts(t *testing.T) {
	s, b, id, template, _ := readyManagementFixture(t)
	i, _ := s.readSoftware(id)
	d := *template.Published
	d.Management.Resources = append(d.Management.Resources, serviceinstall.ResourceDefinition{Key: "managed-listener", Type: "PORT", Value: "19443", BindAddress: "0.0.0.0", Protocol: "TCP"})
	template.Published = &d
	if _, err := s.DB.Exec("UPDATE command_templates SET published_json=?,draft_json=? WHERE id=?", jsonText(d), jsonText(d), template.ID); err != nil {
		t.Fatal(err)
	}
	managementPlan(t, s, b, id, template, nil)
	definition, _ := serviceinstall.Find("nginx", "1.30.6")
	response := b.call("POST", "/hosts/"+i.HostID+"/software-install-plans", map[string]any{"expectedHostVersion": "1", "recipeKey": definition.Key, "recipeVersion": definition.Version, "recipeRevision": definition.Revision, "config": serviceinstall.Config{Name: "conflicting-site", BindAddress: "127.0.0.1", HostPort: 19443, MemoryMiB: 256}, "source": map[string]string{"type": "REGISTRY"}}, 409)
	if !strings.Contains(response.Body.String(), "MANAGEMENT_RESOURCE_RESERVED") {
		t.Fatal(response.Body.String())
	}
}

func TestSoftwareManagementUnappliedFailureKeepsStateAndCanAcknowledge(t *testing.T) {
	s, b, id, template, fixture := readyManagementFixture(t)
	plan := managementPlan(t, s, b, id, template, nil)
	receipt := dataOf(t, b.call("POST", "/software-operation-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "unapplied-management"}, 202))
	job := receipt["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	fixture.unapplied.Store(true)
	fixture.noAck.Store(true)
	b.h.processSoftwareOperation(context.Background(), job, raw)
	state, _ := s.readSoftwareManagement(id)
	var status string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "FAILED" || locks != 2 || state.Version != "0" {
		t.Fatal("unapplied failure changed management state or dropped ACK locks", status, locks, state.Version)
	}
	fixture.noAck.Store(false)
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if locks != 0 || fixture.calls.Load() != 1 {
		t.Fatal("failure acknowledgement replayed operation", locks, fixture.calls.Load())
	}
}

func TestManagementResourceSnapshotRetainsOldClaimsAndRequiresVerifiedRelease(t *testing.T) {
	_, _, id, template, _ := readyManagementFixture(t)
	d := *template.Published
	op := &d.Management.Operations[0]
	old := serviceinstall.ResolvedResource{Key: "old", Type: "PORT", Value: "19444", Protocol: "TCP", BindAddress: "127.0.0.1", IdentityHash: deploy.Digest("old")}
	p := storedSoftwareOperationPlan{PreviousResources: []serviceinstall.ResolvedResource{old}, Binding: deploy.SoftwareRequest{Locator: "fixture"}, softwareOperationPlan: softwareOperationPlan{Management: serviceinstall.ManagementRequest{Definition: d, SoftwareID: id, BindingRevision: 1, OperationKey: op.Key, Root: "/opt/streamfusion-ops-target/software/" + id}, Operation: *op}}
	declared, err := p.Management.ResolveResources(p.Binding.Locator)
	if err != nil {
		t.Fatal(err)
	}
	declared[0].IdentityHash = deploy.Digest("new")
	if err := verifyManagementResourceSnapshot(p, append([]serviceinstall.ResolvedResource{old}, declared...)); err != nil {
		t.Fatal(err)
	}
	if err := verifyManagementResourceSnapshot(p, declared); err == nil {
		t.Fatal("a later operation dropped an earlier claim")
	}
	op.Resources = []serviceinstall.ResourceDefinition{{Key: "release", Type: old.Type, Value: old.Value, Protocol: old.Protocol, BindAddress: old.BindAddress, Release: true}}
	p.Management.Definition, p.Operation = d, *op
	if err := verifyManagementResourceSnapshot(p, declared); err != nil {
		t.Fatal("explicit release retained old claim", err)
	}
	op.Resources, op.ReadOnly = nil, true
	p.Management.Definition, p.Operation = d, *op
	if err := verifyManagementResourceSnapshot(p, []serviceinstall.ResolvedResource{old}); err != nil {
		t.Fatal("read-only query acquired declared resources", err)
	}
}

func TestManagementWritesProtectBaseConfigurationAndReadQueriesAllowIt(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		t.Run(map[bool]string{true: "read-only", false: "managed-write"}[readOnly], func(t *testing.T) {
			s, b, id, template, _ := readyManagementFixture(t)
			definition := *template.Published
			definition.Management.Operations[0].ReadOnly = readOnly
			template.Published = &definition
			if _, err := s.DB.Exec("UPDATE command_templates SET published_json=?,draft_json=? WHERE id=?", jsonText(definition), jsonText(definition), template.ID); err != nil {
				t.Fatal(err)
			}
			before, _ := s.readSoftware(id)
			next := before.Installation.Config
			next.HostPort = 19287
			oldPlan := dataOf(t, b.call("POST", "/software-instances/"+id+"/configuration-plans", map[string]any{"expectedVersion": before.Version, "config": next}, 200))
			plan := managementPlan(t, s, b, id, template, nil)
			receipt := dataOf(t, b.call("POST", "/software-operation-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "config-coordination"}, 202))
			var raw string
			s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", receipt["jobId"]).Scan(&raw)
			b.h.processSoftwareOperation(context.Background(), receipt["jobId"].(string), raw)
			current, _ := s.readSoftware(id)
			status := 409
			if readOnly {
				status = 200
			}
			response := b.call("POST", "/software-instances/"+id+"/configuration-plans", map[string]any{"expectedVersion": current.Version, "config": next}, status)
			if !readOnly {
				if !strings.Contains(response.Body.String(), "MANAGEMENT_CONFIGURATION_REQUIRED") {
					t.Fatal(response.Body.String())
				}
				b.call("POST", "/software-configuration-plans/"+oldPlan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "old-base-plan"}, 409)
			}
			if current.Installation.Config.HostPort != before.Installation.Config.HostPort || current.BindingRevision != before.BindingRevision {
				t.Fatal("configuration guard changed the installed binding")
			}
		})
	}
}

func TestUninstalledManagementClaimsReleaseWithoutDeletingHistory(t *testing.T) {
	s, b, id, template, _ := readyManagementFixture(t)
	plan := managementPlan(t, s, b, id, template, nil)
	receipt := dataOf(t, b.call("POST", "/software-operation-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "release-managed-claims"}, 202))
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", receipt["jobId"]).Scan(&raw)
	b.h.processSoftwareOperation(context.Background(), receipt["jobId"].(string), raw)
	i, _ := s.readSoftware(id)
	state, _ := s.readSoftwareManagement(id)
	for _, released := range []bool{false, true} {
		if released {
			// These are the same durable flags written after verified uninstall.
			if _, err := s.DB.Exec("UPDATE software_instances SET management_mode='RELEASED',installation_status='UNINSTALLED' WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := s.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		err = reserveManagementResources(tx, i.HostID, uuid.NewString(), "", state.Resources)
		tx.Rollback()
		if released && err != nil || !released && err == nil {
			t.Fatal("resource ownership disagrees with installation lifecycle", released, err)
		}
	}
	history, err := s.readSoftwareManagement(id)
	if err != nil || jsonText(history) != jsonText(state) {
		t.Fatal("releasing claims deleted management history", err)
	}
}
