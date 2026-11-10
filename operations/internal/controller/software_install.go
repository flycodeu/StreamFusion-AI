package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	Node           *serviceinstall.NodeContext  `json:"node,omitempty"`
	Official       *serviceinstall.OfficialPlan `json:"official,omitempty"`
	ID             string                       `json:"id"`
	TargetID       string                       `json:"targetId"`
	TargetSnapshot string                       `json:"-"`
	HostVersion    string                       `json:"-"`
	SoftwareID     string                       `json:"-"`
	Definition     serviceinstall.Definition    `json:"definition"`
	Config         serviceinstall.Config        `json:"config"`
	Source         installSource                `json:"source"`
	ExpiresAt      string                       `json:"expiresAt"`
	Checks         []installCheck               `json:"checks"`
	Paths          installPaths                 `json:"paths"`
	PortMappings   []installPortMapping         `json:"portMappings"`
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
	return &serviceinstall.Request{SoftwareID: p.SoftwareID, BindingRevision: 1, Definition: p.Definition, Config: p.Config, Source: p.Source.Source, Official: p.Official, Node: p.Node}
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
	if h.commandTemplateRoute(w, r, s, path) {
		return true
	}
	if h.softwareConfigurationRoute(w, r, s, path) {
		return true
	}
	if h.installationMaterialRoute(w, r, s, path) {
		return true
	}
	if path == "/software-recipes" && r.Method == "GET" {
		recipes, err := h.store.publishedRecipes()
		if err != nil {
			h.internal(w, r, err)
		} else {
			h.success(w, r, recipes)
		}
		return true
	}
	if path == "/software-official-recipes" && r.Method == "GET" {
		recipes, err := h.store.publishedOfficialRecipes()
		if err != nil {
			h.internal(w, r, err)
		} else {
			h.success(w, r, recipes)
		}
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
	seen := map[string]bool{}
	for _, v := range []installCredential{{c.CredentialID, c.CredentialRevision, "PASSWORD"}, {c.UserCredentialID, c.UserCredentialRevision, "PASSWORD"}, {c.AccessKeyCredentialID, c.AccessKeyCredentialRevision, "TOKEN"}} {
		key := serviceinstall.SecretKey(v.ID, v.Revision)
		if v.ID != "" && !seen[key] {
			refs = append(refs, v)
			seen[key] = true
		}
	}
	for _, ref := range c.CredentialReferences() {
		if !seen[serviceinstall.SecretKey(ref.ID, ref.Revision)] {
			refs = append(refs, installCredential{ID: ref.ID, Revision: ref.Revision})
		}
	}
	return refs
}

func (s *Store) installSecret(ref installCredential) ([]byte, error) {
	if ref.Kind == "" {
		return s.parameterSecret(serviceinstall.CredentialReference{ID: ref.ID, Revision: ref.Revision})
	}
	return s.secret(ref.ID, ref.Revision, ref.Kind)
}

func (s *Store) checkInstallSecrets(d serviceinstall.Definition, c serviceinstall.Config) error {
	values := map[string]string{}
	for _, ref := range installCredentials(c) {
		secret, err := s.installSecret(ref)
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
		if err := tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND (kind=? OR ?='') AND revoked_at IS NULL", ref.ID, ref.Revision, ref.Kind, ref.Kind).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return errors.New("INSTALL_CREDENTIAL_UNAVAILABLE")
		}
	}
	return nil
}

type installCompatibilityError struct{ message string }

func (e *installCompatibilityError) Error() string { return "EXECUTOR_UPGRADE_REQUIRED" }

