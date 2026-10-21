package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) submitSoftwareConfigurationPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Key string `json:"idempotencyKey"`
	}
	if err := decode(w, r, &in); err != nil || len(strings.TrimSpace(in.Key)) < 8 || len(in.Key) > 80 {
		h.fail(w, r, 400, "IDEMPOTENCY_REQUIRED", "请携带8–80字节提交标识")
		return
	}
	hash := deploy.Digest(struct{ Plan string }{id})
	var existing, oldHash, priorSoftwareID string
	err := h.store.DB.QueryRow("SELECT j.id,j.request_hash,p.software_id FROM jobs j JOIN software_configuration_plans p ON p.id=j.configuration_plan_id WHERE j.actor_id=? AND j.kind='SOFTWARE_CONFIGURE' AND j.idempotency_key=?", s.AdminID, in.Key).Scan(&existing, &oldHash, &priorSoftwareID)
	if err == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他配置计划")
		} else {
			h.success(w, r, map[string]string{"jobId": existing, "softwareInstanceId": priorSoftwareID})
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	var raw, status, expiry string
	if err = h.store.DB.QueryRow("SELECT snapshot_json,status,expires_at FROM software_configuration_plans WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&raw, &status, &expiry); err != nil {
		h.opsError(w, r, err)
		return
	}
	p, err := parseConfigurationPlan(raw)
	expires, timeErr := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil || timeErr != nil || status != "DRAFT" || expiry != p.ExpiresAt || time.Now().After(expires) || p.change().Validate() != nil {
		h.fail(w, r, 409, "CONFIGURATION_PLAN_STALE", "配置计划已变化、提交或过期，请重新预检")
		return
	}
	i, err := h.store.readSoftware(p.SoftwareID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	t, err := h.store.readTarget(p.TargetID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if i.Version != p.SoftwareVersion || deploy.Digest(softwareRequest(i)) != deploy.Digest(p.Binding) || t.Maintenance || pluginTargetSnapshot(t) != p.TargetSnapshot {
		h.fail(w, r, 409, "CONFIGURATION_PLAN_STALE", "主机或软件身份已变化，请重新预检")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	checked, err := h.checkSoftwareConfiguration(ctx, t, i, p.Config)
	if err != nil {
		h.fail(w, r, 409, configurationErrorCode(err), "配置提交前检查失败，请打开主机安装准备或重新预检")
		return
	}
	if !checked.Ready {
		code, message := h.installPreflightRejection(checked, p.Config)
		h.fail(w, r, 409, code, message)
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	err = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind='SOFTWARE_CONFIGURE' AND idempotency_key=?", s.AdminID, in.Key).Scan(&existing, &oldHash)
	if err == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他配置计划")
		} else {
			h.success(w, r, map[string]string{"jobId": existing, "softwareInstanceId": p.SoftwareID})
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	var currentRaw string
	if err = tx.QueryRow("SELECT snapshot_json,status,expires_at FROM software_configuration_plans WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&currentRaw, &status, &expiry); err != nil {
		h.opsError(w, r, err)
		return
	}
	if currentRaw != raw || status != "DRAFT" || time.Now().After(expires) {
		h.fail(w, r, 409, "CONFIGURATION_PLAN_STALE", "配置计划已变化、提交或过期")
		return
	}
	current, err := scanSoftware(tx.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", p.SoftwareID))
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	host, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", p.TargetID))
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if current.Version != p.SoftwareVersion || !installationBound(current) || deploy.Digest(softwareRequest(current)) != deploy.Digest(p.Binding) || host.Maintenance || pluginTargetSnapshot(host) != p.TargetSnapshot {
		h.fail(w, r, 409, "CONFIGURATION_PLAN_STALE", "提交期间主机或软件发生变化")
		return
	}
	if err = checkInstallCredentialTx(tx, p.Previous.Config); err != nil {
		h.fail(w, r, 409, "INSTALL_CREDENTIAL_UNAVAILABLE", "原配置凭据发生变化，无法安全恢复")
		return
	}
	if err = checkInstallCredentialTx(tx, p.Config); err != nil {
		h.fail(w, r, 409, "INSTALL_CREDENTIAL_UNAVAILABLE", "配置凭据发生变化")
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key IN (?,?)", "target:"+p.TargetID, "software:"+p.SoftwareID).Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "主机或软件存在未决作业，请先核对")
		return
	}
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "未结束作业达到20个")
		return
	}
	if err = reserveInstallPorts(tx, p.TargetID, s.AdminID, p.ID, p.Definition, p.Config); err != nil {
		h.fail(w, r, 409, "INSTALL_PORT_RESERVED", "有效计划已预留该协议端口")
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.TargetID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: "SOFTWARE_CONFIGURE", SoftwareOperation: "SOFTWARE_CONFIGURE", Software: &p.Binding, Reconfiguration: p.change(), DeadlineAt: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,configuration_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_CONFIGURE',?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", job, s.AdminID, p.TargetID, p.ID, in.Key, hash, jsonText(req), req.TraceID, now, now)
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?),(?,?)", "target:"+p.TargetID, job, "software:"+p.SoftwareID, job)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE software_instances SET last_job_id=?,version=version+1,error_code='',updated_at=? WHERE id=?", job, now, p.SoftwareID)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE software_configuration_plans SET status='SUBMITTED' WHERE id=?", p.ID)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "SOFTWARE_CONFIGURATION_ACCEPTED", p.SoftwareID, "SUCCESS", req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job, "softwareInstanceId": p.SoftwareID})
}

func (h *HTTP) processSoftwareConfiguration(ctx context.Context, id string, req deploy.Request) {
	if req.Reconfiguration == nil || req.Reconfiguration.Validate() != nil || req.Software == nil {
		h.jobUnknown(id, "CONFIGURATION_INPUT_DAMAGED")
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
	execCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	progressCtx, stop := context.WithCancel(execCtx)
	done := make(chan struct{})
	go func() { defer close(done); h.pollProgress(progressCtx, t, req) }()
	var result deploy.Result
	err = h.store.callTarget(execCtx, t, req, "", &result)
	stop()
	<-done
	if err != nil || result.Status == "UNKNOWN" {
		h.jobUnknown(id, "CONFIGURATION_RESULT_UNCONFIRMED")
		return
	}
	if err = h.commitSoftwareConfigurationResult(id, req, result); err != nil {
		h.jobUnknown(id, "CONFIGURATION_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

func (h *HTTP) commitSoftwareConfigurationResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Reconfiguration == nil || req.Software == nil || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("CONFIGURATION_RESULT_IDENTITY_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("RESULT_NOT_FINAL")
	}
	expected := req.Reconfiguration.Previous
	if result.Status == "SUCCEEDED" {
		expected.Config = req.Reconfiguration.Config
		expected.BindingRevision++
	}
	unapplied := result.Status == "FAILED" && result.Installation == nil && result.Software == nil &&
		(result.Phase == "CONFIGURATION_NOT_APPLIED" && result.ErrorCode != "" || result.Phase == "NOT_STARTED" && result.ErrorCode == "TARGET_NOT_STARTED")
	fact, evidence := result.Software, result.Installation
	if !unapplied {
		if result.Installation == nil || result.Software == nil {
			return errors.New("CONFIGURATION_EVIDENCE_REQUIRED")
		}
		if err := result.Installation.ValidateFor(expected); err != nil {
			return err
		}
		if fact.Kind != installationKind(*evidence) || fact.Locator != installationLocator(*evidence) || fact.IdentityHash != evidence.IdentityHash || (fact.State != "RUNNING" && fact.State != "STOPPED" && fact.State != "FAILED") {
			return errors.New("CONFIGURATION_EVIDENCE_MISMATCH")
		}
		if _, err := time.Parse(time.RFC3339Nano, fact.ObservedAt); err != nil {
			return errors.New("CONFIGURATION_TIME_INVALID")
		}
		if result.Status == "FAILED" && (fact.Kind != req.Software.Kind || fact.Locator != req.Software.Locator || fact.IdentityHash != req.Software.IdentityHash || evidence.Health != "READY" || (fact.State != "RUNNING" && fact.State != "STOPPED")) {
			return errors.New("CONFIGURATION_ROLLBACK_UNCONFIRMED")
		}
		if result.Status == "SUCCEEDED" && (evidence.Health != "READY" || (fact.State != "RUNNING" && fact.State != "STOPPED") || strings.TrimSpace(evidence.ObservedVersion) == "") {
			return errors.New("CONFIGURATION_HEALTH_UNCONFIRMED")
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, receipt string
	if err = tx.QueryRow("SELECT status,result_json FROM jobs WHERE id=? AND input_json=?", id, jsonText(req)).Scan(&state, &receipt); err != nil {
		return err
	}
	if state == "SUCCEEDED" || state == "FAILED" {
		if receipt != jsonText(result) {
			return errors.New("CONFIGURATION_RESULT_ALREADY_COMMITTED")
		}
		return nil
	}
	host, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", req.TargetID))
	if err != nil {
		return err
	}
	obs := softwareObservation{SoftwareObservation: fact, ErrorCode: result.ErrorCode, Source: "EXECUTOR"}
	code := result.ErrorCode
	if code == "" {
		code = "READY"
	}
	var changed sql.Result
	if unapplied {
		// A trusted executor rejected the frozen change before any stop/config
		// boundary. Keep the previous binding and last confirmed observation.
		changed, err = tx.Exec(`UPDATE software_instances SET error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=? AND binding_revision=? AND identity_hash=?`, result.ErrorCode, Now(), expected.SoftwareID, id, expected.BindingRevision, req.Software.IdentityHash)
	} else if result.Status == "SUCCEEDED" {
		changed, err = tx.Exec(`UPDATE software_instances SET name=?,runtime_kind=?,locator=?,identity_hash=?,binding_revision=?,actual_state=?,installation_json=?,installation_evidence_json=?,configuration_job_id=?,observation_json=?,observed_at=?,observation_identity=?,attempt_json=?,observation_revision=observation_revision+1,error_code='',version=version+1,updated_at=? WHERE id=? AND last_job_id=? AND binding_revision=?`, expected.Config.Name, fact.Kind, fact.Locator, fact.IdentityHash, expected.BindingRevision, fact.State, jsonText(expected), jsonText(evidence), id, jsonText(obs), fact.ObservedAt, observationIdentity(host), jsonText(observationAttempt{code, Now(), req.TraceID}), Now(), expected.SoftwareID, id, req.Reconfiguration.ExpectedBindingRevision)
	} else {
		changed, err = tx.Exec(`UPDATE software_instances SET actual_state=?,observation_json=?,observed_at=?,observation_identity=?,attempt_json=?,observation_revision=observation_revision+1,error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=? AND binding_revision=?`, fact.State, jsonText(obs), fact.ObservedAt, observationIdentity(host), jsonText(observationAttempt{code, Now(), req.TraceID}), result.ErrorCode, Now(), expected.SoftwareID, id, expected.BindingRevision)
	}
	if err == nil {
		n, e := changed.RowsAffected()
		if e != nil {
			err = e
		} else if n != 1 {
			err = errors.New("CONFIGURATION_BINDING_REVISION_CONFLICT")
		}
	}
	if err == nil {
		_, err = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if err == nil {
		err = auditTx(tx, "worker", "SOFTWARE_CONFIGURATION_RESULT", expected.SoftwareID, result.Status, req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}
