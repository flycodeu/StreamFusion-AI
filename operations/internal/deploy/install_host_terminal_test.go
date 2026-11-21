package deploy

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestDockerInstallPreparesHostOncePerJob(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target receipts require Linux directory fsync")
	}
	for _, test := range []struct {
		state    string
		installs int
	}{
		{"SFOPS_TERMINAL_READY", 0},
		{"SFOPS_TERMINAL_NOT_APPLICABLE", 0},
		{"SFOPS_TERMINAL_MISSING", 1},
	} {
		t.Run(test.state, func(t *testing.T) {
			x, req := serviceRequestFixture(t)
			req.Installation.PrepareHostTerminal = true
			dir := filepath.Join(x.root, "jobs", req.JobID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			probes, installs := 0, 0
			x.softwareRun = func(limit time.Duration, command string, args ...string) ([]byte, error) {
				if command != "/usr/bin/env" || limit <= 0 {
					t.Fatalf("unexpected command %s (%v)", command, limit)
				}
				switch args[len(args)-1] {
				case serviceinstall.HostTerminalProbe(serviceinstall.HostTerminalLauncherSHA()):
					probes++
					return []byte(test.state + "\n"), nil
				case serviceinstall.HostTerminalInstaller():
					installs++
					return []byte("SFOPS_TERMINAL_STAGE=VERIFY\nSFOPS_TERMINAL_READY\n"), nil
				default:
					t.Fatal("unexpected terminal program")
					return nil, nil
				}
			}
			for range 2 {
				if code := x.prepareInstallationTerminal(req, dir); code != "" {
					t.Fatal(code)
				}
			}
			if probes != 1 || installs != test.installs {
				t.Fatalf("probe=%d install=%d", probes, installs)
			}
		})
	}
}

func TestDockerHostTerminalFailurePrecedesAnyComponentChange(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target receipts require Linux directory fsync")
	}
	x, req := serviceRequestFixture(t)
	req.Installation.PrepareHostTerminal = true
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	count := 0
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/env" {
			t.Fatalf("component touched: %s", command)
		}
		count++
		if count == 1 {
			return []byte("SFOPS_TERMINAL_MISSING"), nil
		}
		return []byte("SFOPS_TERMINAL_STAGE=APT_UPDATE\n"), errors.New("apt failure")
	}
	result, err := x.installService(req, dir)
	if err != nil || result.Status != "FAILED" || result.Phase != hostTerminalPhase || result.ErrorCode != "TERMINAL_TOOLS_APT_UPDATE" {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(x.softwareFile(req.Installation.SoftwareID)); !os.IsNotExist(err) {
		t.Fatal("unexpected binding", err)
	}
	// Simulate lost final receipt while terminal preparation was in progress.
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
		t.Fatal("STATUS re-executed commands")
		return nil, nil
	}
	recovered := x.reconcileSoftware(req, dir)
	if recovered.Status != "FAILED" || recovered.Phase != hostTerminalPhase {
		t.Fatal(recovered)
	}
	if _, err := x.installResumeOffset(req, dir); err == nil {
		t.Fatal("interrupted terminal install was replayable")
	}
	log, err := os.ReadFile(filepath.Join(dir, "service-install.log"))
	if err != nil || !strings.Contains(string(log), "apt-get update") {
		t.Fatal("no actionable diagnostic", err)
	}
}

func TestHostTerminalInstallFlagBelongsToSignedDockerRequest(t *testing.T) {
	_, req := serviceRequestFixture(t)
	original := CommandRequestDigest(req)
	req.Installation.PrepareHostTerminal = true
	if original == CommandRequestDigest(req) {
		t.Fatal("preparation is outside signed input")
	}
	pub, key, _ := ed25519.GenerateKey(nil)
	req.Secrets = map[string]string{"test": "transport-only"}
	if err := SignCommandRequest(&req, key); err != nil {
		t.Fatal(err)
	}
	req.Installation.PrepareHostTerminal = false
	if err := verifyCommandSignature(req, base64.StdEncoding.EncodeToString(pub)); err == nil {
		t.Fatal("install flag could be removed")
	}
	req.Installation.PrepareHostTerminal = true
	req.Installation.Config.RuntimeMode = "NATIVE"
	if err := req.Installation.Validate(); err == nil || err.Error() != "HOST_TERMINAL_RUNTIME_INVALID" {
		t.Fatal("native installation accepted terminal preparation", err)
	}
}

func TestDockerHostTerminalFiltersPackageOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target shell pipeline runs on Linux")
	}
	body := "echo SFOPS_TERMINAL_STAGE=APT_UPDATE >&2\necho private-repository-url >&2\necho SFOPS_TERMINAL_READY\nexit 100\n"
	command := exec.Command("/bin/bash", "-c", terminalInstallCommand, "host-terminal", body)
	out, err := command.CombinedOutput()
	if err == nil || string(out) != "SFOPS_TERMINAL_STAGE=APT_UPDATE\nSFOPS_TERMINAL_READY\n" {
		t.Fatalf("filtered stream or exit status changed: %q (%v)", out, err)
	}
}

func TestDockerToolManifestIdentitySurvivesBindingAndReceipt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target receipts require Linux directory fsync")
	}
	x, req := serviceRequestFixture(t)
	d, err := serviceinstall.Find("python", "3.12.15")
	if err != nil {
		t.Fatal(err)
	}
	req.Installation.Definition = d
	req.Installation.Config = serviceinstall.Config{Name: "Python", RuntimeMode: "DOCKER", MemoryMiB: 256}
	if !req.Installation.DockerImageIDMatches(d.Digest) {
		t.Fatal("published tool manifest was rejected")
	}
	if err := os.MkdirAll(filepath.Dir(x.softwareFile(req.Installation.SoftwareID)), 0700); err != nil {
		t.Fatal(err)
	}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/docker" {
			t.Fatalf("unexpected executable %s", command)
		}
		if args[0] == "image" && args[len(args)-1] == d.Digest {
			return fmt.Appendf(nil, `{"id":%q,"os":"linux","arch":"amd64"}`, d.Digest), nil
		}
		if args[0] == "run" && slices.Contains(args, d.Digest) {
			return []byte("Python 3.12.15"), nil
		}
		t.Fatalf("unexpected Docker invocation %q", args)
		return nil, nil
	}
	result, err := x.finishDockerToolFlow(req, d.Digest)
	if err != nil || result.Status != "SUCCEEDED" || result.Installation.ImageID != d.Digest || result.Installation.Locator != "image:"+d.Digest {
		t.Fatal(result, err)
	}
	if err = x.verifyOwnedInstallation(req, result); err != nil {
		t.Fatal(err)
	}
	result.Installation.ImageID = d.ImageID
	if err = x.verifyOwnedInstallation(req, result); err == nil {
		t.Fatal("mismatched locator and receipt ID accepted")
	}
}
