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

// The generic uninstall marker filename also prevents NOT_STARTED inference.
// All frozen installation evidence precedes the first stop/remove mutation.
type officialUninstallMarker struct {
	JobID, InputHash string
	Software         SoftwareRequest
	Installation     officialInstallation
	DeleteData       bool
}

func officialDirectPackages(plan serviceinstall.OfficialPlan) []dockerPackage {
	packages := []dockerPackage{}
	for _, p := range plan.Packages {
		if p.Direct {
			packages = append(packages, dockerPackage{Name: p.Name, Version: p.Version})
		}
	}
	return packages
}

func validateOfficialRemovalPlan(output []byte, plan serviceinstall.OfficialPlan) error {
	if err := validateDockerAptPlan(output, officialDirectPackages(plan), true); err != nil {
		return errors.New("OFFICIAL_UNINSTALL_PLAN_UNSAFE")
	}
	return nil
}

func officialRemovalArgs(simulate bool, plan serviceinstall.OfficialPlan) []string {
	names := []string{}
	for _, p := range officialDirectPackages(plan) {
		names = append(names, p.Name)
	}
	// --no-download and AutomaticRemove=false preserve the dependency graph;
	// apt is allowed to remove only explicit direct components, never purge.
	return dockerAptArgs(simulate, true, names)
}

func (x *executor) officialUninstallInstallation(req Request) (Request, officialInstallation, error) {
	var original Request
	var marker officialInstallation
	i, o := req.Uninstallation.Installation, req.Software.Ownership
	dir := filepath.Join(x.root, "jobs", o.JobID)
	if readJSON(filepath.Join(dir, "intent.json"), &original) != nil || original.Installation == nil || original.SoftwareOperation != "SOFTWARE_INSTALL" || original.JobID != o.JobID || original.ControllerID != req.ControllerID || original.TargetID != req.TargetID || original.Installation.SoftwareID != req.Software.ID || validateSoftwareInput(original) != nil || !reflect.DeepEqual(original.Installation, &i) {
		return original, marker, errors.New("OFFICIAL_UNINSTALL_INSTALLATION_MISMATCH")
	}
	if readJSON(filepath.Join(dir, "official-apply.json"), &marker) != nil || marker.JobID != original.JobID || marker.InputHash != original.InputHash || marker.SoftwareID != i.SoftwareID || marker.ConfigHash != Digest(i.Config) || marker.Plan.Validate(i.Definition) != nil || !reflect.DeepEqual(&marker.Plan, i.Official) || marker.BinaryHash != o.BinaryHash {
		return original, marker, errors.New("OFFICIAL_UNINSTALL_INSTALLATION_MISMATCH")
	}
	if marker.Plan.Paths.Unit != "" && (marker.UnitPath != "/usr/lib/systemd/system/"+marker.Plan.Paths.Unit || !Hex.MatchString(marker.UnitHash)) || marker.Plan.Paths.Unit == "" && (marker.UnitPath != "" || marker.UnitHash != "") {
		return original, marker, errors.New("OFFICIAL_UNINSTALL_INSTALLATION_MISMATCH")
	}
	return original, marker, nil
}

