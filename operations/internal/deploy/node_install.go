package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Node files belong to the selected account. Root only maintains the protected
// job/binding records; nvm, node and npm always run through runuser.
func validNodeOwnership(s SoftwareRequest) bool {
	o := s.Ownership
	if o == nil || o.Node == nil || o.Node.Validate() != nil || s.SoftwareKey != "NODE" || s.Kind != "TOOL" || o.RuntimeMode != "NATIVE" {
		return false
	}
	version := path.Base(path.Dir(path.Dir(o.BinaryPath)))
	return serviceinstall.NodeVersionPattern.MatchString(version) && o.BinaryPath == serviceinstall.NodeVersionRoot(*o.Node, version)+"/bin/node" && s.Locator == o.BinaryPath
}
func sameNodeOwner(a, b *serviceinstall.NodeContext) bool {
	return a != nil && b != nil && a.EnvironmentID == b.EnvironmentID && a.Account == b.Account && a.AccountUID == b.AccountUID && a.RootPath == b.RootPath
}
func nodeVersion(obs *NodeEnvironmentObservation, version string) *NodeVersionObservation {
	if obs != nil {
		for i := range obs.Versions {
			if obs.Versions[i].Version == version {
				return &obs.Versions[i]
			}
		}
	}
	return nil
}
func unchangedNodeVersions(before, after *NodeEnvironmentObservation, except string) bool {
	filtered := func(o *NodeEnvironmentObservation) []NodeVersionObservation {
		v := []NodeVersionObservation{}
		for _, x := range o.Versions {
			if x.Version != except {
				v = append(v, x)
			}
		}
		return v
	}
	return reflect.DeepEqual(filtered(before), filtered(after))
}
func observeNodeTool(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner) (*SoftwareObservation, error) {
	n := *s.Ownership.Node
	out, err := runNode(ctx, n, "inspect", "", "", "", "", "", run)
	if err != nil {
		return nil, err
	}
	obs, err := parseNodeObservation(n, out)
	if err != nil {
		return nil, err
	}
	version := path.Base(path.Dir(path.Dir(s.Locator)))
	v := nodeVersion(obs, version)
	if v == nil || v.BinaryPath != s.Locator || v.BinaryHash != s.Ownership.BinaryHash {
		return nil, errors.New("SOFTWARE_NATIVE_BINARY_DRIFT")
	}
	return &SoftwareObservation{SoftwareMetadata: SoftwareMetadata{VersionSource: "VERSION_COMMAND", VersionStatus: "VERIFIED", ExecutablePath: v.BinaryPath}, Kind: "TOOL", Locator: v.BinaryPath, IdentityHash: Digest([]string{"TOOL", v.BinaryPath, v.BinaryHash}), State: "INSTALLED", Version: v.Version, ObservedAt: stamp()}, nil
}
func (x *executor) nodeInstallPreflight(i serviceinstall.Request) serviceinstall.Preflight {
	p := serviceinstall.Preflight{Checks: []serviceinstall.Check{}, PortMappings: []serviceinstall.PortMapping{}}
	fail := func(code string) serviceinstall.Preflight {
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "NODE", Status: "FAIL", Code: code, Message: nodeErrorMessage(code)})
		return p
	}
	if i.Validate() != nil || i.Node == nil {
		return fail("NODE_INSTALL_INPUT_INVALID")
	}
	n := *i.Node
	p.Root = serviceinstall.NodeVersionRoot(n, i.Definition.Version)
	p.BinaryDirectory = path.Join(p.Root, "bin")
	p.DataDirectory = path.Join(n.RootPath, "cache", i.Definition.Version)
	p.ConfigDirectory = path.Join(n.RootPath, "nvm/alias")
	p.LogsDirectory = path.Join(n.RootPath, "backups", i.Definition.Version)
	req := Request{ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, NodeEnvironment: &NodeEnvironmentRequest{Context: n, Action: "OBSERVE"}}
	check := x.nodePreflight(req)
	if !check.Ready {
		p.Checks = check.Checks
		return p
	}
	if nodeVersion(check.Observation, i.Definition.Version) != nil {
		return fail("NODE_ALREADY_INSTALLED")
	}
	if _, err := os.Lstat(p.Root); !os.IsNotExist(err) {
		return fail("NODE_DIRECTORY_EXISTS")
	}
	if n.ConnectTerminal {
		terminal, _, err := nodeTerminalFiles(n)
		if err != nil {
			return fail(err.Error())
		}
		if terminal.Status == "CONFLICT" || terminal.Status == "UNSUPPORTED" {
			return fail("NODE_TERMINAL_" + terminal.Status)
		}
		p.NodeTerminalHash = Digest(terminal)
		if n.TerminalHash != "" && n.TerminalHash != p.NodeTerminalHash {
			return fail("NODE_TERMINAL_CHANGED")
		}
	}
	p.Ready = true
	p.Checks = check.Checks
	return p
}

type nodeInstallMarker struct {
	JobID, InputHash, BinaryHash, NPMVersion string
	Before                                   NodeEnvironmentObservation
}

