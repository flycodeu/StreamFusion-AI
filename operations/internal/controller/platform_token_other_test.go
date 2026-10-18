//go:build !windows

package controller

import (
	"os"
	"path/filepath"
	"testing"
)

func protectPlatformTokenPath(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
}
