package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func validateManagementInput(req Request) error {
	m := req.Management
	if m == nil || m.Validate() != nil || req.Software == nil || req.Software.Validate(false) != nil || m.SoftwareID != req.Software.ID || m.BindingRevision != req.Software.Version || req.Installation != nil || req.Uninstallation != nil || req.Reconfiguration != nil || req.GitAccount != nil || req.GitKey != nil || req.GitRepository != nil || req.NodeEnvironment != nil || req.DockerAccess != nil || req.Plugin != nil || req.Firewall != nil || req.Candidate != nil || req.Baseline != nil || req.Binding != nil {
		return errors.New("MANAGEMENT_INPUT_INVALID")
	}
	if req.Action != "SOFTWARE_OPERATION_CHECK" && (req.SoftwareOperation != "SOFTWARE_OPERATION" || !Hex.MatchString(m.ResourceHash)) {
		return errors.New("MANAGEMENT_RESOURCE_BASELINE_REQUIRED")
	}
	op, _ := m.Definition.ManagementOperation(m.OperationKey)
	mode := "NATIVE"
	if req.Software.Kind == "DOCKER" || req.Software.Ownership != nil && req.Software.Ownership.RuntimeMode == "DOCKER" {
		mode = "DOCKER"
	}
	if !op.SupportsRuntime(mode) {
		return errors.New("MANAGEMENT_RUNTIME_UNSUPPORTED")
	}
	return nil
}

func (x *executor) managementRoot(s SoftwareRequest) string {
	var binding softwareBinding
	if readJSON(x.softwareFile(s.ID), &binding) == nil {
		var intent Request
		if readJSON(filepath.Join(x.root, "jobs", binding.JobID, "intent.json"), &intent) == nil {
			if installation := frozenManagementInstallation(intent); installation != nil && installation.SoftwareID == s.ID {
				return serviceinstall.InstallRoot(x.root, s.ID, installation.Config)
			}
		}
	}
	return filepath.Join(x.root, "software", s.ID)
}

func (x *executor) managementPreflight(req Request) serviceinstall.ManagementPreflight {
	p := serviceinstall.ManagementPreflight{Ready: true, Checks: []serviceinstall.Check{}, Resources: []serviceinstall.ResolvedResource{}}
	fail := func(code, message string) {
		p.Ready = false
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "MANAGEMENT", Status: "FAIL", Code: code, Message: message})
	}
	if err := validateManagementInput(req); err != nil {
		fail(err.Error(), "管理参数或软件身份无效")
		return p
	}
	if err := x.boundSoftware(*req.Software); err != nil {
		fail(err.Error(), "软件绑定已变化，请重新检查")
		return p
	}
	if req.Management.Root != x.managementRoot(*req.Software) {
		fail("MANAGEMENT_ROOT_MISMATCH", "软件工作目录与安装记录不一致")
		return p
	}
	if err := x.validateManagementInstallation(req); err != nil {
		fail(err.Error(), "原安装记录与当前绑定不一致，请重新读取软件")
		return p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner); err != nil {
		fail(err.Error(), "无法核对软件实际身份")
		return p
	}
	resources, err := req.Management.ResolveResources(req.Software.Locator)
	if err != nil {
		fail(err.Error(), "资源声明无法解析，请检查参数")
		return p
	}
	var current softwareBinding
	if readJSON(x.softwareFile(req.Software.ID), &current) != nil {
		fail("SOFTWARE_BINDING_DAMAGED", "软件绑定无法读取")
		return p
	}
	if _, err := serviceinstall.MergeManagementResourceClaims(*req.Management, current.Resources, resources); err != nil {
		fail(err.Error(), "请在此管理操作中明确声明网页入口端口")
		return p
	}
	if serviceinstall.ManagementReplacesWebPort(*req.Management) || serviceinstall.ManagementReplacesInstallationPorts(*req.Management) {
		var intent Request
		if readJSON(filepath.Join(x.root, "jobs", current.JobID, "intent.json"), &intent) != nil || !managementOriginPortsMatch(*req.Management, frozenManagementInstallation(intent)) {
			fail("MANAGEMENT_WEB_ENTRY_UNAVAILABLE", "此软件的原安装记录未声明对应的网页入口，请核对安装配置")
			return p
		}
	}
	others, err := x.otherManagedResources(req.Software.ID)
	if err != nil {
		fail(err.Error(), "无法确认已有软件资源，请检查原记录")
		return p
	}
	owned := x.bindingResources(current)
	for _, resource := range resources {
		if resource.Release && !slices.ContainsFunc(current.Resources, func(prior serviceinstall.ResolvedResource) bool { return serviceinstall.SameResource(prior, resource) }) {
			fail("MANAGEMENT_RESOURCE_NOT_OWNED", "该资源不属于此软件的已登记资源："+resource.Value)
			continue
		}
		for _, other := range others {
			if serviceinstall.ResourceConflict(resource, other) {
				fail("MANAGEMENT_RESOURCE_CONFLICT", "资源与其他软件冲突："+resource.Value)
				break
			}
		}
		resolved, e := x.inspectManagementResource(ctx, resource, owned, *req.Software, req.Management.Root)
		if e != nil {
			fail(e.Error(), "资源占用、身份或读取权限未确认："+resource.Value)
		} else {
			p.Resources = append(p.Resources, resolved)
		}
	}
	p.ResourceHash = Digest(p.Resources)
	if p.Ready {
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "MANAGEMENT", Status: "PASS", Message: "软件身份、参数及资源检查通过"})
	}
	return p
}

