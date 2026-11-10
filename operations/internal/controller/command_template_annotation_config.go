package controller

import (
	"database/sql"
	"strconv"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// These are the shipped Config-19 definitions, normalized only for publication
// revisions and checkout newlines. Any administrator edit changes this hash.
var previousAnnotationConfiguration = map[string]string{
	"custom-label-studio-docker-online":   "43c345903c813fcbeaf872b189d7707f8ceed2c26288612912c5157db85ec09b",
	"custom-label-studio-docker-offline":  "a8937ec8c83bfb39c9b1a907847dde2317890a7fdf824b1ebd401f8d6bba2335",
	"custom-label-studio-native-online":   "4e89dba3147352183f0dfa3c66115e322ca65109e113e4f1c23c261d1119c445",
	"custom-label-studio-native-offline":  "af72aa295cd63e44551da6c64a8f091b4a614a052efee1bfe418cadd0129ec7b",
	"custom-ytlabel-native-online":        "a3b09d4aeae52caca1e57a09ec27391fb9b2b9c7cae956c7cab9667c00bcb454",
	"custom-ytlabel-native-offline":       "e32d2c22ac95c4b817d5c8288e39f4eacf3cc93b817d394f375d3a7f2ac22b2e",
	"custom-ytlabel-py312-native-online":  "50e2b7863864a7d1659acaa78555b5bf6187df3e7bf5dd195150bb16ad860b55",
	"custom-ytlabel-py312-native-offline": "7c229f5486f5f89b1f18ba1bb5543e0c21b6fb5697ed1aa4466883672449e173",
}

func annotationConfigurationHash(d serviceinstall.Definition) string {
	d = serviceinstall.NormalizeCommandScripts(d)
	d.Revision = "1"
	if d.Commands != nil {
		d.Commands.Revision = 1
	}
	return serviceinstall.Digest(d)
}

func annotationConfigurationUpgrade(seed, current serviceinstall.Definition) bool {
	if previousAnnotationConfiguration[seed.Key] == annotationConfigurationHash(current) {
		return true
	}
	if annotationConfigurationHash(current) == annotationConfigurationHash(seed) {
		return false
	}
	// Recognize only the exact stock configuration with untranslated step
	// titles. Scripts, permissions and administrator edits must still match.
	translated := serviceinstall.NormalizeCommandScripts(current)
	if translated.Management == nil {
		return false
	}
	for i := range translated.Management.Operations {
		op := &translated.Management.Operations[i]
		for _, steps := range [][]serviceinstall.CommandStep{op.Steps, op.VerifySteps, op.RollbackSteps} {
			for index := range steps {
				if name, exists := annotationStepNames[steps[index].Name]; exists {
					steps[index].Name = name
				}
			}
		}
	}
	return annotationConfigurationHash(translated) == annotationConfigurationHash(seed)
}

func repairAnnotationConfigurationTemplates(tx *sql.Tx) error {
	for _, seed := range annotationSoftwareDefinitions() {
		var id, draft, published string
		var revision int
		if err := tx.QueryRow("SELECT id,draft_json,published_json,revision FROM command_templates WHERE recipe_key=?", seed.Key).Scan(&id, &draft, &published, &revision); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		if published == "" {
			continue
		}
		var current, editing serviceinstall.Definition
		if err := deploy.StrictJSON([]byte(published), &current); err != nil {
			return err
		}
		if !annotationConfigurationUpgrade(seed, current) {
			continue
		}
		if err := seed.ValidateCommands(); err != nil {
			return err
		}
		if err := deploy.StrictJSON([]byte(draft), &editing); err != nil {
			return err
		}
		if annotationConfigurationUpgrade(seed, editing) {
			updated := seed
			updated.Revision = editing.Revision
			updated.Commands = new(serviceinstall.CommandRecipe)
			*updated.Commands = *seed.Commands
			updated.Commands.Revision = editing.Commands.Revision
			draft = jsonText(updated)
		}
		revision++
		seed.Revision = strconv.Itoa(revision)
		if _, err := tx.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=?,version=version+1,updated_at=? WHERE id=?", draft, jsonText(seed), revision, Now(), id); err != nil {
			return err
		}
		if err := auditTx(tx, "SYSTEM", "COMMAND_TEMPLATE_ANNOTATION_CONFIG_UPDATED", id, "SUCCESS", "annotation-config-"+id); err != nil {
			return err
		}
	}
	return nil
}
