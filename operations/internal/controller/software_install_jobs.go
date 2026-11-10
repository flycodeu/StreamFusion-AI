package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) processSoftwareInstall(ctx context.Context, id string, req deploy.Request) {
	if req.Installation == nil || req.Installation.Validate() != nil {
		h.jobUnknown(id, "INSTALL_INPUT_DAMAGED")
		return
	}
	t, err := h.store.readTarget(req.TargetID)
	if err != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	claimed, resume := h.claimSoftwareInstall(id, t.ID, req.DeadlineAt)
	if !claimed {
		return
	}
	archive := ""
	if req.Installation.Source.Type == "ARCHIVE" || (req.Installation.Source.Type == "ONLINE" && req.Installation.Source.ByteSize > 0) {
		var materialID string
		err = h.store.DB.QueryRow("SELECT p.material_id FROM software_install_plans p JOIN jobs j ON j.software_plan_id=p.id WHERE j.id=?", id).Scan(&materialID)
		if err != nil {
			h.jobUnknown(id, "INSTALL_MATERIAL_REFERENCE_DAMAGED")
			return
		}
		archive = h.store.installationMaterialPath(materialID, false)
	}
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	progressCtx, stop := context.WithCancel(execCtx)
	done := make(chan struct{})
	go func() { defer close(done); h.pollProgress(progressCtx, t, req) }()
	var result deploy.Result
	targetReq := req
	if resume {
		targetReq.Action = "RECOVER_CHECK"
		var point struct{ Offset int64 }
		checkCtx, stopCheck := context.WithTimeout(execCtx, 90*time.Second)
		err = h.store.callTarget(checkCtx, t, targetReq, "", &point)
		stopCheck()
		if err == nil {
			targetReq.Action, targetReq.ResumeOffset = "RECOVER", point.Offset
		} else {
			targetReq.Action, archive = "STATUS", ""
		}
	}
	err = h.store.callTarget(execCtx, t, targetReq, archive, &result)
	stop()
	<-done
	if err != nil || result.Status == "UNKNOWN" {
		h.jobUnknown(id, "INSTALL_RESULT_UNCONFIRMED")
		return
	}
	if err = h.commitSoftwareInstallResult(id, req, result); err != nil {
		h.jobUnknown(id, "INSTALL_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

func (h *HTTP) claimSoftwareInstall(id, hostID, deadline string) (claimed, resume bool) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return false, false
	}
	defer tx.Rollback()
	locked, err := hostOperationLock(tx, hostID)
	if err != nil || locked.ID != "" && locked.ID != id {
		return false, false
	}
	var queuedPhase string
	if err = tx.QueryRow("SELECT phase FROM jobs WHERE id=? AND status='QUEUED'", id).Scan(&queuedPhase); err != nil {
		return false, false
	}
	resume = queuedPhase == "INSTALL_RESUMING"
	expires, err := time.Parse(time.RFC3339Nano, deadline)
	expired := !resume && (err != nil || !time.Now().Before(expires))
	status, phase, code := "RUNNING", "TARGET_EXECUTION", ""
	if expired {
		status, phase, code = "FAILED", "PREFLIGHT", "INSTALL_QUEUE_EXPIRED"
	}
	claim, err := tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", status, phase, code, Now(), id, hostID)
	if err != nil {
		return false, false
	}
	if n, e := claim.RowsAffected(); e != nil || n != 1 {
		return false, false
	}
	if expired {
		_, err = tx.Exec("UPDATE software_instances SET desired_state='UNCHANGED',installation_status='FAILED',error_code=?,version=version+1,updated_at=? WHERE last_job_id=?", code, Now(), id)
		if err == nil {
			_, err = tx.Exec("DELETE FROM operation_locks WHERE job_id=?", id)
		}
	} else if locked.ID == "" {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+hostID, id)
	}
	return err == nil && tx.Commit() == nil && !expired, resume
}

func (h *HTTP) resumeSoftwareInstall(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	updated, err := tx.Exec(`UPDATE jobs SET status='QUEUED',phase='INSTALL_RESUMING',error_code='',updated_at=?
		WHERE id=? AND kind='SOFTWARE_INSTALL' AND status='UNKNOWN' AND target_ack_pending=0
		  AND EXISTS(SELECT 1 FROM targets WHERE id=jobs.target_id AND maintenance=0)
		  AND EXISTS(SELECT 1 FROM operation_locks WHERE resource_key='target:'||jobs.target_id AND job_id=jobs.id)`, Now(), id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		h.fail(w, r, http.StatusConflict, "INSTALL_RESUME_UNAVAILABLE", "当前作业不能继续安装，请先核对作业或主机状态")
		return
	}
	err = auditTx(tx, actor.Username, "SOFTWARE_INSTALL_RESUMED", id, "QUEUED", r.Context().Value(traceKey{}).(string))
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, map[string]string{"jobId": id})
}

