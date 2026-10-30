//go:build windows

package deploy

import "errors"

func RepairTargetPermissions(string, string, string, string) error {
	return errors.New("LINUX_ROOT_REQUIRED")
}
