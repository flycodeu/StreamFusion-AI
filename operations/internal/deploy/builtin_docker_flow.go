package deploy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func (x *executor) receiveDockerFlowMaterial(req Request) (string, error) {
	i := req.Installation
	if x.installationInput == nil {
		return "", errors.New("SERVICE_ARCHIVE_REQUIRED")
	}
	packages := filepath.Join(x.root, "packages")
	if err := os.MkdirAll(packages, 0700); err != nil {
		return "", err
	}
	if err := secureAncestors(packages); err != nil {
		return "", err
	}
	partial := filepath.Join(x.root, "jobs", req.JobID, "service-image.partial")
	file := filepath.Join(packages, "service-"+i.Source.SHA256+".tar")
	if _, err := os.Lstat(partial); req.ResumeOffset != i.Source.ByteSize || !os.IsNotExist(err) {
		if err = receiveInstallArchive(partial, x.installationInput, i.Source.ByteSize, req.ResumeOffset); err != nil {
			return "", errors.New("SERVICE_ARCHIVE_TRANSFER_FAILED")
		}
		if _, err = serviceinstall.ValidateArchive(partial, i.Source.SHA256, i.Source.ByteSize, i.Definition); err != nil {
			return "", err
		}
		if err = os.Rename(partial, file); err != nil {
			return "", err
		}
	} else if _, err = serviceinstall.ValidateArchive(file, i.Source.SHA256, i.Source.ByteSize, i.Definition); err != nil {
		return "", err
	}
	return file, nil
}

func (x *executor) installDockerFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	p := x.serviceInstallPreflight(*i)
	for _, c := range p.Checks {
		if c.Status != "PASS" {
			return result, errors.New(c.Code)
		}
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	vars := map[string]string{"OPS_CONTAINER_ID": serviceName(i.SoftwareID)}
	lists := map[string][]string{}
	var args []string
	creation := serviceCreation{SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config)}
	var software SoftwareRequest
	system := func(step serviceinstall.CommandStep) error {
		var err error
		switch step.Action {
		case "INSTALL_MATERIAL":
			vars["OPS_ARTIFACT_PATH"], err = x.receiveDockerFlowMaterial(req)
			return err
		case "DOCKER_PREPARE":
			out, e := x.softwareRunner(ctx, "/usr/bin/docker", "image", "inspect", "--format", `{"id":{{json .Id}},"os":{{json .Os}},"arch":{{json .Architecture}}}`, i.Definition.ImageID)
			var image struct{ ID, OS, Arch string }
			if e != nil || json.Unmarshal(out, &image) != nil || image.ID != i.Definition.ImageID || image.OS != "linux" || image.Arch != "amd64" {
				return errors.New("SERVICE_IMAGE_IDENTITY_MISMATCH")
			}
			if i.Definition.Kind != "TOOL" {
				args, err = x.prepareService(req, p.Root)
				if err != nil {
					return err
				}
				lists, err = builtinDockerLists(args, i.Definition.ImageID)
				if err != nil {
					return err
				}
				return atomicJSON(filepath.Join(dir, "service-create.json"), creation)
			}
			return err
		case "INSTALL_CONFIRM":
			if i.Definition.Kind == "TOOL" {
				var e error
				result, e = x.finishDockerToolFlow(req)
				return e
			}
			software, _, err = x.createdService(req, vars["OPS_CONTAINER_ID"])
			if err != nil {
				return err
			}
			if err = atomicJSON(x.softwareFile(i.SoftwareID), softwareBinding{Software: software, JobID: req.JobID}); err != nil {
				return err
			}
			version, e := x.waitServiceHealth(req, software, 2*time.Minute)
			if e != nil {
				return e
			}
			result = x.serviceInstallResult(req, software, version, "READY", "SUCCEEDED")
			if result.Status != "SUCCEEDED" {
				return errors.New("SERVICE_INSTALL_RECONCILE_REQUIRED")
			}
			return nil
		}
		return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
	}
	err := x.runBuiltinFlow(ctx, req, []string{"INSTALL", "VERIFY"}, system, func(step serviceinstall.CommandStep) ([]byte, error) {
		if step.ID == "SERVICE_CREATE" {
			if len(args) == 0 {
				return nil, errors.New("BUILTIN_COMMAND_CONTEXT_MISSING")
			}
			if err := atomicJSON(filepath.Join(dir, "service-create.json"), creation); err != nil {
				return nil, err
			}
			out, _, err := x.builtinCommand(ctx, req, step.ID, args)
			if err != nil {
				return out, err
			}
			software, _, err = x.createdService(req, serviceName(i.SoftwareID))
			if err != nil {
				return out, err
			}
			id := software.Locator
			creation.ContainerID = id
			vars["OPS_CONTAINER_ID"] = id
			if err = atomicJSON(filepath.Join(dir, "service-create.json"), creation); err != nil {
				return out, err
			}
			return out, atomicJSON(x.softwareFile(i.SoftwareID), softwareBinding{Software: software, JobID: req.JobID})
		}
		if step.ID == "SERVICE_INITIALIZING" {
			if err := atomicJSON(filepath.Join(dir, "service-start.json"), creation); err != nil {
				return nil, err
			}
		}
		out, _, err := x.runBuiltinStep(ctx, req, step.ID, vars, lists, "")
		if err != nil && step.ID == "SERVICE_IMAGE_PULL" && temporaryInstallNetwork(out) {
			return out, errors.New("SERVICE_IMAGE_PULL_INTERRUPTED")
		}
		return out, err
	})
	if err != nil && i.Definition.Kind != "TOOL" {
		if _, markerErr := os.Lstat(filepath.Join(dir, "service-create.json")); markerErr == nil {
			observed := x.reconcileServiceInstallation(req, dir)
			return failedBuiltinFlowResult(observed, err), nil
		}
	}
	return result, err
}

func (x *executor) finishDockerToolFlow(req Request) (Result, error) {
	result := softwareResult(req)
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	i := req.Installation
	s := SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: "TOOL", Locator: "image:" + i.Definition.ImageID, SoftwareKey: strings.ToUpper(i.Definition.Key), Ownership: x.installOwnership(req)}
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || !installedToolVersionMatches(i.Definition, actualVersion(actual)) {
		return result, errors.New("TOOL_VERSION_CHECK_FAILED")
	}
	s.IdentityHash = actual.IdentityHash
	if serviceinstall.UsesJavaBridge(i.Definition) {
		d, e := toolPluginDefinition(i.Definition)
		if e != nil {
			return result, e
		}
		if e = os.MkdirAll(filepath.Join(x.root, "plugins"), 0700); e != nil {
			return result, e
		}
		if e = atomicJSON(filepath.Join(x.root, "plugins", s.ID+".json"), pluginMaterial{InstallationID: s.ID, Definition: d, Managed: true, JobID: req.JobID, ImageID: i.Definition.ImageID}); e != nil {
			return result, e
		}
	}
	if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
		return result, err
	}
	return x.ownedInstallResult(req, s, actual.Version, "READY", "SUCCEEDED"), nil
}
