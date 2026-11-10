package serviceinstall

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBuiltinFlowAllowsOrderedCommandsAndProtectsConfirmation(t *testing.T) {
	d, _ := FindOfficial("java", "21-ubuntu24.04-repository")
	d.Commands = builtinCommands(d)
	d, err := UpgradeBuiltinCommandFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	step := CommandStep{ID: "custom-download", Kind: "COMMAND", Name: "准备项目文件", Phase: "INSTALL", RuntimeMode: "NATIVE", RunAs: "ROOT", Script: "printf download", TimeoutSeconds: 30}
	index := slices.IndexFunc(d.Commands.Steps, func(s CommandStep) bool { return s.Action == "INSTALL_CONFIRM" })
	d.Commands.Steps = slices.Insert(d.Commands.Steps, index, step)
	if err = ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	d.Commands.Steps[index], d.Commands.Steps[index-1] = d.Commands.Steps[index-1], d.Commands.Steps[index]
	if err = ValidateDefinition(d); err != nil {
		t.Fatalf("command reorder rejected: %v", err)
	}
	d.Commands.Steps = slices.DeleteFunc(d.Commands.Steps, func(s CommandStep) bool { return s.ID == "VERIFY" })
	if err = ValidateDefinition(d); err != nil {
		t.Fatalf("optional verification removal rejected: %v", err)
	}
	d.Commands.Steps = append(d.Commands.Steps, step)
	d.Commands.Steps[len(d.Commands.Steps)-1].ID = "after-confirm"
	if ValidateDefinition(d) == nil {
		t.Fatal("command after confirmation accepted")
	}
	d.Commands.Steps = d.Commands.Steps[:len(d.Commands.Steps)-1]
	d.Commands.Steps = slices.DeleteFunc(d.Commands.Steps, func(s CommandStep) bool { return s.Action == "OFFICIAL_PLAN" })
	if ValidateDefinition(d) == nil {
		t.Fatal("package-plan protection removal accepted")
	}
}

func TestFlowMigrationKeepsEditedNodeCommandsAndMaterial(t *testing.T) {
	d := NodeCatalog()[0]
	d.Commands = builtinCommands(d)
	d.Commands.Steps[0].Script += "\nprintf edited"
	before := d.Commands.Steps[0]
	flow, err := UpgradeBuiltinCommandFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	step := flow.Commands.Steps[slices.IndexFunc(flow.Commands.Steps, func(s CommandStep) bool { return s.ID == before.ID })]
	if step.Script != before.Script || step.RunAs != before.RunAs || !reflect.DeepEqual(BareDefinition(d), BareDefinition(flow)) {
		t.Fatal("migration changed saved command or material")
	}
	for _, s := range flow.Commands.Steps {
		if s.ID == "NODE_STAGE" {
			t.Fatal("edited install was split")
		}
	}
	if err = ValidateDefinition(flow); err != nil {
		t.Fatal(err)
	}
}

func TestCustomSequentialFlowDoesNotRequireSeparateVerifyPhase(t *testing.T) {
	for _, d := range CommandExamples() {
		d.Commands.FlowVersion = 1
		for index := range d.Commands.Steps {
			if d.Commands.Steps[index].Phase == "VERIFY" {
				d.Commands.Steps[index].Phase = "INSTALL"
			}
		}
		if err := ValidateDefinition(d); err != nil {
			t.Fatal(err)
		}
		for _, step := range d.Commands.Steps {
			if strings.TrimSpace(step.Script) == "" {
				t.Fatal("empty command")
			}
		}
	}
}
