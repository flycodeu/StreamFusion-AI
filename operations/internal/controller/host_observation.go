package controller

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type hostCapability struct {
	Status     string `json:"status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	ObservedAt string `json:"observedAt"`
	Source     string `json:"source"`
}

// Keep the complete additive CAPABILITIES contract in one strict decoder.
type executorCapabilities struct {
	FirewallVersion          int          `json:"firewallVersion"`
	Owner                    deploy.Owner `json:"owner"`
	Platform                 string       `json:"platform"`
	Docker                   bool         `json:"dockerAvailable"`
	DockerEngine             bool         `json:"dockerEngineAvailable"`
	DockerCompose            bool         `json:"dockerComposeAvailable"`
	SoftwareLifecycleVersion int          `json:"softwareLifecycleVersion"`
	InstallVersion           int          `json:"serviceInstallVersion"`
	CatalogHash              string       `json:"serviceCatalogHash"`
	NativeInstallVersion     int          `json:"nativeInstallVersion"`
	SoftwareConfigureVersion int          `json:"softwareConfigureVersion"`
	SoftwareUninstallVersion int          `json:"softwareUninstallVersion"`
	ToolProbeVersion         int          `json:"toolProbeVersion"`
}

type hostCapabilities struct {
	Reachability  hostCapability `json:"reachability"`
	ReadOnly      hostCapability `json:"readOnly"`
	Mutation      hostCapability `json:"mutation"`
	Docker        hostCapability `json:"docker"`
	DockerCompose hostCapability `json:"dockerCompose"`
	Platform      string         `json:"platform"`
	ExpiresAt     string         `json:"expiresAt"`
}

func unknownHostCapabilities() hostCapabilities {
	u := hostCapability{Status: "UNKNOWN", Code: "NOT_CHECKED", Message: "尚未检查"}
	return hostCapabilities{Reachability: u, ReadOnly: u, Mutation: u, Docker: u, DockerCompose: u}
}

// Fixed, read-only POSIX probe. User input is never inserted into this command;
// it does not source /etc/os-release, use sudo, write files or evaluate output.
const hostReadOnlyCommand = `/bin/sh -c 'printf "SFOPS_HOST_V1\n"; uname -s; uname -m; if command -v docker >/dev/null 2>&1; then printf "DOCKER_PRESENT\n"; else printf "DOCKER_MISSING\n"; fi'`

func checkedCapability(available bool, code, message string) hostCapability {
	status := "UNAVAILABLE"
	if available {
		status = "AVAILABLE"
	}
	return hostCapability{Status: status, Code: code, Message: message, ObservedAt: Now(), Source: hostCapabilitySource(code)}
}

func hostCapabilitySource(code string) string {
	switch {
	case strings.HasPrefix(code, "SSH_") || strings.HasPrefix(code, "CREDENTIAL_"):
		return "SSH"
	case strings.HasPrefix(code, "LOCAL_"):
		return "LOCAL"
	case strings.HasPrefix(code, "READ_ONLY_"):
		return "SSH_COMMAND"
	case strings.HasPrefix(code, "DOCKER_") || strings.HasPrefix(code, "COMPOSE_"):
		return "DOCKER"
	case strings.HasPrefix(code, "EXECUTOR_") || strings.HasPrefix(code, "LINUX_EXECUTOR_") || strings.HasPrefix(code, "SUDO_"):
		return "EXECUTOR"
	default:
		return "NONE"
	}
}

func hostSSHError(err error) (string, string) {
	s := err.Error()
	switch {
	case strings.Contains(s, "SSH_HOST_KEY_CHANGED"):
		return "SSH_HOST_KEY_CHANGED", "主机指纹已变化，请核对后编辑连接"
	case strings.Contains(s, "CREDENTIAL_") || strings.Contains(s, "SSH_KEY_INVALID"):
		return "CREDENTIAL_UNAVAILABLE", "连接凭据不可用，请检查凭据"
	case strings.Contains(s, "unable to authenticate"):
		return "SSH_AUTH_FAILED", "SSH认证失败，请检查用户与凭据"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(s, "timeout"):
		return "SSH_TIMEOUT", "SSH连接超时，请检查网络与端口"
	default:
		return "SSH_UNAVAILABLE", "SSH连接失败，请检查地址、端口和SSH服务"
	}
}

func (h *HTTP) observeHost(ctx context.Context, t target) hostCapabilities {
	facts := unknownHostCapabilities()
	facts.ExpiresAt = time.Now().UTC().Add(hostCapabilityFreshnessTTL).Format(time.RFC3339Nano)
	out := &limitedOutput{limit: 4096}
	errOut := &limitedOutput{limit: 1024}
	if t.Kind == "SSH" {
		client, closeClient, e := h.store.connectSSH(ctx, t)
		if e != nil {
			code, message := hostSSHError(e)
			facts.Reachability = checkedCapability(false, code, message)
			return facts
		}
		facts.Reachability = checkedCapability(true, "SSH_CONNECTED", "SSH认证与指纹核对通过")
		e = runSSHCommand(client, hostReadOnlyCommand, nil, out, errOut)
		closeClient()
		if e != nil {
			facts.ReadOnly = checkedCapability(false, "READ_ONLY_PROBE_FAILED", "SSH已连接，但只读系统观察未成功")
		} else {
			applyHostReadOnly(&facts, out.String())
		}
	} else {
		facts.Reachability = checkedCapability(true, "LOCAL_CONTROLLER", "本机控制器可访问")
		facts.Platform = runtime.GOOS + "/" + runtime.GOARCH
		facts.ReadOnly = checkedCapability(true, "LOCAL_OBSERVATION", "本机系统信息可读取")
		if runtime.GOOS != "linux" {
			facts.Mutation = checkedCapability(false, "LINUX_EXECUTOR_REQUIRED", "当前主机未提供Linux受管执行器")
			facts.Docker = hostCapability{Status: "UNKNOWN", Code: "DOCKER_UNCHECKED", Message: "当前执行方式尚未检查Docker能力", ObservedAt: Now(), Source: "DOCKER"}
			return facts
		}
	}
	var executor executorCapabilities
	if e := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &executor); e != nil {
		code, message := h.executorFailure(ctx, t, e)
		facts.Mutation = checkedCapability(false, code, message)
		if facts.Docker.Status == "UNKNOWN" {
			facts.Docker = hostCapability{Status: "UNKNOWN", Code: "DOCKER_ACCESS_UNCONFIRMED", Message: "Docker访问能力尚未确认", ObservedAt: Now(), Source: "DOCKER"}
		}
		return facts
	}
	if executor.Owner.ControllerID != h.store.controllerID() || executor.Owner.TargetID != t.ID || executor.Owner.ExecutorVersion != deploy.Version {
		facts.Mutation = checkedCapability(false, "EXECUTOR_IDENTITY_MISMATCH", "执行器归属不匹配，请检查受管目录")
		return facts
	}
	facts.Mutation = checkedCapability(true, "EXECUTOR_AUTHORIZED", "受管执行器身份与变更授权通过")
	if executor.DockerEngine || executor.Docker {
		facts.Docker = checkedCapability(true, "DOCKER_ENGINE_READY", "执行器可访问Docker Engine")
	} else {
		facts.Docker = checkedCapability(false, "DOCKER_ENGINE_UNAVAILABLE", "Docker Engine未安装、未运行或执行器无法访问")
	}
	if executor.DockerCompose || executor.Docker {
		facts.DockerCompose = checkedCapability(true, "DOCKER_COMPOSE_READY", "Docker Compose可用，支持应用发布")
	} else {
		facts.DockerCompose = checkedCapability(false, "COMPOSE_UNAVAILABLE", "Docker Compose不可用；仅影响依赖Compose的应用发布")
	}
	if executor.Platform != "" {
		facts.Platform = executor.Platform
	}
	return facts
}

func applyHostReadOnly(facts *hostCapabilities, output string) {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n")), "\n")
	if len(lines) != 4 || lines[0] != "SFOPS_HOST_V1" || len(lines[1]) > 64 || len(lines[2]) > 64 {
		facts.ReadOnly = checkedCapability(false, "READ_ONLY_RESPONSE_INVALID", "只读观察返回格式不正确")
		return
	}
	facts.ReadOnly = checkedCapability(true, "READ_ONLY_READY", "可读取系统信息")
	facts.Platform = strings.ToLower(lines[1]) + "/" + lines[2]
	if lines[3] == "DOCKER_MISSING" {
		facts.Docker = checkedCapability(false, "DOCKER_NOT_INSTALLED", "未检测到Docker")
	} else if lines[3] == "DOCKER_PRESENT" {
		facts.Docker = hostCapability{Status: "UNKNOWN", Code: "DOCKER_ACCESS_UNCONFIRMED", Message: "已安装Docker，执行器访问能力尚未确认", ObservedAt: Now(), Source: "DOCKER"}
	}
}

func (h *HTTP) checkHost(w http.ResponseWriter, r *http.Request, s *session, id string) {
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	t, e := h.store.readTarget(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	facts := h.observeHost(ctx, t)
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE targets SET capabilities_json=?,observed_at=? WHERE id=? AND CAST(version AS TEXT)=?", jsonText(facts), Now(), id, t.Version)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		h.fail(w, r, 409, "VERSION_CONFLICT", "检查期间主机已变化，请重新检查")
		return
	}
	if e = auditTx(tx, s.Username, "HOST_CHECKED", id, "SUCCESS", r.Context().Value(traceKey{}).(string)); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	t, e = h.store.readTarget(id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, t)
}

func (h *HTTP) acquireHostCheck(w http.ResponseWriter, r *http.Request) bool {
	select {
	case h.hostCheckSlots <- struct{}{}:
		return true
	default:
		h.fail(w, r, 429, "HOST_CHECK_BUSY", "主机检查已达并发上限，请稍后重试")
		return false
	}
}
