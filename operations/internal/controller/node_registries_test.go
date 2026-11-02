package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func nodeRegistryObservationFixture(url string) *deploy.NodeRegistryObservation {
	if url == "" {
		return &deploy.NodeRegistryObservation{Status: "DEFAULT", URL: "https://registry.npmjs.org/", ConfigPath: "/home/ubuntu/.npmrc"}
	}
	return &deploy.NodeRegistryObservation{Status: "CONFIGURED", URL: url, ConfigPath: "/home/ubuntu/.npmrc", ConfigSHA256: strings.Repeat("c", 64), Mode: 0600, Exists: true}
}
func createRegistry(t *testing.T, b *browser, url string) nodeRegistry {
	t.Helper()
	var response struct{ Data nodeRegistry }
	if err := json.Unmarshal(b.call("POST", "/node-registries", map[string]string{"name": "private registry", "url": url}, 201).Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}
func installedRegistryFixture(t *testing.T) (*Store, *browser, string, string, *nodeControllerFixture) {
	t.Helper()
	s, b, host, id, f := initializedTerminalFixture(t)
	env, _ := s.readNodeEnvironment(id)
	d := serviceinstall.NodeCatalog()[0]
	input := map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "npm registry fixture", RuntimeMode: "NATIVE", MemoryMiB: 512}, "source": map[string]string{"type": "OFFICIAL"}, "node": nodeInstallInput{EnvironmentID: id, EnvironmentVersion: env.Version, MakeDefault: true}}
	p := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", input, 200))
	accepted := dataOf(t, b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "registry-node-install"}, 202))
	var raw string
	job := accepted["jobId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	return s, b, host, id, f
}
func registryPlan(t *testing.T, s *Store, b *browser, host, id string, registry nodeRegistry) map[string]any {
	t.Helper()
	env, err := s.readNodeEnvironment(id)
	if err != nil {
		t.Fatal(err)
	}
	return nodePlan(t, b, host, map[string]any{"action": "SET_NPM_REGISTRY", "environmentId": id, "expectedEnvironmentVersion": env.Version, "registryId": registry.ID, "expectedRegistryVersion": registry.Version})
}

func TestNodeRegistriesRequireAuthenticationVersionsAndSafeURLs(t *testing.T) {
	s, b := setup(t)
	b.call("GET", "/node-registries", nil, 401)
	b.login()
	var listed struct{ Data []nodeRegistry }
	json.Unmarshal(b.call("GET", "/node-registries", nil, 200).Body.Bytes(), &listed)
	if len(listed.Data) != 2 || !listed.Data[0].BuiltIn || listed.Data[0].URL != "https://registry.npmjs.org/" {
		t.Fatal(listed.Data)
	}
	fixed := listed.Data[0]
	b.call("DELETE", "/node-registries/"+fixed.ID, map[string]string{"expectedVersion": fixed.Version}, 409)
	b.call("PATCH", "/node-registries/"+fixed.ID, map[string]string{"expectedVersion": fixed.Version, "name": "override", "url": "https://elsewhere.example/"}, 409)
	csrf := b.csrf
	b.csrf = "invalid"
	b.call("POST", "/node-registries", map[string]string{"name": "blocked", "url": "https://blocked.example/"}, 403)
	b.csrf = csrf
	for _, url := range []string{"https://user:opaque-secret@example.com/", "https://example.com/npm?token=opaque-secret", "https://example.com/npm#opaque-secret", "file:///etc/passwd", "https://example.com/\nregistry=secret", "https://example.com/${TOKEN}/"} {
		body := b.call("POST", "/node-registries", map[string]string{"name": "invalid", "url": url}, 400).Body.String()
		if strings.Contains(body, "opaque-secret") {
			t.Fatal("URL secret echoed")
		}
	}
	custom := createRegistry(t, b, "http://127.0.0.1:4873/repository/npm/")
	b.call("POST", "/node-registries", map[string]string{"name": "duplicate", "url": custom.URL}, 409)
	changed := dataOf(t, b.call("PATCH", "/node-registries/"+custom.ID, map[string]string{"expectedVersion": custom.Version, "name": "internal mirror", "url": "http://10.0.0.2:8081/repository/npm/"}, 200))
	if changed["version"] != "2" {
		t.Fatal(changed)
	}
	b.call("DELETE", "/node-registries/"+custom.ID, map[string]string{"expectedVersion": "1"}, 409)
	b.call("DELETE", "/node-registries/"+custom.ID, map[string]string{"expectedVersion": "2"}, 200)
	for n := 0; n < 18; n++ {
		createRegistry(t, b, fmt.Sprintf("https://mirror-%d.example/npm/", n))
	}
	b.call("POST", "/node-registries", map[string]string{"name": "full", "url": "https://overflow.example/"}, 409)
	var audits int
	s.DB.QueryRow("SELECT count(*) FROM audit WHERE action LIKE 'NODE_REGISTRY_%'").Scan(&audits)
	if audits != 21 {
		t.Fatal("missing registry audits", audits)
	}
}

