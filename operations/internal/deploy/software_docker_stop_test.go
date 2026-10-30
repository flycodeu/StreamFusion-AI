package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOwnedDockerStopIncludesSocketAndEvidence(t *testing.T) {
	s := SoftwareRequest{Kind: "SYSTEMD", SoftwareKey: "DOCKER", Locator: "docker.service", Ownership: &SoftwareOwnership{RuntimeMode: "NATIVE", BinaryPath: "/usr/bin/dockerd"}}
	if got := strings.Join(softwareSystemdAction(s, "stop"), " "); got != "stop -- docker.service docker.socket" {
		t.Fatal(got)
	}
	if got := strings.Join(softwareSystemdAction(s, "start"), " "); got != "start -- docker.service" {
		t.Fatal(got)
	}
	s.Ownership = nil
	if strings.Contains(strings.Join(softwareSystemdAction(s, "stop"), " "), "docker.socket") {
		t.Fatal("external systemd unit gained engine lifecycle semantics")
	}
	for _, tc := range []struct {
		state    string
		err      error
		inactive bool
	}{{"ActiveState=inactive\n", nil, true}, {"ActiveState=active\n", nil, false}, {"ActiveState=inactive\n", errors.New("timeout"), false}, {"ActiveState=inactive\nActiveState=active\n", nil, false}} {
		x := &executor{softwareRun: func(_ time.Duration, command string, args ...string) ([]byte, error) {
			if command != "/usr/bin/systemctl" || strings.Join(args, " ") != "show --no-pager --property=ActiveState -- docker.socket" {
				t.Fatal(command, args)
			}
			return []byte(tc.state), tc.err
		}}
		if x.softwareDockerSocketInactive(context.Background()) != tc.inactive {
			t.Fatal(tc.state, tc.err)
		}
	}
}

func TestOwnedDockerStopDurableJobRequiresSocketInactiveAndNeverReplays(t *testing.T) {
	f := newSoftwareFixture(t)
	state, socket := "active", "active"
	stopCalls := 0
	digest := strings.Repeat("a", 64)
	f.x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/docker" {
			return []byte(""), nil
		}
		if command == "/usr/bin/systemctl" {
			if strings.Contains(strings.Join(args, " "), "docker.socket") && args[0] == "show" {
				return []byte("ActiveState=" + socket + "\n"), nil
			}
			if args[0] == "stop" {
				if strings.Join(args, " ") != "stop -- docker.service docker.socket" {
					t.Fatal(args)
				}
				stopCalls++
				state = "inactive"
				// Simulate a lost socket stop. The daemon stopped but the socket
				// can still reactivate it, so this must remain UNKNOWN.
				return nil, errors.New("lost reply")
			}
		}
		out, err := unitSoftwareRunner(state, digest, &f.calls)(context.Background(), command, args...)
		return []byte(strings.ReplaceAll(string(out), "redis.service", "docker.service")), err
	}
	s := &SoftwareRequest{ID: uuid.NewString(), Version: 1, Kind: "SYSTEMD", Locator: "docker.service", SoftwareKey: "DOCKER", Ownership: &SoftwareOwnership{RuntimeMode: "NATIVE", BinaryPath: "/usr/bin/dockerd", BinaryHash: digest, ControllerID: f.x.owner.ControllerID, TargetID: f.x.owner.TargetID, SoftwareID: "", JobID: uuid.NewString(), ConfigHash: digest}}
	s.Ownership.SoftwareID = s.ID
	actual, err := ObserveSoftware(context.Background(), *s, f.x.softwareRunner)
	if err != nil {
		t.Fatal(err)
	}
	s.IdentityHash = actual.IdentityHash
	req := f.request(t, "SOFTWARE_STOP", s)
	if err = os.MkdirAll(filepath.Dir(f.x.softwareFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(f.x.softwareFile(s.ID), softwareBinding{Software: *s, JobID: s.Ownership.JobID}); err != nil {
		t.Skip("Linux directory fsync fixture:", err)
	}
	result := runSoftwareFixture(t, f, req)
	if result.Status != "UNKNOWN" || stopCalls != 1 {
		t.Fatal(result, stopCalls)
	}
	req.Action = "STATUS"
	result = runSoftwareFixture(t, f, req)
	if result.Status != "UNKNOWN" || stopCalls != 1 {
		t.Fatal("socket-active stop finalized or replayed", result, stopCalls)
	}
	socket = "inactive"
	result = runSoftwareFixture(t, f, req)
	if result.Status != "SUCCEEDED" || !result.DockerSocketInactive || stopCalls != 1 {
		t.Fatal(result, stopCalls)
	}
	ackSoftwareFixture(t, f, req, result)
}
