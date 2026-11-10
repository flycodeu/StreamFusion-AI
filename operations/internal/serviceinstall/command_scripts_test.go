package serviceinstall

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeCommandScriptsKeepsFrozenDefinitionAndNonScriptFields(t *testing.T) {
	d := Definition{
		Name:     "Windows\r\nlabel",
		Commands: &CommandRecipe{Steps: []CommandStep{{Name: "安装", Script: "set -euo pipefail\r\nprintf 'install\\n'\r\n"}}},
		Management: &ManagementDefinition{Operations: []ManagementOperation{{
			Key: "check", Steps: []CommandStep{{Script: "printf check\r\n"}}, VerifySteps: []CommandStep{{Script: "printf 'literal\rcarriage'\r\n"}},
		}}},
	}
	before, _ := json.Marshal(d)
	normalized := NormalizeCommandScripts(d)
	after, _ := json.Marshal(d)
	if string(before) != string(after) {
		t.Fatal("normalization changed a shared frozen definition")
	}
	if normalized.Name != d.Name || normalized.Commands.Steps[0].Script != "set -euo pipefail\nprintf 'install\\n'\n" || normalized.Management.Operations[0].Steps[0].Script != "printf check\n" || normalized.Management.Operations[0].VerifySteps[0].Script != "printf 'literal\rcarriage'\n" {
		t.Fatalf("unexpected normalization: %+v", normalized)
	}
	if !reflect.DeepEqual(normalized, NormalizeCommandScripts(normalized)) {
		t.Fatal("normalization is not idempotent")
	}
	if got := NormalizeCommandScripts(Definition{}); got.Commands != nil || got.Management != nil {
		t.Fatal("normalization added commands to an empty definition")
	}
}
