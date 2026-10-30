package deploy

import (
	"errors"
	"io"
	"os"
	"strings"
)

func uninstallMountBoundary(root string) error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return errors.New("SOFTWARE_UNINSTALL_MOUNTS_UNKNOWN")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return errors.New("SOFTWARE_UNINSTALL_MOUNTS_UNKNOWN")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			return errors.New("SOFTWARE_UNINSTALL_MOUNTS_UNKNOWN")
		}
		point := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
		if point == root || strings.HasPrefix(point, root+"/") {
			return errors.New("SOFTWARE_UNINSTALL_MOUNT_FORBIDDEN")
		}
	}
	return nil
}
