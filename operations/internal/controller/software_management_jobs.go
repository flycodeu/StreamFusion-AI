package controller

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func (h *HTTP) operationReceipt(w http.ResponseWriter, r *http.Request, actor *session, key, hash string) (bool, error) {
	var job, oldHash, softwareID string
	err := h.store.DB.QueryRow("SELECT j.id,j.request_hash,p.software_id FROM jobs j JOIN software_configuration_plans p ON p.id=j.configuration_plan_id WHERE j.actor_id=? AND j.kind='SOFTWARE_OPERATION' AND j.idempotency_key=?", actor.AdminID, key).Scan(&job, &oldHash, &softwareID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if oldHash != hash {
		h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他操作计划")
	} else {
		h.success(w, r, map[string]string{"jobId": job, "softwareInstanceId": softwareID})
	}
	return true, nil
}

func (h *HTTP) submitSoftwareOperationPlan(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in struct {
		Key string `json:"idempotencyKey"`
	}
	if err := decode(w, r, &in); err != nil || len(strings.TrimSpace(in.Key)) < 8 || len(in.Key) > 80 {
		h.fail(w, r, 400, "IDEMPOTENCY_REQUIRED", "请携带有效提交标识")
		return
	}
	hash := deploy.Digest(struct{ Plan string }{id})
	if done, err := h.operationReceipt(w, r, actor, in.Key, hash); done {
		return
	} else if err != nil {
		h.internal(w, r, err)
		return
	}
	var raw, state, expiry string
	if err := h.store.DB.QueryRow("SELECT snapshot_json,status,expires_at FROM software_configuration_plans WHERE id=? AND actor_id=?", id, actor.AdminID).Scan(&raw, &state, &expiry); err != nil {
		h.opsError(w, r, err)
		return
	}
	p, err := decodeSoftwareOperationPlan(raw)
	expires, timeErr := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil || timeErr != nil || state != "DRAFT" || expiry != p.ExpiresAt || time.Now().After(expires) {
		h.fail(w, r, 409, "MANAGEMENT_PLAN_STALE", "操作计划已变化或过期，请重新检查")
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
	if !managementInstance(i) || i.Version != p.SoftwareVersion || deploy.Digest(softwareRequest(i)) != deploy.Digest(p.Binding) || t.Maintenance || pluginTargetSnapshot(t) != p.TargetSnapshot {
		h.fail(w, r, 409, "MANAGEMENT_STATE_CHANGED", "主机或软件身份已变化，请重新检查")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	checked, err := h.checkSoftwareOperation(ctx, t, i, p.Management)
	if err != nil || !checked.Ready || checked.ResourceHash != p.Management.ResourceHash {
		h.fail(w, r, 409, "MANAGEMENT_RESOURCES_CHANGED", "操作资源或权限已变化，请重新检查")
		return
	}
	failedPorts, err := h.store.confirmedFailedManagementPorts(t, checked)
	if err != nil || !maps.Equal(failedPorts, p.FailedPortClaims) {
		h.fail(w, r, 409, "MANAGEMENT_RESOURCES_CHANGED", "历史安装记录或端口归属已变化，请重新检查")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	// Serialize this second lookup with all other submissions. Reusing the
	// accepted key always returns its original receipt, even after plan expiry.
	if done, err := h.operationReceipt(w, r, actor, in.Key, hash); done {
		return
	} else if err != nil {
		h.internal(w, r, err)
		return
	}
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var currentRaw string
	if err = tx.QueryRow("SELECT snapshot_json,status,expires_at FROM software_configuration_plans WHERE id=? AND actor_id=?", id, actor.AdminID).Scan(&currentRaw, &state, &expiry); err != nil {
		h.opsError(w, r, err)
		return
	}
	if currentRaw != raw || state != "DRAFT" || expiry != p.ExpiresAt || time.Now().After(expires) {
		h.fail(w, r, 409, "MANAGEMENT_PLAN_STALE", "操作计划已变化或提交，请重新检查")
		return
	}
	if err = verifySoftwareOperationState(tx, p); err == nil {
		err = checkManagementCredentialsTx(tx, p.Management)
	}
	if err == nil {
		err = reserveManagementResources(tx, p.TargetID, p.SoftwareID, p.ID, p.Resources, resourceClaimExemptions{VerifiedPorts: p.FailedPortClaims})
	}
	if err != nil {
		h.fail(w, r, 409, configurationErrorCode(err), "操作身份、凭据或资源已变化，请重新检查")
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); err != nil || count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "待处理作业过多，请先处理现有任务")
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.TargetID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: "SOFTWARE_OPERATION", SoftwareOperation: "SOFTWARE_OPERATION", Software: &p.Binding, Management: &p.Management, DeadlineAt: time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,configuration_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_OPERATION',?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", job, actor.AdminID, p.TargetID, p.ID, in.Key, hash, jsonText(req), req.TraceID, now, now)
	if err == nil {
		for _, key := range []string{"target:" + p.TargetID, "software:" + p.SoftwareID} {
			if _, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", key, job); err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.Exec("UPDATE software_instances SET last_job_id=?,error_code='',version=version+1,updated_at=? WHERE id=?", job, now, p.SoftwareID)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE software_configuration_plans SET status='SUBMITTED' WHERE id=?", id)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, "SOFTWARE_OPERATION_ACCEPTED", p.SoftwareID, "SUCCESS", req.TraceID)
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

func (h *HTTP) processSoftwareOperation(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.Management == nil || req.Software == nil || req.Management.Validate() != nil || req.Software.ID != req.Management.SoftwareID || req.SoftwareOperation != "SOFTWARE_OPERATION" || req.Action != "SOFTWARE_OPERATION" {
		h.jobUnknown(id, "MANAGEMENT_INPUT_DAMAGED")
		return
	}
	t, err := h.store.readTarget(req.TargetID)
	if err != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	h.store.mu.Lock()
	claimed, err := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, req.TargetID)
	h.store.mu.Unlock()
	if err != nil {
		return
	}
	if count, _ := claimed.RowsAffected(); count != 1 {
		return
	}
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	progressCtx, stop := context.WithCancel(execCtx)
	done := make(chan struct{})
	go func() { defer close(done); h.pollProgress(progressCtx, t, req) }()
	var result deploy.Result
	err = h.store.callTarget(execCtx, t, req, "", &result)
	stop()
	<-done
	if err != nil || result.Status == "UNKNOWN" {
		h.jobUnknown(id, "MANAGEMENT_RESULT_UNCONFIRMED")
		return
	}
	if err = h.commitSoftwareOperationResult(id, req, result); err != nil {
		h.jobUnknown(id, "MANAGEMENT_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

func (h *HTTP) commitSoftwareOperationResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Management == nil || req.Software == nil || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("MANAGEMENT_RESULT_IDENTITY_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("RESULT_NOT_FINAL")
	}
	if result.Status == "SUCCEEDED" {
		if result.Management == nil || result.Management.ValidateFor(*req.Management) != nil || validateManagementBindingResult(req, result) != nil {
			return errors.New("MANAGEMENT_EVIDENCE_MISMATCH")
		}
		if _, err := time.Parse(time.RFC3339Nano, result.Management.ObservedAt); err != nil {
			return errors.New("MANAGEMENT_EVIDENCE_MISMATCH")
		}
		if result.Software.State != "RUNNING" && result.Software.State != "STOPPED" && result.Software.State != "INSTALLED" && result.Software.State != "PAUSED" && result.Software.State != "FAILED" {
			return errors.New("MANAGEMENT_STATE_UNCONFIRMED")
		}
		if _, err := time.Parse(time.RFC3339Nano, result.Software.ObservedAt); err != nil {
			return errors.New("MANAGEMENT_EVIDENCE_MISMATCH")
		}
		if err := h.store.managementOutputSafe(*req.Management, *result.Management); err != nil {
			return err
		}
	} else {
		op, _ := req.Management.Definition.ManagementOperation(req.Management.OperationKey)
		restored := op.Rebind && result.Phase == "OPERATION_RESTORED" && result.Installation == nil && result.Software != nil && result.Software.Kind == req.Software.Kind && result.Software.Locator == req.Software.Locator && result.Software.IdentityHash == req.Software.IdentityHash && (result.Software.State == "RUNNING" || result.Software.State == "STOPPED")
		unapplied := result.Software == nil && result.Installation == nil && (result.Phase == "OPERATION_NOT_APPLIED" || result.Phase == "NOT_STARTED" && result.ErrorCode == "TARGET_NOT_STARTED")
		if result.Management != nil || !restored && !unapplied {
			return errors.New("MANAGEMENT_FAILURE_UNCONFIRMED")
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, committed, planID string
	if err = tx.QueryRow("SELECT status,result_json,configuration_plan_id FROM jobs WHERE id=? AND input_json=?", id, jsonText(req)).Scan(&state, &committed, &planID); err != nil {
		return err
	}
	if state == "SUCCEEDED" || state == "FAILED" {
		if committed != jsonText(result) {
			return errors.New("MANAGEMENT_RESULT_ALREADY_COMMITTED")
		}
		return nil
	}
	var raw string
	if err = tx.QueryRow("SELECT snapshot_json FROM software_configuration_plans WHERE id=?", planID).Scan(&raw); err != nil {
		return err
	}
	p, err := decodeSoftwareOperationPlan(raw)
	if err != nil || deploy.Digest(p.Management) != deploy.Digest(req.Management) || deploy.Digest(p.Binding) != deploy.Digest(req.Software) {
		return errors.New("MANAGEMENT_PLAN_DAMAGED")
	}
	current, err := scanSoftwareManagement(tx, req.Software.ID)
	if err != nil || current.Version != p.StateVersion {
		return errors.New("MANAGEMENT_STATE_CHANGED")
	}
	if result.Status == "SUCCEEDED" {
		if err = verifyManagementResourceSnapshot(p, result.Management.Resources); err != nil {
			return err
		}
		p.Management.Parameters = confirmedManagementParameters(p, current, result.Management)
		if err = saveManagementState(tx, p, current, id, result.Management.Resources); err != nil {
			return err
		}
	}
	changed, err := commitManagementBinding(tx, req, result)
	if err == nil {
		if count, _ := changed.RowsAffected(); count != 1 {
			return errors.New("MANAGEMENT_BINDING_CHANGED")
		}
		_, err = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if err == nil {
		err = auditTx(tx, "worker", "SOFTWARE_OPERATION_RESULT", req.Software.ID, result.Status, req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}

func saveManagementState(tx *sql.Tx, p storedSoftwareOperationPlan, before softwareManagementState, job string, resources []serviceinstall.ResolvedResource) error {
	m := p.Management
	version, _ := strconv.ParseInt(before.Version, 10, 64)
	changed := before.TemplateID != p.TemplateID || before.TemplateRevision != p.TemplateRevision || deploy.Digest(before.Definition) != deploy.Digest(m.Definition) || !reflect.DeepEqual(nonNilParameters(before.Parameters), nonNilParameters(m.Parameters)) || !reflect.DeepEqual(nonNilCredentialRefs(before.CredentialRefs), nonNilCredentialRefs(m.ParameterCredentials))
	if changed {
		version++
	}
	_, err := tx.Exec(`INSERT INTO software_management_state(software_id,template_id,template_revision,definition_json,parameters_json,credential_refs_json,resources_json,version,last_job_id,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(software_id) DO UPDATE SET template_id=excluded.template_id,template_revision=excluded.template_revision,definition_json=excluded.definition_json,parameters_json=excluded.parameters_json,credential_refs_json=excluded.credential_refs_json,resources_json=excluded.resources_json,version=excluded.version,last_job_id=excluded.last_job_id,updated_at=excluded.updated_at`, p.SoftwareID, p.TemplateID, p.TemplateRevision, jsonText(m.Definition), jsonText(nonNilParameters(m.Parameters)), jsonText(nonNilCredentialRefs(m.ParameterCredentials)), jsonText(resources), version, job, Now())
	if err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM software_management_credentials WHERE software_id=?", p.SoftwareID); err != nil {
		return err
	}
	for _, ref := range (serviceinstall.ManagementRequest{ParameterCredentials: m.ParameterCredentials}).CredentialReferences() {
		if _, err = tx.Exec("INSERT INTO software_management_credentials VALUES(?,?,?)", p.SoftwareID, ref.ID, ref.Revision); err != nil {
			return err
		}
	}
	return nil
}

func nonNilParameters(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func nonNilCredentialRefs(refs map[string]serviceinstall.CredentialReference) map[string]serviceinstall.CredentialReference {
	if refs == nil {
		return map[string]serviceinstall.CredentialReference{}
	}
	return refs
}

func (s *Store) managementOutputSafe(m serviceinstall.ManagementRequest, evidence serviceinstall.ManagementEvidence) error {
	values := []string{evidence.Output}
	if evidence.Table != nil {
		values = append(values, evidence.Table.Columns...)
		for _, row := range evidence.Table.Rows {
			for _, value := range row {
				values = append(values, value)
			}
		}
	}
	for _, ref := range m.CredentialReferences() {
		secret, err := s.parameterSecret(ref)
		if err != nil {
			return err
		}
		for _, value := range values {
			if strings.Contains(value, string(secret)) {
				return errors.New("MANAGEMENT_SECRET_IN_OUTPUT")
			}
		}
	}
	return nil
}
