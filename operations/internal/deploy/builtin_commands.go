package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func commandDefinition(req Request) *serviceinstall.Definition {
	if req.Management != nil {
		return &req.Management.Definition
	}
	if i := commandInstallation(req); i != nil {
		return &i.Definition
	}
	if req.NodeEnvironment != nil && req.NodeEnvironment.Definition != nil && req.NodeEnvironment.Definition.Commands != nil {
		return req.NodeEnvironment.Definition
	}
	return nil
}

func builtinStep(req Request, id string) *serviceinstall.CommandStep {
	d := commandDefinition(req)
	if d == nil || !serviceinstall.IsBuiltinCommand(*d) {
		return nil
	}
	for index := range d.Commands.Steps {
		s := &d.Commands.Steps[index]
		if s.ID == id {
			if i := commandInstallation(req); i != nil && (s.RuntimeMode != "" && s.RuntimeMode != i.Config.Mode() || len(s.SourceModes) > 0 && !slices.Contains(s.SourceModes, i.Source.Type)) {
				return nil
			}
			return s
		}
	}
	return nil
}

func shellArguments(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = posixSingleQuote(value)
	}
	return strings.Join(quoted, " ")
}

func (x *executor) builtinVariables(req Request) map[string]string {
	v := map[string]string{"OPS_JOB_ID": req.JobID, "OPS_BUILD_JOBS": nativeBuildJobs()}
	i := commandInstallation(req)
	if i == nil {
		return v
	}
	d, c := i.Definition, i.Config
	if values, err := serviceinstall.ResolveParameters(d.Parameters, c.Parameters, c.ParameterCredentials); err == nil {
		for key, value := range values {
			v["OPS_PARAM_"+key] = value
		}
	}
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, c)
	v["OPS_INSTALL_ROOT"], v["OPS_DATA_ROOT"], v["OPS_CONFIG_ROOT"] = root, filepath.Join(root, "data"), filepath.Join(root, "config")
	v["OPS_SOFTWARE_ID"], v["OPS_VERSION"], v["OPS_ACCOUNT"] = i.SoftwareID, d.Version, c.Account
	v["OPS_PLATFORM"], v["OPS_IMAGE"], v["OPS_IMAGE_ID"], v["OPS_IMAGE_DIGEST"] = d.Platform, d.Image, d.ImageID, d.Digest
	v["OPS_SOURCE"], v["OPS_HOST_PORT"], v["OPS_MEMORY_MIB"], v["OPS_BIND_ADDRESS"] = i.Source.Type, strconv.Itoa(c.HostPort), strconv.Itoa(c.MemoryMiB), c.BindAddress
	v["OPS_CONTAINER_NAME"], v["OPS_SERVICE_UNIT"], v["OPS_UNIT_PATH"] = serviceName(i.SoftwareID), nativeUnit(i.SoftwareID), nativeUnitPath(i.SoftwareID)
	v["OPS_BINARY"] = filepath.Join(root, serviceinstall.NativeBinaryRelative(d))
	if c.Mode() == "NATIVE" && !serviceinstall.IsOfficial(d) && nativeNeedsAccount(d) && c.Account == "" {
		v["OPS_ACCOUNT"] = nativeUserName(i.SoftwareID)
	}
	if d.Key == "docker" {
		v["OPS_BINARY"], v["OPS_SERVICE_UNIT"] = "/usr/bin/dockerd", "docker.service"
	}
	if serviceinstall.IsOfficial(d) {
		spec, _ := serviceinstall.OfficialRecipeSpec(d)
		v["OPS_BINARY"], v["OPS_SERVICE_UNIT"] = spec.Paths.Binary, spec.Paths.Unit
	}
	if req.Software != nil {
		v["OPS_CONTAINER_ID"] = req.Software.Locator
	}
	return v
}

