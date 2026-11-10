package deploy

import (
	"errors"
	"os"
	"strconv"
	"syscall"
)

func managedDataDirectoryIdentity(directory string) (string, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("MIGRATED_DATA_PATH_CHANGED")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("MIGRATED_DATA_PATH_CHANGED")
	}
	return strconv.FormatUint(uint64(identity.Dev), 10) + ":" + strconv.FormatUint(identity.Ino, 10), nil
}
