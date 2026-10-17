package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type platformZLMInstance struct {
	InstanceID    string `json:"instanceId"`
	Name          string `json:"name"`
	TargetName    string `json:"targetName"`
	TargetAddress string `json:"targetAddress"`
	InstallRoot   string `json:"installRoot"`
	ReleaseID     string `json:"releaseId"`
	APIURL        string `json:"apiUrl"`
	MediaAddress  string `json:"mediaAddress"`
	APIPort       int    `json:"apiPort"`
	RTCPort       int    `json:"rtcPort"`
	MaxSources    int    `json:"maxSources"`
	MaxViewers    int    `json:"maxViewers"`
	MaxTranscodes int    `json:"maxTranscodes"`
}

func zlmCatalogEntry(i instance, t target, r deploy.Release) platformZLMInstance {
	z := r.Config.ZLM
	return platformZLMInstance{
		InstanceID: i.ID, Name: i.Name, TargetName: t.Name,
		TargetAddress: t.Address, InstallRoot: t.InstallRoot, ReleaseID: r.ID,
		APIURL:       fmt.Sprintf("http://%s:%d", z.MediaAddress, z.APIPort),
		MediaAddress: z.MediaAddress, APIPort: z.APIPort, RTCPort: z.RTCPort,
		MaxSources: z.MaxSources, MaxViewers: z.MaxViewers, MaxTranscodes: 0,
	}
}

func (h *HTTP) platformZLMInstances(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.Query(`SELECT i.id FROM instances i JOIN releases r ON r.id=i.current_release_id AND r.instance_id=i.id WHERE json_type(r.snapshot_json,'$.config.zlm')='object' ORDER BY i.created_at DESC LIMIT 201`)
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
		h.fail(w, r, 409, "CATALOG_LIMIT", "实例目录超出上限")
		return
	}
	items := make([]platformZLMInstance, 0, len(ids))
	for _, id := range ids {
		i, e := h.store.readInstance(id)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		release, e := h.store.readRelease(i.Current)
		if e != nil || release.InstanceID != id || release.Config.ZLM == nil || release.Config.Validate() != nil {
			h.fail(w, r, 409, "RELEASE_IDENTITY_MISMATCH", "已发布实例状态不一致")
			return
		}
		target, e := h.store.readTarget(i.TargetID)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		items = append(items, zlmCatalogEntry(i, target, release))
	}
	h.success(w, r, map[string]any{"items": items})
}

func (h *HTTP) platformZLMHandoff(w http.ResponseWriter, r *http.Request, id string) {
	var input struct {
		ReleaseID string `json:"releaseId"`
	}
	if err := decode(w, r, &input); err != nil || !deploy.ID.MatchString(input.ReleaseID) {
		h.fail(w, r, 400, "INVALID_INPUT", "请提供有效的发布版本")
		return
	}
	i, err := h.store.readInstance(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if i.Current != input.ReleaseID {
		h.fail(w, r, 409, "RELEASE_CHANGED", "发布版本已变化，请刷新实例目录")
		return
	}
	release, err := h.store.readRelease(input.ReleaseID)
	if err != nil || release.InstanceID != id || release.Config.ZLM == nil || release.Config.Validate() != nil {
		h.fail(w, r, 409, "ZLM_INSTANCE_REQUIRED", "当前发布版本不是独立 ZLM")
		return
	}
	target, err := h.store.readTarget(i.TargetID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var actual deploy.Result
	if err = h.store.callTarget(ctx, target, deploy.Request{Action: "INSPECT", Candidate: &release, TraceID: r.Context().Value(traceKey{}).(string)}, "", &actual); err != nil || actual.Health != "APPLICATION_READY" || actual.ReleaseID != release.ID || actual.ConfigHash != deploy.Digest(release.Config) {
		h.auditPlatformHandoff(r, id, "FAILED")
		h.fail(w, r, 409, "ZLM_HANDOFF_UNAVAILABLE", "目标实例未就绪，请核对 Ops 目标和实例状态")
		return
	}
	z := release.Config.ZLM
	secret, err := h.store.secret(z.SecretCredentialID, z.SecretRevision, "TOKEN")
	if err != nil || !deploy.Hex.MatchString(string(secret)) {
		h.auditPlatformHandoff(r, id, "FAILED")
		h.fail(w, r, 409, "ZLM_SECRET_UNAVAILABLE", "目标实例的 API 凭据修订不可用")
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
	var current string
	if err = tx.QueryRow("SELECT coalesce(current_release_id,'') FROM instances WHERE id=?", id).Scan(&current); errors.Is(err, sql.ErrNoRows) || err == nil && current != input.ReleaseID {
		h.fail(w, r, 409, "RELEASE_CHANGED", "发布版本已变化，请刷新实例目录")
		return
	} else if err != nil {
		h.internal(w, r, err)
		return
	}
	if err = auditTx(tx, "platform-service", "ZLM_HANDOFF", id+":"+input.ReleaseID, "SUCCESS", r.Context().Value(traceKey{}).(string)); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	connection := map[string]any{
		"apiUrl": fmt.Sprintf("http://%s:%d", z.MediaAddress, z.APIPort),
		"mediaAddress": z.MediaAddress, "apiPort": z.APIPort, "rtcPort": z.RTCPort,
		"maxSources": z.MaxSources, "maxViewers": z.MaxViewers, "maxTranscodes": 0,
	}
	h.success(w, r, map[string]any{"instanceId": id, "releaseId": input.ReleaseID, "connection": connection, "apiSecret": string(secret)})
}
