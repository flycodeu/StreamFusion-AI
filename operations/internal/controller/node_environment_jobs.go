package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type nodeEnvironmentPlan struct {
	ID            string                             `json:"id"`
	HostID        string                             `json:"hostId"`
	EnvironmentID string                             `json:"environmentId"`
	Name          string                             `json:"name"`
	Action        string                             `json:"action"`
	Change        deploy.NodeEnvironmentRequest      `json:"change"`
	Snapshot      *deploy.NodeEnvironmentObservation `json:"snapshot,omitempty"`
	Checks        []serviceinstall.Check             `json:"checks"`
	Commands      []serviceinstall.OfficialCommand   `json:"commands"`
	RemovedPaths  []string                           `json:"removedPaths"`
	MaterialID    string                             `json:"materialId,omitempty"`
	HostSnapshot  string                             `json:"hostSnapshot"`
	ExpiresAt     string                             `json:"expiresAt"`
}

func (h *HTTP) nodePreflight(ctx context.Context, t target, c deploy.NodeEnvironmentRequest) (deploy.NodeEnvironmentPreflight, error) {
	var p deploy.NodeEnvironmentPreflight
	if err := h.nodeTargetReady(ctx, t, c.Action == "CONNECT_TERMINAL" || c.Context.ConnectTerminal); err != nil {
		return p, err
	}
	err := h.store.callTarget(ctx, t, deploy.Request{Action: "NODE_ENVIRONMENT_CHECK", NodeEnvironment: &c}, "", &p)
	if err != nil {
		return p, err
	}
	if !p.Ready {
		for _, v := range p.Checks {
			if v.Status == "FAIL" {
				return p, errors.New(v.Code)
			}
		}
		return p, errors.New("NODE_PREFLIGHT_FAILED")
	}
	if p.Context.Validate() != nil || p.Context.EnvironmentID != c.Context.EnvironmentID || p.Context.Account != c.Context.Account {
		return p, errors.New("NODE_PREFLIGHT_MISMATCH")
	}
	if c.Action != "INITIALIZE" && deploy.Digest(p.Context) != deploy.Digest(c.Context) {
		return p, errors.New("NODE_ENVIRONMENT_DRIFT")
	}
	n := nodeEnvironment{ID: p.Context.EnvironmentID, Account: p.Context.Account, AccountUID: p.Context.AccountUID, RootPath: p.Context.RootPath, ManagerType: "NVM", ManagementMode: "MANAGED", NVMVersion: p.Context.NVMVersion, NVMHash: p.Context.NVMHash}
	if p.Observation == nil || !nodeObservationMatches(n, *p.Observation) || c.ExpectedHash != "" && p.Observation.Fingerprint != c.ExpectedHash {
		return p, errors.New("NODE_PREFLIGHT_MISMATCH")
	}
	var removed []string
	if c.Action == "REMOVE_ENVIRONMENT" && c.DeleteData {
		removed = []string{p.Context.RootPath}
	}
	if !slices.Equal(p.RemovedPaths, removed) {
		return p, errors.New("NODE_PREFLIGHT_MISMATCH")
	}
	return p, nil
}

