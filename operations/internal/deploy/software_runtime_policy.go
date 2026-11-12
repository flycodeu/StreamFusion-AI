package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

func runtimePolicyEffective(p runtimepolicy.Policy, held bool, kind string) runtimepolicy.Policy {
	if held {
		p.AutoStart = false
		if kind == "DOCKER" {
			p.Restart, p.MaxRetries = "no", 0
		}
	}
	return p
}

func policySeconds(value string) (int, bool) {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "min", "m"), " ", "")
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 || d > 24*time.Hour || d%time.Second != 0 {
		return 0, false
	}
	return int(d.Round(time.Second) / time.Second), true
}

func (x *executor) readRuntimePolicy(ctx context.Context, s SoftwareRequest) (*runtimepolicy.State, error) {
	state := &runtimepolicy.State{ObservedAt: stamp()}
	if s.Kind == "DOCKER" {
		out, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/docker", "container", "inspect", "--format", `{"restart":{{json .HostConfig.RestartPolicy.Name}},"maxRetries":{{json .HostConfig.RestartPolicy.MaximumRetryCount}},"stopTimeout":{{json .Config.StopTimeout}}}`, "--", s.Locator)
		var actual struct {
			Restart     string `json:"restart"`
			MaxRetries  int    `json:"maxRetries"`
			StopTimeout *int   `json:"stopTimeout"`
		}
		if err != nil || json.Unmarshal(out, &actual) != nil || actual.Restart == "" {
			return nil, errors.New("RUNTIME_POLICY_READ_FAILED")
		}
		p := runtimepolicy.Policy{Restart: actual.Restart, AutoStart: actual.Restart == "always" || actual.Restart == "unless-stopped", MaxRetries: actual.MaxRetries, StopTimeoutSeconds: 10}
		if actual.StopTimeout != nil {
			p.StopTimeoutSeconds = *actual.StopTimeout
		}
		if p.Validate(s.Kind) != nil {
			state.Reason = "当前容器策略超出可编辑范围"
			return state, nil
		}
		state.Actual, state.Configured, state.Editable = &p, &p, true
		return state, nil
	}
	if s.Kind != "SYSTEMD" {
		state.Reason = "此软件没有常驻进程"
		return state, nil
	}
	out, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/systemctl", "show", "--no-pager", "--property=UnitFileState,Restart,RestartUSec,TimeoutStopUSec", "--", s.Locator)
	if err != nil {
		return nil, errors.New("RUNTIME_POLICY_READ_FAILED")
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			if _, exists := props[key]; exists {
				return nil, errors.New("RUNTIME_POLICY_READ_FAILED")
			}
			props[key] = value
		}
	}
	p := runtimepolicy.Policy{Restart: props["Restart"], AutoStart: props["UnitFileState"] == "enabled"}
	delay, delayOK := policySeconds(props["RestartUSec"])
	stop, stopOK := policySeconds(props["TimeoutStopUSec"])
	p.RestartDelaySeconds, p.StopTimeoutSeconds = delay, stop
	if !delayOK || !stopOK || p.Validate(s.Kind) != nil {
		state.Reason = "当前服务策略超出可编辑范围"
		return state, nil
	}
	state.Actual, state.Configured = &p, &p
	if props["UnitFileState"] != "enabled" && props["UnitFileState"] != "disabled" {
		state.Reason = "此服务不支持持久开机启动配置"
		return state, nil
	}
	state.Editable = true
	return state, nil
}

// The path and file marker are fixed by the executor, never supplied as shell
// source. The regular root-owned drop-in survives unit/template replacement.