func (h *HTTP) installTargetReady(ctx context.Context, t target, config serviceinstall.Config, source serviceinstall.Source, selected ...serviceinstall.Definition) error {
	var capability executorCapabilities
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &capability); err != nil {
		return err
	}
	if capability.Owner.ControllerID != h.store.controllerID() || capability.Owner.TargetID != t.ID {
		return errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if capability.Owner.ProtocolVersion != 1 {
		return &installCompatibilityError{message: fmt.Sprintf("目标执行器协议版本不匹配（需要1，实际%d），请在主机安装准备中升级该受管目录的执行器，再重新预检", capability.Owner.ProtocolVersion)}
	}
	if capability.Owner.ExecutorVersion != deploy.Version {
		return &installCompatibilityError{message: "目标执行器程序版本与当前控制器不匹配，请在主机安装准备中升级该受管目录的执行器，再重新预检"}
	}
	if capability.InstallVersion != serviceinstall.CapabilityVersion {
		return &installCompatibilityError{message: fmt.Sprintf("目标执行器软件安装能力版本不匹配（需要%d，实际%d），请在主机安装准备中升级该受管目录的执行器，再重新预检", serviceinstall.CapabilityVersion, capability.InstallVersion)}
	}
	if capability.Platform != "linux/amd64" {
		return errors.New("INSTALL_PLATFORM_UNSUPPORTED")
	}
	if len(selected) > 0 && selected[0].Commands != nil {
		if capability.CommandRecipeVersion < deploy.RequiredCommandRecipeVersion(selected[0]) {
			return &installCompatibilityError{message: "命令安装需要更新接入组件，请先完成主机安装准备"}
		}
		if capability.CommandTrustFingerprint != deploy.CommandPublicKeyFingerprint(h.store.commandPublicKey()) {
			return errors.New("COMMAND_TRUST_REQUIRED")
		}
		if config.Mode() == "DOCKER" && !capability.DockerEngine {
			return errors.New("DOCKER_ENGINE_UNAVAILABLE")
		}
		if !serviceinstall.IsBuiltinCommand(selected[0]) {
			return nil
		}
	}
	if len(selected) > 0 && serviceinstall.IsNode(selected[0]) && capability.NodeEnvironmentVersion != deploy.NodeEnvironmentCapabilityVersion {
		return errors.New("NODE_CAPABILITY_REQUIRED")
	}
	if len(selected) > 0 && serviceinstall.IsOfficial(selected[0]) && capability.OfficialInstallVersion != 1 {
		return &installCompatibilityError{message: "主机已接入；当前组件尚不支持官方命令安装，请在此更新接入组件后重新预检，已有授权与业务数据保留"}
	}
	switch config.Mode() {
	case "DOCKER":
		if !capability.DockerEngine {
			return errors.New("DOCKER_ENGINE_UNAVAILABLE")
		}
	case "NATIVE":
		if capability.NativeInstallVersion != 1 {
			return errors.New("NATIVE_INSTALL_UPGRADE_REQUIRED")
		}
		if source.TargetDownload(config) && capability.NativeDownloadVersion != 1 {
			return &installCompatibilityError{message: "在线原生安装需要更新接入组件，请更新后重试"}
		}
	default:
		return errors.New("INSTALL_RUNTIME_UNSUPPORTED")
	}
	// A catalog can grow without changing any existing recipe. The following
	// SOFTWARE_INSTALL_CHECK validates the exact selected Definition on target;
	// a global catalog digest must not block recipes that executor still trusts.
	return nil
}

