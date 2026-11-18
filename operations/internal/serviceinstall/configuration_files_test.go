package serviceinstall

import (
	"strings"
	"testing"
)

func TestConfigurationFileRequestValidation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name   string
		change func(*ConfigurationFileRequest)
		valid  bool
	}{
		{"save existing", func(f *ConfigurationFileRequest) {}, true},
		{"save new", func(f *ConfigurationFileRequest) { f.Revision = "ABSENT:" + hash }, true},
		{"bare absent", func(f *ConfigurationFileRequest) { f.Revision = "ABSENT" }, false},
		{"read", func(f *ConfigurationFileRequest) { *f = ConfigurationFileRequest{Name: "site", Action: "read"} }, true},
		{"external read", func(f *ConfigurationFileRequest) { *f = ConfigurationFileRequest{Name: "@main", Action: "read"} }, true},
		{"external write", func(f *ConfigurationFileRequest) { f.Name = "@main" }, false},
		{"external path", func(f *ConfigurationFileRequest) {
			*f = ConfigurationFileRequest{Name: "@../nginx.conf", Action: "read"}
		}, false},
		{"list", func(f *ConfigurationFileRequest) { *f = ConfigurationFileRequest{Action: "list"} }, true},
		{"list name", func(f *ConfigurationFileRequest) { *f = ConfigurationFileRequest{Action: "list", Name: "site"} }, false},
		{"path traversal", func(f *ConfigurationFileRequest) { f.Name = "../site" }, false},
		{"absolute path", func(f *ConfigurationFileRequest) { f.Name = "/etc/nginx/nginx.conf" }, false},
		{"name too long", func(f *ConfigurationFileRequest) { f.Name = strings.Repeat("a", 49) }, false},
		{"unknown action", func(f *ConfigurationFileRequest) { f.Action = "execute" }, false},
		{"invalid revision", func(f *ConfigurationFileRequest) { f.Revision = "outdated" }, false},
		{"invalid digest", func(f *ConfigurationFileRequest) { f.SHA256 = "invalid" }, false},
		{"missing envelope", func(f *ConfigurationFileRequest) { f.ContentEnvelope = nil }, false},
		{"short envelope", func(f *ConfigurationFileRequest) { f.ContentEnvelope = make([]byte, 28) }, false},
		{"maximum envelope", func(f *ConfigurationFileRequest) { f.ContentEnvelope = make([]byte, ConfigurationFileLimit+28) }, true},
		{"oversized envelope", func(f *ConfigurationFileRequest) { f.ContentEnvelope = make([]byte, ConfigurationFileLimit+29) }, false},
		{"enable", func(f *ConfigurationFileRequest) { f.Action, f.SHA256, f.ContentEnvelope = "enable", "", nil }, true},
		{"disable", func(f *ConfigurationFileRequest) { f.Action, f.SHA256, f.ContentEnvelope = "disable", "", nil }, true},
		{"delete", func(f *ConfigurationFileRequest) { f.Action, f.SHA256, f.ContentEnvelope = "delete", "", nil }, true},
		{"delete new revision", func(f *ConfigurationFileRequest) {
			f.Action, f.Revision, f.SHA256, f.ContentEnvelope = "delete", "ABSENT:"+hash, "", nil
		}, false},
		{"read envelope", func(f *ConfigurationFileRequest) { f.Action, f.Revision, f.SHA256 = "read", "", "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := ConfigurationFileRequest{Name: "site", Action: "save", Revision: hash, SHA256: hash, ContentEnvelope: make([]byte, 29)}
			tc.change(&f)
			if err := f.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid = %v, error = %v", tc.valid, err)
			}
		})
	}
}

func TestConfigurationFileBodyValidation(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		valid         bool
	}{
		{"LF and Chinese", "# 站点\nserver {\n\tlisten 80;\n}\n", true},
		{"maximum size", strings.Repeat("a", ConfigurationFileLimit), true},
		{"oversized", strings.Repeat("a", ConfigurationFileLimit+1), false},
		{"invalid UTF8", string([]byte{0xff}), false},
		{"CRLF", "server {}\r\n", false},
		{"NUL", "server {}\x00", false},
		{"blank", " \t\n", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ValidConfigurationFileBody(tc.content) != tc.valid {
				t.Fatalf("valid = %v", tc.valid)
			}
		})
	}
}

