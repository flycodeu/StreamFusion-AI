package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

type runtimeCleanupIntent struct {
	InputHash string          `json:"inputHash"`
	Software  SoftwareRequest `json:"software"`
	Result    Result          `json:"result"`
}

func (x *executor) cleanupRuntimePolicyAfterUninstall(req Request, dir string, result Result) Result {
	if req.Uninstallation == nil || req.Software == nil || req.Software.Kind != "SYSTEMD" || result.Status != "SUCCEEDED" {
		return result
	}
	var policy runtimepolicy.State
	err := readJSON(x.runtimePolicyFile(req.Software.ID), &policy)
	if os.IsNotExist(err) {
		return result
	}
	if err != nil || policy.Configured == nil || policy.Configured.Validate("SYSTEMD") != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_RECORD_DAMAGED")
	}
	intent := runtimeCleanupIntent{InputHash: req.InputHash, Software: *req.Software, Result: result}
	if err = atomicJSON(filepath.Join(dir, "runtime-policy-cleanup-intent.json"), intent); err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_CLEANUP_UNCONFIRMED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	logged, closeLog := x.installationLogContext(ctx, req, "RUNTIME_POLICY_CLEANUP")
	_, err = x.softwareRunner(logged, "/usr/bin/bash", "-c", deployScript("runtime-policy/systemd-remove.sh"), "ops-runtime-cleanup", req.Software.Locator, req.Software.Locator)
	closeLog()
	if err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_CLEANUP_UNCONFIRMED")
	}
	verified, err := x.completeRuntimePolicyCleanup(req, dir, intent)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	return verified
}

func (x *executor) completeRuntimePolicyCleanup(req Request, dir string, intent runtimeCleanupIntent) (Result, error) {
	if req.Uninstallation == nil || req.Software == nil || intent.InputHash != req.InputHash || !reflect.DeepEqual(intent.Software, *req.Software) || intent.Result.Status != "SUCCEEDED" || intent.Result.Uninstallation == nil || !intent.Result.Uninstallation.ProgramRemoved || intent.Result.Uninstallation.SoftwareID != req.Software.ID {
		return softwareResult(req), errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	file := "/etc/systemd/system/" + req.Software.Locator + ".d/90-streamfusion-ops-runtime.conf"
	if _, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/bash", "-c", `[[ ! -e "$1" && ! -L "$1" ]]`, "ops-runtime-cleanup-check", file); err != nil {
		return softwareResult(req), errors.New("RUNTIME_POLICY_CLEANUP_UNCONFIRMED")
	}
	out, _ := softwareCommand(ctx, x.softwareRunner, "/usr/bin/systemctl", "show", "--no-pager", "--property=NeedDaemonReload", "--", req.Software.Locator)
	if strings.TrimSpace(string(out)) != "NeedDaemonReload=no" {
		return softwareResult(req), errors.New("RUNTIME_POLICY_CLEANUP_UNCONFIRMED")
	}
	if err := atomicJSON(filepath.Join(dir, "runtime-policy-cleanup-verified.json"), intent); err != nil {
		return softwareResult(req), err
	}
	return intent.Result, nil
}

func (x *executor) reconcileRuntimePolicyCleanup(req Request, dir string) (Result, bool) {
	var intent runtimeCleanupIntent
	err := readJSON(filepath.Join(dir, "runtime-policy-cleanup-intent.json"), &intent)
	if os.IsNotExist(err) {
		return Result{}, false
	}
	if err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_RECEIPT_INVALID"), true
	}
	result, err := x.completeRuntimePolicyCleanup(req, dir, intent)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error()), true
	}
	return result, true
}

func (x *executor) verifyRuntimePolicyCleanup(req Request, result Result) error {
	if req.Uninstallation == nil || req.Software == nil || req.Software.Kind != "SYSTEMD" {
		return nil
	}
	if _, err := os.Stat(x.runtimePolicyFile(req.Software.ID)); os.IsNotExist(err) {
		return nil
	}
	var receipt runtimeCleanupIntent
	if readJSON(filepath.Join(x.root, "jobs", req.JobID, "runtime-policy-cleanup-verified.json"), &receipt) != nil || receipt.InputHash != req.InputHash || !reflect.DeepEqual(receipt.Software, *req.Software) || !reflect.DeepEqual(receipt.Result, result) {
		return errors.New("RUNTIME_POLICY_RECEIPT_INVALID")
	}
	return nil
}

func (x *executor) verifyRecoveredRuntimeCleanup(req Request, dir string, result Result) Result {
	if req.Uninstallation == nil || req.Software == nil || req.Software.Kind != "SYSTEMD" || result.Status != "SUCCEEDED" {
		return result
	}
	if _, err := os.Stat(x.runtimePolicyFile(req.Software.ID)); os.IsNotExist(err) {
		return result
	}
	intent := runtimeCleanupIntent{InputHash: req.InputHash, Software: *req.Software, Result: result}
	if err := atomicJSON(filepath.Join(dir, "runtime-policy-cleanup-intent.json"), intent); err != nil {
		return unknownRuntimeInstallation(req, "RUNTIME_POLICY_CLEANUP_UNCONFIRMED")
	}
	verified, err := x.completeRuntimePolicyCleanup(req, dir, intent)
	if err != nil {
		return unknownRuntimeInstallation(req, err.Error())
	}
	return verified
}
