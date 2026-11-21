package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const hostTerminalPhase = "SERVICE_HOST_TERMINAL"
const hostTerminalReceipt = "host-terminal-ready.json"

var terminalInstallFailureMessages = map[string]string{
	"TERMINAL_TOOLS_PLATFORM":       "请核对主机的 /etc/os-release 和安装条件。",
	"TERMINAL_TOOLS_LAUNCHER":       "请检查 /usr/local/bin 的权限及已有 sf-docker 文件。",
	"TERMINAL_TOOLS_DPKG":           "请运行 dpkg --audit 检查未完成的软件包操作。",
	"TERMINAL_TOOLS_APT_UPDATE":     "请运行 sudo apt-get update 检查主机软件源、签名和网络。",
	"TERMINAL_TOOLS_APT_PLAN":       "请核对软件源是否提供兼容的客户端及其依赖。",
	"TERMINAL_TOOLS_APT_INSTALL":    "请检查 APT 安装日志和 dpkg 状态后重试。",
	"TERMINAL_TOOLS_VERIFY":         "请检查是否有其他终端工具作业在运行，以及主机客户端和 sf-docker 是否可执行。",
	"TERMINAL_TOOLS_INSTALL_FAILED": "请检查主机磁盘空间及软件包状态后重试。",
}

// Package-manager diagnostics may contain authenticated repository URLs. Only
// the installer's fixed markers cross the process boundary.
const terminalInstallCommand = `set -o pipefail
/bin/bash -c "$1" 2>&1 | /usr/bin/awk '
  /^SFOPS_TERMINAL_STAGE=(PLATFORM|LAUNCHER|DPKG|APT_UPDATE|APT_PLAN|APT_INSTALL|VERIFY)$/ {print}
  {ready=($0=="SFOPS_TERMINAL_READY")}
  END {if (ready) print "SFOPS_TERMINAL_READY"}'
`

func (x *executor) prepareInstallationTerminal(req Request, dir string) string {
	receipt := filepath.Join(dir, hostTerminalReceipt)
	var prepared string
	if err := readJSON(receipt, &prepared); err == nil && prepared == req.InputHash {
		return "" // A resumed material transfer belongs to the same signed job.
	} else if !os.IsNotExist(err) {
		return "SERVICE_INSTALL_RECEIPT_MISMATCH"
	}
	if err := x.checkpoint(req, hostTerminalPhase); err != nil {
		return "TERMINAL_TOOLS_INSTALL_FAILED"
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	probeCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	state, err := x.softwareRunner(probeCtx, "/usr/bin/env", "/bin/bash", "-c", serviceinstall.HostTerminalProbe(serviceinstall.HostTerminalLauncherSHA()))
	stop()
	if err != nil {
		return "TERMINAL_TOOLS_VERIFY"
	}
	switch strings.TrimSpace(string(state)) {
	case "SFOPS_TERMINAL_READY":
		x.appendServiceLog(req, hostTerminalPhase, "主机终端工具已就绪")
	case "SFOPS_TERMINAL_NOT_APPLICABLE":
		x.appendServiceLog(req, hostTerminalPhase, "此发行版不适用 Ubuntu 24.04 客户端，继续安装组件")
	case "SFOPS_TERMINAL_MISSING":
		x.appendServiceLog(req, hostTerminalPhase, "正在准备宿主机客户端和 Docker 终端入口")
		installCtx, stop := context.WithTimeout(ctx, 13*time.Minute)
		out, err := x.softwareRunner(installCtx, "/usr/bin/env", "/bin/bash", "-c", terminalInstallCommand, "host-terminal", serviceinstall.HostTerminalInstaller())
		stop()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if err != nil || lines[len(lines)-1] != "SFOPS_TERMINAL_READY" {
			code := "TERMINAL_TOOLS_INSTALL_FAILED"
			for _, line := range lines {
				stage := strings.TrimPrefix(line, "SFOPS_TERMINAL_STAGE=")
				switch stage {
				case "PLATFORM", "LAUNCHER", "DPKG", "APT_UPDATE", "APT_PLAN", "APT_INSTALL", "VERIFY":
					code = "TERMINAL_TOOLS_" + stage
				}
			}
			return code
		}
		x.appendServiceLog(req, hostTerminalPhase, "主机终端工具已就绪")
	default:
		return "TERMINAL_TOOLS_VERIFY"
	}
	if err := atomicJSON(receipt, req.InputHash); err != nil {
		return "TERMINAL_TOOLS_INSTALL_FAILED"
	}
	return ""
}
