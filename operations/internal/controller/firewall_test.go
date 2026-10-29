package controller

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// Controlled SSH receipts validate HTTP, durable plans/jobs and loss recovery.
// This fixture never changes a Linux firewall or proves packet reachability.
func TestFirewallPlanJobReplayConfirmAndEvidenceGuards(t *testing.T) {
	s, b := setup(t)
	b.login()
	var mu sync.Mutex
	calls := 0
	oldExecutor := false
	snapshot := deploy.FirewallSnapshot{Status: "INACTIVE", Hash: strings.Repeat("a", 64), RulesHash: strings.Repeat("b", 64), ObservedAt: Now(), Ports: []deploy.HostPort{}, ManagedRules: []deploy.FirewallRule{}, ManagementSource: "192.0.2.1/32", ManagementPort: 22, ProtectedPorts: []int{22}}
	results := map[string]deploy.Result{}
	host := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		mu.Lock()
		defer mu.Unlock()
		encode := func(v any) ([]byte, bool) { raw, _ := json.Marshal(v); return raw, true }
		switch req.Action {
		case "CAPABILITIES":
			version := deploy.FirewallCapabilityVersion
			if oldExecutor {
				version = 1
			}
			return encode(executorCapabilities{Owner: deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, FirewallVersion: version})
		case "FIREWALL_OBSERVE":
			return encode(snapshot)
		case "FIREWALL_CHECK":
			return encode(map[string]any{"snapshot": snapshot, "code": ""})
		case "FIREWALL_CHANGE":
			calls++
			after := snapshot
			after.Status = "ACTIVE"
			after.Hash = strings.Repeat("c", 64)
			r := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "UNKNOWN", Phase: "FIREWALL_AWAITING_CONFIRM", FirewallConfirmBy: time.Now().Add(time.Minute).UTC().Format(time.RFC3339), Firewall: &after}
			results[req.JobID] = r
			return encode(r)
		case "FIREWALL_CONFIRM":
			r := results[req.JobID]
			r.Status, r.Phase = "SUCCEEDED", "FIREWALL_APPLIED"
			results[req.JobID] = r
			return encode(r)
		case "ACK":
			r := results[req.JobID]
			r.Ack = req.ResultHash == deploy.Digest(r)
			return encode(r)
		case "STATUS":
			return encode(results[req.JobID])
		}
		return nil, false
	})
	b.call("POST", "/hosts/"+host+"/ports-observe", map[string]string{"expectedVersion": "1"}, 200)
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/firewall-plans", map[string]any{"expectedVersion": "1", "snapshotHash": snapshot.Hash, "operation": "ENABLE", "rule": deploy.FirewallRule{}}, 200))
	path := "/firewall-plans/" + plan["id"].(string) + "/submit"
	body := map[string]any{"idempotencyKey": "firewall-enable-once", "confirmed": true}
	accepted := dataOf(t, b.call("POST", path, body, 202))
	job := accepted["jobId"].(string)
	var raw string
	if e := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	b.h.processFirewall(context.Background(), job, raw)
	var status, phase string
	var locks int
	s.DB.QueryRow("SELECT status,phase FROM jobs WHERE id=?", job).Scan(&status, &phase)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "UNKNOWN" || phase != "FIREWALL_AWAITING_CONFIRM" || locks != 1 {
		t.Fatal(status, phase, locks)
	}
	if _, e := s.DB.Exec("UPDATE firewall_plans SET expires_at='2000-01-01T00:00:00Z' WHERE id=?", plan["id"]); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec("UPDATE targets SET maintenance=1 WHERE id=?", host); e != nil {
		t.Fatal(e)
	}
	if dataOf(t, b.call("POST", path, body, 200))["jobId"] != job {
		t.Fatal("expired plan or maintenance prevented finding accepted job")
	}
	if _, e := s.DB.Exec("UPDATE jobs SET phase='TARGET_UNCONFIRMED',result_json='{}' WHERE id=?", job); e != nil {
		t.Fatal(e)
	}
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	s.DB.QueryRow("SELECT phase FROM jobs WHERE id=?", job).Scan(&phase)
	if phase != "FIREWALL_AWAITING_CONFIRM" {
		t.Fatal("lost initial response did not restore confirmation action", phase)
	}
	b.call("POST", "/jobs/"+job+"/firewall-confirm", map[string]any{}, 200)
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "SUCCEEDED" || locks != 0 || calls != 1 {
		t.Fatal(status, locks, calls)
	}
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil {
		t.Fatal("input damaged")
	}
	bad := results[job]
	late := bad
	late.Status, late.Phase = "UNKNOWN", "FIREWALL_UNCONFIRMED"
	if e := b.h.recordPendingFirewallResult(job, req, late); e != nil {
		t.Fatal(e)
	}
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	if status != "SUCCEEDED" {
		t.Fatal("late pending response overwrote terminal result")
	}
	bad.Firewall = &deploy.FirewallSnapshot{Status: "INACTIVE", Hash: strings.Repeat("d", 64)}
	if b.h.commitFirewallResult(job, req, bad) == nil {
		t.Fatal("wrong enable evidence accepted")
	}
	if !strings.Contains(b.call("GET", "/jobs/"+job+"/logs", nil, 200).Body.String(), "FIREWALL_APPLIED") {
		t.Fatal("job receipt missing")
	}
	mu.Lock()
	oldExecutor = true
	mu.Unlock()
	s.DB.Exec("UPDATE targets SET maintenance=0 WHERE id=?", host)
	b.call("POST", "/hosts/"+host+"/ports-observe", map[string]string{"expectedVersion": "1"}, 200)
	blocked := b.call("POST", "/hosts/"+host+"/firewall-plans", map[string]any{"expectedVersion": "1", "snapshotHash": snapshot.Hash, "operation": "ENABLE"}, 409)
	if !strings.Contains(blocked.Body.String(), "FIREWALL_EXECUTOR_UPGRADE_REQUIRED") {
		t.Fatal("unsafe older firewall executor was accepted")
	}
	var schema int
	s.DB.QueryRow("pragma user_version").Scan(&schema)
	if schema != SchemaVersion {
		t.Fatal("missing current schema")
	}
	rows, e := s.DB.Query("pragma foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("broken migration relations")
	}
}
