package controller

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type platformPreviewInstance struct {
	ControllerID  string `json:"controllerId"`
	TargetID      string `json:"targetId"`
	InstanceID    string `json:"instanceId"`
	Name          string `json:"name"`
	TargetName    string `json:"targetName"`
	TargetAddress string `json:"targetAddress"`
	InstallRoot   string `json:"installRoot"`
	ReleaseID     string `json:"releaseId"`
	NodeID        string `json:"nodeId"`
	ControlURI    string `json:"controlUri"`
	MediaAddress  string `json:"mediaAddress"`
	MaxSources    int    `json:"maxSources"`
	MaxViewers    int    `json:"maxViewers"`
	MaxTranscodes int    `json:"maxTranscodes"`
}

func (h *HTTP) platformRoute(w http.ResponseWriter, r *http.Request, path string) {
	if len(h.platformToken) == 0 {
		h.fail(w, r, 404, "NOT_FOUND", "接口不存在")
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(provided), h.platformToken) != 1 {
		h.fail(w, r, 401, "SERVICE_AUTH_REQUIRED", "服务身份无效")
		return
	}
	if r.Method == http.MethodGet && path == "/platform/preview-instances" {
		h.platformPreviewInstances(w, r)
		return
	}
	if r.Method == http.MethodGet && path == "/platform/zlm-instances" {
		h.platformZLMInstances(w, r)
		return
	}
	const zlmPrefix = "/platform/zlm-instances/"
	if r.Method == http.MethodPost && strings.HasPrefix(path, zlmPrefix) && strings.HasSuffix(path, "/handoff") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, zlmPrefix), "/handoff")
		if deploy.ID.MatchString(id) {
			h.platformZLMHandoff(w, r, id)
			return
		}
	}
	const prefix = "/platform/preview-instances/"
	if r.Method == http.MethodPost && strings.HasPrefix(path, prefix) && strings.HasSuffix(path, "/handoff") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/handoff")
		if deploy.ID.MatchString(id) {
			h.platformPreviewHandoff(w, r, id)
			return
		}
	}
	h.fail(w, r, 404, "NOT_FOUND", "接口不存在")
}

func (h *HTTP) platformPreviewInstances(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.Query(`SELECT i.id FROM instances i JOIN releases r ON r.id=i.current_release_id AND r.instance_id=i.id WHERE json_type(r.snapshot_json,'$.config.preview')='object' ORDER BY i.created_at DESC LIMIT 201`)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if len(ids) > 200 {
		h.fail(w, r, 409, "CATALOG_LIMIT", "实例目录超出上限，请缩小 Ops 实例数量")
		return
	}
	items := make([]platformPreviewInstance, 0)
	for _, id := range ids {
		instance, e := h.store.readInstance(id)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		if instance.Current == "" {
			continue
		}
		release, e := h.store.readRelease(instance.Current)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		if release.InstanceID != id || release.ID != instance.Current {
			h.fail(w, r, 409, "RELEASE_IDENTITY_MISMATCH", "已发布实例状态不一致")
			return
		}
		if release.Config.Preview == nil {
			continue
		}
		target, e := h.store.readTarget(instance.TargetID)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		p := release.Config.Preview
		items = append(items, platformPreviewInstance{ControllerID: h.store.controllerID(), TargetID: target.ID, InstanceID: instance.ID, Name: instance.Name, TargetName: target.Name, TargetAddress: target.Address, InstallRoot: target.InstallRoot, ReleaseID: release.ID, NodeID: p.NodeID, ControlURI: "https://" + p.ControlHost + ":9443", MediaAddress: p.MediaAddress, MaxSources: p.MaxSources, MaxViewers: p.MaxViewers, MaxTranscodes: p.MaxTranscodes})
	}
	h.success(w, r, map[string]any{"items": items})
}

