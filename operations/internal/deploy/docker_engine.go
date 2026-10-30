package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Engine installation uses the distribution package manager and immutable
// official DEBs. It never changes apt sources, downloads target dependencies,
// upgrades an existing runtime, or adopts pre-existing container data.
type dockerPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type dockerPackageOwnership struct {
	SoftwareID string          `json:"softwareId"`
	JobID      string          `json:"jobId"`
	ConfigHash string          `json:"configHash"`
	Root       string          `json:"root"`
	DaemonHash string          `json:"daemonHash"`
	Packages   []dockerPackage `json:"packages"`
	UnitPath   string          `json:"unitPath,omitempty"`
	UnitHash   string          `json:"unitHash,omitempty"`
	BinaryHash string          `json:"binaryHash,omitempty"`
}

// A preparation error proves no package or data removal command was invoked.
// Later failures remain UNKNOWN and must be reconciled without replay.
type dockerRemovalNotStartedError struct{ code string }

func (e *dockerRemovalNotStartedError) Error() string { return e.code }

func bytesSHA(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func dockerRecipePackages(d serviceinstall.Definition) ([]dockerPackage, error) {
	if d.Key != "docker" || d.Native == nil || d.Native.BuildRecipe != "DEB_PACKAGE" || serviceinstall.ValidateDefinition(d) != nil {
		return nil, errors.New("DOCKER_PACKAGE_RECIPE_INVALID")
	}
	packages := make([]dockerPackage, 0, len(d.Native.Parts))
	for _, part := range d.Native.Parts {
		fields := strings.Split(strings.TrimSuffix(part.Name, ".deb"), "_")
		if len(fields) != 3 || fields[2] != "amd64" {
			return nil, errors.New("DOCKER_PACKAGE_RECIPE_INVALID")
		}
		version := fields[1]
		if fields[0] == "docker-ce" || fields[0] == "docker-ce-cli" {
			version = "5:" + version
		}
		packages = append(packages, dockerPackage{Name: fields[0], Version: version})
	}
	return packages, nil
}

func (x *executor) installedDockerPackages(ctx context.Context) (map[string]string, error) {
	// Query once, including packages in incomplete install states. A truncated
	// list cannot establish absence and fails closed.
	out, err := x.softwareRunner(ctx, "/usr/bin/dpkg-query", "--show", "--showformat=${binary:Package}\t${db:Status-Abbrev}\t${Version}\n")
	if err != nil || len(out) >= 64<<10 {
		return nil, errors.New("DOCKER_PACKAGE_STATE_UNAVAILABLE")
	}
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || len(fields[1]) != 3 {
			return nil, errors.New("DOCKER_PACKAGE_STATE_UNAVAILABLE")
		}
		name := strings.TrimSuffix(fields[0], ":amd64")
		if fields[1] == "ii " {
			result[name] = fields[2]
		} else if fields[1][1] != 'n' && fields[1][1] != 'c' {
			// Incomplete package transactions must be repaired by the host admin.
			return nil, errors.New("DOCKER_PACKAGE_TRANSACTION_PENDING")
		}
	}
	return result, nil
}

func (x *executor) dockerEnginePreflight(ctx context.Context) error {
	packages, err := x.installedDockerPackages(ctx)
	if err != nil {
		return err
	}
	for _, name := range []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin", "docker.io", "docker-compose", "docker-compose-v2", "docker-doc", "docker-buildx", "podman-docker", "containerd", "runc"} {
		if packages[name] != "" {
			return errors.New("DOCKER_EXISTING_RUNTIME_" + name)
		}
	}
	for _, file := range []string{"/usr/bin/docker", "/usr/bin/dockerd", "/usr/bin/containerd", "/usr/bin/runc", "/run/docker.sock", "/etc/docker/daemon.json", "/etc/containerd/config.toml", "/var/lib/docker", "/var/lib/containerd", "/etc/systemd/system/docker.service", "/etc/systemd/system/docker.socket", "/etc/systemd/system/containerd.service", "/etc/systemd/system/docker.service.d", "/etc/systemd/system/containerd.service.d", "/usr/lib/systemd/system/docker.service", "/usr/lib/systemd/system/containerd.service"} {
		if _, err := os.Lstat(file); !os.IsNotExist(err) {
			return errors.New("DOCKER_EXISTING_CONFIGURATION_OR_DATA")
		}
	}
	for _, name := range []string{"iptables", "nftables", "libc6", "libseccomp2", "libsystemd0"} {
		if packages[name] == "" {
			return errors.New("DOCKER_OS_DEPENDENCY_MISSING_" + name)
		}
	}
	return nil
}

