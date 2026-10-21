package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type softwareFixture struct {
	x      *executor
	state  string
	digest string
	calls  []string
	fail   bool
}

func newSoftwareFixture(t *testing.T) *softwareFixture {
	t.Helper()
	f := &softwareFixture{state: "inactive", digest: strings.Repeat("a", 64)}
	f.x = &executor{root: t.TempDir(), owner: Owner{ControllerID: uuid.NewString(), TargetID: uuid.NewString()}}
	if err := os.Mkdir(filepath.Join(f.x.root, "jobs"), 0700); err != nil {
		t.Fatal(err)
	}
	f.x.softwareRun = func(limit time.Duration, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/systemctl" && (args[0] == "start" || args[0] == "stop") {
			f.calls = append(f.calls, command+" "+strings.Join(args, " "))
			if f.fail {
				return nil, errors.New("command result lost")
			}
			f.state = "active"
			if args[0] == "stop" {
				f.state = "inactive"
			}
			return nil, nil
		}
		return unitSoftwareRunner(f.state, f.digest, &f.calls)(context.Background(), command, args...)
	}
	return f
}

func (f *softwareFixture) request(t *testing.T, action string, software *SoftwareRequest) Request {
	t.Helper()
	if software == nil {
		actual, err := ObserveSoftware(context.Background(), SoftwareRequest{Kind: "SYSTEMD", Locator: "redis.service"}, f.x.softwareRunner)
		if err != nil {
			t.Fatal(err)
		}
		software = &SoftwareRequest{ID: uuid.NewString(), Version: 1, Kind: "SYSTEMD", Locator: "redis.service", IdentityHash: actual.IdentityHash, SoftwareKey: "REDIS"}
	}
	req := Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: f.x.owner.ControllerID, TargetID: f.x.owner.TargetID, JobID: uuid.NewString(), TraceID: uuid.NewString(), Action: action, SoftwareOperation: action, Software: software, DeadlineAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
	req.InputHash = Digest(req)
	return req
}

func runSoftwareFixture(t *testing.T, f *softwareFixture, req Request) Result {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux directory fsync durability fixture; Windows cannot fsync directories")
	}
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// The Linux fixture exercises the exact durable state machine below the
	// transport lock using only a supplied command fake.
	if err := f.x.softwareLocked(req, &out); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func ackSoftwareFixture(t *testing.T, f *softwareFixture, req Request, result Result) {
	t.Helper()
	req.Action, req.ResultHash = "ACK", Digest(result)
	ack := runSoftwareFixture(t, f, req)
	if !ack.Ack {
		t.Fatal("acknowledgement missing")
	}
}

func TestSoftwareDurableAdoptStartStopUnmanage(t *testing.T) {
	f := newSoftwareFixture(t)
	adopt := f.request(t, "SOFTWARE_ADOPT", nil)
	result := runSoftwareFixture(t, f, adopt)
	if result.Status != "SUCCEEDED" || f.x.boundSoftware(*adopt.Software) != nil {
		t.Fatalf("adoption failed: %+v", result)
	}
	ackSoftwareFixture(t, f, adopt, result)
	start := f.request(t, "SOFTWARE_START", adopt.Software)
	result = runSoftwareFixture(t, f, start)
	if result.Status != "SUCCEEDED" || result.Software.State != "RUNNING" {
		t.Fatalf("start failed: %+v", result)
	}
	ackSoftwareFixture(t, f, start, result)
	stop := f.request(t, "SOFTWARE_STOP", adopt.Software)
	result = runSoftwareFixture(t, f, stop)
	if result.Status != "SUCCEEDED" || result.Software.State != "STOPPED" {
		t.Fatalf("stop failed: %+v", result)
	}
	ackSoftwareFixture(t, f, stop, result)
	// Releasing a running service must leave it running and touch no files
	// outside the executor's own binding and job records.
	f.state = "active"
	before := strings.Count(strings.Join(f.calls, "\n"), "/usr/bin/systemctl stop")
	release := f.request(t, "SOFTWARE_UNMANAGE", adopt.Software)
	result = runSoftwareFixture(t, f, release)
	if result.Status != "SUCCEEDED" || f.state != "active" || f.x.boundSoftware(*adopt.Software) == nil {
		t.Fatalf("detach failed: %+v", result)
	}
	if strings.Count(strings.Join(f.calls, "\n"), "/usr/bin/systemctl stop") != before {
		t.Fatal("unmanage stopped the external service")
	}
	ackSoftwareFixture(t, f, release, result)
}

