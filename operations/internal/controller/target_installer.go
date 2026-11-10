package controller

import (
	"fmt"
	"strings"
)

// The host-specific wrapper reuses the existing installer and executor commands.
// No credentials are packaged. Identifiers and paths have already been validated.
func targetInstaller(p hostPreparation, helper string) string {
	if p.UpdateFromSHA != "" {
		return targetUpdateInstaller(p)
	}
	marker := "SFOPS_TARGET_INSTALLER_" + p.Package.SHA256
	return strings.NewReplacer(
		"@ROOT@", p.InstallRoot, "@CONTROLLER@", p.ControllerID,
		"@TARGET@", p.TargetID, "@ACCOUNT@", p.Account, "@SHA@", p.Package.SHA256,
		"@REPAIR_SHA@", p.RepairSHA,
		"@COMMAND_PUBLIC_KEY@", p.CommandPublicKey,
		"@HELPER_MARKER@", marker, "@HELPER@", helper,
	).Replace(controllerScript("preparation/install.sh.tmpl"))
}

// Automatic maintenance never initializes missing directories or edits sudo
// policy. The shipped binary verifies ownership and takes the target lock.
func targetUpdateInstaller(p hostPreparation) string {
	return strings.NewReplacer("@ROOT@", p.InstallRoot, "@CONTROLLER@", p.ControllerID, "@TARGET@", p.TargetID, "@OLD@", p.UpdateFromSHA, "@NEW@", p.Package.SHA256).Replace(controllerScript("preparation/update.sh.tmpl"))
}

func targetSetupReadme(p hostPreparation) string {
	return fmt.Sprintf(controllerScript("preparation/readme.md.tmpl"), p.InstallRoot, p.Account, p.ControllerID, p.TargetID)
}
