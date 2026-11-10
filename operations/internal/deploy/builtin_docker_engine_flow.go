package deploy

import (
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"os"
	"path/filepath"
	"strings"
)

func (x *executor) installDockerEngineFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	preflight := x.nativeInstallPreflight(*i)
	for _, c := range preflight.Checks {
		if c.Status != "PASS" {
			return result, errors.New(c.Code)
		}
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	var parts map[string]string
	var packages []dockerPackage
	var files []string
	var marker dockerPackageOwnership
	system := func(step serviceinstall.CommandStep) error {
		var err error
		switch step.Action {
		case "INSTALL_MATERIAL":
			parts, err = x.receiveNativeMaterial(req, dir)
			return err
		case "NATIVE_PREPARE":
			packages, err = dockerRecipePackages(i.Definition)
			if err != nil {
				return err
			}
			files = make([]string, 0, len(packages))
			for index, p := range packages {
				file := parts[i.Definition.Native.Parts[index].Name]
				if file == "" {
					return errors.New("DOCKER_PACKAGE_MISSING")
				}
				out, fieldErr := x.softwareRunner(ctx, "/usr/bin/dpkg-deb", "--show", "--showformat=${Package}\t${Version}\t${Architecture}\n", file)
				if fieldErr != nil || string(out) != p.Name+"\t"+p.Version+"\tamd64\n" {
					return errors.New("DOCKER_PACKAGE_IDENTITY_MISMATCH")
				}
				files = append(files, file)
			}
			out, err := x.loggedSourceCommand(ctx, req, "DOCKER_PACKAGE_PLAN", "docker-apt", dockerAptArgs(true, false, files)...)
			if err != nil {
				return errors.New("DOCKER_OFFLINE_DEPENDENCIES_MISSING")
			}
			if err = validateDockerAptPlan(out, packages, false); err != nil {
				return err
			}
			if err = x.dockerEnginePreflight(ctx); err != nil {
				return err
			}
			root := preflight.Root
			if err = ensureNativeParent(root); err != nil {
				return err
			}
			if err = os.Mkdir(root, 0750); err != nil {
				return err
			}
			for _, name := range []string{"data", "config", "logs", "bin", "runtime"} {
				if err = os.Mkdir(filepath.Join(root, name), 0750); err != nil {
					return err
				}
			}
			daemon := dockerDaemonConfig(root)
			marker = dockerPackageOwnership{SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), Root: root, DaemonHash: bytesSHA(daemon), Packages: packages}
			// Pin the exact program and unit bytes from the already validated official
			// DEB before package mutation. Reconciliation can then prove ownership even
			// if the process exits immediately after apt finishes.
			expectedDir := filepath.Join(dir, "docker-expected")
			if err = os.Mkdir(expectedDir, 0700); err != nil {
				return err
			}
			var enginePart string
			for name, file := range parts {
				if strings.HasPrefix(name, "docker-ce_") {
					enginePart = file
				}
			}
			if enginePart == "" {
				return errors.New("DOCKER_PACKAGE_MISSING")
			}
			if err = extractDebPayload(ctx, enginePart, expectedDir, map[string]string{"usr/bin/dockerd": "dockerd", "usr/lib/systemd/system/docker.service": "docker.service"}); err != nil {
				return err
			}
			marker.UnitPath = "/usr/lib/systemd/system/docker.service"
			marker.UnitHash, err = fileSHA(filepath.Join(expectedDir, "docker.service"))
			if err != nil {
				return err
			}
			marker.BinaryHash, err = fileSHA(filepath.Join(expectedDir, "dockerd"))
			if err != nil {
				return err
			}
			// The durable marker precedes config and package writes. Any subsequent
			// interruption is UNKNOWN and only observation may complete the job.
			if err = atomicJSON(filepath.Join(dir, "docker-packages.json"), marker); err != nil {
				return err
			}
			if err = os.MkdirAll("/etc/docker", 0755); err != nil || secureAncestors("/etc/docker") != nil {
				return errors.New("DOCKER_PACKAGE_RECONCILE_REQUIRED")
			}
			if err = serviceFile("/etc/docker/daemon.json", daemon, 0, false); err != nil {
				return errors.New("DOCKER_PACKAGE_RECONCILE_REQUIRED")
			}
			if err = serviceFile(filepath.Join(root, "config/daemon.json"), daemon, 0, false); err != nil {
				return errors.New("DOCKER_PACKAGE_RECONCILE_REQUIRED")
			}
			return nil
		case "NATIVE_CONFIGURE":
			return x.completeDockerOwnership(ctx, req, dir, &marker)
		case "INSTALL_CONFIRM":
			result = x.dockerEngineInstalledResult(ctx, req, dir, marker)
			if result.Status != "SUCCEEDED" {
				return errors.New("DOCKER_PACKAGE_RECONCILE_REQUIRED")
			}
			return nil
		}
		return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
	}
	err := x.runBuiltinFlow(ctx, req, []string{"INSTALL", "VERIFY"}, system, func(step serviceinstall.CommandStep) ([]byte, error) {
		if step.ID == "DOCKER_PACKAGES_APPLY" {
			if len(files) == 0 {
				return nil, errors.New("BUILTIN_COMMAND_CONTEXT_MISSING")
			}
			out, _, err := x.builtinCommand(ctx, req, step.ID, dockerAptArgs(false, false, files))
			return out, err
		}
		out, _, err := x.runBuiltinStep(ctx, req, step.ID, nil, map[string][]string{"OPS_PACKAGE_ARGUMENTS": files}, "")
		return out, err
	})
	if err != nil {
		if _, markerErr := os.Lstat(filepath.Join(dir, "docker-packages.json")); markerErr == nil {
			return failedBuiltinFlowResult(x.reconcileDockerEngine(req, dir), err), nil
		}
	}
	return result, err
}