func TestSoftwareInputHashBindsVersionLocatorAndAction(t *testing.T) {
	req := Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString(), JobID: uuid.NewString(), Action: "SOFTWARE_ADOPT", SoftwareOperation: "SOFTWARE_ADOPT", Software: &SoftwareRequest{ID: uuid.NewString(), Version: 1, Kind: "SYSTEMD", Locator: "redis.service", IdentityHash: strings.Repeat("a", 64)}}
	req.InputHash = Digest(req)
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*Request){
		func(r *Request) { r.Software.Version++ },
		func(r *Request) { r.Software.Locator = "other.service" },
		func(r *Request) { r.SoftwareOperation = "SOFTWARE_STOP" },
	} {
		changed := req
		s := *req.Software
		changed.Software = &s
		mutation(&changed)
		if err := validateSoftwareInput(changed); err == nil {
			t.Fatal("frozen input mutation accepted")
		}
	}
}

func TestSoftwareIdentityAndBindingRevisionPreventStart(t *testing.T) {
	f := newSoftwareFixture(t)
	adopt := f.request(t, "SOFTWARE_ADOPT", nil)
	result := runSoftwareFixture(t, f, adopt)
	ackSoftwareFixture(t, f, adopt, result)
	changed := *adopt.Software
	changed.Version++
	start := f.request(t, "SOFTWARE_START", &changed)
	result = runSoftwareFixture(t, f, start)
	if result.Status != "FAILED" || result.ErrorCode != "SOFTWARE_BINDING_REVISION_MISMATCH" {
		t.Fatalf("version changed binding: %+v", result)
	}
	ackSoftwareFixture(t, f, start, result)
	f.digest = strings.Repeat("b", 64)
	start = f.request(t, "SOFTWARE_START", adopt.Software)
	result = runSoftwareFixture(t, f, start)
	if result.Status != "FAILED" || result.ErrorCode != "SOFTWARE_IDENTITY_DRIFT" || f.state != "inactive" {
		t.Fatalf("drifted service started: %+v", result)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "/usr/bin/systemctl start") {
			t.Fatal("start invoked before identity check")
		}
	}
}

