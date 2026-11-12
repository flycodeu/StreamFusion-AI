package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

func applicationLifecycleFixture(t *testing.T) (*executor, Request, *applicationContainer, *runtimepolicy.Policy, *[]string) {
	t.Helper()
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"UP"}`)) }))
	t.Cleanup(health.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	n, _ := strconv.Atoi(port)
	x := &executor{root: t.TempDir(), owner: Owner{ControllerID: uuid.NewString(), TargetID: uuid.NewString()}}
	r := Release{ID: uuid.NewString(), InstanceID: uuid.NewString(), ArtifactID: uuid.NewString(), ArchiveSHA256: strings.Repeat("a", 64), ByteSize: 1, ImageID: "sha256:" + strings.Repeat("b", 64), Config: Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: n, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/health", HealthSeconds: 5, DataCompatibility: "v1"}}
	c := &applicationContainer{ID: strings.Repeat("c", 64), Image: r.ImageID, Running: true, StartedAt: "2026-11-12T00:00:00Z", Labels: map[string]string{"com.streamfusion.ops.controller": x.owner.ControllerID, "com.streamfusion.ops.instance": r.InstanceID, "com.streamfusion.ops.release": r.ID, "com.streamfusion.ops.config": Digest(r.Config)}}
	p := &runtimepolicy.Policy{Restart: "always", AutoStart: true, StopTimeoutSeconds: 10}
	calls := []string{}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/systemctl" && len(args) > 0 && args[0] == "is-enabled" {
			return []byte("enabled\n"), nil
		}
		if command != "/usr/bin/docker" {
			return nil, errors.New("unexpected command")
		}
		switch {
		case args[0] == "inspect":
			return json.Marshal(c)
		case len(args) > 1 && args[0] == "container" && args[1] == "inspect":
			if len(args) > 3 && args[2] == "--format" {
				return json.Marshal(map[string]any{"restart": p.Restart, "maxRetries": p.MaxRetries, "stopTimeout": p.StopTimeoutSeconds})
			}
			return json.Marshal([]any{map[string]any{"ID": c.ID, "Image": c.Image, "Config": map[string]any{"Labels": c.Labels}, "State": map[string]any{"Running": c.Running}}})
		case len(args) > 1 && args[0] == "container" && args[1] == "update":
			calls = append(calls, strings.Join(args, " "))
			mode := args[3]
			p.Restart = mode
			p.AutoStart = mode == "always" || mode == "unless-stopped"
			p.MaxRetries = 0
			return nil, nil
		case args[0] == "stop":
			calls = append(calls, "stop")
			c.Running = false
			return nil, nil
		case args[0] == "start" || args[0] == "restart":
			calls = append(calls, args[0])
			c.Running = true
			c.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return nil, nil
		}
		return nil, errors.New("unexpected docker args: " + strings.Join(args, " "))
	}
	return x, Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, JobID: uuid.NewString(), Candidate: &r, Action: "APP_STOP", ApplicationOperation: "APP_STOP", InputHash: strings.Repeat("e", 64), RuntimePolicy: &runtimepolicy.Change{Version: 1}}, c, p, &calls
}

func TestApplicationStopEditStartPreservesInstancePolicy(t *testing.T) {
	x, req, c, p, calls := applicationLifecycleFixture(t)
	run := func(action string, policy *runtimepolicy.Policy) Result {
		t.Helper()
		req.Action = action
		req.ApplicationOperation = action
		req.JobID = uuid.NewString()
		req.RuntimePolicy.Policy = policy
		dir := filepath.Join(x.root, "jobs", req.JobID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		result, err := x.changeApplicationLifecycle(req, dir)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	stopped := run("APP_STOP", nil)
	if c.Running || p.Restart != "no" || !stopped.RuntimePolicy.Held || stopped.RuntimePolicy.Configured.Restart != "always" {
		t.Fatalf("stop did not preserve original policy: %+v", stopped)
	}
	updated := runtimepolicy.Policy{Restart: "unless-stopped", AutoStart: true, StopTimeoutSeconds: 10}
	result := run("APP_RUNTIME_POLICY", &updated)
	if c.Running || p.Restart != "no" || !result.RuntimePolicy.Held {
		t.Fatal("editing stopped instance started it")
	}
	// A new executor process (or host boot) reads the same persisted hold.
	if held, err := (&executor{root: x.root}).applicationHeld(req.Candidate.InstanceID); err != nil || !held {
		t.Fatalf("hold not durable: %v", err)
	}
	run("APP_START", nil)
	if !c.Running || p.Restart != "unless-stopped" {
		t.Fatal("start did not restore saved policy")
	}
	if got := strings.Join(*calls, "|"); strings.Contains(got, "pull") || strings.Contains(got, "compose") {
		t.Fatal("lifecycle operation rebuilt the application")
	}
}

func TestApplicationReconcileDoesNotReplayCommandsAndRejectsIdentityDrift(t *testing.T) {
	x, req, c, _, calls := applicationLifecycleFixture(t)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := x.changeApplicationLifecycle(req, dir); err != nil {
		t.Fatal(err)
	}
	n := len(*calls)
	if _, err := x.verifyApplicationLifecycle(req, dir); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != n {
		t.Fatal("reconcile replayed command")
	}
	c.Labels["com.streamfusion.ops.controller"] = uuid.NewString()
	if _, err := x.verifyApplicationLifecycle(req, dir); err == nil {
		t.Fatal("foreign container accepted")
	}
}

func TestApplicationComposeOverridePreservesPolicyAcrossReleaseRollback(t *testing.T) {
	x, req, _, _, _ := applicationLifecycleFixture(t)
	p := runtimepolicy.Policy{Restart: "on-failure", AutoStart: false, MaxRetries: 3, StopTimeoutSeconds: 20}
	if err := x.saveApplicationPolicy(req.Candidate.InstanceID, &runtimepolicy.State{Configured: &p, Held: true}); err != nil {
		t.Fatal(err)
	}
	r := *req.Candidate
	r.Config.RuntimePolicy = &runtimepolicy.Policy{Restart: "always", AutoStart: true}
	file, held, err := x.applicationComposePolicy(r)
	if err != nil || !held {
		t.Fatalf("%v", err)
	}
	var conf struct{ Services map[string]map[string]any }
	if err := readJSON(file, &conf); err != nil {
		t.Fatal(err)
	}
	if conf.Services["app"]["restart"] != "no" || conf.Services["app"]["stop_grace_period"] != "20s" {
		t.Fatalf("bad override: %+v", conf)
	}
	state, err := x.applicationPolicy(context.Background(), r, applicationContainer{ID: strings.Repeat("c", 64)})
	if err != nil || state.Configured.Restart != "on-failure" || state.Configured.MaxRetries != 3 {
		t.Fatalf("current policy replaced by release policy: %v", err)
	}
}

func TestApplicationRestartRequiresNewStartIdentity(t *testing.T) {
	x, req, _, _, _ := applicationLifecycleFixture(t)
	req.Action = "APP_RESTART"
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := x.changeApplicationLifecycle(req, dir); err != nil {
		t.Fatal(err)
	}
	var before applicationContainer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	before, err := x.applicationContainer(ctx, *req.Candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(dir, "application-before.json"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := x.verifyApplicationLifecycle(req, dir); err == nil {
		t.Fatal("unchanged start identity accepted")
	}
}

func TestApplicationLifecycleLostReplyReconcilesAndAcknowledgesFrozenReceipt(t *testing.T) {
	x, req, c, _, calls := applicationLifecycleFixture(t)
	req.Action, req.ApplicationOperation = "APP_START", "APP_START"
	req.DeadlineAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	req.InputHash = ""
	req.InputHash = Digest(req)
	var output bytes.Buffer
	if err := x.executeApplicationLifecycle(req, &output); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := StrictJSON(output.Bytes(), &result); err != nil || result.Status != "SUCCEEDED" {
		t.Fatalf("%s: %v", output.String(), err)
	}
	file := filepath.Join(x.root, "jobs", req.JobID, "result.json")
	// Simulate termination after the command and verification but before its
	// reply was committed. The original operation is observed, never repeated.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	count := len(*calls)
	query := req
	query.Action = "STATUS"
	output.Reset()
	if err := x.executeApplicationLifecycle(query, &output); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != count {
		t.Fatal("lost reply caused a repeated command")
	}
	if err := StrictJSON(output.Bytes(), &result); err != nil || result.Status != "SUCCEEDED" {
		t.Fatal(output.String(), err)
	}
	// Later process exit does not invalidate the completed verification receipt.
	c.Running = false
	ack := req
	ack.Action = "ACK"
	ack.Candidate = nil
	ack.ResultHash = Digest(result)
	output.Reset()
	if err := x.executeApplicationLifecycle(ack, &output); err != nil {
		t.Fatal(err)
	}
	if err := StrictJSON(output.Bytes(), &result); err != nil || !result.Ack {
		t.Fatal(output.String(), err)
	}
	if _, err := os.Stat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("ack retained target pending")
	}
}