// Only named argument lists come from the validated operation. Scalar values
// remain environment variables, never source text; credentials are files.
func (x *executor) runBuiltinStep(ctx context.Context, req Request, id string, variables map[string]string, lists map[string][]string, account string) ([]byte, bool, error) {
	step := builtinStep(req, id)
	if step == nil {
		return nil, false, nil
	}
	v := x.builtinVariables(req)
	for key, value := range variables {
		v[key] = value
	}
	script := step.Script
	for key, value := range lists {
		script = strings.ReplaceAll(script, "${"+key+"}", shellArguments(value))
	}
	if strings.Contains(script, "${OPS_PACKAGE_ARGUMENTS}") || strings.Contains(script, "${OPS_OWNED_PATHS}") || strings.Contains(script, "${OPS_CONTAINER_ARGUMENTS}") {
		return nil, true, errors.New("BUILTIN_COMMAND_CONTEXT_MISSING")
	}
	if account == "" {
		account = v["OPS_ACCOUNT"]
	}
	home, username := "/root", "root"
	if step.RunAs == "ACCOUNT" {
		u, err := user.Lookup(account)
		if err != nil || u.Uid == "0" || u.Gid == "0" {
			return nil, true, errors.New("COMMAND_ACCOUNT_INVALID")
		}
		home, username = u.HomeDir, u.Username
	}
	if d := commandDefinition(req); d != nil && (d.Key == "codex" || d.Key == "claude") {
		home = v["OPS_DATA_ROOT"]
		v["CODEX_HOME"], v["CLAUDE_CONFIG_DIR"], v["DISABLE_AUTOUPDATER"], v["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = home, home, "1", "1"
	}
	if i := commandInstallation(req); i != nil && len(i.Config.ParameterCredentials) > 0 {
		var selectedAccount *user.User
		if step.RunAs == "ACCOUNT" {
			selectedAccount, _ = user.Lookup(username)
		}
		stub := &exec.Cmd{Path: "/usr/bin/env", Dir: home}
		if step.RunAs == "ACCOUNT" {
			stub.Path = "/usr/sbin/runuser"
		}
		cleanup, err := x.appendParameterSecrets(stub, req, selectedAccount, i.Config.ParameterCredentials)
		if err != nil {
			return nil, true, err
		}
		defer cleanup()
		for _, item := range stub.Env {
			key, value, _ := strings.Cut(item, "=")
			v[key] = value
		}
	}
	work := "/"
	if step.WorkingDirectory != "" {
		work = step.WorkingDirectory
		for key, value := range v {
			work = strings.ReplaceAll(work, "${"+key+"}", value)
		}
		if !filepath.IsAbs(work) || strings.ContainsAny(work, "$\x00\r\n") {
			return nil, true, errors.New("COMMAND_WORKING_DIRECTORY_INVALID")
		}
	}
	args := []string{"-i", "--chdir=" + work, "PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + home, "USER=" + username, "LOGNAME=" + username, "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0"}
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, key+"="+v[key])
	}
	args = append(args, "/bin/bash", "--noprofile", "--norc", "-e")
	// nvm's shell functions retain their established shell options. Its own
	// action script performs the checks; pipefail changes upstream nvm behavior.
	d := commandDefinition(req)
	if !slices.Contains([]string{"node", "nvm", "npm", "pnpm"}, d.Key) {
		args = append(args, "-o", "pipefail")
	}
	args = append(args, "-c", script)
	command := "/usr/bin/env"
	if step.RunAs == "ACCOUNT" {
		command = "/usr/sbin/runuser"
		args = append([]string{"-u", username, "--", "/usr/bin/env"}, args...)
	}
	limit := time.Duration(step.TimeoutSeconds) * time.Second
	if deadline, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil {
		limit = min(limit, time.Until(deadline))
	}
	if limit <= 0 {
		return nil, true, errors.New("DEADLINE_EXCEEDED")
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	receiptFile := filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+id+".json")
	if _, err := os.Lstat(receiptFile); !os.IsNotExist(err) {
		return nil, true, errors.New("COMMAND_STEP_RECONCILE_REQUIRED")
	}
	receipt := commandStepReceipt{Phase: step.Phase, Name: step.Name, State: "RUNNING", StartedAt: stamp()}
	if err := atomicJSON(receiptFile, receipt); err != nil {
		return nil, true, err
	}
	var details strings.Builder
	for _, key := range keys {
		if strings.HasPrefix(key, "OPS_") && builtinVariableMention(script, key) {
			fmt.Fprintf(&details, "%s=%s\n", key, posixSingleQuote(v[key]))
		}
	}
	x.appendServiceLog(req, id, fmt.Sprintf("%s · 执行账号 %s\n%s$ %s", step.Name, username, details.String(), script))
	ctx, closeLog := x.installationLogContext(ctx, req, id)
	defer closeLog()
	ctx = context.WithValue(ctx, commandAlreadyLoggedKey{}, true)
	out, err := x.softwareRunner(ctx, command, args...)
	receipt.State, receipt.FinishedAt = "SUCCEEDED", stamp()
	if err != nil {
		receipt.State = "FAILED"
	}
	if ctx.Err() != nil {
		receipt.State = "UNKNOWN"
	}
	if writeErr := atomicJSON(receiptFile, receipt); writeErr != nil {
		return out, true, errors.New("COMMAND_STEP_RECONCILE_REQUIRED")
	}
	if ctx.Err() != nil {
		return out, true, errors.New("COMMAND_STEP_INTERRUPTED")
	}
	return out, true, err
}