func managementOriginPortsMatch(m serviceinstall.ManagementRequest, original *serviceinstall.Request) bool {
	if original == nil || original.SoftwareID != m.SoftwareID {
		return false
	}
	if serviceinstall.ManagementReplacesWebPort(m) && serviceinstall.WebPortKey(original.Definition) != serviceinstall.WebPortKey(m.Definition) && (!serviceinstall.IsBuiltinCommand(m.Definition) || !serviceinstall.DeclaredInstallationPort(original.Definition, serviceinstall.WebPortKey(m.Definition), "TCP")) {
		return false
	}
	if serviceinstall.ManagementReplacesInstallationPorts(m) {
		op, _ := m.Definition.ManagementOperation(m.OperationKey)
		for _, resource := range op.Resources {
			if resource.Type == "PORT" && !resource.Release && serviceinstall.DeclaredInstallationPort(m.Definition, resource.Key, resource.Protocol) && !serviceinstall.DeclaredInstallationPort(original.Definition, resource.Key, resource.Protocol) {
				return false
			}
		}
	}
	return true
}

func (x *executor) bindingResources(binding softwareBinding) []serviceinstall.ResolvedResource {
	resources := []serviceinstall.ResolvedResource{}
	s := binding.Software
	if s.Ownership != nil {
		resources = append(resources, serviceinstall.ResolvedResource{Type: "PATH_WRITE", Value: x.managementRoot(s)})
	}
	if validOfficialOwnership(s) {
		for _, directory := range s.Ownership.Official.Paths.OwnedPaths {
			resources = append(resources, serviceinstall.ResolvedResource{Type: "PATH_WRITE", Value: directory})
		}
	}
	if s.Kind == "SYSTEMD" {
		resources = append(resources, serviceinstall.ResolvedResource{Type: "UNIT", Value: s.Locator})
	}
	if s.Kind == "DOCKER" {
		resources = append(resources, serviceinstall.ResolvedResource{Type: "CONTAINER", Value: s.Locator})
	}
	var intent Request
	if readJSON(filepath.Join(x.root, "jobs", binding.JobID, "intent.json"), &intent) == nil {
		if installation := frozenManagementInstallation(intent); installation != nil && installation.SoftwareID == s.ID {
			for _, port := range serviceinstall.PortMappings(installation.Definition, installation.Config) {
				resources = append(resources, serviceinstall.ResolvedResource{Key: port.Key, Type: "PORT", Value: strconv.Itoa(port.HostPort), BindAddress: port.BindAddress, Protocol: port.Protocol})
			}
			return serviceinstall.OverlayWebPortClaims(installation.Definition, resources, binding.Resources)
		}
	}
	return append(resources, binding.Resources...)
}

func (x *executor) otherManagedResources(id string) ([]serviceinstall.ResolvedResource, error) {
	files, err := filepath.Glob(filepath.Join(x.root, "software", "*.json"))
	if err != nil || len(files) > 1000 {
		return nil, errors.New("MANAGEMENT_BINDING_LIMIT")
	}
	resources := []serviceinstall.ResolvedResource{}
	for _, file := range files {
		var binding softwareBinding
		if readJSON(file, &binding) != nil {
			return nil, errors.New("SOFTWARE_BINDING_DAMAGED")
		}
		if binding.Software.ID != id {
			resources = append(resources, x.bindingResources(binding)...)
		}
	}
	return resources, nil
}