func (x *executor) installNodeVersion(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	p := x.nodeInstallPreflight(*i)
	if !p.Ready {
		return result, errors.New(p.Checks[0].Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	before, err := x.observeNode(ctx, *i.Node, false)
	if err != nil {
		return result, err
	}
	material, cleanup, err := x.nodeMaterial(ctx, req, dir, i.Definition, i.Source)
	if err != nil {
		return result, err
	}
	defer cleanup()
	// Read trusted archive metadata before changing the user's environment.
	extracted := filepath.Join(dir, "node-verified")
	if err = os.MkdirAll(extracted, 0700); err != nil {
		return result, err
	}
	if err = extractNativePart(ctx, i.Definition.Native.Parts[0], material, extracted); err != nil {
		return result, err
	}
	hash, err := fileSHA(filepath.Join(extracted, "bin/node"))
	if err != nil {
		return result, err
	}
	raw, err := os.ReadFile(filepath.Join(extracted, "lib/node_modules/npm/package.json"))
	if err != nil {
		return result, err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version == "" {
		return result, errors.New("NODE_NPM_MANIFEST_INVALID")
	}
	marker := nodeInstallMarker{req.JobID, req.InputHash, hash, manifest.Version, *before}
	if i.Node.ConnectTerminal {
		terminal, plan, e := nodeTerminalFiles(*i.Node)
		if e != nil {
			return result, e
		}
		if terminal.Status == "CONFLICT" || terminal.Status == "UNSUPPORTED" {
			return result, errors.New("NODE_TERMINAL_" + terminal.Status)
		}
		if i.Node.TerminalHash != "" && i.Node.TerminalHash != Digest(terminal) {
			return result, errors.New("NODE_TERMINAL_CHANGED")
		}
		if e = atomicJSON(filepath.Join(dir, "node-terminal.json"), plan); e != nil {
			return result, e
		}
	}
	if err = atomicJSON(filepath.Join(dir, "node-install.json"), marker); err != nil {
		return result, err
	}
	def := i.Node.ExpectedDefault
	if i.Node.MakeDefault {
		def = i.Definition.Version
	}
	out, err := x.runNodeTemplate(ctx, req, *i.Node, "install", i.Definition.Version, "", material, def)
	x.appendServiceLog(req, "NODE_INSTALL", string(out))
	if err != nil {
		result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
		return result, nil
	}
	if i.Node.ConnectTerminal {
		if err = x.connectNodeTerminal(ctx, *i.Node, req.JobID, dir); err != nil {
			result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
			return result, nil
		}
	}
	return x.reconcileNodeInstall(req, dir), nil
}
func (x *executor) reconcileNodeInstall(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
	i := req.Installation
	var m nodeInstallMarker
	if readJSON(filepath.Join(dir, "node-install.json"), &m) != nil || m.JobID != req.JobID || m.InputHash != req.InputHash || !Hex.MatchString(m.BinaryHash) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	obs, err := x.observeNode(ctx, *i.Node, false)
	if err != nil {
		return result
	}
	v := nodeVersion(obs, i.Definition.Version)
	def := i.Node.ExpectedDefault
	if i.Node.MakeDefault {
		def = i.Definition.Version
	}
	if v == nil || v.BinaryHash != m.BinaryHash || v.NPMVersion != m.NPMVersion || obs.DefaultVersion != def || !unchangedNodeVersions(&m.Before, obs, i.Definition.Version) {
		return result
	}
	if i.Node.ConnectTerminal && !nodeTerminalPlanApplied(*i.Node, dir) {
		return result
	}
	o := &SoftwareOwnership{ControllerID: req.ControllerID, TargetID: req.TargetID, SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), RuntimeMode: "NATIVE", BinaryPath: v.BinaryPath, BinaryHash: v.BinaryHash, Node: i.Node}
	s := SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: "TOOL", Locator: v.BinaryPath, SoftwareKey: "NODE", Ownership: o, IdentityHash: Digest([]string{"TOOL", v.BinaryPath, v.BinaryHash})}
	if s.Validate(false) != nil {
		return result
	}
	if err = os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		return result
	}
	var existing softwareBinding
	err = readJSON(x.softwareFile(s.ID), &existing)
	if err == nil && (existing.JobID != req.JobID || !reflect.DeepEqual(existing.Software, s)) {
		return result
	}
	if err != nil && !os.IsNotExist(err) {
		return result
	}
	if atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID}) != nil {
		return result
	}
	result = x.ownedInstallResult(req, s, v.Version, "READY", "SUCCEEDED")
	result.NodeEnvironment = obs
	return result
}
func nodeVersionDataPaths(n serviceinstall.NodeContext, version string) []string {
	return []string{path.Join(n.RootPath, "cache", version), path.Join(n.RootPath, "backups", version), path.Join(n.RootPath, "nvm/.cache/bin/node-"+version+"-linux-x64")}
}

type nodeUninstallMarker struct {
	JobID, InputHash string
	Before           NodeEnvironmentObservation
}

