package controller

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

func TestAdoptedSoftwareReadsRuntimePolicyThroughTargetExecutor(t *testing.T) {
	seen := false
	s, b, _, id, _ := readyNginxFixture(t, func(req deploy.Request) ([]byte, bool) {
		if req.Action != "SOFTWARE_OBSERVE" || req.Software == nil || req.Software.Ownership != nil {
			return nil, false
		}
		seen = true
		p := runtimepolicy.Policy{Restart: "unless-stopped", AutoStart: true, StopTimeoutSeconds: 10}
		state := &runtimepolicy.State{Configured: &p, Actual: &p, Editable: true, ObservedAt: Now()}
		result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, Status: "SUCCEEDED", Phase: "SOFTWARE_OBSERVED", Software: &deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: "RUNNING", ObservedAt: Now(), RuntimePolicy: state}}
		raw, _ := json.Marshal(result)
		return raw, true
	})
	if _, err := s.DB.Exec("UPDATE software_instances SET installation_json='{}',installation_status='',data_ownership='EXTERNAL' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200)
	i, err := s.readSoftware(id)
	if err != nil || !seen || i.Observation.RuntimePolicy == nil || !i.Observation.RuntimePolicy.Editable || i.ManagementMode != "MANAGED" || i.Installation != nil {
		t.Fatal("adopted service policy was not read from its executor", i, err)
	}
}

func runtimePolicyReceiptFixture(req deploy.Request) *runtimepolicy.State {
	if req.RuntimePolicy == nil {
		return nil
	}
	p := runtimepolicy.Policy{Restart: "on-failure", AutoStart: true, StopTimeoutSeconds: 30}
	if req.Software != nil && req.Software.Kind == "DOCKER" || req.Candidate != nil {
		p = runtimepolicy.Policy{Restart: "unless-stopped", AutoStart: true, StopTimeoutSeconds: 10}
	}
	if req.RuntimePolicy.Policy != nil {
		p = *req.RuntimePolicy.Policy
	}
	a := p
	held := req.Action == "SOFTWARE_STOP" || req.Action == "APP_STOP"
	if held {
		a.AutoStart = false
		if req.Software != nil && req.Software.Kind == "DOCKER" || req.Candidate != nil {
			a.Restart = "no"
			a.MaxRetries = 0
		}
	}
	return &runtimepolicy.State{Configured: &p, Actual: &a, Held: held, Editable: true, ObservedAt: Now()}
}

func TestRuntimePolicyReceiptDistinguishesPersistentStopAndConfiguredPolicy(t *testing.T) {
	for _, kind := range []string{"SYSTEMD", "DOCKER"} {
		policy := runtimepolicy.Policy{Restart: "on-failure", StopTimeoutSeconds: 30}
		if kind == "SYSTEMD" {
			policy.AutoStart = true
		}
		actual := policy
		actual.AutoStart = false
		if kind == "DOCKER" {
			actual.Restart = "no"
		}
		req := deploy.Request{Action: "SOFTWARE_STOP", RuntimePolicy: &runtimepolicy.Change{Version: 1}}
		result := deploy.Result{RuntimePolicy: &runtimepolicy.State{Configured: &policy, Actual: &actual, Held: true, ObservedAt: Now(), Editable: true}}
		if err := validateRuntimePolicyResult(req, result, kind); err != nil {
			t.Fatal(kind, err)
		}
		result.RuntimePolicy.Held = false
		if validateRuntimePolicyResult(req, result, kind) == nil {
			t.Fatal("ordinary stopped receipt accepted as persistent stop")
		}
		result.RuntimePolicy.Held = true
		req.Action = "SOFTWARE_START"
		if validateRuntimePolicyResult(req, result, kind) == nil {
			t.Fatal("held receipt accepted as restored start")
		}
	}
}

func TestRuntimePolicyRebindCannotChangeSoftwareOwnership(t *testing.T) {
	old := &deploy.SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Version: 1, Kind: "SYSTEMD", Locator: "example.service", IdentityHash: strings.Repeat("a", 64), Ownership: &deploy.SoftwareOwnership{JobID: "original", BinaryPath: "/opt/app/bin/run"}}
	next := *old
	next.Version++
	next.IdentityHash = strings.Repeat("b", 64)
	req := deploy.Request{Software: old, RuntimePolicy: &runtimepolicy.Change{Version: 1}}
	result := deploy.Result{RuntimePolicyBinding: &next, Software: &deploy.SoftwareObservation{Kind: next.Kind, Locator: next.Locator, IdentityHash: next.IdentityHash}}
	if err := validateRuntimePolicyBinding(req, result); err != nil {
		t.Fatal(err)
	}
	ownership := *old.Ownership
	ownership.JobID = "replacement"
	next.Ownership = &ownership
	if validateRuntimePolicyBinding(req, result) == nil {
		t.Fatal("policy edit replaced installation ownership")
	}
}

func TestRuntimePolicyRebindPreservesFrozenInstallationConfiguration(t *testing.T) {
	s, _, _, id, _ := readyNginxFixture(t)
	i, err := s.readSoftware(id)
	if err != nil {
		t.Fatal(err)
	}
	old := softwareRequest(i)
	next := *old
	next.Version++
	next.IdentityHash = strings.Repeat("f", 64)
	req := deploy.Request{JobID: i.LastJobID, Software: old, RuntimePolicy: &runtimepolicy.Change{Version: 1}}
	result := deploy.Result{Status: "SUCCEEDED", RuntimePolicyBinding: &next}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = commitRuntimePolicyBinding(tx, req, result); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := s.readSoftware(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.BindingRevision != next.Version || got.IdentityHash != next.IdentityHash || got.Installation.BindingRevision != next.Version || !reflect.DeepEqual(got.Installation.Config, i.Installation.Config) || got.InstallationJobID != i.InstallationJobID || softwareRequest(got).Ownership.JobID != old.Ownership.JobID {
		t.Fatal("policy rebind changed frozen installation", got, err)
	}
}
