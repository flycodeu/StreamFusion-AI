package deploy

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed node.sh
var nodeScript string

type nodeEnvironmentOwner struct{ ControllerID, TargetID, EnvironmentID, Account, AccountUID, RootPath string }

func (x *executor) nodeOwnerFile(id string) string {
	return filepath.Join(x.root, "node-environments", id+".json")
}
func nodeOwner(req Request, n serviceinstall.NodeContext) nodeEnvironmentOwner {
	return nodeEnvironmentOwner{req.ControllerID, req.TargetID, n.EnvironmentID, n.Account, n.AccountUID, n.RootPath}
}

func resolveNodeAccount(n serviceinstall.NodeContext, discovery bool) (serviceinstall.NodeContext, *user.User, error) {
	if !serviceinstall.NodeAccountPattern.MatchString(n.Account) || n.Account == "root" {
		return n, nil, errors.New("NODE_ACCOUNT_INVALID")
	}
	account, err := user.Lookup(n.Account)
	if err != nil || account.Uid == "0" || !strings.HasPrefix(account.HomeDir, "/home/") {
		return n, nil, errors.New("NODE_ACCOUNT_UNAVAILABLE")
	}
	if n.AccountUID != "" && n.AccountUID != account.Uid {
		return n, nil, errors.New("NODE_ACCOUNT_CHANGED")
	}
	n.AccountUID = account.Uid
	if n.EnvironmentVersion == "" {
		n.EnvironmentVersion = "1"
	}
	if n.NVMVersion == "" {
		n.NVMVersion = serviceinstall.NodeNVMVersion
	}
	if n.NVMHash == "" {
		n.NVMHash = serviceinstall.NodeNVMHash
	}
	managed := path.Join(account.HomeDir, ".local/share/streamfusion-ops/node", n.EnvironmentID)
	if discovery {
		if n.RootPath == "" {
			n.RootPath = path.Join(account.HomeDir, ".nvm")
		}
		if n.RootPath != path.Join(account.HomeDir, ".nvm") && n.RootPath != "/usr" && n.RootPath != managed {
			return n, nil, errors.New("NODE_DISCOVERY_PATH_INVALID")
		}
	} else {
		if n.RootPath == "" {
			n.RootPath = managed
		}
		if n.RootPath != managed || n.Validate() != nil {
			return n, nil, errors.New("NODE_ENVIRONMENT_INVALID")
		}
	}
	return n, account, nil
}

