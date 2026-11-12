package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

// Configuration commands may replace a unit or container. Capture its current
// policy first, so template defaults cannot replace the instance's selection.
func (x *executor) preserveRuntimePolicy(req Request) error {
	if req.Software == nil || req.Software.Kind == "TOOL" || req.Reconfiguration == nil && !managementRebind(req) {
		return nil
	}
	if err := x.boundSoftware(*req.Software); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	state, err := x.softwareRuntimeState(ctx, *req.Software)
	if err != nil {
		return err
	}
	if !state.Editable || state.Configured == nil {
		return errors.New("RUNTIME_POLICY_READ_FAILED")
	}
	if state.Held {
		return errors.New("RUNTIME_POLICY_HELD_CONFIGURATION")
	}
	if err = os.MkdirAll(filepath.Dir(x.runtimePolicyFile(req.Software.ID)), 0700); err != nil {
		return err
	}
	return atomicJSON(x.runtimePolicyFile(req.Software.ID), state)
}

type installedRuntimeIntent struct {
	InputHash    string              `json:"inputHash"`
	Result       Result              `json:"result"`
	Software     SoftwareRequest     `json:"software"`
	BaseIdentity string              `json:"baseIdentity"`
	State        runtimepolicy.State `json:"state"`
}

func runtimeInstallationChange(req Request) bool {
	return req.Installation != nil || req.Reconfiguration != nil || managementRebind(req)
}

func (x *executor) configuredInstallationPolicy(req Request, id, kind string) (*runtimepolicy.State, error) {
	var state runtimepolicy.State
	err := readJSON(x.runtimePolicyFile(id), &state)
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
	}
	if os.IsNotExist(err) && req.Installation != nil {
		state.Configured = req.Installation.Config.RuntimePolicy
	}
	if state.Configured == nil {
		if err == nil {
			return nil, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
		}
		return nil, nil
	}
	if state.Configured.Validate(kind) != nil {
		return nil, errors.New("RUNTIME_POLICY_RECORD_DAMAGED")
	}
	return &state, nil
}

func (x *executor) applyInstalledRuntimePolicy(req Request, dir string, result Result) Result {
	if !runtimeInstallationChange(req) || result.Status != "SUCCEEDED" || result.Installation == nil || result.Software == nil || result.Software.Kind == "TOOL" {
		return result
	}
	stored, err := x.configuredInstallationPolicy(req, result.Installation.SoftwareID, result.Software.Kind)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	if stored == nil {
		return result
	}
	var binding softwareBinding
	if readJSON(x.softwareFile(result.Installation.SoftwareID), &binding) != nil || binding.Software.IdentityHash != result.Software.IdentityHash {
		return unknownRuntimeInstallation(req, "SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	base, err := x.runtimeUnitBaseIdentity(ctx, binding.Software)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	intent := installedRuntimeIntent{InputHash: req.InputHash, Result: result, Software: binding.Software, BaseIdentity: base, State: *stored}
	if err = atomicJSON(filepath.Join(dir, "installation-runtime-intent.json"), intent); err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_UNCONFIRMED")
	}
	if err = os.MkdirAll(filepath.Dir(x.runtimePolicyFile(binding.Software.ID)), 0700); err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_UNCONFIRMED")
	}
	if err = atomicJSON(x.runtimePolicyFile(binding.Software.ID), stored); err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_UNCONFIRMED")
	}
	effective := runtimePolicyEffective(*stored.Configured, stored.Held, binding.Software.Kind)
	logged, closeLog := x.installationLogContext(ctx, req, "RUNTIME_POLICY")
	err = x.applyRuntimePolicy(logged, binding.Software, effective)
	if err == nil && stored.Held && result.Software.State != "STOPPED" {
		if binding.Software.Kind == "SYSTEMD" {
			command := softwareSystemdCommand(binding.Software, "stop")
			_, err = x.softwareRunner(logged, command[0], command[1:]...)
		} else {
			_, err = x.sourceSoftwareCommand(logged, "docker-stop", binding.Software.Locator)
		}
	}
	closeLog()
	if err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_UNCONFIRMED")
	}
	verified, err := x.completeInstalledRuntimePolicy(req, dir, intent)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	return verified
}

func unknownRuntimeInstallation(req Request, code string) Result {
	r := softwareResult(req)
	r.Phase, r.ErrorCode = "SOFTWARE_RECONCILE_REQUIRED", code
	return r
}