func (h *HTTP) createNodeEnvironmentPlan(w http.ResponseWriter, r *http.Request, s *session, hostID string) {
	var in struct {
		HostVersion        string `json:"expectedHostVersion"`
		EnvironmentID      string `json:"environmentId"`
		EnvironmentVersion string `json:"expectedEnvironmentVersion"`
		Name               string `json:"name"`
		Action             string `json:"action"`
		Account            string `json:"account"`
		NodeVersion        string `json:"nodeVersion"`
		PackageVersion     string `json:"packageVersion"`
		Source             struct {
			Type       string `json:"type"`
			MaterialID string `json:"materialId,omitempty"`
		} `json:"source"`
		DeleteData       bool   `json:"deleteData"`
		ConfirmationName string `json:"confirmationName"`
	}
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "请核对环境操作参数")
		return
	}
	switch in.Action {
	case "INITIALIZE", "SET_DEFAULT", "UPDATE_NVM", "UPDATE_NPM", "INSTALL_PNPM", "UPDATE_PNPM", "REMOVE_PNPM", "REMOVE_ENVIRONMENT", "CONNECT_TERMINAL":
	default:
		h.nodeError(w, r, errors.New("NODE_ACTION_INVALID"))
		return
	}
	t, err := h.store.readTarget(hostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Version != in.HostVersion || t.Maintenance {
		h.nodeError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	var env nodeEnvironment
	c := deploy.NodeEnvironmentRequest{Action: in.Action, NodeVersion: in.NodeVersion, PackageVersion: in.PackageVersion, Source: serviceinstall.Source{Type: in.Source.Type}, DeleteData: in.DeleteData}
	if in.Action == "INITIALIZE" {
		if in.EnvironmentID != "" || strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 || !serviceinstall.NodeAccountPattern.MatchString(in.Account) || in.Account == "root" {
			h.fail(w, r, 400, "NODE_ACCOUNT_INVALID", "填写环境名称并选择非 root 账号")
			return
		}
		c.Context = serviceinstall.NodeContext{EnvironmentID: uuid.NewString(), EnvironmentVersion: "1", Account: in.Account, NVMVersion: serviceinstall.NodeNVMVersion, NVMHash: serviceinstall.NodeNVMHash}
	} else {
		env, err = h.store.readNodeEnvironment(in.EnvironmentID)
		if err != nil {
			h.opsError(w, r, err)
			return
		}
		if env.HostID != hostID || env.Version != in.EnvironmentVersion {
			h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_CHANGED"))
			return
		}
		if env.ManagementMode != "MANAGED" {
			h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_READ_ONLY"))
			return
		}
		c.Context = env.context()
		c.ExpectedHash = env.Observation.Fingerprint
		if in.Action == "CONNECT_TERMINAL" {
			// Older observations have no terminal fields. Freeze the live read
			// below while retaining the environment/account/default identity.
			c.ExpectedHash = ""
			c.Context.ConnectTerminal = true
		}
		in.Name = env.Name
		if in.Action == "REMOVE_ENVIRONMENT" && in.ConfirmationName != env.Name {
			h.nodeError(w, r, errors.New("NODE_CONFIRMATION_REQUIRED"))
			return
		}
		if in.Action == "REMOVE_ENVIRONMENT" {
			var count int
			if err = h.store.DB.QueryRow("SELECT count(*) FROM software_instances WHERE node_environment_id=? AND management_mode<>'RELEASED' AND installation_status NOT IN ('FAILED','CANCELLED','UNINSTALLED')", env.ID).Scan(&count); err != nil {
				h.internal(w, r, err)
				return
			}
			if count != 0 {
				h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_NOT_EMPTY"))
				return
			}
		}
	}
	needsMaterial := in.Action == "INITIALIZE" || in.Action == "UPDATE_NVM" || in.Action == "UPDATE_NPM" || in.Action == "INSTALL_PNPM" || in.Action == "UPDATE_PNPM"
	if needsMaterial {
		definition, e := serviceinstall.NodeEnvironmentRecipe(in.Action, in.PackageVersion)
		if e != nil {
			h.nodeError(w, r, e)
			return
		}
		if in.Action == "UPDATE_NVM" {
			c.PackageVersion = definition.Version
		}
		if in.Source.Type == "ARCHIVE" {
			m, e := h.store.readInstallationMaterial(in.Source.MaterialID)
			if e != nil || !m.Available {
				h.nodeError(w, r, errors.New("NODE_MATERIAL_MISSING"))
				return
			}
			digest, e := fileDigest(h.store.installationMaterialPath(m.ID, false))
			if e != nil || digest != m.SHA256 || deploy.Digest(m.Definition) != deploy.Digest(definition) {
				h.nodeError(w, r, errors.New("NODE_MATERIAL_MISMATCH"))
				return
			}
			c.Source.SHA256, c.Source.ByteSize = m.SHA256, m.ByteSize
		} else if in.Source.Type != "OFFICIAL" || in.Source.MaterialID != "" {
			h.fail(w, r, 400, "NODE_SOURCE_INVALID", "请选择官方在线或已校验离线材料")
			return
		}
	} else if in.Source.Type != "" || in.Source.MaterialID != "" {
		h.fail(w, r, 400, "NODE_SOURCE_UNEXPECTED", "此操作不需要安装材料")
		return
	}
	if in.DeleteData && in.Action != "REMOVE_ENVIRONMENT" {
		h.fail(w, r, 400, "NODE_DELETE_DATA_ACTION_INVALID", "删除数据选项仅适用于移除环境")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	check, err := h.nodePreflight(ctx, t, c)
	if err != nil {
		h.nodeError(w, r, err)
		return
	}
	c.Context = check.Context
	if c.Action == "CONNECT_TERMINAL" {
		c.ExpectedHash = check.Observation.Fingerprint
	}
	if c.Validate() != nil {
		h.nodeError(w, r, errors.New("NODE_PREFLIGHT_MISMATCH"))
		return
	}
	p := nodeEnvironmentPlan{ID: uuid.NewString(), HostID: hostID, EnvironmentID: c.Context.EnvironmentID, Name: in.Name, Action: in.Action, Change: c, Snapshot: check.Observation, Checks: check.Checks, Commands: check.Commands, RemovedPaths: check.RemovedPaths, MaterialID: in.Source.MaterialID, HostSnapshot: pluginTargetSnapshot(t), ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", hostID))
	if err != nil || pluginTargetSnapshot(current) != p.HostSnapshot || current.Maintenance {
		h.nodeError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	if err = nodeTargetUnlocked(tx, hostID); err == nil && env.ID != "" {
		err = nodeEnvironmentCurrent(tx, env)
	}
	if err == nil {
		err = validateNodeActionMembers(tx, p.EnvironmentID, p.Change)
	}
	if err != nil {
		h.nodeError(w, r, err)
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM node_environment_plans WHERE host_id=? AND status='DRAFT' AND expires_at>?", hostID, Now()).Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 20 {
		h.nodeError(w, r, errors.New("QUEUE_FULL"))
		return
	}
	_, err = tx.Exec("INSERT INTO node_environment_plans(id,actor_id,host_id,environment_id,action,snapshot_json,expires_at,created_at) VALUES(?,?,?,nullif(?,''),?,?,?,?)", p.ID, s.AdminID, hostID, env.ID, p.Action, jsonText(p), p.ExpiresAt, Now())
	if err == nil {
		err = auditTx(tx, s.Username, "NODE_ENVIRONMENT_PLAN_CREATED", p.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
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

func (h *HTTP) submitNodeEnvironmentPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Key       string `json:"idempotencyKey"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(w, r, &in) != nil || len(in.Key) < 8 || len(in.Key) > 80 || !in.Confirmed {
		h.fail(w, r, 400, "NODE_CONFIRMATION_REQUIRED", "请确认计划后提交")
		return
	}
	hash := deploy.Digest([]string{id})
	var old, oldHash, oldEnv string
	err := h.store.DB.QueryRow("SELECT j.id,j.request_hash,p.environment_id FROM jobs j JOIN node_environment_plans p ON p.id=j.node_environment_plan_id WHERE j.actor_id=? AND j.kind='NODE_ENVIRONMENT_CHANGE' AND j.idempotency_key=?", s.AdminID, in.Key).Scan(&old, &oldHash, &oldEnv)
	if err == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他操作")
		} else {
			h.success(w, r, map[string]string{"jobId": old, "environmentId": oldEnv})
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	var raw, status, expiry string
	if err = h.store.DB.QueryRow("SELECT snapshot_json,status,expires_at FROM node_environment_plans WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&raw, &status, &expiry); err != nil {
		h.opsError(w, r, err)
		return
	}
	var p nodeEnvironmentPlan
	deadline, e := time.Parse(time.RFC3339, expiry)
	if deploy.StrictJSON([]byte(raw), &p) != nil || e != nil || status != "DRAFT" || time.Now().After(deadline) || p.Change.Validate() != nil {
		h.nodeError(w, r, errors.New("NODE_PLAN_STALE"))
		return
	}
	t, err := h.store.readTarget(p.HostID)
	if err != nil || t.Maintenance || pluginTargetSnapshot(t) != p.HostSnapshot {
		h.nodeError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	if p.MaterialID != "" {
		m, e := h.store.readInstallationMaterial(p.MaterialID)
		if e != nil || !m.Available || m.SHA256 != p.Change.Source.SHA256 || m.ByteSize != p.Change.Source.ByteSize {
			h.nodeError(w, r, errors.New("NODE_MATERIAL_MISSING"))
			return
		}
		digest, e := fileDigest(h.store.installationMaterialPath(m.ID, false))
		if e != nil || digest != m.SHA256 {
			h.nodeError(w, r, errors.New("NODE_MATERIAL_MISMATCH"))
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	checked, err := h.nodePreflight(ctx, t, p.Change)
	if err != nil {
		h.nodeError(w, r, err)
		return
	}
	if deploy.Digest(checked.Context) != deploy.Digest(p.Change.Context) {
		h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_DRIFT"))
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
	err = tx.QueryRow("SELECT j.id,j.request_hash,p.environment_id FROM jobs j JOIN node_environment_plans p ON p.id=j.node_environment_plan_id WHERE j.actor_id=? AND j.kind='NODE_ENVIRONMENT_CHANGE' AND j.idempotency_key=?", s.AdminID, in.Key).Scan(&old, &oldHash, &oldEnv)
	if err == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识冲突")
		} else {
			h.success(w, r, map[string]string{"jobId": old, "environmentId": oldEnv})
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", p.HostID))
	if err != nil || current.Maintenance || pluginTargetSnapshot(current) != p.HostSnapshot {
		h.nodeError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM node_environment_plans WHERE id=? AND status='DRAFT' AND snapshot_json=? AND expires_at>?", id, raw, Now()).Scan(&count); err != nil || count != 1 {
		h.nodeError(w, r, errors.New("NODE_PLAN_STALE"))
		return
	}
	if err = nodeTargetUnlocked(tx, p.HostID); err != nil {
		h.nodeError(w, r, err)
		return
	}
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 20 {
		h.nodeError(w, r, errors.New("QUEUE_FULL"))
		return
	}
	c := p.Change.Context
	if p.Action != "INITIALIZE" {
		n, e := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", p.EnvironmentID))
		expectedContext := n.context()
		if p.Action == "CONNECT_TERMINAL" {
			expectedContext.ConnectTerminal = true
		}
		if e != nil || n.Version != c.EnvironmentVersion || n.ManagementMode != "MANAGED" || deploy.Digest(expectedContext) != deploy.Digest(c) {
			h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_CHANGED"))
			return
		}
	}
	if p.MaterialID != "" {
		if err = tx.QueryRow("SELECT count(*) FROM installation_materials WHERE id=? AND status='AVAILABLE' AND sha256=? AND byte_size=?", p.MaterialID, p.Change.Source.SHA256, p.Change.Source.ByteSize).Scan(&count); err != nil || count != 1 {
			h.nodeError(w, r, errors.New("NODE_MATERIAL_MISSING"))
			return
		}
	}
	if err = validateNodeActionMembers(tx, p.EnvironmentID, p.Change); err != nil {
		h.nodeError(w, r, err)
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.HostID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: "NODE_ENVIRONMENT_CHANGE", SoftwareOperation: "NODE_ENVIRONMENT_CHANGE", NodeEnvironment: &p.Change, DeadlineAt: time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	if p.Action == "INITIALIZE" {
		_, err = tx.Exec("INSERT INTO node_environments(id,host_id,name,account,account_uid,root_path,manager_type,management_mode,nvm_version,nvm_hash,error_code,last_job_id,created_at,updated_at) VALUES(?,?,?,?,?,?,'NVM','MANAGED',?,?,'NODE_INITIALIZING',?,?,?)", p.EnvironmentID, p.HostID, p.Name, c.Account, c.AccountUID, c.RootPath, c.NVMVersion, c.NVMHash, job, now, now)
	} else {
		_, err = tx.Exec("UPDATE node_environments SET last_job_id=?,updated_at=? WHERE id=?", job, now, p.EnvironmentID)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE node_environment_plans SET environment_id=?,status='SUBMITTED' WHERE id=?", p.EnvironmentID, id)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,node_environment_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'NODE_ENVIRONMENT_CHANGE',?,?,?,?,'QUEUED','NODE_PREFLIGHT',?,?,?,?)", job, s.AdminID, p.HostID, id, in.Key, hash, jsonText(req), req.TraceID, now, now)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+p.HostID, job)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "NODE_ENVIRONMENT_CHANGE_ACCEPTED", p.EnvironmentID, "SUCCESS", req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job, "environmentId": p.EnvironmentID})
}

func (h *HTTP) processNodeEnvironment(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.NodeEnvironment == nil || req.NodeEnvironment.Validate() != nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	t, err := h.store.readTarget(req.TargetID)
	if err != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	h.store.mu.Lock()
	claimed, err := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='NODE_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, t.ID)
	h.store.mu.Unlock()
	if err != nil {
		return
	}
	if count, _ := claimed.RowsAffected(); count != 1 {
		return
	}
	archive := ""
	if req.NodeEnvironment.Source.Type == "ARCHIVE" {
		var rawPlan string
		err = h.store.DB.QueryRow("SELECT p.snapshot_json FROM node_environment_plans p JOIN jobs j ON j.node_environment_plan_id=p.id WHERE j.id=?", id).Scan(&rawPlan)
		var p nodeEnvironmentPlan
		if err != nil || deploy.StrictJSON([]byte(rawPlan), &p) != nil || !deploy.ID.MatchString(p.MaterialID) {
			h.jobUnknown(id, "NODE_MATERIAL_MISSING")
			return
		}
		archive = h.store.installationMaterialPath(p.MaterialID, false)
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	progressCtx, stop := context.WithCancel(runCtx)
	done := make(chan struct{})
	go func() { defer close(done); h.pollProgress(progressCtx, t, req) }()
	var result deploy.Result
	err = h.store.callTarget(runCtx, t, req, archive, &result)
	stop()
	<-done
	if err != nil || result.Status == "UNKNOWN" {
		h.jobUnknown(id, "NODE_RESULT_UNCONFIRMED")
		return
	}
	if h.commitNodeEnvironmentResult(id, req, result) != nil {
		h.jobUnknown(id, "NODE_RESULT_MISMATCH")
		return
	}
	h.ackResult(ctx, t, req, result)
}

func (h *HTTP) commitNodeEnvironmentResult(id string, req deploy.Request, result deploy.Result) error {
	c := req.NodeEnvironment
	if c == nil || result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID || result.JobID != id || result.InputHash != req.InputHash {
		return errors.New("NODE_RESULT_MISMATCH")
	}
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("NODE_RESULT_NOT_FINAL")
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, receipt string
	if err = tx.QueryRow("SELECT status,result_json FROM jobs WHERE id=? AND input_json=?", id, jsonText(req)).Scan(&status, &receipt); err != nil {
		return err
	}
	if status == "SUCCEEDED" || status == "FAILED" {
		if receipt != jsonText(result) {
			return errors.New("NODE_RESULT_ALREADY_COMMITTED")
		}
		return nil
	}
	n, err := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", c.Context.EnvironmentID))
	if err != nil {
		return err
	}
	if n.LastJobID != id {
		return errors.New("NODE_ENVIRONMENT_CHANGED")
	}
	if result.Status == "SUCCEEDED" {
		o := result.NodeEnvironment
		expectedEnvironment := n
		if c.Action == "UPDATE_NVM" {
			version := c.PackageVersion
			if version == "" {
				version = c.Context.NVMVersion
			}
			d, e := serviceinstall.NodeEnvironmentRecipe(c.Action, version)
			if e != nil {
				return e
			}
			expectedEnvironment.NVMVersion = d.Version
			for _, part := range d.Native.Parts {
				if part.Name == "nvm.sh" {
					expectedEnvironment.NVMHash = part.SHA256
				}
			}
		}
		if o == nil || !nodeObservationMatches(expectedEnvironment, *o) || c.Action != "REMOVE_ENVIRONMENT" && !o.Exists || c.Action == "REMOVE_ENVIRONMENT" && o.Exists {
			return errors.New("NODE_EVIDENCE_MISMATCH")
		}
		if _, e := time.Parse(time.RFC3339Nano, o.ObservedAt); e != nil {
			return errors.New("NODE_OBSERVATION_TIME_INVALID")
		}
		if c.Action == "SET_DEFAULT" && o.DefaultVersion != c.NodeVersion {
			return errors.New("NODE_DEFAULT_MISMATCH")
		}
		if c.Action == "CONNECT_TERMINAL" && (o.Terminal == nil || o.Terminal.Status != "CONNECTED") {
			return errors.New("NODE_TERMINAL_UNCONFIRMED")
		}
		if c.Action == "REMOVE_ENVIRONMENT" && c.Context.ConnectTerminal && (o.Terminal == nil || o.Terminal.Status != "NOT_CONNECTED") {
			return errors.New("NODE_TERMINAL_UNCONFIRMED")
		}
		if c.Action != "SET_DEFAULT" && c.Action != "REMOVE_ENVIRONMENT" && o.DefaultVersion != c.Context.ExpectedDefault {
			return errors.New("NODE_DEFAULT_MISMATCH")
		}
		if !nodeUnchangedVersions(n.Observation.Versions, o.Versions, c.NodeVersion) {
			return errors.New("NODE_SIBLING_VERSION_CHANGED")
		}
		if c.Action == "INITIALIZE" && len(o.Versions) != 0 {
			return errors.New("NODE_EVIDENCE_MISMATCH")
		}
		if c.NodeVersion != "" {
			for _, before := range n.Observation.Versions {
				if before.Version == c.NodeVersion {
					found := false
					for _, after := range o.Versions {
						if after.Version == before.Version {
							found = after.BinaryPath == before.BinaryPath && after.BinaryHash == before.BinaryHash
						}
					}
					if !found {
						return errors.New("NODE_BINARY_CHANGED")
					}
				}
			}
		}
		if c.Action == "UPDATE_NPM" || c.Action == "INSTALL_PNPM" || c.Action == "UPDATE_PNPM" || c.Action == "REMOVE_PNPM" {
			matched := false
			for _, v := range o.Versions {
				if v.Version == c.NodeVersion {
					matched = c.Action == "UPDATE_NPM" && v.NPMVersion == c.PackageVersion || (c.Action == "INSTALL_PNPM" || c.Action == "UPDATE_PNPM") && v.PNPMVersion == c.PackageVersion || c.Action == "REMOVE_PNPM" && v.PNPMVersion == ""
				}
			}
			if !matched {
				return errors.New("NODE_PACKAGE_RESULT_MISMATCH")
			}
		}
		err = storeNodeObservation(tx, n.ID, *o)
		if err == nil && c.Action == "SET_DEFAULT" {
			var softwareID string
			if err = tx.QueryRow("SELECT id FROM software_instances WHERE node_environment_id=? AND software_version=? AND management_mode='MANAGED' AND installation_status='READY'", n.ID, c.NodeVersion).Scan(&softwareID); err == nil {
				_, err = tx.Exec("UPDATE node_environments SET desired_default_software_id=? WHERE id=?", softwareID, n.ID)
			}
		}
		if err == nil && c.Action == "REMOVE_ENVIRONMENT" {
			_, err = tx.Exec("UPDATE node_environments SET management_mode='RELEASED',desired_default_software_id=NULL WHERE id=?", n.ID)
		}
	} else {
		_, err = tx.Exec("UPDATE node_environments SET error_code=?,version=version+1,updated_at=? WHERE id=?", result.ErrorCode, Now(), n.ID)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE jobs SET status=?,phase=?,error_code=?,result_json=?,target_ack_pending=1,updated_at=? WHERE id=?", result.Status, result.Phase, result.ErrorCode, jsonText(result), Now(), id)
	}
	if err == nil {
		err = auditTx(tx, "worker", "NODE_ENVIRONMENT_CHANGE_RESULT", n.ID, result.Status, req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}
