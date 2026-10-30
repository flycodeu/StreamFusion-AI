//go:build linux

package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestTargetRepairPermissionsRequiresTrustedIdentityAndPreservesData(t *testing.T) {
	root, owner := targetUpgradeFixture(t)
	executor, err := os.ReadFile(filepath.Join(root, "executor"))
	if err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(filepath.Join(root, "preserved-state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "owner.json"), filepath.Join(root, "executor")} {
		if err = os.Chown(path, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Chmod(root, 0777); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(root, "owner.json"), 0666); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(root, "executor"), 0777); err != nil {
		t.Fatal(err)
	}
	if err = RepairTargetPermissions(root, owner.ControllerID, owner.TargetID, upgradeDigest(executor)); err != nil {
		t.Fatal(err)
	}
	if err = VerifyTarget(root, owner.ControllerID, owner.TargetID); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{root: 0755, filepath.Join(root, "owner.json"): 0600, filepath.Join(root, "executor"): 0755} {
		var stat unix.Stat_t
		info, statErr := os.Stat(path)
		if statErr != nil || unix.Stat(path, &stat) != nil || stat.Uid != 0 || info.Mode().Perm() != mode {
			t.Fatal("permission not repaired", path, statErr, stat.Uid, info.Mode().Perm())
		}
	}
	after, err := os.ReadFile(filepath.Join(root, "preserved-state"))
	if err != nil || !bytes.Equal(after, preserved) {
		t.Fatal("unrelated data changed", err)
	}
}

func TestTargetRepairPermissionsRejectsMismatchAndLinks(t *testing.T) {
	root, owner := targetUpgradeFixture(t)
	executor, err := os.ReadFile(filepath.Join(root, "executor"))
	if err != nil {
		t.Fatal(err)
	}
	if err = RepairTargetPermissions(root, uuid.NewString(), owner.TargetID, upgradeDigest(executor)); err == nil {
		t.Fatal("wrong controller accepted")
	}
	if err = RepairTargetPermissions(root, owner.ControllerID, owner.TargetID, upgradeDigest(upgradeELFFixture('Z'))); err == nil {
		t.Fatal("wrong trusted digest accepted")
	}
	ownerPath := filepath.Join(root, "owner.json")
	if err = os.Rename(ownerPath, ownerPath+".real"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(ownerPath+".real", ownerPath); err != nil {
		t.Fatal(err)
	}
	if err = RepairTargetPermissions(root, owner.ControllerID, owner.TargetID, upgradeDigest(executor)); err == nil {
		t.Fatal("owner symlink accepted")
	}
}
