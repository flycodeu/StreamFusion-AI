package deploy

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUninstallRootAcceptsShortDirectoryAndLegacyUUID(t *testing.T) {
	x, req := uninstallRequestFixture(t)
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	wantLegacy := req.Uninstallation.Installation.InstallRoot(x.root)
	if filepath.Base(filepath.FromSlash(wantLegacy)) != req.Software.ID {
		t.Fatalf("legacy path base=%q want %q", wantLegacy, req.Software.ID)
	}

	// New short directory record: DirectoryName lives on Request & Ownership without changing ConfigHash.
	x2, install := serviceRequestFixture(t)
	install.Installation.DirectoryName = "nginx-80"
	install.InputHash = ""
	install.InputHash = Digest(install)
	s := &SoftwareRequest{
		ID:           install.Installation.SoftwareID,
		Version:      1,
		Kind:         "DOCKER",
		Locator:      strings.Repeat("b", 64),
		IdentityHash: Digest([]string{"DOCKER", strings.Repeat("b", 64), install.Installation.Definition.ImageID}),
		SoftwareKey:  "NGINX",
		Ownership:    x2.installOwnership(install),
	}
	shortReq := Request{
		ProtocolVersion:   1,
		ExecutorVersion:   Version,
		ControllerID:      x2.owner.ControllerID,
		TargetID:          x2.owner.TargetID,
		JobID:             uuid.NewString(),
		TraceID:           uuid.NewString(),
		Action:            "SOFTWARE_UNINSTALL",
		SoftwareOperation: "SOFTWARE_UNINSTALL",
		Software:          s,
		Uninstallation:    &SoftwareUninstallRequest{Installation: *install.Installation},
		DeadlineAt:        time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}
	shortReq.InputHash = Digest(shortReq)
	if err := validateSoftwareInput(shortReq); err != nil {
		t.Fatalf("short directory uninstall validate failed: %v", err)
	}
	wantShort := shortReq.Uninstallation.Installation.InstallRoot(x2.root)
	if filepath.Base(filepath.FromSlash(wantShort)) != "nginx-80" {
		t.Fatalf("unexpected short path=%q", wantShort)
	}

	// Tampering DirectoryName must be rejected by ownership binding even when InputHash is recomputed.
	tampered := shortReq
	tamperedInst := tampered.Uninstallation.Installation
	tamperedInst.DirectoryName = "other"
	tampered.Uninstallation = &SoftwareUninstallRequest{Installation: tamperedInst}
	tampered.InputHash = ""
	tampered.InputHash = Digest(tampered)
	if err := validateSoftwareInput(tampered); err == nil || err.Error() != "SOFTWARE_UNINSTALL_OWNERSHIP_MISMATCH" {
		t.Fatalf("expected SOFTWARE_UNINSTALL_OWNERSHIP_MISMATCH, got %v", err)
	}

	if runtime.GOOS == "linux" {
		root, err := x.uninstallRoot(req)
		if err != nil || root != wantLegacy {
			t.Fatalf("legacy uninstallRoot=%q err=%v want %q", root, err, wantLegacy)
		}
		shortRoot, err := x2.uninstallRoot(shortReq)
		if err != nil || shortRoot != wantShort {
			t.Fatalf("short directory uninstallRoot=%q err=%v want %q", shortRoot, err, wantShort)
		}
	}
}