func (x *executor) applyRuntimePolicy(ctx context.Context, s SoftwareRequest, p runtimepolicy.Policy) error {
	if p.Validate(s.Kind) != nil {
		return errors.New("RUNTIME_POLICY_INVALID")
	}
	if s.Kind == "DOCKER" {
		mode := p.Restart
		if p.MaxRetries > 0 {
			mode += ":" + strconv.Itoa(p.MaxRetries)
		}
		args := []string{"container", "update", "--restart", mode}
		// Docker exposes stop timeout on create, not container update. Avoid
		// claiming an in-place timeout edit that the Engine cannot perform.
		current, err := x.readRuntimePolicy(ctx, s)
		if err != nil || current.Actual == nil || p.StopTimeoutSeconds != current.Actual.StopTimeoutSeconds {
			return errors.New("RUNTIME_POLICY_STOP_TIMEOUT_IMMUTABLE")
		}
		if err = x.checkDockerRuntimeBoot(ctx, p); err != nil {
			return err
		}
		_, err = x.softwareRunner(ctx, "/usr/bin/docker", append(args, "--", s.Locator)...)
		return err
	}
	current, err := x.readRuntimePolicy(ctx, s)
	if err != nil || !current.Editable {
		return errors.New("RUNTIME_POLICY_UNSUPPORTED")
	}
	socketEnabled := false
	if ownedDockerEngine(s) {
		socketEnabled, err = x.savedDockerSocketPolicy(ctx, s)
		if err != nil {
			return err
		}
	}
	_, err = x.softwareRunner(ctx, "/usr/bin/bash", "-c", deployScript("runtime-policy/systemd-write.sh"), "ops-runtime-policy", s.Locator, s.Locator, p.Restart, strconv.Itoa(p.RestartDelaySeconds), strconv.Itoa(p.StopTimeoutSeconds))
	if err != nil {
		return err
	}
	action := "disable"
	if p.AutoStart {
		action = "enable"
	}
	_, err = x.softwareRunner(ctx, "/usr/bin/systemctl", action, "--", s.Locator)
	if err == nil && ownedDockerEngine(s) {
		socketAction := "disable"
		if p.AutoStart && socketEnabled {
			socketAction = "enable"
		}
		_, err = x.softwareRunner(ctx, "/usr/bin/systemctl", socketAction, "--", "docker.socket")
	}
	return err
}

func (x *executor) checkDockerRuntimeBoot(ctx context.Context, p runtimepolicy.Policy) error {
	if !p.AutoStart {
		return nil
	}
	boot, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "is-enabled", "--", "docker.service")
	if err != nil || strings.TrimSpace(string(boot)) != "enabled" {
		return errors.New("RUNTIME_POLICY_DOCKER_BOOT_DISABLED")
	}
	return nil
}

func (x *executor) runtimePolicyFile(id string) string {
	return filepath.Join(x.root, "runtime-policies", id+".json")
}

func (x *executor) savedDockerSocketPolicy(ctx context.Context, s SoftwareRequest) (bool, error) {
	file := x.runtimePolicyFile(s.ID) + ".socket"
	var enabled bool
	err := readJSON(file, &enabled)
	if err == nil {
		return enabled, nil
	}
	if !os.IsNotExist(err) {
		return false, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
	}
	out, _ := x.softwareRunner(ctx, "/usr/bin/systemctl", "is-enabled", "--", "docker.socket")
	value := strings.TrimSpace(string(out))
	if value != "enabled" && value != "disabled" {
		return false, errors.New("DOCKER_SOCKET_POLICY_UNCONFIRMED")
	}
	enabled = value == "enabled"
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return false, err
	}
	return enabled, atomicJSON(file, enabled)
}

type softwareRuntimeIntent struct {
	InputHash     string              `json:"inputHash"`
	Before        SoftwareRequest     `json:"before"`
	BaseIdentity  string              `json:"baseIdentity"`
	State         runtimepolicy.State `json:"state"`
	DesiredState  string              `json:"desiredState"`
	StartIdentity string              `json:"startIdentity,omitempty"`
}

type softwareRuntimeReceipt struct {
	InputHash   string              `json:"inputHash"`
	Binding     softwareBinding     `json:"binding"`
	Observation SoftwareObservation `json:"observation"`
	State       runtimepolicy.State `json:"state"`
}

func (x *executor) softwareRuntimeState(ctx context.Context, s SoftwareRequest) (*runtimepolicy.State, error) {
	state, err := x.readRuntimePolicy(ctx, s)
	if err != nil {
		return nil, err
	}
	var stored runtimepolicy.State
	if err = readJSON(x.runtimePolicyFile(s.ID), &stored); err != nil && !os.IsNotExist(err) || err == nil && (stored.Configured == nil || stored.Configured.Validate(s.Kind) != nil) {
		return nil, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
	}
	if err == nil {
		state.Configured, state.Held = stored.Configured, stored.Held
	}
	return state, nil
}

