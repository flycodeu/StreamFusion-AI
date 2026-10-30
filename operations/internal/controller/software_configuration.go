package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type softwareConfigurationPlan struct {
	ID             string                    `json:"id"`
	SoftwareID     string                    `json:"softwareId"`
	TargetID       string                    `json:"targetId"`
	Definition     serviceinstall.Definition `json:"definition"`
	PreviousConfig serviceinstall.Config     `json:"previousConfig"`
	Config         serviceinstall.Config     `json:"config"`
	Paths          installPaths              `json:"paths"`
	PortMappings   []installPortMapping      `json:"portMappings"`
	Checks         []installCheck            `json:"checks"`
	ExpiresAt      string                    `json:"expiresAt"`
}

type storedConfigurationPlan struct {
	softwareConfigurationPlan
	SoftwareVersion string                 `json:"softwareVersion"`
	TargetSnapshot  string                 `json:"targetSnapshot"`
	Binding         deploy.SoftwareRequest `json:"binding"`
	Previous        serviceinstall.Request `json:"previous"`
}

func parseConfigurationPlan(raw string) (storedConfigurationPlan, error) {
	var p storedConfigurationPlan
	err := deploy.StrictJSON([]byte(raw), &p)
	return p, err
}

func (p storedConfigurationPlan) change() *serviceinstall.ChangeRequest {
	return &serviceinstall.ChangeRequest{Previous: p.Previous, Config: p.Config, ExpectedBindingRevision: p.Binding.Version}
}

func (h *HTTP) softwareConfigurationRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) != 3 || !deploy.ID.MatchString(p[1]) || r.Method != "POST" {
		return false
	}
	if p[0] == "software-instances" && p[2] == "configuration-plans" {
		h.createSoftwareConfigurationPlan(w, r, s, p[1])
		return true
	}
	if p[0] == "software-configuration-plans" && p[2] == "submit" {
		h.submitSoftwareConfigurationPlan(w, r, s, p[1])
		return true
	}
	return false
}

func (h *HTTP) checkSoftwareConfiguration(ctx context.Context, t target, i softwareInstance, config serviceinstall.Config) (serviceinstall.Preflight, error) {
	var out serviceinstall.Preflight
	if i.Installation == nil || !installationBound(i) || i.InstallationStatus != "READY" || i.RuntimeKind == "TOOL" || i.SoftwareType == "DOCKER" {
		return out, errors.New("CONFIGURATION_UNAVAILABLE")
	}
	change := &serviceinstall.ChangeRequest{Previous: *i.Installation, Config: config, ExpectedBindingRevision: i.BindingRevision}
	if err := change.Validate(); err != nil {
		return out, err
	}
	if err := h.store.checkInstallSecrets(i.Installation.Definition, config); err != nil {
		return out, err
	}
	if err := h.installTargetReady(ctx, t, config); err != nil {
		return out, err
	}
	err := h.store.callTarget(ctx, t, deploy.Request{Action: "SOFTWARE_CONFIGURE_CHECK", Software: softwareRequest(i), Reconfiguration: change}, "", &out)
	if err == nil {
		root := serviceinstall.InstallRoot(t.InstallRoot, i.ID, config)
		if out.Root != root || out.DataDirectory != root+"/data" || out.ConfigDirectory != root+"/config" {
			err = errors.New("INSTALL_PREFLIGHT_MISMATCH")
		}
	}
	return out, err
}

func (h *HTTP) createSoftwareConfigurationPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		ExpectedVersion string                `json:"expectedVersion"`
		Config          serviceinstall.Config `json:"config"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	i, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if i.Version != in.ExpectedVersion {
		h.fail(w, r, 409, "VERSION_CONFLICT", "软件已变化，请刷新后重新配置")
		return
	}
	if i.Installation == nil || !installationBound(i) || i.InstallationStatus != "READY" || i.RuntimeKind == "TOOL" || i.SoftwareType == "DOCKER" {
		h.fail(w, r, 409, "CONFIGURATION_UNAVAILABLE", "仅已确认安装的服务可修改配置；工具无监听端口，外部只读登记不能直接写配置")
		return
	}
	if i.Installation.Config.StorageRoot != in.Config.StorageRoot {
		h.fail(w, r, 409, "STORAGE_MIGRATION_REQUIRED", "已有数据位置需停机迁移；本次只修改网络配置，不搬移数据")
		return
	}
	t, err := h.store.readTarget(i.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Maintenance {
		h.fail(w, r, 409, "HOST_MAINTENANCE", "主机维护中，请恢复后修改配置")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	checked, err := h.checkSoftwareConfiguration(ctx, t, i, in.Config)
	if err != nil {
		h.fail(w, r, 409, configurationErrorCode(err), "配置检查未完成，请核对凭据、运行方式和主机安装准备；MySQL初始化凭据不能以改文件方式轮换")
		return
	}
	if !checked.Ready {
		code, message := h.installPreflightRejection(checked, in.Config)
		h.fail(w, r, 409, code, message)
		return
	}
	p := storedConfigurationPlan{softwareConfigurationPlan: softwareConfigurationPlan{ID: uuid.NewString(), SoftwareID: i.ID, TargetID: i.HostID, Definition: i.Installation.Definition, PreviousConfig: i.Installation.Config, Config: in.Config, Paths: installPaths{Root: checked.Root, Data: checked.DataDirectory, Config: checked.ConfigDirectory, Bin: checked.BinaryDirectory, Logs: checked.LogsDirectory}, ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}, SoftwareVersion: i.Version, TargetSnapshot: pluginTargetSnapshot(t), Binding: *softwareRequest(i), Previous: *i.Installation}
	for _, v := range checked.PortMappings {
		p.PortMappings = append(p.PortMappings, installPortMapping{Key: v.Key, Address: v.BindAddress, HostPort: v.HostPort, ContainerPort: v.ContainerPort, Protocol: v.Protocol})
	}
	for _, v := range checked.Checks {
		status := "PASSED"
		if v.Status != "PASS" {
			status = "WARNING"
		}
		p.Checks = append(p.Checks, installCheck{Key: v.Key, Status: status, Message: v.Message})
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	current, err := scanSoftware(tx.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", id))
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if current.Version != i.Version || !installationBound(current) || current.IdentityHash != i.IdentityHash {
		h.fail(w, r, 409, "VERSION_CONFLICT", "配置检查期间软件已变化")
		return
	}
	host, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", t.ID))
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if host.Maintenance || pluginTargetSnapshot(host) != p.TargetSnapshot {
		h.fail(w, r, 409, "HOST_CHANGED", "配置检查期间主机已变化")
		return
	}
	var busy int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key IN (?,?)", "target:"+i.HostID, "software:"+i.ID).Scan(&busy); err != nil {
		h.internal(w, r, err)
		return
	}
	if busy > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "主机或软件有未决作业，请先核对")
		return
	}
	if err = checkInstallCredentialTx(tx, in.Config); err != nil {
		h.fail(w, r, 409, "INSTALL_CREDENTIAL_UNAVAILABLE", "配置检查期间凭据已变化")
		return
	}
	if _, err = tx.Exec("UPDATE software_configuration_plans SET status='SUPERSEDED' WHERE actor_id=? AND software_id=? AND status='DRAFT'", s.AdminID, id); err != nil {
		h.internal(w, r, err)
		return
	}
	if err = reserveInstallPorts(tx, t.ID, s.AdminID, "", p.Definition, p.Config); err != nil {
		h.fail(w, r, 409, "INSTALL_PORT_RESERVED", "其他有效计划已预留该协议端口")
		return
	}
	if err = tx.QueryRow("SELECT count(*) FROM software_configuration_plans WHERE target_id=? AND status='DRAFT' AND expires_at>?", t.ID, time.Now().UTC().Format(time.RFC3339)).Scan(&busy); err != nil {
		h.internal(w, r, err)
		return
	}
	if busy >= 20 {
		h.fail(w, r, 409, "CONFIGURATION_PLAN_LIMIT", "该主机有效配置计划达到20个")
		return
	}
	_, err = tx.Exec("INSERT INTO software_configuration_plans VALUES(?,?,?,?,?,'DRAFT',?,?)", p.ID, s.AdminID, p.TargetID, p.SoftwareID, jsonText(p), p.ExpiresAt, Now())
	if err == nil {
		for _, ref := range append(installCredentials(i.Installation.Config), installCredentials(in.Config)...) {
			_, err = tx.Exec("INSERT OR IGNORE INTO software_configuration_credentials VALUES(?,?,?)", p.ID, ref.ID, ref.Revision)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = auditTx(tx, s.Username, "SOFTWARE_CONFIGURATION_PLAN_CREATED", p.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, p.softwareConfigurationPlan)
}

func configurationErrorCode(err error) string {
	code := err.Error()
	if len(code) < 100 && !strings.ContainsAny(code, " \t\r\n:") {
		return code
	}
	return "CONFIGURATION_CHECK_FAILED"
}
