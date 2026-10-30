package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func uninstallRequestFixture(t *testing.T) (*executor, Request) {
	t.Helper()
	x, install := serviceRequestFixture(t)
	s := &SoftwareRequest{ID: install.Installation.SoftwareID, Version: 1, Kind: "DOCKER", Locator: strings.Repeat("b", 64), IdentityHash: Digest([]string{"DOCKER", strings.Repeat("b", 64), install.Installation.Definition.ImageID}), SoftwareKey: "NGINX", Ownership: x.installOwnership(install)}
	req := Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, JobID: uuid.NewString(), TraceID: uuid.NewString(), Action: "SOFTWARE_UNINSTALL", SoftwareOperation: "SOFTWARE_UNINSTALL", Software: s, Uninstallation: &SoftwareUninstallRequest{Installation: *install.Installation}, DeadlineAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
	req.InputHash = Digest(req)
	return x, req
}

func TestSoftwareUninstallRequiresInstallationOwnershipAndFreezesDeleteOption(t *testing.T) {
	_, req := uninstallRequestFixture(t)
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	req.Uninstallation.DeleteData = true
	if err := validateSoftwareInput(req); err == nil || err.Error() != "INPUT_HASH_MISMATCH" {
		t.Fatal(err)
	}
	req.Uninstallation.DeleteData = false
	req.Software.Ownership = nil
	if validateSoftwareInput(req) == nil {
		t.Fatal("external adopted software accepted for uninstall")
	}
	_, req = uninstallRequestFixture(t)
	req.Uninstallation.Installation.Config.StorageRoot = "/srv/foreign"
	req.InputHash = ""
	req.InputHash = Digest(req)
	if validateSoftwareInput(req) == nil {
		t.Fatal("installation path changed without bound config hash")
	}
}

func TestSoftwareUninstallEngineFailureOnlyFinalizesBeforeMutation(t *testing.T) {
	_, req := uninstallRequestFixture(t)
	known := &dockerRemovalNotStartedError{code: "DOCKER_ENGINE_HAS_DEPENDENT_CONTAINERS"}
	result, err := softwareUninstallEngineFailure(req, fmt.Errorf("%w", known))
	if err == nil || result.ErrorCode != "DOCKER_ENGINE_HAS_DEPENDENT_CONTAINERS" {
		t.Fatal(result, err)
	}
	result, err = softwareUninstallEngineFailure(req, errors.New("DOCKER_CONFIGURATION_CHANGED"))
	if err != nil || result.Status != "UNKNOWN" || result.ErrorCode != "DOCKER_CONFIGURATION_CHANGED" {
		t.Fatal(result, err)
	}
	result, err = softwareUninstallEngineFailure(req, errors.New("apt output token=secret-value"))
	if err != nil || result.ErrorCode != "SOFTWARE_UNINSTALL_UNCONFIRMED" {
		t.Fatal(result, err)
	}
}

type uninstallFixture struct {
	x                            *executor
	req                          Request
	root                         string
	removed, failObserve, forged bool
	commands                     int
}

