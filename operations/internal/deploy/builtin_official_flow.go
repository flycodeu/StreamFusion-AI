package deploy

import (
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (x *executor) installOfficialFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	if i.Official == nil || i.Official.Validate(i.Definition) != nil {
		return result, errors.New("OFFICIAL_PLAN_REQUIRED")
	}
	p := x.officialInstallPreflight(*i)
	for _, c := range p.Checks {
		if c.Status != "PASS" {
			return result, errors.New(c.Code)
		}
	}
	if Digest(p.Official) != Digest(i.Official) {
		return result, errors.New("OFFICIAL_PLAN_CHANGED")
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	marker := officialInstallation{JobID: req.JobID, InputHash: req.InputHash, SoftwareID: i.SoftwareID, ConfigHash: Digest(i.Config), Plan: *i.Official}
	if err := atomicJSON(filepath.Join(dir, "official-update.json"), marker); err != nil {
		return result, err
	}
	var command serviceinstall.OfficialCommand
	var files []string
	system := func(step serviceinstall.CommandStep) error {
		switch step.Action {
		case "INSTALL_MATERIAL":
			var err error
			files, err = x.prepareOfficialDocker(ctx, req, dir)
			return err
		case "OFFICIAL_PLAN":
			installed, err := x.installedDockerPackages(ctx)
			if err != nil {
				return errors.New(officialPackageStateCode(err))
			}
			spec, _ := serviceinstall.OfficialRecipeSpec(i.Definition)
			if err = officialInstallAbsent(spec, installed); err != nil {
				return errors.New(err.Error())
			}
			plan, err := x.officialCandidatePlan(ctx, i.Definition, installed)
			if err != nil {
				return errors.New(err.Error())
			}
			if Digest(plan) != Digest(i.Official) {
				// Refreshing indexes is a completed, bounded preparation. No package
				// apply marker exists, so a changed plan is a final rejection.
				return errors.New("OFFICIAL_PLAN_CHANGED")
			}
			command = i.Official.Commands[1]
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
					return errors.New("OFFICIAL_DOCKER_PLAN_CHANGED")
				}
			} else {
				exact := []string{}
				for _, p := range i.Official.Packages {
					exact = append(exact, p.Name+"="+p.Version)
				}
				out, simulateErr := x.softwareRunner(ctx, "/usr/bin/apt-get", officialAptSimulateArgs(exact)...)
				actual, parseErr := parseOfficialAptPlan(out, spec.Packages, installed)
				if simulateErr != nil || parseErr != nil || Digest(actual) != Digest(i.Official.Packages) {
					return errors.New("OFFICIAL_PLAN_CHANGED")
				}
			}
			return nil
		case "OFFICIAL_OWNERSHIP":
			return x.completeOfficialOwnership(ctx, req, dir, &marker)
		case "INSTALL_CONFIRM":
			result = x.officialInstalledResult(ctx, req, dir, marker)
			if result.Status != "SUCCEEDED" {
				return errors.New("OFFICIAL_RECONCILE_REQUIRED")
			}
			return nil
		}
		return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
	}
	err := x.runBuiltinFlow(ctx, req, []string{"INSTALL", "VERIFY"}, system, func(step serviceinstall.CommandStep) ([]byte, error) {
		lists := map[string][]string{}
		if step.ID == "OFFICIAL_PACKAGES_APPLY" || strings.Contains(step.Script, "${OPS_PACKAGE_ARGUMENTS}") {
			index := slices.Index(command.Args, "--")
			if index < 0 {
				return nil, errors.New("OFFICIAL_PLAN_REQUIRED")
			}
			lists["OPS_PACKAGE_ARGUMENTS"] = command.Args[index+1:]
			if err := atomicJSON(filepath.Join(dir, "official-apply.json"), marker); err != nil {
				return nil, err
			}
		}
		out, _, err := x.runBuiltinStep(ctx, req, step.ID, nil, lists, "")
		if err != nil && step.ID == "OFFICIAL_REFRESH" && (ctx.Err() != nil || temporaryInstallNetwork(out)) {
			return out, errors.New("OFFICIAL_REFRESH_INTERRUPTED")
		}
		return out, err
	})
	if err != nil {
		if err.Error() == "OFFICIAL_REFRESH_INTERRUPTED" {
			result.ErrorCode = err.Error()
			result.Phase = "INSTALL_TRANSFER_INTERRUPTED"
			return result, nil
		}
		if _, e := os.Lstat(filepath.Join(dir, "official-apply.json")); e == nil {
			return failedBuiltinFlowResult(x.reconcileOfficialSoftware(req, dir), err), nil
		}
	}
	return result, err
}
