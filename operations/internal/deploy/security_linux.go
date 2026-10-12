package deploy

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func secureAncestors(path string) error {
	for {
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		var stat unix.Stat_t
		if e = unix.Lstat(path, &stat); e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("EXECUTOR_PATH_NOT_PROTECTED")
		}
		if path == "/" {
			return nil
		}
		path = filepath.Dir(path)
	}
}
func targetLock(root string) (*os.File, error) {
	f, e := os.OpenFile(filepath.Join(root, "executor.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("EXECUTOR_BUSY")
	}
	return f, nil
}

func availableBytes(dir string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
