package controller

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestManagedWebViewUsesConfirmedNetworkWithoutChangingInstallationHistory(t *testing.T) {
	s, b, id, template, _ := readyManagementFixture(t)
	installed, err := s.readSoftware(id)
	if err != nil {
		t.Fatal(err)
	}
	installed.Installation.Definition.Web = &serviceinstall.WebAccessDefinition{Scheme: "http", PortKey: "main"}
	original := jsonText(installed.Installation)
	if _, err = s.DB.Exec("UPDATE software_instances SET installation_json=? WHERE id=?", original, id); err != nil {
		t.Fatal(err)
	}
	resources := []serviceinstall.ResolvedResource{
		{Key: "site", Type: "PORT", Value: "8080", BindAddress: "127.0.0.1", Protocol: "TCP", IdentityHash: strings.Repeat("a", 64)},
		{Key: "main", Type: "PORT", Value: "9924", BindAddress: "0.0.0.0", Protocol: "TCP", IdentityHash: strings.Repeat("b", 64)},
	}
	_, err = s.DB.Exec(`INSERT INTO software_management_state(software_id,template_id,template_revision,definition_json,parameters_json,credential_refs_json,resources_json,version,updated_at) VALUES(?,?,?,'{}','{}','{}',?,1,?)`, id, template.ID, template.Revision, jsonText(resources), Now())
	if err != nil {
		t.Fatal(err)
	}
	view := dataOf(t, b.call("GET", "/software-instances/"+id, nil, 200))
	config := view["installation"].(map[string]any)["config"].(map[string]any)
	if config["bindAddress"] != "0.0.0.0" || config["hostPort"] != float64(9924) {
		t.Fatal("confirmed Web listener missing from response", config)
	}
	current, _ := s.readSoftware(id)
	if jsonText(current.Installation) != original || jsonText(installed.Installation) != original {
		t.Fatal("presentation changed the signed installation snapshot")
	}
	if _, err = s.DB.Exec("DELETE FROM software_management_state WHERE software_id=?", id); err != nil {
		t.Fatal(err)
	}
	view = dataOf(t, b.call("GET", "/software-instances/"+id, nil, 200))
	config = view["installation"].(map[string]any)["config"].(map[string]any)
	if config["hostPort"] == float64(9924) {
		t.Fatal("missing confirmation was guessed from an earlier view")
	}
}

func TestYtlabelConfigurationOperationIsEditableAndDeclaresItsWebListener(t *testing.T) {
	d := ytlabelPython312Definitions()[0]
	if err := serviceinstall.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	op, err := d.ManagementOperation("configure")
	if err != nil || op.ReadOnly || !op.Rebind || len(op.VerifySteps) == 0 || len(op.RollbackSteps) == 0 {
		t.Fatal("configuration operation is not explicit and recoverable", err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	installed := &serviceinstall.Request{Definition: d, SoftwareID: id, BindingRevision: 1, Source: serviceinstall.Source{Type: "ONLINE"}, Config: serviceinstall.Config{Name: "ytlabel", RuntimeMode: "NATIVE", BindAddress: "0.0.0.0", HostPort: 9924, MemoryMiB: 2048}}
	m := serviceinstall.ManagementRequest{Definition: d, SoftwareID: id, BindingRevision: 1, Root: "/opt/ops/software/test", OperationKey: "configure", Installation: installed, Parameters: map[string]string{"BIND_ADDRESS": "0.0.0.0", "WEB_PORT": "9924"}}
	resources, err := m.ResolveResources("ops-app-11111111-1111-4111-8111-111111111111.service")
	if err != nil {
		t.Fatal(err)
	}
	var listener bool
	for _, resource := range resources {
		listener = listener || resource.Key == "main" && resource.Type == "PORT" && resource.BindAddress == "0.0.0.0" && resource.Value == "9924"
	}
	if !listener {
		t.Fatal("network operation did not declare its actual listener")
	}
	for _, step := range append(append(op.Steps, op.VerifySteps...), op.RollbackSteps...) {
		command := exec.Command(additionalTestBash(t), "-n")
		command.Stdin = strings.NewReader(d.Management.ScriptPrelude + "\n" + step.Script)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("invalid network script: %v: %s", err, out)
		}
	}
}

func TestManagementPreflightPreservesResourceFailureCode(t *testing.T) {
	p := serviceinstall.ManagementPreflight{Checks: []serviceinstall.Check{{Status: "FAIL", Code: "SERVICE_PORT_UNAVAILABLE"}}}
	if managementPreflightError(p) != "SERVICE_PORT_UNAVAILABLE" {
		t.Fatal("management incorrectly required a Node error prefix")
	}
	p.Checks[0].Code = "bad\ncode"
	if managementPreflightError(p) != "MANAGEMENT_CHECK_FAILED" {
		t.Fatal("unsafe diagnostic code was accepted")
	}
}
