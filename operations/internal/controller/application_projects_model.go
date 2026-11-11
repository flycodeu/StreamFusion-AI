package controller

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type applicationProject struct {
	ID           string                   `json:"id"`
	Code         string                   `json:"code"`
	Name         string                   `json:"name"`
	Description  string                   `json:"description"`
	Version      string                   `json:"version"`
	Archived     bool                     `json:"archived"`
	CreatedAt    string                   `json:"createdAt"`
	UpdatedAt    string                   `json:"updatedAt"`
	Repositories []applicationRepository  `json:"repositories"`
	Modules      []applicationModule      `json:"modules"`
	Environments []applicationEnvironment `json:"environments"`
}
type applicationRepository struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	GitAccountID string `json:"gitAccountId"`
	Branch       string `json:"branch"`
}
type applicationModule struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	RepositoryID string             `json:"repositoryId"`
	Directory    string             `json:"directory"`
	Build        applicationBuild   `json:"build"`
	Runtime      applicationRuntime `json:"runtime"`
}
type applicationBuild struct {
	Kind       string            `json:"kind"`
	HostID     string            `json:"hostId"`
	Account    string            `json:"account"`
	SoftwareID string            `json:"softwareId"`
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile"`
	OutputPath string            `json:"outputPath"`
	Toolchain  string            `json:"toolchain"`
	Steps      []applicationStep `json:"steps"`
}
type applicationStep struct {
	Name           string `json:"name"`
	Command        string `json:"command"`
	Workdir        string `json:"workdir"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	Privilege      string `json:"privilege"`
}
type applicationRuntime struct {
	Kind    string            `json:"kind"`
	Command string            `json:"command"`
	Health  applicationHealth `json:"health"`
}
type applicationHealth struct {
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	Port           int    `json:"port"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type applicationEnvironment struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Deployments []applicationDeployment `json:"deployments"`
}
type applicationDeployment struct {
	ID                string                `json:"id"`
	ModuleID          string                `json:"moduleId"`
	HostID            string                `json:"hostId"`
	RuntimeSoftwareID string                `json:"runtimeSoftwareId"`
	BindAddress       string                `json:"bindAddress"`
	MemoryMiB         int                   `json:"memoryMiB"`
	Ports             []applicationPort     `json:"ports"`
	Variables         []applicationVariable `json:"variables"`
	Mounts            []applicationMount    `json:"mounts"`
	Logs              applicationLogs       `json:"logs"`
}
type applicationPort struct {
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
}
type applicationVariable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	CredentialID       string `json:"credentialId"`
	CredentialRevision string `json:"credentialRevision"`
}
type applicationMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}
type applicationLogs struct {
	Kind          string `json:"kind"`
	Directory     string `json:"directory"`
	Pattern       string `json:"pattern"`
	RetentionDays int    `json:"retentionDays"`
	MaxMiB        int    `json:"maxMiB"`
}

var applicationVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)
var applicationSensitiveName = regexp.MustCompile(`(?i)(password|passwd|secret|token|api_?key|private_?key|credential)`)
var applicationConnectionSecret = regexp.MustCompile(`(?i)(^|[;?&\s])(password|passwd|pwd|token|access_token|secret|api[_-]?key|authorization)\s*=\s*[^;\s&]+`)
var applicationSCP = regexp.MustCompile(`^([A-Za-z0-9_.-]+)@([A-Za-z0-9][A-Za-z0-9.-]*):([^\s]+)$`)

func applicationSecretValue(value string) bool {
	if applicationConnectionSecret.MatchString(value) || strings.Contains(value, "PRIVATE KEY-----") {
		return true
	}
	u, err := url.Parse(value)
	if err == nil && u.User != nil {
		password, present := u.User.Password()
		return present && password != ""
	}
	return false
}

func applicationText(value string, limit int) bool {
	return len(value) <= limit && !strings.ContainsRune(value, '\x00')
}
func applicationRelative(value string) bool {
	if value == "" {
		return true
	}
	return len(value) <= 240 && path.Clean(value) == value && !path.IsAbs(value) && value != ".." && !strings.HasPrefix(value, "../") && !strings.ContainsAny(value, "\\\r\n\x00:")
}
func applicationAbsolute(value string) bool {
	return len(value) <= 240 && strings.HasPrefix(value, "/") && value != "/" && path.Clean(value) == value && !strings.ContainsAny(value, "\\\r\n\x00")
}

