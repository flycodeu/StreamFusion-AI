package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) configureGitAccount(w http.ResponseWriter, r *http.Request, s *session, id string, remove bool) {
	var in struct {
		ExpectedVersion     string `json:"expectedVersion"`
		ExpectedHostVersion string `json:"expectedHostVersion"`
	}
	if decode(w, r, &in) != nil {
		h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	a, err := scanGitAccount(h.store.DB.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", id))
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if a.Version != in.ExpectedVersion {
		h.gitError(w, r, errors.New("GIT_PROFILE_CHANGED"))
		return
	}
	t, err := h.store.readTarget(a.HostID)
	if err != nil || t.Maintenance || t.Version != in.ExpectedHostVersion {
		h.gitError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	locked, err := hostOperationLock(h.store.DB, a.HostID)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if locked.ID != "" {
		h.operationBlocked(w, r, locked)
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var cap executorCapabilities
	if err = h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &cap); err != nil {
		h.gitError(w, r, err)
		return
	}
	if cap.Owner.ControllerID != h.store.controllerID() || cap.Owner.TargetID != t.ID || cap.Owner.ProtocolVersion != 1 || cap.Owner.ExecutorVersion != deploy.Version {
		h.gitError(w, r, errors.New("EXECUTOR_IDENTITY_MISMATCH"))
		return
	}
	if cap.GitAccountVersion != 1 {
		h.gitError(w, r, errors.New("EXECUTOR_GIT_UNSUPPORTED"))
		return
	}
	username := a.Username
	if a.AuthMethod == "SSH" {
		username = a.SSHUser
	}
	g := deploy.GitAccountRequest{ID: a.ID, Version: a.Version, Account: a.SystemAccount, AuthMethod: a.AuthMethod, Host: a.SSHHost, Port: a.SSHPort, Username: username, KeyPath: a.KeyPath, KeyFingerprint: a.KeyFingerprint}
	g.CredentialID, g.CredentialRevision = a.CredentialID, a.CredentialRevision
	g.AccountUID = a.AccountUID
	g.AuthorName, g.AuthorEmail = a.AuthorName, a.AuthorEmail
	g.HostPublicKey = a.HostPublicKey
	if g.Validate() != nil {
		h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
		return
	}
	action := "GIT_ACCOUNT_CONFIGURE"
	if remove {
		action = "GIT_ACCOUNT_REMOVE"
	}
	req := deploy.Request{Action: action, GitAccount: &g, TraceID: r.Context().Value(traceKey{}).(string)}
	var evidence deploy.GitAccountEvidence
	if err = h.store.callTarget(ctx, t, req, "", &evidence); err != nil {
		h.gitError(w, r, err)
		return
	}
	expected := "CONFIGURED"
	if remove {
		expected = "REMOVED"
	}
	uid, uidErr := strconv.ParseUint(evidence.AccountUID, 10, 32)
	_, timeErr := time.Parse(time.RFC3339Nano, evidence.ObservedAt)
	if evidence.ID != a.ID || evidence.Version != a.Version || evidence.Account != a.SystemAccount || evidence.Status != expected || uidErr != nil || uid == 0 || timeErr != nil || a.AccountUID != "" && evidence.AccountUID != a.AccountUID {
		h.gitError(w, r, errors.New("GIT_RESULT_MISMATCH"))
		return
	}
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var revision string
	if tx.QueryRow("SELECT CAST(version AS TEXT) FROM git_accounts WHERE id=? AND deleted_at=''", id).Scan(&revision) != nil || revision != a.Version {
		h.gitError(w, r, errors.New("GIT_PROFILE_CHANGED"))
		return
	}
	configured := a.Version
	if remove {
		configured = "0"
	}
	_, err = tx.Exec("UPDATE git_accounts SET configured_version=?,account_uid=? WHERE id=?", configured, evidence.AccountUID, id)
	if err == nil {
		err = auditTx(tx, s.Username, action, id, "SUCCESS", req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, evidence)
}
