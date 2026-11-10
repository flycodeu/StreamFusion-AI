package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestBuiltinUninstallRechecksDeletionPathsAfterCommands(t *testing.T) {
	f := newUninstallFixture(t)
	for _, definition := range serviceinstall.BuiltinCommandCatalog() {
		if definition.Key == f.req.Uninstallation.Installation.Definition.Key && definition.Version == f.req.Uninstallation.Installation.Definition.Version {
			f.req.Uninstallation.Installation.Definition = definition
			break
		}
	}
	if !builtinFlow(f.req) {
		t.Fatal("missing flow fixture")
	}
	f.req.Uninstallation.DeleteData = true
	dir := filepath.Join(f.x.root, "jobs", f.req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	file := filepath.Join(outside, "keep")
	if err := os.WriteFile(file, []byte("outside installation"), 0600); err != nil {
		t.Fatal(err)
	}
	base := f.x.softwareRun
	deletions := 0
	f.x.softwareRun = func(timeout time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/env" {
			return base(timeout, command, args...)
		}
		script := args[len(args)-1]
		if strings.Contains(script, "container rm") {
			f.removed = true
			return nil, os.Symlink(outside, filepath.Join(f.root, "data", "changed-after-check"))
		}
		if strings.Contains(script, "rm -rf") {
			deletions++
		}
		return nil, nil
	}
	result, err := f.x.uninstallBuiltinFlow(f.req, dir)
	if err != nil || result.Status != "UNKNOWN" || result.ErrorCode != "SOFTWARE_UNINSTALL_SYMLINK_FORBIDDEN" || deletions != 0 {
		t.Fatalf("result=%+v err=%v deletions=%d", result, err, deletions)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("outside file changed", err)
	}
	if _, err = os.Stat(filepath.Join(f.root, "data", "record")); err != nil {
		t.Fatal("data deleted after path changed", err)
	}
}