func runtimePolicyAction(req Request) bool {
	return req.RuntimePolicy != nil && (req.SoftwareOperation == "SOFTWARE_RUNTIME_POLICY" || req.SoftwareOperation == "SOFTWARE_START" || req.SoftwareOperation == "SOFTWARE_STOP" || req.SoftwareOperation == "SOFTWARE_RESTART")
}

func runtimePolicyIntentMatches(req Request, state runtimepolicy.State) bool {
	if req.RuntimePolicy == nil || state.Configured == nil || state.Configured.Validate(req.Software.Kind) != nil {
		return false
	}
	if req.RuntimePolicy.Policy != nil && !reflect.DeepEqual(state.Configured, req.RuntimePolicy.Policy) {
		return false
	}
	switch req.SoftwareOperation {
	case "SOFTWARE_STOP":
		return state.Held
	case "SOFTWARE_START", "SOFTWARE_RESTART":
		return !state.Held
	}
	return true
}

// Identity still hashes every unit and drop-in. This additional comparison
// isolates the one executor-owned file when verifying an intentional rebind.
func (x *executor) runtimeUnitBaseIdentity(ctx context.Context, s SoftwareRequest) (string, error) {
	if s.Kind != "SYSTEMD" {
		s.IdentityHash = ""
		actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
		if err != nil {
			return "", err
		}
		return actual.IdentityHash, nil
	}
	s.IdentityHash = ""
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		out, err := x.softwareRunner(ctx, command, args...)
		if command == "/usr/bin/systemctl" && len(args) > 0 && args[0] == "show" {
			lines := strings.Split(string(out), "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, "DropInPaths=") {
					kept := []string{}
					for _, file := range strings.Fields(strings.TrimPrefix(line, "DropInPaths=")) {
						if file != "/etc/systemd/system/"+s.Locator+".d/90-streamfusion-ops-runtime.conf" {
							kept = append(kept, file)
						}
					}
					lines[i] = "DropInPaths=" + strings.Join(kept, " ")
				}
			}
			out = []byte(strings.Join(lines, "\n"))
		}
		return out, err
	}
	actual, err := ObserveSoftware(ctx, s, run)
	if err != nil {
		return "", err
	}
	return actual.IdentityHash, nil
}