func TestNodeRegistryPlanRejectsListAndAccountConfigurationDrift(t *testing.T) {
	for _, scenario := range []string{"rename", "url", "delete", "npmrc", "old-executor"} {
		t.Run(scenario, func(t *testing.T) {
			s, b, host, id, f := installedRegistryFixture(t)
			registry := createRegistry(t, b, "http://10.0.0.2:4873/npm/")
			p := registryPlan(t, s, b, host, id, registry)
			switch scenario {
			case "rename":
				b.call("PATCH", "/node-registries/"+registry.ID, map[string]string{"expectedVersion": "1", "name": "renamed", "url": registry.URL}, 200)
			case "url":
				b.call("PATCH", "/node-registries/"+registry.ID, map[string]string{"expectedVersion": "1", "name": registry.Name, "url": "https://different.example/"}, 200)
			case "delete":
				b.call("DELETE", "/node-registries/"+registry.ID, map[string]string{"expectedVersion": "1"}, 200)
			case "npmrc":
				f.registryDrift.Store(true)
			case "old-executor":
				f.noRegistry.Store(true)
			}
			body := b.call("POST", "/node-environment-plans/"+p["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "registry-drift-" + scenario}, 409).Body.String()
			code := "NODE_REGISTRY_CHANGED"
			if scenario == "npmrc" {
				code = "NODE_PREFLIGHT_MISMATCH"
			}
			if scenario == "old-executor" {
				code = "NODE_REGISTRY_CAPABILITY_REQUIRED"
			}
			if !strings.Contains(body, code) || f.mutations.Load() != 2 {
				t.Fatal(body, f.mutations.Load())
			}
		})
	}
}

func TestNodeRegistryAcceptedSnapshotSurvivesPresetDeletionAndUnknown(t *testing.T) {
	s, b, host, id, f := installedRegistryFixture(t)
	registry := createRegistry(t, b, "http://10.0.0.2:4873/npm/")
	p := registryPlan(t, s, b, host, id, registry)
	input := map[string]any{"confirmed": true, "idempotencyKey": "registry-accepted-once"}
	accepted := dataOf(t, b.call("POST", "/node-environment-plans/"+p["id"].(string)+"/submit", input, 202))
	job := accepted["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	var request deploy.Request
	if deploy.StrictJSON([]byte(raw), &request) != nil || request.NodeEnvironment.RegistryURL != registry.URL {
		t.Fatal("URL not frozen")
	}
	b.call("DELETE", "/node-registries/"+registry.ID, map[string]string{"expectedVersion": "1"}, 200)
	f.noRegistry.Store(true)
	again := dataOf(t, b.call("POST", "/node-environment-plans/"+p["id"].(string)+"/submit", input, 200))
	if again["jobId"] != job {
		t.Fatal("replay performed new checks")
	}
	f.noRegistry.Store(false)
	f.unknown.Store(true)
	b.h.processNodeEnvironment(context.Background(), job, raw)
	var state string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&state)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if state != "UNKNOWN" || locks != 2 || f.mutations.Load() != 3 {
		t.Fatal(state, locks, f.mutations.Load())
	}
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	env, _ := s.readNodeEnvironment(id)
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&state)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if state != "SUCCEEDED" || locks != 0 || f.mutations.Load() != 3 || env.Observation.Registry == nil || env.Observation.Registry.URL != registry.URL {
		t.Fatal("frozen URL lost or command replayed", state, locks)
	}
	var original string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&original)
	if original != raw {
		t.Fatal("job snapshot rewritten")
	}
}

