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
	"EXECUTOR_MISSING":                 "受管执行器尚未安装，请在安装准备中下载材料并初始化",
	"EXECUTOR_NOT_EXECUTABLE":          "受管执行器不可执行，请核对文件权限及Linux架构",
	"EXECUTOR_INITIALIZATION_REQUIRED": "执行器尚未完成受管初始化，请核对安装准备；不要覆盖已有目录",
	"EXECUTOR_PRIVILEGE_REQUIRED":      "执行器没有所需的Linux root身份，请核对受限sudo授权或主机类型",
	"EXECUTOR_PATH_UNSAFE":             "执行器路径不安全，请核对受管目录及符号链接",
	"SUDO_UNAVAILABLE":                 "目标未提供sudo；请由管理员完成受限授权，root连接可直接执行",
	"SUDO_AUTHORIZATION_REQUIRED":      "连接账号未获受限免交互授权，请按安装准备中的固定策略授权",
	"EXECUTOR_IDENTITY_MISMATCH":       "执行器归属不匹配，请核对控制器、主机ID和受管目录",
	"EXECUTOR_UPGRADE_REQUIRED":        "执行器版本或协议不匹配，请按安装准备升级后重新检查",
	"EXECUTOR_RESPONSE_INVALID":        "执行器响应格式不正确，请升级匹配版本并检查SSH登录输出",
	"EXECUTOR_CALL_FAILED":             "执行器调用失败，请在安装准备中核对文件、受限授权和版本",
	"EXECUTOR_CHECK_TIMEOUT":           "执行器检查超时，请检查目标负载、连接及受管程序",
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
	switch {
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
	authorization := ""
	if len(rootUser) == 0 || !rootUser[0] {
		authorization = "elif [ ! -x /usr/bin/sudo ]; then printf 'SFOPS_EXECUTOR_V1\\nSUDO_MISSING\\n'; elif ! /usr/bin/sudo -n -l -- " + file + " target-exec >/dev/null 2>&1; then printf 'SFOPS_EXECUTOR_V1\\nAUTHORIZATION_REQUIRED\\n'; "
	}
	return "/bin/sh -c \"if [ -L " + file + " ]; then printf 'SFOPS_EXECUTOR_V1\\nUNSAFE\\n'; elif [ ! -e " + file + " ]; then printf 'SFOPS_EXECUTOR_V1\\nMISSING\\n'; elif [ ! -f " + file + " ] || [ ! -x " + file + " ]; then printf 'SFOPS_EXECUTOR_V1\\nNOT_EXECUTABLE\\n'; " + authorization + "else printf 'SFOPS_EXECUTOR_V1\\nPRESENT\\n'; fi\""
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
