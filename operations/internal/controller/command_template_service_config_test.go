package controller

import (
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestServiceConfigurationStockUpgradeKeepsEditsWithdrawalAndHistory(t *testing.T) {
	var previous serviceinstall.Definition
	for _, definition := range serviceinstall.BuiltinCommandCatalog() {
		if definition.Key == "redis" && definition.Version == "7.4.11" {
			previous = definition
			break
		}
	}
	key := commandTemplateKey(previous.Key, previous.Version)
	if previous.Key == "" || annotationConfigurationHash(previous) != previousServiceConfiguration[key] {
		t.Fatal("captured stock definition changed")
	}
	s, _ := setup(t)
	for _, scenario := range []string{"stock", "edited-publication", "edited-draft", "withdrawn"} {
		t.Run(scenario, func(t *testing.T) {
			current, editing := serviceinstall.NormalizeCommandScripts(previous), serviceinstall.NormalizeCommandScripts(previous)
			if scenario == "edited-publication" {
				current.Name = "我的 Redis"
				editing = current
			}
			if scenario == "edited-draft" {
				editing.Commands.Steps[0].Script += "\n# administrator draft"
			}
			published := jsonText(current)
			if scenario == "withdrawn" {
				published = ""
			}
			if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=7,version=5 WHERE recipe_key=?", jsonText(editing), published, key); err != nil {
				t.Fatal(err)
			}
			history := schemaSeventeenHistoricalRows(t, s.DB)
			delete(history, "audit")
			for pass := 0; pass < 2; pass++ {
				tx, err := s.DB.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if err = repairServiceConfigurationTemplates(tx); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				actual, err := scanCommandTemplate(s.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE recipe_key=?", key))
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "withdrawn" || scenario == "edited-publication" {
					if actual.Revision != 7 || actual.Version != "5" || jsonText(actual.Draft) != jsonText(editing) || (actual.Published == nil) != (published == "") || actual.Published != nil && jsonText(*actual.Published) != published {
						t.Fatal("edited or withdrawn template changed")
					}
				} else {
					if actual.Revision != 8 || actual.Version != "6" || actual.Published == nil || actual.Published.ValidateCommands() != nil {
						t.Fatal("stock update missing or repeated")
					}
					if _, err := actual.Published.ManagementOperation("configure"); err != nil {
						t.Fatal(err)
					}
					if scenario == "edited-draft" && jsonText(actual.Draft) != jsonText(editing) {
						t.Fatal("unpublished draft lost")
					}
				}
			}
			after := schemaSeventeenHistoricalRows(t, s.DB)
			delete(after, "audit")
			if jsonText(history) != jsonText(after) {
				t.Fatal("template update changed historical data")
			}
		})
	}
}

func TestServiceConfigurationTemplatesMatchExistingInstallations(t *testing.T) {
	for _, definition := range serviceConfigurationSeeds() {
		if definition.Management == nil {
			continue
		}
		write, err := definition.ManagementOperation("configure")
		if err != nil {
			continue
		}
		t.Run(definition.Key+"/"+definition.Version, func(t *testing.T) {
			if definition.ValidateCommands() != nil || !write.Rebind || len(write.VerifySteps) == 0 || len(write.RollbackSteps) == 0 {
				t.Fatal("configuration must be validated and recoverable")
			}
			read, err := definition.ManagementOperation("read-config")
			if err != nil || !read.ReadOnly || read.ResultFormat != "TABLE" {
				t.Fatal("missing actual configuration reader")
			}
			old := serviceinstall.NormalizeCommandScripts(definition)
			old.Management = nil
			item := softwareInstance{ManagementMode: "MANAGED", InstallationStatus: "READY", BindingRevision: 1, IdentityHash: deploy.Digest("identity"), RuntimeKind: "SYSTEMD", Locator: "existing.service", Installation: &serviceinstall.Request{Definition: old, BindingRevision: 1}}
			if !managementTemplateMatches(item, definition) {
				t.Fatal("new configuration requires reinstalling unchanged program")
			}
			if serviceinstall.IsBuiltinCommand(old) {
				bare := serviceinstall.BareDefinition(old)
				item.Installation.Definition = bare
				if !managementTemplateMatches(item, definition) {
					t.Fatal("fixed installation without editor metadata cannot use its configuration")
				}
				item.Installation.Definition.ImageID = deploy.Digest("different-image")
				if managementTemplateMatches(item, definition) {
					t.Fatal("another installed image was accepted")
				}
			}
		})
	}
}
