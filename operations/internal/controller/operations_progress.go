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
			if err != nil || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != req.JobID || result.InputHash != req.InputHash {
				continue
			}
			switch result.Phase {
			case "TRANSFER", "IMPORT", "ACTIVATE", "HEALTH", "RECOVERY", "PREFLIGHT", "PULLING", "VERIFYING", "VERSION_CHECK", "RECORDED", "REMOVING":
				h.store.mu.Lock()
				_, err = h.store.DB.Exec("UPDATE jobs SET phase=?,updated_at=? WHERE id=? AND status='RUNNING'", result.Phase, Now(), req.JobID)
				h.store.mu.Unlock()
			}
		}
	}
}