func (x *executor) uninstallOfficialSoftware(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	original, installed, err := x.officialUninstallInstallation(req)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err = x.verifyOfficialInstalled(ctx, original, installed); err != nil {
		return result, err
	}
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil || actual.State == "UNKNOWN" {
		return result, errors.New("OFFICIAL_UNINSTALL_IDENTITY_UNCONFIRMED")
	}
	plan := installed.Plan
	if req.Uninstallation.DeleteData {
		if err = validateOfficialUninstallPaths(req.Uninstallation.Installation.Definition, plan); err != nil {
			return result, err
		}
	}
	if req.Software.SoftwareKey == "DOCKER" {
		if err = x.dockerEngineHasNoContainers(ctx); err != nil {
			return result, err
		}
	}
	out, err := x.softwareRunner(ctx, "/usr/bin/apt-get", officialRemovalArgs(true, plan)...)
	if err != nil || validateOfficialRemovalPlan(out, plan) != nil {
		return result, errors.New("OFFICIAL_UNINSTALL_PLAN_UNSAFE")
	}
	// Recheck all frozen bytes/packages after simulation and before the marker.
	if err = x.verifyOfficialInstalled(ctx, original, installed); err != nil {
		return result, err
	}
	marker := officialUninstallMarker{JobID: req.JobID, InputHash: req.InputHash, Software: *req.Software, Installation: installed, DeleteData: req.Uninstallation.DeleteData}
	if err = atomicJSON(filepath.Join(dir, "software-uninstall.json"), marker); err != nil {
		return result, err
	}
	unknown := func(code string) (Result, error) { result.ErrorCode = code; return result, nil }
	if plan.Paths.Unit != "" {
		if req.Software.SoftwareKey == "DOCKER" {
			if err = x.dockerEngineHasNoContainers(ctx); err != nil {
				// No stop or package mutation has started, so this is a refusal.
				return result, err
			}
		}
		if _, err = x.softwareRunner(ctx, "/usr/bin/systemctl", softwareSystemdAction(*req.Software, "stop")...); err != nil {
			return unknown("OFFICIAL_UNINSTALL_STOP_UNCONFIRMED")
		}
		if !x.officialUnitState(ctx, plan.Paths.Unit, false) || req.Software.SoftwareKey == "DOCKER" && !x.softwareDockerSocketInactive(ctx) {
			return unknown("OFFICIAL_UNINSTALL_STOP_UNCONFIRMED")
		}
	}
	out, err = x.softwareRunner(ctx, "/usr/bin/apt-get", officialRemovalArgs(false, plan)...)
	x.appendServiceLog(req, "OFFICIAL_PACKAGES_REMOVE", string(out))
	if err != nil {
		return x.reconcileOfficialUninstall(req, dir), nil
	}
	if !x.officialProgramAbsent(ctx, req) {
		return unknown("OFFICIAL_UNINSTALL_PROGRAM_UNCONFIRMED")
	}
	if req.Uninstallation.DeleteData {
		if err = removeOfficialUninstallPaths(req.Uninstallation.Installation.Definition, plan); err != nil {
			return unknown(softwareUninstallErrorCode(err))
		}
	}
	return x.finishOfficialUninstall(req, dir), nil
}

func (x *executor) officialUnitState(ctx context.Context, unit string, absent bool) bool {
	out, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=LoadState,ActiveState", "--", unit)
	if err != nil || len(out) >= 64<<10 {
		return false
	}
	properties := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || properties[key] != "" {
			return false
		}
		properties[key] = value
	}
	return properties["ActiveState"] == "inactive" && (!absent || properties["LoadState"] == "not-found")
}

func (x *executor) officialProgramAbsent(ctx context.Context, req Request) bool {
	i := req.Uninstallation.Installation
	if i.Official == nil || i.Official.Validate(i.Definition) != nil {
		return false
	}
	installed, err := x.installedDockerPackages(ctx)
	if err != nil {
		return false
	}
	for _, p := range officialDirectPackages(*i.Official) {
		if installed[p.Name] != "" {
			return false
		}
	}
	if _, err = os.Lstat(i.Official.Paths.Binary); !os.IsNotExist(err) {
		return false
	}
	unit := i.Official.Paths.Unit
	if unit == "" {
		return true
	}
	units := []string{unit}
	if i.Definition.Key == "docker" {
		units = append(units, "docker.socket")
	}
	for _, name := range units {
		for _, directory := range []string{"/etc/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system", "/run/systemd/system"} {
			if _, err = os.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
				return false
			}
		}
		if !x.officialUnitState(ctx, name, true) {
			return false
		}
	}
	return true
}

