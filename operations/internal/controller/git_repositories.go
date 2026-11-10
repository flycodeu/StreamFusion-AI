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

type gitRepository struct {
	ID                string                           `json:"id"`
	HostID            string                           `json:"hostId"`
	Account           string                           `json:"account"`
	AccountUID        string                           `json:"accountUid"`
	RepoPath          string                           `json:"repoPath"`
	ProfileID         string                           `json:"profileId"`
	ProfileName       string                           `json:"profileName"`
	Change            deploy.GitRepositoryRequest      `json:"-"`
	Status            string                           `json:"status"`
	Version           string                           `json:"version"`
	Observation       *deploy.GitRepositoryObservation `json:"observation,omitempty"`
	ErrorCode         string                           `json:"errorCode"`
	LastJobID         string                           `json:"lastJobId"`
	LastJobStatus     string                           `json:"lastJobStatus"`
	LastJobAckPending bool                             `json:"lastJobAckPending"`
	CreatedAt         string                           `json:"createdAt"`
	UpdatedAt         string                           `json:"updatedAt"`
}

const gitRepositoryColumns = "b.id,b.host_id,b.account,b.account_uid,b.repo_path,b.profile_id,a.name,b.change_json,b.status,CAST(b.version AS TEXT),b.observation_json,b.error_code,b.last_job_id,coalesce((SELECT status FROM jobs WHERE id=b.last_job_id),''),coalesce((SELECT target_ack_pending FROM jobs WHERE id=b.last_job_id),0),b.created_at,b.updated_at"

func scanGitRepository(row interface{ Scan(...any) error }) (gitRepository, error) {
	var b gitRepository
	var change, observation string
	err := row.Scan(&b.ID, &b.HostID, &b.Account, &b.AccountUID, &b.RepoPath, &b.ProfileID, &b.ProfileName, &change, &b.Status, &b.Version, &observation, &b.ErrorCode, &b.LastJobID, &b.LastJobStatus, &b.LastJobAckPending, &b.CreatedAt, &b.UpdatedAt)
	if err == nil {
		err = deploy.StrictJSON([]byte(change), &b.Change)
	}
	if err == nil && observation != "{}" {
		err = json.Unmarshal([]byte(observation), &b.Observation)
	}
	return b, err
}
func readGitRepository(q gitQuery, id string) (gitRepository, error) {
	return scanGitRepository(q.QueryRow("SELECT "+gitRepositoryColumns+" FROM git_repositories b JOIN git_accounts a ON a.id=b.profile_id WHERE b.id=?", id))
}
func (h *HTTP) listGitRepositories(w http.ResponseWriter, r *http.Request, hostID string) {
	rows, err := h.store.DB.Query("SELECT "+gitRepositoryColumns+" FROM git_repositories b JOIN git_accounts a ON a.id=b.profile_id WHERE b.host_id=? ORDER BY b.created_at DESC", hostID)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer rows.Close()
	out := []gitRepository{}
	for rows.Next() {
		b, e := scanGitRepository(rows)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		out = append(out, b)
	}
	if err = rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, out)
}

func (h *HTTP) gitTargetReady(ctx context.Context, t target, targetKeys ...bool) error {
	var cap executorCapabilities
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &cap); err != nil {
		return err
	}
	if cap.Owner.ControllerID != h.store.controllerID() || cap.Owner.TargetID != t.ID || cap.Owner.ProtocolVersion != 1 || cap.Owner.ExecutorVersion != deploy.Version {
		return errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if cap.GitRepositoryVersion < 1 || cap.GitRepositoryVersion > deploy.GitRepositoryCapabilityVersion || len(targetKeys) > 0 && targetKeys[0] && cap.GitRepositoryVersion < deploy.GitRepositoryCapabilityVersion {
		return errors.New("EXECUTOR_GIT_UNSUPPORTED")
	}
	return nil
}

