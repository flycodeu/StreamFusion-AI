package controller

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// A real SSH transport and durable controller prove mutual exclusion and the
// separation of software configuration from firewall state, not Linux packets.
func TestFirewallAndSoftwareConfigurationShareHostResponsibility(t *testing.T) {
	var mu sync.Mutex
	snap := deploy.FirewallSnapshot{Status: "INACTIVE", Hash: strings.Repeat("a", 64), RulesHash: strings.Repeat("b", 64), ManagementSource: "192.0.2.1/32", ManagementPort: 22, ObservedAt: Now(), Ports: []deploy.HostPort{}, ManagedRules: []deploy.FirewallRule{}}
	results := map[string]deploy.Result{}
	firewallCalls := 0
	s, b, host, id, software := readyNginxFixture(t, func(req deploy.Request) ([]byte, bool) {
		mu.Lock()
		defer mu.Unlock()
		encode := func(v any) ([]byte, bool) { data, _ := json.Marshal(v); return data, true }
		if req.Action == "CAPABILITIES" {
			return encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "platform": "linux/amd64", "firewallVersion": deploy.FirewallCapabilityVersion, "softwareConfigureVersion": 1, "softwareLifecycleVersion": 1, "serviceInstallVersion": serviceinstall.CapabilityVersion, "serviceCatalogHash": serviceinstall.CatalogHash(), "dockerEngineAvailable": true})
		}
		if req.Action == "FIREWALL_OBSERVE" {
			return encode(snap)
		}
		if req.Firewall == nil {
			return nil, false
		}
		switch req.Action {
		case "FIREWALL_CHECK":
			return encode(map[string]any{"snapshot": snap, "code": ""})
		case "FIREWALL_CHANGE":
			firewallCalls++
			snap.Status, snap.Hash = "ACTIVE", strings.Repeat("c", 64)
			after := snap
			r := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "UNKNOWN", Phase: "FIREWALL_AWAITING_CONFIRM", Firewall: &after, FirewallConfirmBy: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
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
		}
		return nil, false
	})
	before, _ := s.readSoftware(id)
	next := before.Installation.Config
	next.HostPort = 19288
	config := dataOf(t, b.call("POST", "/software-instances/"+id+"/configuration-plans", map[string]any{"expectedVersion": before.Version, "config": next}, 200))
	configPath := "/software-configuration-plans/" + config["id"].(string) + "/submit"
	b.call("POST", "/hosts/"+host+"/ports-observe", map[string]string{"expectedVersion": "1"}, 200)
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/firewall-plans", map[string]any{"expectedVersion": "1", "operation": "ENABLE", "snapshotHash": snap.Hash}, 200))
	accepted := dataOf(t, b.call("POST", "/firewall-plans/"+plan["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "firewall-before-config"}, 202))
	job := accepted["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processFirewall(context.Background(), job, raw)
	blocked := b.call("POST", configPath, map[string]string{"idempotencyKey": "config-after-firewall"}, 409)
	if !strings.Contains(blocked.Body.String(), "OPERATION_LOCKED") || software.ConfigurationCount.Load() != 0 {
		t.Fatal("pending firewall allowed configuration execution", blocked.Body.String())
	}
	b.call("POST", "/jobs/"+job+"/firewall-confirm", map[string]any{}, 200)
	confirmed := dataOf(t, b.call("GET", "/hosts/"+host+"/ports", nil, 200))
	if confirmed["freshness"] != "FRESH" {
		t.Fatal("persisted firewall snapshot was not marked fresh", confirmed)
	}
	configJob := dataOf(t, b.call("POST", configPath, map[string]string{"idempotencyKey": "config-after-firewall"}, 202))["jobId"].(string)
	plan2 := dataOf(t, b.call("POST", "/hosts/"+host+"/firewall-plans", map[string]any{"expectedVersion": "1", "operation": "DISABLE", "snapshotHash": snap.Hash}, 200))
	blocked = b.call("POST", "/firewall-plans/"+plan2["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "firewall-after-config"}, 409)
	if !strings.Contains(blocked.Body.String(), "OPERATION_LOCKED") {
		t.Fatal("queued configuration allowed firewall execution", blocked.Body.String())
	}
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", configJob).Scan(&raw)
	b.h.processSoftware(context.Background(), configJob, raw)
	after, _ := s.readSoftware(id)
	if after.Installation.Config.HostPort != 19288 || after.InstallationStatus != "READY" || software.ConfigurationCount.Load() != 1 || firewallCalls != 1 {
		t.Fatal("configuration did not remain independent", after.InstallationStatus, firewallCalls)
	}
	current := dataOf(t, b.call("GET", "/hosts/"+host+"/ports", nil, 200))
	if jsonText(current["snapshot"]) != jsonText(confirmed["snapshot"]) {
		t.Fatal("software port change silently changed firewall snapshot")
	}
}