func (h *HTTP) platformPreviewHandoff(w http.ResponseWriter, r *http.Request, id string) {
	var input struct {
		ReleaseID string `json:"releaseId"`
	}
	if err := decode(w, r, &input); err != nil || !deploy.ID.MatchString(input.ReleaseID) {
		h.fail(w, r, 400, "INVALID_INPUT", "请提供有效的发布版本")
		return
	}
	instance, err := h.store.readInstance(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if instance.Current != input.ReleaseID {
		h.fail(w, r, 409, "RELEASE_CHANGED", "发布版本已变化，请刷新实例目录")
		return
	}
	release, err := h.store.readRelease(input.ReleaseID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if release.InstanceID != id || release.ID != input.ReleaseID || release.Config.Preview == nil {
		h.fail(w, r, 409, "PREVIEW_INSTANCE_REQUIRED", "当前发布版本不是预览节点")
		return
	}
	target, err := h.store.readTarget(instance.TargetID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if err = platformHandoffReady(h.store.DB, id, target.ID, input.ReleaseID); err != nil {
		h.fail(w, r, 409, err.Error(), "实例或目标仍有未决作业、待确认结果或版本变化，请先核对原作业")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var material deploy.PreviewHandoff
	request := deploy.Request{Action: "PREVIEW_HANDOFF", Candidate: &release, TraceID: r.Context().Value(traceKey{}).(string)}
	if err = h.store.callTarget(ctx, target, request, "", &material); err != nil {
		h.auditPlatformHandoff(r, id, "FAILED")
		h.fail(w, r, 409, "PREVIEW_HANDOFF_UNAVAILABLE", "目标执行器或预览节点不可用，请核对 Ops 作业与目标状态")
		return
	}
	if material.Connection.NodeID != release.Config.Preview.NodeID || material.Connection.ControlURI != "https://"+release.Config.Preview.ControlHost+":9443" || material.Connection.MediaAddress != release.Config.Preview.MediaAddress || !deploy.Hex.MatchString(material.Connection.CertificateSHA256) || !deploy.Hex.MatchString(material.MachineToken) {
		h.auditPlatformHandoff(r, id, "FAILED")
		h.fail(w, r, 409, "PREVIEW_HANDOFF_INVALID", "目标执行器返回的登记材料与发布版本不一致")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if err = platformHandoffReady(tx, id, target.ID, input.ReleaseID); err != nil {
		h.fail(w, r, 409, err.Error(), "交接期间版本或作业状态已变化，请刷新后核对")
		return
	}
	if err = auditTx(tx, "platform-service", "PREVIEW_HANDOFF", id+":"+input.ReleaseID, "SUCCESS", r.Context().Value(traceKey{}).(string)); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	connection := struct {
		deploy.PreviewConnection
		ControllerID string `json:"controllerId"`
		TargetID     string `json:"targetId"`
	}{material.Connection, h.store.controllerID(), target.ID}
	h.success(w, r, map[string]any{"controllerId": connection.ControllerID, "targetId": target.ID, "instanceId": id, "releaseId": input.ReleaseID, "connection": connection, "machineToken": material.MachineToken})
}

// Rechecked before target I/O and in the final short transaction. This fences
// local changes; it does not promise atomicity with a later independent Ops job.
func platformHandoffReady(q interface{ QueryRow(string, ...any) *sql.Row }, instanceID, targetID, releaseID string) error {
	var current, actualTarget string
	if err := q.QueryRow("SELECT coalesce(current_release_id,''),target_id FROM instances WHERE id=?", instanceID).Scan(&current, &actualTarget); err != nil {
		return errors.New("RELEASE_CHANGED")
	}
	if current != releaseID || actualTarget != targetID {
		return errors.New("RELEASE_CHANGED")
	}
	var pending int
	if err := q.QueryRow("SELECT count(*) FROM jobs WHERE (target_id=? OR instance_id=?) AND (status IN ('QUEUED','RUNNING','UNKNOWN') OR target_ack_pending=1)", targetID, instanceID).Scan(&pending); err != nil {
		return errors.New("HANDOFF_STATE_UNAVAILABLE")
	}
	if pending != 0 {
		return errors.New("HANDOFF_JOB_PENDING")
	}
	if err := q.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key IN (?,?)", "target:"+targetID, "instance:"+instanceID).Scan(&pending); err != nil {
		return errors.New("HANDOFF_STATE_UNAVAILABLE")
	}
	if pending != 0 {
		return errors.New("HANDOFF_TARGET_LOCKED")
	}
	return nil
}

func (h *HTTP) auditPlatformHandoff(r *http.Request, id, result string) {
	_, _ = h.store.DB.Exec("INSERT INTO audit VALUES(?,?,?,?,?,?,?)", uuid.NewString(), "platform-service", "PREVIEW_HANDOFF", id, result, r.Context().Value(traceKey{}).(string), Now())
}
