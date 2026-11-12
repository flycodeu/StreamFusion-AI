package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

func applicationLifecycleAction(action string) bool {
	switch action {
	case "APP_RUNTIME_POLICY", "APP_START", "APP_STOP", "APP_RESTART":
		return true
	}
	return false
}

func (x *executor) applicationPolicyFile(instanceID string) string {
	return filepath.Join(x.root, "instances", instanceID, "runtime-policy.json")
}

type applicationContainer struct {
	ID        string
	Image     string
	Labels    map[string]string
	Running   bool
	Paused    bool
	StartedAt string
}

// Only inspect identity and lifecycle fields, never the application's environment.
func (x *executor) applicationContainer(ctx context.Context, r Release) (applicationContainer, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	name := "sfops-" + strings.ReplaceAll(r.InstanceID, "-", "")
	out, err := x.softwareRunner(ctx, "/usr/bin/docker", "inspect", "--type", "container", "--format", `{"ID":{{json .Id}},"Image":{{json .Image}},"Labels":{{json .Config.Labels}},"Running":{{json .State.Running}},"Paused":{{json .State.Paused}},"StartedAt":{{json .State.StartedAt}}}`, name)
	var actual applicationContainer
	if err != nil || json.Unmarshal(out, &actual) != nil || !Hex.MatchString(actual.ID) {
		return actual, errors.New("APPLICATION_STATE_UNAVAILABLE")
	}
	labels := actual.Labels
	if labels["com.streamfusion.ops.controller"] != x.owner.ControllerID || labels["com.streamfusion.ops.instance"] != r.InstanceID || labels["com.streamfusion.ops.release"] != r.ID || labels["com.streamfusion.ops.config"] != Digest(r.Config) || actual.Image != r.ImageID {
		return actual, errors.New("ACTUAL_RELEASE_MISMATCH")
	}
	return actual, nil
}

func (x *executor) applicationPolicy(ctx context.Context, r Release, c applicationContainer) (*runtimepolicy.State, error) {
	actual, err := x.readRuntimePolicy(ctx, SoftwareRequest{ID: r.InstanceID, Kind: "DOCKER", Locator: c.ID})
	if err != nil {
		return nil, err
	}
	var saved runtimepolicy.State
	if err := readJSON(x.applicationPolicyFile(r.InstanceID), &saved); err == nil {
		if saved.Configured == nil || saved.Configured.Validate("DOCKER") != nil {
			return nil, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
		}
		actual.Configured, actual.Held = saved.Configured, saved.Held
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		actual.Configured = actual.Actual
	}
	return actual, nil
}

func (x *executor) saveApplicationPolicy(id string, state *runtimepolicy.State) error {
	file := x.applicationPolicyFile(id)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	return atomicJSON(file, state)
}

func (x *executor) applicationHeld(id string) (bool, error) {
	var state runtimepolicy.State
	err := readJSON(x.applicationPolicyFile(id), &state)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state.Configured == nil || state.Configured.Validate("DOCKER") != nil {
		return false, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
	}
	return state.Held, nil
}

// Keep instance policy outside immutable release material. Both normal deploys
// and rollback compose the selected release with this same current override.
func (x *executor) applicationComposePolicy(r Release) (string, bool, error) {
	var state runtimepolicy.State
	err := readJSON(x.applicationPolicyFile(r.InstanceID), &state)
	if os.IsNotExist(err) {
		if r.Config.RuntimePolicy == nil {
			return "", false, nil
		}
		p := *r.Config.RuntimePolicy
		if p.StopTimeoutSeconds == 0 {
			p.StopTimeoutSeconds = 10
		}
		state.Configured = &p
		if err := x.saveApplicationPolicy(r.InstanceID, &state); err != nil {
			return "", false, err
		}
	} else if err != nil {
		return "", false, err
	}
	if state.Configured == nil || state.Configured.Validate("DOCKER") != nil {
		return "", false, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
	}
	p := runtimePolicyEffective(*state.Configured, state.Held, "DOCKER")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := x.checkDockerRuntimeBoot(ctx, p); err != nil {
		return "", false, err
	}
	file := filepath.Join(x.root, "instances", r.InstanceID, "runtime-compose.json")
	service := map[string]any{"restart": p.DockerRestart()}
	if p.StopTimeoutSeconds > 0 {
		service["stop_grace_period"] = strconv.Itoa(p.StopTimeoutSeconds) + "s"
	}
	if err := atomicJSON(file, map[string]any{"services": map[string]any{"app": service}}); err != nil {
		return "", false, err
	}
	return file, state.Held, nil
}

