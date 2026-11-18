package controller

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestNginxConfigurationFilePlanApplyAndHistory(t *testing.T) {
	s, b := setup(t)
	b.login()
	const original = "server { listen 80; }\n"
	const privateMarker = "config-private-marker-2317"
	const unrelatedSecret = "unrelated-management-token-2317"
	body := "# " + privateMarker + "\n" + original
	revision := deploy.Digest(original)
	var fileVersion atomic.Int32
	var calls atomic.Int32
	var mu sync.Mutex
	var result deploy.Result
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { return []byte(jsonText(v)), true }
		if req.Action == "CAPABILITIES" {
			return encode(executorCapabilities{
				Owner:    deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID},
				Platform: "linux/amd64", DockerEngine: true, InstallVersion: serviceinstall.CapabilityVersion,
				SoftwareManagementVersion: deploy.SoftwareManagementVersion, ConfigurationFileVersion: int(fileVersion.Load()),
				NativeInstallVersion: 1, CatalogHash: serviceinstall.CatalogHash(), CommandRecipeVersion: deploy.CommandRecipeVersion,
				CommandTrustFingerprint: deploy.CommandPublicKeyFingerprint(s.commandPublicKey()),
			})
		}
		m := req.Management
		if m == nil || m.ConfigFile == nil {
			return nil, false
		}
		if req.CommandSignature == "" {
			t.Error("configuration request was not signed")
		}
		wantSecrets := m.ConfigFile.Action == "save" && (req.Action == "SOFTWARE_OPERATION_CHECK" || req.Action == "SOFTWARE_OPERATION")
		if (!wantSecrets && len(req.Secrets) != 0) || (wantSecrets && (len(req.Secrets) != 1 || req.Secrets[serviceinstall.ConfigurationFileSecret] != body)) {
			t.Errorf("unexpected configuration secret scope for %s", req.Action)
		}
		switch req.Action {
		case "SOFTWARE_OPERATION_CHECK":
			resources, err := m.ResolveResources(req.Software.Locator)
			if err != nil {
				t.Error(err)
			}
			for j := range resources {
				resources[j].IdentityHash = deploy.Digest(resources[j].Value)
			}
			file := serviceinstall.ConfigurationFileResult{Status: "PASSED", CanCreate: true, Revision: revision, Editable: true}
			switch m.ConfigFile.Action {
			case "list":
				file.Files = []serviceinstall.ConfigurationFile{{ID: "site", Name: "site.conf", Enabled: true, Managed: true, Editable: true}}
			case "read":
				file.Content = original
			}
			return encode(serviceinstall.ManagementPreflight{Ready: true, File: &file, Resources: resources, ResourceHash: deploy.Digest(resources)})
		case "SOFTWARE_OPERATION":
			calls.Add(1)
			resources, _ := m.ResolveResources(req.Software.Locator)
			for j := range resources {
				resources[j].IdentityHash = deploy.Digest(resources[j].Value)
			}
			fact := &deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, State: "RUNNING", ObservedAt: Now()}
			evidence := &serviceinstall.ManagementEvidence{SoftwareID: m.SoftwareID, OperationKey: m.OperationKey, DefinitionHash: deploy.Digest(m.Definition), ParametersHash: m.ParameterHash(), ResourceHash: m.ResourceHash, Resources: resources, Output: "配置已验证", ObservedAt: Now()}
			mu.Lock()
			defer mu.Unlock()
			result = deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "SOFTWARE_OPERATION_COMPLETED", Software: fact, Management: evidence, ObservedAt: Now()}
			return encode(result)
		case "STATUS", "ACK":
			mu.Lock()
			defer mu.Unlock()
			out := result
			out.Ack = req.Action == "ACK" && req.ResultHash == deploy.Digest(result)
			return encode(out)
		case "PROGRESS":
			return encode(deploy.Result{Phase: "MANAGEMENT_VERIFY"})
		}
		return nil, false
	})
	read := func(query string, args ...any) string {
		t.Helper()
		var value string
		if err := s.DB.QueryRow(query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	install := nginxPlan(t, b, host, 18188)
	installed := dataOf(t, b.call("POST", "/software-install-plans/"+install["id"].(string)+"/submit", map[string]string{"idempotencyKey": "nginx-file-install"}, 202))
	installJob, id := installed["jobId"].(string), installed["softwareInstanceId"].(string)
	jobInput := "SELECT input_json FROM jobs WHERE id=?"
	jobResult := "SELECT result_json FROM jobs WHERE id=?"
	installationInput := read(jobInput, installJob)
	b.h.processSoftware(context.Background(), installJob, installationInput)
	installationResult := read(jobResult, installJob)
	before, err := s.readSoftware(id)
	if err != nil || !managementInstance(before) {
		t.Fatalf("fixture installation not confirmed: %v", err)
	}
	template, err := s.managementTemplate(before, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	d := serviceinstall.WithoutConfigurationFileOperations(configurationFileDefinition(*template.Published))
	if err := serviceinstall.ValidateDefinition(d); err != nil || jsonText(d) != jsonText(template.Published) {
		t.Fatalf("transient file operations changed the published template: %v", err)
	}
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "other token", "kind": "TOKEN", "secret": unrelatedSecret}, 201))
	refs := map[string]serviceinstall.CredentialReference{"TOKEN": {ID: credential["id"].(string), Revision: credential["revision"].(string)}}
	adopted := *template.Published
	adopted.Management.Parameters = []serviceinstall.ParameterDefinition{{Key: "TOKEN", Label: "管理凭据", Type: "CREDENTIAL"}}
	if _, err = s.DB.Exec(`INSERT INTO software_management_state(software_id,template_id,template_revision,definition_json,parameters_json,credential_refs_json,resources_json,version,last_job_id,updated_at) VALUES(?,?,?,?,'{}',?,'[]',1,?,?)`, id, template.ID, template.Revision, jsonText(adopted), jsonText(refs), installJob, Now()); err != nil {
		t.Fatal(err)
	}
	var latest serviceinstall.Definition
	if err = json.Unmarshal([]byte(jsonText(adopted)), &latest); err != nil {
		t.Fatal(err)
	}
	latest.Commands.Revision = int64(template.Revision + 1)
	latest.Management.Operations[0].Name = "新版管理操作"
	published := jsonText(latest)
	if _, err = s.DB.Exec("UPDATE command_templates SET published_json=?,draft_json=?,revision=revision+1 WHERE id=?", published, published, template.ID); err != nil {
		t.Fatal(err)
	}
	base := "/software-instances/" + id + "/config-files"
	if res := b.call("GET", base, nil, 409); !strings.Contains(res.Body.String(), "EXECUTOR_UPGRADE_REQUIRED") {
		t.Fatal("missing file capability did not reject read")
	}
	fileVersion.Store(deploy.ConfigurationFileVersion)
	if listing := dataOf(t, b.call("GET", base, nil, 200)); listing["canCreate"] != true || len(listing["files"].([]any)) != 1 {
		t.Fatal("unexpected file list", listing)
	}
	if file := dataOf(t, b.call("GET", base+"/site", nil, 200)); file["content"] != original || file["revision"] != revision {
		t.Fatal("file content or revision differs")
	}
	input := map[string]string{"name": "site", "action": "save", "revision": revision, "content": body}
	if res := b.rawCall("POST", base+"/check", input, 400); !strings.Contains(res.Body.String(), "SECURE_REQUEST_INVALID") {
		t.Fatal("plaintext check accepted")
	}
	input["name"] = "../site"
	b.call("POST", base+"/check", input, 400)
	input["name"] = "site"
	checked := b.call("POST", base+"/check", input, 200)
	for _, value := range []string{privateMarker, unrelatedSecret, "contentEnvelope"} {
		if strings.Contains(checked.Body.String(), value) {
			t.Fatal("check response exposed private payload")
		}
	}
	plan := dataOf(t, checked)
	if plan["status"] != "PASSED" {
		t.Fatal("candidate check did not pass", plan)
	}
	planID := plan["planId"].(string)
	submit := map[string]string{"planId": planID, "idempotencyKey": "nginx-file-save-once"}
	job := dataOf(t, b.call("POST", base+"/apply", submit, 202))["jobId"].(string)
	if dataOf(t, b.call("POST", base+"/apply", submit, 200))["jobId"] != job {
		t.Fatal("retry created another configuration job")
	}
	raw := read(jobInput, job)
	var frozen deploy.Request
	if err := json.Unmarshal([]byte(raw), &frozen); err != nil || frozen.Management == nil || frozen.Management.ConfigFile == nil || len(frozen.Secrets) != 0 || frozen.Management.ParameterCredentials["TOKEN"] != refs["TOKEN"] {
		t.Fatal("frozen job lost file or unrelated credential reference", err)
	}
	if err := s.releaseSecrets(&frozen); err != nil || len(frozen.Secrets) != 1 || frozen.Secrets[serviceinstall.ConfigurationFileSecret] != body {
		t.Fatal("frozen file could not be decrypted in isolation", err)
	}
	b.h.processSoftwareOperation(context.Background(), job, raw)
	if receipt := read("SELECT status||':'||target_ack_pending FROM jobs WHERE id=?", job); receipt != "SUCCEEDED:0" || calls.Load() != 1 {
		t.Fatalf("job receipt %s, target applies %d", receipt, calls.Load())
	}
	for _, value := range []string{raw, read(jobResult, job), read("SELECT snapshot_json FROM software_configuration_plans WHERE id=?", planID)} {
		if strings.Contains(value, privateMarker) || strings.Contains(value, unrelatedSecret) {
			t.Fatal("configuration or credential body persisted in plan or job")
		}
	}
	after, err := s.readSoftware(id)
	if err != nil || jsonText(after.Installation) != jsonText(before.Installation) || after.BindingRevision != before.BindingRevision || after.IdentityHash != before.IdentityHash || read(jobInput, installJob) != installationInput || read(jobResult, installJob) != installationResult {
		t.Fatal("original installation or history changed", err)
	}
	state, err := s.readSoftwareManagement(id)
	if err != nil || state.TemplateID != template.ID || state.TemplateRevision != template.Revision || state.Version != "1" || jsonText(state.Definition) != jsonText(adopted) || state.CredentialRefs["TOKEN"] != refs["TOKEN"] || state.LastJobID != job {
		t.Fatal("adopted management definition changed", err)
	}
	if read("SELECT published_json FROM command_templates WHERE id=?", template.ID) != published {
		t.Fatal("configuration save changed the latest published template")
	}
}
