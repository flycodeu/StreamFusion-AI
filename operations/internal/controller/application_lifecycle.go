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
	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

func applicationLifecycleAction(action string) bool {
	return action == "APP_START" || action == "APP_STOP" || action == "APP_RESTART" || action == "APP_RUNTIME_POLICY"
}

func (h *HTTP) changeApplicationLifecycle(w http.ResponseWriter, r *http.Request, actor *session, id string, policy bool) {
	var in struct {
		ExpectedVersion string                `json:"expectedVersion"`
		Key             string                `json:"idempotencyKey"`
		Action          string                `json:"action"`
		Policy          *runtimepolicy.Policy `json:"policy,omitempty"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	action := "APP_" + in.Action
	if policy {
		action = "APP_RUNTIME_POLICY"
	}
	if !deploy.ID.MatchString(id) || !applicationLifecycleAction(action) || in.ExpectedVersion == "" || len(strings.TrimSpace(in.Key)) < 8 || len(in.Key) > 80 || policy != (in.Policy != nil) || in.Policy != nil && in.Policy.Validate("DOCKER") != nil {
		h.fail(w, r, 400, "RUNTIME_POLICY_INVALID", "请填写有效的运行策略和当前版本")
		return
	}
	hash := deploy.Digest(struct {
		ID, Version, Action string
		Policy              *runtimepolicy.Policy
	}{id, in.ExpectedVersion, action, in.Policy})
	if h.replayApplicationLifecycle(w, r, actor, action, in.Key, hash) {
		return
	}
	i, err := h.store.readInstance(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if i.Current == "" {
		h.fail(w, r, 409, "NOT_DEPLOYED", "请先发布应用")
		return
	}
	t, err := h.store.readTarget(i.TargetID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	var capability executorCapabilities
	err = h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &capability)
	cancel()
	if err != nil {
		h.fail(w, r, 409, "EXECUTOR_UNAVAILABLE", "无法连接接入组件，请检查主机连接")
		return
	}
	if capability.Owner.ControllerID != h.store.controllerID() || capability.Owner.TargetID != t.ID || capability.Owner.ExecutorVersion != deploy.Version || capability.RuntimePolicyVersion != runtimepolicy.Version {
		h.fail(w, r, 409, "EXECUTOR_UPGRADE_REQUIRED", "运行策略需要更新接入组件")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var current, hostVersion, version string
	err = tx.QueryRow("SELECT coalesce(current_release_id,''),CAST(version AS TEXT) FROM instances WHERE id=? AND target_id=?", id, i.TargetID).Scan(&current, &version)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if version != in.ExpectedVersion || current != i.Current {
		h.fail(w, r, 409, "VERSION_CONFLICT", "应用已变化，请刷新")
		return
	}
	if err = hostMutationAllowed(tx, t.ID); err != nil {
		h.fail(w, r, 409, "HOST_MAINTENANCE", "主机处于维护中")
		return
	}
	if err = tx.QueryRow("SELECT CAST(version AS TEXT) FROM targets WHERE id=?", t.ID).Scan(&hostVersion); err != nil {
		h.internal(w, r, err)
		return
	}
	if hostVersion != t.Version {
		h.fail(w, r, 409, "HOST_CHANGED", "主机已变化，请刷新")
		return
	}
	var old, oldHash string
	err = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind=? AND idempotency_key=?", actor.AdminID, action, in.Key).Scan(&old, &oldHash)
	if err == nil {
		if oldHash == hash {
			h.success(w, r, map[string]string{"jobId": old, "instanceId": id})
		} else {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他操作")
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key IN (?,?)", "target:"+t.ID, "instance:"+id).Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "应用或主机有未完成操作，请先核对")
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
	var raw string
	if err = tx.QueryRow("SELECT snapshot_json FROM releases WHERE id=? AND instance_id=?", current, id).Scan(&raw); err != nil {
		h.internal(w, r, err)
		return
	}
	var release deploy.Release
	if err = deploy.StrictJSON([]byte(raw), &release); err != nil {
		h.internal(w, r, err)
		return
	}
	if release.Config.Preview != nil || release.Config.ZLM != nil {
		h.fail(w, r, 409, "NODE_MAINTENANCE_REQUIRED", "请通过节点维护操作修改此实例")
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: t.ID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: action, ApplicationOperation: action, Candidate: &release, RuntimePolicy: &runtimepolicy.Change{Version: runtimepolicy.Version, Policy: in.Policy}, ExecutionLogVersion: 1, DeadlineAt: time.Now().UTC().Add(12 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,instance_id,release_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", job, actor.AdminID, action, t.ID, id, current, in.Key, hash, jsonText(req), req.TraceID, now, now)
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?),(?,?)", "target:"+t.ID, job, "instance:"+id, job)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE instances SET version=version+1 WHERE id=?", id)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, action+"_ACCEPTED", id, "SUCCESS", req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job, "instanceId": id})
}

func (h *HTTP) replayApplicationLifecycle(w http.ResponseWriter, r *http.Request, actor *session, action, key, hash string) bool {
	var job, oldHash string
	err := h.store.DB.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind=? AND idempotency_key=?", actor.AdminID, action, key).Scan(&job, &oldHash)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		h.internal(w, r, err)
	} else if hash != oldHash {
		h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他操作")
	} else {
		h.success(w, r, map[string]string{"jobId": job})
	}
	return true
}

func (h *HTTP) commitApplicationLifecycleResult(id string, req deploy.Request, result deploy.Result) error {
	if req.Candidate == nil || req.RuntimePolicy == nil || !applicationLifecycleAction(req.ApplicationOperation) || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash || result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("RESULT_IDENTITY_MISMATCH")
	}
	if result.Status == "SUCCEEDED" {
		if result.ReleaseID != req.Candidate.ID || result.ImageID != req.Candidate.ImageID || result.ConfigHash != deploy.Digest(req.Candidate.Config) || result.Health != "APPLICATION_READY" && result.Health != "STOPPED" {
			return errors.New("RUNTIME_POLICY_UNCONFIRMED")
		}
		if err := validateRuntimePolicyResult(req, result, "DOCKER"); err != nil {
			return err
		}
		if req.Action == "APP_STOP" && result.Health != "STOPPED" || (req.Action == "APP_START" || req.Action == "APP_RESTART") && result.Health != "APPLICATION_READY" || result.RuntimePolicy.Held && result.Health != "STOPPED" {
			return errors.New("RUNTIME_POLICY_STATE_MISMATCH")
		}
		if req.Action == "APP_RESTART" {
			evidence := result.Lifecycle
			if evidence == nil || evidence.Action != req.Action || evidence.Before == "" || evidence.After == "" || evidence.Before == evidence.After {
				return errors.New("SOFTWARE_RESTART_UNCONFIRMED")
			}
			if _, err := time.Parse(time.RFC3339Nano, evidence.ObservedAt); err != nil {
				return errors.New("SOFTWARE_RESTART_UNCONFIRMED")
			}
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, current string
	if err = tx.QueryRow("SELECT j.status,i.current_release_id FROM jobs j JOIN instances i ON i.id=j.instance_id WHERE j.id=? AND j.input_json=?", id, jsonText(req)).Scan(&status, &current); err != nil {
		return err
	}
	if status == "SUCCEEDED" || status == "FAILED" {
		return nil
	}
	if current != req.Candidate.ID {
		return errors.New("APPLIED_GENERATION_CONFLICT")
	}
	if result.Status == "SUCCEEDED" {
		_, err = tx.Exec("INSERT INTO observations VALUES(?,?,?) ON CONFLICT(instance_id) DO UPDATE SET payload_json=excluded.payload_json,observed_at=excluded.observed_at", req.Candidate.InstanceID, jsonText(result), Now())
		if err == nil && req.RuntimePolicy.Policy != nil {
			_, err = tx.Exec("UPDATE instances SET draft_json=json_set(draft_json,'$.runtimePolicy',json(?)),draft_revision=draft_revision+1,version=version+1 WHERE id=?", jsonText(req.RuntimePolicy.Policy), req.Candidate.InstanceID)
		}
	}
	if err == nil {
		_, err = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if err == nil {
		err = auditTx(tx, "worker", req.Action+"_RESULT", req.Candidate.InstanceID, result.Status, req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}
