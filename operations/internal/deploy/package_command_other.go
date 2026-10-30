//go:build !linux

package deploy

import (
	"os/exec"
	"time"
)

func boundPackageCommand(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
