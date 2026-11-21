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
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if err != nil || len(lines) < 4 || strings.TrimSpace(lines[3]) != tc.want {
				t.Fatalf("probe: %s, error=%v", out, err)
			}
		})
	}
}

func TestHostReadOnlyRecordsDistributionSeparatelyFromArchitecture(t *testing.T) {
	facts := unknownHostCapabilities()
	applyHostReadOnly(&facts, "SFOPS_HOST_V1\nLinux\nx86_64\nDOCKER_MISSING\nDISTRO centos 9\n")
	if facts.Distribution != "centos" || facts.DistributionVersion != "9" || facts.Platform != "linux/x86_64" {
		t.Fatal(facts)
	}
}

func TestHostReadOnlyRejectsUnexpectedFifthLine(t *testing.T) {
	for _, line := range []string{"warning: unexpected login output", "DISTRO ubuntu bad/version", "DISTRO"} {
		facts := unknownHostCapabilities()
		applyHostReadOnly(&facts, "SFOPS_HOST_V1\nLinux\nx86_64\nDOCKER_MISSING\n"+line+"\n")
		if facts.ReadOnly.Status != "UNAVAILABLE" || facts.ReadOnly.Code != "READ_ONLY_RESPONSE_INVALID" || facts.Platform != "" {
			t.Fatalf("untrusted fifth line %q was accepted: %+v", line, facts)
		}
	}
	for _, line := range []string{"DISTRO ubuntu 24.04", "DISTRO rocky"} {
		facts := unknownHostCapabilities()
		applyHostReadOnly(&facts, "SFOPS_HOST_V1\nLinux\nx86_64\nDOCKER_MISSING\n"+line+"\n")
		if facts.ReadOnly.Status != "AVAILABLE" || facts.Platform != "linux/x86_64" {
			t.Fatalf("valid distribution %q was rejected: %+v", line, facts)
		}
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

func TestHostCapabilityCheckPreservesKnownFactsWhenExecutorCheckFails(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr, status, code string
		exit                               uint32
	}{
		{"timeout", "", "", "UNKNOWN", "EXECUTOR_CHECK_TIMEOUT", 0},
		{"invalid reply", "unfinished JSON", "", "UNKNOWN", "EXECUTOR_RESPONSE_INVALID", 0},
		{"denied", "", "sudo: a password is required", "UNAVAILABLE", "SUDO_AUTHORIZATION_REQUIRED", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, false)
			host := f.create(t, b, "capabilities-"+tc.name)
			target, err := s.readTarget(host["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			responses := func(command string) *hostSSHResponse {
				switch command {
				case hostReadOnlyCommand:
					return &hostSSHResponse{Stdout: "SFOPS_HOST_V1\nLinux\nx86_64\nDOCKER_PRESENT\n"}
				case executorSSHCommand(target):
					response := &hostSSHResponse{Stdout: tc.stdout, Stderr: tc.stderr, Exit: tc.exit}
					if tc.name == "timeout" {
						response.Input = func([]byte) { <-ctx.Done() }
					}
					return response
				}
				return nil
			}
			f.Responses.Store(&responses)
			facts := b.h.observeHost(ctx, target)
			if facts.Reachability.Status != "AVAILABLE" || facts.ReadOnly.Status != "AVAILABLE" {
				t.Fatalf("lost successful read-only evidence: %+v", facts)
			}
			if facts.Mutation.Status != tc.status || facts.Mutation.Code != tc.code {
				t.Fatalf("unexpected authorization: %+v", facts.Mutation)
			}
			if facts.Docker.Status != "UNKNOWN" || !strings.Contains(facts.Docker.Message, "已安装Docker") || facts.DockerCompose.Status != "UNKNOWN" {
				t.Fatalf("lost installed Docker evidence or inferred access: %+v", facts)
			}
			if tc.name == "timeout" {
				facts.Mutation.Status = "UNAVAILABLE" // A persisted observation from an older controller.
				if _, err = s.DB.Exec("UPDATE targets SET capabilities_json=? WHERE id=?", jsonText(facts), target.ID); err != nil {
					t.Fatal(err)
				}
				stored, err := s.readTarget(target.ID)
				if err != nil || stored.Capabilities.Mutation.Status != "UNKNOWN" || stored.Capabilities.Mutation.ObservedAt != facts.Mutation.ObservedAt {
					t.Fatalf("legacy observation: %+v, %v", stored.Capabilities.Mutation, err)
				}
			}
		})
	}
}
