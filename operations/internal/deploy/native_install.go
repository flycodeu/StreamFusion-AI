package deploy

import (
	"context"
	"errors"
	"fmt"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type nativeInstallation struct{ SoftwareID, JobID, ConfigHash, BinaryPath, BinaryHash, UnitHash string }

func (x *executor) installNative(req Request, dir string) (Result, error) {
	if req.Installation.Definition.Key == "docker" {
		return x.installDockerEngine(req, dir)
	}
	result := softwareResult(req)
	i := req.Installation
	preflight := x.nativeInstallPreflight(*i)
	for _, check := range preflight.Checks {
		if check.Status != "PASS" {
			return result, errors.New(check.Code)
		}
	}
	if err := x.checkpoint(req, "NATIVE_MATERIAL"); err != nil {
		return result, err
	}
	parts, err := x.receiveNativeMaterial(req, dir)
	if err != nil {
		return result, err
	}
	root := preflight.Root
	if err = ensureNativeParent(root); err != nil {
		return result, err
	}
	if err = os.Mkdir(root, 0755); err != nil {
		return result, errors.New("SERVICE_DATA_DIRECTORY_EXISTS")
	}
	for _, name := range []string{"config", "data", "bin", "logs", "runtime"} {
		if err = os.Mkdir(filepath.Join(root, name), 0750); err != nil {
			return result, err
		}
	}
	owner := nativeInstallation{SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), BinaryPath: filepath.Join(root, nativeBinaryRelative(i.Definition))}
	if err = atomicJSON(filepath.Join(dir, "native-owned.json"), owner); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeDeadline(req))
	defer cancel()
	user := ""
	gid := 0
	uid := 0
	if nativeNeedsAccount(i.Definition) {
		user = nativeUserName(i.SoftwareID)
		if _, err = x.softwareRunner(ctx, "/usr/bin/id", "-u", user); err == nil {
			return result, errors.New("NATIVE_ACCOUNT_ALREADY_EXISTS")
		}
		if _, err = x.softwareRunner(ctx, "/usr/sbin/useradd", "--system", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", "--user-group", "--", user); err != nil {
			return result, errors.New("NATIVE_ACCOUNT_CREATE_FAILED")
		}
		idOut, idErr := x.softwareRunner(ctx, "/usr/bin/id", "-u", user)
		groupOut, groupErr := x.softwareRunner(ctx, "/usr/bin/id", "-g", user)
		uid, err = strconv.Atoi(strings.TrimSpace(string(idOut)))
		gid, _ = strconv.Atoi(strings.TrimSpace(string(groupOut)))
		if idErr != nil || groupErr != nil || err != nil || uid < 1 || gid < 1 {
			return result, errors.New("NATIVE_ACCOUNT_ID_INVALID")
		}
	}
	if err = x.prepareNativeProgram(ctx, req, dir, root, user, uid, gid, parts); err != nil {
		return result, err
	}
	if err = rootOwnNativeTree(root, gid, i.Definition.Kind == "TOOL"); err != nil {
		return result, err
	}
	if i.Definition.Category == "PROGRAMMING" {
		if err = os.Chown(filepath.Join(root, "data"), uid, gid); err != nil {
			return result, err
		}
		if err = os.Chmod(filepath.Join(root, "data"), 0700); err != nil {
			return result, err
		}
	}
	owner.BinaryHash, err = fileSHA(owner.BinaryPath)
	if err != nil {
		return result, err
	}
	if i.Definition.Kind == "TOOL" {
		if err = atomicJSON(filepath.Join(dir, "native-owned.json"), owner); err != nil {
			return result, err
		}
		s := x.nativeSoftware(req, owner, "")
		actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
		if err != nil {
			return result, err
		}
		if !installedToolVersionMatches(i.Definition, actual.Version) {
			return result, errors.New("TOOL_VERSION_CHECK_FAILED")
		}
		s.IdentityHash = actual.IdentityHash
		if err = atomicJSON(x.softwareFile(i.SoftwareID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
			return result, err
		}
		return x.ownedInstallResult(req, s, actual.Version, "READY", "SUCCEEDED"), nil
	}
	if err = x.writeNativeConfiguration(req, root, gid); err != nil {
		return result, err
	}
	for _, name := range []string{"data", "logs"} {
		if err = chownNativeTree(filepath.Join(root, name), uid, gid); err != nil {
			return result, err
		}
	}
	unit := renderNativeUnit(req, root, user)
	if err = serviceFile(nativeUnitPath(i.SoftwareID), []byte(unit), 0, false); err != nil {
		return result, err
	}
	owner.UnitHash, err = fileSHA(nativeUnitPath(i.SoftwareID))
	if err != nil {
		return result, err
	}
	if err = atomicJSON(filepath.Join(dir, "native-owned.json"), owner); err != nil {
		return result, err
	}
	if _, err = x.softwareRunner(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return result, nil
	}
	s := x.nativeSoftware(req, owner, "")
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return result, nil
	}
	s.IdentityHash = actual.IdentityHash
	if err = atomicJSON(x.softwareFile(i.SoftwareID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
		return result, nil
	}
	// Persist before enable/start: lost replies are observed, never replayed.
	if err = atomicJSON(filepath.Join(dir, "native-start.json"), owner); err != nil {
		return result, nil
	}
	if err = x.checkpoint(req, "NATIVE_START"); err != nil {
		return result, nil
	}
	_, startErr := x.softwareRunner(ctx, "/usr/bin/systemctl", "enable", "--now", "--", s.Locator)
	if startErr == nil {
		if version, healthErr := x.waitServiceHealth(req, s, 2*time.Minute); healthErr == nil {
			return x.ownedInstallResult(req, s, version, "READY", "SUCCEEDED"), nil
		}
	}
	failed := x.ownedInstallResult(req, s, "", "FAILED", "FAILED")
	if failed.Status == "FAILED" {
		failed.ErrorCode = "NATIVE_HEALTH_FAILED"
	}
	return failed, nil
}

