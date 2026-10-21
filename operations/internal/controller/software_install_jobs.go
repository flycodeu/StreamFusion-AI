package controller

import (
	"context"
	"database/sql"
	"errors"
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
	h.store.mu.Lock()
	claim, err := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, t.ID)
	h.store.mu.Unlock()
	if err != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n != 1 {
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
	err = h.store.callTarget(execCtx, t, req, archive, &result)
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
