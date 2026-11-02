package deploy

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// The marker is persisted before refreshing indexes or changing packages. A
// recovery reads it and observes dpkg/files/services; it never repeats apt.
type officialInstallation struct {
	JobID, InputHash, SoftwareID, ConfigHash   string
	Plan                                       serviceinstall.OfficialPlan
	BinaryHash, UnitPath, UnitHash, DaemonHash string
}

func (x *executor) installOfficialSoftware(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	if i.Official.Validate(i.Definition) != nil {
		return result, errors.New("OFFICIAL_PLAN_REQUIRED")
	}
	preflight := x.officialInstallPreflight(*i)
	for _, check := range preflight.Checks {
		if check.Status != "PASS" {
			return result, errors.New(check.Code)
		}
	}
	if Digest(preflight.Official) != Digest(i.Official) {
		return result, errors.New("OFFICIAL_PLAN_CHANGED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeDeadline(req))
	defer cancel()
	marker := officialInstallation{JobID: req.JobID, InputHash: req.InputHash, SoftwareID: i.SoftwareID, ConfigHash: Digest(i.Config), Plan: *i.Official}
	if err := atomicJSON(filepath.Join(dir, "official-update.json"), marker); err != nil {
		return result, err
	}
	unknown := func(code string) (Result, error) { result.ErrorCode = code; return result, nil }
	if err := x.checkpoint(req, "OFFICIAL_REFRESH"); err != nil {
		return unknown("OFFICIAL_RECONCILE_REQUIRED")
	}
	var files []string
	refresh := i.Official.Commands[0]
	refreshOut, refreshErr := x.softwareRunner(ctx, refresh.Executable, refresh.Args...)
	x.appendServiceLog(req, "OFFICIAL_REFRESH", string(refreshOut))
	if refreshErr != nil {
		return unknown("OFFICIAL_REFRESH_FAILED")
	}
	if i.Definition.Key == "docker" {
		var err error
		files, err = x.prepareOfficialDocker(ctx, req, dir)
		if err != nil {
			return unknown(err.Error())
		}
	}
	installed, err := x.installedDockerPackages(ctx)
	if err != nil {
		return unknown(officialPackageStateCode(err))
	}
	spec, _ := serviceinstall.OfficialRecipeSpec(i.Definition)
	if err = officialInstallAbsent(spec, installed); err != nil {
		return unknown(err.Error())
	}
	plan, err := x.officialCandidatePlan(ctx, i.Definition, installed)
	if err != nil {
		return unknown(err.Error())
	}
	if Digest(plan) != Digest(i.Official) {
		// Refreshing indexes is a completed, bounded preparation. No package
		// apply marker exists, so a changed plan is a final rejection.
		result.Status, result.Phase, result.ErrorCode = "FAILED", "OFFICIAL_PLAN_CHANGED", "OFFICIAL_PLAN_CHANGED"
		return result, nil
	}
	command := i.Official.Commands[1]
	if i.Definition.Key == "docker" {
		command.Args = append([]string(nil), command.Args...)
		for n, arg := range command.Args {
			for _, file := range files {
				if arg == "./"+filepath.Base(file) {
					command.Args[n] = file
				}
			}
		}
		out, simulateErr := x.softwareRunner(ctx, "/usr/bin/apt-get", append([]string{"--simulate"}, command.Args[1:]...)...)
		packages := []dockerPackage{}
		for _, p := range i.Official.Packages {
			packages = append(packages, dockerPackage{Name: p.Name, Version: p.Version})
		}
		if simulateErr != nil || validateDockerAptPlan(out, packages, false) != nil {
			return unknown("OFFICIAL_DOCKER_PLAN_CHANGED")
		}
	} else {
		exact := []string{}
		for _, p := range i.Official.Packages {
			exact = append(exact, p.Name+"="+p.Version)
		}
		out, simulateErr := x.softwareRunner(ctx, "/usr/bin/apt-get", officialAptSimulateArgs(exact)...)
		actual, parseErr := parseOfficialAptPlan(out, spec.Packages, installed)
		if simulateErr != nil || parseErr != nil || Digest(actual) != Digest(i.Official.Packages) {
			return unknown("OFFICIAL_PLAN_CHANGED")
		}
	}
	if err = x.checkpoint(req, "OFFICIAL_PACKAGES_APPLY"); err != nil {
		return unknown("OFFICIAL_RECONCILE_REQUIRED")
	}
	if err = atomicJSON(filepath.Join(dir, "official-apply.json"), marker); err != nil {
		return unknown("OFFICIAL_RECONCILE_REQUIRED")
	}
	out, err := x.softwareRunner(ctx, command.Executable, command.Args...)
	x.appendServiceLog(req, "OFFICIAL_PACKAGES_APPLY", string(out))
	if err != nil {
		return unknown("OFFICIAL_PACKAGE_RECONCILE_REQUIRED")
	}
	if err = x.completeOfficialOwnership(ctx, req, dir, &marker); err != nil {
		return unknown(err.Error())
	}
	// Package maintainers normally start services. Enable/start is performed
	// once in the original transaction; reconciliation only observes it.
	if marker.Plan.Paths.Unit != "" {
		start := i.Official.Commands[len(i.Official.Commands)-1]
		if _, err = x.softwareRunner(ctx, start.Executable, start.Args...); err != nil {
			return unknown("OFFICIAL_SERVICE_START_UNCONFIRMED")
		}
	}
	return x.officialInstalledResult(ctx, req, dir, marker), nil
}

func (x *executor) prepareOfficialDocker(ctx context.Context, req Request, dir string) ([]string, error) {
	archive := filepath.Join(dir, "official-material.tar")
	if err := serviceinstall.PrepareNativeMaterial(ctx, req.Installation.Definition, archive); err != nil {
		return nil, err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return nil, err
	}
	hash, err := fileSHA(archive)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Only the local reader parameters change. The durable request and source
	// remain OFFICIAL and are never rehashed as an offline material upload.
	local := *req.Installation
	local.Source = serviceinstall.Source{Type: "NATIVE_ARCHIVE", SHA256: hash, ByteSize: info.Size()}
	localReq := req
	localReq.Installation = &local
	prior := x.installationInput
	x.installationInput = f
	defer func() { x.installationInput = prior }()
	parts, err := x.receiveNativeMaterial(localReq, dir)
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, part := range req.Installation.Definition.Native.Parts {
		file := parts[part.Name]
		if file == "" {
			return nil, errors.New("OFFICIAL_DOCKER_PACKAGE_MISSING")
		}
		var wanted serviceinstall.OfficialPackage
		for _, p := range req.Installation.Official.Packages {
			if strings.HasPrefix(part.Name, p.Name+"_") {
				wanted = p
				break
			}
		}
		out, err := x.softwareRunner(ctx, "/usr/bin/dpkg-deb", "--show", "--showformat=${Package}\t${Version}\t${Architecture}\n", file)
		if err != nil || string(out) != wanted.Name+"\t"+wanted.Version+"\tamd64\n" {
			return nil, errors.New("OFFICIAL_DOCKER_PACKAGE_IDENTITY_MISMATCH")
		}
		files = append(files, file)
	}
	return files, nil
}

func (x *executor) completeOfficialOwnership(ctx context.Context, req Request, dir string, marker *officialInstallation) error {
	if err := x.verifyOfficialPackages(ctx, marker.Plan); err != nil {
		return err
	}
	binary := marker.Plan.Paths.Binary
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil || secureAncestors(resolved) != nil {
		return errors.New("OFFICIAL_BINARY_NOT_PROTECTED")
	}
	hash, err := fileSHA(binary)
	if err != nil {
		return err
	}
	if marker.BinaryHash != "" && marker.BinaryHash != hash {
		return errors.New("OFFICIAL_BINARY_IDENTITY_MISMATCH")
	}
	marker.BinaryHash = hash
	if unit := marker.Plan.Paths.Unit; unit != "" {
		out, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--property=FragmentPath,DropInPaths", "--", unit)
		if err != nil {
			return errors.New("OFFICIAL_UNIT_UNAVAILABLE")
		}
		properties := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				properties[k] = v
			}
		}
		resolved, err := filepath.EvalSymlinks(properties["FragmentPath"])
		if err != nil || resolved != "/usr/lib/systemd/system/"+unit || properties["DropInPaths"] != "" || secureAncestors(resolved) != nil {
			return errors.New("OFFICIAL_UNIT_IDENTITY_MISMATCH")
		}
		hash, err := fileSHA(resolved)
		if err != nil {
			return err
		}
		if marker.UnitHash != "" && (marker.UnitHash != hash || marker.UnitPath != resolved) {
			return errors.New("OFFICIAL_UNIT_IDENTITY_MISMATCH")
		}
		marker.UnitPath, marker.UnitHash = resolved, hash
	}
	if req.Installation.Definition.Key == "docker" {
		if _, err := os.Lstat("/etc/docker/daemon.json"); !os.IsNotExist(err) {
			return errors.New("OFFICIAL_DOCKER_DAEMON_CONFIG_UNEXPECTED")
		}
	}
	return atomicJSON(filepath.Join(dir, "official-apply.json"), marker)
}

