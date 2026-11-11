package controller

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func testCommandDefinition() serviceinstall.Definition {
	return serviceinstall.Definition{Key: "custom-demo", Name: "演示工具", Version: "1", Revision: "1", Kind: "TOOL", Platform: "linux/amd64", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ONLINE"}, Commands: &serviceinstall.CommandRecipe{Kind: "TOOL", Locator: "${OPS_INSTALL_ROOT}/bin/demo", Steps: []serviceinstall.CommandStep{
		{Phase: "INSTALL", Name: "安装", Script: "printf install", RunAs: "ROOT", TimeoutSeconds: 30},
		{Phase: "VERIFY", Name: "验证", Script: "printf verify", RunAs: "ACCOUNT", TimeoutSeconds: 30},
		{Phase: "UNINSTALL", Name: "卸载", Script: "printf uninstall", RunAs: "ROOT", TimeoutSeconds: 30},
	}}}
}

func TestCommandTemplateDraftPublishAndWithdrawAreSeparate(t *testing.T) {
	s, b := setup(t)
	var before int
	if err := s.DB.QueryRow("SELECT count(*) FROM command_templates").Scan(&before); err != nil {
		t.Fatal(err)
	}
	b.call("POST", "/command-templates", map[string]any{"definition": testCommandDefinition()}, 401)
	b.login()
	saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": testCommandDefinition()}, 200))
	endpoint := "/command-templates/" + saved["id"].(string)
	if _, err := s.findInstallRecipe("custom-demo", "1"); err == nil {
		t.Fatal("unpublished draft in catalog")
	}
	first := dataOf(t, b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200))
	original, err := s.findInstallRecipe("custom-demo", "1")
	if err != nil {
		t.Fatal(err)
	}
	changed := testCommandDefinition()
	changed.Commands.Steps[0].Script = "printf changed"
	draft := dataOf(t, b.call("PUT", endpoint, map[string]any{"expectedVersion": first["version"], "definition": changed}, 200))
	live, _ := s.findInstallRecipe("custom-demo", "1")
	if deploy.Digest(live) != deploy.Digest(original) {
		t.Fatal("draft edit changed published template")
	}
	b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": first["version"]}, 409)
	second := dataOf(t, b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": draft["version"]}, 200))
	live, _ = s.findInstallRecipe("custom-demo", "1")
	if live.Revision != "2" || live.Commands.Steps[0].Script != "printf changed" {
		t.Fatal("published revision mismatch")
	}
	b.call("DELETE", endpoint, map[string]any{"expectedVersion": second["version"]}, 200)
	if _, err = s.findInstallRecipe("custom-demo", "1"); err == nil {
		t.Fatal("withdrawn template in catalog")
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM command_templates").Scan(&count)
	if count != before+1 {
		t.Fatal("withdrawal deleted history")
	}
	if original.Commands.Steps[0].Script != "printf install" {
		t.Fatal("frozen definition changed")
	}
}

func TestSchemaTwentyOneAddsOnlyTemplateStorage(t *testing.T) {
	s, b := setup(t)
	b.login()
	dir := s.Dir
	before := schemaSeventeenHistoricalRows(t, s.DB)
	if _, err := s.DB.Exec(dropApplicationFixtureTables + "DROP TABLE software_management_credentials; DROP TABLE software_management_state; DROP TABLE command_templates; DELETE FROM schema_migrations WHERE version>=21; PRAGMA user_version=20"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	upgraded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, upgraded.DB)) {
		t.Fatal("historical rows changed")
	}
	var version int
	if err = upgraded.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "before-schema-21-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatal("migration backup missing")
	}
	var count int
	if err = upgraded.DB.QueryRow("SELECT count(*) FROM command_templates").Scan(&count); err != nil || count != len(serviceinstall.BuiltinCommandCatalog())+len(additionalSoftwareDefinitions())+len(annotationSoftwareDefinitions()) {
		t.Fatal(count, err)
	}
}

func TestCommandTemplateCanonicalizesScriptsBeforeSavingAndPublishing(t *testing.T) {
	s, b := setup(t)
	b.login()
	d := testCommandDefinition()
	d.Commands.Steps[0].Script = "set -euo pipefail\r\nprintf install\r\n"
	saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
	read := func() commandTemplate {
		t.Helper()
		stored, err := scanCommandTemplate(s.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE id=?", saved["id"]))
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}
	if got := read().Draft.Commands.Steps[0].Script; got != "set -euo pipefail\nprintf install\n" {
		t.Fatalf("saved editor CRLF unchanged: %q", got)
	}
	// A pre-existing draft is canonicalized by an explicit publish, while its
	// stored draft and all earlier snapshots remain unchanged.
	if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=? WHERE id=?", jsonText(d), saved["id"]); err != nil {
		t.Fatal(err)
	}
	b.call("POST", "/command-templates/"+saved["id"].(string)+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200)
	published := read()
	if published.Published.Commands.Steps[0].Script != "set -euo pipefail\nprintf install\n" || published.Draft.Commands.Steps[0].Script != d.Commands.Steps[0].Script {
		t.Fatal("publish did not canonicalize independently of the frozen draft")
	}
	changed := testCommandDefinition()
	changed.Commands.Steps[0].Script = "printf changed\r\n"
	b.call("PUT", "/command-templates/"+saved["id"].(string), map[string]any{"expectedVersion": published.Version, "definition": changed}, 200)
	if read().Draft.Commands.Steps[0].Script != "printf changed\n" {
		t.Fatal("edited script retained CRLF")
	}
	if read().Published.Commands.Steps[0].Script != "set -euo pipefail\nprintf install\n" {
		t.Fatal("editing a draft changed the published snapshot")
	}
}
