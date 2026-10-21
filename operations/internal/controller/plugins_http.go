package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/plugin"
)

func (h *HTTP) listPlugins(w http.ResponseWriter, r *http.Request) {
	out := []plugin.Definition{}
	for _, d := range plugin.All() {
		if kind := r.URL.Query().Get("kind"); kind == "" || d.Kind == kind {
			out = append(out, d)
		}
	}
	h.success(w, r, out)
}

func (h *HTTP) listPluginVersions(w http.ResponseWriter, r *http.Request, key string) {
	out := []plugin.Definition{}
	for _, d := range plugin.All() {
		if d.Key == key {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		h.fail(w, r, 404, "PLUGIN_NOT_FOUND", "插件不在内置目录中，请刷新目录")
		return
	}
	h.success(w, r, out)
}

func (h *HTTP) listPluginInstallations(w http.ResponseWriter, r *http.Request) {
	targetID, status := r.URL.Query().Get("targetId"), r.URL.Query().Get("status")
	rows, e := h.store.DB.Query("SELECT "+pluginColumns+" FROM plugin_installations WHERE (?='' OR target_id=?) AND (?='' OR status=?) ORDER BY created_at DESC LIMIT 200", targetID, targetID, status, status)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	out := []pluginInstallation{}
	for rows.Next() {
		i, e := scanPlugin(rows)
		if e != nil {
			rows.Close()
			h.internal(w, r, e)
			return
		}
		out = append(out, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		h.internal(w, r, e)
		return
	}
	for n := range out {
		out[n].ReferenceCount, e = pluginRemovalReferences(h.store.DB, out[n])
		if e != nil {
			h.internal(w, r, e)
			return
		}
	}
	h.success(w, r, out)
}

func (h *HTTP) getPluginInstallation(w http.ResponseWriter, r *http.Request, id string) {
	i, e := h.store.readPlugin(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	h.success(w, r, i)
}

func (h *HTTP) pluginTargetReady(w http.ResponseWriter, r *http.Request, t target) (string, bool) {
	if t.Maintenance {
		h.fail(w, r, 409, "HOST_MAINTENANCE", "主机维护中，请恢复后再提交变更")
		return "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out json.RawMessage
	e := h.store.callTarget(ctx, t, deploy.Request{Action: "TOOL_PROBE"}, "", &out)
	var fact struct {
		Platform   string `json:"platform"`
		Docker     string `json:"dockerVersion"`
		Compose    string `json:"composeVersion"`
		Pending    string `json:"pendingJobId"`
		DockerRoot string `json:"dockerRootDir"`
		Catalog    string `json:"pluginCatalogHash"`
	}
	if e != nil {
		code, message := h.executorFailure(ctx, t, e)
		h.fail(w, r, 409, code, message)
		return "", false
	}
	if json.Unmarshal(out, &fact) != nil || fact.Docker == "" {
		h.fail(w, r, 409, "DOCKER_ENGINE_UNAVAILABLE", "工具安装需要可用的Docker Engine，无需Compose；请检查Engine和目标执行器版本")
		return "", false
	}
	if fact.Platform != "linux/amd64" {
		h.fail(w, r, 409, "PLUGIN_PLATFORM_UNSUPPORTED", "当前插件仅支持 Linux x86_64")
		return "", false
	}
	if fact.Catalog != deploy.Digest(plugin.All()) {
		h.fail(w, r, 409, "PLUGIN_EXECUTOR_UPGRADE_REQUIRED", "目标执行器尚未支持当前插件目录，请升级执行器后重新检查")
		return "", false
	}
	if fact.Pending != "" {
		h.fail(w, r, 409, "TARGET_PENDING", "目标有未收尾作业，请先核对原作业")
		return "", false
	}
	fact.Pending = ""
	// Compose is unrelated to preparing a Java/Maven tool image. Installing it
	// between preflight and submit must not invalidate this tool plan.
	fact.Compose = ""
	return deploy.Digest(fact), true
}

func (h *HTTP) createPluginPlan(w http.ResponseWriter, r *http.Request, s *session, targetID string) {
	var in struct {
		Key      string `json:"pluginKey"`
		Version  string `json:"version"`
		Revision string `json:"recipeRevision"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	d, e := plugin.Find(in.Key, in.Version)
	if e != nil || in.Revision != d.RecipeRevision {
		h.fail(w, r, 400, "PLUGIN_NOT_FOUND", "请选择内置插件版本与配方")
		return
	}
	t, e := h.store.readTarget(targetID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	capabilities, ready := h.pluginTargetReady(w, r, t)
	if !ready {
		return
	}
	p := pluginPlan{ID: uuid.NewString(), TargetID: targetID, TargetSnapshot: pluginTargetSnapshot(t), Capabilities: capabilities, Definition: d, ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO plugin_install_plans VALUES(?,?,?,?,?,?,?,?, 'DRAFT',?,?)", p.ID, p.TargetID, d.Key, d.Version, d.Kind, d.Digest, d.RecipeRevision, jsonText(p), p.ExpiresAt, Now())
	if e == nil {
		e = auditTx(tx, s.Username, "PLUGIN_PLAN_CREATED", p.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, p)
}

func decodePluginKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Key string `json:"idempotencyKey"`
	}
	if e := decode(w, r, &in); e != nil || len(strings.TrimSpace(in.Key)) < 8 || len(in.Key) > 80 {
		return "", false
	}
	return in.Key, true
}

func (h *HTTP) submitPluginPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	key, ok := decodePluginKey(w, r)
	if !ok {
		h.fail(w, r, 400, "IDEMPOTENCY_REQUIRED", "提交标识须为8–80字节")
		return
	}
	hash := hashToken(id)
	var old, oldHash, oldRaw string
	e := h.store.DB.QueryRow("SELECT id,request_hash,input_json FROM jobs WHERE actor_id=? AND kind='PLUGIN_INSTALL' AND idempotency_key=?", s.AdminID, key).Scan(&old, &oldHash, &oldRaw)
	if e == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于另一计划")
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
	var raw, state string
	e = h.store.DB.QueryRow("SELECT snapshot_json,status FROM plugin_install_plans WHERE id=?", id).Scan(&raw, &state)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	var p pluginPlan
	if deploy.StrictJSON([]byte(raw), &p) != nil || plugin.Validate(p.Definition) != nil {
		h.fail(w, r, 409, "PLUGIN_PLAN_STALE", "计划清单已变化，请重新预检")
		return
	}
	expires, e := time.Parse(time.RFC3339, p.ExpiresAt)
	if e != nil || time.Now().After(expires) || state != "DRAFT" {
		h.fail(w, r, 409, "PLUGIN_PLAN_STALE", "计划已提交或过期，请重新预检")
		return
	}
	t, e := h.store.readTarget(p.TargetID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if pluginTargetSnapshot(t) != p.TargetSnapshot {
		h.fail(w, r, 409, "PLUGIN_PLAN_STALE", "目标配置已变化，请重新预检")
		return
	}
	capabilities, ready := h.pluginTargetReady(w, r, t)
	if !ready {
		return
	}
	if capabilities != p.Capabilities {
		h.fail(w, r, 409, "PLUGIN_PLAN_STALE", "目标运行能力已变化，请重新预检")
		return
	}
	h.enqueuePlugin(w, r, s, p, key, hash, "PLUGIN_INSTALL", "")
}

func (h *HTTP) removePluginInstallation(w http.ResponseWriter, r *http.Request, s *session, id string) {
	key, ok := decodePluginKey(w, r)
	if !ok {
		h.fail(w, r, 400, "IDEMPOTENCY_REQUIRED", "提交标识须为8–80字节")
		return
	}
	// Repeated removes must still return the original Job after the record is gone.
	var old, hash string
	e := h.store.DB.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind='PLUGIN_REMOVE' AND idempotency_key=?", s.AdminID, key).Scan(&old, &hash)
	if e == nil {
		if hash != hashToken(id) {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于另一记录")
			return
		}
		h.success(w, r, map[string]string{"jobId": old, "pluginInstallationId": id})
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	i, e := h.store.readPlugin(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	d, e := installationDefinition(i)
	if e != nil {
		h.fail(w, r, 409, "PLUGIN_REMOVE_BLOCKED", "当前清单不能验证该记录，请检查发行版本")
		return
	}
	if i.ReferenceCount > 0 {
		h.fail(w, r, 409, "PLUGIN_REFERENCE_EXISTS", "环境仍被引用，不能卸载")
		return
	}
	if i.Status != "READY" && i.Status != "FAILED" {
		h.fail(w, r, 409, "PLUGIN_REMOVE_BLOCKED", "环境有未决作业，请先核对")
		return
	}
	t, e := h.store.readTarget(i.TargetID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	_, ready := h.pluginTargetReady(w, r, t)
	if !ready {
		return
	}
	p := pluginPlan{TargetID: i.TargetID, TargetSnapshot: pluginTargetSnapshot(t), Definition: d}
	h.enqueuePlugin(w, r, s, p, key, hashToken(id), "PLUGIN_REMOVE", id)
}

func (h *HTTP) verifyPluginInstallation(w http.ResponseWriter, r *http.Request, s *session, id string) {
	i, e := h.store.readPlugin(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	var jobStatus string
	var pending bool
	if e = h.store.DB.QueryRow("SELECT status,target_ack_pending FROM jobs WHERE id=?", i.LastJobID).Scan(&jobStatus, &pending); e != nil {
		h.internal(w, r, e)
		return
	}
	if i.Status == "INSTALLING" || i.Status == "REMOVING" || (jobStatus != "SUCCEEDED" && jobStatus != "FAILED") || pending {
		h.fail(w, r, 409, "PLUGIN_JOB_UNKNOWN", "请先核对并收尾原作业，再重新校验")
		return
	}
	d, e := installationDefinition(i)
	if e != nil {
		h.fail(w, r, 409, "PLUGIN_MANIFEST_UNTRUSTED", "发行清单与安装记录不一致")
		return
	}
	t, e := h.store.readTarget(i.TargetID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	var actual deploy.Result
	e = h.store.callTarget(ctx, t, deploy.Request{Action: "PLUGIN_VERIFY", Plugin: pluginRequest(d), PluginInstallationID: id}, "", &actual)
	status, code := "READY", ""
	if e != nil {
		status, code = "UNKNOWN", "PLUGIN_VERIFY_UNCONFIRMED"
	} else if actual.Status != "SUCCEEDED" || actual.ImageID != i.ImageID || actual.PluginImageDigest != d.Digest || actual.Platform != d.Platform || actual.Health != "PLUGIN_READY" {
		status, code = "FAILED", "PLUGIN_ACTUAL_STATE_DRIFT"
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE plugin_installations SET status=?,error_code=?,observed_at=?,observed_version=?,version=version+1,updated_at=? WHERE id=? AND version=? AND NOT EXISTS(SELECT 1 FROM operation_locks WHERE resource_key=?)", status, code, Now(), actual.ObservedVersion, Now(), id, i.Version, "target:"+i.TargetID)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		h.fail(w, r, 409, "VERSION_CONFLICT", "校验期间记录已变化，请刷新")
		return
	}
	if e = auditTx(tx, s.Username, "PLUGIN_VERIFIED", id, status, r.Context().Value(traceKey{}).(string)); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, map[string]string{"id": id, "status": status, "errorCode": code})
}

func (h *HTTP) pluginJobLogs(w http.ResponseWriter, r *http.Request, id string) {
	var raw string
	if e := h.store.DB.QueryRow("SELECT input_json FROM jobs WHERE id=? AND (kind IN ('PLUGIN_INSTALL','PLUGIN_REMOVE') OR kind LIKE 'SOFTWARE_%' OR kind='FIREWALL_CHANGE')", id).Scan(&raw); e != nil {
		h.opsError(w, r, e)
		return
	}
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil {
		h.internal(w, r, errors.New("INPUT_DAMAGED"))
		return
	}
	if req.Firewall != nil || req.Software != nil && req.Reconfiguration == nil {
		var phase, status, code, updated string
		if e := h.store.DB.QueryRow("SELECT phase,status,error_code,updated_at FROM jobs WHERE id=?", id).Scan(&phase, &status, &code, &updated); e != nil {
			h.opsError(w, r, e)
			return
		}
		h.success(w, r, map[string]any{"text": "作业执行回执\njobId=" + id + "\nphase=" + phase + "\nstatus=" + status + "\nerrorCode=" + code + "\nupdatedAt=" + updated, "limited": false, "source": "JOB_RECEIPT"})
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	req.Action = "PLUGIN_LOGS"
	if req.Installation != nil || req.Reconfiguration != nil {
		req.Action = "SOFTWARE_INSTALL_LOGS"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var logs struct {
		Text    string `json:"text"`
		Limited bool   `json:"limited"`
		Source  string `json:"source,omitempty"`
	}
	if e = h.store.callTarget(ctx, t, req, "", &logs); e != nil {
		h.fail(w, r, 409, "PLUGIN_LOG_UNAVAILABLE", "目标日志暂不可用，请核对目标连接和原作业")
		return
	}
	h.success(w, r, logs)
}
