//go:build linux

package deploy

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// A build runs in its own process group as the dedicated unprivileged account.
// Deadline cancellation terminates make/cmake and their compiler children.
func runNativeBuild(ctx context.Context, user, directory, command string, args ...string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(command)
	if err != nil {
		return nil, err
	}
	if err = secureAncestors(resolved); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "/usr/sbin/runuser", append([]string{"-u", user, "--", resolved}, args...)...)
	cmd.Dir = directory
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + directory, "LC_ALL=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	output := &cappedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	return output.Bytes(), err
}

func nativeFileOwner(info os.FileInfo) (int, int) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid), int(stat.Gid)
	}
	return 0, 0
}