func resourceOwned(resource serviceinstall.ResolvedResource, owned []serviceinstall.ResolvedResource) bool {
	for _, prior := range owned {
		if resource.Type == "PATH_WRITE" && prior.Type == "PATH_WRITE" && (resource.Value == prior.Value || strings.HasPrefix(resource.Value, prior.Value+string(filepath.Separator))) {
			return true
		}
		if resource.Type == prior.Type && resource.Value == prior.Value && resource.Protocol == prior.Protocol && resource.BindAddress == prior.BindAddress {
			return true
		}
	}
	return false
}

func managementPathIdentity(file string) (string, bool, error) {
	for current := file; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", false, errors.New("MANAGEMENT_RESOURCE_SYMLINK")
		}
		if err != nil && !os.IsNotExist(err) {
			return "", false, errors.New("MANAGEMENT_RESOURCE_UNREADABLE")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	info, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return Digest("missing"), false, nil
	}
	if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
		return "", false, errors.New("MANAGEMENT_RESOURCE_UNREADABLE")
	}
	digest := ""
	if info.Mode().IsRegular() {
		if info.Size() > 4<<20 {
			return "", false, errors.New("MANAGEMENT_RESOURCE_FILE_TOO_LARGE")
		}
		if digest, err = fileSHA(file); err != nil {
			return "", false, errors.New("MANAGEMENT_RESOURCE_UNREADABLE")
		}
	}
	return Digest([]string{info.Mode().String(), strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10), digest}), true, nil
}

func (x *executor) inspectManagementResource(ctx context.Context, resource serviceinstall.ResolvedResource, owned []serviceinstall.ResolvedResource, s SoftwareRequest, root string) (serviceinstall.ResolvedResource, error) {
	current := resourceOwned(resource, owned)
	switch resource.Type {
	case "PATH_READ", "PATH_WRITE":
		identity, exists, err := managementPathIdentity(resource.Value)
		if err != nil {
			return resource, err
		}
		if resource.Type == "PATH_READ" && !exists {
			return resource, errors.New("MANAGEMENT_RESOURCE_MISSING")
		}
		if resource.Type == "PATH_WRITE" && exists && !current && resource.Value != root && !strings.HasPrefix(resource.Value, root+string(filepath.Separator)) {
			return resource, errors.New("MANAGEMENT_RESOURCE_NOT_OWNED")
		}
		resource.IdentityHash = identity
	case "PORT":
		port, _ := strconv.Atoi(resource.Value)
		proof := ""
		if availableServicePort(serviceinstall.PortMapping{BindAddress: resource.BindAddress, HostPort: port, Protocol: resource.Protocol}) != nil {
			if !x.softwareOwnsPort(ctx, s, resource) {
				return resource, errors.New("SERVICE_PORT_UNAVAILABLE")
			}
			proof = s.Locator
		}
		if _, err := x.softwareRunner(ctx, "/usr/bin/docker", "version", "--format", "{{.Server.Version}}"); err == nil {
			owner, e := x.servicePublishedPortFor(ctx, resource.BindAddress, port, resource.Protocol)
			if e != nil {
				return resource, errors.New("SERVICE_PORT_PUBLISHED")
			}
			if owner != "" {
				if s.Kind != "DOCKER" || !x.softwareOwnsPort(ctx, s, resource) {
					return resource, errors.New("SERVICE_PORT_PUBLISHED")
				}
				proof = s.Locator
			}
		}
		resource.IdentityHash = Digest([]string{resource.Type, resource.Value, resource.Protocol, resource.BindAddress, proof})
	case "UNIT":
		if !current {
			probe := SoftwareRequest{Kind: "SYSTEMD", Locator: resource.Value}
			if probe.Validate(true) != nil {
				return resource, errors.New("MANAGEMENT_RESOURCE_UNIT_INVALID")
			}
			data, err := x.sourceSoftwareCommand(ctx, "management-unit-load", resource.Value)
			if err != nil || strings.TrimSpace(string(data)) != "not-found" {
				return resource, errors.New("COMMAND_OBJECT_ALREADY_EXISTS")
			}
		}
		resource.IdentityHash = Digest([]string{resource.Type, resource.Value, strconv.FormatBool(current)})
	case "CONTAINER":
		data, err := x.sourceSoftwareCommand(ctx, "management-container-list")
		if err != nil {
			return resource, errors.New("COMMAND_OBJECT_CHECK_UNAVAILABLE")
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 || !Hex.MatchString(fields[0]) {
				return resource, errors.New("COMMAND_OBJECT_CHECK_UNAVAILABLE")
			}
			if (fields[0] == resource.Value || fields[1] == resource.Value) && (s.Kind != "DOCKER" || fields[0] != s.Locator) {
				return resource, errors.New("COMMAND_OBJECT_ALREADY_EXISTS")
			}
		}
		resource.IdentityHash = Digest([]string{resource.Type, resource.Value})
	}
	return resource, nil
}

