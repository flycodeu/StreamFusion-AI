//go:build linux

package deploy

import (
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// apt and dpkg spawn children holding package locks. Deadline cancellation
// terminates the entire operation group; its durable job remains uncertain.
func boundPackageCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
}
