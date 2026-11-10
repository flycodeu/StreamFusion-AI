package controller

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestServiceConfigurationPresetsAreBoundedAndKeepExistingOperations(t *testing.T) {
	bash := managementTestBash(t)
	for _, d := range serviceinstall.BuiltinCommandCatalog() {
		if d.Key != "nginx" && d.Key != "mysql" && d.Key != "redis" {
			continue
		}
		var original *serviceinstall.ManagementDefinition
		if d.Key == "nginx" {
			original = nginxManagementDefinition(d)
		} else if d.Key == "mysql" {
			original = mysqlManagementDefinition(d)
		}
		count := 0
		if original != nil {
			count = len(original.Operations)
		}
		d.Management = serviceConfigurationPreset(d, original)
		data, _ := json.Marshal(d)
		t.Logf("%s/%s definition=%d prelude=%d operations=%d", d.Key, d.Version, len(data), len(d.Management.ScriptPrelude), len(d.Management.Operations))
		if err := d.ValidateCommands(); err != nil {
			t.Fatalf("%s/%s: %v", d.Key, d.Version, err)
		}
		if len(d.Management.Operations) != count+2 {
			t.Fatal("existing operations were lost")
		}
		for _, op := range d.Management.Operations {
			if op.Key == "configure" && (!op.Rebind || len(op.VerifySteps) == 0 || len(op.RollbackSteps) == 0) {
				t.Fatal("configuration recovery is incomplete")
			}
			for _, steps := range [][]serviceinstall.CommandStep{op.Steps, op.VerifySteps, op.RollbackSteps} {
				for _, step := range steps {
					if strings.ContainsRune(step.Script, '\r') {
						t.Fatal("CRLF script")
					}
					cmd := exec.Command(bash, "-n")
					cmd.Stdin = strings.NewReader(d.Management.ScriptPrelude + "\n" + step.Script)
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("%s %s syntax: %v %s", d.Key, op.Key, err, output)
					}
				}
			}
		}
	}
}

func TestServiceConfigurationScriptsPreserveUnrelatedSettings(t *testing.T) {
	python := ytlabelTestPython(t)
	fixture, err := os.ReadFile(filepath.Join("testdata", "service-configuration.py"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "check.py")
	if err := os.WriteFile(file, fixture, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", file, filepath.Join("scripts", "service-configuration"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("configuration scenarios: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
