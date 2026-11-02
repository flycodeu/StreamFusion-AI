package deploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func targetUpgradeFixture(t *testing.T) (string, Owner) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("Linux root protected-directory upgrade fixture")
	}
	// Hosted runners may make /opt writable by the runner account. Exercise
	// the production boundary under protected ancestors, without changing /opt.
	root, err := os.MkdirTemp("/var/lib", "sfops-upgrade-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(root) != "/var/lib" || !strings.HasPrefix(filepath.Base(root), "sfops-upgrade-test-") {
			t.Fatal("unsafe test cleanup path")
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	owner := Owner{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString()}
	data, _ := json.Marshal(owner)
	if err = os.WriteFile(filepath.Join(root, "owner.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "executor"), upgradeELFFixture('A'), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "preserved-state"), []byte("business and jobs remain"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, owner
}

func upgradeELFFixture(marker byte) []byte {
	b := bytes.Repeat([]byte{marker}, 1024)
	copy(b, []byte("\x7fELF"))
	b[4], b[5], b[18], b[19] = 2, 1, 62, 0
	return b
}

func upgradeDigest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

func TestTargetUpgradeKeepsOnePreviousBinaryAndState(t *testing.T) {
	root, owner := targetUpgradeFixture(t)
	for _, marker := range []byte{'B', 'C'} {
		data := upgradeELFFixture(marker)
		source := filepath.Join(root, "new-binary")
		if err := os.WriteFile(source, data, 0600); err != nil {
			t.Fatal(err)
		}
		backup, err := UpgradeTarget(root, source, upgradeDigest(data), owner.ControllerID, owner.TargetID)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(filepath.Join(root, "executor"))
		previous, _ := os.ReadFile(backup)
		if !bytes.Equal(got, data) || !bytes.Equal(previous, upgradeELFFixture(marker-1)) {
			t.Fatal("replacement or previous binary lost")
		}
		if _, err = UpgradeTarget(root, source, upgradeDigest(data), owner.ControllerID, owner.TargetID); err != nil {
			t.Fatal(err)
		}
		repeatedBackup, _ := os.ReadFile(backup)
		if !bytes.Equal(repeatedBackup, previous) {
			t.Fatal("repeated upgrade overwrote previous successful binary")
		}
		state, _ := os.ReadFile(filepath.Join(root, "preserved-state"))
		if string(state) != "business and jobs remain" {
			t.Fatal("unrelated state changed")
		}
	}
}

func TestTargetUpgradeRejectsWrongOwnerHashPendingAndBusy(t *testing.T) {
	root, owner := targetUpgradeFixture(t)
	source := filepath.Join(root, "new-binary")
	data := upgradeELFFixture('B')
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpgradeTarget(root, source, strings.Repeat("a", 64), owner.ControllerID, owner.TargetID); err == nil {
		t.Fatal("wrong binary hash accepted")
	}
	if _, err := UpgradeTarget(root, source, upgradeDigest(data), uuid.NewString(), owner.TargetID); err == nil {
		t.Fatal("wrong controller accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "pending.json"), []byte("unknown operation"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpgradeTarget(root, source, upgradeDigest(data), owner.ControllerID, owner.TargetID); err == nil {
		t.Fatal("pending operation overwritten")
	}
	if err := os.Remove(filepath.Join(root, "pending.json")); err != nil {
		t.Fatal(err)
	}
	lock, err := targetLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = UpgradeTarget(root, source, upgradeDigest(data), owner.ControllerID, owner.TargetID); err == nil {
		t.Fatal("in-flight lock bypassed")
	}
	lock.Close()
	got, _ := os.ReadFile(filepath.Join(root, "executor"))
	if !bytes.Equal(got, upgradeELFFixture('A')) {
		t.Fatal("rejected upgrade mutated executor")
	}
}

func TestTargetUpgradeChecksFrozenCurrentHashUnderLock(t *testing.T) {
	root, owner := targetUpgradeFixture(t)
	source := filepath.Join(root, "new-binary")
	candidate := upgradeELFFixture('C')
	if err := os.WriteFile(source, candidate, 0600); err != nil {
		t.Fatal(err)
	}
	frozen := upgradeDigest(upgradeELFFixture('A'))
	// A separate maintenance operation wins the target lock after the
	// controller observed A. The later request must not overwrite its B.
	lock, err := targetLock(root)
	if err != nil {
		t.Fatal(err)
	}
	current := upgradeELFFixture('B')
	if err = os.WriteFile(filepath.Join(root, "executor"), current, 0755); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	backup := filepath.Join(root, "executor.previous")
	if err = os.WriteFile(backup, upgradeELFFixture('A'), 0755); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	lock.Close()
	if _, err = UpgradeTarget(root, source, upgradeDigest(candidate), owner.ControllerID, owner.TargetID, frozen); err == nil || err.Error() != "TARGET_UPGRADE_CURRENT_HASH_MISMATCH" {
		t.Fatal("stale observed hash was accepted", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "executor"))
	previous, _ := os.ReadFile(backup)
	if !bytes.Equal(got, current) || !bytes.Equal(previous, upgradeELFFixture('A')) {
		t.Fatal("hash rejection changed current binary or rollback point")
	}
	state, _ := os.ReadFile(filepath.Join(root, "preserved-state"))
	if string(state) != "business and jobs remain" {
		t.Fatal("hash rejection changed business state")
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, ".executor-*"))
	if len(leftovers) != 0 {
		t.Fatal("hash rejection left staged files", leftovers)
	}
	if _, err = UpgradeTarget(root, source, upgradeDigest(candidate), owner.ControllerID, owner.TargetID, upgradeDigest(current)); err != nil {
		t.Fatal("matching frozen hash rejected", err)
	}
	got, _ = os.ReadFile(filepath.Join(root, "executor"))
	previous, _ = os.ReadFile(backup)
	if !bytes.Equal(got, candidate) || !bytes.Equal(previous, current) {
		t.Fatal("guarded upgrade did not preserve actual previous binary")
	}
	// CLI passes an empty optional value for legacy invocations.
	if _, err = UpgradeTarget(root, source, upgradeDigest(candidate), owner.ControllerID, owner.TargetID, ""); err != nil {
		t.Fatal("legacy invocation changed", err)
	}
	if _, err = UpgradeTarget(root, source, upgradeDigest(candidate), owner.ControllerID, owner.TargetID, "not-a-sha"); err == nil || err.Error() != "TARGET_UPGRADE_INPUT_INVALID" {
		t.Fatal("malformed current hash accepted", err)
	}
}
