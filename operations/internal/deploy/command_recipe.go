package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func commandInstallation(req Request) *serviceinstall.Request {
	if req.Installation != nil && req.Installation.Definition.Commands != nil {
		return req.Installation
	}
	if req.Uninstallation != nil && req.Uninstallation.Installation.Definition.Commands != nil {
		return &req.Uninstallation.Installation
	}
	return nil
}

func commandPath(value, root, id string) string {
	return strings.NewReplacer("${OPS_INSTALL_ROOT}", root, "${OPS_SOFTWARE_ID}", id).Replace(value)
}

func validCommandBinaryPath(value string) bool {
	return nativeBinaryPathPattern.MatchString(value) && path.IsAbs(value) && path.Clean(value) == value && len(value) <= 512
}

func commandAccount(i serviceinstall.Request) (*user.User, error) {
	for _, step := range i.Definition.Commands.Steps {
		if step.RunAs == "ACCOUNT" {
			account, err := user.Lookup(i.Config.Account)
			if err != nil || account.Uid == "0" || account.Gid == "0" || !filepath.IsAbs(account.HomeDir) {
				return nil, errors.New("COMMAND_ACCOUNT_INVALID")
			}
			return account, nil
		}
	}
	return nil, nil
}

func commandFileAbsent(file string) error {
	if _, err := os.Lstat(file); err == nil {
		return errors.New("COMMAND_OBJECT_ALREADY_EXISTS")
	} else if !os.IsNotExist(err) {
		return errors.New("COMMAND_OBJECT_CHECK_UNAVAILABLE")
	}
	return nil
}

