package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SoftwareCommandRunner is the only transport seam for software observation.
// Callers execute the supplied fixed executable and argument list with a clean
// environment, bounded output, and the supplied timeout; no shell is required.
type SoftwareCommandRunner func(context.Context, string, ...string) ([]byte, error)

func softwareCommand(ctx context.Context, run SoftwareCommandRunner, command string, args ...string) ([]byte, error) {
	if run == nil {
		return nil, errors.New("SOFTWARE_OBSERVER_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	data, err := run(ctx, command, args...)
	if ctx.Err() != nil {
		return nil, errors.New("SOFTWARE_OBSERVATION_TIMEOUT")
	}
	if len(data) > 64<<10 {
		return nil, errors.New("SOFTWARE_OBSERVATION_TOO_LARGE")
	}
	return data, err
}

// ObserveSoftware is also used over an ordinary SSH session: read-only service
// discovery neither installs an executor nor requests mutation authority.
func ObserveSoftware(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner) (*SoftwareObservation, error) {
	if err := s.Validate(true); err != nil {
		return nil, err
	}
	var actual *SoftwareObservation
	var err error
	switch s.Kind {
	case "SYSTEMD":
		actual, err = observeSoftwareUnit(ctx, s, run)
	case "DOCKER":
		actual, err = observeSoftwareContainer(ctx, s, run)
	case "TOOL":
		if s.Ownership != nil {
			actual, err = observeInstalledTool(ctx, s, run)
		} else {
			actual, err = observeSoftwareTool(ctx, s, run)
		}
	}
	if err != nil {
		return nil, err
	}
	if s.Kind == "SYSTEMD" && s.Ownership != nil && s.Ownership.RuntimeMode == "NATIVE" {
		if err = verifyNativeBinary(ctx, s.Ownership, run); err != nil {
			return nil, err
		}
		actual.IdentityHash = Digest([]string{actual.IdentityHash, s.Ownership.BinaryHash})
		actual.ExecutablePath = s.Ownership.BinaryPath
	}
	if s.IdentityHash != "" && actual.IdentityHash != s.IdentityHash {
		return nil, errors.New("SOFTWARE_IDENTITY_DRIFT")
	}
	return actual, nil
}

func observeSoftwareUnit(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner) (*SoftwareObservation, error) {
	data, err := softwareCommand(ctx, run, "/usr/bin/systemctl", "show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,Result,FragmentPath,DropInPaths,Transient,NeedDaemonReload", "--", s.Locator)
	if err != nil {
		return nil, errors.New("SOFTWARE_UNIT_OBSERVE_DENIED")
	}
	properties := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			if _, exists := properties[key]; exists {
				return nil, errors.New("SOFTWARE_UNIT_RESPONSE_INVALID")
			}
			properties[key] = value
		}
	}
	if properties["Id"] != s.Locator || properties["LoadState"] != "loaded" || properties["Transient"] == "yes" {
		return nil, errors.New("SOFTWARE_UNIT_NOT_FOUND_OR_ALIAS")
	}
	if properties["NeedDaemonReload"] == "yes" {
		return nil, errors.New("SOFTWARE_UNIT_RELOAD_REQUIRED")
	}
	paths := append([]string{properties["FragmentPath"]}, strings.Fields(properties["DropInPaths"])...)
	if len(paths) > 17 {
		return nil, errors.New("SOFTWARE_UNIT_CONFIGURATION_LIMIT")
	}
	identities := []string{s.Kind, s.Locator}
	for _, file := range paths {
		if !softwareUnitFile(file) {
			return nil, errors.New("SOFTWARE_UNIT_FILE_FORBIDDEN")
		}
	}
	resolved, err := softwareCommand(ctx, run, "/usr/bin/readlink", append([]string{"-f", "--"}, paths...)...)
	canonical := strings.Split(strings.TrimSpace(string(resolved)), "\n")
	if err != nil || len(canonical) != len(paths) {
		return nil, errors.New("SOFTWARE_UNIT_FILE_UNAVAILABLE")
	}
	for i, file := range canonical {
		if !softwareUnitFile(file) {
			return nil, errors.New("SOFTWARE_UNIT_FILE_FORBIDDEN")
		}
		identities = append(identities, paths[i], file)
	}
	checked := map[string]bool{}
	for _, file := range canonical {
		if !softwareUnitFile(file) {
			return nil, errors.New("SOFTWARE_UNIT_FILE_FORBIDDEN")
		}
		checked[file] = false
		for directory := path.Dir(file); ; directory = path.Dir(directory) {
			checked[directory] = true
			if directory == "/" {
				break
			}
		}
	}
	// One metadata round trip and one digest round trip cover every unit file,
	// drop-in and parent. Observation does not open an SSH session per file.
	statPaths := make([]string, 0, len(checked))
	for file := range checked {
		statPaths = append(statPaths, file)
	}
	sort.Strings(statPaths)
	statArgs := append([]string{"-c", "%u:%a:%F:%s", "--"}, statPaths...)
	stat, err := softwareCommand(ctx, run, "/usr/bin/stat", statArgs...)
	statLines := strings.Split(strings.TrimSpace(string(stat)), "\n")
	if err != nil || len(statLines) != len(statPaths) {
		return nil, errors.New("SOFTWARE_UNIT_FILE_NOT_PROTECTED")
	}
	for i, file := range statPaths {
		if !protectedSoftwarePath(statLines[i], checked[file]) {
			return nil, errors.New("SOFTWARE_UNIT_FILE_NOT_PROTECTED")
		}
	}
	digest, err := softwareCommand(ctx, run, "/usr/bin/sha256sum", append([]string{"--"}, canonical...)...)
	digestLines := strings.Split(strings.TrimSpace(string(digest)), "\n")
	if err != nil || len(digestLines) != len(canonical) {
		return nil, errors.New("SOFTWARE_UNIT_DIGEST_UNAVAILABLE")
	}
	for i, file := range canonical {
		parts := strings.Fields(digestLines[i])
		if len(parts) != 2 || !Hex.MatchString(parts[0]) || strings.TrimPrefix(parts[1], "*") != file {
			return nil, errors.New("SOFTWARE_UNIT_DIGEST_UNAVAILABLE")
		}
		identities = append(identities, file, parts[0])
	}
	state := "UNKNOWN"
	switch properties["ActiveState"] {
	case "active":
		state = "RUNNING"
	case "inactive":
		state = "STOPPED"
	case "failed":
		state = "FAILED"
	}
	return &SoftwareObservation{Kind: s.Kind, Locator: s.Locator, IdentityHash: Digest(identities), State: state, ObservedAt: stamp()}, nil
}

