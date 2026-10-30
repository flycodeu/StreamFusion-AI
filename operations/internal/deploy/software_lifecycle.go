package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func (x *executor) softwareFile(id string) string {
	return filepath.Join(x.root, "software", id+".json")
}

func (x *executor) boundSoftware(s SoftwareRequest) error {
	var binding softwareBinding
	if readJSON(x.softwareFile(s.ID), &binding) != nil {
		return errors.New("SOFTWARE_NOT_ADOPTED")
	}
	if !reflect.DeepEqual(binding.Software, s) {
		return errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	return nil
}

func (x *executor) applySoftware(req Request, dir string) (Result, error) {
	if req.Uninstallation != nil {
		return x.uninstallSoftware(req, dir)
	}
	if req.Reconfiguration != nil {
		return x.configureSoftware(req, dir)
	}
	if req.Installation != nil {
		return x.installService(req, dir)
	}
	s := *req.Software
	result := softwareResult(req)
	if s.Kind == "TOOL" && req.Action != "SOFTWARE_UNMANAGE" {
		return result, errors.New("SOFTWARE_TOOL_HAS_NO_LIFECYCLE")
	}
	if req.Action != "SOFTWARE_ADOPT" {
		if err := x.boundSoftware(s); err != nil {
			return result, err
		}
	}
	if req.Action == "SOFTWARE_UNMANAGE" {
		// A durable detach intent permits recovery after a crash between removing
		// the binding and writing the final result. No lifecycle command runs.
		if err := atomicJSON(filepath.Join(dir, "software-detach.json"), s); err != nil {
			return result, err
		}
		if err := os.Remove(x.softwareFile(s.ID)); err != nil {
			if _, checkErr := os.Lstat(x.softwareFile(s.ID)); os.IsNotExist(checkErr) {
				result.Phase, result.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "SOFTWARE_DETACH_UNCONFIRMED"
				return result, nil
			}
			return result, err
		}
		result.Status, result.Phase = "SUCCEEDED", "SOFTWARE_UNMANAGED"
		return result, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return result, err
	}
	if actual.State == "UNKNOWN" {
		return result, errors.New("SOFTWARE_STATE_TRANSITIONING")
	}
	if req.Action == "SOFTWARE_ADOPT" {
		if err = x.adoptSoftware(req); err != nil {
			if x.boundSoftware(s) == nil {
				result.Phase, result.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "SOFTWARE_BINDING_PERSISTENCE_UNCONFIRMED"
				return result, nil
			}
			return result, err
		}
		result.Status, result.Phase, result.Software = "SUCCEEDED", "SOFTWARE_ADOPTED", actual
		return result, nil
	}
	desired := "RUNNING"
	if req.Action == "SOFTWARE_STOP" {
		desired = "STOPPED"
	}
	if req.Action == "SOFTWARE_STOP" && s.SoftwareKey == "DOCKER" && s.Ownership != nil && s.Ownership.RuntimeMode == "NATIVE" {
		if err = x.dockerEngineHasNoContainers(ctx); err != nil {
			return result, err
		}
	}
	engineStop := req.Action == "SOFTWARE_STOP" && ownedDockerEngine(s)
	if actual.State != desired || engineStop && !x.softwareDockerSocketInactive(ctx) {
		// Persist the command boundary before invoking it. Lost or timed-out
		// replies are reconciled by observing, never by replaying start/stop.
		if err = atomicJSON(filepath.Join(dir, "software-command.json"), s); err != nil {
			return result, err
		}
		if s.Kind == "SYSTEMD" {
			action := "start"
			if req.Action == "SOFTWARE_STOP" {
				action = "stop"
			}
			_, err = x.softwareRunner(ctx, "/usr/bin/systemctl", softwareSystemdAction(s, action)...)
		} else {
			args := []string{"container", "start", "--", s.Locator}
			if req.Action == "SOFTWARE_STOP" {
				args = []string{"container", "stop", "--time", "20", "--", s.Locator}
			}
			_, err = x.softwareRunner(ctx, "/usr/bin/docker", args...)
		}
		// Use a fresh bounded observation even if the command consumed its
		// deadline; a timeout itself cannot prove the requested state.
		observeCtx, observeCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer observeCancel()
		actual, err = ObserveSoftware(observeCtx, s, x.softwareRunner)
		if err != nil || actual.State != desired || engineStop && !x.softwareDockerSocketInactive(observeCtx) {
			result.Phase, result.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", "SOFTWARE_COMMAND_UNCONFIRMED"
			return result, nil
		}
	}
	result.DockerSocketInactive = engineStop
	result.Status, result.Phase, result.Software = "SUCCEEDED", "SOFTWARE_STATE_VERIFIED", actual
	return result, nil
}

func (x *executor) adoptSoftware(req Request) error {
	s := *req.Software
	if err := os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(x.root, "software", "*.json"))
	if err != nil {
		return err
	}
	for _, file := range files {
		var binding softwareBinding
		if readJSON(file, &binding) != nil {
			return errors.New("SOFTWARE_BINDING_DAMAGED")
		}
		if binding.Software.ID == s.ID {
			if !reflect.DeepEqual(binding.Software, s) {
				return errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
			}
			return nil
		}
		if binding.Software.Kind == s.Kind && binding.Software.Locator == s.Locator {
			return errors.New("SOFTWARE_OBJECT_ALREADY_ADOPTED")
		}
	}
	return atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID})
}