// A template installs a new declared object. Existing tools, units and
// containers belong to discovery/adoption, never to installation ownership.
func (x *executor) commandObjectAbsent(i serviceinstall.Request, root string) error {
	c := i.Definition.Commands
	locator := commandPath(c.Locator, root, i.SoftwareID)
	if strings.ContainsAny(locator, "$\x00\r\n") {
		return errors.New("COMMAND_LOCATOR_INVALID")
	}
	if c.Kind == "TOOL" {
		if !validCommandBinaryPath(locator) {
			return errors.New("COMMAND_LOCATOR_INVALID")
		}
		return commandFileAbsent(locator)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c.Kind == "SYSTEMD" {
		if !softwareUnitPattern.MatchString(locator) {
			return errors.New("COMMAND_LOCATOR_INVALID")
		}
		out, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--property=LoadState", "--value", "--", locator)
		if err != nil {
			return errors.New("COMMAND_OBJECT_CHECK_UNAVAILABLE")
		}
		switch strings.TrimSpace(string(out)) {
		case "not-found":
			return nil
		case "loaded", "masked", "error", "bad-setting", "merged", "stub":
			return errors.New("COMMAND_OBJECT_ALREADY_EXISTS")
		default:
			return errors.New("COMMAND_OBJECT_CHECK_UNAVAILABLE")
		}
	}
	if c.Kind != "DOCKER" || !softwareContainerPattern.MatchString(locator) {
		return errors.New("COMMAND_LOCATOR_INVALID")
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "ls", "-a", "--no-trunc", "--format", "{{.ID}} {{.Names}}")
	if err != nil || len(out) >= 64<<10 {
		return errors.New("COMMAND_OBJECT_CHECK_UNAVAILABLE")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !Hex.MatchString(fields[0]) || !softwareContainerPattern.MatchString(fields[1]) {
			return errors.New("COMMAND_OBJECT_CHECK_UNAVAILABLE")
		}
		if fields[1] == locator || strings.Trim(locator, "0123456789abcdef") == "" && strings.HasPrefix(fields[0], locator) {
			return errors.New("COMMAND_OBJECT_ALREADY_EXISTS")
		}
	}
	return nil
}

func (x *executor) commandPreflight(i serviceinstall.Request) serviceinstall.Preflight {
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	p := serviceinstall.Preflight{Ready: true, Root: root, DataDirectory: filepath.Join(root, "data"), ConfigDirectory: filepath.Join(root, "config"), BinaryDirectory: filepath.Join(root, "bin"), LogsDirectory: filepath.Join(root, "logs"), Checks: []serviceinstall.Check{}}
	fail := func(key, code, message string) {
		p.Ready = false
		p.Checks = append(p.Checks, serviceinstall.Check{Key: key, Status: "FAIL", Code: code, Message: message})
	}
	account, accountErr := commandAccount(i)
	if accountErr != nil {
		fail("ACCOUNT", accountErr.Error(), "所选执行账号不存在或不是普通账号")
	}
	for _, check := range x.commandPythonPreflight(i) {
		p.Checks = append(p.Checks, check)
		if check.Status != "PASS" {
			p.Ready = false
		}
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		fail("DIRECTORY", "SERVICE_DATA_DIRECTORY_EXISTS", "安装目录已存在，请核对原安装记录")
	}
	if secureAncestors(existingServiceParent(filepath.Dir(root))) != nil {
		fail("DIRECTORY", "NATIVE_DIRECTORY_NOT_PROTECTED", "安装目录的上级目录权限不满足要求")
	}
	if err := x.commandObjectAbsent(i, root); err != nil {
		message := "无法确认声明的软件是否已存在，请检查目标服务器后重试"
		if err.Error() == "COMMAND_OBJECT_ALREADY_EXISTS" {
			message = "声明的软件已存在，请通过软件发现登记管理"
		}
		if err.Error() == "COMMAND_LOCATOR_INVALID" {
			message = "软件定位格式不正确，请填写完整路径或受支持的占位符"
		}
		fail("EXISTING", err.Error(), message)
	} else {
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "EXISTING", Status: "PASS", Code: "COMMAND_OBJECT_ABSENT", Message: "声明的软件对象不存在"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mappings := serviceinstall.PortMappings(i.Definition, i.Config)
	dockerAvailable := i.Config.Mode() == "DOCKER"
	if len(mappings) > 0 && !dockerAvailable {
		_, err := x.softwareRunner(ctx, "/usr/bin/docker", "version", "--format", "{{.Server.Version}}")
		dockerAvailable = err == nil
	}
	for _, mapping := range mappings {
		if availableServicePort(mapping) != nil {
			fail("PORT_"+mapping.Key, "SERVICE_PORT_UNAVAILABLE", "所选端口已占用或地址不可用")
			continue
		}
		if dockerAvailable {
			owner, err := x.servicePublishedPortFor(ctx, mapping.BindAddress, mapping.HostPort, mapping.Protocol)
			if err != nil || owner != "" {
				fail("PORT_"+mapping.Key, "SERVICE_PORT_PUBLISHED", "所选端口已被容器发布或暂不可核对")
				continue
			}
		}
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "PORT_" + mapping.Key, Status: "PASS", Code: "SERVICE_PORT_AVAILABLE", Message: "所选端口可用"})
	}
	tools := []string{"/bin/bash"}
	if account != nil {
		tools = append(tools, "/usr/sbin/runuser")
	}
	for _, command := range tools {
		resolved, err := filepath.EvalSymlinks(command)
		if err != nil || secureAncestors(resolved) != nil {
			fail("DEPENDENCY", "COMMAND_DEPENDENCY_MISSING", "缺少命令执行工具 "+command)
		}
	}
	if len(i.Definition.Commands.Distributions) > 0 {
		release, err := os.ReadFile("/etc/os-release")
		distribution := ""
		for _, line := range strings.Split(string(release), "\n") {
			if strings.HasPrefix(line, "ID=") {
				distribution = strings.Trim(strings.TrimPrefix(line, "ID="), "\"'")
			}
		}
		if err != nil || !slices.Contains(i.Definition.Commands.Distributions, distribution) {
			fail("PLATFORM", "COMMAND_PLATFORM_UNSUPPORTED", "此模板不支持目标系统")
		}
	}
	if p.Ready {
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "COMMANDS", Status: "PASS", Message: "命令模板与执行环境检查通过"})
	}
	return p
}

