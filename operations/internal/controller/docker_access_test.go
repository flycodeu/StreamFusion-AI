package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type dockerAccessFixture struct {
	mu                          sync.Mutex
	observation                 deploy.DockerAccessObservation
	results                     map[string]deploy.Result
	mutations                   atomic.Int32
	checks                      atomic.Int32
	old, unknown, noAck, forged atomic.Bool
}

func newDockerAccessFixture(t *testing.T) (*Store, *browser, string, string, *dockerAccessFixture) {
	t.Helper()
	s, b := setup(t)
	b.login()
	f := &dockerAccessFixture{results: map[string]deploy.Result{}}
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { body, _ := json.Marshal(v); return body, true }
		if req.Action == "CAPABILITIES" {
			cap := 1
			if f.old.Load() {
				cap = 0
			}
			return encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "platform": "linux/amd64", "dockerEngineAvailable": true, "softwareLifecycleVersion": 1, "nativeInstallVersion": 1, "serviceInstallVersion": serviceinstall.CapabilityVersion, "dockerAccessVersion": cap})
		}
		if req.Action == "SOFTWARE_DOCKER_ACCESS_CHECK" {
			f.checks.Add(1)
			f.mu.Lock()
			defer f.mu.Unlock()
			return encode(f.observation)
		}
		if req.DockerAccess == nil {
			return nil, false
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if req.Action == "SOFTWARE_DOCKER_ACCESS" {
			f.mutations.Add(1)
			member := req.DockerAccess.Action == "GRANT"
			f.observation.Accounts[0].Member = member
			f.observation.Accounts[0].Groups = []deploy.DockerAccessGroup{{Name: "ubuntu", GID: "1000"}}
			if member {
				f.observation.Accounts[0].Groups = append(f.observation.Accounts[0].Groups, deploy.DockerAccessGroup{Name: "docker", GID: "984"})
			}
			f.observation.ObservedAt = Now()
			f.observation.Fingerprint = deploy.FingerprintDockerAccess(f.observation)
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "DOCKER_ACCESS_UPDATED", ObservedAt: Now(), Software: &deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: "RUNNING", ObservedAt: Now()}, DockerAccess: &deploy.DockerAccessEvidence{Account: req.DockerAccess.Account, UID: "1000", Action: req.DockerAccess.Action, Member: member, Observation: f.observation, ObservedAt: Now()}}
			if f.forged.Load() {
				result.DockerAccess.UID = "1001"
			}
			f.results[req.JobID] = result
			if f.unknown.Load() {
				result.Status = "UNKNOWN"
			}
			return encode(result)
		}
		result := f.results[req.JobID]
		if req.Action == "ACK" && !f.noAck.Load() && req.ResultHash == deploy.Digest(result) {
			result.Ack = true
		}
		return encode(result)
	})
	var d serviceinstall.Definition
	for _, candidate := range serviceinstall.Catalog() {
		if candidate.Key == "docker" {
			d = candidate
			break
		}
	}
	config, _ := matrixInstallationConfig(t, b, d, "NATIVE")
	material := matrixProtocolMaterial(t, s, b, d, "NATIVE")
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": config, "source": map[string]string{"type": "ARCHIVE", "materialId": material.ID}}, 200))
	accepted := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "docker-before-access"}, 202))
	id, job := accepted["softwareInstanceId"].(string), accepted["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	i, err := s.readSoftware(id)
	if err != nil || !dockerAccessEligible(i) {
		t.Fatal("Docker installation fixture not ready", err, i.InstallationStatus)
	}
	f.observation = deploy.DockerAccessObservation{SoftwareID: id, EngineIdentityHash: i.IdentityHash, DockerGID: "984", UIDMin: 1000, UIDMax: 60000, Socket: deploy.DockerAccessSocket{Path: "/run/docker.sock", UID: "0", GID: "984", Mode: "660", Ready: true}, Accounts: []deploy.DockerAccessAccount{{Name: "ubuntu", UID: "1000", GID: "1000", Shell: "/bin/bash", Eligible: true, Groups: []deploy.DockerAccessGroup{{Name: "ubuntu", GID: "1000"}}}}, ObservedAt: Now()}
	f.observation.Fingerprint = deploy.FingerprintDockerAccess(f.observation)
	return s, b, host, id, f
}