func dockerPreflightMessage(err error) string {
	if strings.HasPrefix(err.Error(), "DOCKER_OS_DEPENDENCY_MISSING_") {
		return "主机缺少系统依赖 " + strings.TrimPrefix(err.Error(), "DOCKER_OS_DEPENDENCY_MISSING_") + "；请管理员先准备该 Ubuntu 软件包，安装过程不会联网补装"
	}
	if strings.HasPrefix(err.Error(), "DOCKER_EXISTING_RUNTIME_") {
		return "主机已安装 " + strings.TrimPrefix(err.Error(), "DOCKER_EXISTING_RUNTIME_") + "；请使用主机的发现登记入口，本次安装不会替换原运行环境"
	}
	if err.Error() == "DOCKER_EXISTING_CONFIGURATION_OR_DATA" {
		return "发现既有 Docker/containerd 配置或数据，已保留；请管理员核对原安装或决定如何恢复，平台不会覆盖"
	}
	return "无法确认 Docker 包管理状态，请管理员先检查未完成的软件包操作；未更改主机软件"
}

var aptInstallLine = regexp.MustCompile(`^Inst ([a-z0-9][a-z0-9+.-]*)(?::amd64)? \(([^ ]+)(?: |\))`)
var aptRemoveLine = regexp.MustCompile(`^Remv ([a-z0-9][a-z0-9+.-]*)(?::amd64)?(?: |$)`)

func validateDockerAptPlan(output []byte, packages []dockerPackage, remove bool) error {
	if len(output) >= 64<<10 {
		return errors.New("DOCKER_PACKAGE_PLAN_TOO_LARGE")
	}
	wanted, seen := map[string]string{}, map[string]bool{}
	for _, p := range packages {
		wanted[p.Name] = p.Version
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "Inst ") {
			match := aptInstallLine.FindStringSubmatch(line)
			if remove || len(match) != 3 || wanted[match[1]] != match[2] || seen[match[1]] {
				return errors.New("DOCKER_PACKAGE_PLAN_CHANGES_OTHER_SOFTWARE")
			}
			seen[match[1]] = true
		}
		if strings.HasPrefix(line, "Remv ") {
			match := aptRemoveLine.FindStringSubmatch(line)
			if !remove || len(match) != 2 || wanted[match[1]] == "" || seen[match[1]] {
				return errors.New("DOCKER_PACKAGE_PLAN_CHANGES_OTHER_SOFTWARE")
			}
			seen[match[1]] = true
		}
		if strings.HasPrefix(line, "Purg ") {
			return errors.New("DOCKER_PACKAGE_PLAN_CHANGES_OTHER_SOFTWARE")
		}
	}
	if len(seen) != len(wanted) {
		return errors.New("DOCKER_PACKAGE_PLAN_INCOMPLETE")
	}
	return nil
}

func dockerAptArgs(simulate, remove bool, files []string) []string {
	args := []string{"--no-download", "--no-install-recommends", "-o", "APT::Get::AutomaticRemove=false", "-o", "DPkg::Lock::Timeout=15", "-o", "Dpkg::Options::=--force-confold"}
	if simulate {
		args = append(args, "--simulate")
	} else {
		args = append(args, "--assume-yes")
	}
	if remove {
		args = append(args, "remove")
	} else {
		args = append(args, "--no-remove", "install")
	}
	return append(append(args, "--"), files...)
}

