package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestSoftwareConfigurationUnappliedFailureKeepsBindingAndCanAcknowledge(t *testing.T) {
	for _, phase := range []string{"CONFIGURATION_NOT_APPLIED", "NOT_STARTED", "OTHER_UNCONFIRMED"} {
		t.Run(phase, func(t *testing.T) {
			s, b, host, id, f := readyNginxFixture(t)
			before, _ := s.readSoftware(id)
			next := before.Installation.Config
			next.HostPort = 19288
			p := dataOf(t, b.call("POST", "/software-instances/"+id+"/configuration-plans", map[string]any{"expectedVersion": before.Version, "config": next}, 200))
			receipt := dataOf(t, b.call("POST", "/software-configuration-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "rejected-configuration"}, 202))
			job := receipt["jobId"].(string)
			var raw string
			s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
			var req deploy.Request
			if err := json.Unmarshal([]byte(raw), &req); err != nil {
				t.Fatal(err)
			}
			code := "SERVICE_PORT_UNAVAILABLE"
			if phase == "NOT_STARTED" {
				code = "TARGET_NOT_STARTED"
			}
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: host, JobID: job, InputHash: req.InputHash, Status: "FAILED", Phase: phase, ErrorCode: code, ObservedAt: Now()}
			err := b.h.commitSoftwareConfigurationResult(job, req, result)
			if phase == "OTHER_UNCONFIRMED" {
				if err == nil {
					t.Fatal("unconfirmed failure was incorrectly finalized")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _ := s.readSoftware(id)
			if after.BindingRevision != before.BindingRevision || after.IdentityHash != before.IdentityHash || after.Observation.ObservedAt != before.Observation.ObservedAt || after.Installation.Config.HostPort != before.Installation.Config.HostPort || after.DesiredState != before.DesiredState {
				t.Fatal("unapplied rejection changed old confirmed configuration", after)
			}
			f.mu.Lock()
			f.Results[job] = result
			f.mu.Unlock()
			b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
			var locks, pending int
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
			s.DB.QueryRow("SELECT target_ack_pending FROM jobs WHERE id=?", job).Scan(&pending)
			if locks != 0 || pending != 0 || f.ConfigurationCount.Load() != 0 {
				t.Fatal("confirmed early rejection could not close responsibility", locks, pending)
			}
		})
	}
}

// This is protocol evidence from the controlled SSH fixture, not a real
// systemd/Docker installation. Actual recipe/file verification is tested below
// the executor boundary and still needs independent Linux acceptance.
func installReceiptFixture(req deploy.Request, i serviceinstall.Request, marker string) deploy.Result {
	kind := installationRuntimeKind(i.Definition, i.Config)
	locator := strings.Repeat(marker, 64)
	e := &serviceinstall.Evidence{Kind: kind, SoftwareID: i.SoftwareID, BindingRevision: i.BindingRevision, RecipeKey: i.Definition.Key, RecipeRevision: i.Definition.Revision, ConfigHash: serviceinstall.Digest(i.Config), ObservedVersion: i.Definition.Version, Health: "READY", ObservedAt: Now()}
	state := "RUNNING"
	if i.Config.Mode() == "DOCKER" {
		e.ImageID = i.Definition.ImageID
		if kind == "DOCKER" {
			e.ContainerID = locator
		} else {
			locator = "image:" + e.ImageID
			state = "INSTALLED"
		}
	} else {
		e.BinaryHash = strings.Repeat("a", 64)
		e.BinaryPath = serviceinstall.InstallRoot("/opt/plugin-test", i.SoftwareID, i.Config) + "/" + serviceinstall.NativeBinaryRelative(i.Definition)
		if kind == "SYSTEMD" {
			locator = "sfops-service-" + i.SoftwareID + ".service"
		} else {
			locator = e.BinaryPath
			state = "INSTALLED"
		}
		if i.Definition.Key == "docker" {
			locator = "docker.service"
			e.BinaryPath = "/usr/bin/dockerd"
		}
	}
	e.Locator = locator
	e.IdentityHash = deploy.Digest([]string{kind, locator, e.ImageID})
	return deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "VERIFIED", ObservedAt: Now(), Installation: e, Software: &deploy.SoftwareObservation{Kind: kind, Locator: locator, IdentityHash: e.IdentityHash, State: state, Version: i.Definition.Version, ObservedAt: Now()}}
}

func readyNginxFixture(t *testing.T, extensions ...func(deploy.Request) ([]byte, bool)) (*Store, *browser, string, string, *installFixture) {
	t.Helper()
	s, b := setup(t)
	b.login()
	host, f := installSSHFixture(t, s, b, extensions...)
	p := nginxPlan(t, b, host, 18188)
	receipt := dataOf(t, b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "initial-nginx"}, 202))
	job, id := receipt["jobId"].(string), receipt["softwareInstanceId"].(string)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processSoftware(context.Background(), job, raw)
	i, err := s.readSoftware(id)
	if err != nil || !installationBound(i) || i.InstallationStatus != "READY" {
		t.Fatal(i, err)
	}
	return s, b, host, id, f
}