type commandStepReceipt struct {
	Phase      string `json:"phase"`
	Name       string `json:"name"`
	State      string `json:"state"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

func (x *executor) runCommandSteps(req Request, dir, root string, phases ...string) error {
	i := commandInstallation(req)
	account, err := commandAccount(*i)
	if err != nil {
		return err
	}
	indices := []int{}
	if i.Definition.Commands.FlowVersion == 1 {
		for index, step := range i.Definition.Commands.Steps {
			if slices.Contains(phases, step.Phase) {
				indices = append(indices, index)
			}
		}
	} else {
		for _, phase := range phases {
			for index, step := range i.Definition.Commands.Steps {
				if step.Phase == phase {
					indices = append(indices, index)
				}
			}
		}
	}
	for _, index := range indices {
		step := i.Definition.Commands.Steps[index]
		phase := "COMMAND_" + step.Phase
		file := filepath.Join(dir, fmt.Sprintf("command-step-%02d.json", index+1))
		if _, err := os.Lstat(file); !os.IsNotExist(err) {
			return errors.New("COMMAND_STEP_RECONCILE_REQUIRED")
		}
		receipt := commandStepReceipt{Phase: step.Phase, Name: step.Name, State: "RUNNING", StartedAt: stamp()}
		if err := atomicJSON(file, receipt); err != nil {
			return err
		}
		if err := x.checkpoint(req, phase); err != nil {
			return err
		}
		if index == indices[0] && i.Definition.Management != nil && i.Definition.Management.ScriptPrelude != "" {
			x.appendServiceLog(req, phase, "共用脚本\n$ "+i.Definition.Management.ScriptPrelude)
		}
		x.appendServiceLog(req, phase, fmt.Sprintf("步骤 %d · %s [%s]\n$ %s", index+1, step.Name, step.RunAs, step.Script))
		limit := time.Duration(step.TimeoutSeconds) * time.Second
		if deadline, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil {
			limit = min(limit, time.Until(deadline))
		}
		if limit <= 0 {
			return errors.New("DEADLINE_EXCEEDED")
		}
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		logged, closeLog := x.installationLogContext(ctx, req, phase)
		err = x.runCommandStep(logged, req, root, step, account)
		closeLog()
		interrupted := ctx.Err() != nil
		cancel()
		receipt.FinishedAt, receipt.State = stamp(), "SUCCEEDED"
		if err != nil {
			receipt.State = "FAILED"
		}
		if writeErr := atomicJSON(file, receipt); writeErr != nil {
			return writeErr
		}
		if interrupted {
			return errors.New("COMMAND_STEP_INTERRUPTED")
		}
		if err != nil {
			return errors.New("COMMAND_STEP_FAILED")
		}
	}
	return nil
}

func (x *executor) runCommandStep(ctx context.Context, req Request, root string, step serviceinstall.CommandStep, account *user.User) error {
	i := commandInstallation(req)
	command := "/bin/bash"
	script := step.Script
	if i.Definition.Management != nil && i.Definition.Management.ScriptPrelude != "" {
		script = i.Definition.Management.ScriptPrelude + "\n" + script
	}
	args := []string{"--noprofile", "--norc", "-e", "-o", "pipefail", "-c", script}
	home, username := "/root", "root"
	if step.RunAs == "ACCOUNT" {
		if account == nil {
			return errors.New("COMMAND_ACCOUNT_INVALID")
		}
		command, home, username = "/usr/sbin/runuser", account.HomeDir, account.Username
		args = append([]string{"-u", account.Username, "--", "/bin/bash"}, args...)
	}
	resolved, err := filepath.EvalSymlinks(command)
	if err != nil || secureAncestors(resolved) != nil {
		return errors.New("COMMAND_DEPENDENCY_MISSING")
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Dir = filepath.Join(root, "work")
	if step.WorkingDirectory != "" {
		cmd.Dir = commandPath(step.WorkingDirectory, root, i.SoftwareID)
	}
	cmd.Env = []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + home, "USER=" + username, "LOGNAME=" + username,
		"LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0",
		"OPS_INSTALL_ROOT=" + root, "OPS_DATA_ROOT=" + filepath.Join(root, "data"), "OPS_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"OPS_SOFTWARE_ID=" + i.SoftwareID, "OPS_VERSION=" + i.Definition.Version, "OPS_ACCOUNT=" + i.Config.Account,
		"OPS_SOURCE=" + i.Source.Type, "OPS_ARTIFACT_PATH=" + filepath.Join(root, "artifact"), "OPS_IMAGE=" + i.Definition.Image,
		"OPS_HOST_PORT=" + strconv.Itoa(i.Config.HostPort), "OPS_MEMORY_MIB=" + strconv.Itoa(i.Config.MemoryMiB), "OPS_BIND_ADDRESS=" + i.Config.BindAddress,
	}
	values, err := serviceinstall.ResolveParameters(i.Definition.Parameters, i.Config.Parameters, i.Config.ParameterCredentials)
	if err != nil {
		return err
	}
	cmd.Env = append(cmd.Env, parameterEnvironment(values)...)
	for _, mapping := range serviceinstall.PortMappings(i.Definition, i.Config) {
		cmd.Env = append(cmd.Env, "OPS_PORT_"+strings.ToUpper(mapping.Key)+"="+strconv.Itoa(mapping.HostPort))
	}
	cmd.Env = append(cmd.Env, "OPS_JOB_ID="+req.JobID, "OPS_RUNTIME_KIND="+i.Definition.Commands.Kind, "OPS_RUNTIME_MODE="+i.Config.Mode())
	cleanup, err := x.appendParameterSecrets(cmd, req, account, i.Config.ParameterCredentials)
	if err != nil {
		return err
	}
	defer cleanup()
	boundPackageCommand(cmd)
	buffer := &cappedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = commandOutputs(ctx, buffer)
	return cmd.Run()
}

func (x *executor) installCommands(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	for _, step := range i.Definition.Commands.Steps {
		if (step.Phase == "INSTALL" || step.Phase == "VERIFY") && strings.Contains(step.Script, "\r\n") {
			return result, errors.New("COMMAND_SCRIPT_LINE_ENDINGS_INVALID")
		}
	}
	preflight := x.commandPreflight(*i)
	for _, check := range preflight.Checks {
		if check.Status != "PASS" {
			return result, errors.New(check.Code)
		}
	}
	root := preflight.Root
	if i.Source.Type == "ARCHIVE" {
		file := filepath.Join(dir, "command-artifact")
		if x.installationInput == nil || receiveInstallArchive(file, x.installationInput, i.Source.ByteSize, 0) != nil {
			return result, errors.New("COMMAND_ARTIFACT_TRANSFER_FAILED")
		}
		if digest, err := fileSHA(file); err != nil || digest != i.Source.SHA256 {
			return result, errors.New("COMMAND_ARTIFACT_DIGEST_MISMATCH")
		}
	}
	if err := ensureNativeParent(root); err != nil {
		return result, err
	}
	if err := os.Mkdir(root, 0755); err != nil {
		return result, err
	}
	account, err := commandAccount(*i)
	if err != nil {
		return result, err
	}
	for _, name := range []string{"bin", "config", "data", "logs", "work"} {
		child := filepath.Join(root, name)
		if err := os.Mkdir(child, 0755); err != nil {
			return result, err
		}
		if account != nil {
			uid, e1 := strconv.Atoi(account.Uid)
			gid, e2 := strconv.Atoi(account.Gid)
			if e1 != nil || e2 != nil {
				return result, errors.New("COMMAND_ACCOUNT_INVALID")
			}
			if err := os.Chown(child, uid, gid); err != nil {
				return result, err
			}
		}
	}
	if i.Source.Type == "ARCHIVE" {
		artifact := filepath.Join(root, "artifact")
		input, err := os.Open(filepath.Join(dir, "command-artifact"))
		if err != nil {
			return result, err
		}
		err = receiveInstallArchive(artifact, input, i.Source.ByteSize, 0)
		_ = input.Close()
		if err != nil {
			return result, err
		}
		if err := os.Chmod(artifact, 0444); err != nil {
			return result, err
		}
	}
	// Material transfer may take time; close that window before any install
	// script runs. The existing host lock coordinates other Ops mutations.
	if err := x.commandObjectAbsent(*i, root); err != nil {
		return result, err
	}
	if err := x.runCommandSteps(req, dir, root, "INSTALL", "VERIFY"); err != nil {
		return result, err
	}
	s, actual, err := x.commandSoftware(req)
	if err != nil {
		return result, err
	}
	if err = os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		return result, err
	}
	if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
		return result, err
	}
	return x.commandInstallResult(req, s, actual), nil
}

func (x *executor) commandSoftware(req Request) (SoftwareRequest, *SoftwareObservation, error) {
	i := req.Installation
	c := i.Definition.Commands
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	o := x.installOwnership(req)
	o.RuntimeMode = i.Config.Mode()
	o.CommandTemplateHash = Digest(i.Definition)
	s := SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: c.Kind, Locator: commandPath(c.Locator, root, i.SoftwareID), SoftwareKey: strings.ToUpper(i.Definition.Key), ExpectedVersion: i.Definition.Version, Ownership: o}
	if c.Kind == "TOOL" {
		o.BinaryPath = s.Locator
	} else {
		o.BinaryPath = commandPath(c.BinaryPath, root, i.SoftwareID)
	}
	if o.BinaryPath != "" {
		resolved, err := filepath.EvalSymlinks(o.BinaryPath)
		if err != nil || !validCommandBinaryPath(resolved) {
			return s, nil, errors.New("COMMAND_BINARY_UNAVAILABLE")
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return s, nil, errors.New("COMMAND_BINARY_UNAVAILABLE")
		}
		o.BinaryPath = resolved
		if c.Kind == "TOOL" {
			s.Locator = resolved
		}
		o.BinaryHash, err = fileSHA(resolved)
		if err != nil {
			return s, nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if c.Kind == "DOCKER" {
		out, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "inspect", "--format", "{{.Id}} {{.Image}}", "--", s.Locator)
		fields := strings.Fields(string(out))
		if err != nil || len(fields) != 2 || !Hex.MatchString(fields[0]) || !strings.HasPrefix(fields[1], "sha256:") {
			return s, nil, errors.New("COMMAND_CONTAINER_UNCONFIRMED")
		}
		s.Locator, o.ImageID = fields[0], fields[1]
	}
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return s, nil, err
	}
	if actual.State == "UNKNOWN" || actual.State == "FAILED" || c.Kind != "TOOL" && actual.State != "RUNNING" {
		return s, nil, errors.New("COMMAND_SOFTWARE_UNCONFIRMED")
	}
	s.IdentityHash = actual.IdentityHash
	return s, actual, nil
}

func (x *executor) commandInstallResult(req Request, s SoftwareRequest, actual *SoftwareObservation) Result {
	i := req.Installation
	r := softwareResult(req)
	r.Status, r.Phase, r.Software = "SUCCEEDED", "SERVICE_INSTALLED", actual
	r.ConfigHash, r.Health, r.ObservedVersion = Digest(i.Config), "READY", actual.Version
	r.Installation = &serviceinstall.Evidence{SoftwareID: i.SoftwareID, BindingRevision: i.BindingRevision, RecipeKey: i.Definition.Key, RecipeRevision: i.Definition.Revision, ConfigHash: r.ConfigHash, Kind: s.Kind, Locator: s.Locator, IdentityHash: s.IdentityHash, ObservedVersion: actual.Version, Health: "READY", ObservedAt: stamp(), BinaryPath: s.Ownership.BinaryPath, BinaryHash: s.Ownership.BinaryHash, ImageID: s.Ownership.ImageID}
	if s.Kind == "DOCKER" {
		r.Installation.ContainerID = s.Locator
		r.ImageID = s.Ownership.ImageID
	}
	return r
}

func (x *executor) reconcileCommands(req Request, dir string) Result {
	r := softwareResult(req)
	r.ErrorCode = "COMMAND_STEP_RECONCILE_REQUIRED"
	if req.Uninstallation != nil {
		if req.Uninstallation.DataDirectory != "" {
			return x.finishMigratedCommandUninstall(req, dir)
		}
		var evidence SoftwareUninstallEvidence
		if readJSON(filepath.Join(dir, "command-uninstall.json"), &evidence) != nil || evidence.SoftwareID != req.Software.ID || evidence.IdentityHash != req.Software.IdentityHash || evidence.DeleteData != req.Uninstallation.DeleteData || !evidence.ProgramRemoved {
			return r
		}
		if x.boundSoftware(*req.Software) == nil {
			if os.Remove(x.softwareFile(req.Software.ID)) != nil {
				return r
			}
		} else if _, err := os.Lstat(x.softwareFile(req.Software.ID)); !os.IsNotExist(err) {
			return r
		}
		r.Status, r.Phase, r.Uninstallation = "SUCCEEDED", "SOFTWARE_UNINSTALLED", &evidence
		r.ErrorCode = ""
		return r
	}
	var binding softwareBinding
	if readJSON(x.softwareFile(req.Installation.SoftwareID), &binding) != nil || binding.JobID != req.JobID || binding.Software.Ownership == nil || binding.Software.Ownership.CommandTemplateHash != Digest(req.Installation.Definition) {
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, binding.Software, x.softwareRunner)
	if err != nil || actual.IdentityHash != binding.Software.IdentityHash || binding.Software.Kind == "TOOL" && actual.State != "INSTALLED" || binding.Software.Kind != "TOOL" && actual.State != "RUNNING" {
		return r
	}
	return x.commandInstallResult(req, binding.Software, actual)
}

func (x *executor) uninstallCommands(req Request, dir string) (Result, error) {
	r := softwareResult(req)
	i := req.Uninstallation.Installation
	if x.boundSoftware(*req.Software) != nil || req.Software.Ownership.CommandTemplateHash != Digest(i.Definition) {
		return r, errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	if err := x.validateManagedDataClaim(req); err != nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	cancel()
	if err != nil {
		return r, err
	}
	if actual.State == "UNKNOWN" {
		return r, errors.New("SOFTWARE_STATE_TRANSITIONING")
	}
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	if req.Uninstallation.DataDirectory != "" {
		return x.uninstallMigratedCommandData(req, dir, root)
	}
	phases := []string{"UNINSTALL"}
	if req.Uninstallation.DeleteData {
		phases = append(phases, "DELETE_DATA")
	}
	if err := x.runCommandSteps(req, dir, root, phases...); err != nil {
		return r, err
	}
	evidence := &SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: req.Uninstallation.DeleteData, DataRoot: filepath.Join(root, "data"), ObservedAt: stamp()}
	if !x.commandProgramAbsent(req) {
		return r, errors.New("COMMAND_UNINSTALL_UNCONFIRMED")
	}
	if req.Uninstallation.DeleteData {
		if _, err := os.Lstat(evidence.DataRoot); !os.IsNotExist(err) {
			return r, errors.New("COMMAND_DATA_REMOVAL_UNCONFIRMED")
		}
		evidence.DataRemoved = true
	}
	if err := atomicJSON(filepath.Join(dir, "command-uninstall.json"), evidence); err != nil {
		return r, err
	}
	if err := os.Remove(x.softwareFile(req.Software.ID)); err != nil {
		return r, err
	}
	r.Status, r.Phase, r.Uninstallation = "SUCCEEDED", "SOFTWARE_UNINSTALLED", evidence
	return r, nil
}
