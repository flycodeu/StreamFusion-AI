package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestMysqlSocketTemplateCorrectionOnlyUpdatesUntouchedStock(t *testing.T) {
	s, _ := setup(t)
	for _, seed := range serviceConfigurationSeeds() {
		key := commandTemplateKey(seed.Key, seed.Version)
		if _, exists := previousMysqlSocketConfiguration[key]; !exists {
			continue
		}
		old := serviceinstall.NormalizeCommandScripts(seed)
		old.Management.ScriptPrelude = strings.ReplaceAll(old.Management.ScriptPrelude, ",'--socket='+str(root/'data/mysql.sock')", "")
		if annotationConfigurationHash(old) != previousMysqlSocketConfiguration[key] {
			t.Fatal("prior stock socket definition changed", key)
		}
		for _, edited := range []bool{false, true} {
			current := serviceinstall.NormalizeCommandScripts(old)
			if edited {
				current.Management.ScriptPrelude += "\n# administrator settings"
			}
			raw := jsonText(current)
			if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=7,version=5 WHERE recipe_key=?", raw, raw, key); err != nil {
				t.Fatal(err)
			}
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
				if edited {
					if actual.Revision != 7 || jsonText(*actual.Published) != raw {
						t.Fatal("administrator edit replaced")
					}
				} else if actual.Revision != 8 || annotationConfigurationHash(*actual.Published) != annotationConfigurationHash(seed) {
					t.Fatal("socket correction missing or repeated")
				}
			}
		}
	}
}

func TestJenkinsRuntimeCorrectionUpdatesStockAndRetainsEditedTemplates(t *testing.T) {
	s, _ := setup(t)
	for _, seed := range serviceConfigurationSeeds() {
		if seed.CatalogKey != "jenkins" {
			continue
		}
		t.Run(seed.Key, func(t *testing.T) {
			old := previousRegistryDockerDefinition(seed)
			for i := range old.Commands.Steps {
				old.Commands.Steps[i].Script = previousJenkinsRuntimeCommand(old.Commands.Steps[i].Script)
			}
			if annotationConfigurationHash(old) != previousJenkinsRuntimeConfiguration[seed.Key] || annotationConfigurationHash(old) == annotationConfigurationHash(seed) {
				t.Fatal("missing JVM option or prior Jenkins definition changed")
			}
			checkAdditionalStockUpgrade(t, s, seed, old)
		})
	}
}

func TestRegistryDockerCorrectionPreservesEditsAndInstallHistory(t *testing.T) {
	s, b := setup(t)
	b.login()
	jenkins, err := ShippedInstallRecipe("custom-jenkins-docker-online", "2.580.2")
	if err != nil {
		t.Fatal(err)
	}
	old := previousRegistryDockerDefinition(jenkins)
	if _, err = s.DB.Exec("UPDATE command_templates SET published_json=? WHERE recipe_key=?", jsonText(old), old.Key); err != nil {
		t.Fatal(err)
	}
	host, fixture := installSSHFixture(t, s, b)
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{
		"expectedHostVersion": "1", "recipeKey": old.Key, "recipeVersion": old.Version, "recipeRevision": old.Revision,
		"config": serviceinstall.Config{Name: "Prior Jenkins", RuntimeMode: "DOCKER", StorageRoot: "/data/ops-ci", BindAddress: "127.0.0.1", HostPort: 18080, MemoryMiB: old.DefaultMemoryMiB},
		"source": map[string]string{"type": "REGISTRY"},
	}, 200))
	receipt := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "prior-jenkins-install"}, 202))
	job := receipt["jobId"].(string)
	var raw string
	if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processSoftware(context.Background(), job, raw)
	matrixCheckJobFinished(t, s, job)
	if fixture.InstallCount.Load() != 1 {
		t.Fatal("historical template installation was not exercised")
	}
	before := schemaSeventeenHistoricalRows(t, s.DB)
	delete(before, "audit")

	checked := 0
	for _, seed := range serviceConfigurationSeeds() {
		previousHash, affected := previousRegistryDockerConfiguration[seed.Key]
		if !affected {
			continue
		}
		checked++
		t.Run(seed.Key, func(t *testing.T) {
			old := previousRegistryDockerDefinition(seed)
			if annotationConfigurationHash(old) != previousHash || annotationConfigurationHash(seed) == previousHash {
				t.Fatal("registry correction missing or prior stock definition changed")
			}
			checkAdditionalStockUpgrade(t, s, seed, old)
		})
	}
	if checked != len(previousRegistryDockerConfiguration) {
		t.Fatal("a previously published registry Docker template was not checked")
	}
	after := schemaSeventeenHistoricalRows(t, s.DB)
	delete(after, "audit")
	if !reflect.DeepEqual(before, after) || fixture.InstallCount.Load() != 1 {
		t.Fatal("stock correction rewrote or replayed an installation")
	}
}

func checkAdditionalStockUpgrade(t *testing.T, s *Store, seed, old serviceinstall.Definition) {
	t.Helper()
	oldRaw := jsonText(old)
	edited := serviceinstall.NormalizeCommandScripts(old)
	edited.Commands.Steps[0].Script += "\n# administrator settings"
	editedRaw := jsonText(edited)
	for _, scenario := range []struct {
		name, draft, published string
		updated                bool
	}{
		{"stock", oldRaw, oldRaw, true},
		{"edited draft", editedRaw, oldRaw, true},
		{"edited publication", editedRaw, editedRaw, false},
		{"withdrawn", oldRaw, "", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=7,version=5 WHERE recipe_key=?", scenario.draft, scenario.published, seed.Key); err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 2; pass++ {
				if err := s.seedSoftwareManagementPresets(); err != nil {
					t.Fatal(err)
				}
			}
			actual, err := scanCommandTemplate(s.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE recipe_key=?", seed.Key))
			if err != nil {
				t.Fatal(err)
			}
			if scenario.updated {
				if actual.Revision != 8 || actual.Version != "6" || actual.Published == nil || annotationConfigurationHash(*actual.Published) != annotationConfigurationHash(seed) {
					t.Fatal("stock update was missing or repeated")
				}
				if scenario.draft == oldRaw && annotationConfigurationHash(actual.Draft) != annotationConfigurationHash(seed) || scenario.draft != oldRaw && jsonText(actual.Draft) != scenario.draft {
					t.Fatal("stock draft did not update or edited draft was overwritten")
				}
				return
			}
			if actual.Revision != 7 || actual.Version != "5" || jsonText(actual.Draft) != scenario.draft || (actual.Published == nil) != (scenario.published == "") || actual.Published != nil && jsonText(*actual.Published) != scenario.published {
				t.Fatal("edited publication or withdrawal did not retain its original state")
			}
		})
	}
}

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