// A historical claim is not proof that a currently busy socket still belongs
// to this instance. Read current unit/container metadata before reusing it.
func (x *executor) softwareOwnsPort(ctx context.Context, s SoftwareRequest, wanted serviceinstall.ResolvedResource) bool {
	values := []string{}
	if s.Kind == "SYSTEMD" {
		rows, err := readSoftwareUnitMetadata(ctx, []string{s.Locator}, x.softwareRunner)
		if err != nil {
			return false
		}
		values = rows[s.Locator].ListeningPorts
	} else if s.Kind == "DOCKER" {
		data, err := x.sourceSoftwareCommand(ctx, "management-container-ports", s.Locator)
		if err != nil {
			return false
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			mapping, address, ok := strings.Cut(line, " -> ")
			if !ok {
				continue
			}
			_, protocol, ok := strings.Cut(mapping, "/")
			if !ok {
				continue
			}
			values = append(values, address+"/"+protocol)
		}
	}
	for _, value := range values {
		address, protocol, ok := strings.Cut(value, "/")
		if !ok {
			continue
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			continue
		}
		if host == "*" {
			host = "0.0.0.0"
		}
		if serviceinstall.ResourceConflict(wanted, serviceinstall.ResolvedResource{Type: "PORT", Value: port, Protocol: strings.ToUpper(protocol), BindAddress: host}) {
			return true
		}
	}
	return false
}

func managementValues(m serviceinstall.ManagementRequest) (map[string]string, map[string]serviceinstall.CredentialReference, error) {
	op, err := m.Definition.ManagementOperation(m.OperationKey)
	if err != nil {
		return nil, nil, err
	}
	values, err := serviceinstall.ResolveParameters(m.Definition.Management.Parameters, m.Parameters, m.ParameterCredentials)
	if err != nil {
		return nil, nil, err
	}
	inputs, err := serviceinstall.ResolveParameters(op.Parameters, m.OperationParameters, m.OperationCredentials)
	if err != nil {
		return nil, nil, err
	}
	for key, value := range inputs {
		values[key] = value
	}
	refs := map[string]serviceinstall.CredentialReference{}
	for key, value := range m.ParameterCredentials {
		refs[key] = value
	}
	for key, value := range m.OperationCredentials {
		refs[key] = value
	}
	return values, refs, nil
}

func parameterEnvironment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, "OPS_PARAM_"+key+"="+values[key])
	}
	return env
}

