package deploy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestCommandInstallRejectsFrozenCRLFBeforeAnyPreparation(t *testing.T) {
	req := commandTestRequest()
	req.Installation.Definition.Commands.Steps[0].Script = "set -euo pipefail\r\nprintf changed\r\n"
	original := Digest(req)
	x := executor{root: t.TempDir(), softwareRun: func(time.Duration, string, ...string) ([]byte, error) {
		t.Fatal("invalid script reached target preparation")
		return nil, nil
	}}
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if _, err := x.installCommands(req, dir); err == nil || err.Error() != "COMMAND_SCRIPT_LINE_ENDINGS_INVALID" {
		t.Fatalf("invalid script accepted: %v", err)
	}
	root := serviceinstall.InstallRoot(x.root, req.Installation.SoftwareID, req.Installation.Config)
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("invalid script created an install directory: %v", err)
	}
	if Digest(req) != original {
		t.Fatal("frozen request changed")
	}
}