func softwareUnitFile(file string) bool {
	if path.Clean(file) != file || strings.ContainsAny(file, "\r\n\t ") || len(file) > 512 {
		return false
	}
	for _, prefix := range []string{"/etc/systemd/system/", "/usr/lib/systemd/system/", "/lib/systemd/system/"} {
		if strings.HasPrefix(file, prefix) && (strings.HasSuffix(file, ".service") || strings.HasSuffix(file, ".conf")) {
			return true
		}
	}
	return false
}

func protectedSoftwarePath(value string, directory bool) bool {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 4 || parts[0] != "0" {
		return false
	}
	if directory && parts[2] != "directory" || !directory && parts[2] != "regular file" && parts[2] != "regular empty file" {
		return false
	}
	mode, err := strconv.ParseUint(parts[1], 8, 32)
	if err != nil || mode&0022 != 0 {
		return false
	}
	size, err := strconv.ParseInt(parts[3], 10, 64)
	return err == nil && size >= 0 && size <= 1<<20
}

func observeSoftwareContainer(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner) (*SoftwareObservation, error) {
	// Emit only lifecycle metadata. Full Docker inspect contains environment
	// secrets and is never returned to the controller or browser.
	data, err := softwareCommand(ctx, run, "/usr/bin/docker", "container", "inspect", "--format", `{"id":{{json .Id}},"name":{{json .Name}},"imageId":{{json .Image}},"image":{{json .Config.Image}},"labels":{{json .Config.Labels}},"state":{{json .State.Status}}}`, "--", s.Locator)
	if err != nil {
		return nil, errors.New("SOFTWARE_CONTAINER_OBSERVE_DENIED")
	}
	var container struct {
		ID      string            `json:"id"`
		Name    string            `json:"name"`
		ImageID string            `json:"imageId"`
		Image   string            `json:"image"`
		Labels  map[string]string `json:"labels"`
		State   string            `json:"state"`
	}
	if json.Unmarshal(data, &container) != nil {
		return nil, errors.New("SOFTWARE_CONTAINER_RESPONSE_INVALID")
	}
	id, name, imageID, image, labels, state := container.ID, container.Name, container.ImageID, container.Image, container.Labels, container.State
	if !Hex.MatchString(id) || !Hex.MatchString(strings.TrimPrefix(imageID, "sha256:")) {
		return nil, errors.New("SOFTWARE_CONTAINER_RESPONSE_INVALID")
	}
	if s.Ownership != nil {
		if !serviceOwnershipMatches(s, name, imageID, labels) {
			return nil, errors.New("SOFTWARE_OWNERSHIP_MISMATCH")
		}
	} else {
		for key := range labels {
			if strings.HasPrefix(key, "com.streamfusion.ops.") || strings.HasPrefix(key, "sfops.") || strings.HasPrefix(key, "streamfusion.ops.") {
				return nil, errors.New("SOFTWARE_CONTAINER_ALREADY_MANAGED")
			}
		}
		if strings.HasPrefix(strings.TrimPrefix(name, "/"), "sfops-") || strings.Contains(strings.ToLower(name), "streamfusion-ops") {
			return nil, errors.New("SOFTWARE_CONTAINER_FORBIDDEN")
		}
	}
	actualState := "UNKNOWN"
	switch state {
	case "running":
		actualState = "RUNNING"
	case "created", "exited":
		actualState = "STOPPED"
	case "dead":
		actualState = "FAILED"
	}
	return &SoftwareObservation{SoftwareMetadata: SoftwareMetadata{VersionSource: "IMAGE_REFERENCE", VersionStatus: "IMAGE_ONLY"}, Kind: s.Kind, Locator: id, IdentityHash: Digest([]string{s.Kind, id, imageID}), State: actualState, Version: image, ObservedAt: stamp()}, nil
}

