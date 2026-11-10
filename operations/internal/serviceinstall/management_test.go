package serviceinstall

import (
	"encoding/json"
	"testing"
)

func managementFixture() Definition {
	d := CommandExamples()[0]
	d.Management = &ManagementDefinition{Parameters: []ParameterDefinition{{Key: "CONFIG_FILE", Label: "配置文件", Type: "PATH", Default: "/opt/demo/config/demo.conf"}}, Operations: []ManagementOperation{{Key: "update-config", Name: "修改配置", Parameters: []ParameterDefinition{{Key: "VALUE", Label: "值", Type: "TEXT", Required: true}}, Steps: []CommandStep{{Name: "写配置", Script: `printf '%s' "$OPS_PARAM_VALUE"`, RunAs: "ROOT", TimeoutSeconds: 10}}, VerifySteps: []CommandStep{{Name: "核验", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10}}, ResultFormat: "TEXT"}}, Resources: []ResourceDefinition{{Key: "config", Type: "PATH_WRITE", Value: "${OPS_PARAM_CONFIG_FILE}"}}}
	return d
}

func TestManagementParametersAreTypedAndCredentialReferencesStaySeparate(t *testing.T) {
	min, max := int64(1), int64(16)
	defs := []ParameterDefinition{{Key: "TEXT", Label: "名称", Type: "TEXT", Required: true}, {Key: "COUNT", Label: "数量", Type: "INTEGER", Default: "4", Min: &min, Max: &max}, {Key: "ON", Label: "启用", Type: "BOOLEAN", Default: "false"}, {Key: "MODE", Label: "模式", Type: "SELECT", Options: []string{"a", "b"}, Default: "a"}, {Key: "FILE", Label: "目录", Type: "PATH", Default: "/opt/demo"}, {Key: "PORT", Label: "端口", Type: "PORT", Default: "8080"}, {Key: "PASSWORD", Label: "密码", Type: "CREDENTIAL", Required: true}}
	ref := CredentialReference{ID: "11111111-1111-4111-8111-111111111111", Revision: "1"}
	literal := "$(touch /tmp/should-not-exist); 'quoted'\nsecond line"
	values, err := ResolveParameters(defs, map[string]string{"TEXT": literal}, map[string]CredentialReference{"PASSWORD": ref})
	if err != nil || values["TEXT"] != literal || values["COUNT"] != "4" || values["ON"] != "false" {
		t.Fatalf("literal or defaults changed: %+v %v", values, err)
	}
	if _, ok := values["PASSWORD"]; ok {
		t.Fatal("credential leaked into public parameters")
	}
	for _, tc := range []map[string]string{{"TEXT": "a", "UNKNOWN": "x"}, {"TEXT": "a", "PASSWORD": "plaintext"}, {"TEXT": "a", "COUNT": "17"}, {"TEXT": "a", "ON": "yes"}, {"TEXT": "a", "MODE": "c"}, {"TEXT": "a", "FILE": "../relative"}, {"TEXT": "a", "PORT": "65536"}} {
		if _, err := ResolveParameters(defs, tc, map[string]CredentialReference{"PASSWORD": ref}); err == nil {
			t.Fatalf("invalid values accepted: %+v", tc)
		}
	}
	if _, err := ResolveParameters(defs, map[string]string{"TEXT": "a"}, nil); err == nil {
		t.Fatal("required credential omitted")
	}
	if ValidateCredentialValues([]CredentialReference{ref}, map[string]string{SecretKey(ref.ID, ref.Revision): "secret"}) != nil {
		t.Fatal("declared secret rejected")
	}
	if ValidateCredentialValues([]CredentialReference{ref}, map[string]string{"other:1": "secret"}) == nil {
		t.Fatal("unrelated secret accepted")
	}
	if ValidateCredentialSubset([]CredentialReference{ref}, nil) != nil {
		t.Fatal("historical acknowledgement depended on live credential")
	}
}

