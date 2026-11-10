package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type migratedDataUninstallMarker struct {
	softwareUninstallMarker
	DirectoryIdentity string `json:"directoryIdentity"`
}

func lastManagedDataResource(resources []serviceinstall.ResolvedResource) (serviceinstall.ResolvedResource, bool) {
	for n := len(resources) - 1; n >= 0; n-- {
		r := resources[n]
		if r.Key == "active-data" && r.Type == "PATH_WRITE" && !r.Release {
			return r, true
		}
	}
	return serviceinstall.ResolvedResource{}, false
}

func (x *executor) commandProgramAbsent(req Request) bool {
	if req.Software.Kind == "TOOL" {
		_, err := os.Lstat(req.Software.Locator)
		return os.IsNotExist(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if req.Software.Kind == "DOCKER" {
		data, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "ls", "-a", "--no-trunc", "--filter", "id="+req.Software.Locator, "--format", "{{.ID}}")
		return err == nil && strings.TrimSpace(string(data)) == ""
	}
	data, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "show", "--property=LoadState", "--value", "--", req.Software.Locator)
	return err == nil && strings.TrimSpace(string(data)) == "not-found"
}

func (x *executor) validateManagedDataClaim(req Request) error {
	var binding softwareBinding
	if readJSON(x.softwareFile(req.Software.ID), &binding) != nil || !reflect.DeepEqual(binding.Software, *req.Software) {
		return errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	active, exists := lastManagedDataResource(binding.Resources)
	u := req.Uninstallation
	if !exists && u.DataDirectory == "" && u.DataDirectoryIdentity == "" {
		return nil
	}
	root := serviceinstall.InstallRoot(x.root, u.Installation.SoftwareID, u.Installation.Config)
	if !exists || u.DataDirectory == "" || active.Value != u.DataDirectory || active.IdentityHash != u.DataDirectoryIdentity || !Hex.MatchString(active.IdentityHash) || u.DataDirectory == path.Join(root, "data") || strings.HasPrefix(u.DataDirectory, root+"/") || strings.HasPrefix(root, u.DataDirectory+"/") {
		return errors.New("MIGRATED_DATA_CONFIRMATION_REQUIRED")
	}
	return nil
}

func readManagedDataMarker(directory, softwareID string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(".streamfusion-ops-data.json")
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4096 {
		return errors.New("MIGRATED_DATA_MARKER_INVALID")
	}
	file, err := root.Open(".streamfusion-ops-data.json")
	if err != nil {
		return err
	}
	defer file.Close()
	var marker struct {
		SoftwareID string `json:"softwareId"`
	}
	if json.NewDecoder(io.LimitReader(file, 4097)).Decode(&marker) != nil || marker.SoftwareID != softwareID {
		return errors.New("MIGRATED_DATA_MARKER_INVALID")
	}
	return nil
}

func dataMountsAbsent(directory string, data []byte) error {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			return errors.New("MIGRATED_DATA_MOUNTS_UNCONFIRMED")
		}
		point := decode.Replace(fields[4])
		if point == directory || strings.HasPrefix(point, directory+"/") {
			return errors.New("MIGRATED_DATA_MOUNT_PRESENT")
		}
	}
	if scanner.Err() != nil {
		return errors.New("MIGRATED_DATA_MOUNTS_UNCONFIRMED")
	}
	return nil
}

func checkManagedDataMounts(directory string) error {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return errors.New("MIGRATED_DATA_MOUNTS_UNCONFIRMED")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 4<<20 {
		return errors.New("MIGRATED_DATA_MOUNTS_UNCONFIRMED")
	}
	return dataMountsAbsent(directory, data)
}

func validateManagedDataDirectory(directory, softwareID string) error {
	if err := validateManagedDataParent(directory); err != nil {
		return err
	}
	marker, err := os.Lstat(filepath.Join(directory, ".streamfusion-ops-data.json"))
	if err != nil {
		return errors.New("MIGRATED_DATA_MARKER_INVALID")
	}
	uid, _ := nativeFileOwner(marker)
	if uid != 0 || marker.Mode().Perm()&0022 != 0 {
		return errors.New("MIGRATED_DATA_MARKER_INVALID")
	}
	if err = readManagedDataMarker(directory, softwareID); err != nil {
		return err
	}
	return nil
}

func validateManagedDataParent(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || secureAncestors(filepath.Dir(directory)) != nil {
		return errors.New("MIGRATED_DATA_PATH_UNPROTECTED")
	}
	return checkManagedDataMounts(directory)
}

