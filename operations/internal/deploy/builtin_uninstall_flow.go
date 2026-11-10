package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sort"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Both uninstall paths use the published list. Preparation freezes ownership
// before a command runs; confirmation checks absence before detaching records.
func (x *executor) uninstallBuiltinFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Uninstallation.Installation
	official := serviceinstall.IsOfficial(i.Definition)
	engine := i.Definition.Key == "docker"
	ctx, cancel := context.WithTimeout(context.Background(), nativeDeadline(req))
	defer cancel()
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	prepared, shared := false, false
	var actual *SoftwareObservation
	var installed officialInstallation
	var dockerOwner dockerPackageOwnership
	lists := map[string][]string{}
	system := func(step serviceinstall.CommandStep) error {
		switch step.Action {
		case "UNINSTALL_PREPARE":
			var err error
			if official {
				var original Request
				original, installed, err = x.officialUninstallInstallation(req)
				if err != nil {
					return err
				}
				if err = x.verifyOfficialInstalled(ctx, original, installed); err != nil {
					return err
				}
				if req.Uninstallation.DeleteData {
					if err = validateOfficialUninstallPaths(i.Definition, installed.Plan); err != nil {
						return err
					}
				}
				if engine {
					if err = x.dockerEngineHasNoContainers(ctx); err != nil {
						return err
					}
				}
				out, e := x.softwareRunner(ctx, "/usr/bin/apt-get", officialRemovalArgs(true, installed.Plan)...)
				if e != nil || validateOfficialRemovalPlan(out, installed.Plan) != nil {
					return errors.New("OFFICIAL_UNINSTALL_PLAN_UNSAFE")
				}
				if err = x.verifyOfficialInstalled(ctx, original, installed); err != nil {
					return err
				}
				args := officialRemovalArgs(false, installed.Plan)
				lists["OPS_PACKAGE_ARGUMENTS"] = args[slices.Index(args, "--")+1:]
				lists["OPS_OWNED_PATHS"] = installed.Plan.Paths.OwnedPaths
				units := softwareSystemdAction(*req.Software, "stop")
				lists["OPS_SERVICE_UNITS"] = units[slices.Index(units, "--")+1:]
			} else {
				root, err = x.uninstallRoot(req)
				if err != nil {
					return err
				}
				if err = validateUninstallTree(root, req.Uninstallation.DeleteData); err != nil {
					return err
				}
				if engine {
					if err = x.preflightDockerEngineUninstall(ctx, req); err != nil {
						return err
					}
					dockerOwner, err = x.dockerRemovalOwnership(req)
					if err != nil {
						return err
					}
					for _, p := range dockerOwner.Packages {
						lists["OPS_PACKAGE_ARGUMENTS"] = append(lists["OPS_PACKAGE_ARGUMENTS"], p.Name)
					}
					sort.Strings(lists["OPS_PACKAGE_ARGUMENTS"])
				}
				if req.Software.Kind == "SYSTEMD" && !engine {
					if req.Software.Locator != nativeUnit(req.Software.ID) {
						return errors.New("SOFTWARE_UNINSTALL_UNIT_NOT_OWNED")
					}
					if err = secureAncestors(nativeUnitPath(req.Software.ID)); err != nil {
						return err
					}
				}
				if req.Software.Kind == "TOOL" && i.Config.Mode() != "NATIVE" {
					shared, err = x.uninstallToolShared(ctx, req)
					if err != nil {
						return err
					}
				}
			}
			actual, err = ObserveSoftware(ctx, *req.Software, x.softwareRunner)
			if err != nil {
				return err
			}
			if actual.State == "UNKNOWN" {
				return errors.New("SOFTWARE_STATE_TRANSITIONING")
			}
			if official {
				err = atomicJSON(filepath.Join(dir, "software-uninstall.json"), officialUninstallMarker{JobID: req.JobID, InputHash: req.InputHash, Software: *req.Software, Installation: installed, DeleteData: req.Uninstallation.DeleteData})
			} else {
				err = atomicJSON(filepath.Join(dir, "software-uninstall.json"), softwareUninstallMarker{JobID: req.JobID, InputHash: req.InputHash, Root: root, Software: *req.Software, SharedProgramRetained: shared})
			}
			prepared = err == nil
			return err
		case "UNINSTALL_CONFIRM":
			if official {
				result = x.finishOfficialUninstall(req, dir)
			} else {
				var err error
				result, err = x.finishSoftwareUninstall(req, dir, root)
				if err != nil {
					return err
				}
			}
			if result.Status != "SUCCEEDED" {
				return errors.New("SOFTWARE_UNINSTALL_UNCONFIRMED")
			}
			return nil
		default:
			return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
		}
	}
	command := func(step serviceinstall.CommandStep) ([]byte, error) {
		if !prepared {
			return nil, errors.New("COMMAND_PREPARATION_REQUIRED")
		}
		if step.ID == "DOCKER_IMAGE_REMOVE" && shared || step.ID == "DOCKER_STOP" && actual.State != "RUNNING" {
			return nil, x.skipBuiltinStep(req, step, "软件已停止，或镜像仍被其他实例使用")
		}
		if engine && (step.ID == "OFFICIAL_STOP" || step.ID == "OFFICIAL_PACKAGES_REMOVE" || step.ID == "DOCKER_PACKAGES_REMOVE") {
			if err := x.dockerEngineHasNoContainers(ctx); err != nil {
				return nil, err
			}
		}
		if step.ID == "NATIVE_REMOVE_UNIT" {
			stopped, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
			if err != nil || stopped.State != "STOPPED" && stopped.State != "FAILED" {
				return nil, errors.New("SOFTWARE_UNINSTALL_STOP_UNCONFIRMED")
			}
		}
		if step.ID == "NATIVE_REMOVE_PROGRAM" {
			if err := validateUninstallTree(root, false); err != nil {
				return nil, err
			}
		}
		if step.Phase == "DELETE_DATA" {
			if official {
				if err := validateOfficialUninstallPaths(i.Definition, installed.Plan); err != nil {
					return nil, err
				}
			} else if err := validateUninstallTree(root, true); err != nil {
				return nil, err
			}
			probe := req
			uninstall := *req.Uninstallation
			uninstall.DeleteData = false
			probe.Uninstallation = &uninstall
			if !x.softwareProgramAbsent(probe) {
				return nil, errors.New("SOFTWARE_UNINSTALL_PROGRAM_UNCONFIRMED")
			}
			if engine && !official {
				hash, err := fileSHA("/etc/docker/daemon.json")
				if err != nil || hash != dockerOwner.DaemonHash || secureAncestors("/etc/docker/daemon.json") != nil {
					return nil, errors.New("DOCKER_CONFIGURATION_CHANGED")
				}
			}
		}
		out, _, err := x.runBuiltinStep(ctx, req, step.ID, nil, lists, "")
		if err == nil && step.ID == "OFFICIAL_STOP" && (!x.officialUnitState(ctx, installed.Plan.Paths.Unit, false) || engine && !x.softwareDockerSocketInactive(ctx)) {
			err = errors.New("OFFICIAL_UNINSTALL_STOP_UNCONFIRMED")
		}
		return out, err
	}
	phases := []string{"UNINSTALL"}
	if req.Uninstallation.DeleteData {
		phases = append(phases, "DELETE_DATA")
	}
	if err := x.runBuiltinFlow(ctx, req, phases, system, command); err != nil {
		if !prepared {
			return result, err
		}
		result = softwareResult(req)
		result.ErrorCode = softwareUninstallErrorCode(err)
	}
	return result, nil
}
