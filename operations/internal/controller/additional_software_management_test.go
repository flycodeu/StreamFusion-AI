package controller

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdditionalConfigurationDefinitionsHaveRealReadWriteVerificationAndRollback(t *testing.T) {
	for _, d := range additionalSoftwareDefinitions() {
		m := d.Management
		if err := d.ValidateCommands(); err != nil {
			t.Fatal(d.Key, err)
		}
		read, readErr := d.ManagementOperation("read-config")
		write, writeErr := d.ManagementOperation("configure")
		if readErr != nil || writeErr != nil || !read.ReadOnly || read.ResultFormat != "TABLE" || !write.Rebind || len(write.VerifySteps) != 1 || len(write.RollbackSteps) != 1 {
			t.Fatal(d.Key, "incomplete configuration operations")
		}
		if len(m.ScriptPrelude) > 32<<10 {
			t.Fatal(d.Key, "oversized prelude", len(m.ScriptPrelude))
		}
		raw, _ := json.Marshal(d)
		if len(raw) > 48<<10 {
			t.Fatal(d.Key, "oversized definition", len(raw))
		}
		for _, field := range m.Parameters {
			if field.Type == "CREDENTIAL" || strings.Contains(field.Key, "PASSWORD") {
				t.Fatal(d.Key, "configuration requests initialization credential")
			}
			if field.Key == "MEMORY_MIB" && (field.Min == nil || *field.Min != 0) {
				t.Fatal(d.Key, "unlimited memory not preserved")
			}
		}
	}
}

func TestAdditionalConfigurationAdaptersPreserveDataSecretsAndStoppedStateAndRollback(t *testing.T) {
	python := ytlabelTestPython(t)
	for _, app := range additionalSoftwareCatalog {
		for _, mode := range []string{"NATIVE", "DOCKER"} {
			for _, state := range []string{"RUNNING", "STOPPED"} {
				t.Run(app.key+"/"+mode+"/"+state, func(t *testing.T) {
					prelude := additionalConfiguration(app, mode).ScriptPrelude
					_, program, ok := strings.Cut(prelude, "<<'OPS_ADDITIONAL_CONFIG_PY'\n")
					if !ok {
						t.Fatal("missing source body")
					}
					program, _, _ = strings.Cut(program, "\nOPS_ADDITIONAL_CONFIG_PY")
					directory := t.TempDir()
					path := filepath.Join(directory, "configuration.py")
					if err := os.WriteFile(path, []byte(program), 0600); err != nil {
						t.Fatal(err)
					}
					cmd := exec.Command(python, "-I", "testdata/additional_configuration_fixture.py", path, filepath.Join(directory, "installation"), app.key, mode, state)
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("adapter failed: %v\n%s", err, output)
					}
				})
			}
		}
	}
}
