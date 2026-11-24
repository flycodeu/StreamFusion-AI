package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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

func TestCommandTemplateDeleteRemovesUnusedDefinitionOnly(t *testing.T) {
	s, b := setup(t)
	b.login()
	builtIn, err := scanCommandTemplate(s.DB.QueryRow("SELECT " + commandTemplateColumns + " FROM command_templates WHERE builtin=1 LIMIT 1"))
	if err != nil {
		t.Fatal(err)
	}
	protected := b.call("DELETE", "/command-templates/"+builtIn.ID+"/definition", map[string]string{"expectedVersion": builtIn.Version}, 409)
	if !strings.Contains(protected.Body.String(), "COMMAND_TEMPLATE_BUILTIN") {
		t.Fatal(protected.Body.String())
	}
	d := testCommandDefinition()
	draft := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
	id := draft["id"].(string)
	endpoint := "/command-templates/" + id
	published := dataOf(t, b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": draft["version"]}, 200))
	b.call("DELETE", endpoint+"/definition", map[string]any{"expectedVersion": draft["version"]}, 409)
	removed := dataOf(t, b.call("DELETE", endpoint+"/definition", map[string]any{"expectedVersion": published["version"]}, 200))
	if removed["id"] != id {
		t.Fatal(removed)
	}
	if _, err = s.findInstallRecipe(d.Key, d.Version); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted definition remains in the software catalog", err)
	}
	if got := b.call("GET", "/command-templates", nil, 200); strings.Contains(got.Body.String(), id) {
		t.Fatal("deleted definition remains in the editor")
	}
	var auditCount int
	if err = s.DB.QueryRow("SELECT count(*) FROM audit WHERE object_id=? AND action='COMMAND_TEMPLATE_DELETED'", id).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatal(auditCount, err)
	}
	b.call("DELETE", endpoint+"/definition", map[string]any{"expectedVersion": published["version"]}, 404)
	if replacement := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200)); replacement["id"] == id {
		t.Fatal("recreated definition reused deleted identity", replacement)
	}
}

func TestCommandTemplateDeletionWinsAgainstUnfinishedPreflight(t *testing.T) {
	s, b := setup(t)
	b.login()
	d := testCommandDefinition()
	d.Commands.Steps[1].RunAs = "ROOT"
	saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
	endpoint := "/command-templates/" + saved["id"].(string)
	published := dataOf(t, b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200))
	deleting := *b
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		if req.Action != "SOFTWARE_INSTALL_CHECK" {
			return nil, false
		}
		deleting.call("DELETE", endpoint+"/definition", map[string]any{"expectedVersion": published["version"]}, 200)
		replacement := dataOf(t, deleting.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
		deleting.call("POST", "/command-templates/"+replacement["id"].(string)+"/publish", map[string]any{"expectedVersion": replacement["version"]}, 200)
		plan := req.Installation
		root := plan.InstallRoot("/opt/plugin-test")
		raw, _ := json.Marshal(installPreflightFixture(*plan, root, true))
		return raw, true
	})
	res := b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": "1", "config": serviceinstall.Config{Name: "demo", RuntimeMode: "NATIVE", MemoryMiB: 512}, "source": map[string]string{"type": "ONLINE"}}, 409)
	if !strings.Contains(res.Body.String(), "INSTALL_RECIPE_INVALID") {
		t.Fatal(res.Body.String())
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM software_install_plans").Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted definition produced an install plan", count, err)
	}
}

func TestSchemaTwentyOneAddsOnlyTemplateStorage(t *testing.T) {
	s, b := setup(t)
	b.login()
	dir := s.Dir
	before := schemaSeventeenHistoricalRows(t, s.DB)
	rewindSchema34Fixture(t, s.DB)
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
