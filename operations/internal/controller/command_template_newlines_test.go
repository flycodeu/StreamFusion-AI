package controller

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestSeededNewlineRepairPreservesEditsPublicationAndHistory(t *testing.T) {
	s, _ := setup(t)
	seed := ytlabelDefinitions()[0]
	legacy := seed
	legacy.Commands = &serviceinstall.CommandRecipe{}
	*legacy.Commands = *seed.Commands
	legacy.Commands.Steps = append([]serviceinstall.CommandStep(nil), seed.Commands.Steps...)
	for i := range legacy.Commands.Steps {
		legacy.Commands.Steps[i].Script = strings.ReplaceAll(legacy.Commands.Steps[i].Script, "\n", "\r\n")
	}
	legacy.Name = "我的标注平台"
	legacy.Commands.Steps[1].Script = "printf 'my command'\r\n"
	if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=?,version=9,revision=7 WHERE recipe_key=?", jsonText(legacy), jsonText(legacy), seed.Key); err != nil {
		t.Fatal(err)
	}
	withdrawn := ytlabelDefinitions()[1]
	if _, err := s.DB.Exec("UPDATE command_templates SET published_json='' WHERE recipe_key=?", withdrawn.Key); err != nil {
		t.Fatal(err)
	}
	history := schemaSeventeenHistoricalRows(t, s.DB)
	delete(history, "audit")
	for pass := 0; pass < 2; pass++ {
		if err := s.seedSoftwareManagementPresets(); err != nil {
			t.Fatal(err)
		}
		var draft, published string
		var version, revision int
		if err := s.DB.QueryRow("SELECT draft_json,published_json,version,revision FROM command_templates WHERE recipe_key=?", seed.Key).Scan(&draft, &published, &version, &revision); err != nil {
			t.Fatal(err)
		}
		var actual serviceinstall.Definition
		if err := json.Unmarshal([]byte(published), &actual); err != nil {
			t.Fatal(err)
		}
		if actual.Name != legacy.Name || actual.Commands.Steps[0].Script != seed.Commands.Steps[0].Script || actual.Commands.Steps[1].Script != legacy.Commands.Steps[1].Script || actual.Revision != "8" || version != 10 || revision != 8 {
			t.Fatal("repair replaced edits, failed to increment revision, or ran twice")
		}
		after := schemaSeventeenHistoricalRows(t, s.DB)
		delete(after, "audit")
		if !reflect.DeepEqual(history, after) {
			t.Fatal("repair changed historical records")
		}
		var repairs int
		if err := s.DB.QueryRow("SELECT count(*) FROM audit WHERE action='COMMAND_TEMPLATE_NEWLINES_REPAIRED'").Scan(&repairs); err != nil || repairs != 1 {
			t.Fatal("repair audit missing or repeated", repairs, err)
		}
		if err := s.DB.QueryRow("SELECT published_json FROM command_templates WHERE recipe_key=?", withdrawn.Key).Scan(&published); err != nil || published != "" {
			t.Fatal("repair republished a withdrawn template", err)
		}
	}
}
