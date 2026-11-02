package controller

import (
	"errors"
	"path"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func validateInstallPreflightPaths(base string, plan *softwareInstallPlan, preflight serviceinstall.Preflight) error {
	if serviceinstall.IsNode(plan.Definition) {
		n := plan.Node
		if n == nil || n.Validate() != nil {
			return errors.New("NODE_CONTEXT_INVALID")
		}
		root := serviceinstall.NodeVersionRoot(*n, plan.Definition.Version)
		if preflight.Official != nil || preflight.Root != root || preflight.BinaryDirectory != root+"/bin" || preflight.DataDirectory != n.RootPath+"/cache/"+plan.Definition.Version || preflight.ConfigDirectory != n.RootPath+"/nvm/alias" || preflight.LogsDirectory != n.RootPath+"/backups/"+plan.Definition.Version {
			return errors.New("NODE_PATHS_INVALID")
		}
		if n.ConnectTerminal {
			if !deploy.Hex.MatchString(preflight.NodeTerminalHash) || n.TerminalHash != "" && n.TerminalHash != preflight.NodeTerminalHash {
				return errors.New("NODE_TERMINAL_CHANGED")
			}
			n.TerminalHash = preflight.NodeTerminalHash
		} else if preflight.NodeTerminalHash != "" {
			return errors.New("NODE_TERMINAL_UNEXPECTED")
		}
		return nil
	}
	if serviceinstall.IsOfficial(plan.Definition) {
		if plan.Source.Type != "OFFICIAL" || preflight.Official == nil || preflight.Official.Validate(plan.Definition) != nil {
			return errors.New("OFFICIAL_PLAN_INVALID")
		}
		paths := preflight.Official.Paths
		if preflight.Root != paths.Root || preflight.DataDirectory != paths.Data || preflight.ConfigDirectory != paths.Config || preflight.LogsDirectory != paths.Logs || preflight.BinaryDirectory != path.Dir(paths.Binary) {
			return errors.New("OFFICIAL_PATHS_INVALID")
		}
		plan.Official = preflight.Official
		return nil
	}
	root := serviceinstall.InstallRoot(base, plan.SoftwareID, plan.Config)
	if preflight.Official != nil || preflight.Root != root || preflight.DataDirectory != root+"/data" || preflight.ConfigDirectory != root+"/config" {
		return errors.New("INSTALL_PREFLIGHT_MISMATCH")
	}
	return nil
}
