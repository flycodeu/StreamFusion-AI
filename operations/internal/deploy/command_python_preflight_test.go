package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestMissingCommandPythonBlocksBeforeInstallRoot(t *testing.T) {
	req := commandTestRequest()
	x := executor{root: t.TempDir(), softwareRun: func(time.Duration, string, ...string) ([]byte, error) {
		t.Fatal("missing interpreter reached command execution")
		return nil, nil
	}}
	req.Installation.Definition.Parameters = []serviceinstall.ParameterDefinition{{Key: "PYTHON_BINARY", Label: "Python", Type: "PATH", Required: true}}
	req.Installation.Config.Parameters = map[string]string{"PYTHON_BINARY": filepath.Join(x.root, "missing-python")}
	req.Installation.Definition.Commands.Steps[0].Script = "exit 0"
	root := serviceinstall.InstallRoot(x.root, req.Installation.SoftwareID, req.Installation.Config)
	if _, err := x.installCommands(req, filepath.Join(x.root, "jobs", req.JobID)); err == nil || err.Error() != "COMMAND_PYTHON_NOT_FOUND" {
		t.Fatalf("missing interpreter accepted: %v", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("missing interpreter created install directory: %v", err)
	}
}

func TestCommandPythonRequirementsApplyOnlyToExactRecipes(t *testing.T) {
	for _, test := range []struct {
		name, key string
		probe     commandPythonProbe
		want      string
	}{
		{"new ytlabel", "custom-ytlabel-py312-native-online", commandPythonProbe{Version: []int{3, 12, 3}}, ""},
		{"new offline rejects old Python", "custom-ytlabel-py312-native-offline", commandPythonProbe{Version: []int{3, 10, 12}}, "COMMAND_PYTHON_VERSION_UNSUPPORTED"},
		{"old ytlabel keeps its range", "custom-ytlabel-native-online", commandPythonProbe{Version: []int{3, 12, 3}}, "COMMAND_PYTHON_VERSION_UNSUPPORTED"},
		{"old offline accepts 3.10", "custom-ytlabel-native-offline", commandPythonProbe{Version: []int{3, 10, 12}}, ""},
		{"custom has no forced 3.12", "custom-user-ytlabel", commandPythonProbe{Version: []int{3, 11, 9}}, ""},
		{"ytlabel needs GL", "custom-ytlabel-py312-native-online", commandPythonProbe{Version: []int{3, 12, 3}, MissingLibraries: []string{"libGL.so.1"}}, "COMMAND_PYTHON_LIBRARY_MISSING"},
		{"custom does not require GL", "custom-user-python", commandPythonProbe{Version: []int{3, 11, 9}, MissingLibraries: []string{"libGL.so.1"}}, ""},
		{"ensurepip missing", "custom-user-python", commandPythonProbe{Version: []int{3, 11, 9}, MissingModules: []string{"ensurepip"}}, "COMMAND_PYTHON_MODULE_MISSING"},
		{"Label Studio uses current SQLite requirement", "custom-label-studio-native-online", commandPythonProbe{Version: []int{3, 12, 3}, SQLiteVersion: []int{3, 34, 1}}, "COMMAND_PYTHON_SQLITE_UNSUPPORTED"},
		{"Label Studio has no GL dependency", "custom-label-studio-native-offline", commandPythonProbe{Version: []int{3, 12, 3}, SQLiteVersion: []int{3, 45, 1}, MissingLibraries: []string{"libGL.so.1"}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := commandPythonChecks(test.key, test.probe)
			if len(checks) != 1 || test.want == "" && checks[0].Status != "PASS" || test.want != "" && checks[0].Code != test.want {
				t.Fatal(checks)
			}
		})
	}
}

func TestCommandPythonProbeUsesOnlyRequiredModulesAndWritesNoFiles(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python not available for the embedded probe")
	}
	for _, profile := range []string{"", "ytlabel", "label-studio"} {
		t.Run("profile-"+profile, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, python, "-I", "-S", "-B", "-c", deployScript("software/python-preflight.py"), profile)
			command.Dir = t.TempDir()
			data, err := command.CombinedOutput()
			var probe commandPythonProbe
			if err != nil || StrictJSON(data, &probe) != nil || len(probe.Version) != 3 || probe.Version[0] != 3 {
				t.Fatalf("probe output: %s %v", data, err)
			}
			if profile != "ytlabel" && len(probe.MissingLibraries) != 0 || profile != "label-studio" && len(probe.SQLiteVersion) != 0 {
				t.Fatal("probe examined unrelated dependencies", probe)
			}
			entries, err := os.ReadDir(command.Dir)
			if err != nil || len(entries) != 0 || strings.Contains(string(data), "Traceback") {
				t.Fatal("probe created files or returned unstructured output", entries, err)
			}
		})
	}
}
