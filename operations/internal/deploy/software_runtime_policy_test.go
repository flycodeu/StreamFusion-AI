package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestRuntimePolicyReadsOnlySupervisorMetadata(t *testing.T) {
	x := &executor{}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/docker" || len(args) != 6 || strings.Contains(strings.Join(args, " "), ".Config.Env") {
			t.Fatal("unexpected policy read", command, args)
		}
		return []byte(`{"restart":"on-failure","maxRetries":3,"stopTimeout":null}`), nil
	}
	state, err := x.readRuntimePolicy(context.Background(), SoftwareRequest{Kind: "DOCKER", Locator: strings.Repeat("a", 64)})
	if err != nil || !state.Editable || state.Actual.StopTimeoutSeconds != 10 || state.Actual.AutoStart || state.Actual.MaxRetries != 3 {
		t.Fatal(state, err)
	}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		return []byte("UnitFileState=static\nRestart=on-failure\nRestartUSec=5s\nTimeoutStopUSec=1min 30s\n"), nil
	}
	state, err = x.readRuntimePolicy(context.Background(), SoftwareRequest{Kind: "SYSTEMD", Locator: "example.service"})
	if err != nil || state.Editable || state.Actual == nil || state.Actual.StopTimeoutSeconds != 90 || state.Reason == "" {
		t.Fatal(state, err)
	}
	x.softwareRun = func(_ time.Duration, _ string, _ ...string) ([]byte, error) {
		return []byte("UnitFileState=enabled\nRestart=no\nRestartUSec=100ms\nTimeoutStopUSec=30s\n"), nil
	}
	state, err = x.readRuntimePolicy(context.Background(), SoftwareRequest{Kind: "SYSTEMD", Locator: "example.service"})
	if err != nil || !state.Editable || state.Actual == nil || state.Actual.RestartDelaySeconds != 0.1 {
		t.Fatal("default systemd restart interval rejected or rounded", state, err)
	}
}

