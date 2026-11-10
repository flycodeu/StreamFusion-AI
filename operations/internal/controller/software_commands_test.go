package controller

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestCommandOfflineMaterialKeepsOriginalRevisionAndReplaysAfterWithdrawal(t *testing.T) {
	for _, mode := range []string{"NATIVE", "DOCKER"} {
		t.Run(mode, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			d := serviceinstall.Definition{Key: "custom-offline", Name: "Offline demo", Kind: "TOOL", Version: "1", Platform: "linux/amd64", RuntimeModes: []string{mode}, SourceModes: []string{"ARCHIVE"}, Commands: &serviceinstall.CommandRecipe{Kind: "TOOL", Locator: "/opt/demo/bin/demo", Steps: []serviceinstall.CommandStep{
				{Phase: "INSTALL", Name: "Install", Script: "echo install", RunAs: "ROOT", TimeoutSeconds: 30},
				{Phase: "VERIFY", Name: "Verify", Script: "echo verify", RunAs: "ROOT", TimeoutSeconds: 30},
				{Phase: "UNINSTALL", Name: "Uninstall", Script: "echo uninstall", RunAs: "ROOT", TimeoutSeconds: 30},
			}}}
			if mode == "DOCKER" {
				d.Kind, d.Commands.Kind, d.Commands.Locator = "SERVICE", "DOCKER", "demo"
			}
			saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
			published := dataOf(t, b.call("POST", "/command-templates/"+saved["id"].(string)+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200))
			d, err := s.findInstallRecipe(d.Key, d.Version)
			if err != nil {
				t.Fatal(err)
			}
			content := []byte("administrator-provided raw package, not a vendor tar")
			input := map[string]any{"recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "runtimeMode": mode, "name": "demo.bundle", "sha256": hashToken(string(content)), "byteSize": len(content), "idempotencyKey": "custom-offline-upload"}
			material := dataOf(t, b.call("POST", "/installation-materials", input, 201))
			id := material["id"].(string)
			installBytes(b, "PUT", "/installation-materials/"+id+"/content", content, 200, nil)
			installBytes(b, "POST", "/installation-materials/"+id+"/confirm", nil, 200, nil)
			m, err := s.readInstallationMaterial(id)
			if err != nil || !m.Available || m.ImageID != "" || m.Origin.Verification != "UPLOAD_SHA256" || len(m.Sources) != 0 {
				t.Fatalf("custom package was rejected or claimed vendor verification: %+v %v", m, err)
			}
			if got, err := os.ReadFile(s.installationMaterialPath(id, false)); err != nil || string(got) != string(content) {
				t.Fatal("confirmed bytes changed", err)
			}
			withdrawn := dataOf(t, b.call("DELETE", "/command-templates/"+saved["id"].(string), map[string]any{"expectedVersion": published["version"]}, 200))
			if replay := dataOf(t, b.call("POST", "/installation-materials", input, 200)); replay["id"] != id || replay["recipeRevision"] != d.Revision {
				t.Fatal("withdrawal lost the original upload receipt", replay)
			}
			input["idempotencyKey"] = "new-offline-after-withdrawal"
			b.call("POST", "/installation-materials", input, 400)
			b.call("POST", "/command-templates/"+saved["id"].(string)+"/publish", map[string]any{"expectedVersion": withdrawn["version"]}, 200)
			current, err := s.findInstallRecipe(d.Key, d.Version)
			if err != nil || current.Revision == d.Revision {
				t.Fatal("republish did not advance revision", err)
			}
			request := map[string]any{"expectedHostVersion": "1", "recipeKey": current.Key, "recipeVersion": current.Version, "recipeRevision": current.Revision, "config": serviceinstall.Config{Name: "demo", RuntimeMode: mode, MemoryMiB: 512}, "source": map[string]string{"type": "ARCHIVE", "materialId": id}}
			response := b.call("POST", "/hosts/"+uuid.NewString()+"/software-install-plans", request, 409)
			if !strings.Contains(response.Body.String(), "INSTALL_MATERIAL_NOT_READY") {
				t.Fatal("an old package was rebound to a new template revision", response.Body.String())
			}
		})
	}
}

