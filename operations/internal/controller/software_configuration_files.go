package controller

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Configuration-file operations use the regular management plan and job flow.
// The file body is carried in an encrypted envelope and never enters a plan
// response or a normal operation log.
func (h *HTTP) softwareConfigurationFilesRoute(w http.ResponseWriter, r *http.Request, actor *session, p []string) bool {
	if (len(p) != 3 && len(p) != 4) || p[0] != "software-instances" || !deploy.ID.MatchString(p[1]) || p[2] != "config-files" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case len(p) == 3 && r.Method == http.MethodGet:
		h.readSoftwareConfigurationFile(w, r, p[1], serviceinstall.ConfigurationFileRequest{Action: "list"})
	case len(p) == 4 && r.Method == http.MethodGet:
		h.readSoftwareConfigurationFile(w, r, p[1], serviceinstall.ConfigurationFileRequest{Action: "read", Name: p[3]})
	case len(p) == 4 && p[3] == "check" && r.Method == http.MethodPost:
		h.checkSoftwareConfigurationFile(w, r, actor, p[1])
	case len(p) == 4 && p[3] == "apply" && r.Method == http.MethodPost:
		h.applySoftwareConfigurationFile(w, r, actor, p[1])
	default:
		h.fail(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "此配置文件操作不受支持")
	}
	return true
}

func (h *HTTP) softwareConfigurationFileContext(w http.ResponseWriter, r *http.Request, id, operation string) (softwareInstance, softwareManagementState, commandTemplate, bool) {
	i, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return i, softwareManagementState{}, commandTemplate{}, false
	}
	if !managementInstance(i) || i.SoftwareType != "NGINX" || i.Installation == nil || i.Installation.Definition.Key != "nginx" {
		h.fail(w, r, http.StatusConflict, "CONFIG_FILE_UNAVAILABLE", "请先确认 Nginx 已由 Ops 安装和管理")
		return i, softwareManagementState{}, commandTemplate{}, false
	}
	state, err := h.store.readSoftwareManagement(id)
	if err != nil {
		h.internal(w, r, err)
		return i, state, commandTemplate{}, false
	}
	template, err := h.store.managementTemplate(i, state.TemplateID, state.TemplateRevision)
	if err != nil || template.Published == nil || !serviceinstall.IsBuiltinCommand(*template.Published) {
		h.fail(w, r, http.StatusConflict, "CONFIG_FILE_UNAVAILABLE", "此实例未启用 Nginx 文件管理方案")
		return i, state, template, false
	}
	updated := configurationFileDefinition(*template.Published)
	template.Published = &updated
	if _, err = template.Published.ManagementOperation(operation); err != nil {
		h.fail(w, r, http.StatusConflict, "CONFIG_FILE_UNAVAILABLE", "请发布支持配置文件的 Nginx 管理方案")
		return i, state, template, false
	}
	return i, state, template, true
}

