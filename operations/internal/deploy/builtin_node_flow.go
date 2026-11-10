package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"os"
	"path/filepath"
	"slices"
)

func (x *executor) runNodeFlowStep(ctx context.Context, req Request, step serviceinstall.CommandStep, n serviceinstall.NodeContext, action, version, pkg, material, def string) ([]byte, error) {
	return runNode(ctx, n, action, version, pkg, material, req.JobID, def, func(ctx context.Context, command string, args ...string) ([]byte, error) {
		marker := slices.Index(args, "sfops-node")
		if marker < 0 || len(args) != marker+9 {
			return nil, errors.New("NODE_COMMAND_INPUT_INVALID")
		}
		vars := map[string]string{}
		for index, key := range []string{"ACTION", "ROOT", "VERSION", "PACKAGE", "MATERIAL", "JOB", "DEFAULT", "CONFIGS"} {
			vars["OPS_NODE_"+key] = args[marker+1+index]
		}
		vars["NVM_DIR"] = n.RootPath + "/nvm"
		vars["NVM_NO_COLORS"] = "1"
		vars["NPM_CONFIG_USERCONFIG"] = vars["OPS_NODE_CONFIGS"] + "/user.npmrc"
		vars["NPM_CONFIG_GLOBALCONFIG"] = vars["OPS_NODE_CONFIGS"] + "/global.npmrc"
		vars["NPM_CONFIG_IGNORE_SCRIPTS"] = "true"
		vars["NPM_CONFIG_AUDIT"] = "false"
		vars["NPM_CONFIG_FUND"] = "false"
		vars["NPM_CONFIG_CACHE"] = n.RootPath + "/cache/" + version + "/npm"
		vars["NPM_CONFIG_REGISTRY"] = "https://registry.npmjs.org/"
		out, _, err := x.runBuiltinStep(ctx, req, step.ID, vars, nil, n.Account)
		return out, err
	})
}
func (x *executor) installNodeVersionFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	p := x.nodeInstallPreflight(*i)
	if !p.Ready {
		return result, errors.New(p.Checks[0].Code)
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	material := ""
	cleanup := func() {}
	defer func() { cleanup() }()
	before, err := x.observeNode(ctx, *i.Node, false)
	if err != nil {
		return result, err
	}
	def := i.Node.ExpectedDefault
	if i.Node.MakeDefault {
		def = i.Definition.Version
	}
	system := func(step serviceinstall.CommandStep) error {
		var err error
		switch step.Action {
		case "INSTALL_MATERIAL":
			material, cleanup, err = x.nodeMaterial(ctx, req, dir, i.Definition, i.Source)
			if cleanup == nil {
				cleanup = func() {}
			}
			return err
		case "NODE_PREPARE":
			if _, err = runNode(ctx, *i.Node, "prepare-install", i.Definition.Version, "", "", req.JobID, "", x.softwareRunner); err != nil {
				return err
			}
			// Read trusted archive metadata before changing the user's environment.
			extracted := filepath.Join(dir, "node-verified")
			if err = os.MkdirAll(extracted, 0700); err != nil {
				return err
			}
			if err = extractNativePart(ctx, i.Definition.Native.Parts[0], material, extracted); err != nil {
				return err
			}
			hash, err := fileSHA(filepath.Join(extracted, "bin/node"))
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(filepath.Join(extracted, "lib/node_modules/npm/package.json"))
			if err != nil {
				return err
			}
			var manifest struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(raw, &manifest) != nil || manifest.Version == "" {
				return errors.New("NODE_NPM_MANIFEST_INVALID")
			}
			marker := nodeInstallMarker{req.JobID, req.InputHash, hash, manifest.Version, *before}
			if i.Node.ConnectTerminal {
				terminal, plan, e := nodeTerminalFiles(*i.Node)
				if e != nil {
					return e
				}
				if terminal.Status == "CONFLICT" || terminal.Status == "UNSUPPORTED" {
					return errors.New("NODE_TERMINAL_" + terminal.Status)
				}
				if i.Node.TerminalHash != "" && i.Node.TerminalHash != Digest(terminal) {
					return errors.New("NODE_TERMINAL_CHANGED")
				}
				if e = atomicJSON(filepath.Join(dir, "node-terminal.json"), plan); e != nil {
					return e
				}
			}
			if err = atomicJSON(filepath.Join(dir, "node-install.json"), marker); err != nil {
				return err
			}
			return nil
		case "NODE_TERMINAL":
			if i.Node.ConnectTerminal {
				return x.connectNodeTerminal(ctx, *i.Node, req.JobID, dir)
			}
			return nil
		case "INSTALL_CONFIRM":
			result = x.reconcileNodeInstall(req, dir)
			if result.Status != "SUCCEEDED" {
				return errors.New("NODE_RESULT_UNCONFIRMED")
			}
			return nil
		}
		return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
	}
	err = x.runBuiltinFlow(ctx, req, []string{"INSTALL", "VERIFY"}, system, func(step serviceinstall.CommandStep) ([]byte, error) {
		return x.runNodeFlowStep(ctx, req, step, *i.Node, "install", i.Definition.Version, "", material, def)
	})
	if err != nil {
		if _, markerErr := os.Lstat(filepath.Join(dir, "node-install.json")); markerErr == nil {
			return failedBuiltinFlowResult(x.reconcileNodeInstall(req, dir), err), nil
		}
	}
	return result, err
}

