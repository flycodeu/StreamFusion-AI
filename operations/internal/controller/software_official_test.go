package controller

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func officialProtocolPlan(t *testing.T, d serviceinstall.Definition) *serviceinstall.OfficialPlan {
	t.Helper()
	spec, err := serviceinstall.OfficialRecipeSpec(d)
	if err != nil {
		t.Fatal(err)
	}
	p := &serviceinstall.OfficialPlan{Manager: "APT", OS: "ubuntu24.04", Architecture: "amd64", Paths: spec.Paths}
	for _, name := range spec.Packages {
		p.Packages = append(p.Packages, serviceinstall.OfficialPackage{Name: name, Version: "1.0-fixture", Origin: "Ubuntu controlled protocol fixture", Direct: true})
	}
	p.Commands = serviceinstall.OfficialInstallCommands(d, p.Packages)
	if err = p.Validate(d); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOfficialPlanCannotInjectCommandsOrDeletionPaths(t *testing.T) {
	d, _ := serviceinstall.FindOfficial("nginx", "ubuntu24.04-repository")
	for _, changed := range []string{"command", "paths", "package", "offline", "binary-directory"} {
		t.Run(changed, func(t *testing.T) {
			p := officialProtocolPlan(t, d)
			plan := &softwareInstallPlan{Definition: d, Source: installSource{Source: serviceinstall.Source{Type: "OFFICIAL"}}}
			checked := serviceinstall.Preflight{Official: p, Root: p.Paths.Root, DataDirectory: p.Paths.Data, ConfigDirectory: p.Paths.Config, LogsDirectory: p.Paths.Logs, BinaryDirectory: path.Dir(p.Paths.Binary)}
			switch changed {
			case "command":
				p.Commands[0].Executable = "/bin/bash"
			case "paths":
				p.Paths.OwnedPaths = []string{"/"}
			case "package":
				p.Packages[0].Version = "1;rm -rf /"
			case "binary-directory":
				checked.BinaryDirectory = "/tmp/untrusted"
			case "offline":
				plan.Definition, _ = serviceinstall.Find("nginx", "1.30.6")
				plan.Source.Type = "ARCHIVE"
			}
			if err := validateInstallPreflightPaths("/opt/fixture", plan, checked); err == nil {
				t.Fatal("untrusted official plan accepted")
			}
		})
	}
}

// Only the SSH wire and durable controller are real here. The target's package
// manager and software are explicit fixtures; this test never invokes apt.
func TestOfficialInstallationFreezesCommandsAndPreservesHistory(t *testing.T) {
	s, b := setup(t)
	b.login()
	definition, _ := serviceinstall.FindOfficial("nginx", "ubuntu24.04-repository")
	var calls atomic.Int32
	var drift atomic.Bool
	var mu sync.Mutex
	results := map[string]deploy.Result{}
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { data, _ := json.Marshal(v); return data, true }
		if req.Action == "CAPABILITIES" {
			return encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, "platform": "linux/amd64", "nativeInstallVersion": 1, "officialInstallVersion": 1, "softwareLifecycleVersion": 1, "softwareUninstallVersion": 1, "serviceInstallVersion": serviceinstall.CapabilityVersion})
		}
		if req.Installation == nil && req.Uninstallation == nil {
			return nil, false
		}
		if req.Action == "SOFTWARE_INSTALL_CHECK" {
			p := officialProtocolPlan(t, definition)
			if drift.Load() {
				p.Packages[0].Version = "1.1-fixture"
				p.Commands = serviceinstall.OfficialInstallCommands(definition, p.Packages)
			}
			return encode(serviceinstall.Preflight{Ready: true, Root: p.Paths.Root, DataDirectory: p.Paths.Data, ConfigDirectory: p.Paths.Config, LogsDirectory: p.Paths.Logs, BinaryDirectory: path.Dir(p.Paths.Binary), Official: p, Checks: []serviceinstall.Check{{Key: "OFFICIAL", Status: "PASS", Message: "controlled apt plan"}}})
		}
		mu.Lock()
		defer mu.Unlock()
		if req.Action == "SOFTWARE_INSTALL" {
			calls.Add(1)
			p := req.Installation.Official
			if p == nil || req.Installation.Source.Type != "OFFICIAL" || req.Installation.Source.ByteSize != 0 || len(req.Secrets) != 0 {
				t.Error("official installation must not transfer a material or credential")
			}
			actual := &deploy.SoftwareObservation{Kind: "SYSTEMD", Locator: p.Paths.Unit, IdentityHash: strings.Repeat("a", 64), State: "RUNNING", Version: "nginx fixture 1.0", ObservedAt: Now()}
			evidence := &serviceinstall.Evidence{SoftwareID: req.Installation.SoftwareID, BindingRevision: 1, RecipeKey: definition.Key, RecipeRevision: definition.Revision, ConfigHash: deploy.Digest(req.Installation.Config), Kind: actual.Kind, Locator: actual.Locator, IdentityHash: actual.IdentityHash, ObservedVersion: actual.Version, BinaryPath: p.Paths.Binary, BinaryHash: strings.Repeat("b", 64), Health: "READY", ObservedAt: Now()}
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SERVICE_INSTALLED", ObservedAt: Now(), Software: actual, Installation: evidence}
			results[req.JobID] = result
			return encode(result)
		}
		if req.Action == "SOFTWARE_UNINSTALL" {
			p := req.Uninstallation.Installation.Official
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_UNINSTALLED", ObservedAt: Now(), Uninstallation: &deploy.SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DeleteData: req.Uninstallation.DeleteData, DataRemoved: req.Uninstallation.DeleteData, DataRoot: p.Paths.Data, ObservedAt: Now()}}
			if req.Uninstallation.DeleteData {
				result.Uninstallation.RemovedPaths = p.Paths.OwnedPaths
			}
			results[req.JobID] = result
			return encode(result)
		}
		if req.Action == "ACK" || req.Action == "STATUS" {
			result := results[req.JobID]
			if req.Action == "ACK" && req.ResultHash == deploy.Digest(result) {
				result.Ack = true
			}
			return encode(result)
		}
		return nil, false
	})
	var catalog struct{ Data []serviceinstall.Definition }
	if err := json.Unmarshal(b.call("GET", "/software-official-recipes", nil, 200).Body.Bytes(), &catalog); err != nil || len(catalog.Data) != len(serviceinstall.OfficialCatalog()) || len(serviceinstall.Catalog()) != 17 {
		t.Fatal("official catalog must be separate from frozen offline definitions", err)
	}
	input := map[string]any{"expectedHostVersion": "1", "recipeKey": definition.Key, "recipeVersion": definition.Version, "recipeRevision": definition.Revision, "config": serviceinstall.Config{Name: "official nginx fixture", RuntimeMode: "NATIVE", MemoryMiB: 512, HostPort: 80, BindAddress: "0.0.0.0"}, "source": map[string]string{"type": "OFFICIAL"}}
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", input, 200))
	if plan["official"] == nil || plan["paths"].(map[string]any)["root"] != "/" {
		t.Fatal("official system plan missing")
	}
	endpoint := "/software-install-plans/" + plan["id"].(string) + "/submit"
	drift.Store(true)
	if body := b.call("POST", endpoint, map[string]string{"idempotencyKey": "official-once-only"}, 409).Body.String(); !strings.Contains(body, "OFFICIAL_PLAN_CHANGED") {
		t.Fatal(body)
	}
	drift.Store(false)
	accepted := dataOf(t, b.call("POST", endpoint, map[string]string{"idempotencyKey": "official-once-only"}, 202))
	job, id := accepted["jobId"].(string), accepted["softwareInstanceId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	b.h.processSoftware(context.Background(), job, raw)
	installed, err := s.readSoftware(id)
	if err != nil || installed.InstallationStatus != "READY" || installed.Installation.Official == nil || installed.InstallationPaths != nil || installed.SoftwareVersion != "nginx fixture 1.0" || softwareRequest(installed).Ownership.Official == nil || calls.Load() != 1 {
		t.Fatal("official system result must keep actual version and explicit paths", err, installed)
	}
	if dataOf(t, b.call("POST", endpoint, map[string]string{"idempotencyKey": "official-once-only"}, 200))["jobId"] != job {
		t.Fatal("official submit replay lost original job")
	}
	configInput := map[string]any{"expectedVersion": installed.Version, "config": installed.Installation.Config}
	if body := b.call("POST", "/software-instances/"+id+"/configuration-plans", configInput, 409).Body.String(); !strings.Contains(body, "CONFIGURATION_UNAVAILABLE") {
		t.Fatal("official system defaults must not enter private configuration writer", body)
	}
	removed := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", map[string]any{"action": "UNINSTALL", "expectedVersion": installed.Version, "idempotencyKey": "official-delete-confirmed", "confirmed": true, "deleteData": true, "confirmationName": installed.Name}, 202))
	removeJob := removed["jobId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", removeJob).Scan(&raw)
	b.h.processSoftware(context.Background(), removeJob, raw)
	installed, err = s.readSoftware(id)
	if err != nil || installed.InstallationStatus != "UNINSTALLED" || installed.ManagementMode != "RELEASED" {
		t.Fatal("official removal history missing", err, installed)
	}
	var history int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE id IN (?,?)", job, removeJob).Scan(&history)
	if history != 2 {
		t.Fatal("official operation history was removed")
	}
}