func (x *executor) runParameterStep(ctx context.Context, req Request, root string, step serviceinstall.CommandStep, account *user.User, values map[string]string, refs map[string]serviceinstall.CredentialReference, stage string) ([]byte, error) {
	command := "/bin/bash"
	args := []string{"--noprofile", "--norc", "-e", "-o", "pipefail", "-c", managementStepScript(*req.Management, step)}
	home, username := "/root", "root"
	if step.RunAs == "ACCOUNT" {
		if account == nil {
			return nil, errors.New("COMMAND_ACCOUNT_INVALID")
		}
		command, home, username = "/usr/sbin/runuser", account.HomeDir, account.Username
		args = append([]string{"-u", account.Username, "--", "/bin/bash"}, args...)
	}
	resolved, err := filepath.EvalSymlinks(command)
	if err != nil || secureAncestors(resolved) != nil {
		return nil, errors.New("COMMAND_DEPENDENCY_MISSING")
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Dir = filepath.Join(root, "work")
	if step.WorkingDirectory != "" {
		cmd.Dir = commandPath(step.WorkingDirectory, root, req.Software.ID)
	}
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + home, "USER=" + username, "LOGNAME=" + username, "LC_ALL=C", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "DEBIAN_FRONTEND=noninteractive", "OPS_INSTALL_ROOT=" + root, "OPS_DATA_ROOT=" + filepath.Join(root, "data"), "OPS_CONFIG_ROOT=" + filepath.Join(root, "config"), "OPS_SOFTWARE_ID=" + req.Software.ID, "OPS_SOFTWARE_LOCATOR=" + req.Software.Locator}
	cmd.Env = append(cmd.Env, parameterEnvironment(values)...)
	cmd.Env = append(cmd.Env, "OPS_JOB_ID="+req.JobID, "OPS_RUNTIME_KIND="+req.Software.Kind, "OPS_OPERATION_KEY="+req.Management.OperationKey, "OPS_VERSION="+req.Management.Definition.Version, "OPS_ACCOUNT="+req.Management.Account)
	cmd.Env = append(cmd.Env, "OPS_OPERATION_PHASE="+managementOperationPhase(stage), "OPS_PREVIOUS_LOCATOR="+req.Software.Locator, "OPS_PREVIOUS_IDENTITY="+req.Software.IdentityHash, "OPS_PREVIOUS_STATE="+x.managementPreviousState(req))
	runtimeMode := "NATIVE"
	if req.Software.Kind == "DOCKER" {
		runtimeMode = "DOCKER"
	}
	if req.Software.Ownership != nil && req.Software.Ownership.RuntimeMode != "" {
		runtimeMode = req.Software.Ownership.RuntimeMode
	}
	cmd.Env = append(cmd.Env, "OPS_RUNTIME_MODE="+runtimeMode)
	if req.Software.Ownership != nil {
		cmd.Env = append(cmd.Env, "OPS_BINARY_PATH="+req.Software.Ownership.BinaryPath)
	}
	cleanup, err := x.appendParameterSecrets(cmd, req, account, refs)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	boundPackageCommand(cmd)
	buffer := &cappedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = commandOutputs(ctx, buffer)
	err = cmd.Run()
	return buffer.Bytes(), err
}

func (x *executor) appendParameterSecrets(cmd *exec.Cmd, req Request, account *user.User, refs map[string]serviceinstall.CredentialReference) (func(), error) {
	files := []string{}
	cleanup := func() {
		for _, file := range files {
			_ = os.Remove(file)
		}
	}
	for key, ref := range refs {
		value, ok := x.secrets[serviceinstall.SecretKey(ref.ID, ref.Revision)]
		if !ok {
			cleanup()
			return func() {}, errors.New("PARAMETER_SECRET_INVALID")
		}
		directory := filepath.Join(x.root, "jobs", req.JobID)
		accountStep := cmd.Path != "" && filepath.Base(cmd.Path) == "runuser"
		if accountStep {
			directory = cmd.Dir
		}
		file, e := os.CreateTemp(directory, ".ops-secret-"+key+"-")
		if e != nil {
			cleanup()
			return func() {}, e
		}
		name := file.Name()
		files = append(files, name)
		if _, e = file.WriteString(value); e == nil && accountStep {
			uid, e1 := strconv.Atoi(account.Uid)
			gid, e2 := strconv.Atoi(account.Gid)
			if e1 != nil || e2 != nil {
				e = errors.New("COMMAND_ACCOUNT_INVALID")
			} else {
				e = file.Chown(uid, gid)
			}
		}
		closeErr := file.Close()
		if e != nil {
			cleanup()
			return func() {}, e
		}
		if closeErr != nil {
			cleanup()
			return func() {}, closeErr
		}
		cmd.Env = append(cmd.Env, "OPS_SECRET_"+key+"_FILE="+name)
	}
	return cleanup, nil
}

func managementAccount(m serviceinstall.ManagementRequest) (*user.User, error) {
	op, _ := m.Definition.ManagementOperation(m.OperationKey)
	for _, step := range append(append(slices.Clone(op.Steps), op.VerifySteps...), op.RollbackSteps...) {
		if step.RunAs == "ACCOUNT" {
			account, err := user.Lookup(m.Account)
			if err != nil || account.Uid == "0" || account.Gid == "0" || !filepath.IsAbs(account.HomeDir) {
				return nil, errors.New("COMMAND_ACCOUNT_INVALID")
			}
			return account, nil
		}
	}
	return nil, nil
}

