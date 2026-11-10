//go:build !linux

package deploy

import "errors"

func managedDataDirectoryIdentity(string) (string, error) { return "", errors.New("LINUX_ONLY") }