func dockerDaemonConfig(root string) []byte {
	// Keep all image/volume data in this instance. Docker29's containerd image
	// store is disabled explicitly, leaving system containerd metadata retained.
	return []byte(fmt.Sprintf("{\"data-root\":%q,\"features\":{\"containerd-snapshotter\":false},\"log-driver\":\"json-file\",\"log-opts\":{\"max-size\":\"10m\",\"max-file\":\"3\"}}\n", filepath.Join(root, "data")))
}

func (x *executor) installDockerEngine(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	preflight := x.nativeInstallPreflight(*i)
	for _, c := range preflight.Checks {
		if c.Status != "PASS" {
			return result, errors.New(c.Code)
		}
	}
	if err := x.checkpoint(req, "DOCKER_MATERIAL"); err != nil {
		return result, err
	}
	parts, err := x.receiveNativeMaterial(req, dir)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeDeadline(req))
	defer cancel()
	packages, err := dockerRecipePackages(i.Definition)
	if err != nil {
		return result, err
	}
	files := make([]string, 0, len(packages))
	for index, p := range packages {
		file := parts[i.Definition.Native.Parts[index].Name]
		if file == "" {
			return result, errors.New("DOCKER_PACKAGE_MISSING")
		}
		out, fieldErr := x.softwareRunner(ctx, "/usr/bin/dpkg-deb", "--show", "--showformat=${Package}\t${Version}\t${Architecture}\n", file)
		if fieldErr != nil || string(out) != p.Name+"\t"+p.Version+"\tamd64\n" {
			return result, errors.New("DOCKER_PACKAGE_IDENTITY_MISMATCH")
		}
		files = append(files, file)
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/apt-get", dockerAptArgs(true, false, files)...)
	x.appendServiceLog(req, "DOCKER_PACKAGE_PLAN", string(out))
	if err != nil {
		return result, errors.New("DOCKER_OFFLINE_DEPENDENCIES_MISSING")
	}
	if err = validateDockerAptPlan(out, packages, false); err != nil {
		return result, err
	}
	if err = x.dockerEnginePreflight(ctx); err != nil {
		return result, err
	}
	root := preflight.Root
	if err = ensureNativeParent(root); err != nil {
		return result, err
	}
	if err = os.Mkdir(root, 0750); err != nil {
		return result, err
	}
	for _, name := range []string{"data", "config", "logs", "bin", "runtime"} {
		if err = os.Mkdir(filepath.Join(root, name), 0750); err != nil {
			return result, err
		}
	}
	daemon := dockerDaemonConfig(root)
	marker := dockerPackageOwnership{SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), Root: root, DaemonHash: bytesSHA(daemon), Packages: packages}
	// Pin the exact program and unit bytes from the already validated official
	// DEB before package mutation. Reconciliation can then prove ownership even
	// if the process exits immediately after apt finishes.
	expectedDir := filepath.Join(dir, "docker-expected")
	if err = os.Mkdir(expectedDir, 0700); err != nil {
		return result, err
	}
	var enginePart string
	for name, file := range parts {
		if strings.HasPrefix(name, "docker-ce_") {
			enginePart = file
		}
	}
	if enginePart == "" {
		return result, errors.New("DOCKER_PACKAGE_MISSING")
	}
	if err = extractDebPayload(ctx, enginePart, expectedDir, map[string]string{"usr/bin/dockerd": "dockerd", "usr/lib/systemd/system/docker.service": "docker.service"}); err != nil {
		return result, err
	}
	marker.UnitPath = "/usr/lib/systemd/system/docker.service"
	marker.UnitHash, err = fileSHA(filepath.Join(expectedDir, "docker.service"))
	if err != nil {
		return result, err
	}
	marker.BinaryHash, err = fileSHA(filepath.Join(expectedDir, "dockerd"))
	if err != nil {
		return result, err
	}
	// The durable marker precedes config and package writes. Any subsequent
	// interruption is UNKNOWN and only observation may complete the job.
	if err = atomicJSON(filepath.Join(dir, "docker-packages.json"), marker); err != nil {
		return result, err
	}
	unknown := func() (Result, error) { result.ErrorCode = "DOCKER_PACKAGE_RECONCILE_REQUIRED"; return result, nil }
	if err = os.MkdirAll("/etc/docker", 0755); err != nil || secureAncestors("/etc/docker") != nil {
		return unknown()
	}
	if err = serviceFile("/etc/docker/daemon.json", daemon, 0, false); err != nil {
		return unknown()
	}
	if err = serviceFile(filepath.Join(root, "config/daemon.json"), daemon, 0, false); err != nil {
		return unknown()
	}
	if err = x.checkpoint(req, "DOCKER_PACKAGES_APPLY"); err != nil {
		return unknown()
	}
	out, err = x.softwareRunner(ctx, "/usr/bin/apt-get", dockerAptArgs(false, false, files)...)
	x.appendServiceLog(req, "DOCKER_PACKAGES_APPLY", string(out))
	if err != nil {
		return unknown()
	}
	if err = x.completeDockerOwnership(ctx, req, dir, &marker); err != nil {
		return unknown()
	}
	if _, err = x.softwareRunner(ctx, "/usr/bin/systemctl", "enable", "--now", "docker.service"); err != nil {
		return unknown()
	}
	return x.dockerEngineInstalledResult(ctx, req, dir, marker), nil
}

