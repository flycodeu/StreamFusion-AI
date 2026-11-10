package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func migratedUninstallFixture(t *testing.T) (*executor, Request, string) {
	t.Helper()
	x, operation, dir := rebindRequestFixture(t)
	req := operation
	req.Management, req.Action, req.SoftwareOperation = nil, "SOFTWARE_UNINSTALL", "SOFTWARE_UNINSTALL"
	req.Uninstallation = &SoftwareUninstallRequest{Installation: *operation.Management.Installation, DataDirectory: "/srv/current-data", DataDirectoryIdentity: Digest("directory proof"), DeleteData: true}
	req.InputHash = CommandRequestDigest(req)
	writeManagementFixture(t, x.softwareFile(req.Software.ID), softwareBinding{Software: *req.Software, JobID: req.Software.Ownership.JobID, Resources: []serviceinstall.ResolvedResource{{Key: "active-data", Type: "PATH_WRITE", Value: "/srv/older-data", IdentityHash: Digest("old proof")}, {Key: "active-data", Type: "PATH_WRITE", Value: req.Uninstallation.DataDirectory, IdentityHash: req.Uninstallation.DataDirectoryIdentity}}})
	return x, req, dir
}

func TestMigratedDataUninstallRequiresLatestClaimAndFrozenExtraFields(t *testing.T) {
	x, req, _ := migratedUninstallFixture(t)
	if err := validateSoftwareUninstall(req); err != nil || x.validateManagedDataClaim(req) != nil {
		t.Fatal("current data claim rejected", err)
	}
	for _, values := range [][2]string{{"", ""}, {"/srv/older-data", Digest("old proof")}, {"/srv/current-data", Digest("changed")}, {"/opt/demo/" + req.Software.ID + "/data", Digest("directory proof")}} {
		changed := *req.Uninstallation
		changed.DataDirectory, changed.DataDirectoryIdentity = values[0], values[1]
		bad := req
		bad.Uninstallation = &changed
		if x.validateManagedDataClaim(bad) == nil {
			t.Fatal("omitted, historical or changed current data proof accepted", values[0])
		}
	}
	changed := *req.Uninstallation
	changed.DataDirectory = "/srv/another"
	bad := req
	bad.Uninstallation = &changed
	if CommandRequestDigest(bad) == CommandRequestDigest(req) {
		t.Fatal("data directory was not included in the signed command")
	}
}

func TestMigratedDataMountEvidenceAndReceiptUseOnlyCurrentPath(t *testing.T) {
	dir := "/srv/current-data"
	for _, point := range []string{dir, dir + "/nested"} {
		if dataMountsAbsent(dir, []byte("1 0 8:1 / "+point+" rw - ext4 /dev/sda rw\n")) == nil {
			t.Fatal("mounted data accepted", point)
		}
	}
	if err := dataMountsAbsent(dir, []byte("1 0 8:1 / / rw - ext4 /dev/sda rw\n")); err != nil {
		t.Fatal(err)
	}
	x, req, job := migratedUninstallFixture(t)
	e := SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: true, DataRemoved: true, DataRoot: req.Uninstallation.DataDirectory, ObservedAt: stamp()}
	writeManagementFixture(t, filepath.Join(job, "command-uninstall.json"), e)
	if err := os.Remove(x.softwareFile(req.Software.ID)); err != nil {
		t.Fatal(err)
	}
	result := softwareResult(req)
	result.Uninstallation = &e
	if err := x.verifyMigratedDataUninstall(req, result); err != nil {
		t.Fatal(err)
	}
	e.DataRoot = "/srv/older-data"
	if validMigratedUninstallEvidence(req, e) {
		t.Fatal("old retained directory reported as current deletion")
	}
}