func (x *executor) verifyOfficialPackages(ctx context.Context, plan serviceinstall.OfficialPlan) error {
	installed, err := x.installedDockerPackages(ctx)
	if err != nil {
		return err
	}
	direct := []string{}
	for _, p := range plan.Packages {
		if installed[p.Name] != p.Version {
			return errors.New("OFFICIAL_PACKAGE_VERSION_MISMATCH")
		}
		if p.Direct {
			direct = append(direct, p.Name)
		}
	}
	// dpkg verifies package-owned executable/config metadata from its installed
	// manifest. Any unexplained change prevents ownership or recovery claims.
	out, err := x.softwareRunner(ctx, "/usr/bin/dpkg", append([]string{"--verify"}, direct...)...)
	if err != nil || strings.TrimSpace(string(out)) != "" {
		return errors.New("OFFICIAL_PACKAGE_CONTENT_DRIFT")
	}
	return nil
}

func (x *executor) verifyOfficialInstalled(ctx context.Context, req Request, marker officialInstallation) error {
	i := req.Installation
	if i == nil || marker.JobID != req.JobID || marker.InputHash != req.InputHash || marker.SoftwareID != i.SoftwareID || marker.ConfigHash != Digest(i.Config) || marker.Plan.Validate(i.Definition) != nil || Digest(marker.Plan) != Digest(i.Official) || !Hex.MatchString(marker.BinaryHash) {
		return errors.New("OFFICIAL_OWNERSHIP_MISMATCH")
	}
	if err := x.verifyOfficialPackages(ctx, marker.Plan); err != nil {
		return err
	}
	for file, want := range map[string]string{marker.Plan.Paths.Binary: marker.BinaryHash, marker.UnitPath: marker.UnitHash} {
		if file == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(file)
		if err != nil || secureAncestors(resolved) != nil {
			return errors.New("OFFICIAL_PATH_NOT_PROTECTED")
		}
		actual, err := fileSHA(file)
		if err != nil || actual != want {
			return errors.New("OFFICIAL_PROGRAM_DRIFT")
		}
	}
	if i.Definition.Key == "docker" {
		if _, err := os.Lstat("/etc/docker/daemon.json"); !os.IsNotExist(err) {
			return errors.New("OFFICIAL_DOCKER_DAEMON_CONFIG_UNEXPECTED")
		}
	}
	return nil
}

