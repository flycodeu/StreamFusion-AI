package controller

import (
	"errors"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func projectJobExecution(action, phase string) (targetAction, startPhase string, reconcile bool) {
	if phase == "RECONCILING" {
		if action == "DECOMMISSION" {
			return deploy.ProjectDecommission, phase, true
		}
		return deploy.ProjectRecover, phase, true
	}
	switch action {
	case "DEPLOY":
		return deploy.ProjectApply, "ARTIFACT_DOWNLOAD", false
	case "ROLLBACK":
		return deploy.ProjectRollback, "TARGET_EXECUTION", false
	case "DECOMMISSION":
		return deploy.ProjectDecommission, "TARGET_EXECUTION", false
	default:
		return deploy.ProjectObserve, "OBSERVING", false
	}
}

type projectTargetOutcome struct {
	status, phase, code, health string
	promote, keepTargetLog      bool
}

// Decide whether the observed state may replace the running release.
func resolveProjectTargetOutcome(action string, spec deploy.ProjectDeployment, result deploy.ProjectDeployResult, reconcile bool) projectTargetOutcome {
	if action == "VERIFY" {
		if result.Status == "SUCCEEDED" && result.ReleaseID == spec.ExpectedRelease {
			return projectTargetOutcome{status: "SUCCEEDED", phase: "VERIFIED", health: result.Health}
		}
		code := projectWorkerCode(errors.New(result.ErrorCode), "PROJECT_RELEASE_CHANGED")
		return projectTargetOutcome{status: "FAILED", phase: "OBSERVING", code: code, health: result.Health}
	}
	if result.Status == "UNKNOWN" {
		return projectTargetOutcome{status: "UNKNOWN", phase: "RECONCILING", code: "PROJECT_RECOVERY_REQUIRED", keepTargetLog: true}
	}
	if result.Status != "SUCCEEDED" {
		code := projectWorkerCode(errors.New(result.ErrorCode), "PROJECT_TARGET_FAILED")
		status := "FAILED"
		if reconcile || code == "PROJECT_RELEASE_CHANGED" {
			status = "UNKNOWN"
		}
		return projectTargetOutcome{status: status, phase: "TARGET_EXECUTION", code: code, keepTargetLog: true}
	}
	switch {
	case action == "DECOMMISSION":
		if result.ReleaseID == "" && result.Health == "NOT_DEPLOYED" {
			return projectTargetOutcome{status: "SUCCEEDED", phase: "DECOMMISSIONED", health: result.Health, promote: true, keepTargetLog: true}
		}
	case result.ReleaseID == spec.ReleaseID:
		if result.Health != "READY" {
			return projectTargetOutcome{status: "UNKNOWN", phase: "RECONCILING", code: "PROJECT_HEALTH_UNCONFIRMED", health: result.Health}
		}
		return projectTargetOutcome{status: "SUCCEEDED", phase: "VERIFIED", health: result.Health, promote: true, keepTargetLog: true}
	case reconcile && result.ReleaseID == spec.ExpectedRelease:
		return projectTargetOutcome{status: "FAILED", phase: "BASELINE_RETAINED", code: "PROJECT_CHANGE_NOT_ACTIVE", health: result.Health, keepTargetLog: true}
	}
	return projectTargetOutcome{status: "UNKNOWN", phase: "RECONCILING", code: "PROJECT_RELEASE_CHANGED"}
}