func TestRuntimePolicyCorruptStoredIntentDoesNotBecomeEffectiveDefault(t *testing.T) {
	x := &executor{root: t.TempDir()}
	x.softwareRun = func(_ time.Duration, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"restart":"no","maxRetries":0,"stopTimeout":10}`), nil
	}
	s := SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Kind: "DOCKER", Locator: strings.Repeat("a", 64)}
	if err := os.MkdirAll(filepath.Dir(x.runtimePolicyFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(x.runtimePolicyFile(s.ID), []byte(`{"held":true,"configured":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := x.softwareRuntimeState(context.Background(), s); err == nil || err.Error() != "RUNTIME_POLICY_RECORD_DAMAGED" {
		t.Fatal("damaged held policy ignored", err)
	}
}

type runtimeNativeFixture struct {
	x          *executor
	state      string
	boot       bool
	policy     runtimepolicy.Policy
	dropin     bool
	writes     int
	controls   int
	invocation string
	lostStop   bool
	drift      bool
}

func newRuntimeNativeFixture(t *testing.T) (*runtimeNativeFixture, SoftwareRequest) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux target durable directory fsync; supervisor is mocked")
	}
	f := &runtimeNativeFixture{state: "active", boot: true, invocation: strings.Repeat("a", 32), policy: runtimepolicy.Policy{Restart: "on-failure", AutoStart: true, RestartDelaySeconds: 0.1, StopTimeoutSeconds: 30}}
	f.x = &executor{root: t.TempDir()}
	calls := []string{}
	f.x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/systemctl" {
			if strings.Contains(strings.Join(args, " "), "UnitFileState") {
				boot := "disabled"
				if f.boot {
					boot = "enabled"
				}
				return []byte("UnitFileState=" + boot + "\nRestart=" + f.policy.Restart + "\nRestartUSec=" + strconv.FormatFloat(f.policy.RestartDelaySeconds, 'f', -1, 64) + "s\nTimeoutStopUSec=" + strconv.Itoa(f.policy.StopTimeoutSeconds) + "s\n"), nil
			}
			if strings.Contains(strings.Join(args, " "), "InvocationID") {
				return []byte(f.invocation), nil
			}
			switch args[0] {
			case "enable":
				f.boot = true
				return nil, nil
			case "disable":
				f.boot = false
				return nil, nil
			case "start", "stop", "restart":
				f.controls++
				if args[0] == "stop" {
					f.state = "inactive"
					if f.lostStop {
						return nil, errors.New("lost stop reply")
					}
				} else {
					f.state = "active"
					f.invocation = strings.Repeat("b", 32)
				}
				return nil, nil
			}
		}
		if command == "/usr/bin/bash" {
			f.dropin = true
			f.writes++
			f.policy.Restart = args[len(args)-3]
			f.policy.RestartDelaySeconds, _ = strconv.ParseFloat(args[len(args)-2], 64)
			f.policy.StopTimeoutSeconds, _ = strconv.Atoi(args[len(args)-1])
			return nil, nil
		}
		base := unitSoftwareRunner(f.state, strings.Repeat("a", 64), &calls)
		out, err := base(context.Background(), command, args...)
		if command == "/usr/bin/systemctl" && f.dropin {
			out = []byte(strings.ReplaceAll(string(out), "DropInPaths=/etc/systemd/system/redis.service.d/override.conf", "DropInPaths=/etc/systemd/system/redis.service.d/override.conf /etc/systemd/system/redis.service.d/90-streamfusion-ops-runtime.conf"))
		}
		if command == "/usr/bin/sha256sum" {
			lines := strings.Split(string(out), "\n")
			for i, line := range lines {
				if strings.Contains(line, "90-streamfusion-ops-runtime.conf") {
					lines[i] = Digest(f.policy) + "  " + strings.Fields(line)[1]
				} else if f.drift {
					lines[i] = strings.Replace(line, strings.Repeat("a", 64), strings.Repeat("d", 64), 1)
				}
			}
			out = []byte(strings.Join(lines, "\n"))
		}
		return out, err
	}
	s := SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Version: 1, Kind: "SYSTEMD", Locator: "redis.service"}
	actual, err := ObserveSoftware(context.Background(), s, f.x.softwareRunner)
	if err != nil {
		t.Fatal(err)
	}
	s.IdentityHash = actual.IdentityHash
	if err = os.MkdirAll(filepath.Dir(f.x.softwareFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(f.x.softwareFile(s.ID), softwareBinding{Software: s, JobID: "original"}); err != nil {
		t.Fatal(err)
	}
	return f, s
}

func runtimeActionRequest(t *testing.T, x *executor, s SoftwareRequest, action string, p *runtimepolicy.Policy) (Request, string) {
	t.Helper()
	req := commandTestRequest()
	req.Installation = nil
	req.Software = &s
	req.Action, req.SoftwareOperation = action, action
	req.RuntimePolicy = &runtimepolicy.Change{Version: 1, Policy: p}
	req.JobID = strconv.FormatInt(time.Now().UnixNano(), 10)
	req.InputHash = Digest(req)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return req, dir
}

func TestRuntimePolicyStopSurvivesBootAndStartRestoresEditedPolicy(t *testing.T) {
	f, s := newRuntimeNativeFixture(t)
	stop, dir := runtimeActionRequest(t, f.x, s, "SOFTWARE_STOP", nil)
	r, err := f.x.applySoftwareRuntimePolicy(stop, dir)
	if err != nil || r.Status != "SUCCEEDED" || !r.RuntimePolicy.Held || r.RuntimePolicy.Configured.AutoStart != true || r.RuntimePolicy.Configured.RestartDelaySeconds != 0.1 || r.RuntimePolicy.Actual.RestartDelaySeconds != 0.1 || f.boot || f.state != "inactive" || r.RuntimePolicyBinding == nil {
		t.Fatal(r, err)
	}
	if err = f.x.verifyRuntimePolicyReceipt(stop, r); err != nil {
		t.Fatal(err)
	}
	s = *r.RuntimePolicyBinding
	// The host's boot acts only on the disabled local unit; Ops is absent.
	if f.boot {
		f.state = "active"
	}
	if f.state != "inactive" {
		t.Fatal("persistent stop was not retained")
	}
	policy := runtimepolicy.Policy{Restart: "always", AutoStart: true, RestartDelaySeconds: 0.25, StopTimeoutSeconds: 60}
	edit, dir := runtimeActionRequest(t, f.x, s, "SOFTWARE_RUNTIME_POLICY", &policy)
	r, err = f.x.applySoftwareRuntimePolicy(edit, dir)
	if err != nil || r.Status != "SUCCEEDED" || f.boot || f.state != "inactive" || !reflect.DeepEqual(*r.RuntimePolicy.Configured, policy) {
		t.Fatal("editing stopped instance started it", r, err)
	}
	if r.RuntimePolicyBinding != nil {
		s = *r.RuntimePolicyBinding
	}
	start, dir := runtimeActionRequest(t, f.x, s, "SOFTWARE_START", nil)
	r, err = f.x.applySoftwareRuntimePolicy(start, dir)
	if err != nil || r.Status != "SUCCEEDED" || r.RuntimePolicy.Held || !f.boot || f.state != "active" || !reflect.DeepEqual(*r.RuntimePolicy.Actual, policy) {
		t.Fatal("start lost saved policy", r, err)
	}
}

func TestRuntimePolicyUnknownReconcilesWithoutRepeatingMutation(t *testing.T) {
	f, s := newRuntimeNativeFixture(t)
	f.lostStop = true
	req, dir := runtimeActionRequest(t, f.x, s, "SOFTWARE_STOP", nil)
	r, err := f.x.applySoftwareRuntimePolicy(req, dir)
	if err != nil || r.Status != "UNKNOWN" {
		t.Fatal(r, err)
	}
	writes, controls := f.writes, f.controls
	r = f.x.reconcileSoftwareRuntimePolicy(req, dir)
	if r.Status != "SUCCEEDED" || f.writes != writes || f.controls != controls {
		t.Fatal("reconciliation replayed mutation", r)
	}
	f.drift = true
	if r = f.x.reconcileSoftwareRuntimePolicy(req, dir); r.Status != "UNKNOWN" {
		t.Fatal("external unit edit accepted as Ops rebind", r)
	}
}

func TestRuntimePolicyRestartRequiresNewProcessIdentity(t *testing.T) {
	f, s := newRuntimeNativeFixture(t)
	req, dir := runtimeActionRequest(t, f.x, s, "SOFTWARE_RESTART", nil)
	r, err := f.x.applySoftwareRuntimePolicy(req, dir)
	if err != nil || r.Status != "SUCCEEDED" || !validLifecycleEvidence(req, r) {
		t.Fatal(r, err)
	}
	f.invocation = strings.Repeat("a", 32)
	if r = f.x.reconcileSoftwareRuntimePolicy(req, dir); r.Status != "UNKNOWN" {
		t.Fatal("unchanged invocation proved restart", r)
	}
}

func TestRuntimePolicyDockerStopAndStartRestoreWithoutIdentityRebind(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux target directory fsync; Docker is mocked")
	}
	x := &executor{root: t.TempDir()}
	id, image := strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	state, mode, retries := "running", "on-failure", 3
	timeoutUsed := ""
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/systemctl" {
			return []byte("enabled"), nil
		}
		if command != "/usr/bin/docker" {
			return nil, errors.New("unexpected executable")
		}
		switch args[1] {
		case "inspect":
			if strings.Contains(args[3], "maxRetries") {
				return json.Marshal(map[string]any{"restart": mode, "maxRetries": retries, "stopTimeout": 40})
			}
			return json.Marshal(map[string]any{"id": id, "name": "/example", "imageId": image, "image": "fixture:1", "labels": map[string]string{}, "state": state})
		case "update":
			mode = args[3]
			retries = 0
			if name, count, ok := strings.Cut(mode, ":"); ok {
				mode = name
				retries, _ = strconv.Atoi(count)
			}
			return nil, nil
		case "stop":
			state = "exited"
			timeoutUsed = args[3]
			return nil, nil
		case "start":
			state = "running"
			return nil, nil
		}
		return nil, errors.New("unexpected args")
	}
	s := SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Version: 1, Kind: "DOCKER", Locator: id}
	actual, err := ObserveSoftware(context.Background(), s, x.softwareRunner)
	if err != nil {
		t.Fatal(err)
	}
	s.IdentityHash = actual.IdentityHash
	if err = os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: "original"}); err != nil {
		t.Fatal(err)
	}
	stop, dir := runtimeActionRequest(t, x, s, "SOFTWARE_STOP", nil)
	r, err := x.applySoftwareRuntimePolicy(stop, dir)
	if err != nil || r.Status != "SUCCEEDED" || state != "exited" || mode != "no" || !r.RuntimePolicy.Held || r.RuntimePolicyBinding != nil || timeoutUsed != "40" {
		t.Fatal(r, err, timeoutUsed)
	}
	start, dir := runtimeActionRequest(t, x, s, "SOFTWARE_START", nil)
	r, err = x.applySoftwareRuntimePolicy(start, dir)
	if err != nil || r.Status != "SUCCEEDED" || state != "running" || mode != "on-failure" || retries != 3 || r.RuntimePolicy.Held {
		t.Fatal(r, err)
	}
	policy := *r.RuntimePolicy.Configured
	policy.StopTimeoutSeconds = 50
	edit, dir := runtimeActionRequest(t, x, s, "SOFTWARE_RUNTIME_POLICY", &policy)
	r, err = x.applySoftwareRuntimePolicy(edit, dir)
	if err == nil || err.Error() != "RUNTIME_POLICY_STOP_TIMEOUT_IMMUTABLE" {
		t.Fatal("unsupported edit crossed mutation boundary", r, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "runtime-policy-intent.json")); !os.IsNotExist(err) {
		t.Fatal("unsupported edit persisted intent")
	}
}