func (x *executor) officialSoftware(req Request, marker officialInstallation) SoftwareRequest {
	i := req.Installation
	p := marker.Plan.Paths
	kind, locator := "SYSTEMD", p.Unit
	if i.Definition.Kind == "TOOL" {
		kind, locator = "TOOL", p.Binary
	}
	return SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: kind, Locator: locator, SoftwareKey: strings.ToUpper(i.Definition.Key), Ownership: &SoftwareOwnership{Official: i.Official, RuntimeMode: "NATIVE", BinaryPath: p.Binary, BinaryHash: marker.BinaryHash, ControllerID: req.ControllerID, TargetID: req.TargetID, SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config)}}
}

func (x *executor) officialInstalledResult(ctx context.Context, req Request, dir string, marker officialInstallation) Result {
	result := softwareResult(req)
	result.ErrorCode = "OFFICIAL_RECONCILE_REQUIRED"
	if x.verifyOfficialInstalled(ctx, req, marker) != nil {
		return result
	}
	s := x.officialSoftware(req, marker)
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
	version, err := x.officialHealth(ctx, req, s)
	if err != nil {
		result = x.ownedInstallResult(req, s, "", "FAILED", "FAILED")
		result.ErrorCode = "OFFICIAL_HEALTH_FAILED"
		return result
	}
	return x.ownedInstallResult(req, s, version, "READY", "SUCCEEDED")
}