func nodeCommandArguments(n serviceinstall.NodeContext, home, action, version, pkg, material, job, def, configs string) []string {
	return []string{"-u", n.Account, "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=" + home, "LC_ALL=C", "/usr/bin/bash", "--noprofile", "--norc", "-c", nodeScript, "sfops-node", action, n.RootPath, version, pkg, material, job, def, configs}
}
func runNode(ctx context.Context, n serviceinstall.NodeContext, action, version, pkg, material, job, def string, run SoftwareCommandRunner) ([]byte, error) {
	_, account, err := resolveNodeAccount(n, action == "inspect-external" || action == "inspect-system")
	if err != nil {
		return nil, err
	}
	configs, err := os.MkdirTemp("/var/tmp", "sfops-node-config-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(configs)
	for _, name := range []string{"user.npmrc", "global.npmrc"} {
		file := filepath.Join(configs, name)
		if err = os.WriteFile(file, nil, 0444); err != nil {
			return nil, err
		}
		// The controller's restrictive umask must not make these empty,
		// non-secret files unreadable to the selected execution account.
		if err = os.Chmod(file, 0444); err != nil {
			return nil, err
		}
	}
	if err = os.Chmod(configs, 0755); err != nil {
		return nil, err
	}
	out, err := run(ctx, "/usr/sbin/runuser", nodeCommandArguments(n, account.HomeDir, action, version, pkg, material, job, def, configs)...)
	if len(out) >= 64<<10 {
		return nil, errors.New("NODE_OUTPUT_LIMIT")
	}
	if err != nil {
		return out, errors.New("NODE_COMMAND_FAILED")
	}
	return out, nil
}

func parseNodeObservation(n serviceinstall.NodeContext, out []byte) (*NodeEnvironmentObservation, error) {
	obs := &NodeEnvironmentObservation{EnvironmentID: n.EnvironmentID, Account: n.Account, AccountUID: n.AccountUID, RootPath: n.RootPath, ManagerType: "NVM", Versions: []NodeVersionObservation{}, ObservedAt: stamp()}
	indices := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		cols := strings.Split(line, "\t")
		switch cols[0] {
		case "ABSENT":
			if len(cols) != 1 {
				return nil, errors.New("NODE_OBSERVATION_INVALID")
			}
		case "NVM":
			if len(cols) != 4 {
				return nil, errors.New("NODE_OBSERVATION_INVALID")
			}
			obs.Exists = true
			obs.NVMVersion, obs.NVMHash, obs.NVMExecHash = cols[1], cols[2], cols[3]
		case "SYSTEM":
			obs.Exists = true
			obs.ManagerType = "SYSTEM"
		case "DEFAULT":
			if len(cols) != 2 || cols[1] != "" && !serviceinstall.NodeVersionPattern.MatchString(cols[1]) {
				return nil, errors.New("NODE_DEFAULT_UNSUPPORTED")
			}
			obs.DefaultVersion = cols[1]
		case "VERSION":
			if len(cols) != 6 || !serviceinstall.NodeVersionPattern.MatchString(cols[1]) || !Hex.MatchString(cols[3]) || len(obs.Versions) >= 64 {
				return nil, errors.New("NODE_OBSERVATION_INVALID")
			}
			if _, ok := indices[cols[1]]; ok {
				return nil, errors.New("NODE_VERSION_DUPLICATE")
			}
			expected := serviceinstall.NodeVersionRoot(n, cols[1]) + "/bin/node"
			if n.RootPath == "/usr" {
				expected = "/usr/bin/node"
			} else if strings.HasSuffix(n.RootPath, "/.nvm") {
				expected = path.Join(n.RootPath, "versions/node", cols[1], "bin/node")
			}
			if cols[2] != expected {
				return nil, errors.New("NODE_BINARY_PATH_MISMATCH")
			}
			indices[cols[1]] = len(obs.Versions)
			obs.Versions = append(obs.Versions, NodeVersionObservation{Version: cols[1], BinaryPath: cols[2], BinaryHash: cols[3], NPMVersion: cols[4], PNPMVersion: cols[5], GlobalPackages: []NodeGlobalPackage{}})
		case "PACKAGE":
			if len(cols) != 4 {
				return nil, errors.New("NODE_OBSERVATION_INVALID")
			}
			index, ok := indices[cols[1]]
			if !ok || len(obs.Versions[index].GlobalPackages) >= 256 {
				return nil, errors.New("NODE_PACKAGE_LIMIT")
			}
			obs.Versions[index].GlobalPackages = append(obs.Versions[index].GlobalPackages, NodeGlobalPackage{cols[2], cols[3]})
		default:
			return nil, errors.New("NODE_OBSERVATION_INVALID")
		}
	}
	sort.Slice(obs.Versions, func(i, j int) bool { return obs.Versions[i].Version < obs.Versions[j].Version })
	for i := range obs.Versions {
		sort.Slice(obs.Versions[i].GlobalPackages, func(a, b int) bool {
			return obs.Versions[i].GlobalPackages[a].Name < obs.Versions[i].GlobalPackages[b].Name
		})
	}
	frozen := *obs
	frozen.ObservedAt = ""
	obs.Fingerprint = Digest(frozen)
	return obs, nil
}

func (x *executor) observeNode(ctx context.Context, n serviceinstall.NodeContext, discovery bool) (*NodeEnvironmentObservation, error) {
	n, _, err := resolveNodeAccount(n, discovery)
	if err != nil {
		return nil, err
	}
	action := "inspect"
	if discovery && strings.HasSuffix(n.RootPath, "/.nvm") {
		action = "inspect-external"
	}
	if discovery && n.RootPath == "/usr" {
		action = "inspect-system"
	}
	out, err := runNode(ctx, n, action, "", "", "", "", "", x.softwareRunner)
	if err != nil {
		return nil, err
	}
	obs, err := parseNodeObservation(n, out)
	if err != nil {
		return nil, err
	}
	if n.ConnectTerminal && !discovery {
		terminal, _, e := nodeTerminalFiles(n)
		if e != nil {
			terminal = &NodeTerminalObservation{Status: "CONFLICT", Profiles: []NodeTerminalProfile{}}
		}
		obs.Terminal = terminal
		frozen := *obs
		frozen.Fingerprint = ""
		frozen.ObservedAt = ""
		obs.Fingerprint = Digest(frozen)
	}
	return obs, nil
}

