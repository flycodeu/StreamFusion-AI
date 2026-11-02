package controller

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func schemaSixteenFixture(t *testing.T) schemaTenFixture {
	f := schemaFifteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(schemaV16); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schema_migrations VALUES(16,?,?,?)", hashToken(schemaV16), "historical", Now()); err != nil {
		t.Fatal(err)
	}
	return f
}

func schemaSeventeenHistoricalRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	names, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT IN ('schema_migrations','sessions','node_registries') ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{}
	for names.Next() {
		var name string
		if err = names.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	names.Close()
	result := map[string]string{}
	for _, name := range tables {
		rows, e := db.Query("SELECT * FROM \"" + strings.ReplaceAll(name, "\"", "\"\"") + "\" ORDER BY 1")
		if e != nil {
			t.Fatal(e)
		}
		columns, _ := rows.Columns()
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range refs {
				refs[i] = &row[i]
			}
			if e = rows.Scan(refs...); e != nil {
				t.Fatal(e)
			}
			values = append(values, row)
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		rows.Close()
		result[name] = hashToken(jsonText(values))
	}
	return result
}

func TestSchemaSeventeenAddsPresetsWithoutChangingHistoricalTables(t *testing.T) {
	f := schemaSixteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	before := schemaSeventeenHistoricalRows(t, db)
	db.Close()
	s, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, s.DB)) {
		t.Fatal("historical rows changed")
	}
	var version, count, fixed int
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	s.DB.QueryRow("SELECT count(*),sum(built_in) FROM node_registries").Scan(&count, &fixed)
	if version != SchemaVersion || count != 2 || fixed != 1 {
		t.Fatal(version, count, fixed)
	}
	var address string
	s.DB.QueryRow("SELECT url FROM node_registries WHERE built_in=1").Scan(&address)
	if address != "https://registry.npmjs.org/" {
		t.Fatal(address)
	}
	backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-17-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	assertSchemaElevenForeignKeys(t, s.DB)
	if err = s.verifySecrets(); err != nil {
		t.Fatal("historical secrets cannot be decrypted", err)
	}
}

func TestSchemaSeventeenFailureRollsBackNewTableAndPreservesHistory(t *testing.T) {
	f := schemaSixteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TRIGGER reject_registry_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=17 BEGIN SELECT RAISE(ABORT,'fixture migration failure'); END"); err != nil {
		t.Fatal(err)
	}
	before := schemaSeventeenHistoricalRows(t, db)
	db.Close()
	if s, e := Open(f.dir); e == nil {
		s.Close()
		t.Fatal("failed migration accepted")
	}
	db, err = sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, db)) {
		t.Fatal("failed migration changed historical rows")
	}
	var version, tables int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='node_registries'").Scan(&tables)
	if version != 16 || tables != 0 {
		t.Fatal("partial schema survived rollback", version, tables)
	}
	backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-17-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatal("missing backup", backups)
	}
}