func TestSoftwareConfigurationReplayRollbackAndBinding(t *testing.T) {
	for _, mode := range []string{"success", "rollback", "unknown", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, b, _, id, f := readyNginxFixture(t)
			i, _ := s.readSoftware(id)
			next := i.Installation.Config
			next.HostPort = 19188
			plan := dataOf(t, b.call("POST", "/software-instances/"+id+"/configuration-plans", map[string]any{"expectedVersion": i.Version, "config": next}, 200))
			path := "/software-configuration-plans/" + plan["id"].(string) + "/submit"
			receipt := dataOf(t, b.call("POST", path, map[string]string{"idempotencyKey": "configuration-once"}, 202))
			job := receipt["jobId"].(string)
			replay := dataOf(t, b.call("POST", path, map[string]string{"idempotencyKey": "configuration-once"}, 200))
			if replay["jobId"] != job || replay["softwareInstanceId"] != id {
				t.Fatal("replay lost identity", replay)
			}
			if mode == "cancel" {
				b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 200)
				current, _ := s.readSoftware(id)
				if current.DesiredState != i.DesiredState || f.ConfigurationCount.Load() != 0 {
					t.Fatal(current)
				}
				return
			}
			f.ConfigurationFailure.Store(mode == "rollback")
			f.Unknown.Store(mode == "unknown")
			f.NoAck.Store(true)
			var raw string
			s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
			b.h.processSoftware(context.Background(), job, raw)
			current, err := s.readSoftware(id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "success" {
				if current.BindingRevision != i.BindingRevision+1 || current.Installation.Config.HostPort != 19188 || current.Locator == i.Locator || current.ConfigurationJobID != job || softwareRequest(current).Ownership.JobID != job {
					t.Fatal(current)
				}
			} else if current.BindingRevision != i.BindingRevision || current.Installation.Config.HostPort != i.Installation.Config.HostPort {
				t.Fatal("failed/unknown configuration replaced old binding", current)
			}
			var locks int
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
			if locks != 2 {
				t.Fatal("responsibility released before ACK", locks)
			}
			f.NoAck.Store(false)
			f.Unknown.Store(false)
			b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
			if locks != 0 || f.ConfigurationCount.Load() != 1 {
				t.Fatal(locks, f.ConfigurationCount.Load())
			}
			f.OldExecutor.Store(true)
			replay = dataOf(t, b.call("POST", path, map[string]string{"idempotencyKey": "configuration-once"}, 200))
			if replay["softwareInstanceId"] != id {
				t.Fatal(replay)
			}
		})
	}
}

func TestSoftwareConfigurationRejectsDataMigrationAndStaleBinding(t *testing.T) {
	s, b, _, id, _ := readyNginxFixture(t)
	i, _ := s.readSoftware(id)
	next := i.Installation.Config
	next.StorageRoot = "/data/other"
	if !strings.Contains(b.call("POST", "/software-instances/"+id+"/configuration-plans", map[string]any{"expectedVersion": i.Version, "config": next}, 409).Body.String(), "STORAGE_MIGRATION_REQUIRED") {
		t.Fatal("data migration was not blocked")
	}
	next = i.Installation.Config
	next.HostPort = 19189
	p := dataOf(t, b.call("POST", "/software-instances/"+id+"/configuration-plans", map[string]any{"expectedVersion": i.Version, "config": next}, 200))
	if _, err := s.DB.Exec("UPDATE software_instances SET version=version+1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	b.call("POST", "/software-configuration-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "stale-configuration"}, 409)
	var n int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='SOFTWARE_CONFIGURE'").Scan(&n)
	if n != 0 {
		t.Fatal("stale config created a job")
	}
}

func TestUnknownCannotOverwriteTerminalReceipt(t *testing.T) {
	s, b, _, id, _ := readyNginxFixture(t)
	i, _ := s.readSoftware(id)
	var before string
	s.DB.QueryRow("SELECT result_json FROM jobs WHERE id=?", i.LastJobID).Scan(&before)
	b.h.jobUnknown(i.LastJobID, "CONFLICTING_RECEIPT")
	var status, after string
	s.DB.QueryRow("SELECT status,result_json FROM jobs WHERE id=?", i.LastJobID).Scan(&status, &after)
	current, _ := s.readSoftware(id)
	if status != "SUCCEEDED" || before != after || current.ActualState != "RUNNING" {
		t.Fatal(status, current.ActualState)
	}
}
