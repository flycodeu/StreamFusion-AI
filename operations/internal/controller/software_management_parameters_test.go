package controller

import (
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestConfigurationReadsSaveActualValuesWithoutApplyingDraftParameters(t *testing.T) {
	p := storedSoftwareOperationPlan{softwareOperationPlan: softwareOperationPlan{Operation: serviceinstall.ManagementOperation{Key: "read-config", ReadOnly: true}, Management: serviceinstall.ManagementRequest{Definition: serviceinstall.Definition{Management: &serviceinstall.ManagementDefinition{Parameters: []serviceinstall.ParameterDefinition{{Key: "PUBLIC_URL", Label: "访问地址", Type: "TEXT"}, {Key: "WEB_PORT", Label: "端口", Type: "PORT", Default: "8080"}}}}, Parameters: map[string]string{"PUBLIC_URL": "https://draft.invalid", "WEB_PORT": "9000"}}}}
	before := softwareManagementState{Parameters: map[string]string{"PUBLIC_URL": "https://current.invalid", "WEB_PORT": "8080"}}
	if got := confirmedManagementParameters(p, before, nil); got["PUBLIC_URL"] != before.Parameters["PUBLIC_URL"] || got["WEB_PORT"] != "8080" {
		t.Fatal("read request saved an unsubmitted draft", got)
	}
	evidence := &serviceinstall.ManagementEvidence{Table: &serviceinstall.ManagementTable{Columns: []string{"key", "value"}, Rows: []map[string]string{{"key": "PUBLIC_URL", "value": "https://actual.invalid"}, {"key": "WEB_PORT", "value": "8800"}}}}
	if got := confirmedManagementParameters(p, before, evidence); got["PUBLIC_URL"] != "https://actual.invalid" || got["WEB_PORT"] != "8800" {
		t.Fatal("actual configuration was not recorded", got)
	}
	evidence.Table.Rows[1]["value"] = "invalid"
	if got := confirmedManagementParameters(p, before, evidence); got["WEB_PORT"] != "8080" {
		t.Fatal("invalid observed values were saved", got)
	}
}
