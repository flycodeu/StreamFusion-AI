package deploy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// An installed service already has a target lock. A read must never create it.
func jenkinsUnlockLock(root string) (*os.File, error) {
	path := filepath.Join(root, "executor.lock")
	if secureAncestors(path) != nil {
		return nil, jenkinsUnlockBinding
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, jenkinsUnlockBinding
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != 0 || stat.Mode&0022 != 0 {
		unix.Close(fd)
		return nil, jenkinsUnlockBinding
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		unix.Close(fd)
		return nil, errors.New("EXECUTOR_BUSY")
	}
	return os.NewFile(uintptr(fd), path), nil
}

func readJenkinsBootstrapFile(root, softwareID string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return "", jenkinsUnlockBinding
	}
	base, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", jenkinsUnlockBinding
	}
	defer unix.Close(base)
	resolve := uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS)
	rootFD, err := unix.Openat2(base, strings.TrimPrefix(root, "/"), &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: resolve})
	if err != nil {
		return "", jenkinsUnlockBinding
	}
	defer unix.Close(rootFD)
	var rootStat unix.Stat_t
	if unix.Fstat(rootFD, &rootStat) != nil || rootStat.Uid != 0 || rootStat.Mode&0022 != 0 {
		return "", jenkinsUnlockBinding
	}
	resolve |= unix.RESOLVE_NO_XDEV
	marker, err := readJenkinsSmallFile(rootFD, "config/ops-owner", resolve, 0, 64)
	if err != nil || strings.TrimSuffix(string(marker), "\n") != "jenkins:"+softwareID {
		return "", jenkinsUnlockBinding
	}
	data, err := unix.Openat2(rootFD, "data", &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: resolve})
	if err != nil {
		return "", jenkinsUnlockBinding
	}
	defer unix.Close(data)
	var dataStat unix.Stat_t
	if unix.Fstat(data, &dataStat) != nil || dataStat.Uid == 0 || dataStat.Mode&0022 != 0 {
		return "", jenkinsUnlockBinding
	}
	body, err := readJenkinsSmallFile(data, "secrets/initialAdminPassword", resolve, dataStat.Uid, 33)
	if err != nil {
		return "", jenkinsUnlockMissing
	}
	defer clear(body)
	password := strings.TrimSuffix(string(body), "\n")
	if !jenkinsBootstrapPassword.MatchString(password) {
		return "", jenkinsUnlockMissing
	}
	return password, nil
}

func readJenkinsSmallFile(dir int, name string, resolve uint64, owner uint32, limit int64) ([]byte, error) {
	fd, err := unix.Openat2(dir, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC, Resolve: resolve})
	if err != nil {
		return nil, jenkinsUnlockMissing
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Uid != owner || before.Nlink != 1 ||
		before.Mode&0037 != 0 || before.Size < 1 || before.Size > limit {
		return nil, jenkinsUnlockMissing
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	var after unix.Stat_t
	if err != nil || int64(len(body)) != before.Size || unix.Fstat(fd, &after) != nil || after.Nlink != 1 ||
		after.Size != before.Size || after.Mtim != before.Mtim || after.Ctim != before.Ctim {
		clear(body)
		return nil, jenkinsUnlockMissing
	}
	return body, nil
}