func (x *executor) reconcileOfficialSoftware(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "OFFICIAL_RECONCILE_REQUIRED"
	var marker officialInstallation
	if readJSON(filepath.Join(dir, "official-apply.json"), &marker) != nil {
		var preparation officialInstallation
		if readJSON(filepath.Join(dir, "official-update.json"), &preparation) == nil && preparation.JobID == req.JobID && preparation.InputHash == req.InputHash {
			result.Status, result.Phase, result.ErrorCode = "FAILED", "OFFICIAL_PREPARATION_INTERRUPTED", "OFFICIAL_PREPARATION_INTERRUPTED"
		}
		return result
	}
	if marker.JobID != req.JobID || marker.InputHash != req.InputHash || Digest(marker.Plan) != Digest(req.Installation.Official) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A crash immediately after apt may precede the final hashes. Complete
	// the record from verified dpkg manifests; never install or start again.
	if x.completeOfficialOwnership(ctx, req, dir, &marker) != nil {
		return result
	}
	return x.officialInstalledResult(ctx, req, dir, marker)
}

func officialVersionArguments(key string) []string {
	switch key {
	case "java":
		return []string{"-version"}
	case "maven":
		return []string{"-version"}
	case "python":
		return []string{"-I", "--version"}
	case "ffmpeg":
		return []string{"-version"}
	case "nginx":
		return []string{"-v"}
	default:
		return []string{"--version"}
	}
}

func (x *executor) officialHealth(ctx context.Context, req Request, s SoftwareRequest) (string, error) {
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return "", err
	}
	if s.Kind == "TOOL" {
		return actual.Version, nil
	}
	if actual.State != "RUNNING" {
		return "", errors.New("OFFICIAL_SERVICE_NOT_RUNNING")
	}
	p := req.Installation.Official.Paths
	if req.Installation.Definition.Key == "docker" {
		out, err := x.softwareRunner(ctx, "/usr/bin/docker", "info", "--format", "{{.ServerVersion}}\t{{.DockerRootDir}}")
		if err != nil || strings.TrimSpace(string(out)) != "29.9.2\t/var/lib/docker" {
			return "", errors.New("OFFICIAL_DOCKER_HEALTH_MISMATCH")
		}
		return "29.9.2", nil
	}
	probe := map[string][]string{"redis": {"/usr/bin/redis-cli", "-h", "127.0.0.1", "-p", "6379", "PING"}, "mysql": {"/usr/bin/mysql", "--protocol=socket", "--batch", "--skip-column-names", "--execute", "SELECT VERSION()"}, "nginx": {"/usr/sbin/nginx", "-t"}}[req.Installation.Definition.Key]
	if len(probe) == 0 {
		return "", errors.New("OFFICIAL_HEALTH_UNSUPPORTED")
	}
	out, err := x.softwareRunner(ctx, probe[0], probe[1:]...)
	if err != nil {
		return "", errors.New("OFFICIAL_PROTOCOL_PROBE_FAILED")
	}
	if req.Installation.Definition.Key == "redis" && strings.TrimSpace(string(out)) != "PONG" {
		return "", errors.New("OFFICIAL_PROTOCOL_PROBE_FAILED")
	}
	if req.Installation.Definition.Key == "nginx" {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:80")
		if err != nil {
			return "", errors.New("OFFICIAL_PROTOCOL_PROBE_FAILED")
		}
		conn.Close()
	}
	out, err = x.softwareRunner(ctx, p.Binary, officialVersionArguments(req.Installation.Definition.Key)...)
	version := strings.TrimSpace(string(out))
	if err != nil || version == "" || len(version) > 8192 {
		return "", errors.New("OFFICIAL_VERSION_PROBE_FAILED")
	}
	return version, nil
}