func TestInstalledPolicyHookPreservesEvidenceVersionAndReconcilesWithoutReplay(t *testing.T) {
	f, s := newRuntimeNativeFixture(t)
	p := runtimepolicy.Policy{Restart: "always", AutoStart: false, RestartDelaySeconds: 6, StopTimeoutSeconds: 45}
	req := commandTestRequest()
	req.Software = nil
	req.Installation.SoftwareID = s.ID
	req.Installation.Config.RuntimePolicy = &p
	req.InputHash = Digest(req)
	dir := filepath.Join(f.x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	before := &SoftwareObservation{Kind: s.Kind, Locator: s.Locator, IdentityHash: s.IdentityHash, State: "RUNNING", Version: "2.0"}
	base := Result{Status: "SUCCEEDED", Software: before, Installation: &serviceinstall.Evidence{SoftwareID: s.ID, BindingRevision: 1, Kind: s.Kind, Locator: s.Locator, IdentityHash: s.IdentityHash, ObservedVersion: "2.0"}}
	r := f.x.applyInstalledRuntimePolicy(req, dir, base)
	if r.Status != "SUCCEEDED" || r.RuntimePolicy == nil || r.Software.IdentityHash == s.IdentityHash || r.Installation.BindingRevision != 1 || r.Software.Version != "2.0" || before.IdentityHash != s.IdentityHash {
		t.Fatal("install hook lost frozen evidence", r)
	}
	writes := f.writes
	recovered, found := f.x.reconcileInstalledRuntimePolicy(req, dir)
	if !found || recovered.Status != "SUCCEEDED" || f.writes != writes || recovered.Installation.IdentityHash != r.Installation.IdentityHash {
		t.Fatal("policy recovery replayed installation", recovered)
	}
}

func TestRecoveredInstallationCannotConfirmMissingPolicyOrReplayCommands(t *testing.T) {
	f, s := newRuntimeNativeFixture(t)
	p := runtimepolicy.Policy{Restart: "always", AutoStart: false, RestartDelaySeconds: 6, StopTimeoutSeconds: 45}
	req := commandTestRequest()
	req.Installation.SoftwareID = s.ID
	req.Installation.Config.RuntimePolicy = &p
	req.InputHash = Digest(req)
	dir := filepath.Join(f.x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	r := Result{Status: "SUCCEEDED", Software: &SoftwareObservation{Kind: s.Kind, Locator: s.Locator, IdentityHash: s.IdentityHash, State: "RUNNING"}, Installation: &serviceinstall.Evidence{SoftwareID: s.ID, BindingRevision: s.Version, Kind: s.Kind, Locator: s.Locator, IdentityHash: s.IdentityHash}}
	if got := f.x.verifyRecoveredInstallationPolicy(req, dir, r); got.Status != "UNKNOWN" || f.writes != 0 || f.controls != 0 {
		t.Fatal("missing policy accepted or applied during recovery", got)
	}
	// A matching already-applied policy permits metadata recovery only.
	f.policy, f.boot = p, false
	if got := f.x.verifyRecoveredInstallationPolicy(req, dir, r); got.Status != "SUCCEEDED" || f.writes != 0 || f.controls != 0 {
		t.Fatal("matching policy not recovered read-only", got)
	}
}

func TestRuntimePolicyUninstallCleanupKeepsHistoryAndReconcilesWithoutDeletingAgain(t *testing.T) {
	f, s := newRuntimeNativeFixture(t)
	f.dropin = true
	if err := os.MkdirAll(filepath.Dir(f.x.runtimePolicyFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(f.x.runtimePolicyFile(s.ID), runtimepolicy.State{Configured: &f.policy}); err != nil {
		t.Fatal(err)
	}
	base := f.x.softwareRun
	deletions := 0
	f.x.softwareRun = func(limit time.Duration, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/bash" && len(args) > 2 {
			switch args[2] {
			case "ops-runtime-cleanup":
				if args[1] != deployScript("runtime-policy/systemd-remove.sh") {
					t.Fatal("unexpected cleanup script")
				}
				deletions++
				f.dropin = false
				return nil, errors.New("cleanup reply lost")
			case "ops-runtime-cleanup-check":
				if f.dropin {
					return nil, errors.New("dropin still exists")
				}
				return nil, nil
			}
		}
		if command == "/usr/bin/systemctl" && strings.Contains(strings.Join(args, " "), "--property=NeedDaemonReload") {
			return []byte("NeedDaemonReload=no\n"), nil
		}
		return base(limit, command, args...)
	}
	req := commandTestRequest()
	req.Installation = nil
	req.Software = &s
	req.Uninstallation = &SoftwareUninstallRequest{}
	req.SoftwareOperation, req.Action = "SOFTWARE_UNINSTALL", "SOFTWARE_UNINSTALL"
	req.InputHash = Digest(req)
	dir := filepath.Join(f.x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	r := Result{Status: "SUCCEEDED", Uninstallation: &SoftwareUninstallEvidence{SoftwareID: s.ID, Kind: s.Kind, Locator: s.Locator, ProgramRemoved: true}}
	if got := f.x.cleanupRuntimePolicyAfterUninstall(req, dir, r); got.Status != "UNKNOWN" {
		t.Fatal("lost cleanup reply reported final", got)
	}
	got, found := f.x.reconcileRuntimePolicyCleanup(req, dir)
	if !found || got.Status != "SUCCEEDED" || deletions != 1 {
		t.Fatal("cleanup replayed or could not verify", got, deletions)
	}
	if err := f.x.verifyRuntimePolicyCleanup(req, got); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.x.runtimePolicyFile(s.ID)); err != nil {
		t.Fatal("historical policy record deleted", err)
	}
}
