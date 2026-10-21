package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// UpgradeTarget is an explicit local root maintenance command. It changes only
// the protected executor binary, preserves one previous binary, and never adds
// sudo permissions or modifies software/job/data ownership.
func UpgradeTarget(root, sourceBinary, expectedSHA, controllerID, targetID string) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return "", errors.New("LINUX_ROOT_REQUIRED")
	}
	if !ValidRoot(root) || !Hex.MatchString(expectedSHA) || !ID.MatchString(controllerID) || !ID.MatchString(targetID) || !filepath.IsAbs(sourceBinary) || filepath.Clean(sourceBinary) != sourceBinary {
		return "", errors.New("TARGET_UPGRADE_INPUT_INVALID")
	}
	if err := secureAncestors(filepath.Join(root, "executor")); err != nil {
		return "", err
	}
	lock, err := targetLock(root)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	var owner Owner
	if readJSON(filepath.Join(root, "owner.json"), &owner) != nil || owner.ProtocolVersion != 1 || owner.ExecutorVersion != Version || owner.ControllerID != controllerID || owner.TargetID != targetID {
		return "", errors.New("TARGET_UPGRADE_OWNER_MISMATCH")
	}
	if _, err = os.Lstat(filepath.Join(root, "pending.json")); !os.IsNotExist(err) {
		return "", errors.New("TARGET_PENDING_RECONCILE_REQUIRED")
	}
	info, err := os.Lstat(sourceBinary)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 64 || info.Size() > 256<<20 {
		return "", errors.New("TARGET_UPGRADE_BINARY_INVALID")
	}
	file, err := os.Open(sourceBinary)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var header [20]byte
	if _, err = io.ReadFull(file, header[:]); err != nil || string(header[:4]) != "\x7fELF" || header[4] != 2 || header[5] != 1 || header[18] != 62 || header[19] != 0 {
		return "", errors.New("TARGET_UPGRADE_LINUX_AMD64_BINARY_REQUIRED")
	}
	if _, err = file.Seek(0, 0); err != nil {
		return "", err
	}
	candidate, err := os.CreateTemp(root, ".executor-upgrade-")
	if err != nil {
		return "", err
	}
	defer os.Remove(candidate.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(candidate, hash), io.LimitReader(file, (256<<20)+1))
	if err == nil && (n != info.Size() || n > 256<<20 || hex.EncodeToString(hash.Sum(nil)) != expectedSHA) {
		err = errors.New("TARGET_UPGRADE_HASH_MISMATCH")
	}
	if err == nil {
		err = candidate.Chmod(0755)
	}
	if err == nil {
		err = candidate.Sync()
	}
	if closeErr := candidate.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	backup := filepath.Join(root, "executor.previous")
	if _, err = os.Lstat(backup); err == nil {
		if err = secureAncestors(backup); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	previous, err := os.Open(filepath.Join(root, "executor"))
	if err != nil {
		return "", err
	}
	defer previous.Close()
	currentHash := sha256.New()
	currentSize, err := io.Copy(currentHash, io.LimitReader(previous, (256<<20)+1))
	if err != nil || currentSize > 256<<20 {
		return "", errors.New("TARGET_UPGRADE_CURRENT_BINARY_INVALID")
	}
	if hex.EncodeToString(currentHash.Sum(nil)) == expectedSHA {
		// A repeated maintenance command must preserve the actual rollback
		// point, rather than replacing it with another copy of the new binary.
		if _, err = os.Lstat(backup); err == nil {
			return backup, nil
		}
		return "", errors.New("TARGET_EXECUTOR_ALREADY_CURRENT")
	}
	if _, err = previous.Seek(0, 0); err != nil {
		return "", err
	}
	tmpBackup, err := os.CreateTemp(root, ".executor-previous-")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpBackup.Name())
	if _, err = io.Copy(tmpBackup, io.LimitReader(previous, (256<<20)+1)); err == nil {
		err = tmpBackup.Chmod(0755)
	}
	if err == nil {
		err = tmpBackup.Sync()
	}
	if closeErr := tmpBackup.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(tmpBackup.Name(), backup); err != nil {
		return "", err
	}
	directory, err := os.Open(root)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return "", err
	}
	if err = os.Rename(candidate.Name(), filepath.Join(root, "executor")); err != nil {
		return "", err
	}
	if err = directory.Sync(); err != nil {
		return "", errors.New("TARGET_UPGRADE_COMMIT_UNCONFIRMED")
	}
	return backup, nil
}