func newUninstallFixture(t *testing.T) *uninstallFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux protected paths, directory fsync and os.Root deletion fixture")
	}
	x, req := uninstallRequestFixture(t)
	root, err := os.MkdirTemp("/opt", "sfops-uninstall-test-")
	if err != nil {
		t.Skip("isolated root-owned /opt fixture unavailable:", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	x.root = root
	f := &uninstallFixture{x: x, req: req, root: serviceinstall.InstallRoot(root, req.Software.ID, req.Uninstallation.Installation.Config)}
	for _, dir := range []string{filepath.Join(root, "jobs"), filepath.Dir(x.softwareFile(req.Software.ID)), filepath.Join(f.root, "data"), filepath.Join(f.root, "config"), filepath.Join(f.root, "logs")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"data/record", "config/settings", "logs/history"} {
		if err = os.WriteFile(filepath.Join(f.root, name), []byte("must preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = atomicJSON(x.softwareFile(req.Software.ID), softwareBinding{Software: *req.Software, JobID: req.Software.Ownership.JobID}); err != nil {
		t.Fatal(err)
	}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/docker" {
			return nil, errors.New("unexpected command")
		}
		if len(args) > 1 && args[0] == "container" && args[1] == "inspect" {
			labels := serviceLabels(req.Software.Ownership)
			if f.forged {
				labels[serviceLabel+"software"] = uuid.NewString()
			}
			return json.Marshal(map[string]any{"id": req.Software.Locator, "name": "/" + serviceName(req.Software.ID), "imageId": req.Uninstallation.Installation.Definition.ImageID, "image": "nginx", "labels": labels, "state": "running"})
		}
		if len(args) > 1 && args[0] == "container" && args[1] == "stop" {
			return nil, nil
		}
		if len(args) > 1 && args[0] == "container" && args[1] == "rm" {
			f.commands++
			f.removed = true
			return nil, nil
		}
		if len(args) > 1 && args[0] == "container" && args[1] == "ls" {
			if f.failObserve {
				return nil, errors.New("daemon unavailable")
			}
			if f.removed {
				return []byte(""), nil
			}
			return []byte(req.Software.Locator + "\n"), nil
		}
		return nil, errors.New("unexpected docker operation")
	}
	return f
}

func (f *uninstallFixture) run(t *testing.T, req Request) Result {
	t.Helper()
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := f.x.softwareLocked(req, &out); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSoftwareUninstallContainerPreservesDataAndHistoryAndDoesNotReplay(t *testing.T) {
	f := newUninstallFixture(t)
	result := f.run(t, f.req)
	if result.Status != "SUCCEEDED" || result.Uninstallation == nil || result.Uninstallation.DeleteData || result.Uninstallation.DataRemoved || !result.Uninstallation.ProgramRemoved {
		t.Fatalf("%+v", result)
	}
	for _, name := range []string{"data/record", "config/settings", "logs/history"} {
		data, err := os.ReadFile(filepath.Join(f.root, name))
		if err != nil || string(data) != "must preserve" {
			t.Fatal(name, err)
		}
	}
	if f.run(t, f.req).Status != "SUCCEEDED" || f.commands != 1 {
		t.Fatal("duplicate removal executed")
	}
	for _, name := range []string{"intent.json", "result.json", "software-uninstall.json", "software-uninstalled.json"} {
		if _, err := os.Stat(filepath.Join(f.x.root, "jobs", f.req.JobID, name)); err != nil {
			t.Fatal("history missing", name)
		}
	}
	ack := f.req
	ack.Action = "ACK"
	ack.ResultHash = Digest(result)
	if !f.run(t, ack).Ack {
		t.Fatal("uninstall could not acknowledge")
	}
}

func TestSoftwareUninstallExplicitDeleteIsBoundToInstance(t *testing.T) {
	f := newUninstallFixture(t)
	f.req.Uninstallation.DeleteData = true
	f.req.InputHash = ""
	f.req.InputHash = Digest(f.req)
	other := filepath.Join(filepath.Dir(f.root), uuid.NewString())
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "keep"), []byte("sibling"), 0600); err != nil {
		t.Fatal(err)
	}
	// Both vendor-relative and absolute links remain inside this instance.
	// os.Root.RemoveAll must unlink them without visiting their targets twice.
	if err := os.Symlink("../config", filepath.Join(f.root, "data", "internal-relative")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.root, "logs"), filepath.Join(f.root, "data", "internal-absolute")); err != nil {
		t.Fatal(err)
	}
	result := f.run(t, f.req)
	if result.Status != "SUCCEEDED" || !result.Uninstallation.DataRemoved {
		t.Fatalf("%+v", result)
	}
	if _, err := os.Lstat(f.root); !os.IsNotExist(err) {
		t.Fatal("instance data retained", err)
	}
	if _, err := os.Stat(filepath.Join(other, "keep")); err != nil {
		t.Fatal("sibling modified", err)
	}
}

