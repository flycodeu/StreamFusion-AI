package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func nodeTerminalFixture(connected bool) *deploy.NodeTerminalObservation {
	status := "NOT_CONNECTED"
	if connected {
		status = "CONNECTED"
	}
	return &deploy.NodeTerminalObservation{Status: status, Profiles: []deploy.NodeTerminalProfile{
		{Path: "/home/ubuntu/.bashrc", SHA256: strings.Repeat("a", 64), Connected: connected},
		{Path: "/home/ubuntu/.profile", SHA256: strings.Repeat("b", 64), Connected: connected},
	}}
}

func initializedTerminalFixture(t *testing.T) (*Store, *browser, string, string, *nodeControllerFixture) {
	t.Helper()
	s, b := setup(t)
	b.login()
	host, f := nodeFixture(t, s, b)
	p := nodePlan(t, b, host, map[string]any{"action": "INITIALIZE", "name": "terminal environment", "account": "ubuntu", "source": map[string]string{"type": "OFFICIAL"}})
	id := runNodePlan(t, s, b, p, "terminal-initialize")["environmentId"].(string)
	return s, b, host, id, f
}

func TestNodeTerminalConnectFreezesLiveSnapshotAndReconcilesWithoutReplay(t *testing.T) {
	s, b, host, id, f := initializedTerminalFixture(t)
	env, _ := s.readNodeEnvironment(id)
	if env.Observation.Terminal != nil {
		t.Fatal("historical initialization gained terminal changes")
	}
	p := nodePlan(t, b, host, map[string]any{"action": "CONNECT_TERMINAL", "environmentId": id, "expectedEnvironmentVersion": env.Version})
	var frozen nodeEnvironmentPlan
	if deploy.StrictJSON([]byte(jsonText(p)), &frozen) != nil || frozen.Change.ExpectedHash == "" || frozen.Change.ExpectedHash == env.Observation.Fingerprint || frozen.Snapshot.Terminal == nil {
		t.Fatal("live terminal files were not frozen")
	}
	if f.mutations.Load() != 1 {
		t.Fatal("preflight changed target")
	}
	f.terminalDrift.Store(true)
	b.call("POST", "/node-environment-plans/"+frozen.ID+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "terminal-drift-check"}, 409)
	f.terminalDrift.Store(false)
	f.unknown.Store(true)
	accepted := runNodePlan(t, s, b, p, "terminal-connect-once")
	job := accepted["jobId"].(string)
	var status, raw string
	var locks int
	s.DB.QueryRow("SELECT status,input_json FROM jobs WHERE id=?", job).Scan(&status, &raw)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "UNKNOWN" || locks != 1 || f.mutations.Load() != 2 {
		t.Fatal(status, locks, f.mutations.Load())
	}
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	env, _ = s.readNodeEnvironment(id)
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "SUCCEEDED" || locks != 0 || f.mutations.Load() != 2 || env.Observation.Terminal == nil || env.Observation.Terminal.Status != "CONNECTED" || env.ObservedDefaultVersion != "" {
		t.Fatal("terminal reconcile changed default or replayed", status, locks)
	}
	again := dataOf(t, b.call("POST", "/node-environment-plans/"+frozen.ID+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "terminal-connect-once"}, 200))
	if again["jobId"] != job {
		t.Fatal("same intent changed job")
	}
	var persisted string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&persisted)
	if persisted != raw {
		t.Fatal("frozen request changed")
	}
}

func TestNodeTerminalInstallFreezesProfilesAndPreservesExplicitNoDefault(t *testing.T) {
	s, b, host, id, f := initializedTerminalFixture(t)
	env, _ := s.readNodeEnvironment(id)
	d := serviceinstall.NodeCatalog()[0]
	in := map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "Node terminal fixture", RuntimeMode: "NATIVE", MemoryMiB: 512}, "source": map[string]string{"type": "OFFICIAL"}, "node": nodeInstallInput{EnvironmentID: id, EnvironmentVersion: env.Version, ConnectTerminal: true, MakeDefault: false}}
	f.noTerminal.Store(true)
	if got := b.call("POST", "/hosts/"+host+"/software-install-plans", in, 409).Body.String(); !strings.Contains(got, "NODE_TERMINAL_CAPABILITY_REQUIRED") {
		t.Fatal(got)
	}
	f.noTerminal.Store(false)
	p := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", in, 200))
	n := p["node"].(map[string]any)
	if n["terminalHash"] == "" || n["connectTerminal"] != true || n["makeDefault"] == true {
		t.Fatal("missing frozen terminal intent", n)
	}
	f.terminalDrift.Store(true)
	b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "terminal-install-drift"}, 409)
	f.terminalDrift.Store(false)
	accepted := dataOf(t, b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "terminal-install-once"}, 202))
	job := accepted["jobId"].(string)
	var raw, status string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	env, _ = s.readNodeEnvironment(id)
	if status != "SUCCEEDED" || env.ObservedDefaultVersion != "" || env.DesiredDefaultSoftwareID != "" || env.Observation.Terminal == nil || env.Observation.Terminal.Status != "CONNECTED" {
		t.Fatal("explicit no-default changed or terminal unconfirmed", status)
	}
	bad := env.Observation
	bad.Terminal = nodeTerminalFixture(true)
	bad.Terminal.Profiles[0].Path = "/etc/profile"
	bad.Fingerprint = nodeTestFingerprint(bad)
	if nodeObservationMatches(env, bad) {
		t.Fatal("accepted another account or system profile path")
	}
}