func (x *executor) verifySoftwareResult(req Request, result Result) error {
	if req.Uninstallation != nil {
		return x.verifySoftwareUninstall(req, result)
	}
	if req.Reconfiguration != nil {
		return x.verifyConfiguration(req, result)
	}
	if req.Installation != nil {
		return x.verifyServiceInstallation(req, result)
	}
	s := *req.Software
	if req.SoftwareOperation == "SOFTWARE_UNMANAGE" {
		if _, err := os.Lstat(x.softwareFile(s.ID)); !os.IsNotExist(err) {
			return errors.New("SOFTWARE_BINDING_STILL_PRESENT")
		}
		return nil
	}
	if err := x.boundSoftware(s); err != nil {
		return err
	}
	// A final receipt records what was verified at execution time. A service
	// may subsequently exit naturally; that belongs to a new observation and
	// must not make the original operation impossible to acknowledge forever.
	if result.Software == nil || s.IdentityHash != result.Software.IdentityHash || s.Kind != result.Software.Kind || s.Locator != result.Software.Locator {
		return errors.New("SOFTWARE_IDENTITY_DRIFT")
	}
	if req.SoftwareOperation == "SOFTWARE_START" && result.Software.State != "RUNNING" || req.SoftwareOperation == "SOFTWARE_STOP" && result.Software.State != "STOPPED" {
		return errors.New("SOFTWARE_ACTUAL_STATE_DRIFT")
	}
	if req.SoftwareOperation == "SOFTWARE_STOP" && ownedDockerEngine(s) && !result.DockerSocketInactive {
		return errors.New("DOCKER_SOCKET_STOP_UNCONFIRMED")
	}
	return nil
}

func (x *executor) reconcileSoftware(req Request, dir string) Result {
	if req.Uninstallation != nil {
		return x.reconcileSoftwareUninstall(req, dir)
	}
	if req.Reconfiguration != nil {
		return x.reconcileConfiguration(req, dir)
	}
	if req.Installation != nil {
		return x.reconcileServiceInstallation(req, dir)
	}
	result := softwareResult(req)
	result.ErrorCode = "SOFTWARE_JOB_UNKNOWN"
	if req.SoftwareOperation == "SOFTWARE_UNMANAGE" {
		var detached SoftwareRequest
		if readJSON(filepath.Join(dir, "software-detach.json"), &detached) != nil || !reflect.DeepEqual(detached, *req.Software) {
			return result
		}
		if _, err := os.Lstat(x.softwareFile(detached.ID)); !os.IsNotExist(err) {
			return result
		}
		result.Status, result.Phase, result.ErrorCode = "SUCCEEDED", "SOFTWARE_UNMANAGED", ""
		return result
	}
	if x.boundSoftware(*req.Software) != nil {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil {
		return result
	}
	if actual.State == "UNKNOWN" {
		return result
	}
	if req.SoftwareOperation == "SOFTWARE_START" && actual.State != "RUNNING" || req.SoftwareOperation == "SOFTWARE_STOP" && actual.State != "STOPPED" {
		return result
	}
	if req.SoftwareOperation == "SOFTWARE_STOP" && ownedDockerEngine(*req.Software) {
		if !x.softwareDockerSocketInactive(ctx) {
			return result
		}
		result.DockerSocketInactive = true
	}
	result.Status, result.Phase, result.ErrorCode, result.Software = "SUCCEEDED", "SOFTWARE_RECONCILED", "", actual
	return result
}

func ownedDockerEngine(s SoftwareRequest) bool {
	return s.SoftwareKey == "DOCKER" && s.Kind == "SYSTEMD" && s.Locator == "docker.service" && s.Ownership != nil && s.Ownership.RuntimeMode == "NATIVE" && s.Ownership.BinaryPath == "/usr/bin/dockerd"
}

func softwareSystemdAction(s SoftwareRequest, action string) []string {
	if action == "stop" && ownedDockerEngine(s) {
		return []string{"stop", "--", "docker.service", "docker.socket"}
	}
	return []string{action, "--", s.Locator}
}

func (x *executor) softwareDockerSocketInactive(ctx context.Context) bool {
	out, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=ActiveState", "--", "docker.socket")
	return err == nil && strings.TrimSpace(string(out)) == "ActiveState=inactive"
}