func dockerAccessBody(t *testing.T, b *browser, id, action, key string) map[string]any {
	t.Helper()
	checked := dataOf(t, b.call("GET", "/software-instances/"+id+"/docker-access", nil, 200))
	o := checked["observation"].(map[string]any)
	return map[string]any{"expectedVersion": checked["expectedVersion"], "expectedHostVersion": checked["expectedHostVersion"], "action": "DOCKER_ACCESS", "confirmed": true, "idempotencyKey": key, "dockerAccess": map[string]any{"action": action, "account": "ubuntu", "expectedStateHash": o["fingerprint"]}}
}

func TestDockerAccessJobsRetainHistoryReconcileAndDoNotReplay(t *testing.T) {
	s, b, _, id, f := newDockerAccessFixture(t)
	path := "/software-instances/" + id + "/actions"
	input := dockerAccessBody(t, b, id, "GRANT", "docker-access-grant")
	if f.mutations.Load() != 0 {
		t.Fatal("GET changed groups")
	}
	input["confirmed"] = false
	if !strings.Contains(b.call("POST", path, input, 409).Body.String(), "DOCKER_ACCESS_CONFIRMATION_REQUIRED") {
		t.Fatal("root-level confirmation missing")
	}
	input["confirmed"] = true
	accepted := dataOf(t, b.call("POST", path, input, 202))
	job := accepted["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil || req.DockerAccess == nil {
		t.Fatal("missing frozen intent")
	}
	i, _ := s.readSoftware(id)
	if i.Version != req.DockerAccess.ExpectedSoftwareVersion || req.Software.Version != i.BindingRevision {
		t.Fatal("UI/binding versions conflated")
	}
	f.old.Store(true)
	if again := dataOf(t, b.call("POST", path, input, 200)); again["jobId"] != job {
		t.Fatal("replay depended on capability")
	}
	f.old.Store(false)
	f.unknown.Store(true)
	b.h.processSoftware(context.Background(), job, raw)
	var status string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "UNKNOWN" || locks != 2 || f.mutations.Load() != 1 {
		t.Fatal(status, locks, f.mutations.Load())
	}
	f.unknown.Store(false)
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "SUCCEEDED" || locks != 0 || f.mutations.Load() != 1 {
		t.Fatal("reconcile replayed or did not ACK", status, locks, f.mutations.Load())
	}
	revoke := dockerAccessBody(t, b, id, "REVOKE", "docker-access-revoke")
	second := dataOf(t, b.call("POST", path, revoke, 202))["jobId"].(string)
	var secondRaw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", second).Scan(&secondRaw)
	f.noAck.Store(true)
	b.h.processSoftware(context.Background(), second, secondRaw)
	var pending bool
	s.DB.QueryRow("SELECT target_ack_pending FROM jobs WHERE id=?", second).Scan(&pending)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", second).Scan(&locks)
	if !pending || locks != 2 {
		t.Fatal("ACK boundary lost")
	}
	f.noAck.Store(false)
	b.call("POST", "/jobs/"+second+"/reconcile", map[string]any{}, 200)
	var original string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&original)
	if original != raw || f.mutations.Load() != 2 {
		t.Fatal("history changed or repeated mutation")
	}
	var audits int
	s.DB.QueryRow("SELECT count(*) FROM audit WHERE action LIKE 'SOFTWARE_DOCKER_ACCESS%'").Scan(&audits)
	if audits < 4 {
		t.Fatal("missing audit", audits)
	}
}