func (x *executor) nodeBound(req Request, n serviceinstall.NodeContext) error {
	var owner nodeEnvironmentOwner
	if readJSON(x.nodeOwnerFile(n.EnvironmentID), &owner) != nil || owner != nodeOwner(req, n) {
		return errors.New("NODE_ENVIRONMENT_NOT_MANAGED")
	}
	return nil
}

func (x *executor) nodePreflight(req Request) NodeEnvironmentPreflight {
	p := NodeEnvironmentPreflight{Checks: []serviceinstall.Check{}, Commands: []serviceinstall.OfficialCommand{}}
	fail := func(code string) NodeEnvironmentPreflight {
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "NODE", Status: "FAIL", Code: code, Message: nodeErrorMessage(code)})
		return p
	}
	if req.NodeEnvironment == nil {
		return fail("NODE_ENVIRONMENT_REQUIRED")
	}
	n := *req.NodeEnvironment
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fail("NODE_PLATFORM_UNSUPPORTED")
	}
	resolved, _, err := resolveNodeAccount(n.Context, false)
	if err != nil {
		return fail(err.Error())
	}
	n.Context = resolved
	p.Context = resolved
	if err = n.Validate(); err != nil {
		return fail(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	obs, err := x.observeNode(ctx, resolved, false)
	if err != nil {
		return fail(err.Error())
	}
	p.Observation = obs
	if n.Action == "INITIALIZE" {
		if _, e := os.Lstat(resolved.RootPath); obs.Exists || !os.IsNotExist(e) {
			return fail("NODE_DIRECTORY_EXISTS")
		}
	} else {
		if x.nodeBound(req, resolved) != nil {
			return fail("NODE_ENVIRONMENT_NOT_MANAGED")
		}
		if !obs.Exists || !trustedObservedNodeManager(obs) || obs.NVMVersion != resolved.NVMVersion || obs.NVMHash != resolved.NVMHash {
			return fail("NODE_MANAGER_DRIFT")
		}
	}
	if n.ExpectedHash != "" && n.ExpectedHash != obs.Fingerprint {
		return fail("NODE_ENVIRONMENT_DRIFT")
	}
	if obs.DefaultVersion != resolved.ExpectedDefault {
		return fail("NODE_DEFAULT_CHANGED")
	}
	if n.NodeVersion != "" {
		found := false
		for _, v := range obs.Versions {
			if v.Version == n.NodeVersion {
				found = true
			}
		}
		if !found {
			return fail("NODE_VERSION_MISSING")
		}
	}
	if n.Action == "REMOVE_ENVIRONMENT" && len(obs.Versions) > 0 {
		return fail("NODE_ENVIRONMENT_NOT_EMPTY")
	}
	if n.Action == "CONNECT_TERMINAL" || n.Action == "REMOVE_ENVIRONMENT" && resolved.ConnectTerminal {
		terminal, _, e := nodeTerminalFiles(resolved)
		if e != nil {
			return fail(e.Error())
		}
		if terminal.Status == "CONFLICT" || terminal.Status == "UNSUPPORTED" {
			return fail("NODE_TERMINAL_" + terminal.Status)
		}
	}
	if d, e := nodeEnvironmentDefinition(n); e == nil {
		if e = n.Source.ValidateFor(d, serviceinstall.Config{RuntimeMode: "NATIVE"}); e != nil {
			return fail(e.Error())
		}
	} else if n.Source.Type != "" || n.PackageVersion != "" {
		return fail("NODE_ACTION_MATERIAL_UNEXPECTED")
	}
	p.Ready = true
	p.Checks = append(p.Checks, serviceinstall.Check{Key: "NODE", Status: "PASS", Message: "执行账号、环境和精确版本已核对"})
	p.Commands = nodePlanCommands(n)
	if n.Action == "REMOVE_ENVIRONMENT" && n.DeleteData {
		p.RemovedPaths = []string{n.Context.RootPath}
	}
	return p
}

func nodePlanCommands(n NodeEnvironmentRequest) []serviceinstall.OfficialCommand {
	root := n.Context.RootPath
	base := serviceinstall.NodeVersionRoot(n.Context, n.NodeVersion)
	switch n.Action {
	case "OBSERVE", "DISCOVER", "CONNECT_TERMINAL":
		return []serviceinstall.OfficialCommand{}
	case "INITIALIZE", "UPDATE_NVM":
		return []serviceinstall.OfficialCommand{{Executable: "install", Args: []string{"-m", "600", "nvm.sh", path.Join(root, "nvm/nvm.sh")}}, {Executable: "install", Args: []string{"-m", "700", "nvm-exec", path.Join(root, "nvm/nvm-exec")}}}
	case "SET_DEFAULT":
		return []serviceinstall.OfficialCommand{{Executable: "nvm", Args: []string{"alias", "default", n.NodeVersion}}}
	case "REMOVE_ENVIRONMENT":
		if n.DeleteData {
			return []serviceinstall.OfficialCommand{{Executable: "rm", Args: []string{"-rf", "--", root}}}
		}
		return []serviceinstall.OfficialCommand{{Executable: "rm", Args: []string{"-f", "--", path.Join(root, "nvm/nvm.sh"), path.Join(root, "nvm/nvm-exec")}}}
	default:
		args := []string{path.Join(base, "lib/node_modules/npm/bin/npm-cli.js"), "install", "--global", "--offline", "--ignore-scripts", "--no-audit", "--no-fund", "--prefix", base, "--"}
		if n.Action == "REMOVE_PNPM" {
			args[1] = "uninstall"
			args = append(args, "pnpm")
		} else {
			d, err := nodeEnvironmentDefinition(n)
			if err != nil || d.Native == nil || len(d.Native.Parts) == 0 {
				return []serviceinstall.OfficialCommand{}
			}
			args = append(args, d.Native.Parts[0].Name)
		}
		return []serviceinstall.OfficialCommand{{Executable: path.Join(base, "bin/node"), Args: args}}
	}
}
func nodeErrorMessage(code string) string {
	switch code {
	case "NODE_ACCOUNT_CHANGED":
		return "执行账号身份已变化，请重新检测"
	case "NODE_DIRECTORY_EXISTS":
		return "此环境目录已存在，请检测已有安装"
	case "NODE_VERSION_MISSING":
		return "所选 Node 版本尚未安装"
	case "NODE_DEFAULT_CHANGED", "NODE_ENVIRONMENT_DRIFT":
		return "环境已变化，请重新检查"
	case "NODE_ENVIRONMENT_NOT_EMPTY":
		return "请先卸载此环境中的 Node 版本"
	case "NODE_TERMINAL_CONFLICT":
		return "Bash 配置已有其他 nvm 或不同 Ops 环境，请先确认终端归属"
	case "NODE_TERMINAL_PATH_UNSAFE":
		return "Bash 配置不是此账号的普通文件，或权限、大小不符合要求"
	case "NODE_TERMINAL_UNSUPPORTED":
		return "终端接入目前支持本地 Bash 账号"
	case "NODE_TERMINAL_CHANGED":
		return "Bash 配置已变化，请重新预检"
	default:
		return "无法确认 Node 环境，请查看检查详情"
	}
}

func (x *executor) executeNodeRead(req Request, output io.Writer) error {
	if req.NodeEnvironment == nil {
		return errors.New("NODE_ENVIRONMENT_REQUIRED")
	}
	if req.Action == "NODE_ENVIRONMENT_CHECK" {
		return json.NewEncoder(output).Encode(x.nodePreflight(req))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	obs, err := x.observeNode(ctx, req.NodeEnvironment.Context, req.Action == "NODE_ENVIRONMENT_DISCOVER")
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(obs)
}

func (x *executor) nodeMaterial(ctx context.Context, req Request, dir string, d serviceinstall.Definition, source serviceinstall.Source) (string, func(), error) {
	var input io.ReadCloser
	local := req
	local.Installation = &serviceinstall.Request{Definition: d, Source: source}
	if source.Type == "OFFICIAL" {
		file := filepath.Join(dir, "node-download")
		if err := serviceinstall.PrepareNativeMaterial(ctx, d, file); err != nil {
			return "", nil, err
		}
		info, err := os.Stat(file)
		if err != nil {
			return "", nil, err
		}
		hash, err := fileSHA(file)
		if err != nil {
			return "", nil, err
		}
		input, err = os.Open(file)
		if err != nil {
			return "", nil, err
		}
		defer input.Close()
		local.Installation.Source = serviceinstall.Source{Type: "ARCHIVE", SHA256: hash, ByteSize: info.Size()}
	}
	original := x.installationInput
	if input != nil {
		x.installationInput = input
	}
	defer func() { x.installationInput = original }()
	parts, err := x.receiveNativeMaterial(local, dir)
	if err != nil {
		return "", nil, err
	}
	// Stage only verified regular files outside the root-only job directory. The
	// account can read the stage; all mutations of HOME are performed as that user.
	stage, err := os.MkdirTemp("/var/tmp", "sfops-node-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	for name, file := range parts {
		raw, e := os.ReadFile(file)
		if e != nil {
			cleanup()
			return "", nil, e
		}
		file := filepath.Join(stage, name)
		if e = os.WriteFile(file, raw, 0444); e != nil {
			cleanup()
			return "", nil, e
		}
		if e = os.Chmod(file, 0444); e != nil {
			cleanup()
			return "", nil, e
		}
	}
	if err = os.Chmod(stage, 0755); err != nil {
		cleanup()
		return "", nil, err
	}
	if len(parts) == 1 {
		return filepath.Join(stage, d.Native.Parts[0].Name), cleanup, nil
	}
	return stage, cleanup, nil
}

func (x *executor) applyNodeEnvironment(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	n := req.NodeEnvironment
	preflight := x.nodePreflight(req)
	if !preflight.Ready {
		return result, errors.New(preflight.Checks[0].Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	material := ""
	if d, err := nodeEnvironmentDefinition(*n); err == nil {
		var cleanup func()
		material, cleanup, err = x.nodeMaterial(ctx, req, dir, d, n.Source)
		if err != nil {
			return result, err
		}
		defer cleanup()
	}
	if err := atomicJSON(filepath.Join(dir, "node-before.json"), preflight.Observation); err != nil {
		return result, err
	}
	if err := atomicJSON(filepath.Join(dir, "node-change.json"), n); err != nil {
		return result, err
	}
	if n.Action == "CONNECT_TERMINAL" {
		terminalContext := n.Context
		terminalContext.TerminalHash = Digest(preflight.Observation.Terminal)
		if err := x.connectNodeTerminal(ctx, terminalContext, req.JobID, dir); err != nil {
			result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
			return result, nil
		}
		return x.reconcileNodeEnvironment(req, dir), nil
	}
	if n.Action == "REMOVE_ENVIRONMENT" && n.Context.ConnectTerminal {
		terminalContext := n.Context
		terminalContext.TerminalHash = Digest(preflight.Observation.Terminal)
		if err := x.changeNodeTerminal(ctx, terminalContext, req.JobID, dir, true); err != nil {
			result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
			return result, nil
		}
	}
	action, pkg := "", ""
	switch n.Action {
	case "INITIALIZE":
		action = "initialize"
	case "UPDATE_NVM":
		action = "update-nvm"
	case "SET_DEFAULT":
		action = "set-default"
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
	option := ""
	if n.DeleteData {
		option = "delete-data"
	}
	out, err := runNode(ctx, n.Context, action, n.NodeVersion, pkg, material, req.JobID, option, x.softwareRunner)
	x.appendServiceLog(req, "NODE_CHANGE", string(out))
	if err != nil {
		result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
		return result, nil
	}
	if n.Action == "INITIALIZE" {
		if err = os.MkdirAll(filepath.Dir(x.nodeOwnerFile(n.Context.EnvironmentID)), 0700); err != nil {
			return result, nil
		}
		if err = atomicJSON(x.nodeOwnerFile(n.Context.EnvironmentID), nodeOwner(req, n.Context)); err != nil {
			return result, nil
		}
	}
	return x.reconcileNodeEnvironment(req, dir), nil
}

func (x *executor) reconcileNodeEnvironment(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
	n := req.NodeEnvironment
	var intent NodeEnvironmentRequest
	if readJSON(filepath.Join(dir, "node-change.json"), &intent) != nil || Digest(intent) != Digest(n) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	obs, err := x.observeNode(ctx, n.Context, false)
	if err != nil {
		return result
	}
	matched := false
	switch n.Action {
	case "CONNECT_TERMINAL":
		matched = nodeTerminalPlanApplied(n.Context, dir)
	case "INITIALIZE", "UPDATE_NVM":
		d, e := nodeEnvironmentDefinition(*n)
		matched = e == nil && obs.Exists && obs.NVMVersion == d.Version && obs.NVMHash == d.Native.Parts[0].SHA256 && obs.NVMExecHash == d.Native.Parts[1].SHA256
	case "SET_DEFAULT":
		matched = obs.DefaultVersion == n.NodeVersion
	case "REMOVE_ENVIRONMENT":
		matched = !obs.Exists && nodeEnvironmentRemoved(n.Context.RootPath, n.DeleteData)
		if n.Context.ConnectTerminal {
			matched = matched && nodeTerminalPlanMatches(n.Context, dir, false)
		}
	default:
		for _, v := range obs.Versions {
			if v.Version != n.NodeVersion {
				continue
			}
			if n.Action == "UPDATE_NPM" {
				matched = v.NPMVersion == n.PackageVersion
			} else if n.Action == "REMOVE_PNPM" {
				matched = v.PNPMVersion == ""
			} else {
				matched = v.PNPMVersion == n.PackageVersion
			}
		}
	}
	var before NodeEnvironmentObservation
	if readJSON(filepath.Join(dir, "node-before.json"), &before) != nil {
		return result
	}
	if n.NodeVersion != "" {
		old, current := nodeVersion(&before, n.NodeVersion), nodeVersion(obs, n.NodeVersion)
		if old == nil || current == nil || old.BinaryHash != current.BinaryHash {
			return result
		}
		if n.Action != "UPDATE_NPM" && old.NPMVersion != current.NPMVersion {
			return result
		}
	}
	if n.Action != "REMOVE_ENVIRONMENT" && !unchangedNodeVersions(&before, obs, n.NodeVersion) {
		return result
	}
	if n.Action != "SET_DEFAULT" && n.Action != "REMOVE_ENVIRONMENT" && obs.DefaultVersion != before.DefaultVersion {
		return result
	}
	if !matched {
		return result
	}
	if n.Action == "INITIALIZE" {
		if os.MkdirAll(filepath.Dir(x.nodeOwnerFile(n.Context.EnvironmentID)), 0700) != nil || atomicJSON(x.nodeOwnerFile(n.Context.EnvironmentID), nodeOwner(req, n.Context)) != nil {
			return result
		}
	}
	if n.Action == "REMOVE_ENVIRONMENT" {
		if err = os.Remove(x.nodeOwnerFile(n.Context.EnvironmentID)); err != nil && !os.IsNotExist(err) {
			return result
		}
	}
	result.Status, result.Phase, result.ErrorCode, result.NodeEnvironment = "SUCCEEDED", "NODE_ENVIRONMENT_UPDATED", "", obs
	return result
}

func nodeEnvironmentRemoved(root string, deleteData bool) bool {
	paths := []string{path.Join(root, "nvm/nvm.sh"), path.Join(root, "nvm/nvm-exec")}
	if deleteData {
		paths = []string{root}
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func trustedObservedNodeManager(obs *NodeEnvironmentObservation) bool {
	if obs == nil {
		return false
	}
	d, err := serviceinstall.NodeEnvironmentRecipe("UPDATE_NVM", obs.NVMVersion)
	return err == nil && len(d.Native.Parts) == 2 && obs.NVMHash == d.Native.Parts[0].SHA256 && obs.NVMExecHash == d.Native.Parts[1].SHA256
}
