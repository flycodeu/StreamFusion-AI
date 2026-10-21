package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/streamfusion-ai/operations/internal/plugin"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func toolPluginDefinition(d serviceinstall.Definition) (plugin.Definition, error) {
	if d.Key == "java" {
		return plugin.Find("java-runtime", d.Version+"-jre")
	}
	if d.Key == "maven" {
		return plugin.Find("maven-jdk-build", "3.9.11-jdk-21")
	}
	return plugin.Definition{}, errors.New("TOOL_RECIPE_UNSUPPORTED")
}
func toolImageForPlugin(d plugin.Definition) string {
	for _, tool := range serviceinstall.Catalog() {
		if tool.Kind == "TOOL" && tool.Image == d.Image && tool.Digest == d.Digest {
			return tool.ImageID
		}
	}
	return ""
}
func (x *executor) installDockerTool(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	preflight := x.serviceInstallPreflight(*req.Installation)
	for _, check := range preflight.Checks {
		if check.Status != "PASS" {
			return result, errors.New(check.Code)
		}
	}
	if err := x.serviceImage(req); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := SoftwareRequest{ID: req.Installation.SoftwareID, Version: req.Installation.BindingRevision, Kind: "TOOL", Locator: "image:" + req.Installation.Definition.ImageID, SoftwareKey: strings.ToUpper(req.Installation.Definition.Key), Ownership: x.installOwnership(req)}
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || !installedToolVersionMatches(req.Installation.Definition, actualVersion(actual)) {
		return result, errors.New("TOOL_VERSION_CHECK_FAILED")
	}
	s.IdentityHash = actual.IdentityHash
	if serviceinstall.UsesJavaBridge(req.Installation.Definition) {
		d, err := toolPluginDefinition(req.Installation.Definition)
		if err != nil {
			return result, err
		}
		material := pluginMaterial{InstallationID: s.ID, Definition: d, Managed: true, JobID: req.JobID, ImageID: req.Installation.Definition.ImageID}
		if err = os.MkdirAll(filepath.Join(x.root, "plugins"), 0700); err != nil {
			return result, err
		}
		if err = atomicJSON(filepath.Join(x.root, "plugins", s.ID+".json"), material); err != nil {
			return result, nil
		}
	}
	if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
		return result, nil
	}
	return x.ownedInstallResult(req, s, actual.Version, "READY", "SUCCEEDED"), nil
}
func actualVersion(actual *SoftwareObservation) string {
	if actual == nil {
		return ""
	}
	return actual.Version
}
func (x *executor) verifyToolBridge(req Request) error {
	var material pluginMaterial
	d, err := toolPluginDefinition(req.Installation.Definition)
	if err != nil {
		return err
	}
	if readJSON(filepath.Join(x.root, "plugins", req.Installation.SoftwareID+".json"), &material) != nil || material.InstallationID != req.Installation.SoftwareID || material.JobID != req.JobID || material.Definition != d || material.ImageID != req.Installation.Definition.ImageID || !material.Managed {
		return errors.New("TOOL_PLUGIN_BRIDGE_MISMATCH")
	}
	return nil
}
func (x *executor) reconcileDockerTool(req Request, dir string) Result {
	result := softwareResult(req)
	var binding softwareBinding
	if serviceinstall.UsesJavaBridge(req.Installation.Definition) && x.verifyToolBridge(req) != nil {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := readJSON(x.softwareFile(req.Installation.SoftwareID), &binding); os.IsNotExist(err) {
		// A lost reply between the durable plugin bridge and the software binding
		// does not justify re-pulling or re-installing. Reconstruct metadata only
		// after the exact pinned image and version are observed again.
		s := SoftwareRequest{ID: req.Installation.SoftwareID, Version: req.Installation.BindingRevision, Kind: "TOOL", Locator: "image:" + req.Installation.Definition.ImageID, SoftwareKey: strings.ToUpper(req.Installation.Definition.Key), Ownership: x.installOwnership(req)}
		actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
		if err != nil || !installedToolVersionMatches(req.Installation.Definition, actualVersion(actual)) {
			return result
		}
		s.IdentityHash = actual.IdentityHash
		binding = softwareBinding{Software: s, JobID: req.JobID}
		if atomicJSON(x.softwareFile(s.ID), binding) != nil {
			return result
		}
	} else if err != nil || binding.JobID != req.JobID || x.boundSoftware(binding.Software) != nil {
		return result
	}
	actual, err := ObserveSoftware(ctx, binding.Software, x.softwareRunner)
	if err != nil {
		return result
	}
	return x.ownedInstallResult(req, binding.Software, actual.Version, "READY", "SUCCEEDED")
}

func observeInstalledTool(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner) (*SoftwareObservation, error) {
	o := s.Ownership
	command, args, err := installedToolCommand(s.SoftwareKey)
	if err != nil {
		return nil, err
	}
	var out []byte
	if o.RuntimeMode == "NATIVE" {
		if err = verifyNativeBinary(ctx, o, run); err != nil {
			return nil, err
		}
		out, err = nativeToolCommand(ctx, s, run, args...)
	} else {
		inspected, checkErr := softwareCommand(ctx, run, "/usr/bin/docker", "image", "inspect", "--format", `{"id":{{json .Id}},"os":{{json .Os}},"arch":{{json .Architecture}}}`, o.ImageID)
		var actual struct{ ID, OS, Arch string }
		if checkErr != nil || json.Unmarshal(inspected, &actual) != nil || actual.ID != o.ImageID || actual.OS != "linux" || actual.Arch != "amd64" {
			return nil, errors.New("TOOL_IMAGE_IDENTITY_MISMATCH")
		}
		dockerArgs := []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--user", "65534:65534", "--workdir", "/tmp", "--tmpfs", "/tmp:rw,noexec,nosuid,size=32m", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--memory", "256m", "--cpus", "1", "--pids-limit", "64", "--env", "HOME=/tmp", "--entrypoint", command, o.ImageID}
		out, err = softwareCommand(ctx, run, "/usr/bin/docker", append(dockerArgs, args...)...)
	}
	version := strings.TrimSpace(string(out))
	if err != nil && (err.Error() == "FFMPEG_H264_ENCODER_UNAVAILABLE" || err.Error() == "FFPROBE_UNAVAILABLE") {
		return nil, err
	}
	if err != nil || len(version) == 0 || len(version) > 8192 {
		return nil, errors.New("SOFTWARE_TOOL_VERSION_INVALID")
	}
	hash := o.BinaryHash
	if o.RuntimeMode != "NATIVE" {
		hash = o.ImageID
	}
	return &SoftwareObservation{SoftwareMetadata: SoftwareMetadata{VersionSource: "VERSION_COMMAND", VersionStatus: "VERIFIED", ExecutablePath: o.BinaryPath}, Kind: "TOOL", Locator: s.Locator, IdentityHash: Digest([]string{"TOOL", s.Locator, hash}), State: "INSTALLED", Version: version, ObservedAt: stamp()}, nil
}
func verifyNativeBinary(ctx context.Context, o *SoftwareOwnership, run SoftwareCommandRunner) error {
	out, err := softwareCommand(ctx, run, "/usr/bin/sha256sum", "--", o.BinaryPath)
	fields := strings.Fields(string(out))
	if err != nil || len(fields) != 2 || fields[0] != o.BinaryHash || strings.TrimPrefix(fields[1], "*") != o.BinaryPath {
		return errors.New("SOFTWARE_NATIVE_BINARY_DRIFT")
	}
	return nil
}
