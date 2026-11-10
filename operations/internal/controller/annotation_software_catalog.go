package controller

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func annotationSoftwareDefinitions() []serviceinstall.Definition {
	definitions := append(labelStudioDefinitions(), ytlabelDefinitions()...)
	return append(definitions, ytlabelPython312Definitions()...)
}

// Seed once so future controller updates never replace an administrator's
// edited commands, chosen versions, or publication state.
func seedAnnotationSoftwareTemplates(tx *sql.Tx) error {
	for _, definition := range annotationSoftwareDefinitions() {
		if err := definition.ValidateCommands(); err != nil {
			return fmt.Errorf("%s: %w", definition.Key, err)
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("streamfusion-ops/"+definition.Key)).String()
		if _, err := tx.Exec("INSERT OR IGNORE INTO command_templates(id,recipe_key,draft_json,published_json,revision,updated_at,builtin) VALUES(?,?,?,?,1,?,0)", id, definition.Key, jsonText(definition), jsonText(definition), Now()); err != nil {
			return err
		}
	}
	return nil
}
