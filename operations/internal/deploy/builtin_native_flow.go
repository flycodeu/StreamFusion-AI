package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func (x *executor) installNativeFlow(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	i := req.Installation
	p := x.nativeInstallPreflight(*i)
	for _, c := range p.Checks {
		if c.Status != "PASS" {
			return result, errors.New(c.Code)
		}
	}
	ctx, cancel := builtinFlowContext(req)
	defer cancel()
	root := p.Root
	var parts map[string]string
	vars := map[string]string{}
	cleanup := func() {}
	defer func() { cleanup() }()
	account := ""
	uid, gid := 0, 0
	owner := nativeInstallation{SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), BinaryPath: filepath.Join(root, nativeBinaryRelative(i.Definition))}
	var software SoftwareRequest
	system := func(step serviceinstall.CommandStep) error {
		var err error
		switch step.Action {
		case "INSTALL_MATERIAL":
			parts, err = x.receiveNativeMaterial(req, dir)
			return err
		case "NATIVE_PREPARE":
			if err = ensureNativeParent(root); err != nil {
				return err
			}
			if err = os.Mkdir(root, 0755); err != nil {
				return errors.New("SERVICE_DATA_DIRECTORY_EXISTS")
			}
			for _, name := range []string{"config", "data", "bin", "logs", "runtime"} {
				if err = os.Mkdir(filepath.Join(root, name), 0750); err != nil {
					return err
				}
			}
			if err = atomicJSON(filepath.Join(dir, "native-owned.json"), owner); err != nil {
				return err
			}
			if nativeNeedsAccount(i.Definition) {
				account = nativeUserName(i.SoftwareID)
				if _, err = x.softwareRunner(ctx, "/usr/bin/id", "-u", account); err == nil {
					return errors.New("NATIVE_ACCOUNT_ALREADY_EXISTS")
				}
				if _, err = x.sourceSoftwareCommand(ctx, "native-user-create", account); err != nil {
					return errors.New("NATIVE_ACCOUNT_CREATE_FAILED")
				}
				u, ue := x.softwareRunner(ctx, "/usr/bin/id", "-u", account)
				g, ge := x.softwareRunner(ctx, "/usr/bin/id", "-g", account)
				uid, _ = strconv.Atoi(strings.TrimSpace(string(u)))
				gid, _ = strconv.Atoi(strings.TrimSpace(string(g)))
				if ue != nil || ge != nil || uid < 1 || gid < 1 {
					return errors.New("NATIVE_ACCOUNT_ID_INVALID")
				}
			}
			vars, cleanup, err = x.prepareBuiltinNativeFiles(ctx, req, root, account, uid, gid, parts)
			return err
		case "NATIVE_CONFIGURE":
			if err = rootOwnNativeTree(root, gid, i.Definition.Kind == "TOOL"); err != nil {
				return err
			}
			if i.Definition.Category == "PROGRAMMING" {
				if err = os.Chown(filepath.Join(root, "data"), uid, gid); err != nil {
					return err
				}
				if err = os.Chmod(filepath.Join(root, "data"), 0700); err != nil {
					return err
				}
			}
			owner.BinaryHash, err = fileSHA(owner.BinaryPath)
			if err != nil {
				return err
			}
			if i.Definition.Kind != "TOOL" {
				if err = x.writeNativeConfiguration(req, root, gid); err != nil {
					return err
				}
				for _, name := range []string{"data", "logs"} {
					if err = chownNativeTree(filepath.Join(root, name), uid, gid); err != nil {
						return err
					}
				}
				if err = serviceFile(nativeUnitPath(i.SoftwareID), []byte(renderNativeUnit(req, root, account)), 0, false); err != nil {
					return err
				}
				owner.UnitHash, err = fileSHA(nativeUnitPath(i.SoftwareID))
				if err != nil {
					return err
				}
			}
			if err = atomicJSON(filepath.Join(dir, "native-owned.json"), owner); err != nil {
				return err
			}
			software = x.nativeSoftware(req, owner, "")
			return nil
		case "INSTALL_CONFIRM":
			if owner.BinaryHash == "" {
				return errors.New("SERVICE_INSTALL_RECEIPT_MISMATCH")
			}
			actual, e := ObserveSoftware(ctx, software, x.softwareRunner)
			if e != nil {
				return e
			}
			software.IdentityHash = actual.IdentityHash
			if err = atomicJSON(x.softwareFile(i.SoftwareID), softwareBinding{Software: software, JobID: req.JobID}); err != nil {
				return err
			}
			version := actual.Version
			if i.Definition.Kind == "TOOL" {
				if !installedToolVersionMatches(i.Definition, version) {
					return errors.New("TOOL_VERSION_CHECK_FAILED")
				}
			} else {
				version, err = x.waitServiceHealth(req, software, 2*time.Minute)
				if err != nil {
					return err
				}
			}
			result = x.ownedInstallResult(req, software, version, "READY", "SUCCEEDED")
			if result.Status != "SUCCEEDED" {
				return errors.New("NATIVE_RECONCILE_REQUIRED")
			}
			return nil
		}
		return errors.New("COMMAND_SYSTEM_ACTION_INVALID")
	}
	err := x.runBuiltinFlow(ctx, req, []string{"INSTALL", "VERIFY"}, system, func(step serviceinstall.CommandStep) ([]byte, error) {
		if step.ID == "NATIVE_START" {
			if err := atomicJSON(filepath.Join(dir, "native-start.json"), owner); err != nil {
				return nil, err
			}
		}
		out, _, err := x.runBuiltinStep(ctx, req, step.ID, vars, nil, account)
		return out, err
	})
	if err != nil {
		if _, markerErr := os.Lstat(filepath.Join(dir, "native-owned.json")); markerErr == nil {
			return failedBuiltinFlowResult(x.reconcileNative(req, dir), err), nil
		}
	}
	return result, err
}
