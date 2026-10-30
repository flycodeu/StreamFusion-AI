package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// This matrix exercises the controller, authenticated HTTP contracts, durable
// jobs and controlled SSH receipts. The small seeded materials are deliberately
// not vendor archives: this test does not certify package extraction, Docker,
// systemd, real socket checks or a successful installation on a Linux host.
func TestServiceInstallMatrixControlledProtocol(t *testing.T) {
	definitions := serviceinstall.Catalog()
	if len(definitions) != 17 {
		t.Fatalf("update the installation matrix for catalog size %d", len(definitions))
	}
	for _, definition := range definitions {
		for _, mode := range definition.RuntimeModes {
			for _, availability := range []string{"online", "offline"} {
				t.Run(definition.Key+"/"+definition.Version+"/"+mode+"/"+availability, func(t *testing.T) {
					s, b := setup(t)
					b.login()
					host, fixture := installSSHFixture(t, s, b)
					// Native installation must work without Docker. The Docker
					// fixture has Engine but no Compose, which is not required.
					fixture.NoDocker.Store(mode == "NATIVE")
					config, secrets := matrixInstallationConfig(t, b, definition, mode)
					source := map[string]string{"type": "REGISTRY"}
					var material installationMaterial
					if mode == "NATIVE" || availability == "offline" {
						material = matrixProtocolMaterial(t, s, b, definition, mode)
						source = map[string]string{"type": "ARCHIVE", "materialId": material.ID}
						if availability == "online" {
							// Official download/validation is separately tested. This
							// seed isolates the already-verified material contract.
							source["type"] = "ONLINE"
						}
					}
					response := b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{
						"expectedHostVersion": "1", "recipeKey": definition.Key,
						"recipeVersion": definition.Version, "recipeRevision": definition.Revision,
						"config": config, "source": source,
					}, 200)
					publicPlan := dataOf(t, response)
					if _, leaked := publicPlan["softwareId"]; leaked {
						t.Fatal("pending software identity exposed")
					}
					var plan softwareInstallPlan
					if err := json.Unmarshal([]byte(jsonText(publicPlan)), &plan); err != nil {
						t.Fatal(err)
					}
					if serviceinstall.Digest(plan.Definition) != serviceinstall.Digest(definition) || serviceinstall.Digest(plan.Config) != serviceinstall.Digest(config) || plan.Source.Type != source["type"] {
						t.Fatal("plan changed the selected recipe, runtime or configuration")
					}
					matrixCheckPlanPathsAndPorts(t, plan)
					if material.ID != "" && (plan.Source.MaterialID != material.ID || plan.Source.SHA256 != material.SHA256 || plan.Source.ByteSize != material.ByteSize) {
						t.Fatal("material identity or digest was not frozen")
					}
					for _, secret := range secrets {
						if strings.Contains(response.Body.String(), secret) {
							t.Fatal("credential plaintext entered the public plan")
						}
					}
					submitPath := "/software-install-plans/" + plan.ID + "/submit"
					accepted := dataOf(t, b.call("POST", submitPath, map[string]string{"idempotencyKey": "matrix-install-once"}, 202))
					job, id := accepted["jobId"].(string), accepted["softwareInstanceId"].(string)
					var raw string
					if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
						t.Fatal(err)
					}
					var request deploy.Request
					if err := deploy.StrictJSON([]byte(raw), &request); err != nil || request.Installation == nil {
						t.Fatal("missing durable installation intent", err)
					}
					if request.Installation.Config.Mode() != mode || request.Installation.SoftwareID != id || request.Installation.Source != plan.Source.Source || len(request.Secrets) != 0 {
						t.Fatal("durable installation intent lost its frozen identity")
					}
					for _, secret := range secrets {
						if strings.Contains(raw, secret) {
							t.Fatal("credential plaintext entered a durable job")
						}
					}
					b.h.processSoftware(context.Background(), job, raw)
					installed, err := s.readSoftware(id)
					if err != nil || installed.InstallationStatus != "READY" || !installationBound(installed) || installed.DataOwnership != "OPS" || installed.Installation == nil {
						var phase, code string
						s.DB.QueryRow("SELECT phase,error_code FROM jobs WHERE id=?", job).Scan(&phase, &code)
						t.Fatal("verified installation was not committed", err, installed.InstallationStatus, phase, code)
					}
					kind, state := "DOCKER", "RUNNING"
					if mode == "NATIVE" {
						kind = "SYSTEMD"
					}
					if definition.Kind == "TOOL" {
						kind, state = "TOOL", "INSTALLED"
					}
					if installed.RuntimeKind != kind || installed.ActualState != state || installed.DesiredState != state || serviceinstall.Digest(installed.Installation.Config) != serviceinstall.Digest(config) {
						t.Fatal("runtime/desired/actual state mismatch", installed.RuntimeKind, installed.DesiredState, installed.ActualState)
					}
					ownership := softwareRequest(installed).Ownership
					if ownership == nil || ownership.JobID != job || ownership.TargetID != host || ownership.ConfigHash != serviceinstall.Digest(config) {
						t.Fatal("installation ownership was not bound")
					}
					binaryPath := plan.Paths.Root + "/" + serviceinstall.NativeBinaryRelative(definition)
					if definition.Key == "docker" {
						binaryPath = "/usr/bin/dockerd"
					}
					if mode == "NATIVE" && (installed.InstallationEvidence.BinaryPath != binaryPath || ownership.RuntimeMode != "NATIVE" || ownership.ImageID != "") {
						t.Fatal("native installation lost the real binary path or requires an image")
					}
					matrixCheckJobFinished(t, s, job)
					var bridges int
					if err = s.DB.QueryRow("SELECT count(*) FROM plugin_installations WHERE id=? AND target_id=? AND status='READY' AND image_id=?", id, host, definition.ImageID).Scan(&bridges); err != nil {
						t.Fatal(err)
					}
					wantBridges := 0
					if serviceinstall.UsesJavaBridge(definition) && mode == "DOCKER" {
						wantBridges = 1
						var key, version string
						s.DB.QueryRow("SELECT plugin_key,plugin_version FROM plugin_installations WHERE id=?", id).Scan(&key, &version)
						wantKey, wantVersion := "java-runtime", definition.Version+"-jre"
						if definition.Key == "maven" {
							wantKey, wantVersion = "maven-jdk-build", "3.9.11-jdk-21"
						}
						if key != wantKey || version != wantVersion {
							t.Fatal("existing release consumer bridge mismatch", key, version)
						}
					}
					if bridges != wantBridges {
						t.Fatal("tool bridge was missing or created for a native/service install", bridges)
					}
					if wantBridges == 1 {
						legacy := dataOf(t, b.call("GET", "/plugin-installations/"+id, nil, 200))
						if legacy["referenceCount"] != float64(1) {
							t.Fatal("legacy removal UI did not expose the managed software reference")
						}
						blocked := b.call("POST", "/plugin-installations/"+id+"/remove", map[string]string{"idempotencyKey": "matrix-tool-remove"}, 409).Body.String()
						if !strings.Contains(blocked, "PLUGIN_REFERENCE_EXISTS") {
							t.Fatal("legacy removal could delete a managed tool image", blocked)
						}
					}
					if !strings.Contains(b.call("GET", "/jobs/"+job+"/logs", nil, 200).Body.String(), "verified install step") {
						t.Fatal("installation logs missing")
					}
					b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200)
					if definition.Kind == "TOOL" {
						body := b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": installed.Version, "action": "START", "idempotencyKey": "matrix-tool-no-process"}, 409).Body.String()
						if !strings.Contains(body, "SOFTWARE_TOOL_READ_ONLY") {
							t.Fatal("tool exposed a fake service lifecycle", body)
						}
					} else {
						matrixServiceAction(t, s, b, id, "STOP", "STOPPED")
						matrixServiceAction(t, s, b, id, "START", "RUNNING")
					}
					fixture.OldExecutor.Store(true)
					replayed := dataOf(t, b.call("POST", submitPath, map[string]string{"idempotencyKey": "matrix-install-once"}, 200))
					if replayed["jobId"] != job || replayed["softwareInstanceId"] != id {
						t.Fatal("lost reply did not replay the original identities")
					}
					b.h.processSoftware(context.Background(), job, raw)
					if fixture.InstallCount.Load() != 1 {
						t.Fatal("terminal job re-executed the installation")
					}
					if wantBridges == 1 {
						// The protective legacy reference must not count itself
						// when the software owner explicitly releases management.
						fixture.OldExecutor.Store(false)
						current, err := s.readSoftware(id)
						if err != nil {
							t.Fatal(err)
						}
						released := dataOf(t, b.call("POST", "/software-instances/"+id+"/unmanage", map[string]string{"expectedVersion": current.Version, "idempotencyKey": "matrix-tool-unmanage"}, 202))
						releaseJob := released["jobId"].(string)
						var releaseRaw string
						if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", releaseJob).Scan(&releaseRaw); err != nil {
							t.Fatal(err)
						}
						b.h.processSoftware(context.Background(), releaseJob, releaseRaw)
						current, err = s.readSoftware(id)
						if err != nil || current.ManagementMode != "RELEASED" {
							t.Fatal("tool management release was blocked by its own bridge", err)
						}
						matrixCheckJobFinished(t, s, releaseJob)
						legacy := dataOf(t, b.call("GET", "/plugin-installations/"+id, nil, 200))
						if legacy["referenceCount"] != float64(0) {
							t.Fatal("released software still blocked legacy image removal")
						}
					}
				})
			}
		}
	}
}