func (x *executor) completeDockerOwnership(ctx context.Context, req Request, dir string, marker *dockerPackageOwnership) error {
	installed, err := x.installedDockerPackages(ctx)
	if err != nil {
		return err
	}
	for _, p := range marker.Packages {
		if installed[p.Name] != p.Version {
			return errors.New("DOCKER_PACKAGE_VERSION_MISMATCH")
		}
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--property=FragmentPath", "--value", "docker.service")
	if err != nil {
		return errors.New("DOCKER_UNIT_UNAVAILABLE")
	}
	unit := strings.TrimSpace(string(out))
	resolved, err := filepath.EvalSymlinks(unit)
	if err != nil || resolved != "/usr/lib/systemd/system/docker.service" || secureAncestors(resolved) != nil {
		return errors.New("DOCKER_UNIT_IDENTITY_MISMATCH")
	}
	unitHash, err := fileSHA(resolved)
	if err != nil || marker.UnitPath != resolved || marker.UnitHash != unitHash {
		return errors.New("DOCKER_UNIT_IDENTITY_MISMATCH")
	}
	if secureAncestors("/usr/bin/dockerd") != nil {
		return errors.New("DOCKER_BINARY_NOT_PROTECTED")
	}
	binaryHash, err := fileSHA("/usr/bin/dockerd")
	if err != nil || marker.BinaryHash != binaryHash {
		return errors.New("DOCKER_BINARY_IDENTITY_MISMATCH")
	}
	if err = atomicJSON(filepath.Join(dir, "docker-packages.json"), marker); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(dir, "native-owned.json"), nativeInstallation{SoftwareID: marker.SoftwareID, JobID: marker.JobID, ConfigHash: marker.ConfigHash, BinaryPath: "/usr/bin/dockerd", BinaryHash: marker.BinaryHash, UnitHash: marker.UnitHash})
}

func (x *executor) dockerEngineInstalledResult(ctx context.Context, req Request, dir string, marker dockerPackageOwnership) Result {
	result := softwareResult(req)
	result.ErrorCode = "DOCKER_PACKAGE_RECONCILE_REQUIRED"
	if marker.BinaryHash == "" || marker.UnitHash == "" {
		return result
	}
	if err := x.completeDockerOwnership(ctx, req, dir, &marker); err != nil {
		return result
	}
	for file, hash := range map[string]string{"/usr/bin/dockerd": marker.BinaryHash, marker.UnitPath: marker.UnitHash, "/etc/docker/daemon.json": marker.DaemonHash} {
		actual, err := fileSHA(file)
		if err != nil || actual != hash {
			return result
		}
	}
	owner := nativeInstallation{SoftwareID: marker.SoftwareID, JobID: marker.JobID, ConfigHash: marker.ConfigHash, BinaryPath: "/usr/bin/dockerd", BinaryHash: marker.BinaryHash, UnitHash: marker.UnitHash}
	s := x.nativeSoftware(req, owner, "")
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return result
	}
	s.IdentityHash = actual.IdentityHash
	if _, err = os.Lstat(x.softwareFile(s.ID)); err == nil {
		if x.boundSoftware(s) != nil {
			return result
		}
	} else if !os.IsNotExist(err) {
		return result
	} else if atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID}) != nil {
		return result
	}
	version, err := x.dockerEngineHealth(ctx, req)
	if err != nil {
		return result
	}
	return x.ownedInstallResult(req, s, version, "READY", "SUCCEEDED")
}