func (x *executor) applySoftwareRuntimePolicy(req Request, dir string) (Result, error) {
	r := softwareResult(req)
	s := *req.Software
	if err := x.boundSoftware(s); err != nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	before, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return r, err
	}
	if before.State != "RUNNING" && before.State != "STOPPED" && before.State != "FAILED" {
		return r, errors.New("SOFTWARE_STATE_TRANSITIONING")
	}
	state, err := x.softwareRuntimeState(ctx, s)
	if err != nil || !state.Editable || state.Actual == nil {
		return r, errors.New("RUNTIME_POLICY_READ_FAILED")
	}
	if ownedDockerEngine(s) && (req.Action == "SOFTWARE_STOP" || req.Action == "SOFTWARE_RESTART") {
		if err = x.dockerEngineHasNoContainers(ctx); err != nil {
			return r, err
		}
	}
	if req.RuntimePolicy.Policy != nil {
		p := *req.RuntimePolicy.Policy
		state.Configured = &p
	}
	if s.Kind == "DOCKER" && state.Configured.StopTimeoutSeconds != state.Actual.StopTimeoutSeconds {
		return r, errors.New("RUNTIME_POLICY_STOP_TIMEOUT_IMMUTABLE")
	}
	if req.Action == "SOFTWARE_STOP" {
		state.Held = true
	}
	if req.Action == "SOFTWARE_START" || req.Action == "SOFTWARE_RESTART" {
		state.Held = false
	}
	desired := before.State
	if req.Action == "SOFTWARE_STOP" {
		desired = "STOPPED"
	}
	if req.Action == "SOFTWARE_START" || req.Action == "SOFTWARE_RESTART" {
		desired = "RUNNING"
	}
	effective := runtimePolicyEffective(*state.Configured, state.Held, s.Kind)
	if s.Kind == "DOCKER" {
		if err = x.checkDockerRuntimeBoot(ctx, effective); err != nil {
			return r, err
		}
	}
	base, err := x.runtimeUnitBaseIdentity(ctx, s)
	if err != nil {
		return r, err
	}
	intent := softwareRuntimeIntent{InputHash: req.InputHash, Before: s, BaseIdentity: base, State: *state, DesiredState: desired}
	if req.Action == "SOFTWARE_RESTART" {
		intent.StartIdentity, err = x.softwareStartIdentity(ctx, s)
		if err != nil && before.State != "STOPPED" {
			return r, err
		}
		if before.State == "STOPPED" {
			intent.StartIdentity = "STOPPED"
		}
	}
	if err = atomicJSON(filepath.Join(dir, "runtime-policy-intent.json"), intent); err != nil {
		return r, err
	}
	// Store the requested policy before touching the supervisor, so a lost
	// response can only be reconciled against this exact original intent.
	if err = os.MkdirAll(filepath.Dir(x.runtimePolicyFile(s.ID)), 0700); err != nil {
		return r, err
	}
	if err = atomicJSON(x.runtimePolicyFile(s.ID), state); err != nil {
		return r, err
	}
	logged, closeLog := x.installationLogContext(ctx, req, "RUNTIME_POLICY")
	err = x.applyRuntimePolicy(logged, s, effective)
	if err == nil && req.Action != "SOFTWARE_RUNTIME_POLICY" {
		action := strings.ToLower(strings.TrimPrefix(req.Action, "SOFTWARE_"))
		if s.Kind == "SYSTEMD" {
			command := softwareSystemdCommand(s, action)
			_, err = x.softwareRunner(logged, command[0], command[1:]...)
		} else {
			if action == "stop" || action == "restart" {
				_, err = x.sourceSoftwareCommand(logged, "runtime-docker-"+action, s.Locator, strconv.Itoa(effective.StopTimeoutSeconds))
			} else {
				_, err = x.sourceSoftwareCommand(logged, "docker-"+action, s.Locator)
			}
		}
	}
	closeLog()
	if err != nil {
		r.Phase, r.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "RUNTIME_POLICY_UNCONFIRMED"
		return r, nil
	}
	completed, completeErr := x.completeSoftwareRuntimePolicy(req, dir, intent)
	if completeErr != nil {
		r.Phase, r.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", completeErr.Error()
		return r, nil
	}
	return completed, nil
}

