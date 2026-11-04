package controller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostDockerReadOnlyProbe(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("requires a POSIX shell")
	}
	for _, tc := range []struct {
		name, cli, binary, service, want string
	}{
		{"missing", "false", "", "printf not-found", "DOCKER_MISSING"},
		{"cli", "true", "", "false", "DOCKER_PRESENT"},
		{"daemon-outside-path", "false", "dockerd", "false", "DOCKER_PRESENT"},
		{"stopped-service", "false", "", "printf loaded", "DOCKER_PRESENT"},
		{"service-query-denied", "false", "", "false", "DOCKER_UNKNOWN"},
		{"directory-unavailable", "false", "blocked", "printf not-found", "DOCKER_UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.binary != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.binary), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if tc.binary == "blocked" {
					dir = filepath.Join(dir, tc.binary)
				}
			}
			script := strings.TrimSuffix(strings.TrimPrefix(hostReadOnlyCommand, "/bin/sh -c '"), "'")
			// Exercise the real probe against an isolated binary directory and
			// controlled service responses, independent of the test host's Docker.
			const directories = "/bin /sbin /usr/bin /usr/sbin /usr/local/bin /usr/local/sbin /snap/bin"
			if strings.Count(script, directories) != 1 {
				t.Fatal("probe directories changed; update isolated fixture")
			}
			script = strings.Replace(script, directories, shellQuote(filepath.ToSlash(dir)), 1)
			prefix := "command() { " + tc.cli + "; }; systemctl() { " + tc.service + "; };\n"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, shell, "-c", prefix+script).CombinedOutput()
			if err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), tc.want) {
				t.Fatalf("probe: %s, error=%v", out, err)
			}
		})
	}
}

func TestHostDockerObservationPreservesInstallationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, probe, status, code string
		available                 bool
	}{
		{"missing", "DOCKER_MISSING", "UNAVAILABLE", "DOCKER_NOT_INSTALLED", false},
		{"engine-ready", "DOCKER_MISSING", "AVAILABLE", "DOCKER_ENGINE_READY", true},
		{"installed-but-unavailable", "DOCKER_PRESENT", "UNAVAILABLE", "DOCKER_ENGINE_UNAVAILABLE", false},
		{"cannot-confirm", "DOCKER_UNKNOWN", "UNKNOWN", "DOCKER_ACCESS_UNCONFIRMED", false},
		{"unexpected-probe", "unexpected", "UNKNOWN", "DOCKER_ACCESS_UNCONFIRMED", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, tc.available, func(command string) ([]byte, bool) {
				if command == hostReadOnlyCommand {
					return []byte("SFOPS_HOST_V1\nLinux\nx86_64\n" + tc.probe + "\n"), true
				}
				return nil, false
			})
			host := f.create(t, b, tc.name)
			checked := dataOf(t, b.call("POST", "/hosts/"+host["id"].(string)+"/checks", nil, 200))
			caps := checked["capabilities"].(map[string]any)
			docker := caps["docker"].(map[string]any)
			if docker["status"] != tc.status || docker["code"] != tc.code {
				t.Fatalf("Docker observation: %v", docker)
			}
			if caps["mutation"].(map[string]any)["status"] != "AVAILABLE" {
				t.Fatal("Docker status changed executor authorization")
			}
		})
	}
}
