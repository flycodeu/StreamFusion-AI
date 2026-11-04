package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) submitGitPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Key       string `json:"idempotencyKey"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(w, r, &in) != nil || !in.Confirmed || len(in.Key) < 8 || len(in.Key) > 80 {
		h.gitError(w, r, errors.New("GIT_CONFIRMATION_REQUIRED"))
		return
	}
	hash := deploy.Digest([]string{id})
	if h.existingGitJob(w, r, h.store.DB, s.AdminID, in.Key, hash) {
		return
	}
	var raw, status, expiry string
	err := h.store.DB.QueryRow("SELECT snapshot_json,status,expires_at FROM git_repository_plans WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&raw, &status, &expiry)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	var p gitPlan
	until, e := time.Parse(time.RFC3339, expiry)
	if deploy.StrictJSON([]byte(raw), &p) != nil || e != nil || time.Now().After(until) || status != "DRAFT" || p.Change.Validate() != nil || p.ActorGeneration != s.Generation {
		h.gitError(w, r, errors.New("GIT_PLAN_STALE"))
		return
	}
	if err = validateGitPlan(h.store.DB, p); err != nil {
		h.gitError(w, r, err)
		return
	}
	t, err := h.store.readTarget(p.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	o, err := h.checkGitRepository(ctx, t, p.Change)
	if err != nil {
		h.gitError(w, r, err)
		return
	}
	if o.ConfigHash != p.Change.ExpectedConfigHash || o.AccountUID != p.Change.AccountUID || o.TerminalHash != p.Change.ExpectedTerminalHash {
		h.gitError(w, r, errors.New("GIT_PLAN_STALE"))
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
	if h.existingGitJob(w, r, tx, s.AdminID, in.Key, hash) {
		return
	}
	if err = validateGitPlan(tx, p); err != nil {
		h.gitError(w, r, err)
		return
	}
	var count int
	err = tx.QueryRow("SELECT count(*) FROM git_repository_plans WHERE id=? AND snapshot_json=? AND status='DRAFT' AND expires_at>?", id, raw, Now()).Scan(&count)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if count != 1 {
		h.gitError(w, r, errors.New("GIT_PLAN_STALE"))
		return
	}
	err = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 20 {
		h.gitError(w, r, errors.New("QUEUE_FULL"))
		return
	}
	job, now := uuid.NewString(), Now()
	c := p.Change
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.HostID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: "GIT_REPOSITORY_CHANGE", SoftwareOperation: "GIT_REPOSITORY_CHANGE", GitRepository: &c, DeadlineAt: time.Now().UTC().Add(3 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	if p.ExpectedBindingVersion == "" {
		_, err = tx.Exec("INSERT INTO git_repositories(id,host_id,account,account_uid,repo_path,profile_id,change_json,last_job_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)", c.BindingID, p.HostID, c.Account, c.AccountUID, c.RepoPath, c.ProfileID, jsonText(c), job, now, now)
	} else {
		_, err = tx.Exec("UPDATE git_repositories SET last_job_id=?,updated_at=? WHERE id=?", job, now, c.BindingID)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'GIT_REPOSITORY_CHANGE',?,?,?,'QUEUED','GIT_PREFLIGHT',?,?,?,?)", job, s.AdminID, p.HostID, in.Key, hash, jsonText(req), req.TraceID, now, now)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE git_repository_plans SET job_id=?,status='SUBMITTED' WHERE id=?", job, id)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+p.HostID, job)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "GIT_REPOSITORY_CHANGE_ACCEPTED", c.BindingID, "SUCCESS", req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job, "bindingId": c.BindingID})
}

func (h *HTTP) existingGitJob(w http.ResponseWriter, r *http.Request, q gitQuery, actor, key, hash string) bool {
	var id, oldHash, binding string
	err := q.QueryRow("SELECT j.id,j.request_hash,p.binding_id FROM jobs j JOIN git_repository_plans p ON p.job_id=j.id WHERE j.kind='GIT_REPOSITORY_CHANGE' AND j.actor_id=? AND j.idempotency_key=?", actor, key).Scan(&id, &oldHash, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		h.internal(w, r, err)
	} else if hash != oldHash {
		h.gitError(w, r, errors.New("IDEMPOTENCY_CONFLICT"))
	} else {
		h.success(w, r, map[string]string{"jobId": id, "bindingId": binding})
	}
	return true
}

func (h *HTTP) startGitJob(id string, req deploy.Request) bool {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	var raw, state string
	var generation int
	err = tx.QueryRow("SELECT p.snapshot_json,j.status,a.session_generation FROM jobs j JOIN git_repository_plans p ON p.job_id=j.id JOIN admins a ON a.id=j.actor_id WHERE j.id=? AND j.input_json=?", id, jsonText(req)).Scan(&raw, &state, &generation)
	var p gitPlan
	if err != nil || state != "QUEUED" || deploy.StrictJSON([]byte(raw), &p) != nil || deploy.Digest(p.Change) != deploy.Digest(req.GitRepository) {
		return false
	}
	code := ""
	t, e := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", req.TargetID))
	if generation != p.ActorGeneration {
		code = "GIT_ACTOR_CHANGED"
	} else if e != nil || t.Maintenance || pluginTargetSnapshot(t) != p.HostSnapshot {
		code = "HOST_CHANGED"
	}
	b, e := readGitRepository(tx, p.Change.BindingID)
	if e != nil || b.LastJobID != id || b.AccountUID != p.Change.AccountUID || b.RepoPath != p.Change.RepoPath {
		code = "GIT_BINDING_CHANGED"
	}
	var count int
	if tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=? AND job_id=?", "target:"+req.TargetID, id).Scan(&count) != nil || count != 1 {
		code = "GIT_LOCK_MISMATCH"
	}
	if p.Action == "BIND" {
		a, e := scanGitAccount(tx.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", p.Change.ProfileID))
		if e != nil || a.Version != p.Change.ProfileVersion {
			code = "GIT_PROFILE_CHANGED"
		}
		if tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND revision=? AND revoked_at IS NULL", p.Change.CredentialID, p.Change.CredentialRevision).Scan(&count) != nil || count != 1 {
			code = "GIT_CREDENTIAL_UNAVAILABLE"
		}
	}
	if code != "" {
		_, err = tx.Exec("UPDATE jobs SET status='FAILED',phase='NOT_STARTED',error_code=?,updated_at=? WHERE id=?", code, Now(), id)
		if err == nil {
			_, err = tx.Exec("UPDATE git_repositories SET error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=?", code, Now(), p.Change.BindingID, id)
		}
		if err == nil {
			_, err = tx.Exec("DELETE FROM operation_locks WHERE job_id=?", id)
		}
		if err == nil {
			err = auditTx(tx, "worker", "GIT_REPOSITORY_NOT_STARTED", p.Change.BindingID, code, req.TraceID)
		}
		if err == nil {
			_ = tx.Commit()
		}
		return false
	}
	_, err = tx.Exec("UPDATE jobs SET status='RUNNING',phase='GIT_CONFIGURING',updated_at=? WHERE id=? AND status='QUEUED'", Now(), id)
	return err == nil && tx.Commit() == nil
}

func (h *HTTP) processGitRepository(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.GitRepository == nil || req.GitRepository.Validate() != nil || req.Action != "GIT_REPOSITORY_CHANGE" {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	if !h.startGitJob(id, req) {
		return
	}
	t, err := h.store.readTarget(req.TargetID)
	if err != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var result deploy.Result
	if err = h.store.callTarget(runCtx, t, req, "", &result); err != nil || result.Status == "UNKNOWN" {
		h.jobUnknown(id, "GIT_RESULT_UNCONFIRMED")
		return
	}
	if err = h.commitGitResult(id, req, result); err != nil {
		h.jobUnknown(id, "GIT_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

func (h *HTTP) commitGitResult(id string, req deploy.Request, result deploy.Result) error {
	c := req.GitRepository
	if c == nil || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash || (result.Status != "SUCCEEDED" && result.Status != "FAILED") {
		return errors.New("GIT_RESULT_MISMATCH")
	}
	if result.Status == "SUCCEEDED" && (!gitObservationMatches(*c, result.GitRepository) || c.Action == "BIND" && result.GitRepository.Status != "BOUND" || c.Action == "UNBIND" && result.GitRepository.Status != "UNBOUND") {
		return errors.New("GIT_RESULT_MISMATCH")
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, raw string
	if err = tx.QueryRow("SELECT status,result_json FROM jobs WHERE id=? AND input_json=?", id, jsonText(req)).Scan(&status, &raw); err != nil {
		return err
	}
	if status == "SUCCEEDED" || status == "FAILED" {
		if raw != jsonText(result) {
			return errors.New("GIT_RESULT_MISMATCH")
		}
		return nil
	}
	b, err := readGitRepository(tx, c.BindingID)
	if err != nil {
		return err
	}
	if b.LastJobID != id {
		return errors.New("GIT_BINDING_CHANGED")
	}
	if result.Status == "SUCCEEDED" {
		state := "BOUND"
		if c.Action == "UNBIND" {
			state = "RELEASED"
		}
		_, err = tx.Exec("UPDATE git_repositories SET profile_id=?,change_json=?,status=?,observation_json=?,error_code='',version=version+1,updated_at=? WHERE id=?", c.ProfileID, jsonText(c), state, jsonText(result.GitRepository), Now(), c.BindingID)
	} else {
		_, err = tx.Exec("UPDATE git_repositories SET status=CASE WHEN status='PENDING' THEN 'FAILED' ELSE status END,error_code=?,version=version+1,updated_at=? WHERE id=?", result.ErrorCode, Now(), c.BindingID)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if err == nil {
		err = auditTx(tx, "worker", "GIT_REPOSITORY_CHANGE_RESULT", c.BindingID, result.Status, req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}