func (x *executor) completeSoftwareRuntimePolicy(req Request, dir string, intent softwareRuntimeIntent) (Result, error) {
	r := softwareResult(req)
	if intent.InputHash != req.InputHash || !reflect.DeepEqual(intent.Before, *req.Software) {
		return r, errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	if !runtimePolicyIntentMatches(req, intent.State) || req.SoftwareOperation == "SOFTWARE_STOP" && intent.DesiredState != "STOPPED" || (req.SoftwareOperation == "SOFTWARE_START" || req.SoftwareOperation == "SOFTWARE_RESTART") && intent.DesiredState != "RUNNING" {
		return r, errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := intent.Before
	s.IdentityHash = ""
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return r, err
	}
	base, err := x.runtimeUnitBaseIdentity(ctx, s)
	if err != nil || base != intent.BaseIdentity {
		return r, errors.New("SOFTWARE_IDENTITY_DRIFT")
	}
	state, err := x.readRuntimePolicy(ctx, s)
	if err != nil || state.Actual == nil || intent.State.Configured == nil {
		return r, errors.New("RUNTIME_POLICY_UNCONFIRMED")
	}
	effective := runtimePolicyEffective(*intent.State.Configured, intent.State.Held, s.Kind)
	if !reflect.DeepEqual(*state.Actual, effective) || actual.State != intent.DesiredState {
		return r, errors.New("RUNTIME_POLICY_UNCONFIRMED")
	}
	if req.SoftwareOperation == "SOFTWARE_RESTART" {
		after, startErr := x.softwareStartIdentity(ctx, s)
		if startErr != nil || intent.StartIdentity == "" || intent.StartIdentity == after {
			return r, errors.New("SOFTWARE_RESTART_UNCONFIRMED")
		}
		r.Lifecycle = &SoftwareLifecycleEvidence{Action: req.SoftwareOperation, Before: intent.StartIdentity, After: after, ObservedAt: stamp()}
	}
	state.Configured, state.Held = intent.State.Configured, intent.State.Held
	var binding softwareBinding
	if readJSON(x.softwareFile(s.ID), &binding) != nil {
		return r, errors.New("SOFTWARE_NOT_ADOPTED")
	}
	candidate := intent.Before
	if candidate.IdentityHash != actual.IdentityHash {
		candidate.IdentityHash = actual.IdentityHash
		candidate.Version++
	}
	if !reflect.DeepEqual(binding.Software, intent.Before) && !reflect.DeepEqual(binding.Software, candidate) {
		return r, errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	binding.Software = candidate
	actual.RuntimePolicy = state
	receipt := softwareRuntimeReceipt{InputHash: req.InputHash, Binding: binding, Observation: *actual, State: *state}
	if err = atomicJSON(filepath.Join(dir, "runtime-policy-verified.json"), receipt); err != nil {
		return r, err
	}
	if err = atomicJSON(x.softwareFile(s.ID), binding); err != nil {
		return r, err
	}
	if err = atomicJSON(x.runtimePolicyFile(s.ID), state); err != nil {
		return r, err
	}
	r.Status, r.Phase, r.Software, r.RuntimePolicy = "SUCCEEDED", "RUNTIME_POLICY_UPDATED", actual, state
	if !reflect.DeepEqual(candidate, intent.Before) {
		r.RuntimePolicyBinding = &candidate
	}
	if ownedDockerEngine(s) && req.SoftwareOperation == "SOFTWARE_STOP" {
		r.DockerSocketInactive = x.softwareDockerSocketInactive(ctx)
		if !r.DockerSocketInactive {
			return softwareResult(req), errors.New("DOCKER_SOCKET_STOP_UNCONFIRMED")
		}
	}
	return r, nil
}

func (x *executor) reconcileSoftwareRuntimePolicy(req Request, dir string) Result {
	r := softwareResult(req)
	r.ErrorCode = "RUNTIME_POLICY_UNCONFIRMED"
	var intent softwareRuntimeIntent
	if readJSON(filepath.Join(dir, "runtime-policy-intent.json"), &intent) != nil {
		return r
	}
	// Reconciliation verifies the already performed transition. It never
	// repeats update, enable, stop or start after a lost reply.
	complete, err := x.completeSoftwareRuntimePolicy(req, dir, intent)
	if err == nil {
		return complete
	}
	return r
}

func (x *executor) verifyRuntimePolicyReceipt(req Request, result Result) error {
	var receipt softwareRuntimeReceipt
	if readJSON(filepath.Join(x.root, "jobs", req.JobID, "runtime-policy-verified.json"), &receipt) != nil || receipt.InputHash != req.InputHash || !reflect.DeepEqual(result.RuntimePolicy, &receipt.State) || !reflect.DeepEqual(result.Software, &receipt.Observation) || x.boundSoftware(receipt.Binding.Software) != nil {
		return errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	if result.RuntimePolicyBinding != nil && !reflect.DeepEqual(*result.RuntimePolicyBinding, receipt.Binding.Software) {
		return errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	if (result.RuntimePolicyBinding != nil) == reflect.DeepEqual(receipt.Binding.Software, *req.Software) {
		return errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	if req.SoftwareOperation == "SOFTWARE_RESTART" && !validLifecycleEvidence(req, result) {
		return errors.New("SOFTWARE_CONTROL_RECEIPT_INVALID")
	}
	if result.RuntimePolicy == nil || !runtimePolicyIntentMatches(req, *result.RuntimePolicy) || result.RuntimePolicy.Actual == nil || result.RuntimePolicy.Actual.Validate(req.Software.Kind) != nil {
		return errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	if !reflect.DeepEqual(*result.RuntimePolicy.Actual, runtimePolicyEffective(*result.RuntimePolicy.Configured, result.RuntimePolicy.Held, req.Software.Kind)) {
		return errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	if req.SoftwareOperation == "SOFTWARE_STOP" && result.Software.State != "STOPPED" || (req.SoftwareOperation == "SOFTWARE_START" || req.SoftwareOperation == "SOFTWARE_RESTART") && result.Software.State != "RUNNING" {
		return errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	return nil
}
