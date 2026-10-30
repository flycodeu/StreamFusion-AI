package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Uninstallation freezes the installation and exact bound object. Empty fields
// are omitted so existing job input hashes and durable receipts stay unchanged.
type SoftwareUninstallRequest struct {
	Installation serviceinstall.Request `json:"installation"`
	DeleteData   bool                   `json:"deleteData,omitempty"`
}

type SoftwareUninstallEvidence struct {
	SoftwareID            string `json:"softwareId"`
	Kind                  string `json:"kind"`
	Locator               string `json:"locator"`
	IdentityHash          string `json:"identityHash"`
	ProgramRemoved        bool   `json:"programRemoved"`
	SharedProgramRetained bool   `json:"sharedProgramRetained,omitempty"`
	DeleteData            bool   `json:"deleteData"`
	DataRemoved           bool   `json:"dataRemoved"`
	DataRoot              string `json:"dataRoot"`
	ObservedAt            string `json:"observedAt"`
}

type softwareUninstallMarker struct {
	JobID, InputHash, Root string
	Software               SoftwareRequest
	SharedProgramRetained  bool
}

func validateSoftwareUninstall(req Request) error {
	u, s := req.Uninstallation, req.Software
	if u == nil || s == nil || req.SoftwareOperation != "SOFTWARE_UNINSTALL" || req.Installation != nil || req.Reconfiguration != nil || s.Validate(false) != nil || s.Ownership == nil || u.Installation.Validate() != nil {
		return errors.New("SOFTWARE_UNINSTALL_INPUT_INVALID")
	}
	i, o := u.Installation, s.Ownership
	if i.SoftwareID != s.ID || i.BindingRevision != s.Version || o.ControllerID != req.ControllerID || o.TargetID != req.TargetID || o.ConfigHash != Digest(i.Config) || s.SoftwareKey != strings.ToUpper(i.Definition.Key) || (i.Config.Mode() == "NATIVE") != (o.RuntimeMode == "NATIVE") {
		return errors.New("SOFTWARE_UNINSTALL_OWNERSHIP_MISMATCH")
	}
	return nil
}

func (x *executor) uninstallRoot(req Request) (string, error) {
	i := req.Uninstallation.Installation
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	if filepath.Base(root) != req.Software.ID || filepath.Clean(root) != root || !filepath.IsAbs(root) || root == x.root || filepath.Dir(root) == root {
		return "", errors.New("SOFTWARE_UNINSTALL_PATH_INVALID")
	}
	if req.Software.Ownership.RuntimeMode == "NATIVE" && i.Definition.Key != "docker" && req.Software.Ownership.BinaryPath != filepath.Join(root, serviceinstall.NativeBinaryRelative(i.Definition)) {
		return "", errors.New("SOFTWARE_UNINSTALL_PROGRAM_PATH_MISMATCH")
	}
	return root, nil
}

func (x *executor) uninstallSoftware(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	if err := validateSoftwareUninstall(req); err != nil {
		return result, err
	}
	if err := x.boundSoftware(*req.Software); err != nil {
		return result, err
	}
	root, err := x.uninstallRoot(req)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil || actual.State == "UNKNOWN" {
		if err == nil {
			err = errors.New("SOFTWARE_STATE_TRANSITIONING")
		}
		return result, err
	}
	if err = validateUninstallTree(root, req.Uninstallation.DeleteData); err != nil {
		return result, err
	}
	engine := req.Uninstallation.Installation.Definition.Key == "docker"
	if engine {
		if err = x.preflightDockerEngineUninstall(ctx, req); err != nil {
			return result, err
		}
	}
	if req.Software.Kind == "SYSTEMD" && !engine {
		if req.Software.Locator != nativeUnit(req.Software.ID) {
			return result, errors.New("SOFTWARE_UNINSTALL_UNIT_NOT_OWNED")
		}
		if err = secureAncestors(nativeUnitPath(req.Software.ID)); err != nil {
			return result, err
		}
	}
	// This marker precedes every mutation. Recovery only observes the outcome;
	// it never repeats stop/remove/delete after a lost response.
	marker := softwareUninstallMarker{JobID: req.JobID, InputHash: req.InputHash, Root: root, Software: *req.Software}
	if req.Software.Kind == "TOOL" && req.Software.Ownership.RuntimeMode != "NATIVE" {
		marker.SharedProgramRetained, err = x.uninstallToolShared(ctx, req)
		if err != nil {
			return result, err
		}
	}
	if err = atomicJSON(filepath.Join(dir, "software-uninstall.json"), marker); err != nil {
		return result, err
	}
	if engine {
		if err = x.removeDockerEnginePackages(ctx, req, dir); err != nil {
			return softwareUninstallEngineFailure(req, err)
		}
	} else if req.Software.Kind == "TOOL" && req.Software.Ownership.RuntimeMode != "NATIVE" && !marker.SharedProgramRetained {
		i := req.Uninstallation.Installation
		if _, err = x.softwareRunner(ctx, "/usr/bin/docker", "image", "rm", "--", i.Definition.Image+"@"+i.Definition.Digest); err != nil {
			return x.reconcileSoftwareUninstall(req, dir), nil
		}
	} else if req.Software.Kind == "DOCKER" {
		if actual.State == "RUNNING" {
			if _, err = x.softwareRunner(ctx, "/usr/bin/docker", "container", "stop", "--time", "20", "--", req.Software.Locator); err != nil {
				return result, nil
			}
		}
		if _, err = x.softwareRunner(ctx, "/usr/bin/docker", "container", "rm", "--", req.Software.Locator); err != nil {
			return x.reconcileSoftwareUninstall(req, dir), nil
		}
	} else if req.Software.Kind == "SYSTEMD" {
		if _, err = x.softwareRunner(ctx, "/usr/bin/systemctl", "disable", "--now", "--", req.Software.Locator); err != nil {
			return result, nil
		}
		// Reobserve the exact identity after the stop boundary before unlinking.
		stopped, observeErr := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
		if observeErr != nil || stopped.State != "STOPPED" && stopped.State != "FAILED" {
			return result, nil
		}
		if err = os.Remove(nativeUnitPath(req.Software.ID)); err != nil {
			return result, nil
		}
		if _, err = x.softwareRunner(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			return result, nil
		}
	}
	if req.Software.Ownership.RuntimeMode == "NATIVE" && !engine {
		if err = removeOwnedUninstallPaths(root, []string{"bin", "runtime"}, false); err != nil {
			return result, nil
		}
	}
	if req.Uninstallation.DeleteData {
		// Include old configuration snapshots and logs only after explicit data
		// confirmation; program-only removal leaves all of these untouched.
		if err = removeOwnedUninstallPaths(root, []string{"."}, true); err != nil {
			return result, nil
		}
	}
	return x.finishSoftwareUninstall(req, dir, root)
}

