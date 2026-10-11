package controller

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func lockFile(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("STATE_LOCKED: 状态目录正被另一进程使用: %w", err)
	}
	return nil
}

func publishState(source, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
}
