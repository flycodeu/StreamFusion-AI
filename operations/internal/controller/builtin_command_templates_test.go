package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func builtinTemplate(t *testing.T, s *Store, key, version string) commandTemplate {
	t.Helper()
	record, err := scanCommandTemplate(s.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE recipe_key=?", commandTemplateKey(key, version)))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func firstBuiltinCommand(t *testing.T, definition *serviceinstall.Definition) *serviceinstall.CommandStep {
	t.Helper()
	for index := range definition.Commands.Steps {
		if definition.Commands.Steps[index].Kind != "SYSTEM" {
			return &definition.Commands.Steps[index]
		}
	}
	t.Fatal("definition has no editable command")
	return nil
}

func TestBuiltinTemplatesSeedEveryRecipePublished(t *testing.T) {
	s, _ := setup(t)
	catalog := serviceinstall.BuiltinCommandCatalog()
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM command_templates WHERE builtin=1").Scan(&count); err != nil || len(catalog) != count {
		t.Fatalf("seeded=%d catalog=%d error=%v", count, len(catalog), err)
	}
	for _, definition := range catalog {
		t.Run(definition.Key+"/"+definition.Version, func(t *testing.T) {
			record := builtinTemplate(t, s, definition.Key, definition.Version)
			expected := definition
			expected.Management = softwareManagementPreset(definition)
			revision := 1
			if expected.Management != nil {
				revision++
				commandCopy := *definition.Commands
				commandCopy.Revision = int64(revision)
				expected.Commands = &commandCopy
			}
			if record.Published == nil || !reflect.DeepEqual(record.Draft, expected) || !reflect.DeepEqual(*record.Published, expected) || record.Revision != revision || !reflect.DeepEqual(record.Draft.Commands.Steps, definition.Commands.Steps) {
				t.Fatal("built-in recipe was not seeded with its published commands")
			}
			live, err := s.findInstallRecipe(definition.Key, definition.Version)
			if err != nil || !reflect.DeepEqual(live, expected) {
				t.Fatalf("installation does not resolve the published seed: %v", err)
			}
		})
	}
}

func TestBuiltinTemplateDraftAndPublishKeepMaterialRevision(t *testing.T) {
	s, b := setup(t)
	b.login()
	original := builtinTemplate(t, s, "java", "21")
	changed := original.Draft
	// Decode a separate value so the original snapshot cannot share step slices.
	if err := deploy.StrictJSON([]byte(jsonText(original.Draft)), &changed); err != nil {
		t.Fatal(err)
	}
	firstBuiltinCommand(t, &changed).Script += "\n# reviewed installation command"
	endpoint := "/command-templates/" + original.ID
	saved := dataOf(t, b.call("PUT", endpoint, map[string]any{"expectedVersion": original.Version, "definition": changed}, 200))
	draft := builtinTemplate(t, s, "java", "21")
	if !reflect.DeepEqual(draft.Published, original.Published) || firstBuiltinCommand(t, &draft.Draft).Script != firstBuiltinCommand(t, &changed).Script {
		t.Fatal("saving a draft changed the live definition or lost the edit")
	}
	b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200)
	live, err := s.findInstallRecipe("java", "21")
	if err != nil {
		t.Fatal(err)
	}
	if live.Commands.Revision != int64(original.Revision+1) || firstBuiltinCommand(t, &live).Script != firstBuiltinCommand(t, &changed).Script {
		t.Fatal("published command revision or script mismatch")
	}
	if !reflect.DeepEqual(serviceinstall.BareDefinition(live), serviceinstall.BareDefinition(original.Draft)) || live.Revision != original.Draft.Revision {
		t.Fatal("publishing commands changed the material identity")
	}
}