func (h *HTTP) readSoftwareConfigurationFile(w http.ResponseWriter, r *http.Request, id string, file serviceinstall.ConfigurationFileRequest) {
	if file.Validate() != nil {
		h.fail(w, r, http.StatusBadRequest, "CONFIG_FILE_INPUT_INVALID", "配置文件标识无效")
		return
	}
	const operation = "config-file-read"
	i, state, template, ok := h.softwareConfigurationFileContext(w, r, id, operation)
	if !ok {
		return
	}
	t, err := h.store.readTarget(i.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	m := serviceinstall.ManagementRequest{
		ConfigFile: &file, Definition: *template.Published, SoftwareID: i.ID, BindingRevision: i.BindingRevision,
		Root: softwareManagementRoot(i, t), Account: i.Installation.Config.Account, OperationKey: operation,
		Parameters: state.Parameters, ParameterCredentials: state.CredentialRefs,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	checked, err := h.checkSoftwareOperation(ctx, t, i, m)
	if err != nil {
		h.fail(w, r, http.StatusConflict, configurationErrorCode(err), "读取配置文件失败，请核对主机连接和接入组件")
		return
	}
	if checked.Ready && checked.File != nil && checked.File.Status != "FAILED" {
		if file.Action == "read" && checked.File.Content != "" && h.origin.Scheme != "https" {
			host := h.origin.Hostname()
			ip := net.ParseIP(host)
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				h.fail(w, r, http.StatusForbidden, "CONFIG_FILE_TLS_REQUIRED", "请通过 HTTPS 或本机地址读取配置内容")
				return
			}
		}
		h.success(w, r, checked.File)
		return
	}
	if checked.File != nil && validDiagnosticCode(checked.File.Code) {
		message := "配置文件无法读取，请刷新列表或检查服务器文件"
		if checked.File.Code == "CONFIG_FILE_PYTHON_REQUIRED" {
			message = "配置文件编辑需要服务器已有 Python 3.9 或更新版本"
		}
		h.fail(w, r, http.StatusConflict, checked.File.Code, message)
		return
	}
	code := managementPreflightError(checked)
	message := "未能确认配置文件，请检查软件状态和读取权限"
	if code == "TARGET_PENDING" || code == "EXECUTOR_BUSY" {
		message = "主机有进行中或待核对的操作，请先查看执行记录"
	}
	h.fail(w, r, http.StatusConflict, code, message)
}

func (h *HTTP) checkSoftwareConfigurationFile(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in struct {
		Name     string `json:"name"`
		Revision string `json:"revision"`
		Action   string `json:"action"`
		Content  string `json:"content"`
	}
	if err := h.decodeSensitive(w, r, actor, &in); err != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	file := serviceinstall.ConfigurationFileRequest{Name: in.Name, Revision: in.Revision, Action: in.Action}
	if in.Action != "save" && in.Action != "enable" && in.Action != "disable" && in.Action != "delete" {
		h.fail(w, r, http.StatusBadRequest, "CONFIG_FILE_INPUT_INVALID", "配置文件操作无效")
		return
	}
	if in.Action == "save" {
		if !serviceinstall.ValidConfigurationFileBody(in.Content) {
			h.fail(w, r, http.StatusBadRequest, "CONFIG_FILE_CONTENT_INVALID", "请输入不超过 32 KiB 的有效配置文本")
			return
		}
		file.SHA256 = serviceinstall.Digest(in.Content)
		var err error
		file.ContentEnvelope, err = seal(h.store.Key, configurationFileSecretID(id, file.Name), 1, []byte(in.Content))
		if err != nil {
			h.internal(w, r, err)
			return
		}
	}
	if file.Validate() != nil {
		h.fail(w, r, http.StatusBadRequest, "CONFIG_FILE_INPUT_INVALID", "配置文件名称或版本无效，请重新读取")
		return
	}
	const operation = "config-file-save"
	i, state, template, ok := h.softwareConfigurationFileContext(w, r, id, operation)
	if !ok {
		return
	}
	h.createSoftwareOperationPlanInput(w, r, actor, id, softwareOperationPlanInput{
		ExpectedVersion: i.Version, ExpectedManagementVersion: state.Version, TemplateID: template.ID, TemplateRevision: template.Revision,
		OperationKey: operation, Parameters: state.Parameters, CredentialRefs: state.CredentialRefs, ConfigFile: &file,
	})
}

func configurationFileSecretID(softwareID, name string) string {
	return softwareID + "/config-file/" + name
}

func (h *HTTP) applySoftwareConfigurationFile(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in struct {
		PlanID string `json:"planId"`
		Key    string `json:"idempotencyKey"`
	}
	if err := decode(w, r, &in); err != nil || !deploy.ID.MatchString(in.PlanID) || len(strings.TrimSpace(in.Key)) < 8 || len(in.Key) > 80 {
		h.fail(w, r, http.StatusBadRequest, "CONFIG_FILE_INPUT_INVALID", "请先检查配置并重新提交")
		return
	}
	var raw string
	if err := h.store.DB.QueryRow("SELECT snapshot_json FROM software_configuration_plans WHERE id=? AND actor_id=? AND software_id=?", in.PlanID, actor.AdminID, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.fail(w, r, http.StatusConflict, "MANAGEMENT_PLAN_STALE", "找不到原检查计划，请重新检查")
		} else {
			h.internal(w, r, err)
		}
		return
	}
	plan, err := decodeSoftwareOperationPlan(raw)
	if err != nil || plan.Management.ConfigFile == nil || plan.Operation.Key != "config-file-save" {
		h.fail(w, r, http.StatusConflict, "MANAGEMENT_PLAN_STALE", "检查计划已变化，请重新检查")
		return
	}
	h.submitSoftwareOperationPlanKey(w, r, actor, in.PlanID, in.Key)
}
