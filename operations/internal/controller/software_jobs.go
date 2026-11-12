package controller

import (
	"context"
	"errors"
	"slices"
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
	if (req.Action == "SOFTWARE_DOCKER_ACCESS" || req.SoftwareOperation == "SOFTWARE_DOCKER_ACCESS" || req.DockerAccess != nil) && (req.Action != "SOFTWARE_DOCKER_ACCESS" || req.SoftwareOperation != "SOFTWARE_DOCKER_ACCESS" || req.DockerAccess == nil || req.DockerAccess.Validate() != nil || req.Reconfiguration != nil || req.Installation != nil || req.Uninstallation != nil || req.NodeEnvironment != nil) {
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
	if req.DockerAccess != nil {
		if !h.startDockerAccessJob(id, req) {
			return
		}
	} else {
		h.store.mu.Lock()
		res, e := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, req.TargetID)
		h.store.mu.Unlock()
		if e != nil {
			return
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return
		}
	}
	limit := 3 * time.Minute
	if req.RuntimePolicy != nil {
		limit = 12 * time.Minute
	}
	if commandRequest(req) {
		limit = 30 * time.Minute
	}
	execCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if commandRequest(req) {
		progressCtx, stop := context.WithCancel(execCtx)
		done := make(chan struct{})
		go func() { defer close(done); h.pollProgress(progressCtx, t, req) }()
		defer func() { stop(); <-done }()
	}
	var result deploy.Result
	if e = h.store.callTarget(execCtx, t, req, "", &result); e != nil {
		h.jobUnknown(id, "TARGET_RESULT_UNKNOWN")
		return
	}
	if result.Status == "UNKNOWN" && result.ControllerID == req.ControllerID && result.TargetID == req.TargetID && result.JobID == id && result.InputHash == req.InputHash {
		code := "SOFTWARE_COMMAND_UNCONFIRMED"
		if req.DockerAccess != nil {
			code = "DOCKER_ACCESS_UNCONFIRMED"
		}
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
	if result.Status == "SUCCEEDED" && req.DockerAccess != nil {
		if err := verifyDockerAccessResult(req, result); err != nil {
			return err
		}
	}
	if result.Status == "SUCCEEDED" && req.Action == "SOFTWARE_UNINSTALL" {
		e := result.Uninstallation
		t, err := h.store.readTarget(req.TargetID)
		if err != nil {
			return err
		}
		if req.Uninstallation == nil || e == nil {
			return errors.New("SOFTWARE_UNINSTALL_EVIDENCE_MISMATCH")
		}
		dataRoot := serviceinstall.InstallRoot(t.InstallRoot, req.Software.ID, req.Uninstallation.Installation.Config)
		if req.Uninstallation.Installation.Definition.Commands != nil && !serviceinstall.IsBuiltinCommand(req.Uninstallation.Installation.Definition) {
			dataRoot += "/data"
		}
		if req.Uninstallation.DataDirectory != "" {
			dataRoot = req.Uninstallation.DataDirectory
		}
		var removedPaths []string
		if official := req.Uninstallation.Installation.Official; official != nil {
			dataRoot = official.Paths.Data
			if req.Uninstallation.DeleteData {
				removedPaths = official.Paths.OwnedPaths
			}
		}
		if n := req.Uninstallation.Installation.Node; n != nil {
			v := req.Uninstallation.Installation.Definition.Version
			dataRoot = n.RootPath + "/cache/" + v
			if req.Uninstallation.DeleteData {
				removedPaths = nodeVersionDataPaths(*n, v)
			}
		}
		if e.SoftwareID != req.Software.ID || e.Kind != req.Software.Kind || e.Locator != req.Software.Locator || e.IdentityHash != req.Software.IdentityHash || !e.ProgramRemoved && !(req.Software.Kind == "TOOL" && req.Software.Ownership != nil && req.Software.Ownership.RuntimeMode != "NATIVE" && e.SharedProgramRetained) || e.ProgramRemoved && e.SharedProgramRetained || e.DeleteData != req.Uninstallation.DeleteData || e.DataRemoved != e.DeleteData || e.DataRoot != dataRoot || !slices.Equal(e.RemovedPaths, removedPaths) {
			return errors.New("SOFTWARE_UNINSTALL_EVIDENCE_MISMATCH")
		}
		if _, err := time.Parse(time.RFC3339Nano, e.ObservedAt); err != nil {
			return errors.New("SOFTWARE_UNINSTALL_EVIDENCE_MISMATCH")
		}
	}
	if result.Status == "SUCCEEDED" && req.Action != "SOFTWARE_UNMANAGE" && req.Action != "SOFTWARE_UNINSTALL" {
		expectedIdentity := req.Software.IdentityHash
		if err := validateRuntimePolicyBinding(req, result); err != nil {
			return err
		}
		if result.RuntimePolicyBinding != nil {
			expectedIdentity = result.RuntimePolicyBinding.IdentityHash
		}
		if result.Software == nil || result.Software.Kind != req.Software.Kind || result.Software.Locator != req.Software.Locator || result.Software.IdentityHash != expectedIdentity {
			return errors.New("SOFTWARE_EVIDENCE_MISMATCH")
		}
		if req.RuntimePolicy != nil {
			if err := validateRuntimePolicyResult(req, result, req.Software.Kind); err != nil {
				return err
			}
		}
		if req.Action == "SOFTWARE_START" && result.Software.State != "RUNNING" {
			return errors.New("SOFTWARE_START_UNCONFIRMED")
		}
		if req.Action == "SOFTWARE_STOP" && result.Software.State != "STOPPED" {
			return errors.New("SOFTWARE_STOP_UNCONFIRMED")
		}
		if newSoftwareLifecycleAction(req.Action) {
			e := result.Lifecycle
			if e == nil || e.Action != req.Action {
				return errors.New("SOFTWARE_LIFECYCLE_UNCONFIRMED")
			}
			if _, err := time.Parse(time.RFC3339Nano, e.ObservedAt); err != nil {
				return errors.New("SOFTWARE_LIFECYCLE_UNCONFIRMED")
			}
			if req.Action == "SOFTWARE_RESTART" && (e.Before == "" || e.After == "" || e.Before == e.After) {
				return errors.New("SOFTWARE_RESTART_UNCONFIRMED")
			}
			if (req.Action == "SOFTWARE_RESTART" || req.Action == "SOFTWARE_UNPAUSE") && result.Software.State != "RUNNING" || req.Action == "SOFTWARE_PAUSE" && result.Software.State != "PAUSED" {
				return errors.New("SOFTWARE_LIFECYCLE_STATE_MISMATCH")
			}
		}
		if req.Action == "SOFTWARE_STOP" && req.Software.SoftwareKey == "DOCKER" && req.Software.Ownership != nil && req.Software.Ownership.RuntimeMode == "NATIVE" && !result.DockerSocketInactive {
			return errors.New("DOCKER_SOCKET_STOP_UNCONFIRMED")
		}
		if result.Software.State != "RUNNING" && result.Software.State != "STOPPED" && result.Software.State != "FAILED" && result.Software.State != "PAUSED" {
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
	if e = commitRuntimePolicyBinding(tx, req, result); e != nil {
		return e
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
	if e == nil && req.Uninstallation != nil && req.Uninstallation.Installation.Node != nil {
		e = commitNodeSoftwareObservation(tx, id, req.Uninstallation.Installation.Node, result, req.Software.ID, req.Uninstallation.Installation.Definition.Version, true)
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