func (x *executor) prepareApplicationDeploymentPolicy(req Request) error {
	r := *req.Candidate
	var state runtimepolicy.State
	err := readJSON(x.applicationPolicyFile(r.InstanceID), &state)
	if os.IsNotExist(err) {
		if r.Config.RuntimePolicy == nil && req.RuntimePolicy == nil {
			return nil
		}
		if req.Baseline != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			c, err := x.applicationContainer(ctx, *req.Baseline)
			if err != nil {
				return err
			}
			current, err := x.applicationPolicy(ctx, *req.Baseline, c)
			if err != nil {
				return err
			}
			state = *current
		} else {
			p := runtimepolicy.Policy{Restart: "unless-stopped", AutoStart: true, StopTimeoutSeconds: 10}
			if r.Config.RuntimePolicy != nil {
				p = *r.Config.RuntimePolicy
			}
			state.Configured = &p
		}
	} else if err != nil {
		return err
	}
	if req.RuntimePolicy != nil {
		if req.RuntimePolicy.Version != runtimepolicy.Version || req.RuntimePolicy.Policy == nil {
			return errors.New("RUNTIME_POLICY_INVALID")
		}
		p := *req.RuntimePolicy.Policy
		if p.StopTimeoutSeconds == 0 {
			p.StopTimeoutSeconds = 10
		}
		state.Configured = &p
	}
	if state.Configured == nil || state.Configured.Validate("DOCKER") != nil {
		return errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
	}
	state.ObservedAt = ""
	return x.saveApplicationPolicy(r.InstanceID, &state)
}

// Persist intent before changing the supervisor. STATUS only verifies this intent;
// it never repeats a stop, start, restart, or policy update after a lost reply.
func (x *executor) changeApplicationLifecycle(req Request, dir string) (Result, error) {
	r := *req.Candidate
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	ctx, closeLog := x.installationLogContext(ctx, req, "APPLICATION_CONTROL")
	defer closeLog()
	c, err := x.applicationContainer(ctx, r)
	if err != nil {
		return Result{}, err
	}
	if c.Paused {
		return Result{}, errors.New("SOFTWARE_PAUSED_RESUME_REQUIRED")
	}
	state, err := x.applicationPolicy(ctx, r, c)
	if err != nil || !state.Editable || state.Configured == nil {
		if err != nil {
			return Result{}, err
		}
		return Result{}, errors.New("RUNTIME_POLICY_UNSUPPORTED")
	}
	if req.RuntimePolicy.Policy != nil {
		if state.Actual == nil || req.RuntimePolicy.Policy.StopTimeoutSeconds != state.Actual.StopTimeoutSeconds {
			return Result{}, errors.New("RUNTIME_POLICY_STOP_TIMEOUT_IMMUTABLE")
		}
		state.Configured = req.RuntimePolicy.Policy
	}
	switch req.Action {
	case "APP_STOP":
		state.Held = true
	case "APP_START", "APP_RESTART":
		state.Held = false
	}
	state.ObservedAt = ""
	if err := x.checkDockerRuntimeBoot(ctx, runtimePolicyEffective(*state.Configured, state.Held, "DOCKER")); err != nil {
		return Result{}, err
	}
	if err := atomicJSON(filepath.Join(dir, "application-before.json"), c); err != nil {
		return Result{}, err
	}
	if err := x.saveApplicationPolicy(r.InstanceID, state); err != nil {
		return Result{}, err
	}
	p := runtimePolicyEffective(*state.Configured, state.Held, "DOCKER")
	if err := x.applyRuntimePolicy(ctx, SoftwareRequest{ID: r.InstanceID, Kind: "DOCKER", Locator: c.ID}, p); err != nil {
		return Result{}, err
	}
	command := ""
	switch req.Action {
	case "APP_STOP":
		if c.Running {
			command = "stop"
		}
	case "APP_START":
		if !c.Running {
			command = "start"
		}
	case "APP_RESTART":
		command = "restart"
	}
	if command != "" {
		if _, err := x.softwareRunner(ctx, "/usr/bin/docker", command, c.ID); err != nil {
			return Result{}, err
		}
	}
	return x.verifyApplicationLifecycle(req, dir)
}