func matrixInstallationConfig(t *testing.T, b *browser, d serviceinstall.Definition, mode string) (serviceinstall.Config, []string) {
	t.Helper()
	c := serviceinstall.Config{Name: "matrix " + d.Name, RuntimeMode: mode, StorageRoot: "/data/ops-install-matrix", BindAddress: "127.0.0.1", MemoryMiB: 1024}
	secrets := []string{}
	credential := func(name, kind, value string) (string, string) {
		secrets = append(secrets, value)
		ref := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": name, "kind": kind, "secret": value}, 201))
		return ref["id"].(string), ref["revision"].(string)
	}
	if d.Kind != "TOOL" && d.Key != "docker" {
		c.HostPort, c.Ports = d.Port+20000, map[string]int{}
		for _, p := range d.Ports {
			if p.Key != "main" && (!p.NativeOnly || mode == "NATIVE") {
				// Exercise every supported optional protocol; paired defaults
				// deliberately preserve the shared port validator invariants.
				c.Ports[p.Key] = p.DefaultPort
			}
		}
		if d.Key != "nginx" {
			c.CredentialID, c.CredentialRevision = credential("matrix service password", "PASSWORD", "Matrix-Primary-Secret-123")
		}
	}
	if d.Key == "seaweedfs" {
		c.AccessKeyCredentialID, c.AccessKeyCredentialRevision = credential("matrix S3 access key", "TOKEN", "MATRIX_ACCESS_KEY_123")
	}
	if d.Key == "mysql" {
		c.Database, c.Username = "video", "video_user"
		c.UserCredentialID, c.UserCredentialRevision = credential("matrix database user", "PASSWORD", "Matrix-Database-Secret-123")
	}
	if d.Key == "mediamtx" {
		c.Username = "ops"
	}
	if err := c.Validate(d); err != nil {
		t.Fatal("invalid matrix configuration", err, serviceinstall.PortMappings(d, c))
	}
	return c, secrets
}

