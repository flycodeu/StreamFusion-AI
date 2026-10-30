package controller

import (
	"context"
	"errors"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func (h *HTTP) processSoftware(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	if req.Reconfiguration != nil {
		h.processSoftwareConfiguration(ctx, id, req)
		return
	}
	if req.Installation != nil {
		h.processSoftwareInstall(ctx, id, req)
		return
	}
	if req.Software == nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	h.store.mu.Lock()
	res, e := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, req.TargetID)
	h.store.mu.Unlock()
	if e != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	execCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var result deploy.Result
	if e = h.store.callTarget(execCtx, t, req, "", &result); e != nil {
		h.jobUnknown(id, "TARGET_RESULT_UNKNOWN")
		return
	}
	if result.Status == "UNKNOWN" && result.ControllerID == req.ControllerID && result.TargetID == req.TargetID && result.JobID == id && result.InputHash == req.InputHash {
		code := "SOFTWARE_COMMAND_UNCONFIRMED"
		if req.Uninstallation != nil {
			code = "SOFTWARE_UNINSTALL_UNCONFIRMED"
			if result.ErrorCode != "" && len(result.ErrorCode) <= 96 {
				valid := true
				for _, r := range result.ErrorCode {
					if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
						valid = false
						break
					}
				}
				if valid {
					code = result.ErrorCode
				}
			}
		}
		h.jobUnknown(id, code)
		return
	}
	if e = h.commitSoftwareResult(id, req, result); e != nil {
		h.jobUnknown(id, "SOFTWARE_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

func (h *HTTP) commitSoftwareResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Software == nil || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("RESULT_IDENTITY_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("RESULT_NOT_FINAL")
	}
	if result.Status == "SUCCEEDED" && req.Action == "SOFTWARE_UNINSTALL" {
		e := result.Uninstallation
		t, err := h.store.readTarget(req.TargetID)
		if err != nil {
			return err
		}
		if req.Uninstallation == nil || e == nil || e.SoftwareID != req.Software.ID || e.Kind != req.Software.Kind || e.Locator != req.Software.Locator || e.IdentityHash != req.Software.IdentityHash || !e.ProgramRemoved && !(req.Software.Kind == "TOOL" && req.Software.Ownership != nil && req.Software.Ownership.RuntimeMode != "NATIVE" && e.SharedProgramRetained) || e.ProgramRemoved && e.SharedProgramRetained || e.DeleteData != req.Uninstallation.DeleteData || e.DataRemoved != e.DeleteData || e.DataRoot != serviceinstall.InstallRoot(t.InstallRoot, req.Software.ID, req.Uninstallation.Installation.Config) {
			return errors.New("SOFTWARE_UNINSTALL_EVIDENCE_MISMATCH")
		}
		if _, err := time.Parse(time.RFC3339Nano, e.ObservedAt); err != nil {
			return errors.New("SOFTWARE_UNINSTALL_EVIDENCE_MISMATCH")
		}
	}
	if result.Status == "SUCCEEDED" && req.Action != "SOFTWARE_UNMANAGE" && req.Action != "SOFTWARE_UNINSTALL" {
		if result.Software == nil || result.Software.Kind != req.Software.Kind || result.Software.Locator != req.Software.Locator || result.Software.IdentityHash != req.Software.IdentityHash {
			return errors.New("SOFTWARE_EVIDENCE_MISMATCH")
		}
		if req.Action == "SOFTWARE_START" && result.Software.State != "RUNNING" {
			return errors.New("SOFTWARE_START_UNCONFIRMED")
		}
		if req.Action == "SOFTWARE_STOP" && result.Software.State != "STOPPED" {
			return errors.New("SOFTWARE_STOP_UNCONFIRMED")
		}
		if req.Action == "SOFTWARE_STOP" && req.Software.SoftwareKey == "DOCKER" && req.Software.Ownership != nil && req.Software.Ownership.RuntimeMode == "NATIVE" && !result.DockerSocketInactive {
			return errors.New("DOCKER_SOCKET_STOP_UNCONFIRMED")
		}
		if result.Software.State != "RUNNING" && result.Software.State != "STOPPED" && result.Software.State != "FAILED" {
			return errors.New("SOFTWARE_STATE_INVALID")
		}
		if _, e := time.Parse(time.RFC3339Nano, result.Software.ObservedAt); e != nil {
			return errors.New("SOFTWARE_OBSERVATION_TIME_INVALID")
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.store.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var state string
	if e = tx.QueryRow("SELECT status FROM jobs WHERE id=? AND input_json=?", id, jsonText(req)).Scan(&state); e != nil {
		return e
	}
	if state == "SUCCEEDED" || state == "FAILED" {
		return nil
	}
	actual, observed, raw := "UNKNOWN", Now(), "{}"
	if result.Software != nil {
		actual, observed = result.Software.State, result.Software.ObservedAt
		raw = jsonText(softwareObservation{SoftwareObservation: result.Software, ErrorCode: result.ErrorCode, Source: "EXECUTOR"})
	}
	mode := ""
	if result.Status == "SUCCEEDED" {
		if req.Action == "SOFTWARE_ADOPT" {
			mode = "MANAGED"
		}
		if req.Action == "SOFTWARE_UNMANAGE" {
			mode = "RELEASED"
		}
	}
	attemptCode := result.ErrorCode
	if attemptCode == "" {
		attemptCode = "READY"
	}
	confirmed := result.Software != nil && result.Software.State != "UNKNOWN"
	if result.Status == "SUCCEEDED" && req.Action == "SOFTWARE_UNINSTALL" {
		mode, actual = "RELEASED", "REMOVED"
	}
	_, e = tx.Exec("UPDATE software_instances SET management_mode=CASE WHEN ?='' THEN management_mode ELSE ? END,actual_state=?,observation_json=CASE WHEN ? THEN ? ELSE observation_json END,observed_at=CASE WHEN ? THEN ? ELSE observed_at END,attempt_json=?,observation_revision=observation_revision+1,error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=?", mode, mode, actual, confirmed, raw, confirmed, observed, jsonText(observationAttempt{attemptCode, Now(), req.TraceID}), result.ErrorCode, Now(), req.Software.ID, id)
	if e == nil {
		if result.Status == "SUCCEEDED" && req.Action == "SOFTWARE_UNINSTALL" {
			_, e = tx.Exec("UPDATE software_instances SET installation_status='UNINSTALLED' WHERE id=? AND last_job_id=?", req.Software.ID, id)
			if e == nil && req.Software.Kind == "TOOL" {
				_, e = tx.Exec("DELETE FROM plugin_installations WHERE id=?", req.Software.ID)
			}
		}
	}
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if e == nil {
		e = auditTx(tx, "worker", req.Action+"_RESULT", req.Software.ID, result.Status, req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