var builtinVariablePattern = regexp.MustCompile(`\$\{?(OPS_[A-Z0-9_]+)\}?`)

func builtinVariableMention(script, key string) bool {
	for _, match := range builtinVariablePattern.FindAllStringSubmatch(script, -1) {
		if match[1] == key {
			return true
		}
	}
	return false
}

func (x *executor) builtinResultConfirmed(req Request, result Result) bool {
	d := commandDefinition(req)
	if d == nil || !serviceinstall.IsBuiltinCommand(*d) || result.Status != "SUCCEEDED" {
		return true
	}
	if d.Commands.FlowVersion == 1 {
		phases := builtinFlowPhases(req)
		for _, step := range d.Commands.Steps {
			if !slices.Contains(phases, step.Phase) || builtinStep(req, step.ID) == nil {
				continue
			}
			var receipt commandStepReceipt
			if readJSON(filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+step.ID+".json"), &receipt) != nil || !slices.Contains([]string{"SUCCEEDED", "SKIPPED"}, receipt.State) {
				return false
			}
		}
		return true
	}
	id := ""
	if i := req.Installation; i != nil {
		id = "VERIFY_" + i.Config.Mode()
		if serviceinstall.IsOfficial(*d) {
			id = "VERIFY"
		}
		if serviceinstall.IsNode(*d) {
			id = "NODE_VERIFY"
		}
	}
	if req.NodeEnvironment != nil {
		id = "NODE_VERIFY"
		if req.NodeEnvironment.Action == "REMOVE_ENVIRONMENT" || req.NodeEnvironment.Action == "REMOVE_PNPM" {
			id = "NODE_UNINSTALL"
		}
	}
	if u := req.Uninstallation; u != nil {
		switch {
		case serviceinstall.IsNode(*d):
			id = "NODE_UNINSTALL"
		case serviceinstall.IsOfficial(*d):
			id = "OFFICIAL_PACKAGES_REMOVE"
		case u.Installation.Config.Mode() == "NATIVE" && d.Key == "docker":
			id = "DOCKER_PACKAGES_REMOVE"
		case u.Installation.Config.Mode() == "NATIVE":
			id = "NATIVE_REMOVE_PROGRAM"
		case d.Kind != "TOOL":
			id = "DOCKER_REMOVE"
		case result.Uninstallation != nil && !result.Uninstallation.SharedProgramRetained:
			id = "DOCKER_IMAGE_REMOVE"
		}
	}
	ids := []string{id}
	if req.Uninstallation != nil && req.Uninstallation.DeleteData || req.NodeEnvironment != nil && req.NodeEnvironment.DeleteData {
		ids = append(ids, "DELETE_DATA")
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		var receipt commandStepReceipt
		if readJSON(filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+id+".json"), &receipt) != nil || receipt.State != "SUCCEEDED" {
			return false
		}
	}
	return true
}

func (x *executor) builtinCommand(ctx context.Context, req Request, id string, args []string) ([]byte, bool, error) {
	if builtinStep(req, id) == nil {
		return nil, false, nil
	}
	v := map[string]string{}
	lists := map[string][]string{}
	switch id {
	case "OFFICIAL_PACKAGES_APPLY", "OFFICIAL_PACKAGES_REMOVE", "DOCKER_PACKAGES_APPLY", "DOCKER_PACKAGES_REMOVE":
		index := slices.Index(args, "--")
		if index < 0 {
			return nil, true, errors.New("BUILTIN_COMMAND_CONTEXT_MISSING")
		}
		lists["OPS_PACKAGE_ARGUMENTS"] = args[index+1:]
	case "SERVICE_IMAGE_LOAD":
		if len(args) > 0 {
			v["OPS_ARTIFACT_PATH"] = args[len(args)-1]
		}
	case "SERVICE_INITIALIZING", "DOCKER_STOP", "DOCKER_REMOVE":
		if len(args) > 0 {
			v["OPS_CONTAINER_ID"] = args[len(args)-1]
		}
	case "NATIVE_MYSQL_INITIALIZE":
		for _, arg := range args {
			if value, ok := strings.CutPrefix(arg, "--user="); ok {
				v["OPS_ACCOUNT"] = value
			}
		}
	case "OFFICIAL_STOP":
		index := slices.Index(args, "--")
		if index >= 0 {
			lists["OPS_SERVICE_UNITS"] = args[index+1:]
		} else {
			lists["OPS_SERVICE_UNITS"] = args[1:]
		}
	case "SERVICE_CREATE":
		i := commandInstallation(req)
		var err error
		lists, err = builtinDockerLists(args, i.Definition.ImageID)
		if err != nil {
			return nil, true, err
		}
	}
	return x.runBuiltinStep(ctx, req, id, v, lists, "")
}

