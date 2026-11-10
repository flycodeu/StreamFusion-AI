package serviceinstall

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestAllBuiltinsHaveEditableCommandsAndPreserveMaterialIdentity(t *testing.T) {
	all := BuiltinCommandCatalog()
	if len(all) != 33 {
		t.Fatalf("catalog has %d definitions", len(all))
	}
	for _, d := range all {
		t.Run(d.Key+"/"+d.Version, func(t *testing.T) {
			raw, _ := json.Marshal(d)
			if len(raw) > 48<<10 {
				t.Fatalf("template too large: %d", len(raw))
			}
			if err := ValidateDefinition(d); err != nil {
				t.Fatal(err)
			}
			base, err := builtinBase(d.Key, d.Version)
			if err != nil || !reflect.DeepEqual(base, BareDefinition(d)) {
				t.Fatal("original material/definition changed")
			}
			for _, mode := range d.RuntimeModes {
				phases := map[string]bool{}
				ids := map[string]bool{}
				for _, step := range d.Commands.Steps {
					if strings.Contains(step.Script, "\r\n") {
						t.Fatalf("command %s contains Windows line endings", step.ID)
					}
					if ids[step.ID] {
						t.Fatalf("duplicate command point %s", step.ID)
					}
					ids[step.ID] = true
					if step.RuntimeMode == "" || step.RuntimeMode == mode {
						phases[step.Phase] = true
					}
					if strings.Contains(step.Script, "execute-builtin") || strings.TrimSpace(step.Script) == "$@" {
						t.Fatal("opaque command placeholder")
					}
				}
				for _, phase := range []string{"INSTALL", "VERIFY", "UNINSTALL"} {
					if !phases[phase] && !(d.Key == "npm" && phase == "UNINSTALL") {
						t.Fatalf("%s lacks %s", mode, phase)
					}
				}
			}
			for index := range d.Commands.Steps {
				if d.Commands.Steps[index].Kind != "SYSTEM" {
					d.Commands.Steps[index].Script += "\nprintf 'edited\\n'"
					break
				}
			}
			d.Commands.Revision++
			if err := ValidateDefinition(d); err != nil {
				t.Fatalf("valid edit rejected: %v", err)
			}
			d.Version = "tampered"
			if ValidateDefinition(d) == nil {
				t.Fatal("fixed material identity can be edited")
			}
		})
	}
}

func TestBuiltinMySQLRetainsCredentialValidation(t *testing.T) {
	for _, d := range BuiltinCommandCatalog() {
		if d.Key == "mysql" && !IsOfficial(d) {
			if ValidateSecrets(d, Config{}, nil) == nil {
				t.Fatal("built-in command metadata bypassed MySQL credentials")
			}
			return
		}
	}
	t.Fatal("mysql missing")
}

func TestBuiltinNodeScriptsKeepExistingProtection(t *testing.T) {
	for _, d := range BuiltinCommandCatalog() {
		if d.Key == "node" {
			steps := map[string]CommandStep{}
			for _, step := range d.Commands.Steps {
				steps[step.ID] = step
			}
			if !strings.Contains(steps["NODE_INSTALL"].Script, "nvm install --offline") || !strings.Contains(steps["NODE_UNINSTALL"].Script, "NODE_VERSION_RUNNING") || !strings.Contains(steps["DELETE_DATA"].Script, "check_mounts") || steps["NODE_STAGE"].Script == "" || steps["NODE_VERIFY_NPM"].Script == "" {
				t.Fatal("Node lifecycle protection dropped")
			}
		}
	}
}

func TestBuiltinShellSyntax(t *testing.T) {
	bash := "/bin/bash"
	if runtime.GOOS == "windows" {
		git, err := exec.LookPath("git")
		if err != nil {
			t.Skip("Git Bash unavailable")
		}
		bash = filepath.Join(filepath.Dir(filepath.Dir(git)), "usr", "bin", "bash.exe")
	}
	scripts := strings.Builder{}
	index := 0
	for _, d := range BuiltinCommandCatalog() {
		for _, step := range d.Commands.Steps {
			if step.Kind == "SYSTEM" {
				continue
			}
			fmt.Fprintf(&scripts, "step_%d() {\n%s\n}\n", index, step.Script)
			index++
		}
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(scripts.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command syntax: %v\n%s", err, output)
	}
}