func (x *executor) dockerEngineHealth(ctx context.Context, req Request) (string, error) {
	i := req.Installation
	out, err := x.softwareRunner(ctx, "/usr/bin/docker", "info", "--format", "{{.ServerVersion}}\t{{.DockerRootDir}}")
	version := strings.Split(i.Definition.Version, "-")[0]
	want := version + "\t" + filepath.Join(serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config), "data")
	if err != nil || strings.TrimSpace(string(out)) != want {
		return "", errors.New("DOCKER_ENGINE_HEALTH_MISMATCH")
	}
	compose, err := x.softwareRunner(ctx, "/usr/bin/docker", "compose", "version", "--short")
	if err != nil || strings.TrimPrefix(strings.TrimSpace(string(compose)), "v") != "5.6.1" {
		return "", errors.New("DOCKER_COMPOSE_VERSION_MISMATCH")
	}
	return version, nil
}

func (x *executor) reconcileDockerEngine(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "DOCKER_PACKAGE_RECONCILE_REQUIRED"
	var marker dockerPackageOwnership
	if readJSON(filepath.Join(dir, "docker-packages.json"), &marker) != nil || marker.SoftwareID != req.Installation.SoftwareID || marker.JobID != req.JobID || marker.ConfigHash != Digest(req.Installation.Config) || marker.Root != serviceinstall.InstallRoot(x.root, marker.SoftwareID, req.Installation.Config) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// No apt or service command is retried after uncertainty. Only exact fixed
	// packages and bytes plus service health can complete a lost receipt.
	return x.dockerEngineInstalledResult(ctx, req, dir, marker)
}

func (x *executor) dockerEngineHasNoContainers(ctx context.Context) error {
	for _, pattern := range []string{filepath.Join(x.root, "software", "*.json"), filepath.Join(x.root, "plugins", "*.json")} {
		files, err := filepath.Glob(pattern)
		if err != nil || len(files) > 1000 {
			return errors.New("DOCKER_CONTAINER_DEPENDENCIES_UNCONFIRMED")
		}
		for _, file := range files {
			if filepath.Base(filepath.Dir(file)) == "plugins" {
				return errors.New("DOCKER_ENGINE_HAS_MANAGED_DEPENDENCIES")
			}
			var binding softwareBinding
			if readJSON(file, &binding) != nil {
				return errors.New("DOCKER_CONTAINER_DEPENDENCIES_UNCONFIRMED")
			}
			if binding.Software.Kind == "DOCKER" || binding.Software.Kind == "TOOL" && binding.Software.Ownership != nil && binding.Software.Ownership.RuntimeMode != "NATIVE" {
				return errors.New("DOCKER_ENGINE_HAS_MANAGED_DEPENDENCIES")
			}
		}
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "ls", "--all", "--no-trunc", "--format", "{{.ID}}")
	if err != nil || len(out) >= 64<<10 {
		return errors.New("DOCKER_CONTAINER_DEPENDENCIES_UNCONFIRMED")
	}
	if strings.TrimSpace(string(out)) != "" {
		return errors.New("DOCKER_ENGINE_HAS_DEPENDENT_CONTAINERS")
	}
	return nil
}