func (x *executor) runManagementSteps(req Request, dir string, steps []serviceinstall.CommandStep, stage string, account *user.User, values map[string]string, refs map[string]serviceinstall.CredentialReference) (string, error) {
	output := ""
	phase := "MANAGEMENT_EXECUTE"
	if stage == "VERIFY" {
		phase = "MANAGEMENT_VERIFY"
	} else if stage == "ROLLBACK" {
		phase = "MANAGEMENT_ROLLBACK"
	}
	rollbackDeadline := time.Now().Add(90 * time.Second)
	for index, step := range steps {
		file := filepath.Join(dir, fmt.Sprintf("management-%s-step-%02d.json", strings.ToLower(stage), index+1))
		if _, err := os.Lstat(file); !os.IsNotExist(err) {
			return output, errors.New("COMMAND_STEP_RECONCILE_REQUIRED")
		}
		receipt := commandStepReceipt{Phase: phase, Name: step.Name, State: "RUNNING", StartedAt: stamp()}
		if err := atomicJSON(file, receipt); err != nil {
			return output, err
		}
		if err := x.checkpoint(req, phase); err != nil {
			return output, err
		}
		x.appendServiceLog(req, phase, fmt.Sprintf("%s [%s]\n$ %s", step.Name, step.RunAs, managementStepScript(*req.Management, step)))
		limit := time.Duration(step.TimeoutSeconds) * time.Second
		if stage == "ROLLBACK" {
			limit = min(limit, time.Until(rollbackDeadline))
		} else if deadline, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil {
			limit = min(limit, time.Until(deadline))
		}
		if limit <= 0 {
			return output, errors.New("DEADLINE_EXCEEDED")
		}
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		logged, closeLog := x.installationLogContext(ctx, req, phase)
		data, err := x.runParameterStep(logged, req, req.Management.Root, step, account, values, refs, stage)
		closeLog()
		interrupted := ctx.Err() != nil
		cancel()
		output = x.redactSoftwareSecrets(string(data))
		receipt.FinishedAt, receipt.State = stamp(), "SUCCEEDED"
		if err != nil {
			receipt.State = "FAILED"
		}
		if writeErr := atomicJSON(file, receipt); writeErr != nil {
			return output, writeErr
		}
		if interrupted {
			return output, errors.New("COMMAND_STEP_INTERRUPTED")
		}
		if err != nil {
			return output, errors.New("COMMAND_STEP_FAILED")
		}
	}
	return output, nil
}

