package controller

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestAnnotationConfigurationUpgradeMatchesOnlyPublishedStockAndKeepsHistory(t *testing.T) {
	raw, err := os.ReadFile("testdata/annotation-config20-previous-ytlabel.json")
	if err != nil {
		t.Fatal(err)
	}
	var old serviceinstall.Definition
	if err := json.Unmarshal(raw, &old); err != nil || annotationConfigurationHash(old) != previousAnnotationConfiguration[old.Key] {
		t.Fatal("previous shipped fixture no longer matches its captured hash", err)
	}
	s, _ := setup(t)
	old.Revision = "7"
	for _, scenario := range []string{"stock", "edited", "withdrawn"} {
		t.Run(scenario, func(t *testing.T) {
			current := serviceinstall.NormalizeCommandScripts(old)
			if scenario == "edited" {
				current.Name = "我的标注配置"
			}
			published := jsonText(current)
			if scenario == "withdrawn" {
				published = ""
			}
			if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=7,version=5 WHERE recipe_key=?", jsonText(current), published, old.Key); err != nil {
				t.Fatal(err)
			}
			before := schemaSeventeenHistoricalRows(t, s.DB)
			delete(before, "audit")
			for pass := 0; pass < 2; pass++ {
				if err := s.seedSoftwareManagementPresets(); err != nil {
					t.Fatal(err)
				}
				var draft, actual string
				var revision, version int
				if err := s.DB.QueryRow("SELECT draft_json,published_json,revision,version FROM command_templates WHERE recipe_key=?", old.Key).Scan(&draft, &actual, &revision, &version); err != nil {
					t.Fatal(err)
				}
				if scenario != "stock" {
					if actual != published || draft != jsonText(current) || revision != 7 || version != 5 {
						t.Fatal("edited or withdrawn definition changed")
					}
					continue
				}
				var upgraded serviceinstall.Definition
				if json.Unmarshal([]byte(actual), &upgraded) != nil || len(upgraded.Management.Operations) != 4 || upgraded.Management.ScriptPrelude == "" || upgraded.Revision != "8" || revision != 8 || version != 6 || upgraded.ValidateCommands() != nil {
					t.Fatal("stock upgrade was incomplete or applied twice", revision, version)
				}
			}
			after := schemaSeventeenHistoricalRows(t, s.DB)
			delete(after, "audit")
			if jsonText(before) != jsonText(after) {
				t.Fatal("template upgrade changed historical rows")
			}
		})
	}
}