type applicationGitEndpoint struct {
	Host, Method, Scheme, User string
	Port                       int
}

func applicationRepositoryEndpoint(value string) (applicationGitEndpoint, error) {
	endpoint := applicationGitEndpoint{}
	if value == "" {
		return endpoint, nil
	}
	if !applicationText(value, 2048) || strings.ContainsAny(value, "\r\n\t ") {
		return endpoint, errors.New("仓库地址格式不正确")
	}
	if parts := applicationSCP.FindStringSubmatch(value); parts != nil {
		if strings.HasPrefix(parts[3], "-") {
			return endpoint, errors.New("仓库地址格式不正确")
		}
		return applicationGitEndpoint{Host: strings.ToLower(parts[2]), Method: "SSH", Scheme: "scp", User: parts[1]}, nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.Path == "" || u.Path == "/" || u.RawQuery != "" || u.Fragment != "" || strings.HasPrefix(u.Hostname(), "-") {
		return endpoint, errors.New("请填写 HTTPS 或 SSH 仓库地址")
	}
	if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" {
		return endpoint, errors.New("仓库地址只支持 HTTP、HTTPS 或 SSH")
	}
	endpoint.Host, endpoint.Scheme = strings.ToLower(u.Hostname()), u.Scheme
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return endpoint, errors.New("仓库端口不正确")
		}
		endpoint.Port = port
	}
	if u.Scheme == "ssh" {
		if u.User != nil {
			if _, set := u.User.Password(); set {
				return endpoint, errors.New("请通过 Git 账号选择认证，不要把密码写入仓库地址")
			}
			endpoint.User = u.User.Username()
		}
		endpoint.Method = "SSH"
		return endpoint, nil
	}
	if u.User != nil {
		return endpoint, errors.New("请通过 Git 账号选择认证，不要把账号或 Token 写入仓库地址")
	}
	endpoint.Method = "HTTPS"
	if endpoint.Port == 0 {
		if u.Scheme == "https" {
			endpoint.Port = 443
		} else {
			endpoint.Port = 80
		}
	}
	return endpoint, nil
}