func builtinDockerLists(args []string, image string) (map[string][]string, error) {
	imageIndex := slices.Index(args, image)
	start := slices.Index(args, "max-file=3") + 1
	if imageIndex < 0 || start < 1 {
		return nil, errors.New("BUILTIN_COMMAND_CONTEXT_MISSING")
	}
	lists := map[string][]string{"OPS_PUBLISH_ARGUMENTS": {}, "OPS_OWNERSHIP_ARGUMENTS": {}, "OPS_CONTAINER_ARGUMENTS": {}, "OPS_PROGRAM_ARGUMENTS": args[imageIndex+1:]}
	for j := start; j < imageIndex; {
		key := "OPS_CONTAINER_ARGUMENTS"
		if args[j] == "--publish" {
			key = "OPS_PUBLISH_ARGUMENTS"
		}
		if args[j] == "--label" {
			key = "OPS_OWNERSHIP_ARGUMENTS"
		}
		end := j + 1
		if end < imageIndex && !strings.HasPrefix(args[end], "--") {
			end++
		}
		lists[key] = append(lists[key], args[j:end]...)
		j = end
	}
	return lists, nil
}

func (x *executor) verifyBuiltinInstall(req Request, result Result) Result {
	i := req.Installation
	if i == nil || !serviceinstall.IsBuiltinCommand(i.Definition) || builtinFlow(req) || result.Status != "SUCCEEDED" {
		return result
	}
	id := "VERIFY_" + i.Config.Mode()
	if serviceinstall.IsOfficial(i.Definition) {
		id = "VERIFY"
	}
	if serviceinstall.IsNode(i.Definition) {
		id = "NODE_VERIFY"
	}
	v := map[string]string{}
	account := ""
	if result.Software != nil {
		v["OPS_CONTAINER_ID"] = result.Software.Locator
	}
	if i.Node != nil {
		v["OPS_NODE_ROOT"], v["OPS_NODE_VERSION"] = i.Node.RootPath, i.Definition.Version
		account = i.Node.Account
	}
	_, _, err := x.runBuiltinStep(context.Background(), req, id, v, nil, account)
	if err != nil {
		result.Status, result.ErrorCode, result.Health = "FAILED", "COMMAND_VERIFY_FAILED", "FAILED"
		if result.Installation != nil {
			result.Installation.Health = "FAILED"
		}
	}
	return result
}

// Vendor archives are still decoded by the bounded, path-safe extractor before
// an editable command can use them. Only this job's stage is account-writable.
func (x *executor) prepareBuiltinNative(ctx context.Context, req Request, root, account string, uid, gid int, parts map[string]string) error {
	v, cleanup, err := x.prepareBuiltinNativeFiles(ctx, req, root, account, uid, gid, parts)
	if err != nil {
		return err
	}
	defer cleanup()
	_, _, err = x.runBuiltinStep(ctx, req, "NATIVE_PROGRAM", v, nil, account)
	return err
}