func (x *executor) uninstallNodeVersionFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Uninstallation.Installation
	n := i.Node
	if !serviceinstall.IsNode(i.Definition) || !sameNodeOwner(n, req.Software.Ownership.Node) || x.nodeBound(req, *n) != nil {
		return result, errors.New("NODE_UNINSTALL_IDENTITY_MISMATCH")
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	clear := ""
	system := func(step serviceinstall.CommandStep) error {
		switch step.Action {
		case "UNINSTALL_PREPARE":
			before, err := x.observeNode(ctx, *n, false)
			if err != nil {
				return err
			}
			v := nodeVersion(before, i.Definition.Version)
			if v == nil || v.BinaryHash != req.Software.Ownership.BinaryHash || before.DefaultVersion != n.ExpectedDefault {
				return errors.New("NODE_ENVIRONMENT_DRIFT")
			}
			clear = ""
			if before.DefaultVersion == i.Definition.Version {
				if !req.Uninstallation.ClearNodeDefault || len(before.Versions) != 1 {
					return errors.New("NODE_DEFAULT_IN_USE")
				}
				clear = "clear"
			} else if req.Uninstallation.ClearNodeDefault {
				return errors.New("NODE_DEFAULT_CLEAR_INVALID")
			}
			if err = nodeVersionUnused(serviceinstall.NodeVersionRoot(*n, i.Definition.Version)); err != nil {
				return err
			}
			if err = atomicJSON(filepath.Join(dir, "node-uninstall.json"), nodeUninstallMarker{req.JobID, req.InputHash, *before}); err != nil {
				return err
			}
			return nil
		case "UNINSTALL_CONFIRM":
			result = x.reconcileNodeUninstall(req, dir)
			if result.Status != "SUCCEEDED" {
				return errors.New("NODE_RESULT_UNCONFIRMED")
			}
			return nil
		}
		return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
	}
	err := x.runBuiltinFlow(ctx, req, builtinFlowPhases(req), system, func(step serviceinstall.CommandStep) ([]byte, error) {
		action := "uninstall"
		option := clear
		if step.Phase == "DELETE_DATA" {
			action = "clean-version"
			option = ""
		}
		return x.runNodeFlowStep(ctx, req, step, *n, action, i.Definition.Version, "", "", option)
	})
	if err != nil {
		if _, markerErr := os.Lstat(filepath.Join(dir, "node-uninstall.json")); markerErr == nil {
			result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
			return result, nil
		}
	}
	return result, err
}

func (x *executor) applyNodeEnvironmentFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	n := req.NodeEnvironment
	p := x.nodePreflight(req)
	if !p.Ready {
		return result, errors.New(p.Checks[0].Code)
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	material := ""
	cleanup := func() {}
	defer func() { cleanup() }()
	action, pkg := "", ""
	switch n.Action {
	case "INITIALIZE":
		action = "initialize"
	case "UPDATE_NVM":
		action = "update-nvm"
	case "UPDATE_NPM":
		action, pkg = "package-install", "npm"
	case "INSTALL_PNPM", "UPDATE_PNPM":
		action, pkg = "package-install", "pnpm"
	case "REMOVE_PNPM":
		action, pkg = "package-remove", "pnpm"
	case "REMOVE_ENVIRONMENT":
		action = "remove-environment"
	default:
		return result, errors.New("NODE_ACTION_INVALID")
	}
	system := func(step serviceinstall.CommandStep) error {
		var err error
		switch step.Action {
		case "INSTALL_MATERIAL":
			d, e := nodeEnvironmentDefinition(*n)
			if e != nil {
				return e
			}
			material, cleanup, err = x.nodeMaterial(ctx, req, dir, d, n.Source)
			if cleanup == nil {
				cleanup = func() {}
			}
			return err
		case "NODE_PREPARE", "UNINSTALL_PREPARE":
			if err = atomicJSON(filepath.Join(dir, "node-before.json"), p.Observation); err != nil {
				return err
			}
			if err = atomicJSON(filepath.Join(dir, "node-change.json"), n); err != nil {
				return err
			}
			if n.Action == "REMOVE_ENVIRONMENT" && n.Context.ConnectTerminal {
				terminal := n.Context
				terminal.TerminalHash = Digest(p.Observation.Terminal)
				return x.changeNodeTerminal(ctx, terminal, req.JobID, dir, true)
			}
			return nil
		case "INSTALL_CONFIRM", "UNINSTALL_CONFIRM":
			if n.Action == "INITIALIZE" {
				if err = os.MkdirAll(filepath.Dir(x.nodeOwnerFile(n.Context.EnvironmentID)), 0700); err != nil {
					return err
				}
				if err = atomicJSON(x.nodeOwnerFile(n.Context.EnvironmentID), nodeOwner(req, n.Context)); err != nil {
					return err
				}
			}
			result = x.reconcileNodeEnvironment(req, dir)
			if result.Status != "SUCCEEDED" {
				return errors.New("NODE_RESULT_UNCONFIRMED")
			}
			return nil
		}
		return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
	}
	err := x.runBuiltinFlow(ctx, req, builtinFlowPhases(req), system, func(step serviceinstall.CommandStep) ([]byte, error) {
		return x.runNodeFlowStep(ctx, req, step, n.Context, action, n.NodeVersion, pkg, material, "")
	})
	if err != nil {
		if _, markerErr := os.Lstat(filepath.Join(dir, "node-change.json")); markerErr == nil {
			result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
			return result, nil
		}
	}
	return result, err
}
