package controller

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestSchemaTwentyThreeUpgradesTemplatesAndKeepsHistory(t *testing.T) {
	// Populate real job and software rows so preservation checks cover snapshots.
	s, _, _, _, _ := installedRegistryFixture(t)
	edited := builtinTemplate(t, s, "java", "21-ubuntu24.04-repository")
	withdrawn := builtinTemplate(t, s, "java", "17-ubuntu24.04-repository")
	for _, item := range []commandTemplate{edited, withdrawn} {
		var d serviceinstall.Definition
		if err := deploy.StrictJSON([]byte(jsonText(item.Draft)), &d); err != nil {
			t.Fatal(err)
		}
		d.Commands.FlowVersion = 0
		steps := []serviceinstall.CommandStep{}
		for _, step := range d.Commands.Steps {
			if step.Kind != "SYSTEM" {
				step.Kind = ""
				steps = append(steps, step)
			}
		}
		d.Commands.Steps = steps
		firstBuiltinCommand(t, &d).Script += "\n# administrator edit preserved"
		published := jsonText(d)
		if item.ID == withdrawn.ID {
			published = ""
		}
		if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=? WHERE id=?", jsonText(d), published, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	before := schemaSeventeenHistoricalRows(t, s.DB)
	dir := s.Dir
	if _, err := s.DB.Exec("DROP TABLE software_management_credentials; DROP TABLE software_management_state; DELETE FROM schema_migrations WHERE version>=23; PRAGMA user_version=22"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	upgraded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, upgraded.DB)) {
		t.Fatal("migration changed historical rows")
	}
	current := builtinTemplate(t, upgraded, "java", "21-ubuntu24.04-repository")
	if current.Draft.Commands.FlowVersion != 1 || current.Published.Commands.FlowVersion != 1 || current.Revision != edited.Revision+1 || firstBuiltinCommand(t, &current.Draft).Script != firstBuiltinCommand(t, &edited.Draft).Script+"\n# administrator edit preserved" {
		t.Fatal("migration lost an edited command or published revision")
	}
	if err = current.Draft.ValidateCommands(); err != nil {
		t.Fatal("migrated definition is invalid", err)
	}
	removed := builtinTemplate(t, upgraded, "java", "17-ubuntu24.04-repository")
	if removed.Published != nil || removed.Draft.Commands.FlowVersion != 1 || removed.Revision != withdrawn.Revision {
		t.Fatal("withdrawn template was republished or changed revision")
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "before-schema-23-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatal("migration backup missing")
	}
	assertSchemaElevenForeignKeys(t, upgraded.DB)
}