func normalizeApplicationProject(p *applicationProject) error {
	if p.Repositories == nil {
		p.Repositories = []applicationRepository{}
	}
	if p.Modules == nil {
		p.Modules = []applicationModule{}
	}
	if p.Environments == nil {
		p.Environments = []applicationEnvironment{}
	}
	p.Name, p.Description = strings.TrimSpace(p.Name), strings.TrimSpace(p.Description)
	p.Code = strings.TrimSpace(p.Code)
	if !validName(p.Code) {
		return errors.New("请填写项目代码，最多60个字符")
	}
	if !validName(p.Name) || !applicationText(p.Description, 2000) {
		return errors.New("请填写项目名称，说明最多2000字节")
	}
	if len(p.Repositories) > 20 || len(p.Modules) > 40 || len(p.Environments) > 20 {
		return errors.New("每项目最多20个仓库、40个模块和20个环境")
	}
	ids := map[string]bool{}
	assign := func(id *string) error {
		if *id == "" {
			*id = uuid.NewString()
		}
		if !deploy.ID.MatchString(*id) || ids[*id] {
			return errors.New("配置条目标识重复或无效，请刷新后重试")
		}
		ids[*id] = true
		return nil
	}
	repositories, modules := map[string]bool{}, map[string]bool{}
	names := map[string]bool{}
	for i := range p.Repositories {
		r := &p.Repositories[i]
		r.Name, r.URL, r.Branch = strings.TrimSpace(r.Name), strings.TrimSpace(r.URL), strings.TrimSpace(r.Branch)
		if err := assign(&r.ID); err != nil {
			return err
		}
		if !validName(r.Name) || names[strings.ToLower(r.Name)] {
			return errors.New("仓库名称不能为空或重复")
		}
		names[strings.ToLower(r.Name)] = true
		if _, err := applicationRepositoryEndpoint(r.URL); err != nil {
			return err
		}
		if !applicationText(r.Branch, 240) || strings.ContainsAny(r.Branch, "\r\n\t ") || strings.HasPrefix(r.Branch, "-") {
			return errors.New("分支、标签或提交格式不正确")
		}
		if r.GitAccountID != "" && !deploy.ID.MatchString(r.GitAccountID) {
			return errors.New("Git账号不正确")
		}
		repositories[r.ID] = true
	}
	names = map[string]bool{}
	for i := range p.Modules {
		m := &p.Modules[i]
		if m.Build.Steps == nil {
			m.Build.Steps = []applicationStep{}
		}
		m.Name = strings.TrimSpace(m.Name)
		if err := assign(&m.ID); err != nil {
			return err
		}
		if !validName(m.Name) || names[strings.ToLower(m.Name)] {
			return errors.New("模块名称不能为空或重复")
		}
		names[strings.ToLower(m.Name)] = true
		if m.RepositoryID != "" && !repositories[m.RepositoryID] {
			return errors.New("模块只能使用本项目的仓库")
		}
		if m.Directory == "" {
			m.Directory = "."
		}
		if m.Build.Context == "" {
			m.Build.Context = "."
		}
		if m.Build.Kind == "" {
			m.Build.Kind = "DOCKERFILE"
		}
		if m.Runtime.Kind == "" {
			m.Runtime.Kind = "DOCKER"
		}
		if m.Build.Kind != "DOCKERFILE" && m.Build.Kind != "COMMAND" && m.Build.Kind != "STATIC" || m.Runtime.Kind != "DOCKER" && m.Runtime.Kind != "NATIVE" && m.Runtime.Kind != "STATIC" {
			return errors.New("请选择有效的构建和运行方式")
		}
		if !applicationRelative(m.Directory) || !applicationRelative(m.Build.Context) || !applicationRelative(m.Build.Dockerfile) {
			return errors.New("模块、构建上下文和Dockerfile须使用仓库内的相对路径")
		}
		if !applicationRelative(m.Build.OutputPath) || strings.ContainsAny(m.Build.OutputPath, "*?[") {
			return errors.New("产物路径须为模块目录下的固定相对路径")
		}
		if m.Build.Account != "" && (!gitProfileUser.MatchString(m.Build.Account) || m.Build.Account == "root") {
			return errors.New("构建账号须为普通系统账号")
		}
		if !applicationText(m.Build.Toolchain, 240) || !applicationText(m.Runtime.Command, 8192) || len(m.Build.Steps) > 30 {
			return errors.New("工具链、启动命令或构建步骤超出限制")
		}
		for j := range m.Build.Steps {
			s := &m.Build.Steps[j]
			s.Name = strings.TrimSpace(s.Name)
			if s.Privilege == "" {
				s.Privilege = "ACCOUNT"
			}
			if s.Workdir == "" {
				s.Workdir = "."
			}
			if s.TimeoutSeconds == 0 {
				s.TimeoutSeconds = 600
			}
			if !validName(s.Name) || strings.TrimSpace(s.Command) == "" || !applicationText(s.Command, 8192) || !applicationRelative(s.Workdir) || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 7200 || s.Privilege != "ACCOUNT" && s.Privilege != "ROOT" {
				return errors.New("请核对步骤名称、命令、相对目录、权限和超时（最多7200秒）")
			}
		}
		h := &m.Runtime.Health
		if h.Kind == "" {
			h.Kind = "NONE"
		}
		if h.Kind != "NONE" && h.Kind != "HTTP" && h.Kind != "TCP" && h.Kind != "COMMAND" || h.Port < 0 || h.Port > 65535 || h.TimeoutSeconds < 0 || h.TimeoutSeconds > 600 || !applicationText(h.Path, 240) || !applicationText(h.Command, 8192) || strings.ContainsAny(h.Path, "\r\n") {
			return errors.New("健康检查配置不正确")
		}
		if h.Kind == "HTTP" && h.Path != "" && !strings.HasPrefix(h.Path, "/") {
			return errors.New("HTTP健康检查路径须以 / 开头")
		}
		modules[m.ID] = true
	}
	names = map[string]bool{}
	for i := range p.Environments {
		e := &p.Environments[i]
		if e.Deployments == nil {
			e.Deployments = []applicationDeployment{}
		}
		e.Name = strings.TrimSpace(e.Name)
		if err := assign(&e.ID); err != nil {
			return err
		}
		if !validName(e.Name) || names[strings.ToLower(e.Name)] || len(e.Deployments) > 40 {
			return errors.New("环境名称不能为空或重复，每环境最多40个模块配置")
		}
		names[strings.ToLower(e.Name)] = true
		seenModules, ports := map[string]bool{}, map[string]bool{}
		for j := range e.Deployments {
			d := &e.Deployments[j]
			if d.Ports == nil {
				d.Ports = []applicationPort{}
			}
			if d.Variables == nil {
				d.Variables = []applicationVariable{}
			}
			if d.Mounts == nil {
				d.Mounts = []applicationMount{}
			}
			if err := assign(&d.ID); err != nil {
				return err
			}
			if !modules[d.ModuleID] || seenModules[d.ModuleID] {
				return errors.New("每环境的模块配置必须属于本项目且不能重复")
			}
			seenModules[d.ModuleID] = true
			if d.HostID != "" && !deploy.ID.MatchString(d.HostID) || d.BindAddress != "" && net.ParseIP(d.BindAddress) == nil || d.MemoryMiB < 0 || d.MemoryMiB > 1048576 || len(d.Ports) > 30 || len(d.Variables) > 100 || len(d.Mounts) > 30 {
				return errors.New("部署主机、监听地址、内存或配置数量不正确")
			}
			for k := range d.Ports {
				port := &d.Ports[k]
				if port.Protocol == "" {
					port.Protocol = "TCP"
				}
				port.Protocol = strings.ToUpper(port.Protocol)
				if port.HostPort < 1 || port.HostPort > 65535 || port.ContainerPort < 1 || port.ContainerPort > 65535 || port.Protocol != "TCP" && port.Protocol != "UDP" {
					return errors.New("请填写有效端口和TCP/UDP协议")
				}
				key := fmt.Sprintf("%s:%s:%d", d.HostID, port.Protocol, port.HostPort)
				if d.HostID != "" && ports[key] {
					return errors.New("同一环境主机的端口配置重复")
				}
				if d.HostID != "" {
					ports[key] = true
				}
			}
			variables := map[string]bool{}
			for _, v := range d.Variables {
				if !applicationVariableName.MatchString(v.Name) || variables[v.Name] || !applicationText(v.Value, 4096) {
					return errors.New("环境变量名称重复、格式错误或值过长")
				}
				variables[v.Name] = true
				if (v.CredentialID == "") != (v.CredentialRevision == "") || v.CredentialID != "" && (!deploy.ID.MatchString(v.CredentialID) || v.Value != "") {
					return errors.New("变量请填写普通值或选择凭据，不能同时填写")
				}
				if v.CredentialID == "" && v.Value != "" && (applicationSensitiveName.MatchString(v.Name) || applicationSecretValue(v.Value)) {
					return errors.New("密码、Token等敏感变量请使用凭据引用")
				}
			}
			mountTargets := map[string]bool{}
			for _, m := range d.Mounts {
				if !applicationAbsolute(m.Source) || !applicationAbsolute(m.Target) || mountTargets[m.Target] {
					return errors.New("挂载须使用规范的绝对目录，容器目录不能重复")
				}
				mountTargets[m.Target] = true
			}
			l := &d.Logs
			if l.Kind != "" && l.Kind != "FILE" && l.Kind != "DOCKER" && l.Kind != "JOURNAL" || l.Directory != "" && !applicationAbsolute(l.Directory) || !applicationText(l.Pattern, 120) || strings.ContainsAny(l.Pattern, "/\\\r\n") || l.RetentionDays < 0 || l.RetentionDays > 3650 || l.MaxMiB < 0 || l.MaxMiB > 1048576 {
				return errors.New("日志来源、目录、文件规则或保留范围不正确")
			}
		}
	}
	return nil
}

