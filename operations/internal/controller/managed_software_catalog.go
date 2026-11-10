package controller

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// ShippedInstallRecipe supports offline release validation without opening a
// controller database. User-edited templates are resolved from the live store.
func ShippedInstallRecipe(key, version string) (serviceinstall.Definition, error) {
	if definition, err := serviceinstall.Find(key, version); err == nil {
		return definition, nil
	}
	for _, definition := range append(additionalSoftwareDefinitions(), annotationSoftwareDefinitions()...) {
		if definition.Key == key && definition.Version == version {
			return definition, nil
		}
	}
	return serviceinstall.Definition{}, errors.New("INSTALL_RECIPE_INVALID")
}

// New default operations are added once. Existing published management and user
// edits remain authoritative; installed definitions and submitted jobs are never
// rewritten by catalog initialization.
func (s *Store) seedSoftwareManagementPresets() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = seedSoftwareManagementPresets(tx); err != nil {
		return err
	}
	if err = seedAdditionalSoftwareTemplates(tx); err != nil {
		return err
	}
	if err = seedAnnotationSoftwareTemplates(tx); err != nil {
		return err
	}
	if err = repairSeededCommandNewlines(tx); err != nil {
		return err
	}
	if err = repairSeededWebDefaults(tx); err != nil {
		return err
	}
	if err = repairAnnotationConfigurationTemplates(tx); err != nil {
		return err
	}
	if err = repairServiceConfigurationTemplates(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func seedSoftwareManagementPresets(tx *sql.Tx) error {
	rows, err := tx.Query("SELECT id,draft_json,published_json,revision FROM command_templates WHERE builtin=1")
	if err != nil {
		return err
	}
	type record struct {
		id, draft, published string
		revision             int
	}
	items := []record{}
	for rows.Next() {
		var item record
		if err = rows.Scan(&item.id, &item.draft, &item.published, &item.revision); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	stock := map[string]string{}
	for _, definition := range serviceinstall.BuiltinCommandCatalog() {
		stock[commandTemplateKey(definition.Key, definition.Version)] = annotationConfigurationHash(definition)
	}
	for _, item := range items {
		if item.published == "" {
			continue
		}
		changed := false
		apply := func(raw string) (string, error) {
			if raw == "" {
				return raw, nil
			}
			var definition serviceinstall.Definition
			if err := deploy.StrictJSON([]byte(raw), &definition); err != nil {
				return "", err
			}
			if definition.Management != nil {
				return raw, nil
			}
			// New presets are published automatically only for an untouched stock
			// install definition. Administrator command edits stay unpublished.
			if stock[commandTemplateKey(definition.Key, definition.Version)] != annotationConfigurationHash(definition) {
				return raw, nil
			}
			definition.Management = softwareManagementPreset(definition)
			if definition.Management == nil {
				return raw, nil
			}
			if err := definition.ValidateManagement(); err != nil {
				return "", err
			}
			if definition.Commands != nil {
				definition.Commands.Revision = int64(item.revision + 1)
			}
			changed = true
			data, err := json.Marshal(definition)
			return string(data), err
		}
		draft, err := apply(item.draft)
		if err != nil {
			return err
		}
		published, err := apply(item.published)
		if err != nil {
			return err
		}
		if changed {
			if _, err = tx.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=revision+1,version=version+1,updated_at=? WHERE id=?", draft, published, Now(), item.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func softwareManagementPreset(definition serviceinstall.Definition) *serviceinstall.ManagementDefinition {
	if definition.Key == "mysql" {
		return serviceConfigurationPreset(definition, mysqlManagementDefinition(definition))
	}
	if definition.Key == "nginx" {
		return serviceConfigurationPreset(definition, nginxManagementDefinition(definition))
	}
	return serviceConfigurationPreset(definition, nil)
}