func (x *executor) verifyApplicationLifecycle(req Request, dir string) (Result, error) {
	r := *req.Candidate
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := x.applicationContainer(ctx, r)
	if err != nil {
		return Result{}, err
	}
	var before applicationContainer
	if err := readJSON(filepath.Join(dir, "application-before.json"), &before); err != nil {
		return Result{}, err
	}
	state, err := x.applicationPolicy(ctx, r, c)
	if err != nil {
		return Result{}, err
	}
	if state.Configured == nil || state.Actual == nil || c.ID != before.ID || c.Paused || Digest(*state.Actual) != Digest(runtimePolicyEffective(*state.Configured, state.Held, "DOCKER")) {
		return Result{}, errors.New("RUNTIME_POLICY_UNCONFIRMED")
	}
	if req.RuntimePolicy.Policy != nil && Digest(*state.Configured) != Digest(*req.RuntimePolicy.Policy) || req.Action == "APP_STOP" && (!state.Held || c.Running) || (req.Action == "APP_START" || req.Action == "APP_RESTART") && (state.Held || !c.Running) || req.Action == "APP_RUNTIME_POLICY" && c.Running != before.Running {
		return Result{}, errors.New("APPLICATION_LIFECYCLE_UNCONFIRMED")
	}
	if req.Action == "APP_RESTART" {
		started, parseErr := time.Parse(time.RFC3339Nano, c.StartedAt)
		if parseErr != nil || started.IsZero() || c.StartedAt == before.StartedAt {
			return Result{}, errors.New("SOFTWARE_RESTART_UNCONFIRMED")
		}
	}
	actual := Result{ReleaseID: r.ID, ImageID: r.ImageID, ConfigHash: Digest(r.Config), ContainerID: c.ID, Health: "STOPPED", ObservedAt: stamp()}
	if c.Running {
		actual, err = x.healthy(r)
		if err != nil {
			return Result{}, err
		}
	}
	state.ObservedAt = stamp()
	if err := x.saveApplicationPolicy(r.InstanceID, state); err != nil {
		return Result{}, err
	}
	actual.RuntimePolicy = state
	if req.Action == "APP_RESTART" {
		actual.Lifecycle = &SoftwareLifecycleEvidence{Action: req.Action, Before: before.StartedAt, After: c.StartedAt, ObservedAt: stamp()}
	}
	return actual, nil
}