func softwareUninstallEngineFailure(req Request, err error) (Result, error) {
	result := softwareResult(req)
	result.ErrorCode = softwareUninstallErrorCode(err)
	var notStarted *dockerRemovalNotStartedError
	if errors.As(err, &notStarted) {
		return result, err
	}
	return result, nil
}

func softwareUninstallErrorCode(err error) string {
	code := err.Error()
	if len(code) > 0 && len(code) <= 96 {
		valid := true
		for _, r := range code {
			if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				valid = false
				break
			}
		}
		if valid {
			return code
		}
	}
	return "SOFTWARE_UNINSTALL_UNCONFIRMED"
}

func (x *executor) softwareProgramAbsent(req Request) bool {
	s := req.Software
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if req.Uninstallation.Installation.Definition.Key == "docker" {
		return x.dockerEngineUninstalled(ctx, req)
	}
	if s.Kind == "TOOL" && s.Ownership.RuntimeMode != "NATIVE" {
		var marker softwareUninstallMarker
		if readJSON(filepath.Join(x.root, "jobs", req.JobID, "software-uninstall.json"), &marker) != nil {
			return false
		}
		if marker.SharedProgramRetained {
			return true
		}
		out, err := x.softwareRunner(ctx, "/usr/bin/docker", "image", "ls", "--all", "--no-trunc", "--format", "{{.ID}}")
		if err != nil || len(out) >= 64<<10 {
			return false
		}
		for _, id := range strings.Fields(string(out)) {
			if !strings.HasPrefix(id, "sha256:") || !Hex.MatchString(strings.TrimPrefix(id, "sha256:")) || id == s.Ownership.ImageID {
				return false
			}
		}
		return true
	}
	if s.Kind == "DOCKER" {
		// Successful, exhaustive enumeration proves absence. An inspect error
		// alone may mean permissions, transport failure, or a stopped daemon.
		out, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "ls", "--all", "--no-trunc", "--format", "{{.ID}}")
		if err != nil || len(out) >= 64<<10 {
			return false
		}
		for _, id := range strings.Fields(string(out)) {
			if !Hex.MatchString(id) || id == s.Locator {
				return false
			}
		}
		return true
	}
	if s.Kind == "SYSTEMD" {
		if _, err := os.Lstat(nativeUnitPath(s.ID)); !os.IsNotExist(err) {
			return false
		}
		out, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=LoadState,ActiveState", "--", s.Locator)
		if err != nil || !strings.Contains(string(out), "LoadState=not-found\n") || !strings.Contains(string(out), "ActiveState=inactive\n") {
			return false
		}
	}
	if s.Ownership.RuntimeMode == "NATIVE" {
		root, err := x.uninstallRoot(req)
		if err != nil {
			return false
		}
		for _, name := range []string{"bin", "runtime"} {
			if _, err = os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
				return false
			}
		}
	}
	return true
}

