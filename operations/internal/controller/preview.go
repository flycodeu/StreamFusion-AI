package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) previewRegistration(w http.ResponseWriter, r *http.Request, id string) {
	instance, err := h.store.readInstance(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if instance.Current == "" {
		h.fail(w, r, 409, "NOT_DEPLOYED", "请先部署预览节点")
		return
	}
	release, err := h.store.readRelease(instance.Current)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if release.Config.Preview == nil {
		h.fail(w, r, 409, "PREVIEW_INSTANCE_REQUIRED", "该实例不是预览节点")
		return
	}
	target, err := h.store.readTarget(instance.TargetID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var result map[string]any
	if err = h.store.callTarget(ctx, target, deploy.Request{Action: "PREVIEW_REGISTRATION", Candidate: &release}, "", &result); err != nil {
		h.fail(w, r, 409, "PREVIEW_REGISTRATION_UNAVAILABLE", "请核对目标执行器与节点实际状态")
		return
	}
	h.success(w, r, result)
}