func (x *executor) nativeSoftware(req Request, n nativeInstallation, identity string) SoftwareRequest {
	i := req.Installation
	kind, locator := "SYSTEMD", nativeUnit(i.SoftwareID)
	if i.Definition.Key == "docker" {
		locator = "docker.service"
	}
	if i.Definition.Kind == "TOOL" {
		kind, locator = "TOOL", n.BinaryPath
	}
	return SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: kind, Locator: locator, IdentityHash: identity, SoftwareKey: strings.ToUpper(i.Definition.Key), Ownership: &SoftwareOwnership{RuntimeMode: "NATIVE", ControllerID: req.ControllerID, TargetID: req.TargetID, SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), BinaryPath: n.BinaryPath, BinaryHash: n.BinaryHash}}
}

func (x *executor) reconcileNative(req Request, dir string) Result {
	if req.Installation.Definition.Key == "docker" {
		return x.reconcileDockerEngine(req, dir)
	}
	result := softwareResult(req)
	result.ErrorCode = "NATIVE_RECONCILE_REQUIRED"
	var marker nativeInstallation
	if readJSON(filepath.Join(dir, "native-owned.json"), &marker) != nil || marker.SoftwareID != req.Installation.SoftwareID || marker.JobID != req.JobID || marker.ConfigHash != Digest(req.Installation.Config) {
		return result
	}
	if marker.BinaryHash == "" {
		result.Status, result.Phase, result.ErrorCode = "FAILED", "NATIVE_NOT_STARTED", "NATIVE_PREPARATION_INTERRUPTED"
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := x.nativeSoftware(req, marker, "")
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return result
	}
	s.IdentityHash = actual.IdentityHash
	var prior softwareBinding
	if err = readJSON(x.softwareFile(s.ID), &prior); err == nil {
		if x.boundSoftware(s) != nil || prior.JobID != req.JobID {
			return result
		}
	} else if !os.IsNotExist(err) {
		return result
	} else if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
		return result
	}
	if req.Installation.Definition.Kind == "TOOL" {
		return x.ownedInstallResult(req, s, actual.Version, "READY", "SUCCEEDED")
	}
	if version, err := x.waitServiceHealth(req, s, 8*time.Second); err == nil {
		return x.ownedInstallResult(req, s, version, "READY", "SUCCEEDED")
	}
	result = x.ownedInstallResult(req, s, "", "FAILED", "FAILED")
	if result.Status == "FAILED" {
		result.ErrorCode = "NATIVE_HEALTH_FAILED"
	}
	return result
}

