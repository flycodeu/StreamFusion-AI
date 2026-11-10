package controller

import (
	"context"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) pollProgress(ctx context.Context, t target, req deploy.Request) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	req.Action = "PROGRESS"
	req.Candidate, req.Baseline = nil, nil
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			var result deploy.Result
			err := h.store.callTarget(callCtx, t, req, "", &result)
			cancel()
			if err != nil || !validJobProgress(req, result) {
				continue
			}
			h.store.mu.Lock()
			_, err = h.store.DB.Exec("UPDATE jobs SET phase=?,updated_at=? WHERE id=? AND status='RUNNING'", result.Phase, Now(), req.JobID)
			h.store.mu.Unlock()
		}
	}
}

func validJobProgress(req deploy.Request, result deploy.Result) bool {
	if result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != req.JobID || result.InputHash != req.InputHash {
		return false
	}
	// Checkpoints are observations, never terminal receipts. Keep final state
	// changes in the existing commit/reconcile path.
	switch result.Phase {
	case "TRANSFER", "IMPORT", "ACTIVATE", "HEALTH", "RECOVERY", "PREFLIGHT", "PULLING", "VERIFYING", "VERSION_CHECK", "RECORDED", "REMOVING",
		"SOFTWARE_PREFLIGHT", "SERVICE_IMAGE_PULL", "SERVICE_IMAGE_LOAD", "SERVICE_IMAGE_VERIFY", "SERVICE_CONFIGURE", "SERVICE_CREATE", "SERVICE_INITIALIZING", "SERVICE_HEALTH",
		"DOCKER_MATERIAL", "DOCKER_PACKAGES_APPLY", "OFFICIAL_REFRESH", "OFFICIAL_PLAN_VERIFY", "OFFICIAL_PACKAGES_APPLY",
		"NATIVE_MATERIAL", "NATIVE_DOWNLOAD", "NATIVE_VERIFY", "NATIVE_START", "NATIVE_FFMPEG_CONFIGURE", "NATIVE_FFMPEG_BUILD", "NATIVE_PYTHON_CONFIGURE", "NATIVE_PYTHON_BUILD", "NATIVE_PYTHON_INSTALL", "NATIVE_REDIS_BUILD", "NATIVE_NGINX_CONFIGURE", "NATIVE_NGINX_BUILD", "NATIVE_ZLM_CONFIGURE", "NATIVE_ZLM_BUILD",
		"CONFIGURATION_STOP", "CONFIGURATION_CREATE", "COMMAND_INSTALL", "COMMAND_VERIFY", "COMMAND_UNINSTALL", "COMMAND_DELETE_DATA",
		"MANAGEMENT_EXECUTE", "MANAGEMENT_VERIFY", "MANAGEMENT_ROLLBACK":
		return true
	}
	return false
}
