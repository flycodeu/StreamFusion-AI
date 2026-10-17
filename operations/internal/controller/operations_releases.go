package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) createPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		MaintenancePermit string `json:"maintenancePermit"`
		Kind              string `json:"kind"`
		ArtifactID        string `json:"artifactId"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	if in.Kind != "PUBLISH" && in.Kind != "ROLLBACK" {
		h.fail(w, r, 400, "PLAN_KIND_INVALID", "仅支持发布与回滚")
		return
	}
	i, e := h.store.readInstance(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	p := plan{ID: uuid.NewString(), Kind: in.Kind, TargetID: i.TargetID, InstanceID: id, ExpectedVersion: i.Version, ExpectedGeneration: i.Generation, ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	p.MaintenancePermit = in.MaintenancePermit
	if i.Current != "" {
		baseline, e := h.store.readRelease(i.Current)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		p.Baseline = &baseline
	}
	if in.Kind == "ROLLBACK" {
		if i.Previous == "" {
			h.fail(w, r, 409, "PREVIOUS_RELEASE_MISSING", "没有上一成功版本")
			return
		}
		p.Candidate, e = h.store.readRelease(i.Previous)
	} else {
		if !deploy.ID.MatchString(in.ArtifactID) {
			h.fail(w, r, 400, "ARTIFACT_REQUIRED", "请选择制品")
			return
		}
		p.Candidate = deploy.Release{ID: uuid.NewString(), InstanceID: id, ArtifactID: in.ArtifactID, Config: i.Config}
		e = h.store.DB.QueryRow("SELECT archive_sha256,byte_size,image_id FROM artifacts WHERE id=?", in.ArtifactID).Scan(&p.Candidate.ArchiveSHA256, &p.Candidate.ByteSize, &p.Candidate.ImageID)
	}
	if p.Kind == "PUBLISH" {
		for _, bindingID := range p.Candidate.Config.BindingIDs {
			b, e := h.store.readBinding(bindingID)
			if e != nil {
				h.opsError(w, r, e)
				return
			}
			p.Candidate.Bindings = append(p.Candidate.Bindings, b)
		}
	}
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if p.Baseline != nil && p.Baseline.Config.DataCompatibility != p.Candidate.Config.DataCompatibility {
		h.fail(w, r, 409, "DATA_COMPATIBILITY_BLOCKED", "候选与恢复版本的数据兼容标识不同，须先评估数据维护方案")
		return
	}
	if e = deploy.ValidatePreviewTransition(&p.Candidate, p.Baseline, p.MaintenancePermit); e != nil {
		h.fail(w, r, 409, e.Error(), "请先在平台排空节点并取得维护许可；节点身份不可原地变更")
		return
	}
	checkSecrets := deploy.Request{Candidate: &p.Candidate, Baseline: p.Baseline}
	if e = h.store.releaseSecrets(&checkSecrets); e != nil {
		h.fail(w, r, 409, "CREDENTIAL_UNAVAILABLE", "候选或恢复版本的凭据修订不可用")
		return
	}
	for _, rel := range []*deploy.Release{&p.Candidate, p.Baseline} {
		if rel != nil {
			var recipe string
			if e = h.store.DB.QueryRow("SELECT recipe FROM artifacts WHERE id=?", rel.ArtifactID).Scan(&recipe); e != nil || recipe != rel.Config.Recipe() {
				h.fail(w, r, 409, "ARTIFACT_RECIPE_MISMATCH", "请选择与实例配方一致的制品")
				return
			}
			image, e := deploy.ValidateRecipeArchive(h.store.artifactPath(rel.ArtifactID), rel.ArchiveSHA256, rel.ByteSize, rel.Config.Recipe())
			if e != nil || image != rel.ImageID {
				h.fail(w, r, 409, "RECOVERY_MATERIAL_MISSING", "候选或恢复镜像归档缺失或损坏")
				return
			}
		}
	}
	t, e := h.store.readTarget(i.TargetID)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	probeResult, e := h.probe(t)
	if e != nil {
		h.fail(w, r, 409, "TARGET_NOT_READY", "目标执行器或Docker未就绪")
		return
	}
	var targetFact struct {
		PendingJobID  string `json:"pendingJobId"`
		PreviewRecipe string `json:"previewRecipe"`
	}
	if json.Unmarshal(probeResult, &targetFact) != nil || targetFact.PendingJobID != "" {
		h.fail(w, r, 409, "TARGET_PENDING", "目标仍有未决作业，禁止从旧备份或新计划绕过")
		return
	}
	if p.Candidate.Config.Preview != nil && targetFact.PreviewRecipe != deploy.PreviewRecipe {
		h.fail(w, r, 409, "TARGET_PREVIEW_RECIPE_REQUIRED", "目标执行器不支持预览节点配方，请先更新执行器")
		return
	}
	p.InputHash = deploy.Digest(p)
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	var version, gen string
	e = tx.QueryRow("SELECT CAST(version AS TEXT),CAST(applied_generation AS TEXT) FROM instances WHERE id=?", id).Scan(&version, &gen)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if version != p.ExpectedVersion || gen != p.ExpectedGeneration {
		h.fail(w, r, 409, "VERSION_CONFLICT", "预检期间配置已变化，请重新预检")
		return
	}
	_, e = tx.Exec("INSERT INTO operation_plans VALUES(?,?,?,?,?)", p.ID, id, jsonText(p), p.ExpiresAt, Now())
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, p)
}
func (h *HTTP) submitPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	if len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 80 {
		h.fail(w, r, 400, "IDEMPOTENCY_REQUIRED", "提交标识无效")
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
	var raw string
	e = tx.QueryRow("SELECT snapshot_json FROM operation_plans WHERE id=?", id).Scan(&raw)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	var p plan
	if e = deploy.StrictJSON([]byte(raw), &p); e != nil {
		h.internal(w, r, e)
		return
	}
	hash := hashToken(id)
	var old, oldHash string
	e = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind=? AND idempotency_key=?", s.AdminID, p.Kind, in.IdempotencyKey).Scan(&old, &oldHash)
	if e == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "同一提交标识引用不同计划")
		} else {
			h.success(w, r, map[string]string{"jobId": old})
		}
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	e = tx.QueryRow("SELECT id FROM jobs WHERE plan_id=?", id).Scan(&old)
	if e == nil {
		h.fail(w, r, 409, "PLAN_ALREADY_ACCEPTED", "计划已提交，请查看原作业或重新预检")
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	expiry, _ := time.Parse(time.RFC3339, p.ExpiresAt)
	var version, gen string
	if e = tx.QueryRow("SELECT CAST(version AS TEXT),CAST(applied_generation AS TEXT) FROM instances WHERE id=?", p.InstanceID).Scan(&version, &gen); e != nil {
		h.internal(w, r, e)
		return
	}
	if time.Now().After(expiry) || version != p.ExpectedVersion || gen != p.ExpectedGeneration {
		h.fail(w, r, 409, "PLAN_STALE", "计划已过期或实例变化，请重新预检")
		return
	}
	var count int
	_ = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count)
	if count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "未结束作业达到20个")
		return
	}
	_ = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key IN (?,?)", "target:"+p.TargetID, "instance:"+p.InstanceID).Scan(&count)
	if count > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "存在未决作业，请先核对或恢复")
		return
	}
	jobID := uuid.NewString()
	if p.Kind == "PUBLISH" {
		_, e = tx.Exec("INSERT INTO releases VALUES(?,?,?,?,?)", p.Candidate.ID, p.InstanceID, p.Candidate.ArtifactID, jsonText(p.Candidate), Now())
		if e != nil {
			h.internal(w, r, e)
			return
		}
		if e = bindReleaseTx(tx, p.Candidate); e != nil {
			h.internal(w, r, e)
			return
		}
	}
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.TargetID, JobID: jobID, TraceID: r.Context().Value(traceKey{}).(string), Action: "ACTIVATE", Candidate: &p.Candidate, Baseline: p.Baseline, DeadlineAt: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)}
	req.MaintenancePermit = p.MaintenancePermit
	req.InputHash = deploy.Digest(req)
	now := Now()
	_, e = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,instance_id,plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", jobID, s.AdminID, p.Kind, p.TargetID, p.InstanceID, p.ID, p.Candidate.ID, in.IdempotencyKey, hash, jsonText(req), req.TraceID, now, now)
	if e == nil {
		_, e = tx.Exec("INSERT INTO operation_locks VALUES(?,?),(?,?)", "target:"+p.TargetID, jobID, "instance:"+p.InstanceID, jobID)
	}
	if e == nil {
		e = auditTx(tx, s.Username, "PUBLISH_ACCEPTED", jobID, "SUCCESS", req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	w.Header().Set("Location", api+"/jobs/"+jobID)
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": jobID})
}
func (h *HTTP) storeIDTx(tx *sql.Tx) string {
	var id string
	_ = tx.QueryRow("SELECT controller_id FROM controller_state WHERE id=1").Scan(&id)
	return id
}

func (h *HTTP) commitResult(id string, req deploy.Request, result deploy.Result) error {
	if result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("RESULT_IDENTITY_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		h.jobUnknown(id, "RECOVERY_REQUIRED")
		return errors.New("RESULT_NOT_FINAL")
	}
	if result.Status == "SUCCEEDED" && (result.ReleaseID != req.Candidate.ID || result.ImageID != req.Candidate.ImageID || result.ConfigHash != deploy.Digest(req.Candidate.Config) || result.Health != "APPLICATION_READY") {
		return errors.New("HEALTH_EVIDENCE_MISMATCH")
	}
	if result.Status == "FAILED" && req.Baseline != nil && (result.ReleaseID != req.Baseline.ID || result.ImageID != req.Baseline.ImageID || result.ConfigHash != deploy.Digest(req.Baseline.Config) || result.Health != "APPLICATION_READY") {
		return errors.New("RECOVERY_EVIDENCE_MISMATCH")
	}
	if result.Status == "FAILED" && req.Baseline == nil && (result.Health != "STOPPED" || result.ReleaseID != "") {
		return errors.New("STOP_EVIDENCE_MISMATCH")
	}
	evidenceRelease := req.Candidate
	if result.Status == "FAILED" {
		evidenceRelease = req.Baseline
	}
	if evidenceRelease != nil && evidenceRelease.Config.Preview != nil {
		observed := result.Preview
		if observed == nil || observed.ProtocolVersion != 1 || observed.NodeID != evidenceRelease.Config.Preview.NodeID || !deploy.ID.MatchString(observed.BootID) || !deploy.Hex.MatchString(observed.ConfigVersion) || observed.MaxSessions != evidenceRelease.Config.Preview.MaxViewers || observed.MaxTranscodes != evidenceRelease.Config.Preview.MaxTranscodes || (observed.State != "READY" && observed.State != "MAINTENANCE") {
			return errors.New("PREVIEW_HEALTH_EVIDENCE_MISMATCH")
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.store.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var status string
	var pending bool
	e = tx.QueryRow("SELECT status,target_ack_pending FROM jobs WHERE id=?", id).Scan(&status, &pending)
	if e != nil {
		return e
	}
	if status == "SUCCEEDED" || status == "FAILED" {
		return nil
	}
	var raw string
	e = tx.QueryRow("SELECT snapshot_json FROM operation_plans WHERE id=(SELECT plan_id FROM jobs WHERE id=?)", id).Scan(&raw)
	if e != nil {
		return e
	}
	var p plan
	if e = deploy.StrictJSON([]byte(raw), &p); e != nil {
		return e
	}
	if result.Status == "SUCCEEDED" {
		old := ""
		if req.Baseline != nil {
			old = req.Baseline.ID
		}
		res, e := tx.Exec("UPDATE instances SET previous_release_id=nullif(?,''),current_release_id=?,applied_generation=applied_generation+1 WHERE id=? AND CAST(applied_generation AS TEXT)=? AND coalesce(current_release_id,'')=?", old, req.Candidate.ID, req.Candidate.InstanceID, p.ExpectedGeneration, old)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errors.New("APPLIED_GENERATION_CONFLICT")
		}
	}
	_, e = tx.Exec("INSERT INTO observations VALUES(?,?,?) ON CONFLICT(instance_id) DO UPDATE SET payload_json=excluded.payload_json,observed_at=excluded.observed_at", req.Candidate.InstanceID, jsonText(result), Now())
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if e == nil {
		e = auditTx(tx, "worker", "PUBLISH_RESULT", id, result.Status, req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
func (h *HTTP) ackResult(ctx context.Context, t target, req deploy.Request, result deploy.Result) {
	req.Action = "ACK"
	req.ResultHash = deploy.Digest(result)
	req.Candidate = nil
	req.Baseline = nil
	ackCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var ack deploy.Result
	if e := h.store.callTarget(ackCtx, t, req, "", &ack); e != nil || !ack.Ack || ack.JobID != result.JobID || ack.InputHash != result.InputHash {
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.store.DB.Begin()
	if e != nil {
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("UPDATE jobs SET target_ack_pending=0 WHERE id=? AND target_ack_pending=1", result.JobID)
	if e == nil {
		_, e = tx.Exec("DELETE FROM operation_locks WHERE job_id=?", result.JobID)
	}
	if e == nil {
		_ = tx.Commit()
	}
}
func (h *HTTP) reconcile(w http.ResponseWriter, r *http.Request, id string, recover bool) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	var raw, status, resultRaw string
	var pending bool
	e := h.store.DB.QueryRow("SELECT input_json,status,result_json,target_ack_pending FROM jobs WHERE id=? AND kind<>'IMPORT'", id).Scan(&raw, &status, &resultRaw, &pending)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if status != "UNKNOWN" && !pending {
		h.fail(w, r, 409, "RECONCILE_NOT_REQUIRED", "作业正在执行或已经收尾")
		return
	}
	var req deploy.Request
	if e = deploy.StrictJSON([]byte(raw), &req); e != nil {
		h.internal(w, r, e)
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if pending {
		var result deploy.Result
		_ = json.Unmarshal([]byte(resultRaw), &result)
		h.ackResult(ctx, t, req, result)
		h.success(w, r, map[string]string{"jobId": id})
		return
	}
	action := "STATUS"
	if recover && req.Plugin == nil {
		action = "RECOVER"
	}
	query := req
	query.Action = action
	var result deploy.Result
	if e = h.store.callTarget(ctx, t, query, "", &result); e != nil {
		h.fail(w, r, 409, "TARGET_RESULT_UNKNOWN", "目标结果未确认，锁仍保留；核对执行器与目标日志")
		return
	}
	if result.Status == "UNKNOWN" {
		h.fail(w, r, 409, "RECOVERY_REQUIRED", "结果尚未确认，相关变更已锁定；可按冻结基线恢复")
		return
	}
	if req.Plugin != nil {
		e = h.commitPluginResult(id, req, result)
	} else {
		e = h.commitResult(id, req, result)
	}
	if e != nil {
		h.fail(w, r, 409, "RESULT_MISMATCH", "目标证据与发布基线不一致，不能放行")
		return
	}
	h.ackResult(ctx, t, req, result)
	h.success(w, r, map[string]string{"jobId": id})
}
