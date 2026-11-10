package controller

import (
	"database/sql"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const schemaV23 = `PRAGMA user_version=23;`

// Published templates move to ordered flows. Submitted jobs and installations
// keep their original definition and recovery semantics.
func migrateCommandFlows(tx *sql.Tx) error {
	rows, err := tx.Query("SELECT id,draft_json,published_json,revision FROM command_templates WHERE builtin=1")
	if err != nil {
		return err
	}
	type entry struct {
		id, draft, published string
		revision             int
	}
	items := []entry{}
	for rows.Next() {
		var item entry
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
	for _, item := range items {
		revision := item.revision
		if item.published != "" {
			revision++
		}
		upgrade := func(raw string) (string, error) {
			if raw == "" {
				return "", nil
			}
			var d serviceinstall.Definition
			if e := deploy.StrictJSON([]byte(raw), &d); e != nil {
				return "", e
			}
			d, e := serviceinstall.UpgradeBuiltinCommandFlow(d)
			if e != nil {
				return "", e
			}
			d.Commands.Revision = int64(revision)
			return jsonText(d), nil
		}
		draft, e := upgrade(item.draft)
		if e != nil {
			return e
		}
		published, e := upgrade(item.published)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE command_templates SET draft_json=?,published_json=?,revision=?,version=version+1,updated_at=? WHERE id=?", draft, published, revision, Now(), item.id); err != nil {
			return err
		}
	}
	return nil
}