func configurationFileFixture(t *testing.T) ManagementRequest {
	t.Helper()
	for _, d := range BuiltinCommandCatalog() {
		if d.Key != "nginx" {
			continue
		}
		contract := Digest(BareDefinition(d))
		step := CommandStep{Name: "处理配置文件", Script: ConfigurationFileScript, RunAs: "ROOT", TimeoutSeconds: 90}
		d.Management = &ManagementDefinition{Operations: []ManagementOperation{
			{Key: "config-file-read", Name: "读取配置文件", ReadOnly: true, ResultFormat: "TEXT", Steps: []CommandStep{step}},
			{Key: "config-file-save", Name: "保存配置文件", ResultFormat: "TEXT", Steps: []CommandStep{step}, VerifySteps: []CommandStep{step}, RollbackSteps: []CommandStep{step}},
		}}
		r := ManagementRequest{Definition: d, SoftwareID: "11111111-1111-4111-8111-111111111111", BindingRevision: 1, Root: "/opt/demo", OperationKey: "config-file-save", ConfigFile: &ConfigurationFileRequest{
			Name: "site", Action: "save", Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), ContentEnvelope: make([]byte, 29),
		}}
		if err := r.Validate(); err != nil {
			t.Fatalf("invalid fixture: %v", err)
		}
		if Digest(BareDefinition(d)) != contract {
			t.Fatal("configuration operations changed the installation contract")
		}
		return r
	}
	t.Fatal("builtin nginx recipe missing")
	return ManagementRequest{}
}

func TestConfigurationFileOperationsStayFrozen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ManagementRequest)
	}{
		{"missing file", func(r *ManagementRequest) { r.ConfigFile = nil }},
		{"wrong software", func(r *ManagementRequest) { r.Definition.Key = "redis" }},
		{"nonbuiltin", func(r *ManagementRequest) { r.Definition.Commands.Builtin = false }},
		{"script", func(r *ManagementRequest) { r.Definition.Management.Operations[1].Steps[0].Script = "echo changed" }},
		{"account authority", func(r *ManagementRequest) { r.Definition.Management.Operations[1].Steps[0].RunAs = "ACCOUNT" }},
		{"parameters", func(r *ManagementRequest) {
			r.Definition.Management.Operations[1].Parameters = []ParameterDefinition{{Key: "PATH", Label: "路径", Type: "PATH"}}
		}},
		{"missing verification", func(r *ManagementRequest) { r.Definition.Management.Operations[1].VerifySteps = nil }},
		{"different rollback", func(r *ManagementRequest) { r.Definition.Management.Operations[1].RollbackSteps[0].Script = "true" }},
		{"rebind", func(r *ManagementRequest) { r.Definition.Management.Operations[1].Rebind = true }},
		{"save through read", func(r *ManagementRequest) { r.OperationKey = "config-file-read" }},
		{"read through save", func(r *ManagementRequest) { *r.ConfigFile = ConfigurationFileRequest{Name: "site", Action: "read"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := configurationFileFixture(t)
			tc.change(&r)
			if r.ValidateConfigurationFile() == nil {
				t.Fatal("modified configuration-file operation accepted")
			}
		})
	}
	r := configurationFileFixture(t)
	r.OperationKey, r.ConfigFile = "config-file-read", &ConfigurationFileRequest{Action: "list"}
	if err := r.Validate(); err != nil {
		t.Fatalf("read-only operation rejected: %v", err)
	}
}

func TestConfigurationFileHashCoversPayloadAndSkipsUnrelatedSecrets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ConfigurationFileRequest)
	}{
		{"name", func(f *ConfigurationFileRequest) { f.Name = "other" }},
		{"action", func(f *ConfigurationFileRequest) { f.Action = "delete" }},
		{"revision", func(f *ConfigurationFileRequest) { f.Revision = strings.Repeat("c", 64) }},
		{"digest", func(f *ConfigurationFileRequest) { f.SHA256 = strings.Repeat("c", 64) }},
		{"envelope", func(f *ConfigurationFileRequest) { f.ContentEnvelope[0] = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := configurationFileFixture(t)
			before := r.ParameterHash()
			tc.change(r.ConfigFile)
			if r.ParameterHash() == before {
				t.Fatal("changed configuration file retained the same parameter hash")
			}
		})
	}
	r := configurationFileFixture(t)
	r.ParameterCredentials = map[string]CredentialReference{"PASSWORD": {ID: r.SoftwareID, Revision: "1"}}
	if refs := r.CredentialReferences(); len(refs) != 0 {
		t.Fatalf("file operation requested unrelated credentials: %+v", refs)
	}
}

func TestConfigurationFileRequestsKeepThePublishedTemplateSizeLimit(t *testing.T) {
	r := configurationFileFixture(t)
	r.Definition.Commands.Steps[0].Script += strings.Repeat("x", 48<<10)
	if err := r.Validate(); err == nil || err.Error() != "COMMAND_TEMPLATE_TOO_LARGE" {
		t.Fatalf("file operations bypassed the template size limit: %v", err)
	}
}
