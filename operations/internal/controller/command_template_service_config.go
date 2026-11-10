package controller

import (
	"database/sql"
	"strconv"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func serviceConfigurationSeeds() []serviceinstall.Definition {
	seeds := additionalSoftwareDefinitions()
	for _, definition := range serviceinstall.BuiltinCommandCatalog() {
		definition.Management = softwareManagementPreset(definition)
		if definition.Management != nil {
			seeds = append(seeds, definition)
		}
	}
	return seeds
}

// Upgrade only exact shipped definitions. Existing installation and job snapshots
// keep their frozen commands; an edited draft or withdrawn publication stays intact.
func repairServiceConfigurationTemplates(tx *sql.Tx) error {
	for _, seed := range serviceConfigurationSeeds() {
		key := commandTemplateKey(seed.Key, seed.Version)
		previous, known := previousServiceConfiguration[key]
		if !known || previous == annotationConfigurationHash(seed) {
			continue
		}
		var id, draft, published string
		var revision int
		err := tx.QueryRow("SELECT id,draft_json,published_json,revision FROM command_templates WHERE recipe_key=?", key).Scan(&id, &draft, &published, &revision)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if published == "" {
			continue
		}
		var current, editing serviceinstall.Definition
		if err = deploy.StrictJSON([]byte(published), &current); err != nil {
			return err
		}
		if annotationConfigurationHash(current) != previous {
			continue
		}
		if err = seed.ValidateCommands(); err != nil {
			return err
		}
		if err = deploy.StrictJSON([]byte(draft), &editing); err != nil {
			return err
		}
		if annotationConfigurationHash(editing) == previous {
			updated := serviceinstall.NormalizeCommandScripts(seed)
			updated.Revision, updated.Commands.Revision = editing.Revision, editing.Commands.Revision
			draft = jsonText(updated)
		}
		revision++
		if serviceinstall.IsBuiltinCommand(seed) {
			seed.Commands.Revision = int64(revision)
		} else {
			seed.Revision = strconv.Itoa(revision)
		}
		if _, err = tx.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=?,version=version+1,updated_at=? WHERE id=?", draft, jsonText(seed), revision, Now(), id); err != nil {
			return err
		}
		if err = auditTx(tx, "SYSTEM", "COMMAND_TEMPLATE_SERVICE_CONFIG_UPDATED", id, "SUCCESS", "service-config-"+id); err != nil {
			return err
		}
	}
	return nil
}
