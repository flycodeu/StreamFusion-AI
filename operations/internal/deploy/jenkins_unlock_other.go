//go:build !linux

package deploy

import "os"

func jenkinsUnlockLock(string) (*os.File, error) {
	return nil, jenkinsUnlockBinding
}

func readJenkinsBootstrapFile(root, softwareID string) (string, error) {
	return "", jenkinsUnlockBinding
}