func TestSoftwareUninstallRejectsSymlinksAndChangedIdentityBeforeCommands(t *testing.T) {
	for _, mode := range []string{"identity", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := newUninstallFixture(t)
			if mode == "identity" {
				f.forged = true
			} else {
				f.req.Uninstallation.DeleteData = true
				f.req.InputHash = ""
				f.req.InputHash = Digest(f.req)
				if err := os.Symlink("/etc", filepath.Join(f.root, "data", "external")); err != nil {
					t.Fatal(err)
				}
			}
			result := f.run(t, f.req)
			if result.Status != "FAILED" || f.commands != 0 {
				t.Fatalf("unsafe uninstall ran: %+v", result)
			}
			if _, err := os.Stat(filepath.Join(f.root, "data/record")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSoftwareUninstallUnknownRequiresPositiveAbsenceAndRecoversWithoutReplay(t *testing.T) {
	f := newUninstallFixture(t)
	f.failObserve = true
	result := f.run(t, f.req)
	if result.Status != "UNKNOWN" || f.commands != 1 {
		t.Fatalf("%+v", result)
	}
	status := f.req
	status.Action = "STATUS"
	if result = f.run(t, status); result.Status != "UNKNOWN" {
		t.Fatal("daemon error treated as absence", result)
	}
	f.failObserve = false
	if result = f.run(t, status); result.Status != "SUCCEEDED" || f.commands != 1 {
		t.Fatal("removal replayed or not reconciled", result)
	}
	if err := f.x.verifySoftwareUninstall(f.req, result); err != nil {
		t.Fatal(err)
	}
	result.Uninstallation.IdentityHash = strings.Repeat("c", 64)
	if f.x.verifySoftwareUninstall(f.req, result) == nil {
		t.Fatal("forged removal receipt accepted")
	}
}

func TestSoftwareUninstallNativeProgramRemovalLeavesConfigDataAndLogs(t *testing.T) {
	f := newUninstallFixture(t)
	for _, name := range []string{"bin", "runtime"} {
		if err := os.Mkdir(filepath.Join(f.root, name), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.root, name, "program"), []byte("program"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeOwnedUninstallPaths(f.root, []string{"bin", "runtime"}, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config/settings", "data/record", "logs/history"} {
		if _, err := os.Stat(filepath.Join(f.root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bin", "runtime"} {
		if _, err := os.Stat(filepath.Join(f.root, name)); !os.IsNotExist(err) {
			t.Fatal("program retained")
		}
	}
	if _, err := ObserveSoftware(context.Background(), *f.req.Software, f.x.softwareRunner); err != nil {
		t.Fatal(err)
	}
}

func TestSoftwareUninstallToolPreservesSharedImageAndRemovesUnsharedImage(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			f := newUninstallFixture(t)
			d, err := serviceinstall.Find("python", "3.12.15")
			if err != nil {
				t.Fatal(err)
			}
			i := &f.req.Uninstallation.Installation
			i.Definition = d
			i.Config.HostPort = 0
			s := f.req.Software
			s.Kind, s.SoftwareKey, s.Locator = "TOOL", "PYTHON", "image:"+d.ImageID
			s.Ownership.ConfigHash = Digest(i.Config)
			s.Ownership.ImageID = d.ImageID
			s.Ownership.RuntimeMode = "DOCKER"
			s.IdentityHash = Digest([]string{"TOOL", s.Locator, d.ImageID})
			f.req.InputHash = ""
			f.req.InputHash = Digest(f.req)
			if err = atomicJSON(f.x.softwareFile(s.ID), softwareBinding{Software: *s, JobID: s.Ownership.JobID}); err != nil {
				t.Fatal(err)
			}
			otherID := uuid.NewString()
			if shared {
				other := *s
				other.ID = otherID
				owner := *s.Ownership
				owner.SoftwareID = otherID
				other.Ownership = &owner
				if err = atomicJSON(f.x.softwareFile(otherID), softwareBinding{Software: other, JobID: owner.JobID}); err != nil {
					t.Fatal(err)
				}
			}
			removedImage := false
			f.x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
				if command != "/usr/bin/docker" {
					return nil, errors.New("unexpected command")
				}
				joined := strings.Join(args, " ")
				if strings.HasPrefix(joined, "image inspect") {
					return json.Marshal(map[string]string{"id": d.ImageID, "os": "linux", "arch": "amd64"})
				}
				if args[0] == "run" {
					return []byte("Python 3.12.15"), nil
				}
				if strings.HasPrefix(joined, "container ls") {
					return []byte(""), nil
				}
				if strings.HasPrefix(joined, "image rm") {
					if joined != "image rm -- "+d.Image+"@"+d.Digest {
						t.Fatal("image removal not pinned", joined)
					}
					removedImage = true
					return nil, nil
				}
				if strings.HasPrefix(joined, "image ls") {
					if removedImage {
						return []byte(""), nil
					}
					return []byte(d.ImageID + "\n"), nil
				}
				return nil, errors.New("unexpected operation")
			}
			result := f.run(t, f.req)
			if result.Status != "SUCCEEDED" || result.Uninstallation == nil || result.Uninstallation.SharedProgramRetained != shared || result.Uninstallation.ProgramRemoved == shared || removedImage == shared {
				t.Fatalf("%+v removed=%v shared=%v", result, removedImage, shared)
			}
			if shared {
				if _, err = os.Stat(f.x.softwareFile(otherID)); err != nil {
					t.Fatal("shared installation binding removed", err)
				}
			}
		})
	}
}