func (x *executor) finishSoftwareUninstall(req Request, dir, root string) (Result, error) {
	result := softwareResult(req)
	result.ErrorCode = "SOFTWARE_UNINSTALL_UNCONFIRMED"
	if _, err := os.Lstat(x.softwareFile(req.Software.ID)); err == nil {
		if x.boundSoftware(*req.Software) != nil {
			return result, nil
		}
	} else if !os.IsNotExist(err) {
		return result, nil
	}
	if !x.softwareProgramAbsent(req) {
		return result, nil
	}
	if req.Uninstallation.DeleteData {
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			return result, nil
		}
	}
	var marker softwareUninstallMarker
	if readJSON(filepath.Join(dir, "software-uninstall.json"), &marker) != nil {
		return result, nil
	}
	evidence := SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: !marker.SharedProgramRetained, SharedProgramRetained: marker.SharedProgramRetained, DeleteData: req.Uninstallation.DeleteData, DataRemoved: req.Uninstallation.DeleteData, DataRoot: root, ObservedAt: stamp()}
	// Persist a target removal receipt before detaching metadata. The receipt
	// records verified absence, independently of controller row retention.
	if err := atomicJSON(filepath.Join(dir, "software-uninstalled.json"), evidence); err != nil {
		return result, nil
	}
	if err := os.Remove(x.softwareFile(req.Software.ID)); err != nil && !os.IsNotExist(err) {
		return result, nil
	}
	if req.Software.Kind == "TOOL" && req.Software.Ownership.RuntimeMode != "NATIVE" && serviceinstall.UsesJavaBridge(req.Uninstallation.Installation.Definition) {
		if err := os.Remove(filepath.Join(x.root, "plugins", req.Software.ID+".json")); err != nil && !os.IsNotExist(err) {
			return result, nil
		}
	}
	result.Status, result.Phase, result.ErrorCode, result.Uninstallation = "SUCCEEDED", "SOFTWARE_UNINSTALLED", "", &evidence
	return result, nil
}

func (x *executor) reconcileSoftwareUninstall(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "SOFTWARE_UNINSTALL_UNCONFIRMED"
	root, err := x.uninstallRoot(req)
	var marker softwareUninstallMarker
	if err != nil || readJSON(filepath.Join(dir, "software-uninstall.json"), &marker) != nil || marker.JobID != req.JobID || marker.InputHash != req.InputHash || marker.Root != root || !reflect.DeepEqual(marker.Software, *req.Software) {
		return result
	}
	finished, _ := x.finishSoftwareUninstall(req, dir, root)
	return finished
}

func (x *executor) verifySoftwareUninstall(req Request, result Result) error {
	e, s := result.Uninstallation, req.Software
	root, err := x.uninstallRoot(req)
	if err != nil || e == nil || e.SoftwareID != s.ID || e.Kind != s.Kind || e.Locator != s.Locator || e.IdentityHash != s.IdentityHash || !e.ProgramRemoved && !(s.Kind == "TOOL" && s.Ownership != nil && s.Ownership.RuntimeMode != "NATIVE" && e.SharedProgramRetained) || e.ProgramRemoved && e.SharedProgramRetained || e.DeleteData != req.Uninstallation.DeleteData || e.DataRemoved != e.DeleteData || e.DataRoot != root {
		return errors.New("SOFTWARE_UNINSTALL_RECEIPT_MISMATCH")
	}
	if _, err = time.Parse(time.RFC3339Nano, e.ObservedAt); err != nil {
		return errors.New("SOFTWARE_UNINSTALL_RECEIPT_MISMATCH")
	}
	var saved SoftwareUninstallEvidence
	if readJSON(filepath.Join(x.root, "jobs", req.JobID, "software-uninstalled.json"), &saved) != nil || !reflect.DeepEqual(saved, *e) {
		return errors.New("SOFTWARE_UNINSTALL_RECEIPT_MISMATCH")
	}
	return nil
}

func (x *executor) uninstallToolShared(ctx context.Context, req Request) (bool, error) {
	s := req.Software
	if serviceinstall.UsesJavaBridge(req.Uninstallation.Installation.Definition) {
		original := req
		original.JobID, original.Installation = s.Ownership.JobID, &req.Uninstallation.Installation
		if err := x.verifyToolBridge(original); err != nil {
			return false, err
		}
	}
	for _, directory := range []string{"software", "plugins"} {
		files, err := filepath.Glob(filepath.Join(x.root, directory, "*.json"))
		if err != nil {
			return false, err
		}
		for _, file := range files {
			if filepath.Base(file) == s.ID+".json" {
				continue
			}
			if directory == "software" {
				var other softwareBinding
				if readJSON(file, &other) != nil {
					return false, errors.New("SOFTWARE_BINDING_DAMAGED")
				}
				if other.Software.Ownership != nil && other.Software.Ownership.ImageID == s.Ownership.ImageID {
					return true, nil
				}
			} else {
				var other pluginMaterial
				if readJSON(file, &other) != nil {
					return false, errors.New("PLUGIN_RECORD_DAMAGED")
				}
				if other.ImageID == s.Ownership.ImageID {
					return true, nil
				}
			}
		}
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "ls", "--all", "--no-trunc", "--filter", "ancestor="+s.Ownership.ImageID, "--format", "{{.ID}}")
	if err != nil || len(out) >= 64<<10 {
		return false, errors.New("SOFTWARE_TOOL_DEPENDENCIES_UNKNOWN")
	}
	for _, id := range strings.Fields(string(out)) {
		if !Hex.MatchString(id) {
			return false, errors.New("SOFTWARE_TOOL_DEPENDENCIES_UNKNOWN")
		}
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}
