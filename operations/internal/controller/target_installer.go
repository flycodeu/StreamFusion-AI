package controller

import (
	"fmt"
	"strings"
)

// The host-specific wrapper reuses the existing installer and executor commands.
// No credentials are packaged. Identifiers and paths have already been validated.
func targetInstaller(p hostPreparation, helper string) string {
	marker := "SFOPS_TARGET_INSTALLER_" + p.Package.SHA256
	return strings.NewReplacer(
		"@ROOT@", p.InstallRoot, "@CONTROLLER@", p.ControllerID,
		"@TARGET@", p.TargetID, "@ACCOUNT@", p.Account, "@SHA@", p.Package.SHA256,
		"@HELPER_MARKER@", marker, "@HELPER@", helper,
	).Replace(`#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
automatic=false
preview=false
if [[ ${1:-} == --non-interactive && $# == 1 ]]; then automatic=true
elif [[ ${1:-} == --preview && $# == 1 ]]; then preview=true
elif [[ $# != 0 ]]; then echo '用法: bash install.sh'; exit 1; fi
fail(){ printf '安装未完成: %s\n' "$*" >&2; exit 1; }
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || fail '此包适用于 Linux amd64，请获取与服务器架构匹配的安装包。'
command -v sha256sum >/dev/null || fail '缺少 sha256sum，请联系服务器管理员。'
sha256sum -c SHA256SUMS --status || fail '文件摘要不匹配，请重新上传完整安装包。'
[[ $(sha256sum ./streamfusion-ops | cut -d' ' -f1) == '@SHA@' ]] || fail '执行器摘要与可信材料不一致。'
printf '\nStreamFusion Ops 主机接入\n系统架构: Linux amd64\n连接账号: @ACCOUNT@\n安装目录: @ROOT@\n'
echo '安装程序会自动检查已有归属，完成首次接入、同归属升级或授权修复。'
echo '接入后由 Ops 通过 SSH 按需调用，无需启动额外的常驻服务。'
$preview && exit 0
if [[ $EUID != 0 ]]; then
  $automatic && fail '在线安装需要 root 或 sudo 权限。'
  command -v sudo >/dev/null || fail '需要管理员权限，请使用 root 运行 bash install.sh。'
  echo '安装需要管理员权限，接下来可能提示输入当前账号的 sudo 密码。'
  exec sudo -- bash "$PWD/install.sh"
fi
if ! $automatic; then
  read -r -p '确认开始安装？[y/N] ' answer
  [[ $answer == y || $answer == Y ]] || { echo '已取消。'; exit 0; }
fi
chmod 0755 ./streamfusion-ops
helper=$(mktemp)
trap 'rm -f -- "$helper"' EXIT
cat > "$helper" <<'@HELPER_MARKER@'
@HELPER@
@HELPER_MARKER@
args=(--binary ./streamfusion-ops --sha256 '@SHA@' --root '@ROOT@' --controller-id '@CONTROLLER@' --target-id '@TARGET@' --account '@ACCOUNT@')
if [[ -e '@ROOT@' || -L '@ROOT@' ]]; then
  ./streamfusion-ops target-verify --root '@ROOT@' --controller-id '@CONTROLLER@' --target-id '@TARGET@' || fail '已有目录不属于当前主机或目录不安全，已保留原文件；请在 Ops 核对主机归属。'
  # Validate the exact sudo policy before changing an existing executor.
  bash "$helper" "${args[@]}" --authorize-only
  if [[ $(sha256sum '@ROOT@/executor' | cut -d' ' -f1) != '@SHA@' ]]; then
    ./streamfusion-ops target-upgrade --binary "$PWD/streamfusion-ops" --sha256 '@SHA@' --root '@ROOT@' --controller-id '@CONTROLLER@' --target-id '@TARGET@' || fail '升级未完成，请先在 Ops 核对未决作业。'
  fi
  bash "$helper" "${args[@]}" --authorize-only --apply
else
  bash "$helper" "${args[@]}" --apply
fi
./streamfusion-ops target-verify --root '@ROOT@' --controller-id '@CONTROLLER@' --target-id '@TARGET@'
printf '\n安装完成。请回到 Ops 点击“检查接入结果”，然后选择软件进行安装。\n'
echo 'SFOPS_SETUP_READY'
`)
}

func targetSetupReadme(p hostPreparation) string {
	return fmt.Sprintf(`# StreamFusion Ops 离线接入

1. 将本包上传到目标服务器当前账号的主目录（例如 ~），通过文件管理器解压。
2. 进入解压目录，运行一条命令：

    bash install.sh

3. 按提示输入 sudo 密码并确认安装；完成后回到 Ops 点击“检查接入结果”。

本包仅支持 Linux amd64（x86_64）。首次接入、同归属升级和授权修复自动识别。
接入组件按需运行，无额外常驻服务。目标服务器无外网也可完成本包的安装。
此包只准备主机接入；MySQL 等软件的离线材料请在 Ops 的软件安装流程中另行选择。

安装目录: %s
连接账号: %s
控制器ID: %s
主机ID: %s

SHA256SUMS 用于检查传输完整性，安装包须通过可信 Ops 页面取得。
拒绝覆盖其他主机归属、未知已有目录或升级中的未决作业；遇到错误请保留目录并返回 Ops 核对。
`, p.InstallRoot, p.Account, p.ControllerID, p.TargetID)
}