func TestManagementOperationRequiresPublishedVerificationAndNoParameterShadowing(t *testing.T) {
	d := managementFixture()
	if err := ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	d.Management.Operations[0].VerifySteps = nil
	if ValidateDefinition(d) == nil {
		t.Fatal("mutating operation omitted verification")
	}
	d.Management.Operations[0].ReadOnly = true
	if err := ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	d.Management.Operations[0].Parameters = append(d.Management.Operations[0].Parameters, ParameterDefinition{Key: "CONFIG_FILE", Label: "遮蔽", Type: "TEXT"})
	if ValidateDefinition(d) == nil {
		t.Fatal("one-off input shadowed instance setting")
	}
	d = managementFixture()
	d.Management.Operations[0].Steps[0].RunAs = "administrator"
	if ValidateDefinition(d) == nil {
		t.Fatal("unknown execution authority accepted")
	}
}

func TestManagementResourcesUseLiteralVariablesAndDetectOverlap(t *testing.T) {
	d := managementFixture()
	r := ManagementRequest{Definition: d, SoftwareID: "11111111-1111-4111-8111-111111111111", BindingRevision: 1, Root: "/opt/demo", OperationKey: "update-config", OperationParameters: map[string]string{"VALUE": "$(id)"}}
	resources, err := r.ResolveResources("demo.service")
	if err != nil || len(resources) != 1 || resources[0].Value != "/opt/demo/config/demo.conf" {
		t.Fatalf("resources %+v %v", resources, err)
	}
	r.Definition.Management.Resources[0].Value = "${OPS_SECRET_PASSWORD}"
	if _, err := r.ResolveResources("demo.service"); err == nil {
		t.Fatal("secret used as resource locator")
	}
	for _, tc := range []struct {
		a, b     ResolvedResource
		conflict bool
	}{
		{ResolvedResource{Type: "PATH_WRITE", Value: "/opt/demo"}, ResolvedResource{Type: "PATH_READ", Value: "/opt/demo/config"}, true},
		{ResolvedResource{Type: "PATH_READ", Value: "/opt/demo"}, ResolvedResource{Type: "PATH_READ", Value: "/opt/demo"}, false},
		{ResolvedResource{Type: "PATH_WRITE", Value: "/opt/demo"}, ResolvedResource{Type: "PATH_WRITE", Value: "/opt/demo2"}, false},
		{ResolvedResource{Type: "PORT", Value: "8080", Protocol: "TCP", BindAddress: "0.0.0.0"}, ResolvedResource{Type: "PORT", Value: "8080", Protocol: "TCP", BindAddress: "127.0.0.1"}, true},
		{ResolvedResource{Type: "PORT", Value: "8080", Protocol: "UDP", BindAddress: "127.0.0.1"}, ResolvedResource{Type: "PORT", Value: "8080", Protocol: "TCP", BindAddress: "127.0.0.1"}, false},
	} {
		if ResourceConflict(tc.a, tc.b) != tc.conflict {
			t.Fatalf("wrong resource conflict %+v %+v", tc.a, tc.b)
		}
	}
}

func TestBuiltinManagementDoesNotChangeMaterialOrFrozenInstallationContract(t *testing.T) {
	d := BuiltinCommandCatalog()[0]
	before := BareDefinition(d)
	raw, _ := json.Marshal(d)
	d.Management = managementFixture().Management
	d.Parameters = []ParameterDefinition{{Key: "NOTE", Label: "备注", Type: "TEXT"}}
	if err := ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	if Digest(BareDefinition(d)) != Digest(before) {
		t.Fatal("management edit changed vendor material identity")
	}
	var frozen Definition
	if json.Unmarshal(raw, &frozen) != nil || frozen.Management != nil || len(frozen.Parameters) != 0 {
		t.Fatal("historical snapshot changed")
	}
	d.Image = "different"
	if ValidateDefinition(d) == nil {
		t.Fatal("management metadata bypassed vendor install contract")
	}
}

