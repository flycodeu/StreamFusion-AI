package controller

import (
	"fmt"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// The host-specific wrapper reuses the existing installer and executor commands.
// No credentials are packaged. Identifiers and paths have already been validated.
func targetInstaller(p hostPreparation, helper string) string {
	if p.UpdateFromSHA != "" {
		return targetUpdateInstaller(p)
	}
	marker := "SFOPS_TARGET_INSTALLER_" + p.Package.SHA256
	return strings.NewReplacer(
		"@PREPARE_TERMINAL@", bundledTerminalStep(p),
		"@ROOT@", p.InstallRoot, "@CONTROLLER@", p.ControllerID,
		"@TARGET@", p.TargetID, "@ACCOUNT@", p.Account, "@SHA@", p.Package.SHA256,
		"@REPAIR_SHA@", p.RepairSHA,
		"@EMPTY_ROOT@", controllerScript("preparation/empty-root.sh"),
		"@COMMAND_PUBLIC_KEY@", p.CommandPublicKey,
		"@HELPER_MARKER@", marker, "@HELPER@", helper,
	).Replace(controllerScript("preparation/install.sh.tmpl"))
}

// Automatic maintenance never initializes missing directories or edits sudo
// policy. The shipped binary verifies ownership and takes the target lock.
func targetUpdateInstaller(p hostPreparation) string {
	return strings.NewReplacer("@PREPARE_TERMINAL@", bundledTerminalStep(p), "@ROOT@", p.InstallRoot, "@CONTROLLER@", p.ControllerID, "@TARGET@", p.TargetID, "@OLD@", p.UpdateFromSHA, "@NEW@", p.Package.SHA256).Replace(controllerScript("preparation/update.sh.tmpl"))
}

func bundledTerminalStep(p hostPreparation) string {
	if !p.IncludeHostTerminal {
		return ""
	}
	return strings.Replace(controllerScript("preparation/bundled-terminal.sh.tmpl"), "@INSTALLER@", shellQuote(serviceinstall.HostTerminalInstaller()), 1)
}

func targetSetupReadme(p hostPreparation) string {
	text := fmt.Sprintf(controllerScript("preparation/readme.md.tmpl"), p.InstallRoot, p.Account, p.ControllerID, p.TargetID)
	if p.IncludeHostTerminal {
		text += "\nUbuntu 24.04 接入时还会安装 mysql、redis-cli、psql、curl 和 sf-docker。缺少客户端时，主机须能访问自己的 APT 源或本地镜像。其他 Linux 系统只准备接入组件。\n"
	}
	return text
}