func validateOfficialUninstallPaths(d serviceinstall.Definition, plan serviceinstall.OfficialPlan) error {
	if plan.Validate(d) != nil {
		return errors.New("OFFICIAL_UNINSTALL_PATH_INVALID")
	}
	for _, directory := range plan.Paths.OwnedPaths {
		if err := validateOfficialUninstallDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func validateOfficialUninstallDirectory(directory string) error {
	forbidden := map[string]bool{"/": true, "/etc": true, "/usr": true, "/var": true, "/var/lib": true, "/var/log": true, "/var/www": true, "/var/lib/containerd": true}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || forbidden[directory] || filepath.Dir(directory) == directory {
		return errors.New("OFFICIAL_UNINSTALL_PATH_INVALID")
	}
	// Service-owned data roots need not belong to root. Their fixed parent
	// chain must be protected, and the removable directory cannot be a link.
	if secureAncestors(existingServiceParent(filepath.Dir(directory))) != nil {
		return errors.New("OFFICIAL_UNINSTALL_PATH_UNSAFE")
	}
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("OFFICIAL_UNINSTALL_PATH_UNSAFE")
	}
	if err = uninstallMountBoundary(directory); err != nil {
		return err
	}
	// RemoveAll below unlinks internal or external symlinks without following
	// targets. Shared /etc/alternatives and content outside this root survive.
	return validateUninstallSubtree(directory, false)
}

func removeOfficialUninstallPaths(d serviceinstall.Definition, plan serviceinstall.OfficialPlan) error {
	if err := validateOfficialUninstallPaths(d, plan); err != nil {
		return err
	}
	for _, directory := range plan.Paths.OwnedPaths {
		if err := validateOfficialUninstallDirectory(directory); err != nil {
			return err
		}
		if _, err := os.Lstat(directory); os.IsNotExist(err) {
			continue
		}
		root, err := os.OpenRoot(filepath.Dir(directory))
		if err != nil {
			return err
		}
		err = root.RemoveAll(filepath.Base(directory))
		root.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (x *executor) readOfficialUninstallMarker(req Request, dir string) (officialUninstallMarker, error) {
	var marker officialUninstallMarker
	if validateSoftwareUninstall(req) != nil || readJSON(filepath.Join(dir, "software-uninstall.json"), &marker) != nil || marker.JobID != req.JobID || marker.InputHash != req.InputHash || !reflect.DeepEqual(marker.Software, *req.Software) || marker.DeleteData != req.Uninstallation.DeleteData || !reflect.DeepEqual(&marker.Installation.Plan, req.Uninstallation.Installation.Official) || marker.Installation.JobID != req.Software.Ownership.JobID || marker.Installation.ConfigHash != req.Software.Ownership.ConfigHash || marker.Installation.SoftwareID != req.Software.ID || marker.Installation.BinaryHash != req.Software.Ownership.BinaryHash {
		return marker, errors.New("OFFICIAL_UNINSTALL_MARKER_MISMATCH")
	}
	_, installed, err := x.officialUninstallInstallation(req)
	if err != nil || !reflect.DeepEqual(installed, marker.Installation) {
		return marker, errors.New("OFFICIAL_UNINSTALL_MARKER_MISMATCH")
	}
	return marker, nil
}

func (x *executor) finishOfficialUninstall(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "OFFICIAL_UNINSTALL_UNCONFIRMED"
	marker, err := x.readOfficialUninstallMarker(req, dir)
	if err != nil {
		return result
	}
	if _, err = os.Lstat(x.softwareFile(req.Software.ID)); err == nil {
		if x.boundSoftware(*req.Software) != nil {
			return result
		}
	} else if !os.IsNotExist(err) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if !x.officialProgramAbsent(ctx, req) {
		return result
	}
	if req.Uninstallation.DeleteData {
		for _, directory := range marker.Installation.Plan.Paths.OwnedPaths {
			if _, err = os.Lstat(directory); !os.IsNotExist(err) {
				return result
			}
		}
	}
	evidence := SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: req.Uninstallation.DeleteData, DataRemoved: req.Uninstallation.DeleteData, DataRoot: marker.Installation.Plan.Paths.Data, ObservedAt: stamp()}
	if evidence.DeleteData {
		evidence.RemovedPaths = append([]string(nil), marker.Installation.Plan.Paths.OwnedPaths...)
	}
	if atomicJSON(filepath.Join(dir, "software-uninstalled.json"), evidence) != nil {
		return result
	}
	if err = os.Remove(x.softwareFile(req.Software.ID)); err != nil && !os.IsNotExist(err) {
		return result
	}
	result.Status, result.Phase, result.ErrorCode, result.Uninstallation = "SUCCEEDED", "SOFTWARE_UNINSTALLED", "", &evidence
	return result
}

func (x *executor) reconcileOfficialUninstall(req Request, dir string) Result {
	// Recovery only enumerates dpkg and observes program/unit/path absence. It
	// never stops, removes a package, deletes data, or replays a command.
	return x.finishOfficialUninstall(req, dir)
}

func (x *executor) verifyOfficialUninstall(req Request, result Result) error {
	e := result.Uninstallation
	marker, err := x.readOfficialUninstallMarker(req, filepath.Join(x.root, "jobs", req.JobID))
	if err != nil || e == nil || e.SoftwareID != req.Software.ID || e.Kind != req.Software.Kind || e.Locator != req.Software.Locator || e.IdentityHash != req.Software.IdentityHash || !e.ProgramRemoved || e.SharedProgramRetained || e.DeleteData != req.Uninstallation.DeleteData || e.DataRemoved != e.DeleteData || e.DataRoot != marker.Installation.Plan.Paths.Data {
		return errors.New("SOFTWARE_UNINSTALL_RECEIPT_MISMATCH")
	}
	expected := []string(nil)
	if e.DeleteData {
		expected = append(expected, marker.Installation.Plan.Paths.OwnedPaths...)
	}
	if !reflect.DeepEqual(e.RemovedPaths, expected) {
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