func TestNodeTerminalCapabilityDoesNotBlockLegacyNodeOperations(t *testing.T) {
	s, b, host, id, f := initializedTerminalFixture(t)
	f.noTerminal.Store(true)
	target, _ := s.readTarget(host)
	if err := b.h.nodeTargetReady(context.Background(), target); err != nil {
		t.Fatal("legacy capability rejected", err)
	}
	env, _ := s.readNodeEnvironment(id)
	response := b.call("POST", "/hosts/"+host+"/node-environment-plans", map[string]any{"expectedHostVersion": "1", "action": "CONNECT_TERMINAL", "environmentId": id, "expectedEnvironmentVersion": env.Version}, 409)
	if !strings.Contains(response.Body.String(), "NODE_TERMINAL_CAPABILITY_REQUIRED") || f.mutations.Load() != 1 {
		t.Fatal(response.Body.String())
	}
	if strings.Contains(jsonText(env.context()), "connectTerminal") || strings.Contains(jsonText(env.context()), "terminalHash") {
		t.Fatal("legacy request acquired terminal intent")
	}
	b.call("POST", "/node-environments/"+id+"/observe", map[string]string{"expectedVersion": env.Version}, 200)
	env, _ = s.readNodeEnvironment(id)
	if env.Observation.Terminal != nil {
		t.Fatal("legacy executor asked for terminal observation")
	}
	f.noTerminal.Store(false)
	b.call("POST", "/node-environments/"+id+"/observe", map[string]string{"expectedVersion": env.Version}, 200)
	env, _ = s.readNodeEnvironment(id)
	if env.Observation.Terminal == nil || env.Observation.Terminal.Status != "NOT_CONNECTED" || f.mutations.Load() != 1 {
		t.Fatal("new terminal observation was not read-only")
	}
	f.noTerminal.Store(true)
	for _, action := range []string{"SET_DEFAULT", "REMOVE_ENVIRONMENT"} {
		input := map[string]any{"expectedHostVersion": "1", "action": action, "environmentId": id, "expectedEnvironmentVersion": env.Version, "confirmationName": env.Name}
		if action == "SET_DEFAULT" {
			input["nodeVersion"] = serviceinstall.NodeCatalog()[0].Version
		}
		response = b.call("POST", "/hosts/"+host+"/node-environment-plans", input, 409)
		if !strings.Contains(response.Body.String(), "NODE_TERMINAL_CAPABILITY_REQUIRED") || f.mutations.Load() != 1 {
			t.Fatal("terminal-aware operation reached downgraded executor", response.Body.String())
		}
	}
}

func TestNodeTerminalPreflightErrorRemainsActionable(t *testing.T) {
	_, b := setup(t)
	code, _ := b.h.installPreflightRejection(serviceinstall.Preflight{Checks: []serviceinstall.Check{{Status: "FAIL", Code: "NODE_TERMINAL_CONFLICT", Message: "existing shell configuration"}}}, serviceinstall.Config{})
	if code != "NODE_TERMINAL_CONFLICT" {
		t.Fatal(code)
	}
}

func TestNodeTerminalMissingEvidenceKeepsUnknownAndLock(t *testing.T) {
	s, b, host, id, f := initializedTerminalFixture(t)
	env, _ := s.readNodeEnvironment(id)
	p := nodePlan(t, b, host, map[string]any{"action": "CONNECT_TERMINAL", "environmentId": id, "expectedEnvironmentVersion": env.Version})
	f.omitTerminal.Store(true)
	accepted := runNodePlan(t, s, b, p, "terminal-missing-evidence")
	job := accepted["jobId"].(string)
	var status string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "UNKNOWN" || locks != 1 {
		t.Fatal("missing terminal evidence finalized", status, locks)
	}
}

func TestNodeTerminalRemovalRequiresPositiveCleanupEvidence(t *testing.T) {
	for _, evidence := range []string{"removed", "connected", "missing"} {
		t.Run(evidence, func(t *testing.T) {
			s, b, host, id, f := initializedTerminalFixture(t)
			env, _ := s.readNodeEnvironment(id)
			p := nodePlan(t, b, host, map[string]any{"action": "CONNECT_TERMINAL", "environmentId": id, "expectedEnvironmentVersion": env.Version})
			runNodePlan(t, s, b, p, "terminal-before-remove")
			env, _ = s.readNodeEnvironment(id)
			p = nodePlan(t, b, host, map[string]any{"action": "REMOVE_ENVIRONMENT", "environmentId": id, "expectedEnvironmentVersion": env.Version, "confirmationName": env.Name})
			f.terminalRemains.Store(evidence == "connected")
			f.omitTerminal.Store(evidence == "missing")
			accepted := runNodePlan(t, s, b, p, "terminal-remove-evidence")
			job := accepted["jobId"].(string)
			var status string
			var locks int
			s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
			env, _ = s.readNodeEnvironment(id)
			if evidence == "removed" {
				if status != "SUCCEEDED" || locks != 0 || env.ManagementMode != "RELEASED" {
					t.Fatal("valid cleanup rejected", status, locks)
				}
			} else if status != "UNKNOWN" || locks != 1 || env.ManagementMode != "MANAGED" {
				t.Fatal("profile cleanup not proven", status, locks)
			}
		})
	}
}
