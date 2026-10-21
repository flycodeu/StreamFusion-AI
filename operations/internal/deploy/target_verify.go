package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// VerifyTarget validates an existing ownership boundary before an administrator
// repairs its exact sudo rule. It never initializes a directory, replaces an
// executor or edits owner, pending, job, binding or business files. A pending
// receipt is allowed because restoring authorization may be needed to query it.
func VerifyTarget(root, controllerID, targetID string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("LINUX_ROOT_REQUIRED")
	}
	if !ValidRoot(root) || !ID.MatchString(controllerID) || !ID.MatchString(targetID) {
		return errors.New("TARGET_VERIFY_INPUT_INVALID")
	}
	for _, path := range []string{filepath.Join(root, "executor"), filepath.Join(root, "owner.json")} {
		if err := secureAncestors(path); err != nil {
			return err
		}
	}
	info, err := os.Lstat(filepath.Join(root, "executor"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("TARGET_VERIFY_EXECUTOR_INVALID")
	}
	lock, err := targetLock(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	var owner Owner
	if readJSON(filepath.Join(root, "owner.json"), &owner) != nil || owner.ProtocolVersion != 1 || owner.ExecutorVersion != Version || owner.ControllerID != controllerID || owner.TargetID != targetID {
		return errors.New("TARGET_VERIFY_OWNER_MISMATCH")
	}
	return nil
}