func (h *HTTP) createSoftwareInstallPlan(w http.ResponseWriter, r *http.Request, s *session, targetID string) {
	var in struct {
		Node        *nodeInstallInput     `json:"node,omitempty"`
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
	d, err := h.store.findInstallRecipe(in.Key, in.Version)
	if in.Key == "nvm" || in.Key == "npm" || in.Key == "pnpm" {
		h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_ACTION_REQUIRED"))
		return
	}
	if err == nil && serviceinstall.IsOfficial(d) && in.Source.Type != "OFFICIAL" {
		err = errors.New("INSTALL_SOURCE_INVALID")
	}
	if err != nil || d.Revision != in.Revision {
		h.fail(w, r, 400, "INSTALL_RECIPE_INVALID", "安装配置已变化，请重新选择已发布的版本")
		return
	}
	if err = in.Config.Validate(d); err != nil {
		h.fail(w, r, 400, "INSTALL_PARAMETERS_INVALID", "请检查安装参数格式和必填项")
		return
	}
	if len(d.Parameters) != 0 {
		in.Config.Parameters, err = serviceinstall.ResolveParameters(d.Parameters, in.Config.Parameters, in.Config.ParameterCredentials)
		if err != nil {
			h.fail(w, r, 400, "INSTALL_PARAMETERS_INVALID", "请检查安装参数格式和必填项")
			return
		}
	}
	source := installSource{Source: serviceinstall.Source{Type: in.Source.Type}, MaterialID: in.Source.MaterialID}
	if source.Type == "ARCHIVE" || (source.Type == "ONLINE" && in.Config.Mode() == "NATIVE" && source.MaterialID != "") {
		m, e := h.store.readInstallationMaterial(source.MaterialID)
		if e != nil || !m.Available || m.RuntimeMode != in.Config.Mode() || !sameMaterialDefinition(m.Definition, d) {
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
	if locked, e := hostOperationLock(h.store.DB, t.ID); e != nil {
		h.internal(w, r, e)
		return
	} else if locked.blocksInstall() {
		h.operationBlocked(w, r, locked)
		return
	}
	if err = checkInstalledTool(h.store.DB, t.ID, d, in.Config); err != nil {
		h.fail(w, r, 409, "TOOL_ALREADY_INSTALLED", "该Docker工具已有安装记录，请在工具安装记录中使用或核对；原生安装可选择独立目录")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err = h.installTargetReady(ctx, t, in.Config, source.Source, d); err != nil {
		code, message := installTargetError(err)
		h.fail(w, r, 409, code, message)
		return
	}
	node, err := h.resolveNodeInstall(targetID, d, in.Node)
	if err != nil {
		h.nodeError(w, r, err)
		return
	}
	if node != nil && (node.ConnectTerminal || node.ObserveRegistry) {
		if err = h.nodeTargetReady(ctx, t, node.ConnectTerminal, node.ObserveRegistry); err != nil {
			h.nodeError(w, r, err)
			return
		}
	}
	p := softwareInstallPlan{Node: node, ID: uuid.NewString(), TargetID: t.ID, TargetSnapshot: pluginTargetSnapshot(t), HostVersion: t.Version, SoftwareID: uuid.NewString(), Definition: d, Config: in.Config, Source: source, ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
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
	if err = validateInstallPreflightPaths(t.InstallRoot, &p, preflight); err != nil {
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
	available, err := h.confirmedFailedInstallResources(ctx, t, installationResources(t.InstallRoot, p.SoftwareID, d, in.Config))
	if err != nil {
		code, message := installTargetError(err)
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
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", t.ID))
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if pluginTargetSnapshot(current) != p.TargetSnapshot || current.Maintenance {
		h.fail(w, r, 409, "HOST_CHANGED", "预检期间主机变化，请重新检查")
		return
	}
	if err = checkNodeInstallTx(tx, p); err != nil {
		h.nodeError(w, r, err)
		return
	}
	if err = checkInstallCredentialTx(tx, in.Config); err != nil {
		h.fail(w, r, 409, err.Error(), "预检期间凭据变化，请重新检查")
		return
	}
	locked, err := hostOperationLock(tx, t.ID)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if locked.blocksInstall() {
		h.operationBlocked(w, r, locked)
		return
	}
	var n int
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
	if err = reserveManagementResources(tx, t.ID, p.SoftwareID, "", installationResources(t.InstallRoot, p.SoftwareID, d, in.Config), resourceClaimExemptions{AbsentInstallations: available}); err != nil {
		h.fail(w, r, 409, err.Error(), "安装目录、端口或服务与现有软件及待执行操作冲突，请调整后重检")
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
	if locked, e := hostOperationLock(h.store.DB, t.ID); e != nil {
		h.internal(w, r, e)
		return
	} else if locked.blocksInstall() {
		h.operationBlocked(w, r, locked)
		return
	}
	if p.Source.Type == "ARCHIVE" || (p.Source.Type == "ONLINE" && p.Config.Mode() == "NATIVE" && !p.Source.Source.TargetDownload(p.Config)) {
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
	if err = h.installTargetReady(ctx, t, p.Config, p.Source.Source, p.Definition); err != nil {
		code, message := installTargetError(err)
		h.fail(w, r, 409, code, message)
		return
	}
	if p.Node != nil && (p.Node.ConnectTerminal || p.Node.ObserveRegistry) {
		if err = h.nodeTargetReady(ctx, t, p.Node.ConnectTerminal, p.Node.ObserveRegistry); err != nil {
			h.nodeError(w, r, err)
			return
		}
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
	if serviceinstall.IsOfficial(p.Definition) && (checked.Official == nil || checked.Official.Validate(p.Definition) != nil || deploy.Digest(checked.Official) != deploy.Digest(p.Official)) {
		h.fail(w, r, 409, "OFFICIAL_PLAN_CHANGED", "官方仓库的软件版本或依赖已变化，请重新预检并确认安装命令")
		return
	}
	if err = validateInstallPreflightPaths(t.InstallRoot, &p, checked); err != nil {
		h.fail(w, r, 409, "INSTALL_PREFLIGHT_MISMATCH", "执行器返回的目标路径不匹配，请重新预检")
		return
	}
	available, err := h.confirmedFailedInstallResources(ctx, t, installationResources(t.InstallRoot, p.SoftwareID, p.Definition, p.Config))
	if err != nil {
		code, message := installTargetError(err)
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
	if p.Definition.Commands != nil {
		var currentDefinition string
		if e := tx.QueryRow("SELECT published_json FROM command_templates WHERE recipe_key=?", commandTemplateKey(p.Definition.Key, p.Definition.Version)).Scan(&currentDefinition); e != nil || currentDefinition != jsonText(p.Definition) {
			h.fail(w, r, 409, "INSTALL_PLAN_STALE", "命令配置已更新或下架，请重新选择配置并检查安装条件")
			return
		}
	}
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
	if err = checkNodeInstallTx(tx, p); err != nil {
		h.nodeError(w, r, err)
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
	locked, err := hostOperationLock(tx, p.TargetID)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if locked.blocksInstall() {
		h.operationBlocked(w, r, locked)
		return
	}
	var count int
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
	if err = reserveManagementResources(tx, p.TargetID, p.SoftwareID, p.ID, installationResources(current.InstallRoot, p.SoftwareID, p.Definition, p.Config), resourceClaimExemptions{AbsentInstallations: available}); err != nil {
		h.fail(w, r, 409, err.Error(), "安装资源已被其他软件或待执行操作占用，请重新检查安装条件")
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: p.TargetID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: "SOFTWARE_INSTALL", SoftwareOperation: "SOFTWARE_INSTALL", Installation: p.request(), DeadlineAt: time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,software_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_INSTALL',?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", job, s.AdminID, p.TargetID, id, in.Key, hash, jsonText(req), req.TraceID, now, now)
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", "software:"+p.SoftwareID, job)
	}
	if err == nil && locked.ID == "" {
		// Preserve the head of the host's queue. The worker takes the same
		// lock for subsequent installs only after the preceding ACK.
		_, err = tx.Exec("INSERT INTO operation_locks SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE target_id=? AND status='QUEUED' AND kind='SOFTWARE_INSTALL' AND id<>?)", "target:"+p.TargetID, job, p.TargetID, job)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,desired_state,actual_state,binding_revision,last_job_id,data_ownership,install_plan_id,installation_json,installation_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'OBSERVE',?,'UNKNOWN',1,?,'OPS',?,?,'INSTALLING',?,?)", p.SoftwareID, p.Config.Name, p.TargetID, strings.ToUpper(p.Definition.Key), p.Definition.Version, installationRuntimeKind(p.Definition, p.Config), "pending:"+p.SoftwareID, installationDesiredState(p.Definition), job, id, jsonText(req.Installation), now, now)
	}
	if err == nil && p.Node != nil {
		_, err = tx.Exec("UPDATE software_instances SET node_environment_id=? WHERE id=?", p.Node.EnvironmentID, p.SoftwareID)
		if err == nil {
			_, err = tx.Exec("UPDATE node_environments SET last_job_id=?,updated_at=? WHERE id=?", job, now, p.Node.EnvironmentID)
		}
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
	if err != nil {
		code, message := installTargetError(err)
		if code != "EXECUTOR_CALL_FAILED" {
			return code, message
		}
	}
	return "INSTALL_PREFLIGHT_FAILED", "目标检查未完成，请核对主机连接、监听地址、执行器版本与Docker权限"
}

func installTargetError(err error) (string, string) {
	var retryConflict *installRetryConflictError
	if errors.As(err, &retryConflict) {
		return retryConflict.Error(), retryConflict.message
	}
	if err != nil && err.Error() == "MANAGEMENT_RESOURCE_CONFLICT" {
		return "MANAGEMENT_RESOURCE_CONFLICT", "上次安装的服务或端口尚未确认空闲，请先核对原安装记录"
	}
	if err != nil {
		if message, ok := commandErrorMessages[err.Error()]; ok {
			return err.Error(), message
		}
	}
	if err != nil && err.Error() == "NODE_CAPABILITY_REQUIRED" {
		return "NODE_CAPABILITY_REQUIRED", "主机已接入，需更新接入组件以管理 Node；更新后重新检查即可"
	}
	var compatibility *installCompatibilityError
	if errors.As(err, &compatibility) {
		return compatibility.Error(), compatibility.message
	}
	code := executorFailureCode(err)
	if code == "EXECUTOR_UPGRADE_REQUIRED" {
		return code, "目标执行器的协议或软件安装能力版本与当前控制器不兼容，请在主机安装准备中升级该受管目录的执行器，再重新预检"
	}
	if message, ok := executorFailureMessages[code]; ok {
		return code, message
	}
	if strings.HasPrefix(code, "SSH_") || strings.HasPrefix(code, "CREDENTIAL_") {
		return hostSSHError(err)
	}
	if code == "LINUX_TARGET_REQUIRED" {
		return code, "当前目标不是可执行安装的Linux主机，请选择远程Linux连接"
	}
	if code == "TARGET_PATH_INVALID" {
		return code, "受管目录格式不正确，请编辑主机项目目录"
	}
	return "EXECUTOR_CALL_FAILED", executorFailureMessages["EXECUTOR_CALL_FAILED"]
}

func (h *HTTP) installPreflightRejection(p serviceinstall.Preflight, c serviceinstall.Config) (string, string) {
	code := "INSTALL_PREFLIGHT_REJECTED"
	parts := []string{}
	for _, check := range p.Checks {
		if check.Status == "PASS" {
			continue
		}
		if (strings.HasPrefix(check.Code, "SERVICE_") || strings.HasPrefix(check.Code, "OFFICIAL_") || strings.HasPrefix(check.Code, "NATIVE_") || strings.HasPrefix(check.Code, "DOCKER_") || strings.HasPrefix(check.Code, "COMMAND_")) && len(check.Code) < 90 && !strings.ContainsAny(check.Code, " \r\n\t:") {
			code = check.Code
		}
		if validNodeErrorCode(check.Code) {
			code = check.Code
		}
		text := check.Message
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
		if secret, err := h.store.installSecret(ref); err == nil {
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
