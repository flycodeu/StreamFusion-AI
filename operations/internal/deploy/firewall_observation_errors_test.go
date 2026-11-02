package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type portSSHExit int

func (e portSSHExit) Error() string   { return "remote command failed: secret fixture" }
func (e portSSHExit) ExitStatus() int { return int(e) }

type portLocalExit int

func (e portLocalExit) Error() string { return "local command failed: secret fixture" }
func (e portLocalExit) ExitCode() int { return int(e) }

func TestObserveHostPortsClassifiesFailuresWithoutExposingDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, output, code string
		err                error
	}{
		{"timeout", "", "PORT_OBSERVATION_TIMEOUT", fmt.Errorf("secret: %w", context.DeadlineExceeded)},
		{"cancelled", "", "PORT_OBSERVATION_TIMEOUT", context.Canceled},
		{"runner limit", "", "PORT_SNAPSHOT_TOO_LARGE", errors.New("SOFTWARE_OBSERVATION_TOO_LARGE")},
		{"oversized output", strings.Repeat("x", (64<<10)+1), "PORT_SNAPSHOT_TOO_LARGE", nil},
		{"exact capped output", strings.Repeat("x", 64<<10), "PORT_SNAPSHOT_TOO_LARGE", nil},
		{"missing binary", "", "PORT_COMMAND_UNAVAILABLE", &os.PathError{Op: "lstat", Path: "/secret/ss", Err: os.ErrNotExist}},
		{"missing executable", "", "PORT_COMMAND_UNAVAILABLE", &exec.Error{Name: "/secret/ss", Err: exec.ErrNotFound}},
		{"unprotected binary", "", "PORT_COMMAND_UNAVAILABLE", errors.New("EXECUTOR_PATH_NOT_PROTECTED")},
		{"ssh missing command", "", "PORT_COMMAND_UNAVAILABLE", fmt.Errorf("transport: %w", portSSHExit(127))},
		{"local non executable", "", "PORT_COMMAND_UNAVAILABLE", portLocalExit(126)},
		{"permission errno", "", "PORT_OBSERVATION_DENIED", &os.PathError{Op: "exec", Path: "/secret/ss", Err: os.ErrPermission}},
		{"permission stderr", "ss: Cannot open netlink socket: Permission denied\nsecret fixture", "PORT_OBSERVATION_DENIED", portLocalExit(1)},
		{"not permitted stderr", "ss: Operation not permitted\nsecret fixture", "PORT_OBSERVATION_DENIED", portLocalExit(1)},
		{"unknown ssh failure", "", "PORT_OBSERVATION_FAILED", portSSHExit(1)},
		{"unknown local failure", "secret fixture", "PORT_OBSERVATION_FAILED", portLocalExit(1)},
		{"transport failure", "secret fixture", "PORT_OBSERVATION_FAILED", errors.New("transport interrupted with secret fixture")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ports, docker, dockerCode, err := ObserveHostPorts(context.Background(), func(_ context.Context, command string, args ...string) ([]byte, error) {
				calls++
				if command != "/usr/bin/ss" || strings.Join(args, " ") != "-H -lntup" {
					t.Fatalf("unexpected command after failed probe: %s %v", command, args)
				}
				return []byte(tc.output), tc.err
			})
			if err == nil || err.Error() != tc.code || len(ports) != 0 || docker != "" || dockerCode != "" || calls != 1 {
				t.Fatalf("expected only %s, got ports=%v docker=%q dockerCode=%q err=%v calls=%d", tc.code, ports, docker, dockerCode, err, calls)
			}
			change, snapshot := firewallFixture()
			snapshot.Code = err.Error()
			for _, operation := range []string{"ADD", "DELETE", "ENABLE", "DISABLE"} {
				change.Operation = operation
				if err := validateFirewallEffect(change, snapshot); err == nil || err.Error() != "FIREWALL_SNAPSHOT_CHANGED" {
					t.Fatalf("%s permitted an unconfirmed port snapshot: %v", operation, err)
				}
			}
		})
	}
}

func TestObserveHostPortsContextAndUnavailableRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := ObserveHostPorts(ctx, func(context.Context, string, ...string) ([]byte, error) { return nil, nil })
	if err == nil || err.Error() != "PORT_OBSERVATION_TIMEOUT" {
		t.Fatal("cancelled observation was not classified as timeout", err)
	}
	_, _, _, err = ObserveHostPorts(context.Background(), nil)
	if err == nil || err.Error() != "PORT_COMMAND_UNAVAILABLE" {
		t.Fatal("missing runner was not classified as unavailable", err)
	}
}

func TestHostPortLimitRejectsPossiblyTruncatedSuccessfulOutput(t *testing.T) {
	line := "tcp LISTEN 0 128 127.0.0.1:19090 0.0.0.0:*\n"
	for _, size := range []int{(64 << 10) - 1, 64 << 10, (64 << 10) + 1} {
		ports, err := ParseHostPorts(line + strings.Repeat(" ", size-len(line)))
		if size < 64<<10 {
			if err != nil || len(ports) != 1 || ports[0].Port != 19090 {
				t.Fatal("complete below-limit snapshot changed", ports, err)
			}
		} else if err == nil || err.Error() != "PORT_SNAPSHOT_TOO_LARGE" || ports != nil {
			t.Fatal("possibly truncated snapshot was accepted", size, ports, err)
		}
	}
}

func TestObserveHostPortsKeepsSuccessfulPortsWhenDockerUnavailable(t *testing.T) {
	ports, docker, dockerCode, err := ObserveHostPorts(context.Background(), func(_ context.Context, command string, _ ...string) ([]byte, error) {
		if command == "/usr/bin/ss" {
			return []byte("tcp LISTEN 0 128 127.0.0.1:19090 0.0.0.0:*\n"), nil
		}
		return []byte("secret fixture"), os.ErrPermission
	})
	if err != nil || len(ports) != 1 || ports[0].Port != 19090 || docker != "" || dockerCode != "DOCKER_PORTS_UNAVAILABLE" {
		t.Fatal("successful port evidence changed", ports, docker, dockerCode, err)
	}
}
