package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type installSource struct {
	serviceinstall.Source
	MaterialID string `json:"materialId,omitempty"`
}
type installCheck struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type installPaths struct {
	Bin    string `json:"bin"`
	Logs   string `json:"logs"`
	Root   string `json:"root"`
	Data   string `json:"data"`
	Config string `json:"config"`
}
type installPortMapping struct {
	Address       string `json:"address"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	Key           string `json:"key,omitempty"`
}
type softwareInstallPlan struct {
	ID             string                    `json:"id"`
	TargetID       string                    `json:"targetId"`
	TargetSnapshot string                    `json:"-"`
	HostVersion    string                    `json:"-"`
	SoftwareID     string                    `json:"-"`
	Definition     serviceinstall.Definition `json:"definition"`
	Config         serviceinstall.Config     `json:"config"`
	Source         installSource             `json:"source"`
	ExpiresAt      string                    `json:"expiresAt"`
	Checks         []installCheck            `json:"checks"`
	Paths          installPaths              `json:"paths"`
	PortMappings   []installPortMapping      `json:"portMappings"`
}

// Plan-private identity is part of the persisted snapshot, never a browser
// supplied field. The public plan response contains only actionable options.
type storedInstallPlan struct {
	softwareInstallPlan
	Snapshot string `json:"targetSnapshot"`
	Version  string `json:"hostVersion"`
	Software string `json:"softwareId"`
}

func (p softwareInstallPlan) request() *serviceinstall.Request {
	return &serviceinstall.Request{SoftwareID: p.SoftwareID, BindingRevision: 1, Definition: p.Definition, Config: p.Config, Source: p.Source.Source}
}
func (p softwareInstallPlan) snapshot() string {
	return jsonText(storedInstallPlan{softwareInstallPlan: p, Snapshot: p.TargetSnapshot, Version: p.HostVersion, Software: p.SoftwareID})
}
func parseInstallPlan(raw string) (softwareInstallPlan, error) {
	var stored storedInstallPlan
	err := deploy.StrictJSON([]byte(raw), &stored)
	p := stored.softwareInstallPlan
	p.TargetSnapshot, p.HostVersion, p.SoftwareID = stored.Snapshot, stored.Version, stored.Software
	return p, err
}

func (h *HTTP) softwareInstallRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	if h.softwareConfigurationRoute(w, r, s, path) {
		return true
	}
	if h.installationMaterialRoute(w, r, s, path) {
		return true
	}
	if path == "/software-recipes" && r.Method == "GET" {
		h.success(w, r, serviceinstall.Catalog())
		return true
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) == 3 && deploy.ID.MatchString(p[1]) && r.Method == "POST" {
		if p[0] == "hosts" && p[2] == "software-install-plans" {
			h.createSoftwareInstallPlan(w, r, s, p[1])
			return true
		}
		if p[0] == "software-install-plans" && p[2] == "submit" {
			h.submitSoftwareInstallPlan(w, r, s, p[1])
			return true
		}
	}
	return false
}

type installCredential struct{ ID, Revision, Kind string }

func installCredentials(c serviceinstall.Config) []installCredential {
	refs := []installCredential{}
	for _, v := range []installCredential{{c.CredentialID, c.CredentialRevision, "PASSWORD"}, {c.UserCredentialID, c.UserCredentialRevision, "PASSWORD"}, {c.AccessKeyCredentialID, c.AccessKeyCredentialRevision, "TOKEN"}} {
		if v.ID != "" {
			refs = append(refs, v)
		}
	}
	return refs
}

func (s *Store) checkInstallSecrets(d serviceinstall.Definition, c serviceinstall.Config) error {
	values := map[string]string{}
	for _, ref := range installCredentials(c) {
		secret, err := s.secret(ref.ID, ref.Revision, ref.Kind)
		if err != nil {
			return errors.New("INSTALL_CREDENTIAL_UNAVAILABLE")
		}
		values[serviceinstall.SecretKey(ref.ID, ref.Revision)] = string(secret)
	}
	return serviceinstall.ValidateSecrets(d, c, values)
}

func checkInstallCredentialTx(tx *sql.Tx, c serviceinstall.Config) error {
	for _, ref := range installCredentials(c) {
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND kind=? AND revoked_at IS NULL", ref.ID, ref.Revision, ref.Kind).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return errors.New("INSTALL_CREDENTIAL_UNAVAILABLE")
		}
	}
	return nil
}

func (h *HTTP) installTargetReady(ctx context.Context, t target, config serviceinstall.Config) error {
	var capability executorCapabilities
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &capability); err != nil {
		return err
	}
	if capability.Owner.ControllerID != h.store.controllerID() || capability.Owner.TargetID != t.ID || capability.Owner.ExecutorVersion != deploy.Version {
		return errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if capability.Platform != "linux/amd64" {
		return errors.New("INSTALL_PLATFORM_UNSUPPORTED")
	}
	if config.Mode() == "DOCKER" && !capability.DockerEngine {
		return errors.New("DOCKER_ENGINE_UNAVAILABLE")
	}
	if capability.InstallVersion != serviceinstall.CapabilityVersion || capability.CatalogHash != serviceinstall.CatalogHash() {
		return errors.New("EXECUTOR_UPGRADE_REQUIRED")
	}
	return nil
}

func (h *HTTP) createSoftwareInstallPlan(w http.ResponseWriter, r *http.Request, s *session, targetID string) {
	var in struct {
		HostVersion string                `json:"expectedHostVersion"`
		Key         string                `json:"recipeKey"`
		Version     string                `json:"recipeVersion"`
		Revision    string                `json:"recipeRevision"`
		Config      serviceinstall.Config `json:"config"`
		Source      struct {
			Type       string `json:"type"`
			MaterialID string `json:"materialId,omitempty"`
		} `json:"source"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	d, err := serviceinstall.Find(in.Key, in.Version)
	if err != nil || d.Revision != in.Revision {
		h.fail(w, r, 400, "INSTALL_RECIPE_INVALID", "请选择当前内置配方版本")
		return
	}
	if err = in.Config.Validate(d); err != nil {
		h.fail(w, r, 400, err.Error(), "请核对实例名、明确IPv4、端口、内存与该软件的凭据参数")
		return
	}
	source := installSource{Source: serviceinstall.Source{Type: in.Source.Type}, MaterialID: in.Source.MaterialID}
	if source.Type == "ARCHIVE" || (source.Type == "ONLINE" && in.Config.Mode() == "NATIVE") {
		m, e := h.store.readInstallationMaterial(source.MaterialID)
		if e != nil || !m.Available || m.RuntimeMode != in.Config.Mode() || serviceinstall.Digest(m.Definition) != serviceinstall.Digest(d) {
			h.fail(w, r, 409, "INSTALL_MATERIAL_NOT_READY", "离线材料缺失、未校验或不匹配所选版本")
			return
		}
		actual, e := fileDigest(h.store.installationMaterialPath(m.ID, false))
		if e != nil || actual != m.SHA256 {
			h.fail(w, r, 409, "INSTALL_MATERIAL_CHANGED", "离线材料文件已变化，请重新导入")
			return
		}
		source.SHA256, source.ByteSize = m.SHA256, m.ByteSize
	} else if source.MaterialID != "" {
		h.fail(w, r, 400, "INSTALL_SOURCE_INVALID", "在线方式不能带离线材料")
		return
	}
	if err = source.Source.ValidateFor(d, in.Config); err != nil {
		h.fail(w, r, 400, err.Error(), "请选择在线或已校验离线材料")
		return
	}
	if err = h.store.checkInstallSecrets(d, in.Config); err != nil {
		h.fail(w, r, 409, err.Error(), installSecretMessage(err))
		return
	}
	t, err := h.store.readTarget(targetID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Version != in.HostVersion {
		h.fail(w, r, 409, "HOST_CHANGED", "主机已变化，请刷新后重检")
		return
	}
	if t.Maintenance {
		h.fail(w, r, 409, "HOST_MAINTENANCE", "主机维护中，请恢复后预检")
		return
	}
	if err = checkInstalledTool(h.store.DB, t.ID, d, in.Config); err != nil {
		h.fail(w, r, 409, "TOOL_ALREADY_INSTALLED", "该Docker工具已有安装记录，请在工具安装记录中使用或核对；原生安装可选择独立目录")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err = h.installTargetReady(ctx, t, in.Config); err != nil {
		h.fail(w, r, 409, executorFailureCode(err), "请打开主机安装准备，按运行方式核对执行器、授权和依赖")
		return
	}
	p := softwareInstallPlan{ID: uuid.NewString(), TargetID: t.ID, TargetSnapshot: pluginTargetSnapshot(t), HostVersion: t.Version, SoftwareID: uuid.NewString(), Definition: d, Config: in.Config, Source: source, ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	var preflight serviceinstall.Preflight
	if err = h.store.callTarget(ctx, t, deploy.Request{Action: "SOFTWARE_INSTALL_CHECK", Installation: p.request()}, "", &preflight); err != nil {
		code, message := installPreflightError(err)
		h.fail(w, r, 409, code, message)
		return
	}
	if !preflight.Ready {
		code, message := h.installPreflightRejection(preflight, p.Config)
		h.fail(w, r, 409, code, message)
		return
	}
	root := serviceinstall.InstallRoot(t.InstallRoot, p.SoftwareID, p.Config)
	if preflight.Root != root || preflight.DataDirectory != root+"/data" || preflight.ConfigDirectory != root+"/config" {
		h.fail(w, r, 409, "INSTALL_PREFLIGHT_MISMATCH", "执行器返回的目标路径不匹配，请升级并核对执行器")
		return
	}
	p.Paths = installPaths{Root: preflight.Root, Data: preflight.DataDirectory, Config: preflight.ConfigDirectory, Bin: preflight.BinaryDirectory, Logs: preflight.LogsDirectory}
	for _, mapping := range preflight.PortMappings {
		p.PortMappings = append(p.PortMappings, installPortMapping{Address: mapping.BindAddress, HostPort: mapping.HostPort, ContainerPort: mapping.ContainerPort, Protocol: mapping.Protocol, Key: mapping.Key})
	}
	for _, check := range preflight.Checks {
		status := "PASSED"
		if check.Status != "PASS" {
			status = "WARNING"
		}
		p.Checks = append(p.Checks, installCheck{check.Key, status, check.Message})
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", t.ID))
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if pluginTargetSnapshot(current) != p.TargetSnapshot || current.Maintenance {
		h.fail(w, r, 409, "HOST_CHANGED", "预检期间主机变化，请重新检查")
		return
	}
	if err = checkInstallCredentialTx(tx, in.Config); err != nil {
		h.fail(w, r, 409, err.Error(), "预检期间凭据变化，请重新检查")
		return
	}
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+t.ID).Scan(&n); err != nil {
		h.internal(w, r, err)
		return
	}
	if n > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "主机存在未决作业，请先核对")
		return
	}
	if err = tx.QueryRow("SELECT count(*) FROM software_install_plans WHERE target_id=? AND status='DRAFT' AND expires_at>?", t.ID, time.Now().UTC().Format(time.RFC3339)).Scan(&n); err != nil {
		h.internal(w, r, err)
		return
	}
	if n >= 20 {
		h.fail(w, r, 409, "INSTALL_PLAN_LIMIT", "该主机有效安装计划达到20个，请等待过期")
		return
	}
	if err = reserveInstallPorts(tx, t.ID, s.AdminID, "", d, in.Config); err != nil {
		h.fail(w, r, 409, "INSTALL_PORT_RESERVED", "同主机有效计划已预留该协议端口，请修改后重检")
		return
	}
	_, err = tx.Exec("INSERT INTO software_install_plans(id,actor_id,target_id,material_id,snapshot_json,status,expires_at,created_at) VALUES(?,?,?,nullif(?,''),?,'DRAFT',?,?)", p.ID, s.AdminID, t.ID, source.MaterialID, p.snapshot(), p.ExpiresAt, Now())
	if err == nil {
		for _, ref := range installCredentials(in.Config) {
			_, err = tx.Exec("INSERT OR IGNORE INTO software_install_credentials VALUES(?,?,?)", p.ID, ref.ID, ref.Revision)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = auditTx(tx, s.Username, "SOFTWARE_INSTALL_PLAN_CREATED", p.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
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

func (h *HTTP) submitSoftwareInstallPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Key string `json:"idempotencyKey"`
	}
	if err := decode(w, r, &in); err != nil || len(in.Key) < 8 || len(in.Key) > 80 {
		h.fail(w, r, 400, "IDEMPOTENCY_REQUIRED", "请携带8–80字节提交标识")
		return
	}
	hash := deploy.Digest(struct{ Plan string }{id})
	var prior, priorHash, priorInput string
	err := h.store.DB.QueryRow("SELECT id,request_hash,input_json FROM jobs WHERE actor_id=? AND kind='SOFTWARE_INSTALL' AND idempotency_key=?", s.AdminID, in.Key).Scan(&prior, &priorHash, &priorInput)
	if err == nil {
		if priorHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他安装计划")
			return
		}
		var req deploy.Request
		if deploy.StrictJSON([]byte(priorInput), &req) != nil || req.Installation == nil {
			h.internal(w, r, errors.New("INPUT_DAMAGED"))
			return
		}
		h.success(w, r, map[string]string{"jobId": prior, "softwareInstanceId": req.Installation.SoftwareID})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	var raw, state, expiry string
	if err = h.store.DB.QueryRow("SELECT snapshot_json,status,expires_at FROM software_install_plans WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&raw, &state, &expiry); err != nil {
		h.opsError(w, r, err)
		return
	}
	p, err := parseInstallPlan(raw)
	expires, parseErr := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil || parseErr != nil || state != "DRAFT" || expiry != p.ExpiresAt || time.Now().After(expires) || p.request().Validate() != nil {
		h.fail(w, r, 409, "INSTALL_PLAN_STALE", "计划已提交、过期或不匹配当前配方，请重新预检")
		return
	}
	t, err := h.store.readTarget(p.TargetID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Maintenance || pluginTargetSnapshot(t) != p.TargetSnapshot {
		h.fail(w, r, 409, "INSTALL_PLAN_STALE", "主机已变化或维护中，请重新预检")
		return
	}
	if p.Source.Type == "ARCHIVE" || (p.Source.Type == "ONLINE" && p.Config.Mode() == "NATIVE") {
		m, e := h.store.readInstallationMaterial(p.Source.MaterialID)
		if e != nil || !m.Available || m.SHA256 != p.Source.SHA256 || m.ByteSize != p.Source.ByteSize {
			h.fail(w, r, 409, "INSTALL_MATERIAL_NOT_READY", "安装材料已变化或缺失")
			return
		}
		sha, e := fileDigest(h.store.installationMaterialPath(m.ID, false))
		if e != nil || sha != p.Source.SHA256 {
			h.fail(w, r, 409, "INSTALL_MATERIAL_CHANGED", "安装材料内容已变化，请重新导入")
			return
		}
	}
	if err = h.store.checkInstallSecrets(p.Definition, p.Config); err != nil {
		h.fail(w, r, 409, err.Error(), installSecretMessage(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err = h.installTargetReady(ctx, t, p.Config); err != nil {
		h.fail(w, r, 409, executorFailureCode(err), "目标安装能力不可用，请打开主机安装准备")
		return
	}
	var checked serviceinstall.Preflight
	if err = h.store.callTarget(ctx, t, deploy.Request{Action: "SOFTWARE_INSTALL_CHECK", Installation: p.request()}, "", &checked); err != nil || !checked.Ready {
		code, message := installPreflightError(err)
		if err == nil {
			code, message = h.installPreflightRejection(checked, p.Config)
		}
		h.fail(w, r, 409, code, message)
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
	err = tx.QueryRow("SELECT id,request_hash,input_json FROM jobs WHERE actor_id=? AND kind='SOFTWARE_INSTALL' AND idempotency_key=?", s.AdminID, in.Key).Scan(&prior, &priorHash, &priorInput)
	if err == nil {
		if priorHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他安装计划")
			return
		}
		var req deploy.Request
		if deploy.StrictJSON([]byte(priorInput), &req) != nil || req.Installation == nil {
			h.internal(w, r, errors.New("INPUT_DAMAGED"))
			return
		}
		h.success(w, r, map[string]string{"jobId": prior, "softwareInstanceId": req.Installation.SoftwareID})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", p.TargetID))
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if current.Maintenance || pluginTargetSnapshot(current) != p.TargetSnapshot {
		h.fail(w, r, 409, "INSTALL_PLAN_STALE", "提交期间主机发生变化，请重新预检")
		return
	}
	var planRaw, planStatus, planExpiry string
	if err = tx.QueryRow("SELECT snapshot_json,status,expires_at FROM software_install_plans WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&planRaw, &planStatus, &planExpiry); err != nil {
		h.opsError(w, r, err)
		return
	}
	if planRaw != raw || planStatus != "DRAFT" || planExpiry != p.ExpiresAt || time.Now().After(expires) {
		h.fail(w, r, 409, "INSTALL_PLAN_STALE", "计划已变化、提交或过期")
		return
	}
	if err = checkInstallCredentialTx(tx, p.Config); err != nil {
		h.fail(w, r, 409, err.Error(), "凭据在提交期间发生变化")
		return
	}
	if p.Source.MaterialID != "" {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM installation_materials WHERE id=? AND status='AVAILABLE' AND sha256=? AND byte_size=?", p.Source.MaterialID, p.Source.SHA256, p.Source.ByteSize).Scan(&n); err != nil {
			h.internal(w, r, err)
			return
		}
		if n != 1 {
			h.fail(w, r, 409, "INSTALL_MATERIAL_NOT_READY", "材料已变化，请重新预检")
			return
		}
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+p.TargetID).Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "主机有未决作业，请先核对")
		return
	}
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "未结束作业达到20个")
		return
	}
	if err = checkInstalledTool(tx, p.TargetID, p.Definition, p.Config); err != nil {
		h.fail(w, r, 409, "TOOL_ALREADY_INSTALLED", "该Docker工具已有安装记录，请查看原工具及发布引用")
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.TargetID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: "SOFTWARE_INSTALL", SoftwareOperation: "SOFTWARE_INSTALL", Installation: p.request(), DeadlineAt: time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,software_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_INSTALL',?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", job, s.AdminID, p.TargetID, id, in.Key, hash, jsonText(req), req.TraceID, now, now)
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?),(?,?)", "target:"+p.TargetID, job, "software:"+p.SoftwareID, job)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,desired_state,actual_state,binding_revision,last_job_id,data_ownership,install_plan_id,installation_json,installation_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'OBSERVE',?,'UNKNOWN',1,?,'OPS',?,?,'INSTALLING',?,?)", p.SoftwareID, p.Config.Name, p.TargetID, strings.ToUpper(p.Definition.Key), p.Definition.Version, installationRuntimeKind(p.Definition, p.Config), "pending:"+p.SoftwareID, installationDesiredState(p.Definition), job, id, jsonText(req.Installation), now, now)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE software_install_plans SET status='SUBMITTED' WHERE id=?", id)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "SOFTWARE_INSTALL_ACCEPTED", p.SoftwareID, "SUCCESS", req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	w.Header().Set("Location", api+"/jobs/"+job)
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job, "softwareInstanceId": p.SoftwareID})
}