func (x *executor) dockerRemovalOwnership(req Request) (dockerPackageOwnership, error) {
	var marker dockerPackageOwnership
	i, o := req.Uninstallation.Installation, req.Software.Ownership
	dir := filepath.Join(x.root, "jobs", o.JobID)
	expected, err := dockerRecipePackages(i.Definition)
	if err != nil || readJSON(filepath.Join(dir, "docker-packages.json"), &marker) != nil || marker.SoftwareID != i.SoftwareID || marker.JobID != o.JobID || marker.ConfigHash != o.ConfigHash || marker.Root != serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config) || Digest(marker.Packages) != Digest(expected) || marker.BinaryHash != o.BinaryHash || marker.UnitPath != "/usr/lib/systemd/system/docker.service" || !Hex.MatchString(marker.UnitHash) || marker.DaemonHash != bytesSHA(dockerDaemonConfig(marker.Root)) {
		return marker, errors.New("DOCKER_PACKAGE_OWNERSHIP_MISMATCH")
	}
	return marker, nil
}

func (x *executor) preflightDockerEngineUninstall(ctx context.Context, req Request) error {
	marker, err := x.dockerRemovalOwnership(req)
	if err != nil {
		return err
	}
	for file, hash := range map[string]string{"/etc/docker/daemon.json": marker.DaemonHash, "/usr/bin/dockerd": marker.BinaryHash, marker.UnitPath: marker.UnitHash} {
		actual, err := fileSHA(file)
		if err != nil || actual != hash {
			return errors.New("DOCKER_PACKAGE_OWNERSHIP_DRIFT")
		}
	}
	if err = x.dockerEngineHasNoContainers(ctx); err != nil {
		return err
	}
	installed, err := x.installedDockerPackages(ctx)
	if err != nil {
		return err
	}
	names := []string{}
	for _, p := range marker.Packages {
		if installed[p.Name] != p.Version {
			return errors.New("DOCKER_PACKAGE_VERSION_DRIFT")
		}
		names = append(names, p.Name)
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/apt-get", dockerAptArgs(true, true, names)...)
	if err != nil {
		return errors.New("DOCKER_PACKAGE_REMOVE_PLAN_FAILED")
	}
	return validateDockerAptPlan(out, marker.Packages, true)
}

func (x *executor) removeDockerEnginePackages(ctx context.Context, req Request, dir string) error {
	marker, err := x.dockerRemovalOwnership(req)
	if err != nil {
		return &dockerRemovalNotStartedError{err.Error()}
	}
	// Recheck immediately before mutation. Container enumeration includes stopped
	// objects and package removal never invokes autoremove or purge.
	if err = x.dockerEngineHasNoContainers(ctx); err != nil {
		return &dockerRemovalNotStartedError{err.Error()}
	}
	names := []string{}
	for _, p := range marker.Packages {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	out, err := x.softwareRunner(ctx, "/usr/bin/apt-get", dockerAptArgs(false, true, names)...)
	x.appendServiceLog(req, "DOCKER_PACKAGES_REMOVE", string(out))
	if err != nil {
		return errors.New("DOCKER_PACKAGE_REMOVE_UNCONFIRMED")
	}
	if req.Uninstallation.DeleteData {
		actual, err := fileSHA("/etc/docker/daemon.json")
		if err != nil || actual != marker.DaemonHash || secureAncestors("/etc/docker/daemon.json") != nil {
			return errors.New("DOCKER_CONFIGURATION_CHANGED")
		}
		if err = os.Remove("/etc/docker/daemon.json"); err != nil {
			return err
		}
	}
	return nil
}

func (x *executor) dockerEngineUninstalled(ctx context.Context, req Request) bool {
	marker, err := x.dockerRemovalOwnership(req)
	if err != nil {
		return false
	}
	installed, err := x.installedDockerPackages(ctx)
	if err != nil {
		return false
	}
	for _, p := range marker.Packages {
		if installed[p.Name] != "" {
			return false
		}
	}
	if req.Uninstallation.DeleteData {
		if _, err = os.Lstat("/etc/docker/daemon.json"); !os.IsNotExist(err) {
			return false
		}
	}
	for _, file := range []string{"/usr/bin/docker", "/usr/bin/dockerd", marker.UnitPath} {
		if _, err = os.Lstat(file); !os.IsNotExist(err) {
			return false
		}
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=LoadState,ActiveState", "--", "docker.service")
	return err == nil && strings.Contains(string(out), "LoadState=not-found\n") && strings.Contains(string(out), "ActiveState=inactive\n")
}