func (x *executor) prepareBuiltinNativeFiles(ctx context.Context, req Request, root, account string, uid, gid int, parts map[string]string) (map[string]string, func(), error) {
	stage, err := os.MkdirTemp("/var/tmp", "sfops-native-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()
	d := req.Installation.Definition
	v := map[string]string{"OPS_BUILD_ROOT": filepath.Join(stage, "build")}
	for index, part := range d.Native.Parts {
		destination := filepath.Join(stage, fmt.Sprintf("part-%d", index))
		if d.Native.BuildRecipe == "DEB" {
			selected := debProgramPayloads(d)[part.Name]
			wanted := map[string]string{}
			for name := range selected {
				wanted[name] = name
			}
			if len(wanted) == 0 {
				err = os.Mkdir(destination, 0700)
			} else {
				err = extractDebPayload(ctx, parts[part.Name], destination, wanted)
			}
		} else if part.Format == "RAW" {
			err = copyNativeFile(parts[part.Name], destination)
		} else {
			err = extractNativePart(ctx, part, parts[part.Name], destination)
		}
		if err != nil {
			return nil, func() {}, err
		}
		v[fmt.Sprintf("OPS_PART_%d", index)] = destination
	}
	step := builtinStep(req, "NATIVE_PROGRAM")
	accountStep := step != nil && step.RunAs == "ACCOUNT"
	for _, candidate := range req.Installation.Definition.Commands.Steps {
		if candidate.Kind != "SYSTEM" && candidate.RunAs == "ACCOUNT" && candidate.Phase == "INSTALL" && candidate.RuntimeMode != "DOCKER" {
			accountStep = true
		}
	}
	if accountStep {
		if account == "" {
			account = req.Installation.Config.Account
			u, e := user.Lookup(account)
			if e != nil {
				return nil, func() {}, errors.New("COMMAND_ACCOUNT_INVALID")
			}
			uid, _ = strconv.Atoi(u.Uid)
			gid, _ = strconv.Atoi(u.Gid)
		}
		if uid < 1 || gid < 1 {
			return nil, func() {}, errors.New("COMMAND_ACCOUNT_INVALID")
		}
		if err = chownNativeTree(stage, uid, gid); err != nil {
			return nil, func() {}, err
		}
		for _, directory := range []string{"bin", "runtime"} {
			if err = os.Chown(filepath.Join(root, directory), uid, gid); err != nil {
				return nil, func() {}, err
			}
		}
	}
	if account != "" {
		v["OPS_ACCOUNT"] = account
	}
	ok = true
	return v, cleanup, nil
}

func builtinAccountCheck(i serviceinstall.Request) error {
	if !serviceinstall.IsBuiltinCommand(i.Definition) {
		return nil
	}
	for _, step := range i.Definition.Commands.Steps {
		if step.RunAs != "ACCOUNT" || step.RuntimeMode != "" && step.RuntimeMode != i.Config.Mode() || len(step.SourceModes) > 0 && !slices.Contains(step.SourceModes, i.Source.Type) {
			continue
		}
		if serviceinstall.IsNode(i.Definition) && i.Node != nil {
			return nil
		}
		if i.Config.Mode() == "NATIVE" && !serviceinstall.IsOfficial(i.Definition) && nativeNeedsAccount(i.Definition) {
			return nil
		}
		if !serviceinstall.NodeAccountPattern.MatchString(i.Config.Account) || i.Config.Account == "root" {
			return errors.New("COMMAND_ACCOUNT_REQUIRED")
		}
		account, err := user.Lookup(i.Config.Account)
		if err != nil || account.Uid == "0" || account.Gid == "0" {
			return errors.New("COMMAND_ACCOUNT_INVALID")
		}
	}
	return nil
}

func (x *executor) runNodeTemplate(ctx context.Context, req Request, n serviceinstall.NodeContext, action, version, pkg, material, def string) ([]byte, error) {
	id := "NODE_INSTALL"
	if action == "uninstall" || action == "remove-environment" || action == "package-remove" {
		id = "NODE_UNINSTALL"
	}
	if action == "clean-version" {
		id = "DELETE_DATA"
	}
	if builtinStep(req, id) == nil {
		return runNode(ctx, n, action, version, pkg, material, req.JobID, def, x.softwareRunner)
	}
	// runNode creates empty npm configuration files and validates the selected
	// account before handing execution to the published script.
	return runNode(ctx, n, action, version, pkg, material, req.JobID, def, func(ctx context.Context, command string, args ...string) ([]byte, error) {
		marker := slices.Index(args, "sfops-node")
		if marker < 0 || len(args) != marker+9 {
			return nil, errors.New("NODE_COMMAND_INPUT_INVALID")
		}
		v := map[string]string{}
		for index, key := range []string{"ACTION", "ROOT", "VERSION", "PACKAGE", "MATERIAL", "JOB", "DEFAULT", "CONFIGS"} {
			v["OPS_NODE_"+key] = args[marker+1+index]
		}
		out, _, err := x.runBuiltinStep(ctx, req, id, v, nil, n.Account)
		return out, err
	})
}
