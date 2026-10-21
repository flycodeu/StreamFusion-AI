package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) submitFirewallPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Key       string `json:"idempotencyKey"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(w, r, &in) != nil || len(in.Key) < 8 || len(in.Key) > 80 || !in.Confirmed {
		h.fail(w, r, 400, "FIREWALL_CONFIRMATION_REQUIRED", "请确认计划和影响范围后提交")
		return
	}
	hash := deploy.Digest([]string{id, in.Key})
	var prior, priorHash string
	e := h.store.DB.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind='FIREWALL_CHANGE' AND idempotency_key=?", s.AdminID, in.Key).Scan(&prior, &priorHash)
	if e == nil {
		if priorHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他计划")
		} else {
			h.success(w, r, map[string]string{"jobId": prior})
		}
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	var raw, targetSnapshot, hostID, status, expiry string
	e = h.store.DB.QueryRow("SELECT change_json,target_snapshot,target_id,status,expires_at FROM firewall_plans WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&raw, &targetSnapshot, &hostID, &status, &expiry)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	var p firewallPlan
	deadline, e2 := time.Parse(time.RFC3339, expiry)
	if deploy.StrictJSON([]byte(raw), &p) != nil || e2 != nil || status != "DRAFT" || time.Now().After(deadline) {
		h.fail(w, r, 409, "FIREWALL_PLAN_STALE", "请重新预检")
		return
	}
	t, e := h.store.readTarget(hostID)
	if e != nil || t.Maintenance || pluginTargetSnapshot(t) != targetSnapshot {
		h.fail(w, r, 409, "HOST_CHANGED", "主机已变化或维护中")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if _, e = h.firewallPreflight(ctx, t, p.Change); e != nil {
		h.fail(w, r, 409, firewallErrorCode(e), firewallErrorMessage(e))
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	current, e := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", hostID))
	if e != nil || current.Maintenance || pluginTargetSnapshot(current) != targetSnapshot {
		h.fail(w, r, 409, "HOST_CHANGED", "主机已变化")
		return
	}
	var existing, oldHash string
	e = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind='FIREWALL_CHANGE' AND idempotency_key=?", s.AdminID, in.Key).Scan(&existing, &oldHash)
	if e == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识冲突")
		} else {
			h.success(w, r, map[string]string{"jobId": existing})
		}
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	var count int
	if e = tx.QueryRow("SELECT count(*) FROM firewall_plans WHERE id=? AND status='DRAFT' AND change_json=? AND expires_at>?", id, raw, Now()).Scan(&count); e != nil || count != 1 {
		h.fail(w, r, 409, "FIREWALL_PLAN_STALE", "计划已变化或过期")
		return
	}
	if e = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+hostID).Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count != 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "主机存在未决作业，请先核对")
		return
	}
	if e = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "未结束作业已达到上限")
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: hostID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: "FIREWALL_CHANGE", Firewall: &p.Change, DeadlineAt: time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, e = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,firewall_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'FIREWALL_CHANGE',?,?,?,?,'QUEUED','FIREWALL_PREFLIGHT',?,?,?,?)", job, s.AdminID, hostID, id, in.Key, hash, jsonText(req), req.TraceID, now, now)
	if e == nil {
		_, e = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+hostID, job)
	}
	if e == nil {
		_, e = tx.Exec("UPDATE firewall_plans SET status='SUBMITTED' WHERE id=?", id)
	}
	if e == nil {
		e = auditTx(tx, s.Username, "FIREWALL_CHANGE_ACCEPTED", hostID, "SUCCESS", req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job})
}

func (h *HTTP) processFirewall(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.Firewall == nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	h.store.mu.Lock()
	res, e := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='FIREWALL_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, t.ID)
	h.store.mu.Unlock()
	if e != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var result deploy.Result
	if e = h.store.callTarget(callCtx, t, req, "", &result); e != nil {
		h.jobUnknown(id, "FIREWALL_RESULT_UNKNOWN")
		return
	}
	if result.Status == "UNKNOWN" {
		if h.recordPendingFirewallResult(id, req, result) != nil {
			h.jobUnknown(id, "RESULT_IDENTITY_MISMATCH")
		}
		return
	}
	if e = h.commitFirewallResult(id, req, result); e != nil {
		h.jobUnknown(id, "FIREWALL_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

func (h *HTTP) recordPendingFirewallResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Firewall == nil || result.Status != "UNKNOWN" || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("RESULT_IDENTITY_MISMATCH")
	}
	code := "FIREWALL_RESULT_UNKNOWN"
	if result.Phase == "FIREWALL_AWAITING_CONFIRM" {
		if _, e := time.Parse(time.RFC3339, result.FirewallConfirmBy); e != nil || result.Firewall == nil || !deploy.Hex.MatchString(result.Firewall.Hash) || !deploy.FirewallEffectMatches(*req.Firewall, *result.Firewall, req.ControllerID) || (req.Firewall.Operation != "ENABLE" && req.Firewall.Operation != "DISABLE") {
			return errors.New("FIREWALL_EVIDENCE_MISMATCH")
		}
		code = "FIREWALL_CONFIRMATION_REQUIRED"
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	_, e := h.store.DB.Exec("UPDATE jobs SET status='UNKNOWN',phase=?,result_json=?,error_code=?,updated_at=? WHERE id=? AND input_json=? AND status IN ('RUNNING','UNKNOWN') AND target_ack_pending=0", result.Phase, jsonText(result), code, Now(), id, jsonText(req))
	return e
}
func (h *HTTP) commitFirewallResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Firewall == nil || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version {
		return errors.New("RESULT_IDENTITY_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("RESULT_NOT_FINAL")
	}
	if result.Status == "SUCCEEDED" && (result.Firewall == nil || result.Firewall.Code != "" || !deploy.Hex.MatchString(result.Firewall.Hash) || result.Phase != "FIREWALL_APPLIED" || !deploy.FirewallEffectMatches(*req.Firewall, *result.Firewall, req.ControllerID)) {
		return errors.New("FIREWALL_EVIDENCE_MISMATCH")
	}
	if result.Status == "FAILED" && result.Phase != "FIREWALL_NOT_APPLIED" && result.Phase != "FIREWALL_ROLLED_BACK" {
		return errors.New("FIREWALL_EVIDENCE_MISMATCH")
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
	_, e = tx.Exec("UPDATE jobs SET status=?,phase=?,result_json=?,error_code=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, jsonText(result), result.ErrorCode, Now(), id)
	if e == nil && result.Firewall != nil {
		_, e = tx.Exec("UPDATE host_firewall_observations SET payload_json=?,attempt_code=?,attempt_at=?,trace_id=? WHERE host_id=?", jsonText(result.Firewall), result.Firewall.Code, Now(), req.TraceID, req.TargetID)
	}
	if e == nil {
		e = auditTx(tx, "worker", "FIREWALL_CHANGE_RESULT", req.TargetID, result.Status, req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
func (h *HTTP) confirmFirewallJob(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var raw, status string
	e := h.store.DB.QueryRow("SELECT input_json,status FROM jobs WHERE id=? AND kind='FIREWALL_CHANGE' AND actor_id=?", id, s.AdminID).Scan(&raw, &status)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if status != "UNKNOWN" {
		h.fail(w, r, 409, "FIREWALL_CONFIRMATION_NOT_PENDING", "作业尚未等待确认或已收尾")
		return
	}
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.Firewall == nil {
		h.internal(w, r, errors.New("INPUT_DAMAGED"))
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	query := req
	query.Action = "FIREWALL_CONFIRM"
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	var result deploy.Result
	if e = h.store.callTarget(ctx, t, query, "", &result); e != nil {
		h.fail(w, r, 409, "FIREWALL_CONFIRMATION_UNAVAILABLE", "新管理连接未确认；目标到期会尝试恢复，保留原作业")
		return
	}
	if e = h.commitFirewallResult(id, req, result); e != nil {
		h.fail(w, r, 409, "FIREWALL_CONFIRMATION_UNAVAILABLE", "尚未确认或恢复冲突，请核对原作业")
		return
	}
	h.ackResult(ctx, t, req, result)
	h.success(w, r, map[string]string{"jobId": id})
}