func observeSoftwareTool(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner) (*SoftwareObservation, error) {
	command, arg := "/usr/bin/java", "-version"
	switch s.Locator {
	case "python":
		command, arg = "/usr/bin/python3", "--version"
	case "ffmpeg":
		command, arg = "/usr/bin/ffmpeg", "-version"
	}
	data, err := softwareCommand(ctx, run, command, arg)
	if err != nil {
		return nil, errors.New("SOFTWARE_TOOL_UNAVAILABLE")
	}
	version := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if len(version) == 0 || len(version) > 512 {
		return nil, errors.New("SOFTWARE_TOOL_VERSION_INVALID")
	}
	return &SoftwareObservation{SoftwareMetadata: SoftwareMetadata{VersionSource: "VERSION_COMMAND", ExecutablePath: command}, Kind: s.Kind, Locator: s.Locator, IdentityHash: Digest([]string{s.Kind, command, version}), State: "INSTALLED", Version: version, ObservedAt: stamp()}, nil
}

// ReadSoftwareLogs follows no arbitrary path and never runs docker exec. The
// caller receives only a bounded recent tail, after the same identity check.
func ReadSoftwareLogs(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner) (*SoftwareObservation, error) {
	if s.Kind == "TOOL" {
		return nil, errors.New("SOFTWARE_TOOL_HAS_NO_SERVICE_LOG")
	}
	actual, err := ObserveSoftware(ctx, s, run)
	if err != nil {
		return nil, err
	}
	var data []byte
	if s.Kind == "SYSTEMD" {
		data, err = softwareCommand(ctx, run, "/usr/bin/journalctl", "--no-pager", "--quiet", "--lines=200", "--output=short-iso", "--unit="+s.Locator)
	} else {
		data, err = softwareCommand(ctx, run, "/usr/bin/docker", "logs", "--timestamps", "--tail", "200", actual.Locator)
	}
	if err != nil {
		return nil, errors.New("SOFTWARE_LOG_READ_DENIED")
	}
	actual.Log, actual.Truncated = RedactSoftwareLogs(string(data)), true
	return actual, nil
}

var softwareSensitiveAssignment = regexp.MustCompile(`(?i)((?:password|passwd|pwd|secret|token|api[_-]?key|authorization|credential)["']?\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
var softwareURLCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)
var softwareBearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-zA-Z0-9+/_.=:-]+`)

func RedactSoftwareLogs(value string) string {
	value = softwareURLCredentials.ReplaceAllString(value, "${1}[REDACTED]@")
	value = softwareBearer.ReplaceAllString(value, "${1} [REDACTED]")
	value = softwareSensitiveAssignment.ReplaceAllString(value, "${1}[REDACTED]")
	if len(value) > 24000 {
		value = value[len(value)-24000:]
	}
	return strings.ToValidUTF8(value, "�")
}