// The API creates normal metadata and credentials; only the verified material
// payload is seeded. Validation of actual official archives belongs to the
// separate material/executor tests, not to this controller protocol matrix.
func matrixProtocolMaterial(t *testing.T, s *Store, b *browser, d serviceinstall.Definition, mode string) installationMaterial {
	t.Helper()
	payload := []byte("controlled protocol material: " + d.Key + "/" + d.Version + "/" + mode)
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	data := dataOf(t, b.call("POST", "/installation-materials", map[string]any{
		"recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision,
		"runtimeMode": mode, "name": "protocol fixture material", "sha256": digest,
		"byteSize": len(payload), "idempotencyKey": "matrix-material-once",
	}, 201))
	id := data["id"].(string)
	file := s.installationMaterialPath(id, false)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	imageID := d.ImageID
	if mode == "NATIVE" {
		imageID = "native:" + digest
	}
	if _, err := s.DB.Exec("UPDATE installation_materials SET status='AVAILABLE',platform=?,image_id=? WHERE id=?", d.Platform, imageID, id); err != nil {
		t.Fatal(err)
	}
	m, err := s.readInstallationMaterial(id)
	if err != nil || !m.Available {
		t.Fatal("controlled material was not readable", err)
	}
	return m
}

func matrixCheckPlanPathsAndPorts(t *testing.T, p softwareInstallPlan) {
	t.Helper()
	if !strings.HasPrefix(p.Paths.Root, p.Config.StorageRoot+"/") || p.Paths.Data != p.Paths.Root+"/data" || p.Paths.Config != p.Paths.Root+"/config" {
		t.Fatal("dedicated program/data/config root was not preserved", p.Paths)
	}
	if p.Config.Mode() == "NATIVE" {
		if p.Paths.Bin != path.Dir(p.Paths.Root+"/"+serviceinstall.NativeBinaryRelative(p.Definition)) || p.Paths.Logs != p.Paths.Root+"/logs" {
			t.Fatal("native binary/log paths do not reflect the recipe", p.Paths)
		}
	} else if p.Paths.Bin != "" || p.Paths.Logs != "" {
		t.Fatal("Docker plan invented host binary/log directories", p.Paths)
	}
	expected := serviceinstall.PortMappings(p.Definition, p.Config)
	if len(p.PortMappings) != len(expected) {
		t.Fatal("not every active protocol was returned", len(p.PortMappings), len(expected))
	}
	for n, port := range expected {
		if p.PortMappings[n] != (installPortMapping{Key: port.Key, Address: port.BindAddress, HostPort: port.HostPort, ContainerPort: port.ContainerPort, Protocol: port.Protocol}) {
			t.Fatal("public port mapping changed", p.PortMappings[n], port)
		}
	}
}

func matrixCheckJobFinished(t *testing.T, s *Store, job string) {
	t.Helper()
	var status string
	var pending, locks int
	if err := s.DB.QueryRow("SELECT status,target_ack_pending FROM jobs WHERE id=?", job).Scan(&status, &pending); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks); err != nil {
		t.Fatal(err)
	}
	if status != "SUCCEEDED" || pending != 0 || locks != 0 {
		t.Fatal("terminal evidence or ACK/locks not finalized", status, pending, locks)
	}
}

func matrixServiceAction(t *testing.T, s *Store, b *browser, id, action, expected string) {
	t.Helper()
	before, err := s.readSoftware(id)
	if err != nil {
		t.Fatal(err)
	}
	receipt := dataOf(t, b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": before.Version, "action": action, "idempotencyKey": "matrix-service-" + action}, 202))
	job := receipt["jobId"].(string)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processSoftware(context.Background(), job, raw)
	after, err := s.readSoftware(id)
	if err != nil || after.ActualState != expected || after.DesiredState != expected || after.IdentityHash != before.IdentityHash || after.Locator != before.Locator {
		t.Fatal(fmt.Sprintf("%s did not preserve binding and confirm %s", action, expected), err)
	}
	matrixCheckJobFinished(t, s, job)
}
