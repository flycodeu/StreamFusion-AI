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

func (h *HTTP) enqueuePlugin(w http.ResponseWriter, r *http.Request, s *session, p pluginPlan, key, hash, action, installationID string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	var old, oldHash, oldRaw string
	e = tx.QueryRow("SELECT id,request_hash,input_json FROM jobs WHERE actor_id=? AND kind=? AND idempotency_key=?", s.AdminID, action, key).Scan(&old, &oldHash, &oldRaw)
	if e == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已被其他操作使用")
			return
		}
		var req deploy.Request
		if deploy.StrictJSON([]byte(oldRaw), &req) != nil {
			h.internal(w, r, errors.New("INPUT_DAMAGED"))
			return
		}
		h.success(w, r, map[string]string{"jobId": old, "pluginInstallationId": req.PluginInstallationID})
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	var current target
	e = tx.QueryRow("SELECT id,name,install_root,kind,address,ssh_port,ssh_user,host_fingerprint,coalesce(credential_id,''),coalesce(CAST(credential_revision AS TEXT),'') FROM targets WHERE id=?", p.TargetID).Scan(&current.ID, &current.Name, &current.InstallRoot, &current.Kind, &current.Address, &current.Port, &current.User, &current.Fingerprint, &current.CredentialID, &current.Revision)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if pluginTargetSnapshot(current) != p.TargetSnapshot {
		h.fail(w, r, 409, "PLUGIN_PLAN_STALE", "目标配置已变化，请重新预检")
		return
	}
	var count int
	if e = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "未结束作业达到20个")
		return
	}
	if e = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+p.TargetID).Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "目标有未决作业，请先核对")
		return
	}
	d := p.Definition
	jobID, now := uuid.NewString(), Now()
	if action == "PLUGIN_INSTALL" {
		var raw, state, expiresRaw string
		if e = tx.QueryRow("SELECT snapshot_json,status,expires_at FROM plugin_install_plans WHERE id=?", p.ID).Scan(&raw, &state, &expiresRaw); e != nil {
			h.opsError(w, r, e)
			return
		}
		expires, parseErr := time.Parse(time.RFC3339, expiresRaw)
		if state != "DRAFT" || parseErr != nil || time.Now().After(expires) || raw != jsonText(p) {
			h.fail(w, r, 409, "PLUGIN_PLAN_STALE", "计划已提交或变化，请重新预检")
			return
		}
		i, readErr := scanPlugin(tx.QueryRow("SELECT "+pluginColumns+" FROM plugin_installations WHERE target_id=? AND plugin_key=? AND plugin_version=? AND image_digest=?", p.TargetID, d.Key, d.Version, d.Digest))
		if readErr == nil {
			if i.Status != "FAILED" {
				h.fail(w, r, 409, "PLUGIN_ALREADY_INSTALLED", "该版本已登记，请刷新状态或核对原作业")
				return
			}
			installationID = i.ID
			_, e = tx.Exec("UPDATE plugin_installations SET status='INSTALLING',last_job_id=?,error_code='',version=version+1,updated_at=? WHERE id=?", jobID, now, i.ID)
		} else if errors.Is(readErr, sql.ErrNoRows) {
			installationID = uuid.NewString()
			_, e = tx.Exec("INSERT INTO plugin_installations(id,target_id,plugin_key,plugin_version,kind,install_mode,image_ref,image_digest,image_id,recipe_revision,platform,status,last_job_id,created_at,updated_at) VALUES(?,?,?,?,?,'DOCKER_IMAGE',?,?,'',?,?,'INSTALLING',?,?,?)", installationID, p.TargetID, d.Key, d.Version, d.Kind, d.Image, d.Digest, d.RecipeRevision, d.Platform, jobID, now, now)
		} else {
			e = readErr
		}
		if e == nil {
			_, e = tx.Exec("UPDATE plugin_install_plans SET status='SUBMITTED' WHERE id=?", p.ID)
		}
	} else {
		i, readErr := scanPlugin(tx.QueryRow("SELECT "+pluginColumns+" FROM plugin_installations WHERE id=?", installationID))
		if readErr != nil {
			h.opsError(w, r, readErr)
			return
		}
		n, refErr := pluginReferences(tx, i)
		if refErr != nil {
			h.internal(w, r, refErr)
			return
		}
		if n > 0 {
			h.fail(w, r, 409, "PLUGIN_REFERENCE_EXISTS", "环境仍被引用，不能卸载")
			return
		}
		if i.Status != "READY" && i.Status != "FAILED" {
			h.fail(w, r, 409, "PLUGIN_REMOVE_BLOCKED", "环境状态已变化，请刷新")
			return
		}
		_, e = tx.Exec("UPDATE plugin_installations SET status='REMOVING',last_job_id=?,version=version+1,updated_at=? WHERE id=?", jobID, now, installationID)
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.TargetID, JobID: jobID, TraceID: r.Context().Value(traceKey{}).(string), Action: action, PluginOperation: action, Plugin: pluginRequest(d), PluginInstallationID: installationID, DeadlineAt: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, e = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,plugin_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,?,?,nullif(?,''),?,?,'QUEUED','PREFLIGHT',?,?,?,?)", jobID, s.AdminID, action, p.TargetID, p.ID, key, hash, jsonText(req), req.TraceID, now, now)
	if e == nil {
		_, e = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+p.TargetID, jobID)
	}
	if e == nil {
		e = auditTx(tx, s.Username, action+"_ACCEPTED", jobID, "SUCCESS", req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	w.Header().Set("Location", api+"/jobs/"+jobID)
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": jobID, "pluginInstallationId": installationID})
}

func (h *HTTP) processPlugin(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.Plugin == nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	res, e := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED'", Now(), id)
	if e != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	execCtx, cancel := context.WithTimeout(ctx, 9*time.Minute)
	defer cancel()
	progressCtx, stop := context.WithCancel(execCtx)
	done := make(chan struct{})
	go func() { defer close(done); h.pollProgress(progressCtx, t, req) }()
	var result deploy.Result
	e = h.store.callTarget(execCtx, t, req, "", &result)
	stop()
	<-done
	if e != nil {
		h.logger.Printf("traceId=%s jobId=%s targetId=%s pluginResult=UNCONFIRMED", req.TraceID, id, req.TargetID)
		h.jobUnknown(id, "TARGET_RESULT_UNKNOWN")
		return
	}
	if e = h.commitPluginResult(id, req, result); e != nil {
		h.logger.Printf("traceId=%s jobId=%s targetId=%s pluginResult=MISMATCH", req.TraceID, id, req.TargetID)
		h.jobUnknown(id, "PLUGIN_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

// Only QUEUED plugin work can be cancelled. The worker claims RUNNING with a
// conditional update before target invocation, so cancellation cannot race a
// target side effect or clear a RUNNING/UNKNOWN lock.
func (h *HTTP) cancelPluginJob(w http.ResponseWriter, r *http.Request, s *session, id string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	var status, raw string
	if e = tx.QueryRow("SELECT status,input_json FROM jobs WHERE id=? AND kind IN ('PLUGIN_INSTALL','PLUGIN_REMOVE')", id).Scan(&status, &raw); e != nil {
		h.opsError(w, r, e)
		return
	}
	if status != "QUEUED" {
		h.fail(w, r, 409, "PLUGIN_CANCEL_BLOCKED", "只可取消尚未执行的插件作业")
		return
	}
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil {
		h.internal(w, r, errors.New("INPUT_DAMAGED"))
		return
	}
	_, e = tx.Exec("UPDATE jobs SET status='FAILED',phase='CANCELLED',error_code='JOB_CANCELLED',updated_at=? WHERE id=? AND status='QUEUED'", Now(), id)
	if e == nil {
		_, e = tx.Exec("UPDATE plugin_installations SET status='FAILED',error_code='JOB_CANCELLED',version=version+1,updated_at=? WHERE id=? AND last_job_id=?", Now(), req.PluginInstallationID, id)
	}
	if e == nil {
		_, e = tx.Exec("DELETE FROM operation_locks WHERE job_id=?", id)
	}
	if e == nil {
		e = auditTx(tx, s.Username, "PLUGIN_JOB_CANCELLED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, map[string]string{"jobId": id})
}

func (h *HTTP) commitPluginResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Plugin == nil || !deploy.ID.MatchString(req.PluginInstallationID) || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("RESULT_IDENTITY_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("RESULT_NOT_FINAL")
	}
	if result.PluginKey != req.Plugin.Key || result.PluginVersion != req.Plugin.Version || result.PluginKind != req.Plugin.Kind || result.PluginImageDigest != req.Plugin.Digest || result.Platform != req.Plugin.Platform {
		return errors.New("PLUGIN_EVIDENCE_MISMATCH")
	}
	if result.Status == "SUCCEEDED" {
		if req.Action == "PLUGIN_INSTALL" && (result.Health != "PLUGIN_READY" || result.ObservedVersion == "" || len(result.ObservedVersion) > 8192 || len(result.ImageID) != 71 || result.ImageID[:7] != "sha256:" || !deploy.Hex.MatchString(result.ImageID[7:])) {
			return errors.New("PLUGIN_EVIDENCE_MISMATCH")
		}
		if req.Action == "PLUGIN_REMOVE" && result.Health != "PLUGIN_REMOVED" {
			return errors.New("PLUGIN_EVIDENCE_MISMATCH")
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
	if result.Status == "SUCCEEDED" && req.Action == "PLUGIN_REMOVE" {
		_, e = tx.Exec("DELETE FROM plugin_installations WHERE id=? AND last_job_id=?", req.PluginInstallationID, id)
	} else {
		status := "FAILED"
		if result.Status == "SUCCEEDED" {
			status = "READY"
		}
		_, e = tx.Exec("UPDATE plugin_installations SET status=?,image_id=CASE WHEN ?<>'' THEN ? ELSE image_id END,observed_version=CASE WHEN ?<>'' THEN ? ELSE observed_version END,error_code=?,observed_at=?,updated_at=?,version=version+1 WHERE id=? AND last_job_id=?", status, result.ImageID, result.ImageID, result.ObservedVersion, result.ObservedVersion, result.ErrorCode, result.ObservedAt, Now(), req.PluginInstallationID, id)
	}
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if e == nil {
		e = auditTx(tx, "worker", req.Action+"_RESULT", id, result.Status, req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