func installationEvidenceValid(req deploy.Request, result deploy.Result) error {
	i, evidence, actual := req.Installation, result.Installation, result.Software
	if i == nil || evidence == nil || actual == nil {
		return errors.New("INSTALL_EVIDENCE_REQUIRED")
	}
	if err := evidence.ValidateFor(*i); err != nil {
		return err
	}
	if actual.Kind != installationKind(*evidence) || actual.Locator != installationLocator(*evidence) || actual.IdentityHash != evidence.IdentityHash {
		return errors.New("INSTALL_EVIDENCE_MISMATCH")
	}
	if actual.Kind == "DOCKER" && evidence.IdentityHash != deploy.Digest([]string{"DOCKER", evidence.ContainerID, evidence.ImageID}) {
		return errors.New("INSTALL_EVIDENCE_MISMATCH")
	}
	if actual.State != "RUNNING" && actual.State != "STOPPED" && actual.State != "FAILED" && !(i.Definition.Kind == "TOOL" && actual.State == "INSTALLED") {
		return errors.New("INSTALL_STATE_UNCONFIRMED")
	}
	if _, err := time.Parse(time.RFC3339Nano, actual.ObservedAt); err != nil {
		return errors.New("INSTALL_OBSERVATION_TIME_INVALID")
	}
	if result.Status == "SUCCEEDED" && (evidence.Health != "READY" || (actual.State != "RUNNING" && !(i.Definition.Kind == "TOOL" && actual.State == "INSTALLED")) || strings.TrimSpace(evidence.ObservedVersion) == "") {
		return errors.New("INSTALL_HEALTH_UNCONFIRMED")
	}
	if result.Status == "FAILED" && evidence.Health != "FAILED" {
		return errors.New("INSTALL_FAILURE_UNCONFIRMED")
	}
	return nil
}

func (h *HTTP) commitSoftwareInstallResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Installation == nil || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("INSTALL_RESULT_IDENTITY_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("INSTALL_RESULT_NOT_FINAL")
	}
	bound := result.Installation != nil || result.Software != nil
	if bound {
		if err := installationEvidenceValid(req, result); err != nil {
			return err
		}
	} else if result.Status == "SUCCEEDED" {
		return errors.New("INSTALL_EVIDENCE_REQUIRED")
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, receipt string
	if err = tx.QueryRow("SELECT status,result_json FROM jobs WHERE id=? AND input_json=?", id, jsonText(req)).Scan(&status, &receipt); err != nil {
		return err
	}
	if status == "SUCCEEDED" || status == "FAILED" {
		if receipt != jsonText(result) {
			return errors.New("INSTALL_RESULT_ALREADY_COMMITTED")
		}
		return nil
	}
	host, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", req.TargetID))
	if err != nil {
		return err
	}
	installStatus := "FAILED"
	if result.Status == "SUCCEEDED" {
		installStatus = "READY"
	}
	if bound {
		obs := softwareObservation{SoftwareObservation: result.Software, ErrorCode: result.ErrorCode, Source: "EXECUTOR"}
		code := result.ErrorCode
		if code == "" {
			code = "READY"
		}
		var changed sql.Result
		changed, err = tx.Exec("UPDATE software_instances SET management_mode='MANAGED',runtime_kind=?,locator=?,identity_hash=?,actual_state=?,observation_json=?,observed_at=?,attempt_json=?,observation_identity=?,observation_revision=observation_revision+1,error_code=?,installation_status=?,installation_evidence_json=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=?", result.Software.Kind, result.Software.Locator, result.Software.IdentityHash, result.Software.State, jsonText(obs), result.Software.ObservedAt, jsonText(observationAttempt{code, Now(), req.TraceID}), observationIdentity(host), result.ErrorCode, installStatus, jsonText(result.Installation), Now(), req.Installation.SoftwareID, id)
		if err == nil {
			if count, e := changed.RowsAffected(); e != nil {
				err = e
			} else if count != 1 {
				err = errors.New("INSTALL_INSTANCE_REVISION_CONFLICT")
			}
		}
		if err == nil && req.Installation.Official != nil && result.Status == "SUCCEEDED" {
			_, err = tx.Exec("UPDATE software_instances SET software_version=? WHERE id=? AND last_job_id=?", result.Installation.ObservedVersion, req.Installation.SoftwareID, id)
		}
	} else {
		var changed sql.Result
		changed, err = tx.Exec("UPDATE software_instances SET actual_state='UNKNOWN',installation_status='FAILED',error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=?", result.ErrorCode, Now(), req.Installation.SoftwareID, id)
		if err == nil {
			if count, e := changed.RowsAffected(); e != nil {
				err = e
			} else if count != 1 {
				err = errors.New("INSTALL_INSTANCE_REVISION_CONFLICT")
			}
		}
	}
	if err == nil && req.Installation.Node != nil {
		err = commitNodeSoftwareObservation(tx, id, req.Installation.Node, result, req.Installation.SoftwareID, req.Installation.Definition.Version, false)
	}
	if err == nil && result.Status == "SUCCEEDED" {
		err = bridgeInstalledTool(tx, req, *result.Installation)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if err == nil {
		err = auditTx(tx, "worker", "SOFTWARE_INSTALL_RESULT", req.Installation.SoftwareID, result.Status, req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}