func TestCustomMultipleDeclaredPortsKeepHostMappingsExplicit(t *testing.T) {
	var d Definition
	for _, example := range CommandExamples() {
		if example.Commands.Kind == "DOCKER" {
			d = example
			break
		}
	}
	d.Port = 8080
	d.Ports = []PortDescriptor{{Key: "main", Label: "HTTP", Protocol: "TCP", ContainerPort: 8080, DefaultPort: 8080, Required: true}, {Key: "grpc", Label: "RPC", Protocol: "TCP", ContainerPort: 9090, DefaultPort: 9090, Required: true}}
	c := Config{Name: "自定义", RuntimeMode: d.RuntimeModes[0], MemoryMiB: 512, BindAddress: "127.0.0.1", HostPort: 18080, Ports: map[string]int{"grpc": 19090}}
	if err := c.Validate(d); err != nil {
		t.Fatal(err)
	}
	mappings := PortMappings(d, c)
	if len(mappings) != 2 || mappings[0].HostPort != 18080 || mappings[1].HostPort != 19090 {
		t.Fatalf("wrong mappings %+v", mappings)
	}
	c.Ports["grpc"] = 18080
	if c.Validate(d) == nil {
		t.Fatal("duplicate TCP listener accepted")
	}
	d.Ports[1].Key = "rpc-port"
	if d.ValidateCommands() == nil {
		t.Fatal("port key could not form a shell environment name")
	}
	d.Ports[1].Key = "MAIN"
	if d.ValidateCommands() == nil {
		t.Fatal("case-insensitive port environment collision accepted")
	}
}

func TestManagementOperationResourcesRuntimeAndExplicitRelease(t *testing.T) {
	d := managementFixture()
	d.DefaultMemoryMiB = 2048
	op := &d.Management.Operations[0]
	op.Parameters = append(op.Parameters, ParameterDefinition{Key: "LISTEN", Label: "监听", Type: "PORT", Default: "8080"})
	op.RuntimeModes = []string{d.RuntimeModes[0]}
	op.Resources = []ResourceDefinition{{Key: "site-port", Type: "PORT", Value: "${OPS_PARAM_LISTEN}", BindAddress: "0.0.0.0", Protocol: "TCP"}}
	r := ManagementRequest{Definition: d, SoftwareID: "11111111-1111-4111-8111-111111111111", BindingRevision: 1, Root: "/opt/demo", OperationKey: op.Key, OperationParameters: map[string]string{"VALUE": "config"}}
	resources, err := r.ResolveResources("demo.service")
	if err != nil || len(resources) != 2 || resources[1].Value != "8080" || !op.SupportsRuntime(d.RuntimeModes[0]) || op.SupportsRuntime("unsupported") {
		t.Fatalf("operation-specific resource or runtime lost: %+v %v", resources, err)
	}
	op.Resources[0].Release = true
	if err := d.ValidateManagement(); err != nil {
		t.Fatal(err)
	}
	op.ReadOnly = true
	if d.ValidateManagement() == nil {
		t.Fatal("read-only operation released a claim")
	}
	op.ReadOnly = false
	d.Management.Resources[0].Release = true
	if d.ValidateManagement() == nil {
		t.Fatal("global definition released a claim")
	}
	d.Management.Resources[0].Release = false
	d.DefaultMemoryMiB = 127
	if d.ValidateManagement() == nil {
		t.Fatal("invalid memory default accepted")
	}
	prior := []ResolvedResource{{Key: "site", Type: "PORT", Value: "80", Protocol: "TCP", BindAddress: "0.0.0.0"}, {Key: "site", Type: "PORT", Value: "81", Protocol: "TCP", BindAddress: "0.0.0.0"}}
	changes := []ResolvedResource{{Key: "renamed", Type: "PORT", Value: "80", Protocol: "TCP", BindAddress: "0.0.0.0", Release: true}, {Key: "site", Type: "PORT", Value: "82", Protocol: "TCP", BindAddress: "0.0.0.0"}}
	merged, err := MergeResourceClaims(prior, changes)
	if err != nil || len(merged) != 2 || merged[0].Value != "81" || merged[1].Value != "82" || len(prior) != 2 {
		t.Fatalf("resource address merge overwrote another site: %+v %v", merged, err)
	}
}
