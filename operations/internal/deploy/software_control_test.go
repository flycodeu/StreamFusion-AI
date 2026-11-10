package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSoftwareRestartRequiresNewInvocationAndReconcilesWithoutReplay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux target directory fsync; command fake never touches real services")
	}
	for _, changed := range []bool{true, false} {
		t.Run(map[bool]string{true: "new-start", false: "same-start"}[changed], func(t *testing.T) {
			x := &executor{root: t.TempDir()}
			calls := []string{}
			base := unitSoftwareRunner("active", strings.Repeat("a", 64), &calls)
			restarts := 0
			x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
				if command == "/usr/bin/systemctl" {
					if len(args) > 0 && args[0] == "restart" {
						restarts++
						return []byte("restarted\n"), nil
					}
					if strings.Contains(strings.Join(args, " "), "InvocationID") {
						value := strings.Repeat("a", 32)
						if restarts > 0 && changed {
							value = strings.Repeat("b", 32)
						}
						return []byte(value + "\n"), nil
					}
				}
				return base(context.Background(), command, args...)
			}
			s := SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Version: 1, Kind: "SYSTEMD", Locator: "redis.service"}
			actual, err := ObserveSoftware(context.Background(), s, x.softwareRunner)
			if err != nil {
				t.Fatal(err)
			}
			s.IdentityHash = actual.IdentityHash
			if err = os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
				t.Fatal(err)
			}
			if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: "original"}); err != nil {
				t.Fatal(err)
			}
			req := commandTestRequest()
			req.Installation = nil
			req.Software = &s
			req.Action, req.SoftwareOperation = "SOFTWARE_RESTART", "SOFTWARE_RESTART"
			req.InputHash = Digest(req)
			var output bytes.Buffer
			if err = x.softwareLocked(req, &output); err != nil {
				t.Fatal(err)
			}
			var result Result
			if json.Unmarshal(output.Bytes(), &result) != nil {
				t.Fatal(output.String())
			}
			if changed && (result.Status != "SUCCEEDED" || !validLifecycleEvidence(req, result)) {
				t.Fatalf("new invocation unconfirmed %+v", result)
			}
			if !changed && result.Status != "UNKNOWN" {
				t.Fatalf("RUNNING alone proved restart %+v", result)
			}
			if restarts != 1 {
				t.Fatal("restart was split or repeated", restarts)
			}
			if next := x.reconcileSoftwareControl(req, filepath.Join(x.root, "jobs", req.JobID)); (next.Status == "SUCCEEDED") != changed || restarts != 1 {
				t.Fatalf("reconcile replayed or misconfirmed %+v", next)
			}
			if changed {
				output.Reset()
				if err = x.softwareLocked(req, &output); err != nil || restarts != 1 {
					t.Fatalf("same job replayed restart %v count=%d", err, restarts)
				}
			}
		})
	}
}

func TestDockerPauseAndContinueHaveSeparateVerifiedStates(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux target directory fsync; Docker is mocked")
	}
	x := &executor{root: t.TempDir()}
	state := "running"
	id := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	controls := []string{}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/docker" {
			return nil, errors.New("unexpected executable")
		}
		if len(args) > 1 && (args[1] == "pause" || args[1] == "unpause") {
			controls = append(controls, args[1])
			if args[1] == "pause" {
				state = "paused"
			} else {
				state = "running"
			}
			return nil, nil
		}
		if strings.Contains(strings.Join(args, " "), "StartedAt") {
			return []byte("2026-11-09T00:00:00Z"), nil
		}
		return json.Marshal(map[string]any{"id": id, "name": "/demo", "imageId": image, "image": "demo:1", "labels": map[string]string{}, "state": state})
	}
	s := SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Version: 1, Kind: "DOCKER", Locator: id}
	actual, err := ObserveSoftware(context.Background(), s, x.softwareRunner)
	if err != nil {
		t.Fatal(err)
	}
	s.IdentityHash = actual.IdentityHash
	_ = os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700)
	if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: "original"}); err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"SOFTWARE_PAUSE", "SOFTWARE_UNPAUSE"} {
		req := commandTestRequest()
		req.Installation = nil
		req.Software = &s
		req.Action, req.SoftwareOperation = action, action
		dir := filepath.Join(x.root, "jobs", action)
		_ = os.MkdirAll(dir, 0700)
		result, err := x.applySoftwareControl(req, dir)
		if err != nil || result.Status != "SUCCEEDED" || result.Software.State != []string{"PAUSED", "RUNNING"}[index] || !validLifecycleEvidence(req, result) {
			t.Fatalf("action %s result %+v err=%v", action, result, err)
		}
	}
	if strings.Join(controls, ",") != "pause,unpause" {
		t.Fatal("wrong container operations", controls)
	}
}

func TestReloadCannotBeRecoveredFromRunningStateWithoutVerifiedReceipt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux target directory fsync")
	}
	x := &executor{root: t.TempDir()}
	req := commandTestRequest()
	req.Installation = nil
	req.Action, req.SoftwareOperation = "SOFTWARE_RELOAD", "SOFTWARE_RELOAD"
	req.Software = &SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Kind: "SYSTEMD", Locator: "redis.service"}
	_ = os.MkdirAll(filepath.Dir(x.softwareFile(req.Software.ID)), 0700)
	if err := atomicJSON(x.softwareFile(req.Software.ID), softwareBinding{Software: *req.Software}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := atomicJSON(filepath.Join(dir, "software-control.json"), softwareControlIntent{Action: req.Action, Before: "old"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) { calls++; return nil, nil }
	if result := x.reconcileSoftwareControl(req, dir); result.Status != "UNKNOWN" || calls != 0 {
		t.Fatalf("running state incorrectly proved reload %+v calls=%d", result, calls)
	}
}
