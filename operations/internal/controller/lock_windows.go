package controller

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

// Windows 仅支持开发运行；安装和交付验收面向 Linux。
func lockFile(f *os.File) error {
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("STATE_LOCKED: 状态目录正被另一进程使用: %w", err)
	}
	return nil
}

func publishState(source, destination string) error { return os.Rename(source, destination) }