func TestBuiltinTemplateReopenKeepsEditsAndWithdrawal(t *testing.T) {
	s, b := setup(t)
	b.login()
	edited := builtinTemplate(t, s, "java", "21")
	firstBuiltinCommand(t, &edited.Draft).Script += "\n# local administrator change"
	endpoint := "/command-templates/" + edited.ID
	saved := dataOf(t, b.call("PUT", endpoint, map[string]any{"expectedVersion": edited.Version, "definition": edited.Draft}, 200))
	b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200)
	withdrawn := builtinTemplate(t, s, "mysql", "8.4.11")
	b.call("DELETE", "/command-templates/"+withdrawn.ID, map[string]any{"expectedVersion": withdrawn.Version}, 200)
	beforeEdited := builtinTemplate(t, s, "java", "21")
	beforeWithdrawn := builtinTemplate(t, s, "mysql", "8.4.11")
	dir := s.Dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(beforeEdited, builtinTemplate(t, reopened, "java", "21")) || !reflect.DeepEqual(beforeWithdrawn, builtinTemplate(t, reopened, "mysql", "8.4.11")) {
		t.Fatal("startup seeding overwrote an existing edit or withdrawal")
	}
	if _, err = reopened.findInstallRecipe("mysql", "8.4.11"); err == nil {
		t.Fatal("startup republished a withdrawn recipe")
	}
	var count int
	if err = reopened.DB.QueryRow("SELECT count(*) FROM command_templates WHERE builtin=1").Scan(&count); err != nil || count != len(serviceinstall.BuiltinCommandCatalog()) {
		t.Fatalf("startup duplicated recipes: count=%d error=%v", count, err)
	}
}

func TestBuiltinMaterialIdentityIgnoresOnlyCommandChanges(t *testing.T) {
	var built serviceinstall.Definition
	for _, definition := range serviceinstall.BuiltinCommandCatalog() {
		if definition.Key == "java" && definition.Version == "21" {
			built = definition
			break
		}
	}
	if built.Commands == nil || built.Native == nil || len(built.Native.Parts) == 0 {
		t.Fatal("Java fixture must include both commands and a native package")
	}
	old := serviceinstall.BareDefinition(built)
	built.Commands.Revision++
	firstBuiltinCommand(t, &built).Script += "\n# revised command"
	if !sameMaterialDefinition(old, built) {
		t.Fatal("command publication invalidated the existing package")
	}
	for _, tc := range []struct {
		name string
		edit func(*serviceinstall.Definition)
	}{
		{"version", func(d *serviceinstall.Definition) { d.Version = "8" }},
		{"material revision", func(d *serviceinstall.Definition) { d.Revision += "-changed" }},
		{"archive checksum", func(d *serviceinstall.Definition) { d.Native.Parts[0].SHA256 = "different" }},
		{"image digest", func(d *serviceinstall.Definition) { d.Digest += "different" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed serviceinstall.Definition
			if err := deploy.StrictJSON([]byte(jsonText(built)), &changed); err != nil {
				t.Fatal(err)
			}
			tc.edit(&changed)
			if sameMaterialDefinition(old, changed) {
				t.Fatal("different software material was accepted")
			}
		})
	}
	custom, changed := testCommandDefinition(), testCommandDefinition()
	firstBuiltinCommand(t, &changed).Script = "printf changed"
	if sameMaterialDefinition(custom, changed) {
		t.Fatal("custom package lost its full template identity")
	}
}

func TestBuiltinTemplateCannotSwitchToAnotherValidVersion(t *testing.T) {
	s, b := setup(t)
	b.login()
	original := builtinTemplate(t, s, "java", "21")
	other := builtinTemplate(t, s, "java", "8")
	b.call("PUT", "/command-templates/"+original.ID, map[string]any{"expectedVersion": original.Version, "definition": other.Draft}, 400)
	if !reflect.DeepEqual(original, builtinTemplate(t, s, "java", "21")) {
		t.Fatal("rejected version switch changed the original definition")
	}
	original.Draft.Commands.Builtin = false
	b.call("PUT", "/command-templates/"+original.ID, map[string]any{"expectedVersion": original.Version, "definition": original.Draft}, 400)
}