func gitObservationMatches(c deploy.GitRepositoryRequest, o *deploy.GitRepositoryObservation) bool {
	if o == nil || o.BindingID != c.BindingID || o.ProfileID != c.ProfileID || o.ProfileVersion != c.ProfileVersion || o.Account != c.Account || o.RepoPath != c.RepoPath || o.KeyFingerprint != c.KeyFingerprint || !deploy.Hex.MatchString(o.ConfigHash) || o.AccountUID == "" || c.AccountUID != "" && o.AccountUID != c.AccountUID {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, o.ObservedAt)
	return err == nil
}

func (h *HTTP) checkGitRepository(ctx context.Context, t target, c deploy.GitRepositoryRequest) (deploy.GitRepositoryObservation, error) {
	var p deploy.GitRepositoryPreflight
	if err := h.gitTargetReady(ctx, t, c.KeySource == "TARGET"); err != nil {
		return deploy.GitRepositoryObservation{}, err
	}
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "GIT_REPOSITORY_CHECK", GitRepository: &c}, "", &p); err != nil {
		return deploy.GitRepositoryObservation{}, err
	}
	if !p.Ready {
		return deploy.GitRepositoryObservation{}, errors.New(p.Code)
	}
	if !gitObservationMatches(c, p.Observation) {
		return deploy.GitRepositoryObservation{}, errors.New("GIT_RESULT_MISMATCH")
	}
	return *p.Observation, nil
}

type gitPlan struct {
	ID                     string                          `json:"id"`
	HostID                 string                          `json:"hostId"`
	Action                 string                          `json:"action"`
	Change                 deploy.GitRepositoryRequest     `json:"change"`
	Observation            deploy.GitRepositoryObservation `json:"observation"`
	HostSnapshot           string                          `json:"hostSnapshot"`
	ExpectedBindingVersion string                          `json:"expectedBindingVersion"`
	ActorGeneration        int                             `json:"actorGeneration"`
	ExpiresAt              string                          `json:"expiresAt"`
}