func TestSoftwareUnknownReconcilesWithoutReplayingCommand(t *testing.T) {
	f := newSoftwareFixture(t)
	adopt := f.request(t, "SOFTWARE_ADOPT", nil)
	ackSoftwareFixture(t, f, adopt, runSoftwareFixture(t, f, adopt))
	f.fail = true
	start := f.request(t, "SOFTWARE_START", adopt.Software)
	result := runSoftwareFixture(t, f, start)
	if result.Status != "UNKNOWN" {
		t.Fatalf("lost reply reported final: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.x.root, "pending.json")); err != nil {
		t.Fatal("unknown operation lost pending intent", err)
	}
	count := strings.Count(strings.Join(f.calls, "\n"), "/usr/bin/systemctl start")
	// The service later reaches the requested state; STATUS verifies it instead
	// of issuing a duplicate lifecycle command.
	f.state = "active"
	start.Action = "STATUS"
	result = runSoftwareFixture(t, f, start)
	if result.Status != "SUCCEEDED" {
		t.Fatalf("unknown did not reconcile: %+v", result)
	}
	if strings.Count(strings.Join(f.calls, "\n"), "/usr/bin/systemctl start") != count {
		t.Fatal("STATUS replayed a start")
	}
	ackSoftwareFixture(t, f, start, result)
}

func TestSoftwareDetachRecoveryAndLostAckPreserveNewPending(t *testing.T) {
	f := newSoftwareFixture(t)
	adopt := f.request(t, "SOFTWARE_ADOPT", nil)
	ackSoftwareFixture(t, f, adopt, runSoftwareFixture(t, f, adopt))
	release := f.request(t, "SOFTWARE_UNMANAGE", adopt.Software)
	result := runSoftwareFixture(t, f, release)
	if err := os.Remove(filepath.Join(f.x.root, "jobs", release.JobID, "result.json")); err != nil {
		t.Fatal(err)
	}
	release.Action = "STATUS"
	result = runSoftwareFixture(t, f, release)
	if result.Status != "SUCCEEDED" || result.Phase != "SOFTWARE_UNMANAGED" {
		t.Fatalf("detach was not recovered: %+v", result)
	}
	ackSoftwareFixture(t, f, release, result)
	newPending := f.request(t, "SOFTWARE_ADOPT", adopt.Software)
	if err := atomicJSON(filepath.Join(f.x.root, "pending.json"), newPending); err != nil {
		t.Fatal(err)
	}
	ackSoftwareFixture(t, f, release, result)
	var pending Request
	if err := readJSON(filepath.Join(f.x.root, "pending.json"), &pending); err != nil || pending.JobID != newPending.JobID {
		t.Fatal("old ACK deleted a newer operation", err)
	}
}

func TestSoftwareInterruptedAdoptDoesNotClaimTransitioningServiceReady(t *testing.T) {
	f := newSoftwareFixture(t)
	adopt := f.request(t, "SOFTWARE_ADOPT", nil)
	ackSoftwareFixture(t, f, adopt, runSoftwareFixture(t, f, adopt))
	f.state = "activating"
	result := f.x.reconcileSoftware(adopt, filepath.Join(f.x.root, "jobs", adopt.JobID))
	if result.Status != "UNKNOWN" {
		t.Fatalf("transitioning service was adopted as ready: %+v", result)
	}
}

func TestSoftwareFinalStartReceiptCanAckAfterNaturalExit(t *testing.T) {
	f := newSoftwareFixture(t)
	adopt := f.request(t, "SOFTWARE_ADOPT", nil)
	ackSoftwareFixture(t, f, adopt, runSoftwareFixture(t, f, adopt))
	start := f.request(t, "SOFTWARE_START", adopt.Software)
	result := runSoftwareFixture(t, f, start)
	f.state = "inactive"
	query := start
	query.Action = "STATUS"
	later := runSoftwareFixture(t, f, query)
	if Digest(later) != Digest(result) || later.Software.State != "RUNNING" {
		t.Fatal("historical execution receipt changed with later runtime state")
	}
	ackSoftwareFixture(t, f, start, result)
	actual, err := ObserveSoftware(context.Background(), *start.Software, f.x.softwareRunner)
	if err != nil || actual.State != "STOPPED" {
		t.Fatal("new observation did not show natural exit", err)
	}
}

func TestSoftwareUnknownResultCannotAckWithoutEvidence(t *testing.T) {
	f := newSoftwareFixture(t)
	adopt := f.request(t, "SOFTWARE_ADOPT", nil)
	ackSoftwareFixture(t, f, adopt, runSoftwareFixture(t, f, adopt))
	f.fail = true
	start := f.request(t, "SOFTWARE_START", adopt.Software)
	result := runSoftwareFixture(t, f, start)
	start.Action, start.ResultHash = "ACK", Digest(result)
	var out bytes.Buffer
	if err := f.x.softwareLocked(start, &out); err == nil || err.Error() != "RESULT_NOT_FINAL" {
		t.Fatalf("unknown operation acknowledged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.x.root, "pending.json")); err != nil {
		t.Fatal("unknown lock was released", err)
	}
}

func softwareReadBinding(t *testing.T, f *softwareFixture, req Request) {
	t.Helper()
	file := f.x.softwareFile(req.Software.ID)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(softwareBinding{Software: *req.Software, JobID: req.JobID})
	if err != nil {
		t.Fatal(err)
	}
	// This is a read-state fixture only, not a durability assertion.
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSoftwareReadStateRecoveryRequiresFrozenBindingAndActualDesiredState(t *testing.T) {
	f := newSoftwareFixture(t)
	req := f.request(t, "SOFTWARE_START", nil)
	if result := f.x.reconcileSoftware(req, filepath.Join(f.x.root, "jobs", req.JobID)); result.Status != "UNKNOWN" {
		t.Fatal("unbound operation became successful", result)
	}
	softwareReadBinding(t, f, req)
	for _, state := range []string{"inactive", "activating", "failed"} {
		f.state = state
		if result := f.x.reconcileSoftware(req, filepath.Join(f.x.root, "jobs", req.JobID)); result.Status != "UNKNOWN" {
			t.Fatal("unconfirmed start became successful", result)
		}
	}
	f.state = "active"
	result := f.x.reconcileSoftware(req, filepath.Join(f.x.root, "jobs", req.JobID))
	if result.Status != "SUCCEEDED" || result.Software.State != "RUNNING" {
		t.Fatalf("confirmed state not reconciled: %+v", result)
	}
	f.digest = strings.Repeat("b", 64)
	if result = f.x.reconcileSoftware(req, filepath.Join(f.x.root, "jobs", req.JobID)); result.Status != "UNKNOWN" {
		t.Fatal("different service definition became successful", result)
	}
}

func TestSoftwareReadStateReceiptValidationDoesNotConfuseCurrentStateWithHistory(t *testing.T) {
	f := newSoftwareFixture(t)
	req := f.request(t, "SOFTWARE_START", nil)
	softwareReadBinding(t, f, req)
	result := softwareResult(req)
	result.Status, result.Software = "SUCCEEDED", &SoftwareObservation{Kind: "SYSTEMD", Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: "RUNNING", ObservedAt: stamp()}
	f.state = "inactive"
	if err := f.x.verifySoftwareResult(req, result); err != nil {
		t.Fatal("natural exit invalidated the historical receipt", err)
	}
	result.Software.IdentityHash = strings.Repeat("b", 64)
	if err := f.x.verifySoftwareResult(req, result); err == nil {
		t.Fatal("a forged receipt identity was accepted")
	}
	result = softwareResult(req)
	var out bytes.Buffer
	if err := f.x.ackSoftware(req, req, result, filepath.Join(f.x.root, "jobs", req.JobID), &out); err == nil || err.Error() != "RESULT_NOT_FINAL" {
		t.Fatal("unknown receipt could be acknowledged", err)
	}
}
