package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestNodeSchemaFifteenPreservesHistoryAndLocks(t *testing.T) {
	f := newSchemaTenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		v int
		s string
	}{{11, schemaV11}, {12, schemaV12}, {13, schemaV13}, {14, schemaV14}} {
		if _, err = db.Exec(m.s); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", m.v, hashToken(m.s), "historical", Now()); err != nil {
			t.Fatal(err)
		}
	}
	before := schemaTenRows(t, db)
	db.Close()
	s, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for key, value := range before {
		if schemaTenRows(t, s.DB)[key] != value {
			t.Fatal("historical bytes changed", key)
		}
	}
	var version, relationships int
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	if err = s.DB.QueryRow("SELECT count(*) FROM software_instances WHERE node_environment_id IS NOT NULL").Scan(&relationships); err != nil || relationships != 0 {
		t.Fatal("legacy software gained a Node relationship", err)
	}
	backups, err := filepath.Glob(filepath.Join(f.dir, "before-schema-15-*.tar.gz"))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	assertSchemaElevenForeignKeys(t, s.DB)
}

type nodeControllerFixture struct {
	mu              sync.Mutex
	observation     deploy.NodeEnvironmentObservation
	results         map[string]deploy.Result
	mutations       atomic.Int32
	unknown         atomic.Bool
	noCapability    atomic.Bool
	forged          atomic.Bool
	noTerminal      atomic.Bool
	terminalDrift   atomic.Bool
	omitTerminal    atomic.Bool
	terminalRemains atomic.Bool
}