func (x *executor) executeApplicationLifecycle(req Request, output io.Writer) error {
	if !ID.MatchString(req.JobID) || !Hex.MatchString(req.InputHash) || !applicationLifecycleAction(req.ApplicationOperation) || req.RuntimePolicy == nil || req.RuntimePolicy.Version != 1 || req.RuntimePolicy.Policy != nil && req.RuntimePolicy.Policy.Validate("DOCKER") != nil || req.Software != nil || req.Installation != nil || req.Baseline != nil {
		return errors.New("APPLICATION_LIFECYCLE_INPUT_INVALID")
	}
	lock, err := targetLock(x.root)
	if err != nil {
		return err
	}
	defer lock.Close()
	dir := filepath.Join(x.root, "jobs", req.JobID)
	resultFile := filepath.Join(dir, "result.json")
	pendingFile := filepath.Join(x.root, "pending.json")
	var intent Request
	readErr := readJSON(filepath.Join(dir, "intent.json"), &intent)
	if readErr == nil && req.Candidate == nil && (req.Action == "ACK" || req.Action == "STATUS") {
		req.Candidate = intent.Candidate
	}
	if validateRelease(req.Candidate) != nil || req.Candidate.Config.Preview != nil || req.Candidate.Config.ZLM != nil {
		return errors.New("APPLICATION_LIFECYCLE_INPUT_INVALID")
	}
	if readErr == nil {
		if intent.InputHash != req.InputHash || intent.ApplicationOperation != req.ApplicationOperation || intent.Action != req.ApplicationOperation || intent.Candidate == nil || Digest(intent.Candidate) != Digest(req.Candidate) {
			return errors.New("TARGET_INTENT_MISMATCH")
		}
		var res Result
		if req.Action == "STATUS" || req.Action == "ACK" {
			if err := readJSON(resultFile, &res); err != nil || res.Status == "UNKNOWN" {
				actual, verifyErr := x.verifyApplicationLifecycle(intent, dir)
				if verifyErr != nil {
					return errors.New("APPLICATION_LIFECYCLE_UNCONFIRMED")
				}
				res = applicationLifecycleResult(intent, actual)
				if err := atomicJSON(resultFile, res); err != nil {
					return err
				}
			}
			if res.ProtocolVersion != 1 || res.ExecutorVersion != Version || res.ControllerID != intent.ControllerID || res.TargetID != intent.TargetID || res.JobID != intent.JobID || res.InputHash != intent.InputHash {
				return errors.New("RESULT_IDENTITY_MISMATCH")
			}
			if req.Action == "ACK" {
				if Digest(res) != req.ResultHash {
					return errors.New("ACK_RESULT_MISMATCH")
				}
				var ack struct{ ResultHash string }
				if readJSON(filepath.Join(dir, "ack.json"), &ack) == nil {
					if ack.ResultHash != req.ResultHash {
						return errors.New("ACK_CONFLICT")
					}
				} else {
					// The protected final receipt records verification time. A later
					// natural exit must not keep an already completed job locked.
					if res.Status != "SUCCEEDED" && (res.Status != "FAILED" || res.Phase != "NOT_STARTED") {
						return errors.New("RESULT_NOT_FINAL")
					}
					var pending Request
					if readJSON(pendingFile, &pending) != nil || pending.JobID != req.JobID || pending.InputHash != req.InputHash {
						return errors.New("PENDING_IDENTITY_MISMATCH")
					}
					if err := atomicJSON(filepath.Join(dir, "ack.json"), map[string]string{"ResultHash": req.ResultHash}); err != nil {
						return err
					}
				}
				var pending Request
				if readJSON(pendingFile, &pending) == nil && pending.JobID == req.JobID && pending.InputHash == req.InputHash {
					if err := os.Remove(pendingFile); err != nil {
						return err
					}
				}
				res.Ack = true
			}
			return json.NewEncoder(output).Encode(res)
		}
		if err := readJSON(resultFile, &res); err != nil {
			return errors.New("UNKNOWN_RECONCILE_REQUIRED")
		}
		return json.NewEncoder(output).Encode(res)
	}
	if !os.IsNotExist(readErr) {
		return readErr
	}
	frozen := req
	frozen.InputHash, frozen.Secrets = "", nil
	frozen.Action = req.ApplicationOperation
	if Digest(frozen) != req.InputHash {
		return errors.New("INPUT_HASH_MISMATCH")
	}
	if req.Action == "STATUS" {
		if _, err := os.Lstat(pendingFile); !os.IsNotExist(err) {
			return errors.New("TARGET_PENDING")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		c, err := x.applicationContainer(ctx, *req.Candidate)
		if err != nil {
			return err
		}
		state, err := x.applicationPolicy(ctx, *req.Candidate, c)
		if err != nil {
			return err
		}
		frozen.InputHash = req.InputHash
		res := applicationLifecycleResult(frozen, Result{ReleaseID: req.Candidate.ID, ImageID: c.Image, ContainerID: c.ID, ConfigHash: Digest(req.Candidate.Config), RuntimePolicy: state, Health: "STOPPED"})
		if c.Running {
			res.Health = "PROCESS_RUNNING"
		}
		res.Status, res.Phase, res.ErrorCode = "FAILED", "NOT_STARTED", "TARGET_NOT_STARTED"
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := atomicJSON(pendingFile, frozen); err != nil {
			return err
		}
		if err := atomicJSON(filepath.Join(dir, "intent.json"), frozen); err != nil {
			return err
		}
		if err := atomicJSON(resultFile, res); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(res)
	}
	if !applicationLifecycleAction(req.Action) || req.Action != req.ApplicationOperation {
		return errors.New("APPLICATION_LIFECYCLE_INPUT_INVALID")
	}
	if req.Action == "APP_RUNTIME_POLICY" && req.RuntimePolicy.Policy == nil {
		return errors.New("RUNTIME_POLICY_REQUIRED")
	}
	deadline, err := time.Parse(time.RFC3339, req.DeadlineAt)
	if err != nil || time.Now().After(deadline) {
		return errors.New("DEADLINE_EXCEEDED")
	}
	if _, err := os.Lstat(pendingFile); !os.IsNotExist(err) {
		return errors.New("TARGET_PENDING")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := atomicJSON(pendingFile, req); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "intent.json"), req); err != nil {
		return err
	}
	if err := x.checkpoint(req, "APPLICATION_CONTROL"); err != nil {
		return err
	}
	actual, changeErr := x.changeApplicationLifecycle(req, dir)
	res := applicationLifecycleResult(req, actual)
	if changeErr != nil {
		res.Status, res.Phase, res.ErrorCode = "UNKNOWN", "APPLICATION_RECONCILE_REQUIRED", changeErr.Error()
		if _, err := os.Lstat(filepath.Join(dir, "application-before.json")); os.IsNotExist(err) {
			res.Status, res.Phase = "FAILED", "NOT_STARTED"
		}
	}
	if err := atomicJSON(resultFile, res); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(res)
}

func applicationLifecycleResult(req Request, actual Result) Result {
	actual.ProtocolVersion, actual.ExecutorVersion = 1, Version
	actual.ControllerID, actual.TargetID, actual.JobID, actual.InputHash = req.ControllerID, req.TargetID, req.JobID, req.InputHash
	actual.Status, actual.Phase, actual.ObservedAt = "SUCCEEDED", "APPLICATION_STATE_VERIFIED", stamp()
	return actual
}