func TestDockerAccessSubmissionAndExecutionGuards(t *testing.T) {
	for _, scenario := range []string{"actor-revoked", "host-changed", "software-changed", "forged-result", "stale-state", "unconfirmed", "old-executor", "external", "container", "primary-group", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			s, b, host, id, f := newDockerAccessFixture(t)
			input := dockerAccessBody(t, b, id, "GRANT", "guard-"+scenario)
			path := "/software-instances/" + id + "/actions"
			switch scenario {
			case "stale-state":
				input["dockerAccess"].(map[string]any)["expectedStateHash"] = strings.Repeat("a", 64)
			case "unconfirmed":
				input["confirmed"] = false
			case "old-executor":
				f.old.Store(true)
			case "external":
				s.DB.Exec("UPDATE software_instances SET management_mode='OBSERVE' WHERE id=?", id)
			case "container":
				s.DB.Exec("UPDATE software_instances SET runtime_kind='DOCKER' WHERE id=?", id)
			case "primary-group":
				f.observation.Accounts[0].GID = "984"
				f.observation.Accounts[0].PrimaryGroup = true
				f.observation.Accounts[0].Member = true
				f.observation.Fingerprint = deploy.FingerprintDockerAccess(f.observation)
				input = dockerAccessBody(t, b, id, "REVOKE", "guard-primary")
			}
			if scenario == "stale-state" || scenario == "unconfirmed" || scenario == "old-executor" || scenario == "external" || scenario == "container" || scenario == "primary-group" {
				b.call("POST", path, input, 409)
				if f.mutations.Load() != 0 {
					t.Fatal("rejected request changed group")
				}
				return
			}
			accepted := dataOf(t, b.call("POST", path, input, 202))
			job := accepted["jobId"].(string)
			var raw string
			s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
			switch scenario {
			case "actor-revoked":
				s.DB.Exec("UPDATE admins SET session_generation=session_generation+1")
			case "host-changed":
				s.DB.Exec("UPDATE targets SET version=version+1 WHERE id=?", host)
			case "software-changed":
				s.DB.Exec("UPDATE software_instances SET version=version+1 WHERE id=?", id)
			case "forged-result":
				f.forged.Store(true)
			case "cancel":
				b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 200)
			}
			b.h.processSoftware(context.Background(), job, raw)
			var status string
			var locks int
			s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
			if scenario == "forged-result" {
				if status != "UNKNOWN" || locks != 2 {
					t.Fatal("forged receipt released locks", status, locks)
				}
			} else if status != "FAILED" || locks != 0 || f.mutations.Load() != 0 {
				t.Fatal("changed authority/version dispatched", status, locks, f.mutations.Load())
			}
		})
	}
}

func TestDockerAccessDiagnosticsDoNotExposeRemoteOutput(t *testing.T) {
	for code := range dockerAccessMessages {
		if got := classifyExecutorInvocation(errors.New("ssh exit 1"), "\n"+code+"\n"); got.Error() != code {
			t.Fatal(code, got)
		}
		if got := classifyExecutorInvocation(errors.New("ssh exit 1"), code+"\nprivate-account-output"); got.Error() == code || strings.Contains(got.Error(), "private-account-output") {
			t.Fatal("untrusted stderr passed through", got)
		}
	}
}

func TestDockerAccessOmittedFieldsPreserveLegacyJSON(t *testing.T) {
	// Legacy envelopes omit the new field and retain their serialized bytes and
	// receipt hashes through strict parsing and replay.
	for _, envelope := range []any{deploy.Request{Action: "SOFTWARE_STOP", JobID: "legacy-job"}, deploy.Result{Status: "SUCCEEDED", JobID: "legacy-job"}} {
		body, err := json.Marshal(envelope)
		if err != nil || strings.Contains(string(body), "dockerAccess") {
			t.Fatal("legacy envelope acquired an optional field", err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if _, present := fields["dockerAccess"]; present {
			t.Fatal("legacy envelope acquired Docker permission semantics")
		}
		// Strict parsing must retain every prior field and byte order on replay.
		switch envelope.(type) {
		case deploy.Request:
			var replay deploy.Request
			if deploy.StrictJSON(body, &replay) != nil || jsonText(replay) != string(body) || deploy.Digest(replay) != deploy.Digest(envelope) {
				t.Fatal("legacy request digest changed")
			}
		case deploy.Result:
			var replay deploy.Result
			if deploy.StrictJSON(body, &replay) != nil || jsonText(replay) != string(body) || deploy.Digest(replay) != deploy.Digest(envelope) {
				t.Fatal("legacy receipt digest changed")
			}
		}
	}
}
