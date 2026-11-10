package deploy

import (
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestWebMetadataRequiresNewExecutorWithoutUpgradingUnchangedRecipes(t *testing.T) {
	d := serviceinstall.Definition{}
	if RequiredCommandRecipeVersion(d) != 3 {
		t.Fatal("unchanged simple recipes needlessly require an update")
	}
	d.Parameters = []serviceinstall.ParameterDefinition{{Key: "NAME", Type: "TEXT"}}
	if RequiredCommandRecipeVersion(d) != 4 {
		t.Fatal("existing parameter recipes needlessly require an update")
	}
	d.Web = &serviceinstall.WebAccessDefinition{Scheme: "http"}
	if RequiredCommandRecipeVersion(d) != 5 {
		t.Fatal("new definition fields can reach an executor that rejects them")
	}
	d.Parameters = []serviceinstall.ParameterDefinition{{Key: "PYTHON_BINARY", Type: "PATH"}}
	if RequiredCommandRecipeVersion(d) != 6 || CommandRecipeVersion != 9 {
		t.Fatal("Python dependency checks can reach an executor that cannot perform them")
	}
	d.DefaultBindAddress = "0.0.0.0"
	if RequiredCommandRecipeVersion(d) != 7 {
		t.Fatal("remote Web listener can reach an executor that rejects it")
	}
}