// This fixture tests API/plan/receipt integration without executing scripts or
// installing anything on a real server.
func TestCommandSoftwareInstallAndUninstallKeepFrozenTemplate(t *testing.T) {
	for _, kind := range []string{"TOOL", "SYSTEMD", "DOCKER"} {
		t.Run(kind, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			d := serviceinstall.Definition{Key: "custom-demo", Name: "Demo", Version: "1.0", Category: "OTHER", Kind: "SERVICE", Platform: "linux/amd64", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ONLINE"}, Commands: &serviceinstall.CommandRecipe{Kind: kind, Locator: "demo.service", Steps: []serviceinstall.CommandStep{
				{Phase: "INSTALL", Name: "Install", Script: "echo install", RunAs: "ROOT", TimeoutSeconds: 30},
				{Phase: "VERIFY", Name: "Verify", Script: "echo verify", RunAs: "ROOT", TimeoutSeconds: 30},
				{Phase: "UNINSTALL", Name: "Uninstall", Script: "echo uninstall", RunAs: "ROOT", TimeoutSeconds: 30},
			}}}
			if kind == "TOOL" {
				d.Kind, d.Commands.Locator = "TOOL", "/opt/demo/bin/demo"
			} else if kind == "DOCKER" {
				d.RuntimeModes, d.SourceModes, d.Commands.Locator = []string{"DOCKER"}, []string{"REGISTRY"}, "demo"
			}
			saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
			published := dataOf(t, b.call("POST", "/command-templates/"+saved["id"].(string)+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200))
			d, err := s.findInstallRecipe(d.Key, d.Version)
			if err != nil {
				t.Fatal(err)
			}
			frozenHash := deploy.Digest(d)
			var mu sync.Mutex
			results := map[string]deploy.Result{}
			host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
				encode := func(value any) ([]byte, bool) { raw, _ := json.Marshal(value); return raw, true }
				if req.Action == "CAPABILITIES" {
					c := installCompatibilityCapabilities(req, s)
					c.CommandRecipeVersion, c.SoftwareLifecycleVersion, c.SoftwareUninstallVersion = deploy.CommandRecipeVersion, 1, 1
					c.CommandTrustFingerprint = deploy.CommandPublicKeyFingerprint(s.commandPublicKey())
					return encode(c)
				}
				if !commandRequest(req) {
					return nil, false
				}
				sig, err := base64.StdEncoding.DecodeString(req.CommandSignature)
				if err != nil || !ed25519.Verify(s.commandSigningKey().Public().(ed25519.PublicKey), []byte("streamfusion-ops/command-plan/v1\n"+deploy.CommandRequestDigest(req)), sig) {
					t.Error("target received unsigned or altered commands")
				}
				if req.JobID != "" && req.InputHash != deploy.CommandRequestDigest(req) {
					t.Error("transport signature changed frozen job hash")
				}
				if req.Action == "SOFTWARE_INSTALL_CHECK" {
					return encode(installPreflightFixture(*req.Installation, serviceinstall.InstallRoot("/opt/plugin-test", req.Installation.SoftwareID, req.Installation.Config), true))
				}
				if req.Action == "SOFTWARE_INSTALL_LOGS" {
					return encode(map[string]any{"text": "command uninstall output", "limited": false})
				}
				mu.Lock()
				defer mu.Unlock()
				if req.Action == "SOFTWARE_INSTALL" {
					i := req.Installation
					evidence := &serviceinstall.Evidence{SoftwareID: i.SoftwareID, BindingRevision: i.BindingRevision, RecipeKey: d.Key, RecipeRevision: d.Revision, ConfigHash: serviceinstall.Digest(i.Config), Kind: kind, Locator: d.Commands.Locator, ObservedVersion: d.Version, ObservedAt: Now(), Health: "READY"}
					state := "RUNNING"
					if kind == "TOOL" {
						evidence.BinaryPath, evidence.BinaryHash, state = evidence.Locator, strings.Repeat("a", 64), "INSTALLED"
					} else if kind == "DOCKER" {
						evidence.ContainerID, evidence.Locator, evidence.ImageID = strings.Repeat("b", 64), strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64)
					}
					evidence.IdentityHash = deploy.Digest([]string{kind, evidence.Locator, evidence.ImageID})
					results[req.JobID] = deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SERVICE_INSTALLED", ObservedAt: Now(), Installation: evidence, Software: &deploy.SoftwareObservation{Kind: kind, Locator: evidence.Locator, IdentityHash: evidence.IdentityHash, State: state, Version: d.Version, ObservedAt: Now()}}
				}
				if req.Action == "SOFTWARE_UNINSTALL" {
					if req.Uninstallation == nil || deploy.Digest(req.Uninstallation.Installation.Definition) != frozenHash || req.Software.Ownership.CommandTemplateHash != frozenHash || req.Software.ExpectedVersion != "1.0" || req.Software.Validate(false) != nil {
						t.Error("uninstall lost original template or verified ownership")
					}
					target, _ := s.readTarget(req.TargetID)
					data := serviceinstall.InstallRoot(target.InstallRoot, req.Software.ID, req.Uninstallation.Installation.Config) + "/data"
					results[req.JobID] = deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_UNINSTALLED", ObservedAt: Now(), Uninstallation: &deploy.SoftwareUninstallEvidence{SoftwareID: req.Software.ID, Kind: kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ProgramRemoved: true, DataRoot: data, ObservedAt: Now()}}
				}
				result := results[req.JobID]
				if req.Action == "ACK" && req.ResultHash == deploy.Digest(result) {
					result.Ack = true
				}
				return encode(result)
			})
			plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "demo", RuntimeMode: d.RuntimeModes[0], MemoryMiB: 512}, "source": map[string]string{"type": d.SourceModes[0]}}, 200))
			accepted := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "command-install-test"}, 202))
			installJob, id := accepted["jobId"].(string), accepted["softwareInstanceId"].(string)
			var raw string
			if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", installJob).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			original := raw
			b.h.processSoftware(context.Background(), installJob, raw)
			i, err := s.readSoftware(id)
			if err != nil || i.ManagementMode != "MANAGED" || i.InstallationStatus != "READY" {
				t.Fatalf("custom installation did not bind: %+v %v", i, err)
			}
			request := softwareRequest(i)
			if request.Validate(false) != nil || request.ExpectedVersion != "1.0" || request.Ownership.CommandTemplateHash != frozenHash {
				t.Fatalf("ownership cannot be reused: %+v", request)
			}
			if kind == "SYSTEMD" && (i.InstallationPaths.Bin != "" || request.Ownership.BinaryPath != "") {
				t.Fatal("system service invented a binary directory")
			}
			b.call("DELETE", "/command-templates/"+saved["id"].(string), map[string]any{"expectedVersion": published["version"]}, 200)
			input := map[string]any{"expectedVersion": i.Version, "action": "UNINSTALL", "idempotencyKey": "command-uninstall-test", "confirmed": true, "deleteData": true, "confirmationName": i.Name}
			if response := b.call("POST", "/software-instances/"+id+"/actions", input, 409); !strings.Contains(response.Body.String(), "COMMAND_DATA_REMOVAL_UNAVAILABLE") {
				t.Fatal(response.Body.String())
			}
			delete(input, "deleteData")
			delete(input, "confirmationName")
			remove := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", input, 202))
			job := remove["jobId"].(string)
			if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			b.h.processSoftware(context.Background(), job, raw)
			i, err = s.readSoftware(id)
			if err != nil || i.InstallationStatus != "UNINSTALLED" || i.ManagementMode != "RELEASED" || i.Installation == nil || i.InstallationEvidence == nil {
				t.Fatalf("uninstall lost history or failed to verify: %+v %v", i, err)
			}
			if logs := b.call("GET", "/jobs/"+job+"/logs", nil, 200).Body.String(); !strings.Contains(logs, "command uninstall output") || strings.Contains(logs, "JOB_RECEIPT") {
				t.Fatal("uninstall output was replaced with receipt", logs)
			}
			if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", installJob).Scan(&raw); err != nil || raw != original {
				t.Fatal("old job was rewritten", err)
			}
		})
	}
}