func TestBuiltinNodePlanRejectsPublishedCommandChanges(t *testing.T) {
	for _, action := range []string{"publish", "withdraw"} {
		t.Run(action, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			host, fixture := nodeFixture(t, s, b)
			plan := nodePlan(t, b, host, map[string]any{"action": "INITIALIZE", "name": "build tools", "account": "ubuntu", "source": map[string]string{"type": "OFFICIAL"}})
			definition, err := serviceinstall.NodeEnvironmentRecipe("INITIALIZE", "")
			if err != nil {
				t.Fatal(err)
			}
			record := builtinTemplate(t, s, definition.Key, definition.Version)
			endpoint := "/command-templates/" + record.ID
			if action == "publish" {
				firstBuiltinCommand(t, &record.Draft).Script += "\n# changed after preflight"
				saved := dataOf(t, b.call("PUT", endpoint, map[string]any{"expectedVersion": record.Version, "definition": record.Draft}, 200))
				b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200)
			} else {
				b.call("DELETE", endpoint, map[string]any{"expectedVersion": record.Version}, 200)
			}
			response := b.call("POST", "/node-environment-plans/"+plan["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "stale-node-template"}, 409)
			if !strings.Contains(response.Body.String(), "NODE_PLAN_STALE") || fixture.mutations.Load() != 0 {
				t.Fatal("changed commands were submitted from the old plan", response.Body.String())
			}
			var jobs int
			if err = s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
				t.Fatal("rejected plan created a job", jobs, err)
			}
		})
	}
}

func TestBuiltinPnpmRemovalUsesTheSelectedNodeVersionSnapshot(t *testing.T) {
	s, b, host, environmentID, _ := installedRegistryFixture(t)
	versions := serviceinstall.NodeCatalog()
	if len(versions) < 2 {
		t.Fatal("test requires two supported Node versions")
	}
	// Install another Node version in the same account, using the normal job path.
	environment, _ := s.readNodeEnvironment(environmentID)
	second := versions[1]
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{
		"expectedHostVersion": "1", "recipeKey": second.Key, "recipeVersion": second.Version, "recipeRevision": second.Revision,
		"config": serviceinstall.Config{Name: "second node", RuntimeMode: "NATIVE", MemoryMiB: 512}, "source": map[string]string{"type": "OFFICIAL"},
		"node": nodeInstallInput{EnvironmentID: environmentID, EnvironmentVersion: environment.Version},
	}, 200))
	accepted := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "second-node-version"}, 202))
	job := accepted["jobId"].(string)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processSoftware(context.Background(), job, raw)
	packageDefinition, err := serviceinstall.NodeEnvironmentRecipe("INSTALL_PNPM", "10.20.0")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	for _, version := range versions[:2] {
		record := builtinTemplate(t, s, packageDefinition.Key, packageDefinition.Version)
		firstBuiltinCommand(t, &record.Draft).Script += "\n# installed for " + version.Version
		endpoint := "/command-templates/" + record.ID
		saved := dataOf(t, b.call("PUT", endpoint, map[string]any{"expectedVersion": record.Version, "definition": record.Draft}, 200))
		b.call("POST", endpoint+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200)
		published := builtinTemplate(t, s, packageDefinition.Key, packageDefinition.Version)
		expected[version.Version] = deploy.Digest(*published.Published)
		environment, _ = s.readNodeEnvironment(environmentID)
		install := nodePlan(t, b, host, map[string]any{"action": "INSTALL_PNPM", "environmentId": environmentID, "expectedEnvironmentVersion": environment.Version, "nodeVersion": version.Version, "packageVersion": packageDefinition.Version, "source": map[string]string{"type": "OFFICIAL"}})
		runNodePlan(t, s, b, install, "pnpm-"+version.Version)
	}
	for _, version := range versions[:2] {
		environment, _ = s.readNodeEnvironment(environmentID)
		plan := nodePlan(t, b, host, map[string]any{"action": "REMOVE_PNPM", "environmentId": environmentID, "expectedEnvironmentVersion": environment.Version, "nodeVersion": version.Version})
		var removal nodeEnvironmentPlan
		if err = deploy.StrictJSON([]byte(jsonText(plan)), &removal); err != nil || removal.Change.Definition == nil {
			t.Fatal("removal lost its installed command snapshot", err)
		}
		if deploy.Digest(*removal.Change.Definition) != expected[version.Version] {
			t.Fatal("removal selected another Node version's pnpm commands", version.Version)
		}
	}
}
