package controller

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// executorFailureCode is the API boundary for target-invocation failures. It
// deliberately never returns an error string supplied by SSH or a JSON parser.
func executorFailureCode(err error) string {
	if err == nil {
		return ""
	}
	code := err.Error()
	if _, known := commandErrorMessages[code]; known {
		return code
	}
	if _, known := executorFailureMessages[code]; known {
		return code
	}
	if code == "LINUX_TARGET_REQUIRED" || code == "TARGET_PATH_INVALID" || code == "CREDENTIAL_UNAVAILABLE" {
		return code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "EXECUTOR_CHECK_TIMEOUT"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		classified, _ := hostSSHError(err)
		return classified
	}
	if strings.Contains(code, "SSH_HOST_KEY_CHANGED") || strings.Contains(code, "CREDENTIAL_") || strings.Contains(code, "unable to authenticate") || strings.Contains(code, "timeout") {
		classified, _ := hostSSHError(err)
		return classified
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.HasPrefix(code, "json:") || strings.Contains(code, "invalid character") || code == "JSON_TRAILING_DATA" {
		return "EXECUTOR_RESPONSE_INVALID"
	}
	return "EXECUTOR_CALL_FAILED"
}

var executorFailureMessages = map[string]string{
	"COMMAND_TRUST_REQUIRED":           "此主机尚未授权命令模板，请在主机安装准备中更新授权后重试",
	"COMMAND_SIGNATURE_INVALID":        "命令签名校验失败，请核对主机与控制器归属",
	"SOFTWARE_IDENTITY_DRIFT":          "服务身份与登记记录不一致，请重新检测并核对服务，不读取其他服务的日志",
	"SOFTWARE_OWNERSHIP_MISMATCH":      "服务受管归属与安装记录不一致，请核对原安装记录和实际服务",
	"SOFTWARE_LOG_READ_DENIED":         "服务身份已核对，但日志命令未成功，请检查日志读取条件与服务状态",
	"EXECUTOR_MISSING":                 "受管执行器尚未安装，请在安装准备中下载材料并初始化",
	"EXECUTOR_NOT_EXECUTABLE":          "受管执行器不可执行，请核对文件权限及Linux架构",
	"EXECUTOR_INITIALIZATION_REQUIRED": "执行器尚未完成受管初始化，请核对安装准备；不要覆盖已有目录",
	"EXECUTOR_PRIVILEGE_REQUIRED":      "安装操作需要管理员权限，请通过当前 SSH 账号完成 sudo 授权",
	"EXECUTOR_PATH_UNSAFE":             "接入目录不满足安全要求，请检查符号链接、所有者和写入权限",
	"EXECUTOR_PATH_UNREADABLE":         "当前 SSH 账号无法访问接入目录或读取执行器，请在安装准备中恢复接入权限",
	"SUDO_UNAVAILABLE":                 "目标未提供sudo；请由管理员完成受限授权，root连接可直接执行",
	"SUDO_AUTHORIZATION_REQUIRED":      "连接账号未获受限免交互授权，请按安装准备中的固定策略授权",
	"EXECUTOR_IDENTITY_MISMATCH":       "执行器归属不匹配，请核对控制器、主机ID和受管目录",
	"EXECUTOR_UPGRADE_REQUIRED":        "执行器版本或协议不匹配，请按安装准备升级后重新检查",
	"NATIVE_INSTALL_UPGRADE_REQUIRED":  "目标执行器尚未提供当前原生安装能力，请在主机安装准备中升级执行器后重新预检",
	"INSTALL_RUNTIME_UNSUPPORTED":      "所选运行方式不受支持，请从当前配方支持的Docker或原生方式中选择",
	"SERVICE_RECIPE_UNTRUSTED":         "接入组件尚不支持所选配方，请升级执行器后重新预检",
	"SERVICE_INSTALL_INPUT_INVALID":    "接入组件未接受所选配方或安装参数，请升级执行器后重新预检",
	"EXECUTOR_RESPONSE_INVALID":        "执行器响应格式不正确，请升级匹配版本并检查SSH登录输出",
	"EXECUTOR_CALL_FAILED":             "执行器调用失败，请在安装准备中核对文件、受限授权和版本",
	"EXECUTOR_CHECK_TIMEOUT":           "执行器检查超时，请检查目标负载、连接及受管程序",
	"EXECUTOR_CHECK_EXPIRED":           "检查请求已过期，请核对控制器与目标服务器的系统时间后重试",
	"INSTALL_PLATFORM_UNSUPPORTED":     "安装配方要求Linux amd64；原生配方还需通过Ubuntu24.04依赖预检",
	"DOCKER_ENGINE_UNAVAILABLE":        "Docker Engine不可访问，请检查安装、运行状态及固定socket",
	"COMPOSE_UNAVAILABLE":              "Docker Compose不可用，仅依赖Compose的发布需要准备它",
}

// Only stable classifications leave this boundary. Target stderr may contain
// arbitrary program output and must never be copied into API errors or audits.
func classifyExecutorInvocation(err error, stderr string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("EXECUTOR_CHECK_TIMEOUT")
	}
	if strings.TrimSpace(stderr) == "DEADLINE_EXCEEDED" {
		return errors.New("EXECUTOR_CHECK_EXPIRED")
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) && filepath.Base(pathError.Path) == "executor" {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("EXECUTOR_MISSING")
		}
		if errors.Is(err, os.ErrPermission) {
			return errors.New("EXECUTOR_NOT_EXECUTABLE")
		}
	}
	text := strings.ToLower(stderr)
	// Account checks return fixed classifications only. A diagnostic containing
	// another line (including command output or secrets) must not pass through.
	if fixed := strings.TrimSpace(stderr); dockerAccessMessages[fixed] != "" {
		return errors.New(fixed)
	}
	if fixed := strings.TrimSpace(stderr); strings.HasPrefix(fixed, "GIT_") && gitMessages[fixed] != "" {
		return errors.New(fixed)
	}
	switch {
	case strings.TrimSpace(text) == "software_identity_drift":
		return errors.New("SOFTWARE_IDENTITY_DRIFT")
	case strings.TrimSpace(text) == "software_ownership_mismatch":
		return errors.New("SOFTWARE_OWNERSHIP_MISMATCH")
	case strings.TrimSpace(text) == "software_log_read_denied":
		return errors.New("SOFTWARE_LOG_READ_DENIED")
	case strings.TrimSpace(text) == "service_recipe_untrusted":
		return errors.New("SERVICE_RECIPE_UNTRUSTED")
	case strings.TrimSpace(text) == "service_install_input_invalid":
		return errors.New("SERVICE_INSTALL_INPUT_INVALID")
	case strings.Contains(text, "linux_executor_required") || strings.Contains(text, "linux_root_required"):
		return errors.New("EXECUTOR_PRIVILEGE_REQUIRED")
	case strings.Contains(text, "executor_install_required") || strings.Contains(text, "owner.json") && (strings.Contains(text, "not found") || strings.Contains(text, "no such file")):
		return errors.New("EXECUTOR_INITIALIZATION_REQUIRED")
	case strings.Contains(text, "executor_identity_mismatch") || strings.Contains(text, "target_owner_mismatch"):
		return errors.New("EXECUTOR_IDENTITY_MISMATCH")
	case strings.Contains(text, "unknown field") || strings.Contains(text, "envelope_invalid") || strings.Contains(text, "protocol_version") || strings.Contains(text, "unknown_action"):
		return errors.New("EXECUTOR_UPGRADE_REQUIRED")
	case strings.Contains(text, "a password is required") || strings.Contains(text, "not in the sudoers") || strings.Contains(text, "not allowed to execute") || strings.Contains(text, "may not run sudo") || strings.Contains(text, "a terminal is required") || strings.Contains(text, "no askpass"):
		return errors.New("SUDO_AUTHORIZATION_REQUIRED")
	case strings.Contains(text, "docker_unavailable") || strings.Contains(text, "docker_engine_unavailable"):
		return errors.New("DOCKER_ENGINE_UNAVAILABLE")
	case strings.Contains(text, "compose_unavailable"):
		return errors.New("COMPOSE_UNAVAILABLE")
	case strings.Contains(text, "sudo") && (strings.Contains(text, "not found") || strings.Contains(text, "no such file")) && !strings.Contains(text, "/executor"):
		return errors.New("SUDO_UNAVAILABLE")
	case strings.Contains(text, "/executor") && (strings.Contains(text, "not found") || strings.Contains(text, "no such file")):
		return errors.New("EXECUTOR_MISSING")
	case strings.Contains(text, "exec format error") || strings.Contains(text, "permission denied") || strings.Contains(text, "cannot execute"):
		return errors.New("EXECUTOR_NOT_EXECUTABLE")
	case strings.Contains(text, "path_unsafe") || strings.Contains(text, "path_not_protected") || strings.Contains(text, "symlink"):
		return errors.New("EXECUTOR_PATH_UNSAFE")
	default:
		return errors.New("EXECUTOR_CALL_FAILED")
	}
}

