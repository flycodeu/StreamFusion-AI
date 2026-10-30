//go:build linux

package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

const targetRepairFileLimit int64 = 256 << 20

func openRepairFile(rootFD int, name string, limit int64) (*os.File, unix.Stat_t, error) {
	fd, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Size < 1 || stat.Size > limit {
		unix.Close(fd)
		return nil, unix.Stat_t{}, errors.New("TARGET_REPAIR_FILE_INVALID")
	}
	file := os.NewFile(uintptr(fd), filepath.Join("/proc/self/fd", name))
	if file == nil {
		unix.Close(fd)
		return nil, unix.Stat_t{}, errors.New("TARGET_REPAIR_FILE_INVALID")
	}
	return file, stat, nil
}

func sameRepairEntry(rootFD int, name string, expected unix.Stat_t) error {
	var current unix.Stat_t
	if err := unix.Fstatat(rootFD, name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || current.Mode&unix.S_IFMT != unix.S_IFREG || current.Dev != expected.Dev || current.Ino != expected.Ino {
		return errors.New("TARGET_REPAIR_ENTRY_CHANGED")
	}
	return nil
}

// RepairTargetPermissions is an explicit root recovery for an existing target.
// It never executes the existing binary, walks business data, follows links or
// changes pending/jobs/instances. The old executor must match the exact digest
// frozen in the interrupted preparation job before any permission is changed.
func RepairTargetPermissions(root, controllerID, targetID, expectedSHA string) error {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("LINUX_ROOT_REQUIRED")
	}
	if !ValidRoot(root) || !ID.MatchString(controllerID) || !ID.MatchString(targetID) || !Hex.MatchString(expectedSHA) {
		return errors.New("TARGET_REPAIR_INPUT_INVALID")
	}
	if err := secureAncestors(filepath.Dir(root)); err != nil {
		return errors.New("TARGET_REPAIR_PARENT_UNSAFE")
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("TARGET_REPAIR_ROOT_INVALID")
	}
	defer unix.Close(rootFD)
	var rootStat unix.Stat_t
	if err = unix.Fstat(rootFD, &rootStat); err != nil || rootStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("TARGET_REPAIR_ROOT_INVALID")
	}

	ownerFile, ownerStat, err := openRepairFile(rootFD, "owner.json", 128<<10)
	if err != nil {
		return errors.New("TARGET_REPAIR_OWNER_INVALID")
	}
	defer ownerFile.Close()
	ownerRaw, err := io.ReadAll(io.LimitReader(ownerFile, (128<<10)+1))
	if err != nil || len(ownerRaw) > 128<<10 {
		return errors.New("TARGET_REPAIR_OWNER_INVALID")
	}
	var owner Owner
	if StrictJSON(ownerRaw, &owner) != nil || owner.ProtocolVersion != 1 || owner.ExecutorVersion != Version || owner.ControllerID != controllerID || owner.TargetID != targetID {
		return errors.New("TARGET_REPAIR_OWNER_MISMATCH")
	}

	executorFile, executorStat, err := openRepairFile(rootFD, "executor", targetRepairFileLimit)
	if err != nil {
		return errors.New("TARGET_REPAIR_EXECUTOR_INVALID")
	}
	defer executorFile.Close()
	var header [20]byte
	if _, err = io.ReadFull(executorFile, header[:]); err != nil || string(header[:4]) != "\x7fELF" || header[4] != 2 || header[5] != 1 || header[18] != 62 || header[19] != 0 {
		return errors.New("TARGET_REPAIR_EXECUTOR_INVALID")
	}
	if _, err = executorFile.Seek(0, 0); err != nil {
		return errors.New("TARGET_REPAIR_EXECUTOR_INVALID")
	}
	hash := sha256.New()
	if n, copyErr := io.Copy(hash, io.LimitReader(executorFile, targetRepairFileLimit+1)); copyErr != nil || n != executorStat.Size || hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
		return errors.New("TARGET_REPAIR_EXECUTOR_MISMATCH")
	}

	lockFD, err := unix.Openat(rootFD, "executor.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return errors.New("TARGET_REPAIR_LOCK_INVALID")
	}
	defer unix.Close(lockFD)
	var lockStat unix.Stat_t
	if err = unix.Fstat(lockFD, &lockStat); err != nil || lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Nlink != 1 {
		return errors.New("TARGET_REPAIR_LOCK_INVALID")
	}
	if err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("EXECUTOR_BUSY")
	}
	defer unix.Flock(lockFD, unix.LOCK_UN)

	if sameRepairEntry(rootFD, "owner.json", ownerStat) != nil || sameRepairEntry(rootFD, "executor", executorStat) != nil || sameRepairEntry(rootFD, "executor.lock", lockStat) != nil {
		return errors.New("TARGET_REPAIR_ENTRY_CHANGED")
	}
	if err = unix.Fchown(rootFD, 0, 0); err == nil {
		err = unix.Fchmod(rootFD, 0755)
	}
	if err != nil {
		return errors.New("TARGET_REPAIR_ROOT_FAILED")
	}
	// Once the root is protected, ensure the verified entries are still the ones
	// now named by the directory before changing their metadata.
	if sameRepairEntry(rootFD, "owner.json", ownerStat) != nil || sameRepairEntry(rootFD, "executor", executorStat) != nil || sameRepairEntry(rootFD, "executor.lock", lockStat) != nil {
		return errors.New("TARGET_REPAIR_ENTRY_CHANGED")
	}
	for _, item := range []struct {
		fd   int
		mode uint32
	}{{int(ownerFile.Fd()), 0600}, {int(executorFile.Fd()), 0755}, {lockFD, 0600}} {
		if err = unix.Fchown(item.fd, 0, 0); err == nil {
			err = unix.Fchmod(item.fd, item.mode)
		}
		if err != nil {
			return errors.New("TARGET_REPAIR_PERMISSION_FAILED")
		}
		if err = unix.Fsync(item.fd); err != nil {
			return errors.New("TARGET_REPAIR_SYNC_FAILED")
		}
	}
	if err = unix.Fsync(rootFD); err != nil {
		return errors.New("TARGET_REPAIR_SYNC_FAILED")
	}
	if secureAncestors(filepath.Join(root, "owner.json")) != nil || secureAncestors(filepath.Join(root, "executor")) != nil {
		return errors.New("TARGET_REPAIR_NOT_PROTECTED")
	}
	return nil
}