func (x *executor) completeInstalledRuntimePolicy(req Request, dir string, intent installedRuntimeIntent) (Result, error) {
	if intent.InputHash != req.InputHash || intent.Result.Installation == nil || intent.Result.Software == nil || intent.State.Configured == nil || intent.State.Configured.Validate(intent.Software.Kind) != nil || intent.Result.Installation.SoftwareID != intent.Software.ID || intent.Result.Installation.BindingRevision != intent.Software.Version || intent.Result.Installation.IdentityHash != intent.Software.IdentityHash || intent.Result.Software.IdentityHash != intent.Software.IdentityHash {
		return softwareResult(req), errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s := intent.Software
	s.IdentityHash = ""
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return softwareResult(req), err
	}
	base, err := x.runtimeUnitBaseIdentity(ctx, s)
	if err != nil || base != intent.BaseIdentity {
		return softwareResult(req), errors.New("SOFTWARE_IDENTITY_DRIFT")
	}
	state, err := x.readRuntimePolicy(ctx, s)
	if err != nil || state.Actual == nil {
		return softwareResult(req), errors.New("RUNTIME_POLICY_UNCONFIRMED")
	}
	effective := runtimePolicyEffective(*intent.State.Configured, intent.State.Held, s.Kind)
	desired := intent.Result.Software.State
	if intent.State.Held {
		desired = "STOPPED"
	}
	if !reflect.DeepEqual(*state.Actual, effective) || actual.State != desired {
		return softwareResult(req), errors.New("RUNTIME_POLICY_UNCONFIRMED")
	}
	var binding softwareBinding
	if readJSON(x.softwareFile(s.ID), &binding) != nil {
		return softwareResult(req), errors.New("SOFTWARE_NOT_ADOPTED")
	}
	previous := binding.Software
	s.IdentityHash = actual.IdentityHash
	if !reflect.DeepEqual(previous, intent.Software) && !reflect.DeepEqual(previous, s) {
		return softwareResult(req), errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	binding.Software = s
	state.Configured, state.Held = intent.State.Configured, intent.State.Held
	if err = atomicJSON(x.runtimePolicyFile(s.ID), state); err != nil {
		return softwareResult(req), err
	}
	if err = atomicJSON(x.softwareFile(s.ID), binding); err != nil {
		return softwareResult(req), err
	}
	r := intent.Result
	if actual.Version == "" {
		actual.Version, actual.SoftwareMetadata = intent.Result.Software.Version, intent.Result.Software.SoftwareMetadata
	}
	r.Software = actual
	r.Software.RuntimePolicy = state
	r.RuntimePolicy = state
	e := *r.Installation
	e.IdentityHash = actual.IdentityHash
	r.Installation = &e
	if managementRebind(req) {
		var receipt managementRebindReceipt
		file := filepath.Join(dir, "management-rebind.json")
		if readJSON(file, &receipt) != nil || receipt.validate(req) != nil || !reflect.DeepEqual(receipt.Binding.Software, intent.Software) && !reflect.DeepEqual(receipt.Binding.Software, s) {
			return softwareResult(req), errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
		}
		receipt.Binding, receipt.Installation, receipt.Software = binding, e, *actual
		if err = receipt.validate(req); err != nil {
			return softwareResult(req), err
		}
		if err = atomicJSON(file, receipt); err != nil {
			return softwareResult(req), err
		}
	}
	if err = atomicJSON(filepath.Join(dir, "installation-runtime-verified.json"), r); err != nil {
		return softwareResult(req), err
	}
	return r, nil
}

// The installation may have finished just before the post-install boundary was
// recorded. Recovery can confirm an already matching policy, but cannot apply a
// missing one by replaying commands from an uncertain installation.
func (x *executor) verifyRecoveredInstallationPolicy(req Request, dir string, result Result) Result {
	if !runtimeInstallationChange(req) || result.Status != "SUCCEEDED" || result.Installation == nil || result.Software == nil || result.Software.Kind == "TOOL" {
		return result
	}
	state, err := x.configuredInstallationPolicy(req, result.Installation.SoftwareID, result.Software.Kind)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	if state == nil {
		return result
	}
	var binding softwareBinding
	if readJSON(x.softwareFile(result.Installation.SoftwareID), &binding) != nil || binding.Software.IdentityHash != result.Software.IdentityHash {
		return unknownRuntimeInstallation(req, "SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	actual, err := x.readRuntimePolicy(ctx, binding.Software)
	if err != nil || actual.Actual == nil || !reflect.DeepEqual(*actual.Actual, runtimePolicyEffective(*state.Configured, state.Held, binding.Software.Kind)) {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_UNCONFIRMED")
	}
	base, err := x.runtimeUnitBaseIdentity(ctx, binding.Software)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	intent := installedRuntimeIntent{InputHash: req.InputHash, Result: result, Software: binding.Software, BaseIdentity: base, State: *state}
	if err = atomicJSON(filepath.Join(dir, "installation-runtime-intent.json"), intent); err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_UNCONFIRMED")
	}
	if err = os.MkdirAll(filepath.Dir(x.runtimePolicyFile(binding.Software.ID)), 0700); err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_UNCONFIRMED")
	}
	verified, err := x.completeInstalledRuntimePolicy(req, dir, intent)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	return verified
}

func (x *executor) reconcileInstalledRuntimePolicy(req Request, dir string) (Result, bool) {
	var intent installedRuntimeIntent
	err := readJSON(filepath.Join(dir, "installation-runtime-intent.json"), &intent)
	if os.IsNotExist(err) {
		return Result{}, false
	}
	if err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_RECEIPT_INVALID"), true
	}
	r, err := x.completeInstalledRuntimePolicy(req, dir, intent)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error()), true
	}
	return r, true
}