func nodeFixture(t *testing.T, s *Store, b *browser) (string, *nodeControllerFixture) {
	f := &nodeControllerFixture{results: map[string]deploy.Result{}}
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { raw, _ := json.Marshal(v); return raw, true }
		if req.Action == "CAPABILITIES" {
			cap := 1
			terminal := 1
			if f.noTerminal.Load() {
				terminal = 0
			}
			if f.noCapability.Load() {
				cap = 0
			}
			return encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "platform": "linux/amd64", "nodeEnvironmentVersion": cap, "nodeTerminalVersion": terminal, "nativeInstallVersion": 1, "softwareLifecycleVersion": 1, "softwareUninstallVersion": 1, "serviceInstallVersion": serviceinstall.CapabilityVersion})
		}
		if req.NodeEnvironment == nil && (req.Installation == nil || req.Installation.Node == nil) && (req.Uninstallation == nil || req.Uninstallation.Installation.Node == nil) {
			return nil, false
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if req.Action == "NODE_ENVIRONMENT_CHECK" {
			c := req.NodeEnvironment.Context
			if c.AccountUID == "" {
				c.AccountUID = "1000"
				c.RootPath = "/home/ubuntu/.local/share/streamfusion-ops/node/" + c.EnvironmentID
			}
			o := f.observation
			if o.EnvironmentID == "" {
				o = deploy.NodeEnvironmentObservation{EnvironmentID: c.EnvironmentID, Account: c.Account, AccountUID: c.AccountUID, RootPath: c.RootPath, ManagerType: "NVM", ObservedAt: Now(), Versions: []deploy.NodeVersionObservation{}}
			}
			if req.NodeEnvironment.Action == "CONNECT_TERMINAL" {
				o.Terminal = nodeTerminalFixture(false)
				if f.terminalDrift.Load() {
					o.Terminal.Profiles[0].SHA256 = strings.Repeat("d", 64)
				}
			}
			o.Fingerprint = nodeTestFingerprint(o)
			var removed []string
			if req.NodeEnvironment.Action == "REMOVE_ENVIRONMENT" && req.NodeEnvironment.DeleteData {
				removed = []string{c.RootPath}
			}
			return encode(deploy.NodeEnvironmentPreflight{Ready: true, Context: c, Observation: &o, RemovedPaths: removed, Checks: []serviceinstall.Check{{Key: "NODE", Status: "PASS"}}, Commands: []serviceinstall.OfficialCommand{{Executable: "nvm", Args: []string{"alias", "default", req.NodeEnvironment.NodeVersion}}}})
		}
		if req.Action == "NODE_ENVIRONMENT_OBSERVE" || req.Action == "NODE_ENVIRONMENT_DISCOVER" {
			o := f.observation
			if req.Action == "NODE_ENVIRONMENT_OBSERVE" && req.NodeEnvironment.Context.ConnectTerminal && o.Terminal == nil {
				o.Terminal = nodeTerminalFixture(false)
			}
			o.EnvironmentID = req.NodeEnvironment.Context.EnvironmentID
			o.Account = req.NodeEnvironment.Context.Account
			if req.Action == "NODE_ENVIRONMENT_DISCOVER" {
				o.Exists = true
				o.AccountUID = "1000"
				o.RootPath = "/home/ubuntu/.nvm"
				o.ManagerType = "NVM"
				o.NVMVersion = serviceinstall.NodeNVMVersion
				o.NVMHash = serviceinstall.NodeNVMHash
				o.Fingerprint = ""
				o.ObservedAt = Now()
				o.Versions = []deploy.NodeVersionObservation{{Version: "v22.21.1", BinaryPath: o.RootPath + "/versions/node/v22.21.1/bin/node", BinaryHash: strings.Repeat("b", 64), NPMVersion: "10.9.4"}}
			}
			o.Fingerprint = nodeTestFingerprint(o)
			return encode(o)
		}
		if req.Action == "SOFTWARE_INSTALL_CHECK" {
			n := req.Installation.Node
			v := req.Installation.Definition.Version
			root := serviceinstall.NodeVersionRoot(*n, v)
			p := serviceinstall.Preflight{Ready: true, Root: root, BinaryDirectory: root + "/bin", DataDirectory: n.RootPath + "/cache/" + v, ConfigDirectory: n.RootPath + "/nvm/alias", LogsDirectory: n.RootPath + "/backups/" + v}
			if n.ConnectTerminal {
				p.NodeTerminalHash = deploy.Digest(nodeTerminalFixture(false))
				if f.terminalDrift.Load() {
					p.NodeTerminalHash = strings.Repeat("d", 64)
				}
			}
			return encode(p)
		}
		if req.Action == "ACK" || req.Action == "STATUS" {
			res := f.results[req.JobID]
			if req.Action == "ACK" && req.ResultHash == deploy.Digest(res) {
				res.Ack = true
			}
			return encode(res)
		}
		if req.Action == "PROGRESS" {
			return encode(deploy.Result{Phase: "NODE_EXECUTION"})
		}
		if req.Action == "NODE_ENVIRONMENT_CHANGE" || req.Action == "SOFTWARE_INSTALL" || req.Action == "SOFTWARE_UNINSTALL" {
			f.mutations.Add(1)
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "NODE_VERIFIED", ObservedAt: Now()}
			if c := req.NodeEnvironment; c != nil {
				n := c.Context
				switch c.Action {
				case "INITIALIZE":
					f.observation = deploy.NodeEnvironmentObservation{EnvironmentID: n.EnvironmentID, Account: n.Account, AccountUID: n.AccountUID, RootPath: n.RootPath, ManagerType: "NVM", NVMVersion: n.NVMVersion, NVMHash: n.NVMHash, Exists: true, Versions: []deploy.NodeVersionObservation{}}
				case "SET_DEFAULT":
					f.observation.DefaultVersion = c.NodeVersion
				case "CONNECT_TERMINAL":
					f.observation.Terminal = nodeTerminalFixture(true)
				case "REMOVE_ENVIRONMENT":
					f.observation.Exists = false
					f.observation.DefaultVersion = ""
					if c.Context.ConnectTerminal && !f.terminalRemains.Load() {
						f.observation.Terminal = nodeTerminalFixture(false)
					}
				case "UPDATE_NPM", "INSTALL_PNPM", "UPDATE_PNPM", "REMOVE_PNPM":
					for i, v := range f.observation.Versions {
						if v.Version == c.NodeVersion {
							if c.Action == "UPDATE_NPM" {
								f.observation.Versions[i].NPMVersion = c.PackageVersion
							} else {
								f.observation.Versions[i].PNPMVersion = c.PackageVersion
							}
						}
					}
				}
			}
			if i := req.Installation; i != nil {
				binary := serviceinstall.NodeVersionRoot(*i.Node, i.Definition.Version) + "/bin/node"
				f.observation.Versions = append(f.observation.Versions, deploy.NodeVersionObservation{Version: i.Definition.Version, BinaryPath: binary, BinaryHash: strings.Repeat("b", 64), NPMVersion: "10.9.4"})
				if i.Node.MakeDefault {
					f.observation.DefaultVersion = i.Definition.Version
				}
				if i.Node.ConnectTerminal {
					f.observation.Terminal = nodeTerminalFixture(true)
				}
				result.Software = &deploy.SoftwareObservation{Kind: "TOOL", Locator: binary, IdentityHash: strings.Repeat("a", 64), State: "INSTALLED", Version: i.Definition.Version, ObservedAt: Now()}
				result.Installation = &serviceinstall.Evidence{SoftwareID: i.SoftwareID, BindingRevision: 1, RecipeKey: "node", RecipeRevision: i.Definition.Revision, ConfigHash: deploy.Digest(i.Config), Kind: "TOOL", Locator: binary, IdentityHash: result.Software.IdentityHash, ObservedVersion: i.Definition.Version, BinaryPath: binary, BinaryHash: strings.Repeat("b", 64), Health: "READY", ObservedAt: Now()}
			}
			if u := req.Uninstallation; u != nil {
				keep := []deploy.NodeVersionObservation{}
				for _, v := range f.observation.Versions {
					if v.Version != u.Installation.Definition.Version {
						keep = append(keep, v)
					}
				}
				f.observation.Versions = keep
				if u.ClearNodeDefault {
					f.observation.DefaultVersion = ""
				}
				root := u.Installation.Node.RootPath
				v := u.Installation.Definition.Version
				result.Uninstallation = &deploy.SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: "TOOL", Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: u.DeleteData, DataRemoved: u.DeleteData, DataRoot: root + "/cache/" + v, ObservedAt: Now()}
				if u.DeleteData {
					result.Uninstallation.RemovedPaths = nodeVersionDataPaths(*u.Installation.Node, v)
				}
			}
			f.observation.ObservedAt = Now()
			if f.omitTerminal.Load() {
				f.observation.Terminal = nil
			}
			f.observation.Fingerprint = nodeTestFingerprint(f.observation)
			snapshot := f.observation
			result.NodeEnvironment = &snapshot
			if f.forged.Load() {
				result.NodeEnvironment.RootPath = "/"
			}
			f.results[req.JobID] = result
			if f.unknown.Load() {
				result.Status = "UNKNOWN"
			}
			return encode(result)
		}
		t.Errorf("unexpected Node invocation %s", req.Action)
		return nil, false
	})
	return host, f
}