// Validate through an open directory, then run the fixed non-following cleanup
// in a cancellable process group. Uncertain cleanup never becomes a final job.
func removeManagedDataTree(ctx context.Context, directory, softwareID string) error {
	if err := readManagedDataMarker(directory, softwareID); err != nil {
		return err
	}
	if err := checkManagedDataMounts(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Lstat(".")
	if err != nil {
		return err
	}
	count := 0
	var size int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("MIGRATED_DATA_SPECIAL_FILE")
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		count++
		if count > 100000 || size > 128<<30 {
			return errors.New("MIGRATED_DATA_DELETE_LIMIT")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = checkManagedDataMounts(directory); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(opened, current) {
		return errors.New("MIGRATED_DATA_PATH_CHANGED")
	}
	return runManagedDataCleanup(ctx, directory, false)
}

func runManagedDataCleanup(ctx context.Context, directory string, emptyOnly bool) error {
	for _, binary := range []string{"/bin/bash", "/usr/bin/find", "/usr/bin/rm", "/usr/bin/rmdir"} {
		resolved, err := filepath.EvalSymlinks(binary)
		if err != nil || secureAncestors(resolved) != nil {
			return errors.New("MIGRATED_DATA_DELETE_TOOL_UNAVAILABLE")
		}
	}
	mode := "tree"
	if emptyOnly {
		mode = "empty"
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", deployScript("software/delete-managed-data.sh"), "ops-delete-managed-data", directory, mode)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	boundPackageCommand(cmd)
	output := &cappedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return errors.New("MIGRATED_DATA_REMOVAL_UNCONFIRMED")
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		return errors.New("MIGRATED_DATA_REMOVAL_UNCONFIRMED")
	}
	return nil
}

func resumeManagedDataTree(ctx context.Context, directory, softwareID, identity string) error {
	current, err := managedDataDirectoryIdentity(directory)
	if err != nil || current != identity {
		return errors.New("MIGRATED_DATA_PATH_CHANGED")
	}
	if _, err = os.Lstat(filepath.Join(directory, ".streamfusion-ops-data.json")); err == nil {
		return removeManagedDataTree(ctx, directory, softwareID)
	} else if !os.IsNotExist(err) {
		return err
	}
	// Only the exact original empty directory may finish after its marker was
	// removed. A later replacement, added file or nested mount remains protected.
	root, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err = root.Readdirnames(1); err != io.EOF {
		return errors.New("MIGRATED_DATA_MARKER_INVALID")
	}
	if err = checkManagedDataMounts(directory); err != nil {
		return err
	}
	return runManagedDataCleanup(ctx, directory, true)
}

func (x *executor) uninstallMigratedCommandData(req Request, dir, installRoot string) (Result, error) {
	r := softwareResult(req)
	u := req.Uninstallation
	if err := x.validateManagedDataClaim(req); err != nil {
		return r, err
	}
	if err := validateManagedDataDirectory(u.DataDirectory, req.Software.ID); err != nil {
		return r, err
	}
	identity, err := managedDataDirectoryIdentity(u.DataDirectory)
	if err != nil {
		return r, err
	}
	marker := migratedDataUninstallMarker{softwareUninstallMarker: softwareUninstallMarker{JobID: req.JobID, InputHash: req.InputHash, Root: u.DataDirectory, Software: *req.Software}, DirectoryIdentity: identity}
	if err := atomicJSON(filepath.Join(dir, "migrated-uninstall.json"), marker); err != nil {
		return r, err
	}
	if err := x.runCommandSteps(req, dir, installRoot, "UNINSTALL"); err != nil {
		r.ErrorCode = "MIGRATED_DATA_UNINSTALL_UNCONFIRMED"
		return r, nil
	}
	if !x.commandProgramAbsent(req) {
		r.ErrorCode = "COMMAND_UNINSTALL_UNCONFIRMED"
		return r, nil
	}
	if u.DeleteData {
		limit := 90 * time.Second
		if deadline, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil {
			limit = min(limit, time.Until(deadline))
		}
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		if err := x.validateManagedDataClaim(req); err == nil {
			err = validateManagedDataDirectory(u.DataDirectory, req.Software.ID)
			if err == nil {
				x.appendServiceLog(req, "DELETE_CURRENT_DATA", "删除当前数据目录："+u.DataDirectory)
				err = resumeManagedDataTree(ctx, u.DataDirectory, req.Software.ID, marker.DirectoryIdentity)
			}
			if err != nil {
				x.appendServiceLog(req, "DELETE_CURRENT_DATA", "当前数据清理未确认，请核对目录："+u.DataDirectory+"；任务日志："+filepath.Join(dir, "service-install.log"))
				r.ErrorCode = "MIGRATED_DATA_REMOVAL_UNCONFIRMED"
				return r, nil
			}
		} else {
			r.ErrorCode = "MIGRATED_DATA_CONFIRMATION_REQUIRED"
			return r, nil
		}
	}
	return x.finishMigratedCommandUninstall(req, dir), nil
}

func validMigratedUninstallEvidence(req Request, e SoftwareUninstallEvidence) bool {
	return e.SoftwareID == req.Software.ID && e.Kind == req.Software.Kind && e.Locator == req.Software.Locator && e.IdentityHash == req.Software.IdentityHash && e.ProgramRemoved && !e.SharedProgramRetained && e.DeleteData == req.Uninstallation.DeleteData && e.DataRemoved == req.Uninstallation.DeleteData && e.DataRoot == req.Uninstallation.DataDirectory && len(e.RemovedPaths) == 0
}

func (x *executor) finishMigratedCommandUninstall(req Request, dir string) Result {
	r := softwareResult(req)
	r.ErrorCode = "MIGRATED_DATA_UNINSTALL_UNCONFIRMED"
	var saved SoftwareUninstallEvidence
	if readJSON(filepath.Join(dir, "command-uninstall.json"), &saved) != nil {
		var marker migratedDataUninstallMarker
		if readJSON(filepath.Join(dir, "migrated-uninstall.json"), &marker) != nil || marker.DirectoryIdentity == "" || marker.JobID != req.JobID || marker.InputHash != req.InputHash || marker.Root != req.Uninstallation.DataDirectory || !reflect.DeepEqual(marker.Software, *req.Software) || x.validateManagedDataClaim(req) != nil || !x.commandProgramAbsent(req) {
			return r
		}
		if req.Uninstallation.DeleteData {
			if _, err := os.Lstat(marker.Root); err == nil {
				if validateManagedDataParent(marker.Root) != nil {
					return r
				}
				if _, markerErr := os.Lstat(filepath.Join(marker.Root, ".streamfusion-ops-data.json")); markerErr == nil && validateManagedDataDirectory(marker.Root, req.Software.ID) != nil {
					return r
				} else if markerErr != nil && !os.IsNotExist(markerErr) {
					return r
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				err = resumeManagedDataTree(ctx, marker.Root, req.Software.ID, marker.DirectoryIdentity)
				cancel()
				if err != nil {
					x.appendServiceLog(req, "DELETE_CURRENT_DATA", "当前目录仍待核对，未重放卸载步骤；请查看本任务日志并检查："+marker.Root)
					return r
				}
			} else if !os.IsNotExist(err) {
				return r
			}
		} else if validateManagedDataDirectory(marker.Root, req.Software.ID) != nil {
			return r
		}
		saved = SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: req.Uninstallation.DeleteData, DataRemoved: req.Uninstallation.DeleteData, DataRoot: marker.Root, ObservedAt: stamp()}
		if atomicJSON(filepath.Join(dir, "command-uninstall.json"), saved) != nil {
			return r
		}
	}
	if !validMigratedUninstallEvidence(req, saved) {
		return r
	}
	if _, err := os.Lstat(x.softwareFile(req.Software.ID)); err == nil {
		if x.boundSoftware(*req.Software) != nil || os.Remove(x.softwareFile(req.Software.ID)) != nil {
			return r
		}
	} else if !os.IsNotExist(err) {
		return r
	}
	r.Status, r.Phase, r.ErrorCode, r.Uninstallation = "SUCCEEDED", "SOFTWARE_UNINSTALLED", "", &saved
	return r
}

func (x *executor) verifyMigratedDataUninstall(req Request, result Result) error {
	var saved SoftwareUninstallEvidence
	if result.Uninstallation == nil || !validMigratedUninstallEvidence(req, *result.Uninstallation) || readJSON(filepath.Join(x.root, "jobs", req.JobID, "command-uninstall.json"), &saved) != nil || !reflect.DeepEqual(saved, *result.Uninstallation) {
		return errors.New("SOFTWARE_UNINSTALL_RECEIPT_MISMATCH")
	}
	if _, err := time.Parse(time.RFC3339Nano, saved.ObservedAt); err != nil {
		return errors.New("SOFTWARE_UNINSTALL_RECEIPT_MISMATCH")
	}
	if _, err := os.Lstat(x.softwareFile(req.Software.ID)); !os.IsNotExist(err) {
		return errors.New("SOFTWARE_BINDING_STILL_PRESENT")
	}
	return nil
}
