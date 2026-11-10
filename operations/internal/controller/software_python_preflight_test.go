package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestMissingPythonPreflightCreatesNoInstallPlanJobOrSoftware(t *testing.T) {
	s, b := setup(t)
	b.login()
	d := testCommandDefinition()
	d.Commands.Steps[1].RunAs = "ROOT"
	d.Parameters = []serviceinstall.ParameterDefinition{{Key: "PYTHON_BINARY", Label: "Python", Type: "PATH", Required: true}}
	saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
	b.call("POST", "/command-templates/"+saved["id"].(string)+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200)
	host, fixture := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		if req.Action != "SOFTWARE_INSTALL_CHECK" {
			return nil, false
		}
		checked := serviceinstall.Preflight{Ready: false, Checks: []serviceinstall.Check{{Key: "PYTHON", Status: "FAIL", Code: "COMMAND_PYTHON_NOT_FOUND", Message: "所选 Python 不存在，请先安装兼容版本或选择已有解释器"}}}
		data, _ := json.Marshal(checked)
		return data, true
	})
	counts := func() string {
		t.Helper()
		var result string
		if err := s.DB.QueryRow("SELECT (SELECT count(*) FROM jobs)||':'||(SELECT count(*) FROM software_install_plans)||':'||(SELECT count(*) FROM software_instances)").Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := counts()
	response := b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{
		"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision,
		"config": serviceinstall.Config{Name: "demo", RuntimeMode: "NATIVE", MemoryMiB: 256, Parameters: map[string]string{"PYTHON_BINARY": "/usr/bin/python3.10"}},
		"source": map[string]string{"type": "ONLINE"},
	}, 409)
	if !strings.Contains(response.Body.String(), "COMMAND_PYTHON_NOT_FOUND") || !strings.Contains(response.Body.String(), "所选 Python 不存在") || counts() != before || fixture.InstallCount.Load() != 0 {
		t.Fatal("missing dependency became an installation task", response.Body.String())
	}
}