func nodePlan(t *testing.T, b *browser, host string, in map[string]any) map[string]any {
	t.Helper()
	in["expectedHostVersion"] = "1"
	return dataOf(t, b.call("POST", "/hosts/"+host+"/node-environment-plans", in, 200))
}
func runNodePlan(t *testing.T, s *Store, b *browser, plan map[string]any, key string) map[string]any {
	t.Helper()
	accepted := dataOf(t, b.call("POST", "/node-environment-plans/"+plan["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": key}, 202))
	job := accepted["jobId"].(string)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processNodeEnvironment(context.Background(), job, raw)
	return accepted
}

func TestNodeEnvironmentLifecycleUsesExistingJobsAndRetainsHistory(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := nodeFixture(t, s, b)
	plan := nodePlan(t, b, host, map[string]any{"action": "INITIALIZE", "name": "build tools", "account": "ubuntu", "source": map[string]string{"type": "OFFICIAL"}})
	accepted := runNodePlan(t, s, b, plan, "node-initialize-once")
	id := accepted["environmentId"].(string)
	env, err := s.readNodeEnvironment(id)
	if err != nil || !env.Observation.Exists || env.ManagementMode != "MANAGED" {
		t.Fatal(env, err)
	}
	again := dataOf(t, b.call("POST", "/node-environment-plans/"+plan["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "node-initialize-once"}, 200))
	if again["jobId"] != accepted["jobId"] || f.mutations.Load() != 1 {
		t.Fatal("initialize replay changed job")
	}
	d := serviceinstall.NodeCatalog()[0]
	input := map[string]any{"expectedHostVersion": "1", "recipeKey": "node", "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "node fixture", RuntimeMode: "NATIVE", MemoryMiB: 512}, "source": map[string]string{"type": "OFFICIAL"}, "node": nodeInstallInput{EnvironmentID: id, EnvironmentVersion: env.Version, MakeDefault: true}}
	ip := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", input, 200))
	installed := dataOf(t, b.call("POST", "/software-install-plans/"+ip["id"].(string)+"/submit", map[string]string{"idempotencyKey": "node-install-once"}, 202))
	job := installed["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	software, err := s.readSoftware(installed["softwareInstanceId"].(string))
	if err != nil || software.InstallationStatus != "READY" || software.NodeEnvironmentID != id {
		t.Fatal(software, err)
	}
	env, _ = s.readNodeEnvironment(id)
	if env.DesiredDefaultSoftwareID != software.ID || env.ObservedDefaultVersion != d.Version {
		t.Fatal("default not committed", env)
	}
	// A stale plan context cannot silently follow a newly observed environment.
	if body := b.call("POST", "/hosts/"+host+"/software-install-plans", input, 409).Body.String(); !strings.Contains(body, "NODE_ENVIRONMENT_CHANGED") {
		t.Fatal(body)
	}
	npm := nodePlan(t, b, host, map[string]any{"action": "UPDATE_NPM", "environmentId": id, "expectedEnvironmentVersion": env.Version, "nodeVersion": d.Version, "packageVersion": "11.6.2", "source": map[string]string{"type": "OFFICIAL"}})
	runNodePlan(t, s, b, npm, "node-npm-once")
	env, _ = s.readNodeEnvironment(id)
	if env.Observation.Versions[0].NPMVersion != "11.6.2" {
		t.Fatal(env)
	}
	uninstall := map[string]any{"action": "UNINSTALL", "expectedVersion": software.Version, "idempotencyKey": "node-uninstall-once", "confirmed": true, "deleteData": true, "confirmationName": software.Name}
	if body := b.call("POST", "/software-instances/"+software.ID+"/actions", uninstall, 409).Body.String(); !strings.Contains(body, "NODE_CLEAR_DEFAULT_REQUIRED") {
		t.Fatal(body)
	}
	uninstall["clearNodeDefault"] = true
	removed := dataOf(t, b.call("POST", "/software-instances/"+software.ID+"/actions", uninstall, 202))
	job = removed["jobId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	software, err = s.readSoftware(software.ID)
	if err != nil || software.InstallationStatus != "UNINSTALLED" {
		t.Fatal(software, err)
	}
	env, _ = s.readNodeEnvironment(id)
	if env.ObservedDefaultVersion != "" || env.DesiredDefaultSoftwareID != "" {
		t.Fatal("default not cleared", env)
	}
	removal := nodePlan(t, b, host, map[string]any{"action": "REMOVE_ENVIRONMENT", "environmentId": id, "expectedEnvironmentVersion": env.Version, "confirmationName": env.Name, "deleteData": true, "source": map[string]string{}})
	runNodePlan(t, s, b, removal, "node-remove-once")
	env, _ = s.readNodeEnvironment(id)
	if env.ManagementMode != "RELEASED" {
		t.Fatal(env)
	}
	var history struct{ Data []json.RawMessage }
	if err = json.Unmarshal(b.call("GET", "/node-environments/"+id+"/jobs", nil, 200).Body.Bytes(), &history); err != nil || len(history.Data) != 5 {
		t.Fatal("history missing", len(history.Data), err)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM software_instances WHERE id=?", software.ID).Scan(&count)
	if count != 1 {
		t.Fatal("software history deleted")
	}
}

func TestNodeUnknownKeepsLockReconcilesWithoutReplay(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := nodeFixture(t, s, b)
	plan := nodePlan(t, b, host, map[string]any{"action": "INITIALIZE", "name": "uncertain", "account": "ubuntu", "source": map[string]string{"type": "OFFICIAL"}})
	f.unknown.Store(true)
	accepted := runNodePlan(t, s, b, plan, "node-unknown-once")
	job := accepted["jobId"].(string)
	var status string
	var locks int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "UNKNOWN" || locks != 1 {
		t.Fatal(status, locks)
	}
	b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 409)
	b.call("POST", "/jobs/"+job+"/recover", map[string]any{}, 200)
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks)
	if status != "SUCCEEDED" || locks != 0 || f.mutations.Load() != 1 {
		t.Fatal("reconcile replayed change", status, locks, f.mutations.Load())
	}
}

func TestNodeExternalRegistrationIsReadOnlyAndCapabilityIsSpecific(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := nodeFixture(t, s, b)
	f.noCapability.Store(true)
	body := b.call("POST", "/hosts/"+host+"/node-environments/discover", map[string]string{"expectedHostVersion": "1", "account": "ubuntu"}, 409).Body.String()
	if !strings.Contains(body, "NODE_CAPABILITY_REQUIRED") {
		t.Fatal(body)
	}
	f.noCapability.Store(false)
	discovered := dataOf(t, b.call("POST", "/hosts/"+host+"/node-environments/discover", map[string]string{"expectedHostVersion": "1", "account": "ubuntu"}, 200))
	candidate := discovered["candidates"].([]any)[0]
	registered := dataOf(t, b.call("POST", "/hosts/"+host+"/node-environments/register", map[string]any{"expectedHostVersion": "1", "name": "external", "candidate": candidate}, 200))
	id := registered["environmentId"].(string)
	env, _ := s.readNodeEnvironment(id)
	if env.ManagementMode != "OBSERVE" || env.RootPath != "/home/ubuntu/.nvm" {
		t.Fatal(env)
	}
	b.call("POST", "/hosts/"+host+"/node-environment-plans", map[string]any{"expectedHostVersion": "1", "environmentId": id, "expectedEnvironmentVersion": env.Version, "action": "SET_DEFAULT", "nodeVersion": "v22.21.1"}, 409)
	b.call("POST", "/node-environments/"+id+"/release", map[string]any{"expectedVersion": env.Version, "confirmed": true, "confirmationName": env.Name}, 200)
	if f.mutations.Load() != 0 {
		t.Fatal("external registration mutated target")
	}
	versions, err := s.nodeEnvironmentVersions(id)
	if err != nil || len(versions) != 1 || versions[0].ManagementMode != "RELEASED" {
		t.Fatal(versions, err)
	}
}

func nodeTestFingerprint(o deploy.NodeEnvironmentObservation) string {
	o.ObservedAt = ""
	o.Fingerprint = ""
	return deploy.Digest(o)
}

func TestNodeReceiptTamperingKeepsUnknownAndLock(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := nodeFixture(t, s, b)
	plan := nodePlan(t, b, host, map[string]any{"action": "INITIALIZE", "name": "guarded", "account": "ubuntu", "source": map[string]string{"type": "OFFICIAL"}})
	f.forged.Store(true)
	accepted := runNodePlan(t, s, b, plan, "node-forged-once")
	job := accepted["jobId"].(string)
	var status string
	var count int
	s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&count)
	if status != "UNKNOWN" || count != 1 {
		t.Fatal(status, count)
	}
	env, _ := s.readNodeEnvironment(accepted["environmentId"].(string))
	if env.Observation.Exists {
		t.Fatal("forged result trusted")
	}
}

func TestNodeObservationRejectsForgedContent(t *testing.T) {
	n := nodeEnvironment{ID: "e48914cd-36ca-4ecd-b99b-fd6978aef90c", Account: "ubuntu", AccountUID: "1000", RootPath: "/home/ubuntu/.local/share/streamfusion-ops/node/e48914cd-36ca-4ecd-b99b-fd6978aef90c", ManagerType: "NVM", ManagementMode: "MANAGED", NVMVersion: serviceinstall.NodeNVMVersion, NVMHash: serviceinstall.NodeNVMHash}
	base := deploy.NodeEnvironmentObservation{EnvironmentID: n.ID, Account: n.Account, AccountUID: n.AccountUID, RootPath: n.RootPath, ManagerType: n.ManagerType, NVMVersion: serviceinstall.NodeNVMVersion, NVMHash: serviceinstall.NodeNVMHash, Exists: true, ObservedAt: Now(), Versions: []deploy.NodeVersionObservation{{Version: "v22.21.1", BinaryPath: serviceinstall.NodeVersionRoot(n.context(), "v22.21.1") + "/bin/node", BinaryHash: strings.Repeat("a", 64)}}}
	base.Fingerprint = nodeTestFingerprint(base)
	if !nodeObservationMatches(n, base) {
		t.Fatal("valid observation rejected")
	}
	for name, change := range map[string]func(*deploy.NodeEnvironmentObservation){
		"nvm hash":                    func(o *deploy.NodeEnvironmentObservation) { o.NVMHash = strings.Repeat("e", 64) },
		"nvm version":                 func(o *deploy.NodeEnvironmentObservation) { o.NVMVersion = "0.39.0" },
		"duplicate versions":          func(o *deploy.NodeEnvironmentObservation) { o.Versions = append(o.Versions, o.Versions[0]) },
		"binary path":                 func(o *deploy.NodeEnvironmentObservation) { o.Versions[0].BinaryPath = "/usr/bin/node" },
		"default outside environment": func(o *deploy.NodeEnvironmentObservation) { o.DefaultVersion = "v24.11.0" },
	} {
		t.Run(name, func(t *testing.T) {
			o := base
			o.Versions = append([]deploy.NodeVersionObservation{}, base.Versions...)
			change(&o)
			o.Fingerprint = nodeTestFingerprint(o)
			if nodeObservationMatches(n, o) {
				t.Fatal("tampered observation accepted")
			}
		})
	}
	base.Fingerprint = strings.Repeat("f", 64)
	if nodeObservationMatches(n, base) {
		t.Fatal("forged fingerprint accepted")
	}
}

func TestNodeQueuedCancellationAndMaterialReference(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := nodeFixture(t, s, b)
	plan := nodePlan(t, b, host, map[string]any{"action": "INITIALIZE", "name": "cancelled", "account": "ubuntu", "source": map[string]string{"type": "OFFICIAL"}})
	material := materialCreate(t, b, 1, "node-material-protection")
	var raw string
	if err := s.DB.QueryRow("SELECT snapshot_json FROM node_environment_plans WHERE id=?", plan["id"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored nodeEnvironmentPlan
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	stored.MaterialID = material
	if _, err := s.DB.Exec("UPDATE node_environment_plans SET snapshot_json=? WHERE id=?", jsonText(stored), plan["id"]); err != nil {
		t.Fatal(err)
	}
	if body := b.call("DELETE", "/installation-materials/"+material, nil, 409).Body.String(); !strings.Contains(body, "INSTALL_MATERIAL_IN_USE") {
		t.Fatal(body)
	}
	if _, err := s.DB.Exec("UPDATE node_environment_plans SET snapshot_json=? WHERE id=?", raw, plan["id"]); err != nil {
		t.Fatal(err)
	}
	accepted := dataOf(t, b.call("POST", "/node-environment-plans/"+plan["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "node-cancel-once"}, 202))
	job := accepted["jobId"].(string)
	b.call("POST", "/jobs/"+job+"/cancel", map[string]any{}, 200)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processNodeEnvironment(context.Background(), job, raw)
	env, _ := s.readNodeEnvironment(accepted["environmentId"].(string))
	if env.ErrorCode != "JOB_CANCELLED" || f.mutations.Load() != 0 {
		t.Fatal(env, f.mutations.Load())
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
}
