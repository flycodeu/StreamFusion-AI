//go:build linux

package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestTargetInitPreservesControlModesUnderPrivateInstallerUmask(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root and protected ancestors")
	}
	parent, err := os.MkdirTemp("/var/lib", "sfops-init-mode-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(parent) != "/var/lib" || !strings.HasPrefix(filepath.Base(parent), "sfops-init-mode-test-") {
			t.Fatal("unsafe cleanup path")
		}
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	previous := unix.Umask(0077)
	defer unix.Umask(previous)
	root := filepath.Join(parent, "target")
	controller, target := uuid.NewString(), uuid.NewString()
	if err = InitTarget(root, controller, target, "/var/run/docker.sock", "/usr/bin/docker"); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]os.FileMode{root: 0755, filepath.Join(root, "executor"): 0755, filepath.Join(root, "owner.json"): 0600, filepath.Join(root, "jobs"): 0700, filepath.Join(root, "instances"): 0700, filepath.Join(root, "packages"): 0700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != expected {
			t.Errorf("%s mode=%o expected=%o", path, info.Mode().Perm(), expected)
		}
	}
	if err = VerifyTarget(root, controller, target); err != nil {
		t.Fatal(err)
	}
}
