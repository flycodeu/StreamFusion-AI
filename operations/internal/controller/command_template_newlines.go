package controller

import (
	"database/sql"
	"strconv"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Earlier Windows builds seeded shared scripts with CRLF. Repair only scripts
// that still match a shipped definition after newline normalization. User edits
// and frozen installation/job snapshots remain authoritative.
func repairSeededCommandNewlines(tx *sql.Tx) error {
	definitions := append(additionalSoftwareDefinitions(), annotationSoftwareDefinitions()...)
	for _, seed := range definitions {
		var id, draft, published string
		var revision int
		err := tx.QueryRow("SELECT id,draft_json,published_json,revision FROM command_templates WHERE recipe_key=?", seed.Key).Scan(&id, &draft, &published, &revision)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		repair := func(raw string) (serviceinstall.Definition, bool, error) {
			var d serviceinstall.Definition
			if raw == "" {
				return d, false, nil
			}
			if err := deploy.StrictJSON([]byte(raw), &d); err != nil {
				return d, false, err
			}
			changed := false
			steps := func(actual, expected []serviceinstall.CommandStep) {
				for i := range actual {
					if i >= len(expected) {
						break
					}
					normalized := serviceinstall.NormalizeScript(actual[i].Script)
					if normalized != actual[i].Script && normalized == expected[i].Script {
						actual[i].Script, changed = normalized, true
					}
				}
			}
			if d.Commands != nil && seed.Commands != nil {
				steps(d.Commands.Steps, seed.Commands.Steps)
			}
			if d.Management != nil && seed.Management != nil {
				for i := range d.Management.Operations {
					for _, expected := range seed.Management.Operations {
						actual := &d.Management.Operations[i]
						if actual.Key == expected.Key {
							steps(actual.Steps, expected.Steps)
							steps(actual.VerifySteps, expected.VerifySteps)
						}
					}
				}
			}
			return d, changed, nil
		}
		d, draftChanged, err := repair(draft)
		if err != nil {
			return err
		}
		p, publishedChanged, err := repair(published)
		if err != nil {
			return err
		}
		if !draftChanged && !publishedChanged {
			continue
		}
		if draftChanged {
			draft = jsonText(d)
		}
		if publishedChanged {
			revision++
			p.Revision = strconv.Itoa(revision)
			published = jsonText(p)
		}
		if _, err = tx.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=?,version=version+1,updated_at=? WHERE id=?", draft, published, revision, Now(), id); err != nil {
			return err
		}
		if err = auditTx(tx, "SYSTEM", "COMMAND_TEMPLATE_NEWLINES_REPAIRED", id, "SUCCESS", "template-newlines-"+id); err != nil {
			return err
		}
	}
	return nil
}