func (h *HTTP) createGitPlan(w http.ResponseWriter, r *http.Request, s *session, hostID string) {
	var in struct {
		ExpectedHostVersion    string `json:"expectedHostVersion"`
		BindingID              string `json:"bindingId"`
		ExpectedBindingVersion string `json:"expectedBindingVersion"`
		Action                 string `json:"action"`
		Account                string `json:"account"`
		RepoPath               string `json:"repoPath"`
		ProfileID              string `json:"profileId"`
		ExpectedProfileVersion string `json:"expectedProfileVersion"`
	}
	if decode(w, r, &in) != nil || (in.Action != "BIND" && in.Action != "UNBIND") {
		h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
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
	var b gitRepository
	if in.BindingID != "" {
		b, err = readGitRepository(h.store.DB, in.BindingID)
		if err != nil {
			h.opsError(w, r, err)
			return
		}
		if b.HostID != hostID || b.Version != in.ExpectedBindingVersion || b.Status == "RELEASED" {
			h.gitError(w, r, errors.New("GIT_BINDING_CHANGED"))
			return
		}
		in.Account, in.RepoPath = b.Account, b.RepoPath
	} else if in.Action != "BIND" {
		h.gitError(w, r, errors.New("GIT_BINDING_CHANGED"))
		return
	}
	var c deploy.GitRepositoryRequest
	if in.Action == "UNBIND" {
		c = b.Change
		c.Action = "UNBIND"
		c.ExpectedConfigHash = ""
	} else {
		a, e := scanGitAccount(h.store.DB.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", in.ProfileID))
		if e != nil {
			h.opsError(w, r, e)
			return
		}
		if a.Version != in.ExpectedProfileVersion {
			h.gitError(w, r, errors.New("GIT_PROFILE_CHANGED"))
			return
		}
		if a.KeySource == "TARGET" && (a.HostID != hostID || a.SystemAccount != in.Account) {
			h.gitError(w, r, errors.New("GIT_KEY_SCOPE_MISMATCH"))
			return
		}
		if a.KeySource != "TARGET" {
			if _, e = h.store.secret(a.CredentialID, a.CredentialRevision, "SSH_KEY"); e != nil {
				h.gitError(w, r, errors.New("GIT_CREDENTIAL_UNAVAILABLE"))
				return
			}
		}
		c = a.request()
		c.Account = in.Account
		c.RepoPath = in.RepoPath
		if b.ID != "" {
			c.BindingID = b.ID
			c.BindingVersion = b.Version
			c.AccountUID = b.AccountUID
			c.GitBinary = b.Change.GitBinary
		} else {
			// Only an already registered tool can supply a private executable path.
			var binary string
			e = h.store.DB.QueryRow("SELECT locator FROM software_instances WHERE host_id=? AND software_type='GIT' AND runtime_kind='TOOL' AND management_mode IN ('MANAGED','OBSERVE') ORDER BY CASE WHEN locator='/usr/bin/git' THEN 0 ELSE 1 END,created_at LIMIT 1", hostID).Scan(&binary)
			if e == nil {
				c.GitBinary = binary
			} else if !errors.Is(e, sql.ErrNoRows) {
				h.internal(w, r, e)
				return
			}
		}
	}
	if c.Validate() != nil {
		h.gitError(w, r, errors.New("GIT_LEGACY_CONFIGURATION_INCOMPLETE"))
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	o, err := h.checkGitRepository(ctx, t, c)
	if err != nil {
		h.gitError(w, r, err)
		return
	}
	c.AccountUID = o.AccountUID
	c.ExpectedConfigHash = o.ConfigHash
	c.ExpectedTerminalHash = o.TerminalHash
	p := gitPlan{ID: uuid.NewString(), HostID: hostID, Action: in.Action, Change: c, Observation: o, HostSnapshot: pluginTargetSnapshot(t), ExpectedBindingVersion: in.ExpectedBindingVersion, ActorGeneration: s.Generation, ExpiresAt: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if err = validateGitPlan(tx, p); err != nil {
		h.gitError(w, r, err)
		return
	}
	_, err = tx.Exec("INSERT INTO git_repository_plans(id,actor_id,host_id,binding_id,snapshot_json,expires_at,created_at) VALUES(?,?,?,?,?,?,?)", p.ID, s.AdminID, hostID, c.BindingID, jsonText(p), p.ExpiresAt, Now())
	if err == nil {
		err = auditTx(tx, s.Username, "GIT_REPOSITORY_PLAN_CREATED", p.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, p)
}

func validateGitPlan(q gitQuery, p gitPlan) error {
	t, err := scanHost(q.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", p.HostID))
	if err != nil || t.Maintenance || pluginTargetSnapshot(t) != p.HostSnapshot {
		return errors.New("HOST_CHANGED")
	}
	if p.Action == "BIND" {
		a, e := scanGitAccount(q.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", p.Change.ProfileID))
		if e != nil || a.Version != p.Change.ProfileVersion {
			return errors.New("GIT_PROFILE_CHANGED")
		}
		if err := validateGitProfileKey(q, a, p.HostID, p.Change); err != nil {
			return err
		}
	}
	var count int
	if p.ExpectedBindingVersion != "" {
		b, e := readGitRepository(q, p.Change.BindingID)
		if e != nil || b.Version != p.ExpectedBindingVersion || b.HostID != p.HostID || b.AccountUID != p.Change.AccountUID || b.RepoPath != p.Change.RepoPath || b.Status == "RELEASED" {
			return errors.New("GIT_BINDING_CHANGED")
		}
	} else {
		if err = q.QueryRow("SELECT count(*) FROM git_repositories WHERE host_id=? AND repo_path=? AND status<>'RELEASED'", p.HostID, p.Change.RepoPath).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return errors.New("GIT_REPOSITORY_EXISTS")
		}
		if err = q.QueryRow("SELECT count(*) FROM git_repositories WHERE host_id=?", p.HostID).Scan(&count); err != nil {
			return err
		}
		if count >= 200 {
			return errors.New("GIT_REPOSITORY_LIMIT")
		}
	}
	if err = q.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+p.HostID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("OPERATION_LOCKED")
	}
	return nil
}

func validateGitProfileKey(q gitQuery, a gitAccount, hostID string, c deploy.GitRepositoryRequest) error {
	if a.KeySource == "TARGET" {
		if c.KeySource != "TARGET" || a.HostID != hostID || a.SystemAccount != c.Account || a.AccountUID != c.AccountUID || a.KeyPath != c.KeyPath || a.KeyFingerprint != c.KeyFingerprint {
			return errors.New("GIT_KEY_SCOPE_MISMATCH")
		}
		return nil
	}
	if c.KeySource == "TARGET" || a.CredentialID != c.CredentialID || a.CredentialRevision != c.CredentialRevision {
		return errors.New("GIT_PROFILE_CHANGED")
	}
	var valid int
	if q.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND revision=? AND revoked_at IS NULL", a.CredentialID, a.CredentialRevision).Scan(&valid) != nil || valid != 1 {
		return errors.New("GIT_CREDENTIAL_UNAVAILABLE")
	}
	return nil
}

func (h *HTTP) observeGitRepository(w http.ResponseWriter, r *http.Request, s *session, id string, test bool) {
	var in struct {
		ExpectedVersion string `json:"expectedVersion"`
	}
	if decode(w, r, &in) != nil {
		h.gitError(w, r, errors.New("GIT_BINDING_CHANGED"))
		return
	}
	b, err := readGitRepository(h.store.DB, id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if b.Version != in.ExpectedVersion || b.Status != "BOUND" {
		h.gitError(w, r, errors.New("GIT_BINDING_CHANGED"))
		return
	}
	t, err := h.store.readTarget(b.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	c := b.Change
	c.ExpectedConfigHash = ""
	action := "GIT_REPOSITORY_OBSERVE"
	if test {
		action = "GIT_REPOSITORY_TEST"
	}
	if err = h.gitTargetReady(ctx, t, c.KeySource == "TARGET"); err != nil {
		h.gitError(w, r, err)
		return
	}
	var result deploy.GitRepositoryPreflight
	err = h.store.callTarget(ctx, t, deploy.Request{Action: action, GitRepository: &c}, "", &result)
	if err != nil {
		h.gitError(w, r, err)
		return
	}
	if result.Observation == nil && !result.Ready && result.Code != "" {
		h.gitError(w, r, errors.New(result.Code))
		return
	}
	if !gitObservationMatches(c, result.Observation) {
		h.gitError(w, r, errors.New("GIT_RESULT_MISMATCH"))
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
	updated, e := tx.Exec("UPDATE git_repositories SET observation_json=?,error_code=?,updated_at=? WHERE id=? AND version=? AND status='BOUND'", jsonText(result.Observation), result.Code, Now(), id, in.ExpectedVersion)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	n, _ := updated.RowsAffected()
	if n != 1 {
		h.gitError(w, r, errors.New("GIT_BINDING_CHANGED"))
		return
	}
	if test {
		err = auditTx(tx, s.Username, "GIT_REPOSITORY_TEST", id, result.Code, r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if test {
		h.success(w, r, result)
	} else {
		h.success(w, r, result.Observation)
	}
}

func (h *HTTP) gitRepositoryJobs(w http.ResponseWriter, r *http.Request, id string) {
	rows, err := h.store.DB.Query("SELECT j.id,j.kind,j.status,j.phase,j.error_code,j.trace_id,j.target_ack_pending,j.created_at,j.updated_at FROM jobs j JOIN git_repository_plans p ON p.job_id=j.id WHERE p.binding_id=? ORDER BY j.created_at DESC LIMIT 50", id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var job, kind, status, phase, code, trace, created, updated string
		var ack bool
		if err = rows.Scan(&job, &kind, &status, &phase, &code, &trace, &ack, &created, &updated); err != nil {
			h.internal(w, r, err)
			return
		}
		out = append(out, map[string]any{"id": job, "kind": kind, "status": status, "phase": phase, "errorCode": code, "traceId": trace, "targetAckPending": ack, "createdAt": created, "updatedAt": updated})
	}
	if err = rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, out)
}