func (h *HTTP) executorFailure(ctx context.Context, t target, err error) (string, string) {
	if ctx.Err() != nil {
		return "EXECUTOR_CHECK_TIMEOUT", executorFailureMessages["EXECUTOR_CHECK_TIMEOUT"]
	}
	code := executorFailureCode(err)
	if code == "EXECUTOR_CALL_FAILED" {
		if state := h.executorFileState(ctx, t); state != "" {
			code = state
		}
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

func executorFileProbeCommand(root string, rootUser ...bool) string {
	file := "'" + root + "/executor'"
	directory := "'" + root + "'"
	authorization := ""
	if len(rootUser) == 0 || !rootUser[0] {
		authorization = controllerTemplate("observe/executor-authorization-probe.sh.tmpl", file)
	}
	return controllerTemplate("observe/executor-file-probe.sh.tmpl", directory, file, authorization)
}

// This fallback only inspects the configured path and asks sudo to list the
// exact permitted command. It never executes a privileged command or writes
// target files, and remains useful when sudo emits a localized error message.
func (h *HTTP) executorFileState(ctx context.Context, t target) string {
	state := ""
	if t.Kind == "SSH" {
		client, closeClient, err := h.store.connectSSH(ctx, t)
		if err != nil {
			return ""
		}
		defer closeClient()
		out, stderr := &limitedOutput{limit: 128}, &limitedOutput{limit: 128}
		if runSSHCommand(client, executorFileProbeCommand(t.InstallRoot, t.User == "root"), nil, out, stderr) != nil {
			return ""
		}
		parts := strings.Split(strings.TrimSpace(strings.ReplaceAll(out.String(), "\r\n", "\n")), "\n")
		if len(parts) == 2 && parts[0] == "SFOPS_EXECUTOR_V1" {
			state = parts[1]
		}
	} else {
		info, err := os.Lstat(filepath.Join(t.InstallRoot, "executor"))
		switch {
		case os.IsNotExist(err):
			state = "MISSING"
		case err != nil:
			return ""
		case info.Mode()&os.ModeSymlink != 0:
			state = "UNSAFE"
		case !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0:
			state = "NOT_EXECUTABLE"
		}
	}
	switch state {
	case "MISSING":
		return "EXECUTOR_MISSING"
	case "UNREADABLE":
		return "EXECUTOR_PATH_UNREADABLE"
	case "UNSAFE":
		return "EXECUTOR_PATH_UNSAFE"
	case "NOT_EXECUTABLE":
		return "EXECUTOR_NOT_EXECUTABLE"
	case "SUDO_MISSING":
		return "SUDO_UNAVAILABLE"
	case "AUTHORIZATION_REQUIRED":
		return "SUDO_AUTHORIZATION_REQUIRED"
	}
	return ""
}