// These payloads remove only directories freshly created by t.TempDir.
func TestManagedDataCleanupDeletesCurrentAndRetainsOldData(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux mount metadata and fixed cleanup commands")
	}
	base, sid := t.TempDir(), "11111111-1111-4111-8111-111111111111"
	old, current, source := filepath.Join(base, "old"), filepath.Join(base, "current"), filepath.Join(base, "source")
	for _, directory := range []string{old, current, source} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "keep.txt"), []byte(directory), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeManagementFixture(t, filepath.Join(current, ".streamfusion-ops-data.json"), map[string]string{"softwareId": sid})
	identity, err := managedDataDirectoryIdentity(current)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = resumeManagedDataTree(ctx, current, sid, identity); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(current); !os.IsNotExist(err) {
		t.Fatal("current data remains", err)
	}
	for _, directory := range []string{old, source} {
		data, err := os.ReadFile(filepath.Join(directory, "keep.txt"))
		if err != nil || string(data) != directory {
			t.Fatal("retained data changed", directory, err)
		}
	}
}

func TestManagedDataCleanupRejectsTimeoutSymlinkAndReplacedDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux directory identity and fixed cleanup commands")
	}
	base, sid := t.TempDir(), "11111111-1111-4111-8111-111111111111"
	directory := filepath.Join(base, "current")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writeManagementFixture(t, filepath.Join(directory, ".streamfusion-ops-data.json"), map[string]string{"softwareId": sid})
	identity, _ := managedDataDirectoryIdentity(directory)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if removeManagedDataTree(ctx, directory, sid) == nil {
		t.Fatal("cancelled cleanup continued")
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	if removeManagedDataTree(context.Background(), directory, sid) == nil {
		t.Fatal("linked data was cleaned")
	}
	if err := os.Rename(directory, filepath.Join(base, "old-current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writeManagementFixture(t, filepath.Join(directory, ".streamfusion-ops-data.json"), map[string]string{"softwareId": sid})
	if resumeManagedDataTree(context.Background(), directory, sid, identity) == nil {
		t.Fatal("replaced directory resumed old deletion")
	}
	if data, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(data) != "retained" {
		t.Fatal("linked external data changed", err)
	}
	// The final marker may be gone after a timeout; only the same empty inode
	// can finish, and rmdir still refuses a newly added file.
	if err := os.Remove(filepath.Join(directory, ".streamfusion-ops-data.json")); err != nil {
		t.Fatal(err)
	}
	identity, _ = managedDataDirectoryIdentity(directory)
	if err := resumeManagedDataTree(context.Background(), directory, sid, identity); err != nil {
		t.Fatal("same empty directory could not finish", err)
	}
}

func TestReboundContainerCleanupUsesOnlyStoppedOldFullID(t *testing.T) {
	for _, mode := range []string{"stopped", "running", "foreign", "same-id", "builtin-stopped", "builtin-running", "builtin-foreign", "builtin-same-id"} {
		t.Run(mode, func(t *testing.T) {
			fixture := rebindRequestFixture
			builtin := strings.HasPrefix(mode, "builtin-")
			if builtin {
				fixture = func(t *testing.T) (*executor, Request, string) { return builtinRebindFixture(t, "DOCKER") }
			}
			x, req, _ := fixture(t)
			mode = strings.TrimPrefix(mode, "builtin-")
			next := *req.Software
			next.Locator, next.Version = strings.Repeat("b", 64), 2
			if mode == "same-id" {
				next.Locator = req.Software.Locator
			}
			writeManagementFixture(t, x.softwareFile(next.ID), softwareBinding{Software: next, JobID: req.Software.Ownership.JobID})
			removed := ""
			x.softwareRun = func(_ time.Duration, _ string, args ...string) ([]byte, error) {
				if len(args) > 1 && args[1] == "rm" {
					removed = args[len(args)-1]
					return nil, nil
				}
				state, image := "exited", req.Software.Ownership.ImageID
				if mode == "running" {
					state = "running"
				}
				if mode == "foreign" {
					image = "sha256:" + strings.Repeat("d", 64)
				}
				labels := map[string]string{"streamfusion.ops.software-id": req.Software.ID}
				if builtin {
					labels = serviceLabels(req.Software.Ownership)
				}
				return json.Marshal(map[string]any{"id": req.Software.Locator, "image": image, "state": state, "labels": labels})
			}
			_ = x.cleanupReboundContainer(context.Background(), req, next)
			if mode == "stopped" && removed != req.Software.Locator || mode != "stopped" && removed != "" {
				t.Fatal("cleanup selected a live, new or foreign container", mode, removed)
			}
		})
	}
}
