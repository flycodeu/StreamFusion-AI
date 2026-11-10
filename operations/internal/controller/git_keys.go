package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

func (h *HTTP) targetGitKey(ctx context.Context, t target, action string, k deploy.GitKeyRequest) (deploy.GitKeyEvidence, error) {
	var evidence deploy.GitKeyEvidence
	if err := h.gitTargetReady(ctx, t, true); err != nil {
		return evidence, err
	}
	traceID, _ := ctx.Value(traceKey{}).(string)
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: action, GitKey: &k, TraceID: traceID}, "", &evidence); err != nil {
		return evidence, err
	}
	if evidence.ID != k.ID || evidence.Account != k.Account || evidence.AccountUID == "" || evidence.AccountUID == "0" {
		return evidence, errors.New("GIT_RESULT_MISMATCH")
	}
	if action == "GIT_HOST_KEY_CHECK" {
		key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(evidence.HostPublicKey))
		if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 || ssh.FingerprintSHA256(key) != evidence.HostFingerprint {
			return evidence, errors.New("GIT_RESULT_MISMATCH")
		}
	} else {
		key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(evidence.PublicKey))
		if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 || ssh.FingerprintSHA256(key) != evidence.Fingerprint || !strings.HasPrefix(evidence.KeyPath, "/") || !strings.HasSuffix(evidence.KeyPath, "/.local/share/streamfusion-ops/git-keys/"+k.ID+"/key") {
			return evidence, errors.New("GIT_RESULT_MISMATCH")
		}
	}
	return evidence, nil
}

func (h *HTTP) prepareGitKey(w http.ResponseWriter, r *http.Request, s *session, hostID string) {
	var in struct {
		deploy.GitKeyRequest
		ExpectedHostVersion string `json:"expectedHostVersion"`
		Operation           string `json:"operation"`
	}
	if decode(w, r, &in) != nil || !deploy.ID.MatchString(in.ID) || in.Account == "" || len(in.Account) > 64 || in.Account == "root" || in.Operation != "GENERATE" && in.Operation != "HOST_KEY" {
		h.gitError(w, r, errors.New("GIT_KEY_INPUT_INVALID"))
		return
	}
	t, err := h.store.readTarget(hostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Version != in.ExpectedHostVersion || t.Maintenance {
		h.gitError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	action := "GIT_KEY_PREPARE"
	if in.Operation == "HOST_KEY" {
		action = "GIT_HOST_KEY_CHECK"
	}
	evidence, err := h.targetGitKey(ctx, t, action, in.GitKeyRequest)
	if err != nil {
		h.gitError(w, r, err)
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", hostID))
	if err != nil || current.Maintenance || pluginTargetSnapshot(current) != pluginTargetSnapshot(t) {
		h.gitError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	err = auditTx(tx, s.Username, action, hostID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, evidence)
}
