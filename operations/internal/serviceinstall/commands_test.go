package serviceinstall

import (
	"strings"
	"testing"
)

func TestCommandExamplesPinSourcesAndKeepJavaVersionsSeparate(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range CommandExamples() {
		if err := ValidateDefinition(d); err != nil {
			t.Fatalf("%s: %v", d.Key, err)
		}
		if seen[d.Key] {
			t.Fatal("duplicate template", d.Key)
		}
		seen[d.Key] = true
		if d.Commands.Kind == "TOOL" {
			if d.CatalogKey != "java" || d.Name != "Java" {
				t.Fatalf("Java grouping: %+v", d)
			}
			base, err := Find("java", d.Version)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(d.Commands.Steps[0].Script, base.Native.Parts[0].SHA256) {
				t.Fatal("template material not pinned to Java version")
			}
		} else if d.CatalogKey != "nginx" || d.Name != "Nginx" {
			t.Fatalf("Nginx grouping: %+v", d)
		}
	}
	for _, key := range []string{"custom-java-8-online", "custom-java-21-offline", "custom-nginx-online", "custom-nginx-offline"} {
		if !seen[key] {
			t.Fatal("missing template", key)
		}
	}
}

func TestCommandCatalogKeyIsOptionalDisplayMetadata(t *testing.T) {
	d := CommandExamples()[0]
	identity := d.Key
	for _, key := range []string{"", "java", "java-runtime-21", "a" + strings.Repeat("b", 63)} {
		d.CatalogKey = key
		if err := d.ValidateCommands(); err != nil {
			t.Fatalf("valid grouping %q: %v", key, err)
		}
		if d.Key != identity {
			t.Fatal("display grouping changed recipe identity")
		}
	}
	for _, key := range []string{"Java", "1java", "java runtime", "java_runtime", "java/runtime", "a" + strings.Repeat("b", 64)} {
		d.CatalogKey = key
		if err := d.ValidateCommands(); err == nil || err.Error() != "COMMAND_CATALOG_KEY_INVALID" {
			t.Fatalf("invalid grouping %q: %v", key, err)
		}
	}
}

func TestCustomCommandsCannotHideStepsOutsideTheirSinglePlan(t *testing.T) {
	d := CommandExamples()[0]
	step := &d.Commands.Steps[0]
	step.RuntimeMode, step.SourceModes = d.RuntimeModes[0], []string{d.SourceModes[0]}
	if err := d.ValidateCommands(); err != nil {
		t.Fatal("matching scope rejected", err)
	}
	step.RuntimeMode = "DOCKER"
	if err := d.ValidateCommands(); err == nil {
		t.Fatal("hidden runtime command accepted")
	}
	step.RuntimeMode, step.SourceModes = d.RuntimeModes[0], []string{"ARCHIVE"}
	if err := d.ValidateCommands(); err == nil {
		t.Fatal("hidden source command accepted")
	}
}

func TestCommandPortConfigurationMatchesDeclaredListener(t *testing.T) {
	var d Definition
	for _, example := range CommandExamples() {
		if example.Key == "custom-nginx-online" {
			d = example
		}
	}
	valid := Config{Name: "Nginx", RuntimeMode: "DOCKER", MemoryMiB: 512, BindAddress: "127.0.0.1", HostPort: 8080}
	if err := valid.Validate(d); err != nil {
		t.Fatal(err)
	}
	valid.BindAddress = "0.0.0.0"
	if err := valid.Validate(d); err != nil {
		t.Fatal("Web wildcard listener rejected", err)
	}
	for _, invalid := range []Config{
		{Name: valid.Name, RuntimeMode: valid.RuntimeMode, MemoryMiB: 512, BindAddress: "127.0.0.1", HostPort: -1},
		{Name: valid.Name, RuntimeMode: valid.RuntimeMode, MemoryMiB: 512, BindAddress: "224.0.0.1", HostPort: 8080},
		{Name: valid.Name, RuntimeMode: valid.RuntimeMode, MemoryMiB: 512, BindAddress: "not-an-ip", HostPort: 8080},
	} {
		if err := invalid.Validate(d); err == nil {
			t.Fatal("invalid listener accepted", invalid)
		}
	}
	d.Port = 0
	if err := valid.Validate(d); err == nil {
		t.Fatal("undeclared listener accepted")
	}
	d.Web, d.DefaultBindAddress = nil, ""
	valid.HostPort = 0
	if err := valid.Validate(d); err != nil {
		t.Fatal("service without listener rejected", err)
	}
}