func (x *executor) applyManagement(req Request, dir string) (Result, error) {
	r := softwareResult(req)
	m := *req.Management
	preflight := x.managementPreflight(req)
	if !preflight.Ready {
		return r, errors.New("MANAGEMENT_PREFLIGHT_REJECTED")
	}
	if m.ResourceHash != preflight.ResourceHash {
		return r, errors.New("MANAGEMENT_RESOURCES_CHANGED")
	}
	if err := serviceinstall.ValidateCredentialValues(m.CredentialReferences(), x.secrets); err != nil {
		return r, err
	}
	account, err := managementAccount(m)
	if err != nil {
		return r, err
	}
	values, refs, err := managementValues(m)
	if err != nil {
		return r, err
	}
	work := filepath.Join(m.Root, "work")
	if err = os.MkdirAll(work, 0755); err != nil {
		return r, err
	}
	if _, _, err = managementPathIdentity(work); err != nil {
		return r, err
	}
	if account != nil {
		uid, _ := strconv.Atoi(account.Uid)
		gid, _ := strconv.Atoi(account.Gid)
		if err = os.Chown(work, uid, gid); err != nil {
			return r, err
		}
	}
	if err = atomicJSON(filepath.Join(dir, "management-started.json"), m); err != nil {
		return r, err
	}
	op, _ := m.Definition.ManagementOperation(m.OperationKey)
	if len(op.RollbackSteps) > 0 || op.ReadOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		before, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
		cancel()
		if err != nil || len(op.RollbackSteps) > 0 && before.State != "RUNNING" && before.State != "STOPPED" {
			return r, errors.New("MANAGEMENT_STATE_UNCONFIRMED")
		}
		if err = atomicJSON(filepath.Join(dir, "management-before.json"), managementBefore{InputHash: req.InputHash, State: before.State}); err != nil {
			return r, err
		}
	}
	output, stepErr := x.runManagementSteps(req, dir, op.Steps, "APPLY", account, values, refs)
	if stepErr == nil {
		_, stepErr = x.runManagementSteps(req, dir, op.VerifySteps, "VERIFY", account, values, refs)
	}
	if stepErr != nil {
		if len(op.RollbackSteps) > 0 {
			return x.restoreManagement(req, dir, op, stepErr), nil
		}
		r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", stepErr.Error()
		return r, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resultRequest := req
	var candidate SoftwareRequest
	var candidateObservation *SoftwareObservation
	if op.Rebind {
		candidate, candidateObservation, err = x.managementCandidate(ctx, req)
		resultRequest.Software = &candidate
	} else {
		_, err = ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	}
	if err != nil {
		if len(op.RollbackSteps) > 0 {
			return x.restoreManagement(req, dir, op, err), nil
		}
		r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", err.Error()
		return r, nil
	}
	resources, err := x.managementResultResources(ctx, resultRequest, preflight.Resources)
	if err != nil {
		if len(op.RollbackSteps) > 0 {
			return x.restoreManagement(req, dir, op, err), nil
		}
		r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", err.Error()
		return r, nil
	}
	evidence := &serviceinstall.ManagementEvidence{SoftwareID: m.SoftwareID, OperationKey: m.OperationKey, DefinitionHash: serviceinstall.Digest(m.Definition), ParametersHash: m.ParameterHash(), ResourceHash: m.ResourceHash, ReadOnly: op.ReadOnly, Output: output, Resources: resources, ObservedAt: stamp()}
	if op.ResultFormat == "TABLE" {
		var table serviceinstall.ManagementTable
		if StrictJSON([]byte(output), &table) != nil {
			if op.Rebind {
				return x.restoreManagement(req, dir, op, errors.New("MANAGEMENT_TABLE_INVALID")), nil
			}
			r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", "MANAGEMENT_TABLE_INVALID"
			return r, nil
		}
		for i, row := range table.Rows {
			for key, value := range row {
				table.Rows[i][key] = x.redactSoftwareSecrets(value)
			}
		}
		evidence.Table = &table
		evidence.Output = ""
	}
	if evidence.ValidateFor(m) != nil {
		if op.Rebind {
			return x.restoreManagement(req, dir, op, errors.New("MANAGEMENT_RECEIPT_MISMATCH")), nil
		}
		r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", "MANAGEMENT_RECEIPT_MISMATCH"
		return r, nil
	}
	if op.Rebind {
		var binding softwareBinding
		if readJSON(x.softwareFile(req.Software.ID), &binding) != nil {
			r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", "SOFTWARE_BINDING_DAMAGED"
			return r, nil
		}
		receipt := newManagementRebindReceipt(req, binding, candidate, *candidateObservation, resources)
		if receipt.validate(req) != nil || atomicJSON(filepath.Join(dir, "management-rebind.json"), receipt) != nil {
			r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", "MANAGEMENT_REBIND_RECEIPT_WRITE_FAILED"
			return r, nil
		}
	}
	if err = atomicJSON(filepath.Join(dir, "management-verified.json"), evidence); err != nil {
		r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", "MANAGEMENT_RECEIPT_WRITE_FAILED"
		return r, nil
	}
	completed, completeErr := x.completeManagement(req, dir, *evidence)
	if completeErr != nil {
		r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", completeErr.Error()
		return r, nil
	}
	return completed, nil
}