func (x *executor) ownedInstallResult(req Request, s SoftwareRequest, version, health, status string) Result {
	result := softwareResult(req)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || actual.State == "UNKNOWN" {
		return result
	}
	result.Status, result.Phase, result.Software = status, "SERVICE_INSTALLED", actual
	if version != "" {
		actual.Version, actual.VersionSource, actual.VersionStatus = version, "INSTALL_PROBE", "VERIFIED_AT_INSTALL"
	}
	i := req.Installation
	result.ConfigHash = Digest(i.Config)
	result.Health, result.ObservedVersion = health, version
	e := &serviceinstall.Evidence{SoftwareID: i.SoftwareID, BindingRevision: i.BindingRevision, RecipeKey: i.Definition.Key, RecipeRevision: i.Definition.Revision, ConfigHash: result.ConfigHash, Kind: s.Kind, Locator: s.Locator, IdentityHash: s.IdentityHash, ObservedVersion: version, Health: health, ObservedAt: stamp()}
	if s.Ownership.RuntimeMode == "NATIVE" {
		e.BinaryHash, e.BinaryPath = s.Ownership.BinaryHash, s.Ownership.BinaryPath
	} else {
		e.ImageID = i.Definition.ImageID
		result.ImageID = e.ImageID
	}
	result.Installation = e
	return result
}
func (x *executor) verifyOwnedInstallation(req Request, result Result) error {
	if result.Installation == nil || result.Software == nil || result.Installation.ValidateFor(*req.Installation) != nil {
		return errors.New("SERVICE_INSTALL_RECEIPT_MISMATCH")
	}
	var binding softwareBinding
	if err := readJSON(x.softwareFile(req.Installation.SoftwareID), &binding); err != nil {
		return err
	}
	if binding.JobID != req.JobID || binding.Software.IdentityHash != result.Installation.IdentityHash || binding.Software.Kind != result.Installation.Kind || binding.Software.Locator != result.Installation.Locator {
		return errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	if serviceinstall.UsesJavaBridge(req.Installation.Definition) && req.Installation.Config.Mode() == "DOCKER" {
		return x.verifyToolBridge(req)
	}
	return nil
}

func renderNativeUnit(req Request, root, user string) string {
	i := req.Installation
	command := filepath.Join(root, nativeBinaryRelative(i.Definition))
	args := nativeServiceArguments(*i, root)
	line := command
	for _, arg := range args {
		line += " " + strconv.Quote(arg)
	}
	capabilities := ""
	for _, port := range serviceinstall.PortMappings(i.Definition, i.Config) {
		if port.HostPort < 1024 {
			capabilities = "AmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\n"
			break
		}
	}
	if capabilities == "" {
		capabilities = "CapabilityBoundingSet=\n"
	}
	return fmt.Sprintf("[Unit]\nDescription=StreamFusion %s %s\nAfter=network-online.target\nWants=network-online.target\n[Service]\nType=simple\nUser=%s\nGroup=%s\nWorkingDirectory=%s/data\nExecStart=%s\nRestart=on-failure\nRestartSec=5\nTimeoutStopSec=30\nKillMode=control-group\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nProtectHome=true\nReadWritePaths=%s/data %s/logs\nUMask=0027\nLimitNOFILE=65536\nMemoryMax=%dM\n%sLogRateLimitIntervalSec=30s\nLogRateLimitBurst=2000\nStandardOutput=journal\nStandardError=journal\n[Install]\nWantedBy=multi-user.target\n", i.Definition.Name, i.SoftwareID, user, user, root, line, root, root, i.Config.MemoryMiB, capabilities)
}
func nativeServiceArguments(i serviceinstall.Request, root string) []string {
	config := filepath.Join(root, "config")
	data := filepath.Join(root, "data")
	switch i.Definition.Key {
	case "mediamtx":
		return []string{filepath.Join(config, "mediamtx.yml")}
	case "mysql":
		return []string{"--defaults-file=" + filepath.Join(config, "my.cnf")}
	case "redis":
		return []string{filepath.Join(config, "redis.conf")}
	case "nginx":
		return []string{"-p", root + "/", "-c", filepath.Join(config, "nginx.conf"), "-g", "daemon off;"}
	case "zlm":
		return []string{"-c", filepath.Join(config, "config.ini"), "-s", filepath.Join(root, "runtime/dtls.pem"), "-l", "1", "--log-dir", filepath.Join(root, "logs"), "--log-size", "10", "--log-slice", "3", "-m", "3"}
	case "seaweedfs":
		p := func(key string) string { return strconv.Itoa(serviceinstall.PortValue(i.Definition, i.Config, key)) }
		return []string{"server", "-dir=" + data + "/volume", "-master.dir=" + data + "/master", "-volume.max=8", "-master.volumeSizeLimitMB=1024", "-ip=127.0.0.1", "-ip.bind=127.0.0.1", "-master.telemetry=false", "-master.port=" + p("master"), "-master.port.grpc=" + p("masterGrpc"), "-volume.port=" + p("volume"), "-volume.port.grpc=" + p("volumeGrpc"), "-filer", "-filer.port=" + p("filer"), "-filer.port.grpc=" + p("filerGrpc"), "-s3", "-s3.ip.bind=" + i.Config.BindAddress, "-s3.port=" + p("main"), "-s3.port.grpc=" + p("s3Grpc"), "-s3.port.iceberg=0", "-s3.port.lance=0", "-s3.config=" + filepath.Join(config, "s3.json")}
	}
	return nil
}

func rootOwnNativeTree(root string, gid int, tool bool) error {
	return filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return os.Lchown(name, 0, gid)
		}
		if err = os.Chown(name, 0, gid); err != nil {
			return err
		}
		if entry.IsDir() {
			mode := os.FileMode(0750)
			if tool {
				mode = 0755
			}
			return os.Chmod(name, mode)
		}
		return nil
	})
}
func chownNativeTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return os.Lchown(name, uid, gid)
		}
		return os.Chown(name, uid, gid)
	})
}

func copyNativeFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("NATIVE_BUILD_OUTPUT_INVALID")
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, io.LimitReader(input, 512<<20))
	if err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
func nativeBuildJobs() string { return strconv.Itoa(min(runtime.NumCPU(), 4)) }
