package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

func TestApplicationLifecyclePreservesReleaseAndStoppedPolicyAcrossJobs(t *testing.T) {
	s, b := setup(t)
	b.login()
	results := map[string]deploy.Result{}
	held, running, calls := false, true, 0
	configured := runtimepolicy.Policy{Restart: "unless-stopped", AutoStart: true, StopTimeoutSeconds: 10}
	host := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		if req.ApplicationOperation == "" {
			return nil, false
		}
		if req.Action == "PROGRESS" {
			raw, _ := json.Marshal(deploy.Result{Phase: "APPLICATION_STATE_VERIFIED"})
			return raw, true
		}
		var result deploy.Result
		if req.Action == "ACK" || req.Action == "STATUS" {
			result = results[req.JobID]
			if req.Action == "ACK" && req.ResultHash == deploy.Digest(result) {
				result.Ack = true
			}
		} else {
			calls++
			if req.Action == "APP_STOP" {
				held, running = true, false
			}
			if req.Action == "APP_START" || req.Action == "APP_RESTART" {
				held, running = false, true
			}
			if req.RuntimePolicy.Policy != nil {
				configured = *req.RuntimePolicy.Policy
			}
			p, a := configured, configured
			state := &runtimepolicy.State{Configured: &p, Actual: &a, Editable: true, ObservedAt: Now()}
			state.Held = held
			if held {
				a := *state.Configured
				a.Restart = "no"
				a.AutoStart = false
				a.MaxRetries = 0
				state.Actual = &a
			}
			health := "STOPPED"
			if running {
				health = "APPLICATION_READY"
			}
			result = deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "APPLICATION_STATE_VERIFIED", ReleaseID: req.Candidate.ID, ImageID: req.Candidate.ImageID, ConfigHash: deploy.Digest(req.Candidate.Config), RuntimePolicy: state, Health: health, ObservedAt: Now()}
			results[req.JobID] = result
			if req.Action == "APP_STOP" {
				// The command completed, but the first response was lost.
				result.Status = "UNKNOWN"
			}
		}
		raw, _ := json.Marshal(result)
		return raw, true
	})
	conf := deploy.Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/health", HealthSeconds: 10, DataCompatibility: "v1"}
	id := dataOf(t, b.call("POST", "/instances", map[string]any{"name": "应用运行策略", "targetId": host, "config": conf}, 200))["id"].(string)
	artifact, releaseID := uuid.NewString(), uuid.NewString()
	release := deploy.Release{ID: releaseID, InstanceID: id, ArtifactID: artifact, ArchiveSHA256: strings.Repeat("a", 64), ByteSize: 1, ImageID: "sha256:" + strings.Repeat("b", 64), Config: conf}
	if _, err := s.DB.Exec("INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?,'fixture',?,1,?,?)", artifact, release.ArchiveSHA256, release.ImageID, Now()); err != nil {
		t.Fatal(err)
	}
	frozen := jsonText(release)
	if _, err := s.DB.Exec("INSERT INTO releases VALUES(?,?,?,?,?)", releaseID, id, artifact, frozen, Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE instances SET current_release_id=? WHERE id=?", releaseID, id); err != nil {
		t.Fatal(err)
	}
	for n, action := range []string{"STOP", "RUNTIME_POLICY", "START"} {
		i, err := s.readInstance(id)
		if err != nil {
			t.Fatal(err)
		}
		input := map[string]any{"expectedVersion": i.Version, "idempotencyKey": "lifecycle-" + action}
		endpoint := "/instances/" + id + "/actions"
		if action == "RUNTIME_POLICY" {
			endpoint = "/instances/" + id + "/runtime-policy"
			input["policy"] = runtimepolicy.Policy{Restart: "on-failure", MaxRetries: 3, StopTimeoutSeconds: 10}
		} else {
			input["action"] = action
		}
		accepted := dataOf(t, b.call("POST", endpoint, input, 202))
		job := accepted["jobId"].(string)
		var raw string
		if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		b.h.processRelease(context.Background(), job, raw)
		if n == 0 {
			var status string
			var locks int
			if err = s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status); err != nil || status != "UNKNOWN" {
				t.Fatal("unconfirmed stop was not preserved", status, err)
			}
			if err = s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks); err != nil || locks != 2 {
				t.Fatal("unconfirmed stop lost locks", locks, err)
			}
			pending, err := s.readInstance(id)
			if err != nil || pending.PendingJob == nil || pending.PendingJob.ID != job || pending.PendingJob.Status != "UNKNOWN" {
				t.Fatal("original operation is not visible", err)
			}
			newDraft := conf
			newDraft.RuntimePolicy = &runtimepolicy.Policy{Restart: "no", StopTimeoutSeconds: 10}
			b.call("PATCH", "/instances/"+id, map[string]any{"name": pending.Name, "targetId": host, "version": pending.Version, "config": newDraft}, 409)
			b.call("POST", "/jobs/"+job+"/recover", map[string]any{}, 200)
		}
		if replay := dataOf(t, b.call("POST", endpoint, input, 200)); replay["jobId"] != job {
			t.Fatal("duplicate lifecycle action created work")
		}
		i, err = s.readInstance(id)
		if err != nil || i.PendingJob != nil {
			t.Fatal(err)
		}
		var observation deploy.Result
		if err = json.Unmarshal(i.Observation, &observation); err != nil || observation.RuntimePolicy == nil || observation.RuntimePolicy.Held != (n < 2) {
			t.Fatal("held state lost", string(i.Observation), err)
		}
		if n > 0 && (observation.RuntimePolicy.Configured.Restart != "on-failure" || observation.RuntimePolicy.Configured.MaxRetries != 3) {
			t.Fatal("configured policy was not restored")
		}
		var locks int
		if err = s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks); err != nil || locks != 0 {
			t.Fatal("confirmed lifecycle lock retained", locks, err)
		}
		var current string
		if err = s.DB.QueryRow("SELECT snapshot_json FROM releases WHERE id=?", releaseID).Scan(&current); err != nil || current != frozen || i.Current != releaseID || i.Generation != "0" {
			t.Fatal("lifecycle rewrote historical release", err)
		}
	}
	if calls != 3 {
		t.Fatal("lifecycle replayed a command", calls)
	}
}