func (x *executor) completeManagement(req Request, dir string, evidence serviceinstall.ManagementEvidence) (Result, error) {
	r := softwareResult(req)
	if evidence.ValidateFor(*req.Management) != nil {
		return r, errors.New("MANAGEMENT_RECEIPT_MISMATCH")
	}
	if managementRebind(req) {
		return x.completeManagementRebind(req, dir, evidence)
	}
	if x.boundSoftware(*req.Software) != nil {
		return r, errors.New("MANAGEMENT_RECEIPT_MISMATCH")
	}
	var binding softwareBinding
	if readJSON(x.softwareFile(req.Software.ID), &binding) != nil {
		return r, errors.New("SOFTWARE_BINDING_DAMAGED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil {
		r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", err.Error()
		return r, nil
	}
	if !evidence.ReadOnly {
		binding.Resources = evidence.Resources
		if err := atomicJSON(x.softwareFile(req.Software.ID), binding); err != nil {
			r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", "MANAGEMENT_BINDING_WRITE_FAILED"
			return r, nil
		}
	}
	r.Status, r.Phase, r.Management, r.Software = "SUCCEEDED", "SOFTWARE_OPERATION_COMPLETED", &evidence, actual
	return r, nil
}

func (x *executor) managementResultResources(ctx context.Context, req Request, changes []serviceinstall.ResolvedResource) ([]serviceinstall.ResolvedResource, error) {
	var binding softwareBinding
	if readJSON(x.softwareFile(req.Software.ID), &binding) != nil {
		return nil, errors.New("SOFTWARE_BINDING_DAMAGED")
	}
	op, _ := req.Management.Definition.ManagementOperation(req.Management.OperationKey)
	if op.ReadOnly {
		return slices.Clone(binding.Resources), nil
	}
	owned := append(x.bindingResources(binding), changes...)
	resolved := slices.Clone(changes)
	for index, resource := range changes {
		if resource.Release {
			if err := x.managementResourceReleased(ctx, resource); err != nil {
				return nil, err
			}
			continue
		}
		actual, err := x.inspectManagementResource(ctx, resource, owned, *req.Software, req.Management.Root)
		if err != nil {
			return nil, err
		}
		resolved[index] = actual
	}
	return serviceinstall.MergeManagementResourceClaims(*req.Management, binding.Resources, resolved)
}

func (x *executor) managementResourceReleased(ctx context.Context, resource serviceinstall.ResolvedResource) error {
	confirmed := false
	switch resource.Type {
	case "PATH_READ", "PATH_WRITE":
		_, exists, err := managementPathIdentity(resource.Value)
		confirmed = err == nil && !exists
	case "PORT":
		port, _ := strconv.Atoi(resource.Value)
		confirmed = availableServicePort(serviceinstall.PortMapping{BindAddress: resource.BindAddress, HostPort: port, Protocol: resource.Protocol}) == nil
		if _, err := x.softwareRunner(ctx, "/usr/bin/docker", "version", "--format", "{{.Server.Version}}"); err == nil {
			owner, err := x.servicePublishedPortFor(ctx, resource.BindAddress, port, resource.Protocol)
			confirmed = confirmed && err == nil && owner == ""
		}
	case "UNIT":
		data, err := x.sourceSoftwareCommand(ctx, "management-unit-load", resource.Value)
		confirmed = err == nil && strings.TrimSpace(string(data)) == "not-found"
	case "CONTAINER":
		data, err := x.sourceSoftwareCommand(ctx, "management-container-list")
		confirmed = err == nil
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 || !Hex.MatchString(fields[0]) || fields[0] == resource.Value || fields[1] == resource.Value {
				confirmed = false
			}
		}
	}
	if !confirmed {
		return errors.New("MANAGEMENT_RESOURCE_RELEASE_UNCONFIRMED")
	}
	return nil
}

func (x *executor) reconcileManagement(req Request, dir string) Result {
	r := softwareResult(req)
	r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED"
	var restored Result
	if readJSON(filepath.Join(dir, "management-restored.json"), &restored) == nil && validManagementRestoredResult(req, restored) && x.boundSoftware(*req.Software) == nil {
		return restored
	}
	var evidence serviceinstall.ManagementEvidence
	if readJSON(filepath.Join(dir, "management-verified.json"), &evidence) != nil || evidence.ValidateFor(*req.Management) != nil {
		return r
	}
	completed, err := x.completeManagement(req, dir, evidence)
	if err != nil {
		return r
	}
	return completed
}

func (x *executor) verifyManagementEvidence(req Request, result Result) error {
	if result.Management == nil {
		return errors.New("MANAGEMENT_RECEIPT_MISMATCH")
	}
	if err := result.Management.ValidateFor(*req.Management); err != nil {
		return err
	}
	if managementRebind(req) {
		var receipt managementRebindReceipt
		if readJSON(filepath.Join(x.root, "jobs", req.JobID, "management-rebind.json"), &receipt) != nil || receipt.validate(req) != nil || receipt.Software.State != x.managementPreviousState(req) || !reflect.DeepEqual(result.Installation, &receipt.Installation) || !reflect.DeepEqual(result.Software, &receipt.Software) || x.boundSoftware(receipt.Binding.Software) != nil {
			return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
		}
	} else if result.Installation != nil || result.Software == nil || result.Software.IdentityHash != req.Software.IdentityHash || result.Software.Locator != req.Software.Locator {
		return errors.New("MANAGEMENT_RECEIPT_MISMATCH")
	}
	return nil
}