func TestNodeRegistrySameAccountEnvironmentsShareOperationLock(t *testing.T) {
	s, b, host, id, f := installedRegistryFixture(t)
	first, _ := s.readNodeEnvironment(id)
	second := first
	second.ID = uuid.NewString()
	second.Name = "same account other environment"
	second.RootPath = "/home/ubuntu/.local/share/streamfusion-ops/node/" + second.ID
	second.Observation = first.Observation
	second.Observation.EnvironmentID = second.ID
	second.Observation.RootPath = second.RootPath
	second.Observation.Versions = append([]deploy.NodeVersionObservation(nil), first.Observation.Versions...)
	second.Observation.Versions[0].BinaryPath = serviceinstall.NodeVersionRoot(second.context(), second.Observation.Versions[0].Version) + "/bin/node"
	second.Observation.Fingerprint = nodeTestFingerprint(second.Observation)
	_, err := s.DB.Exec("INSERT INTO node_environments(id,host_id,name,account,account_uid,root_path,manager_type,management_mode,nvm_version,nvm_hash,observed_default_version,observation_json,created_at,updated_at) VALUES(?,?,?,?,?,?,'NVM','MANAGED',?,?,?,?,?,?)", second.ID, host, second.Name, second.Account, second.AccountUID, second.RootPath, second.NVMVersion, second.NVMHash, second.ObservedDefaultVersion, jsonText(second.Observation), Now(), Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec("INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,installation_status,node_environment_id,created_at,updated_at) VALUES(?,'sibling Node',?,'NODE',?,'TOOL',?,'MANAGED','READY',?,?,?)", uuid.NewString(), host, second.ObservedDefaultVersion, second.Observation.Versions[0].BinaryPath, second.ID, Now(), Now())
	if err != nil {
		t.Fatal(err)
	}
	registry := createRegistry(t, b, "https://registry.example/npm/")
	p := registryPlan(t, s, b, host, id, registry)
	f.mu.Lock()
	f.observation = second.Observation
	f.mu.Unlock()
	other := registryPlan(t, s, b, host, second.ID, registry)
	f.mu.Lock()
	f.observation = first.Observation
	f.mu.Unlock()
	accepted := dataOf(t, b.call("POST", "/node-environment-plans/"+p["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "account-registry-first"}, 202))
	job := accepted["jobId"].(string)
	var locked int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=? AND resource_key=?", job, nodeRegistryAccountKey(host, first.AccountUID)).Scan(&locked)
	if locked != 1 {
		t.Fatal("account lock missing")
	}
	// Isolate the account guard from the existing, broader target lock.
	s.DB.Exec("DELETE FROM operation_locks WHERE job_id=? AND resource_key=?", job, "target:"+host)
	f.mu.Lock()
	f.observation = second.Observation
	f.mu.Unlock()
	response := b.call("POST", "/node-environment-plans/"+other["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "account-registry-second"}, 409)
	if !strings.Contains(response.Body.String(), "OPERATION_LOCKED") || f.mutations.Load() != 2 {
		t.Fatal(response.Body.String())
	}
}

func TestNodeRegistryWorkerRechecksActorIdentityAndLock(t *testing.T) {
	for _, scenario := range []string{"actor", "host", "environment", "lock"} {
		t.Run(scenario, func(t *testing.T) {
			s, b, host, id, f := installedRegistryFixture(t)
			registry := createRegistry(t, b, "https://registry.example/npm/")
			p := registryPlan(t, s, b, host, id, registry)
			accepted := dataOf(t, b.call("POST", "/node-environment-plans/"+p["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "registry-gate-" + scenario}, 202))
			job := accepted["jobId"].(string)
			var raw string
			s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
			var err error
			code := ""
			switch scenario {
			case "actor":
				_, err = s.DB.Exec("UPDATE admins SET session_generation=session_generation+1")
				code = "NODE_REGISTRY_ACTOR_CHANGED"
			case "host":
				_, err = s.DB.Exec("UPDATE targets SET maintenance=1 WHERE id=?", host)
				code = "HOST_CHANGED"
			case "environment":
				_, err = s.DB.Exec("UPDATE node_environments SET version=version+1 WHERE id=?", id)
				code = "NODE_ENVIRONMENT_CHANGED"
			case "lock":
				_, err = s.DB.Exec("DELETE FROM operation_locks WHERE job_id=? AND resource_key LIKE 'account:%'", job)
				code = "NODE_REGISTRY_LOCK_MISMATCH"
			}
			if err != nil {
				t.Fatal(err)
			}
			b.h.processNodeEnvironment(context.Background(), job, raw)
			var status, phase, got string
			var locks int
			s.DB.QueryRow("SELECT status,phase,error_code FROM jobs WHERE id=?", job).Scan(&status, &phase, &got)
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
			if status != "FAILED" || phase != "NOT_STARTED" || got != code || locks != 0 || f.mutations.Load() != 2 {
				t.Fatal(status, phase, got, locks, f.mutations.Load())
			}
		})
	}
}

func TestNodeRegistryForgedActualURLDoesNotBecomeConfirmed(t *testing.T) {
	s, b, host, id, f := installedRegistryFixture(t)
	registry := createRegistry(t, b, "https://registry.example/npm/")
	p := registryPlan(t, s, b, host, id, registry)
	accepted := dataOf(t, b.call("POST", "/node-environment-plans/"+p["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "registry-forged-result"}, 202))
	job := accepted["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	f.wrongRegistry.Store(true)
	b.h.processNodeEnvironment(context.Background(), job, raw)
	var state string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&state)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	env, _ := s.readNodeEnvironment(id)
	if state != "UNKNOWN" || locks != 2 || env.Observation.Registry != nil {
		t.Fatal("unverified registry committed", state, locks)
	}
}

func TestNodeRegistryOptionalFieldsPreserveLegacyRequestsAndCapabilities(t *testing.T) {
	s, b, host, id, f := installedRegistryFixture(t)
	env, err := s.readNodeEnvironment(id)
	if err != nil {
		t.Fatal(err)
	}
	c := deploy.NodeEnvironmentRequest{Context: env.context(), Action: "SET_DEFAULT", NodeVersion: env.ObservedDefaultVersion, ExpectedHash: env.Observation.Fingerprint}
	legacy := jsonText(c)
	if strings.Contains(legacy, "observeRegistry") || strings.Contains(legacy, "registryUrl") || strings.Contains(jsonText(env.Observation), "\"registry\"") {
		t.Fatal("new optional registry data leaked into legacy snapshot")
	}
	var roundtrip deploy.NodeEnvironmentRequest
	if deploy.StrictJSON([]byte(legacy), &roundtrip) != nil || jsonText(roundtrip) != legacy || deploy.Digest(c) != deploy.Digest(roundtrip) {
		t.Fatal("legacy request digest changed")
	}
	f.noRegistry.Store(true)
	target, err := s.readTarget(host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.h.nodePreflight(context.Background(), target, c); err != nil {
		t.Fatal("old operation required registry capability", err)
	}
	c.Context.ObserveRegistry = true
	if _, err := b.h.nodePreflight(context.Background(), target, c); err == nil || err.Error() != "NODE_REGISTRY_CAPABILITY_REQUIRED" {
		t.Fatal("new snapshot bypassed capability", err)
	}
}