func (x *executor) uninstallNodeVersion(req Request, dir string) (Result, error) {
	if builtinFlow(req) {
		return x.uninstallNodeVersionFlow(req, dir)
	}
	result := softwareResult(req)
	i := req.Uninstallation.Installation
	n := i.Node
	if !serviceinstall.IsNode(i.Definition) || !sameNodeOwner(n, req.Software.Ownership.Node) || x.nodeBound(req, *n) != nil {
		return result, errors.New("NODE_UNINSTALL_IDENTITY_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	before, err := x.observeNode(ctx, *n, false)
	if err != nil {
		return result, err
	}
	v := nodeVersion(before, i.Definition.Version)
	if v == nil || v.BinaryHash != req.Software.Ownership.BinaryHash || before.DefaultVersion != n.ExpectedDefault {
		return result, errors.New("NODE_ENVIRONMENT_DRIFT")
	}
	clear := ""
	if before.DefaultVersion == i.Definition.Version {
		if !req.Uninstallation.ClearNodeDefault || len(before.Versions) != 1 {
			return result, errors.New("NODE_DEFAULT_IN_USE")
		}
		clear = "clear"
	} else if req.Uninstallation.ClearNodeDefault {
		return result, errors.New("NODE_DEFAULT_CLEAR_INVALID")
	}
	if err = nodeVersionUnused(serviceinstall.NodeVersionRoot(*n, i.Definition.Version)); err != nil {
		return result, err
	}
	if err = atomicJSON(filepath.Join(dir, "node-uninstall.json"), nodeUninstallMarker{req.JobID, req.InputHash, *before}); err != nil {
		return result, err
	}
	out, err := x.runNodeTemplate(ctx, req, *n, "uninstall", i.Definition.Version, "", "", clear)
	x.appendServiceLog(req, "NODE_UNINSTALL", string(out))
	if err != nil {
		return result, nil
	}
	if req.Uninstallation.DeleteData {
		if _, err = x.runNodeTemplate(ctx, req, *n, "clean-version", i.Definition.Version, "", "", ""); err != nil {
			return result, nil
		}
	}
	return x.reconcileNodeUninstall(req, dir), nil
}
func (x *executor) reconcileNodeUninstall(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "NODE_RESULT_UNCONFIRMED"
	i := req.Uninstallation.Installation
	var m nodeUninstallMarker
	if readJSON(filepath.Join(dir, "node-uninstall.json"), &m) != nil || m.JobID != req.JobID || m.InputHash != req.InputHash {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	obs, err := x.observeNode(ctx, *i.Node, false)
	if err != nil {
		return result
	}
	def := m.Before.DefaultVersion
	if req.Uninstallation.ClearNodeDefault {
		def = ""
	}
	if nodeVersion(obs, i.Definition.Version) != nil || obs.DefaultVersion != def || !unchangedNodeVersions(&m.Before, obs, i.Definition.Version) {
		return result
	}
	paths := []string(nil)
	if req.Uninstallation.DeleteData {
		paths = nodeVersionDataPaths(*i.Node, i.Definition.Version)
		for _, p := range paths {
			if _, err = os.Lstat(p); !os.IsNotExist(err) {
				return result
			}
		}
	}
	if err = os.Remove(x.softwareFile(i.SoftwareID)); err != nil && !os.IsNotExist(err) {
		return result
	}
	result.Status, result.Phase, result.ErrorCode = "SUCCEEDED", "SOFTWARE_UNINSTALLED", ""
	result.NodeEnvironment = obs
	result.Uninstallation = &SoftwareUninstallEvidence{SoftwareID: i.SoftwareID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: req.Uninstallation.DeleteData, DataRemoved: req.Uninstallation.DeleteData, DataRoot: path.Join(i.Node.RootPath, "cache", i.Definition.Version), RemovedPaths: paths, ObservedAt: stamp()}
	return result
}
func (x *executor) verifyNodeUninstall(req Request, result Result) error {
	e := result.Uninstallation
	if e == nil || e.SoftwareID != req.Software.ID || e.IdentityHash != req.Software.IdentityHash || !e.ProgramRemoved || e.DeleteData != req.Uninstallation.DeleteData || e.DataRemoved != e.DeleteData || result.NodeEnvironment == nil {
		return errors.New("NODE_UNINSTALL_RECEIPT_MISMATCH")
	}
	if _, err := os.Lstat(x.softwareFile(req.Software.ID)); !os.IsNotExist(err) {
		return errors.New("SOFTWARE_BINDING_STILL_PRESENT")
	}
	return nil
}

// Root reads process links without executing account-controlled files. This
// also detects services launched by a different uid using the selected binary.
func nodeVersionUnused(root string) error {
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return errors.New("NODE_PROCESS_CHECK_UNAVAILABLE")
	}
	for _, p := range processes {
		if _, err = strconv.Atoi(p.Name()); err != nil {
			continue
		}
		exe, err := os.Readlink(path.Join("/proc", p.Name(), "exe"))
		if os.IsPermission(err) {
			return errors.New("NODE_PROCESS_CHECK_UNAVAILABLE")
		}
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		if strings.HasPrefix(exe, root+"/") {
			return errors.New("NODE_VERSION_RUNNING")
		}
	}
	return nil
}
