package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitDiscoveryUsesFixedReadOnlyCommand(t *testing.T) {
	observation, err := ObserveSoftware(context.Background(), SoftwareRequest{Kind: "TOOL", Locator: "git"}, func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/git" || strings.Join(args, " ") != "--version" {
			t.Fatalf("unexpected command: %s %v", command, args)
		}
		return []byte("git version 2.43.0\n"), nil
	})
	if err != nil || observation.State != "INSTALLED" || observation.ExecutablePath != "/usr/bin/git" || observation.Version != "git version 2.43.0" {
		t.Fatalf("Git detection: %+v %v", observation, err)
	}
}

func TestGitTerminalBlockPreservesPathAndDoesNotDuplicate(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("requires a POSIX shell")
	}
	dir := t.TempDir()
	binary := filepath.ToSlash(filepath.Join(dir, "bin/git"))
	if err := os.Mkdir(filepath.Dir(binary), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'git fixture\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		binary = "/" + strings.ToLower(binary[:1]) + binary[2:]
	}
	block := string(gitTerminalBlock(binary))
	command := exec.Command(shell, "-c", "PATH=/usr/bin:/bin\n"+block+block+"command -v git\nprintf '%s\\n' \"$PATH\"\ngit --version")
	out, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "git fixture") || strings.Count(string(out), filepath.ToSlash(filepath.Dir(binary))) != 2 || !strings.Contains(string(out), ":/usr/bin:/bin") {
		t.Fatalf("terminal Git path: %s %v", out, err)
	}
}
