package controller

import (
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestAnnotationStepTitlesUpgradeOnlyUntouchedStock(t *testing.T) {
	for _, seed := range annotationSoftwareDefinitions() {
		old := serviceinstall.NormalizeCommandScripts(seed)
		for i := range old.Management.Operations {
			op := &old.Management.Operations[i]
			for _, steps := range [][]serviceinstall.CommandStep{op.Steps, op.VerifySteps, op.RollbackSteps} {
				for index := range steps {
					for action, name := range annotationStepNames {
						if steps[index].Name == name {
							steps[index].Name = action
							break
						}
					}
				}
			}
		}
		if !annotationConfigurationUpgrade(seed, old) {
			t.Fatal("stock titles were not upgraded", seed.Key)
		}
		if annotationConfigurationUpgrade(seed, seed) {
			t.Fatal("current template is not idempotent", seed.Key)
		}
		old.Management.Operations[0].Steps[0].Script += "\nprintf administrator-edited\n"
		if annotationConfigurationUpgrade(seed, old) {
			t.Fatal("administrator changes were overwritten", seed.Key)
		}
		if err := seed.ValidateCommands(); err != nil {
			t.Fatal("translated template exceeds its bounds", seed.Key, err)
		}
	}
}
