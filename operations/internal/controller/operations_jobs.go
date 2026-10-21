package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) listJobs(w http.ResponseWriter, r *http.Request) {
	rows, e := h.store.DB.Query("SELECT id,kind,coalesce(instance_id,''),coalesce(target_id,''),status,phase,error_code,target_ack_pending,trace_id,result_json,created_at,updated_at FROM jobs ORDER BY created_at DESC LIMIT 100")
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, k, inst, hostID, st, ph, code, trace, raw, created, updated string
		var pending bool
		if e = rows.Scan(&id, &k, &inst, &hostID, &st, &ph, &code, &pending, &trace, &raw, &created, &updated); e != nil {
			h.internal(w, r, e)
			return
		}
		out = append(out, map[string]any{"id": id, "kind": k, "instanceId": inst, "targetId": hostID, "status": st, "phase": ph, "errorCode": code, "targetAckPending": pending, "traceId": trace, "result": json.RawMessage(raw), "createdAt": created, "updatedAt": updated})
	}
	h.success(w, r, out)
}
func (h *HTTP) getJob(w http.ResponseWriter, r *http.Request, id string) {
	var kind, targetID, instanceID, status, phase, code, trace, raw, created, updated string
	var pending bool
	e := h.store.DB.QueryRow("SELECT kind,coalesce(target_id,''),coalesce(instance_id,''),status,phase,error_code,target_ack_pending,trace_id,result_json,created_at,updated_at FROM jobs WHERE id=?", id).Scan(&kind, &targetID, &instanceID, &status, &phase, &code, &pending, &trace, &raw, &created, &updated)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	h.success(w, r, map[string]any{"id": id, "kind": kind, "targetId": targetID, "instanceId": instanceID, "status": status, "phase": phase, "errorCode": code, "targetAckPending": pending, "traceId": trace, "result": json.RawMessage(raw), "createdAt": created, "updatedAt": updated})
}
func (h *HTTP) RunOperations(ctx context.Context) {
	h.store.recoverInstallationMaterials()
	_, _ = h.store.DB.Exec("UPDATE jobs SET status='UNKNOWN',error_code='CONTROLLER_RESTARTED' WHERE status='RUNNING' AND kind<>'IMPORT'; UPDATE jobs SET status='FAILED',error_code='UPLOAD_INTERRUPTED' WHERE kind='IMPORT' AND (phase='UPLOADING' OR status='RUNNING')")
	_, _ = h.store.DB.Exec("UPDATE plugin_installations SET status='UNKNOWN',error_code='CONTROLLER_RESTARTED' WHERE last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	_, _ = h.store.DB.Exec("UPDATE software_instances SET actual_state='UNKNOWN',error_code='CONTROLLER_RESTARTED' WHERE last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	_, _ = h.store.DB.Exec("UPDATE software_instances SET installation_status='UNKNOWN' WHERE installation_status='INSTALLING' AND last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.finishPendingAck(ctx)
			var id, kind, raw string
			e := h.store.DB.QueryRow("SELECT id,kind,input_json FROM jobs WHERE status='QUEUED' AND phase<>'WAITING_UPLOAD' AND phase<>'UPLOADING' AND phase<>'UPLOADED' AND (target_id IS NULL OR EXISTS(SELECT 1 FROM targets WHERE targets.id=jobs.target_id AND maintenance=0)) ORDER BY created_at LIMIT 1").Scan(&id, &kind, &raw)
			if e == nil {
				if kind == "IMPORT" {
					h.processImport(id, raw)
				} else if kind == "PLUGIN_INSTALL" || kind == "PLUGIN_REMOVE" {
					h.processPlugin(ctx, id, raw)
				} else if kind == "FIREWALL_CHANGE" {
					h.processFirewall(ctx, id, raw)
				} else if len(kind) > 9 && kind[:9] == "SOFTWARE_" {
					h.processSoftware(ctx, id, raw)
				} else {
					h.processRelease(ctx, id, raw)
				}
			}
		}
	}
}
func (h *HTTP) finishPendingAck(ctx context.Context) {
	var raw, resultRaw string
	e := h.store.DB.QueryRow("SELECT input_json,result_json FROM jobs WHERE target_ack_pending=1 AND status IN ('SUCCEEDED','FAILED') ORDER BY updated_at LIMIT 1").Scan(&raw, &resultRaw)
	if e != nil {
		return
	}
	var req deploy.Request
	var result deploy.Result
	if deploy.StrictJSON([]byte(raw), &req) != nil || deploy.StrictJSON([]byte(resultRaw), &result) != nil {
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e == nil {
		h.ackResult(ctx, t, req, result)
	}
}
func (h *HTTP) jobUnknown(id, code string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	res, err := h.store.DB.Exec("UPDATE jobs SET status='UNKNOWN',error_code=?,updated_at=? WHERE id=? AND status IN ('QUEUED','RUNNING','UNKNOWN') AND phase<>'CANCELLED'", code, Now(), id)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	_, _ = h.store.DB.Exec("UPDATE plugin_installations SET status='UNKNOWN',error_code=?,updated_at=? WHERE last_job_id=?", code, Now(), id)
	_, _ = h.store.DB.Exec("UPDATE software_instances SET actual_state='UNKNOWN',error_code=?,updated_at=? WHERE last_job_id=?", code, Now(), id)
	_, _ = h.store.DB.Exec("UPDATE software_instances SET installation_status='UNKNOWN' WHERE installation_status='INSTALLING' AND last_job_id=?", id)
}
func (h *HTTP) processRelease(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	h.store.mu.Lock()
	claim, e := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, req.TargetID)
	h.store.mu.Unlock()
	if e != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n != 1 {
		return
	}
	execCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	var result deploy.Result
	progressCtx, stopProgress := context.WithCancel(execCtx)
	progressDone := make(chan struct{})
	go func() { defer close(progressDone); h.pollProgress(progressCtx, t, req) }()
	e = h.store.callTarget(execCtx, t, req, h.store.artifactPath(req.Candidate.ArtifactID), &result)
	stopProgress()
	<-progressDone
	if e != nil {
		h.logger.Printf("traceId=%s jobId=%s target=UNCONFIRMED", req.TraceID, id)
		h.jobUnknown(id, "TARGET_RESULT_UNKNOWN")
		return
	}
	if e = h.commitResult(id, req, result); e != nil {
		h.jobUnknown(id, "RESULT_COMMIT_FAILED")
		return
	}
	h.ackResult(ctx, t, req, result)
}