func installSecretMessage(err error) string {
	switch err.Error() {
	case "SERVICE_MYSQL_SECRET_CHARACTERS":
		return "MySQL密码须8–128位受支持ASCII字符；当前官方初始化配方不接受引号、反斜线、空白或非ASCII字符"
	case "SERVICE_ACCESS_KEY_INVALID":
		return "S3 Access Key须3–128位字母、数字或_+=.@-，请确认选的是TOKEN凭据"
	case "SERVICE_SECRET_INVALID", "SERVICE_USER_SECRET_INVALID":
		return "服务密码须8–256字节，不含换行、空字节或首尾空白"
	default:
		return "凭据不存在、已撤销或类型不匹配，请选择有效修订"
	}
}

func installPreflightError(err error) (string, string) {
	if err != nil && strings.Contains(err.Error(), "SERVICE_SECRET_INVALID") {
		return "SERVICE_SECRET_INVALID", "目标配方拒绝凭据内容，请检查凭据类型、长度和字符；不要把SSH凭据当服务密码"
	}
	return "INSTALL_PREFLIGHT_FAILED", "目标检查未完成，请核对主机连接、监听地址、执行器版本与Docker权限"
}

func (h *HTTP) installPreflightRejection(p serviceinstall.Preflight, c serviceinstall.Config) (string, string) {
	code := "INSTALL_PREFLIGHT_REJECTED"
	parts := []string{}
	for _, check := range p.Checks {
		if check.Status == "PASS" {
			continue
		}
		if strings.HasPrefix(check.Code, "SERVICE_") && len(check.Code) < 90 && !strings.ContainsAny(check.Code, " \r\n\t:") {
			code = check.Code
		}
		text := check.Key + ": " + check.Message
		if check.Code != "" {
			text += " (" + check.Code + ")"
		}
		parts = append(parts, text)
		if len(parts) >= 8 {
			break
		}
	}
	message := strings.Join(parts, "；")
	if message == "" {
		message = "端口、目录或依赖未通过检查，请修改参数后重新预检"
	}
	for _, ref := range installCredentials(c) {
		if secret, err := h.store.secret(ref.ID, ref.Revision, ref.Kind); err == nil {
			message = strings.ReplaceAll(message, string(secret), "[REDACTED]")
		}
	}
	message = deploy.RedactSoftwareLogs(message)
	runes := []rune(message)
	if len(runes) > 1200 {
		message = string(runes[:1200])
	}
	return code, message
}
