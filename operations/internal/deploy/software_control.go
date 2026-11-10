package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

type SoftwareLifecycleEvidence struct {
	Action     string `json:"action"`
	Before     string `json:"before"`
	After      string `json:"after"`
	ObservedAt string `json:"observedAt"`
}

type softwareControlIntent struct {
	Action string `json:"action"`
	Before string `json:"before"`
}

func softwareControlAction(action string) bool {
	return action == "SOFTWARE_RESTART" || action == "SOFTWARE_RELOAD" || action == "SOFTWARE_PAUSE" || action == "SOFTWARE_UNPAUSE"
}

func (x *executor) softwareStartIdentity(ctx context.Context, s SoftwareRequest) (string, error) {
	name := "systemd-start-identity"
	if s.Kind == "DOCKER" {
		name = "docker-start-identity"
	}
	data, err := x.sourceSoftwareCommand(ctx, name, s.Locator)
	value := strings.TrimSpace(string(data))
	if err != nil || value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("SOFTWARE_START_IDENTITY_UNAVAILABLE")
	}
	if s.Kind == "SYSTEMD" {
		if len(value) != 32 || strings.Trim(value, "0") == "" || !Hex.MatchString(value+value) {
			return "", errors.New("SOFTWARE_START_IDENTITY_UNAVAILABLE")
		}
	} else if started, e := time.Parse(time.RFC3339Nano, value); e != nil || started.IsZero() {
		return "", errors.New("SOFTWARE_START_IDENTITY_UNAVAILABLE")
	}
	return value, nil
}

func lifecycleState(action string) string {
	if action == "SOFTWARE_PAUSE" {
		return "PAUSED"
	}
	return "RUNNING"
}

func validLifecycleEvidence(req Request, result Result) bool {
	e := result.Lifecycle
	if e == nil || e.Action != req.SoftwareOperation || e.ObservedAt == "" || result.Software == nil || result.Software.State != lifecycleState(req.SoftwareOperation) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, e.ObservedAt); err != nil {
		return false
	}
	return req.SoftwareOperation != "SOFTWARE_RESTART" || e.Before != "" && e.After != "" && e.Before != e.After
}

func (x *executor) applySoftwareControl(req Request, dir string) (Result, error) {
	r := softwareResult(req)
	s := *req.Software
	if s.Kind == "TOOL" || req.Action == "SOFTWARE_RELOAD" && s.Kind != "SYSTEMD" || (req.Action == "SOFTWARE_PAUSE" || req.Action == "SOFTWARE_UNPAUSE") && s.Kind != "DOCKER" {
		return r, errors.New("SOFTWARE_ACTION_UNSUPPORTED")
	}
	if err := x.boundSoftware(s); err != nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return r, err
	}
	if actual.State == "UNKNOWN" {
		return r, errors.New("SOFTWARE_STATE_TRANSITIONING")
	}
	if ownedDockerEngine(s) && req.Action == "SOFTWARE_RESTART" {
		if err = x.dockerEngineHasNoContainers(ctx); err != nil {
			return r, err
		}
	}
	if (req.Action == "SOFTWARE_RELOAD" || req.Action == "SOFTWARE_PAUSE") && actual.State != "RUNNING" && !(req.Action == "SOFTWARE_PAUSE" && actual.State == "PAUSED") {
		return r, errors.New("SOFTWARE_STATE_TRANSITIONING")
	}
	if req.Action == "SOFTWARE_RESTART" && actual.State == "PAUSED" {
		return r, errors.New("SOFTWARE_RESUME_REQUIRED")
	}
	if req.Action == "SOFTWARE_RELOAD" {
		canReload, e := x.sourceSoftwareCommand(ctx, "systemd-can-reload", s.Locator)
		if e != nil || strings.TrimSpace(string(canReload)) != "yes" {
			return r, errors.New("SOFTWARE_RELOAD_UNSUPPORTED")
		}
	}
	before, identityErr := x.softwareStartIdentity(ctx, s)
	if req.Action == "SOFTWARE_RESTART" && identityErr != nil {
		if actual.State == "STOPPED" {
			before = "STOPPED"
		} else {
			return r, identityErr
		}
	}
	intent := softwareControlIntent{Action: req.Action, Before: before}
	if err = atomicJSON(filepath.Join(dir, "software-control.json"), intent); err != nil {
		return r, err
	}
	needsCommand := req.Action == "SOFTWARE_RESTART" || req.Action == "SOFTWARE_RELOAD" || actual.State != lifecycleState(req.Action)
	if needsCommand {
		logged, closeLog := x.installationLogContext(ctx, req, "SOFTWARE_CONTROL")
		action := strings.ToLower(strings.TrimPrefix(req.Action, "SOFTWARE_"))
		if s.Kind == "SYSTEMD" {
			_, err = x.sourceSoftwareCommand(logged, "systemd-action", action, s.Locator)
		} else {
			_, err = x.sourceSoftwareCommand(logged, "docker-"+action, s.Locator)
		}
		closeLog()
		if req.Action == "SOFTWARE_RELOAD" && err != nil {
			r.Phase, r.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "SOFTWARE_COMMAND_UNCONFIRMED"
			return r, nil
		}
	}
	observeCtx, observeCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer observeCancel()
	actual, err = ObserveSoftware(observeCtx, s, x.softwareRunner)
	if err != nil || actual.State != lifecycleState(req.Action) {
		r.Phase, r.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "SOFTWARE_COMMAND_UNCONFIRMED"
		return r, nil
	}
	after, _ := x.softwareStartIdentity(observeCtx, s)
	r.Software = actual
	r.Lifecycle = &SoftwareLifecycleEvidence{Action: req.Action, Before: before, After: after, ObservedAt: stamp()}
	if !validLifecycleEvidence(req, r) {
		r.Phase, r.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "SOFTWARE_RESTART_UNCONFIRMED"
		return r, nil
	}
	if err = atomicJSON(filepath.Join(dir, "software-control-verified.json"), r.Lifecycle); err != nil {
		r.Phase, r.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "SOFTWARE_CONTROL_RECEIPT_UNCONFIRMED"
		return r, nil
	}
	r.Status, r.Phase = "SUCCEEDED", "SOFTWARE_STATE_VERIFIED"
	return r, nil
}

func (x *executor) reconcileSoftwareControl(req Request, dir string) Result {
	r := softwareResult(req)
	r.ErrorCode = "SOFTWARE_COMMAND_UNCONFIRMED"
	if x.boundSoftware(*req.Software) != nil {
		return r
	}
	var intent softwareControlIntent
	if readJSON(filepath.Join(dir, "software-control.json"), &intent) != nil || intent.Action != req.SoftwareOperation {
		return r
	}
	var verified SoftwareLifecycleEvidence
	hasVerified := readJSON(filepath.Join(dir, "software-control-verified.json"), &verified) == nil
	if req.SoftwareOperation == "SOFTWARE_RELOAD" && !hasVerified {
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil || actual.State != lifecycleState(req.SoftwareOperation) {
		return r
	}
	after, _ := x.softwareStartIdentity(ctx, *req.Software)
	r.Software = actual
	if hasVerified {
		r.Lifecycle = &verified
	} else {
		r.Lifecycle = &SoftwareLifecycleEvidence{Action: req.SoftwareOperation, Before: intent.Before, After: after, ObservedAt: stamp()}
	}
	if !validLifecycleEvidence(req, r) {
		return r
	}
	r.Status, r.Phase, r.ErrorCode = "SUCCEEDED", "SOFTWARE_RECONCILED", ""
	return r
}
