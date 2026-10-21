package controller

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type hostSoftwareCandidate struct {
	deploy.SoftwareCandidate
	RegisteredID string `json:"registeredId,omitempty"`
}

type hostSoftwareDiscovery struct {
	*deploy.SoftwareDiscovery
	HostID      string                  `json:"hostId"`
	HostVersion string                  `json:"hostVersion"`
	Candidates  []hostSoftwareCandidate `json:"candidates"`
}

// Discovery reads only lifecycle metadata and selected non-secret configuration
// values. It neither registers software nor acquires mutation authority.
func (h *HTTP) discoverHostSoftware(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		ExpectedVersion string `json:"expectedVersion"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	if version, e := strconv.ParseUint(in.ExpectedVersion, 10, 64); e != nil || version == 0 {
		h.fail(w, r, 400, "VERSION_REQUIRED", "请携带当前主机版本")
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	t, e := h.store.readTarget(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if t.Version != in.ExpectedVersion {
		h.fail(w, r, 409, "HOST_CHANGED", "主机配置已变化，请刷新后重新识别")
		return
	}
	if t.Kind != "SSH" && runtime.GOOS != "linux" {
		h.fail(w, r, 409, "LINUX_REQUIRED", "软件识别仅支持Linux主机，请连接Linux服务器后重试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var report *deploy.SoftwareDiscovery
	e = h.withSoftwareRunner(ctx, t, func(run deploy.SoftwareCommandRunner) error {
		var err error
		report, err = deploy.DiscoverSoftware(ctx, run)
		return err
	})
	if e != nil {
		code, message := hostSSHError(e)
		h.fail(w, r, 409, code, message)
		return
	}
	if report == nil {
		h.fail(w, r, 409, "SOFTWARE_DISCOVERY_UNAVAILABLE", "软件识别结果未确认，请检查主机连接后重试")
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
	var hostVersion string
	if e = tx.QueryRow("SELECT CAST(version AS TEXT) FROM targets WHERE id=?", id).Scan(&hostVersion); e != nil {
		h.opsError(w, r, e)
		return
	}
	if hostVersion != t.Version {
		h.fail(w, r, 409, "HOST_CHANGED", "识别期间主机配置已变化，请刷新后重新识别")
		return
	}
	rows, e := tx.Query("SELECT id,runtime_kind,locator FROM software_instances WHERE host_id=? AND management_mode<>'RELEASED'", id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	registered := map[string]string{}
	for rows.Next() {
		var registeredID, kind, locator string
		if e = rows.Scan(&registeredID, &kind, &locator); e != nil {
			break
		}
		registered[kind+":"+locator] = registeredID
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		h.internal(w, r, e)
		return
	}
	out := hostSoftwareDiscovery{SoftwareDiscovery: report, HostID: id, HostVersion: hostVersion, Candidates: []hostSoftwareCandidate{}}
	for _, candidate := range report.Candidates {
		out.Candidates = append(out.Candidates, hostSoftwareCandidate{SoftwareCandidate: candidate, RegisteredID: registered[candidate.RuntimeKind+":"+candidate.Locator]})
	}
	unavailable := 0
	for _, check := range report.Checks {
		if check.Status == "UNAVAILABLE" {
			unavailable++
		}
	}
	result := fmt.Sprintf("SUCCESS candidates=%d unavailable=%d limited=%t", len(out.Candidates), unavailable, report.Limited)
	if e = auditTx(tx, s.Username, "HOST_SOFTWARE_DISCOVERED", id, result, r.Context().Value(traceKey{}).(string)); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, out)
}
