package controller

import (
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestInstallationProgressAcceptsActualCheckpointsButNotForeignOrFinalResults(t *testing.T) {
	req := deploy.Request{ControllerID: "controller", TargetID: "host", JobID: "job", InputHash: "hash"}
	base := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Phase: "OFFICIAL_PACKAGES_APPLY", Status: "UNKNOWN"}
	for _, phase := range []string{"TRANSFER", "SERVICE_IMAGE_PULL", "OFFICIAL_REFRESH", "OFFICIAL_PACKAGES_APPLY", "DOCKER_PACKAGES_APPLY", "NATIVE_REDIS_BUILD", "NATIVE_START", "SERVICE_HEALTH"} {
		got := base
		got.Phase = phase
		if !validJobProgress(req, got) {
			t.Errorf("lost actual checkpoint %s", phase)
		}
	}
	for _, mutate := range []func(*deploy.Result){
		func(r *deploy.Result) { r.ControllerID = "other" },
		func(r *deploy.Result) { r.TargetID = "other" },
		func(r *deploy.Result) { r.JobID = "other" },
		func(r *deploy.Result) { r.InputHash = "other" },
		func(r *deploy.Result) { r.ProtocolVersion = 2 },
		func(r *deploy.Result) { r.ExecutorVersion = "other" },
		func(r *deploy.Result) { r.Phase = "SERVICE_INSTALLED"; r.Status = "SUCCEEDED" },
		func(r *deploy.Result) { r.Phase = "untrusted text" },
	} {
		got := base
		mutate(&got)
		if validJobProgress(req, got) {
			t.Fatal("accepted unrelated or final result", got)
		}
	}
}
