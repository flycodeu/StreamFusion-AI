package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestTargetVerifyPreservesOwnerExecutorAndPending(t *testing.T) {
	root, owner := targetUpgradeFixture(t)
	pending := []byte("unconfirmed receipt remains untouched")
	if err := os.WriteFile(filepath.Join(root, "pending.json"), pending, 0600); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, name := range []string{"owner.json", "executor", "pending.json", "preserved-state"} {
		var err error
		before[name], err = os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyTarget(root, owner.ControllerID, owner.TargetID); err != nil {
		t.Fatal(err)
	}
	for name, expected := range before {
		actual, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatal("verification modified protected state", name, err)
		}
	}
}

func TestTargetVerifyRejectsWrongOwnerPermissionsAndBusy(t *testing.T) {
	root, owner := targetUpgradeFixture(t)
	if err := VerifyTarget(root, uuid.NewString(), owner.TargetID); err == nil {
		t.Fatal("different controller gained an authorization proof")
	}
	if err := VerifyTarget(root, owner.ControllerID, uuid.NewString()); err == nil {
		t.Fatal("different target gained an authorization proof")
	}
	ownerPath := filepath.Join(root, "owner.json")
	if err := os.Chmod(ownerPath, 0666); err != nil {
		t.Fatal(err)
	}
	if err := VerifyTarget(root, owner.ControllerID, owner.TargetID); err == nil {
		t.Fatal("unprotected owner accepted")
	}
	if err := os.Chmod(ownerPath, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := targetLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = VerifyTarget(root, owner.ControllerID, owner.TargetID); err == nil || err.Error() != "EXECUTOR_BUSY" {
		t.Fatal("verification bypassed an active mutation", err)
	}
}
