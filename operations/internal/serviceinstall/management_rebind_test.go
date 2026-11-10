package serviceinstall

import (
	"encoding/json"
	"strings"
	"testing"
)

func rebindManagementFixture() ManagementRequest {
	d := CommandExamples()[0]
	d.Kind, d.Commands.Kind, d.Commands.Locator, d.Commands.BinaryPath = "SERVICE", "SYSTEMD", "ops-${OPS_SOFTWARE_ID}.service", ""
	d.Management = &ManagementDefinition{ScriptPrelude: "message() { printf ready; }", Operations: []ManagementOperation{{Key: "configure", Name: "配置", Rebind: true, Steps: []CommandStep{{Name: "应用", Script: "message", RunAs: "ROOT", TimeoutSeconds: 10}}, VerifySteps: []CommandStep{{Name: "验证", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10}}, RollbackSteps: []CommandStep{{Name: "恢复", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10}}, ResultFormat: "TEXT"}}}
	i := &Request{SoftwareID: "11111111-1111-4111-8111-111111111111", BindingRevision: 2, Definition: d, Config: Config{Name: "demo", RuntimeMode: "NATIVE", MemoryMiB: 512}, Source: Source{Type: "ONLINE"}}
	return ManagementRequest{Installation: i, Definition: d, SoftwareID: i.SoftwareID, BindingRevision: i.BindingRevision, Root: "/opt/demo", OperationKey: "configure"}
}

func TestManagementRebindRequiresExplicitSafeContractAndFrozenInstallation(t *testing.T) {
	r := rebindManagementFixture()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ManagementRequest){
		"ordinary operation": func(r *ManagementRequest) {
			r.Definition.Management.Operations[0].Key, r.OperationKey = "restart", "restart"
		},
		"read only":           func(r *ManagementRequest) { r.Definition.Management.Operations[0].ReadOnly = true },
		"missing rollback":    func(r *ManagementRequest) { r.Definition.Management.Operations[0].RollbackSteps = nil },
		"missing verify":      func(r *ManagementRequest) { r.Definition.Management.Operations[0].VerifySteps = nil },
		"implicit rollback":   func(r *ManagementRequest) { r.Definition.Management.Operations[0].Rebind = false },
		"installation absent": func(r *ManagementRequest) { r.Installation = nil },
		"revision":            func(r *ManagementRequest) { r.Installation.BindingRevision++ },
		"locator":             func(r *ManagementRequest) { r.Installation.Definition.Commands.Locator = "another.service" },
		"binary":              func(r *ManagementRequest) { r.Installation.Definition.Commands.BinaryPath = "/opt/another" },
		"oversized prelude":   func(r *ManagementRequest) { r.Definition.Management.ScriptPrelude = strings.Repeat("x", 32769) },
		"NUL prelude":         func(r *ManagementRequest) { r.Definition.Management.ScriptPrelude = "bad\x00source" },
	} {
		t.Run(name, func(t *testing.T) {
			var changed ManagementRequest
			data, _ := json.Marshal(r)
			json.Unmarshal(data, &changed)
			mutate(&changed)
			if changed.Validate() == nil {
				t.Fatal("invalid rebind contract accepted")
			}
		})
	}
	r.Definition.Management.Operations[0].Rebind = false
	r.Definition.Management.Operations[0].RollbackSteps = nil
	if r.Validate() == nil {
		t.Fatal("ordinary management accepted a frozen installation")
	}
}

func TestManagementRebindReceiptKeepsOriginalConfigAndAdvancesOnlyBinding(t *testing.T) {
	r := rebindManagementFixture()
	e := Evidence{SoftwareID: r.SoftwareID, BindingRevision: 3, RecipeKey: r.Installation.Definition.Key, RecipeRevision: r.Installation.Definition.Revision, ConfigHash: Digest(r.Installation.Config), Kind: "SYSTEMD", Locator: "ops-" + r.SoftwareID + ".service", IdentityHash: strings.Repeat("a", 64), Health: "READY"}
	if err := r.ValidateRebindEvidence(e); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Evidence){func(e *Evidence) { e.BindingRevision = 2 }, func(e *Evidence) { e.ConfigHash = strings.Repeat("b", 64) }, func(e *Evidence) { e.Locator = "another.service" }} {
		bad := e
		change(&bad)
		if r.ValidateRebindEvidence(bad) == nil {
			t.Fatal("unrelated binding receipt accepted")
		}
	}
	if r.Installation.BindingRevision != 2 {
		t.Fatal("receipt validation changed frozen input")
	}
}

func TestNormalizeManagementPreludeAndRollbackDoesNotMutateFrozenInput(t *testing.T) {
	r := rebindManagementFixture()
	r.Definition.Management.ScriptPrelude = "function source() { :; }\r\n"
	r.Definition.Management.Operations[0].RollbackSteps[0].Script = "restore\r\n"
	normalized := NormalizeCommandScripts(r.Definition)
	if strings.Contains(normalized.Management.ScriptPrelude, "\r") || strings.Contains(normalized.Management.Operations[0].RollbackSteps[0].Script, "\r") || !strings.Contains(r.Definition.Management.ScriptPrelude, "\r") || !strings.Contains(r.Definition.Management.Operations[0].RollbackSteps[0].Script, "\r") {
		t.Fatal("normalization changed frozen input or retained CRLF")
	}
}