func validateApplicationReferences(tx *sql.Tx, p applicationProject) error {
	accounts := map[string]gitAccount{}
	for _, r := range p.Repositories {
		if r.GitAccountID == "" {
			continue
		}
		a, err := scanGitAccount(tx.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", r.GitAccountID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return applicationInvalid("所选Git账号不存在，请刷新")
			}
			return err
		}
		endpoint, err := applicationRepositoryEndpoint(r.URL)
		if err != nil {
			return err
		}
		if endpoint.Host != "" && (!strings.EqualFold(endpoint.Host, a.SSHHost) || endpoint.Method != a.AuthMethod) {
			return applicationInvalid("仓库地址与Git账号的服务地址或认证方式不匹配")
		}
		if endpoint.Host != "" && endpoint.Method == "HTTPS" && (endpoint.Scheme != "https" || endpoint.Port != a.SSHPort) {
			return applicationInvalid("HTTPS凭据需要HTTPS仓库地址，端口须与Git账号一致")
		}
		if endpoint.Method == "SSH" && (endpoint.Port != 0 && endpoint.Port != a.SSHPort || endpoint.User != "" && endpoint.User != a.SSHUser) {
			return applicationInvalid("SSH仓库的登录账号或端口与Git账号不匹配")
		}
		accounts[r.ID] = a
	}
	checkHost := func(id string) error {
		if id == "" {
			return nil
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM targets WHERE id=?", id).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return applicationInvalid("所选主机不存在，请刷新")
		}
		return nil
	}
	checkSoftware := func(id, host string) error {
		if id == "" {
			return nil
		}
		if host == "" {
			return applicationInvalid("选择运行环境前请先选择对应主机")
		}
		var target, mode, status, jobKind, jobStatus string
		err := tx.QueryRow("SELECT s.host_id,s.management_mode,s.installation_status,coalesce(j.kind,''),coalesce(j.status,'') FROM software_instances s LEFT JOIN jobs j ON j.id=s.last_job_id WHERE s.id=?", id).Scan(&target, &mode, &status, &jobKind, &jobStatus)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil || mode == "RELEASED" || status == "UNINSTALLED" {
			return applicationInvalid("所选运行环境不存在或已解除管理")
		}
		if host != "" && target != host {
			return applicationInvalid("所选运行环境与主机不匹配")
		}
		if (jobKind == "SOFTWARE_UNINSTALL" || jobKind == "SOFTWARE_UNMANAGE" || jobKind == "PLUGIN_REMOVE") && (jobStatus == "QUEUED" || jobStatus == "RUNNING" || jobStatus == "UNKNOWN") {
			return applicationInvalid("所选运行环境正在移除或结果待确认，请先核对原操作")
		}
		return nil
	}
	for _, m := range p.Modules {
		if err := checkHost(m.Build.HostID); err != nil {
			return err
		}
		if err := checkSoftware(m.Build.SoftwareID, m.Build.HostID); err != nil {
			return err
		}
		if a, ok := accounts[m.RepositoryID]; ok && (m.Build.HostID != "" && a.HostID != m.Build.HostID || m.Build.Account != "" && a.SystemAccount != m.Build.Account) {
			return applicationInvalid("构建主机及系统账号须与所选Git账号一致")
		}
	}
	for _, e := range p.Environments {
		for _, d := range e.Deployments {
			if err := checkHost(d.HostID); err != nil {
				return err
			}
			if err := checkSoftware(d.RuntimeSoftwareID, d.HostID); err != nil {
				return err
			}
			for _, v := range d.Variables {
				if v.CredentialID != "" {
					var count int
					err := tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND revoked_at IS NULL AND kind IN ('PASSWORD','TOKEN')", v.CredentialID, v.CredentialRevision).Scan(&count)
					if err != nil {
						return err
					}
					if count != 1 {
						return applicationInvalid("所选密码或Token凭据修订不存在或已撤销")
					}
				}
			}
		}
	}
	return nil
}
