package controller

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSchema31MigratesHistoricalLoginCiphertext(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "rollback"}[damaged], func(t *testing.T) {
			s, b, host := setupSoftwareLogin(t)
			item := registerTestSoftware(t, b, host, "SYSTEMD", "historical-db.service", "MYSQL")
			id, dir := item["id"].(string), s.Dir
			original := softwareLoginAccount{"historical-admin", "Historical User", "operator@example.test", "migration-test-password"}
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			const oldVersion = 7
			ciphertext, err := seal(s.Key, "software-login:"+id, oldVersion, data)
			if err != nil {
				t.Fatal(err)
			}
			if damaged {
				ciphertext[len(ciphertext)-1] ^= 1
			}
			if _, err = s.DB.Exec("DROP TRIGGER application_qa_scope_changed; DROP TABLE application_qa_requests; DROP TABLE application_qa_monitors; DROP TABLE software_login_accounts;" + schemaV29 + "DELETE FROM schema_migrations WHERE version>=31; PRAGMA user_version=30;"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec("INSERT INTO software_login_accounts VALUES(?,?,?)", id, oldVersion, ciphertext); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := Open(dir)
			if damaged {
				if err == nil {
					upgraded.Close()
					t.Fatal("invalid historical ciphertext accepted")
				}
				db, err := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var schema int
				var retained []byte
				if err = db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != 30 {
					t.Fatalf("failed upgrade partially applied: schema %d, error %v", schema, err)
				}
				if err = db.QueryRow("SELECT ciphertext FROM software_login_accounts WHERE software_id=?", id).Scan(&retained); err != nil || !bytes.Equal(retained, ciphertext) {
					t.Fatal("failed upgrade changed historical account", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			if backups, err := filepath.Glob(filepath.Join(dir, "before-schema-31-*.tar.gz")); err != nil || len(backups) != 1 {
				t.Fatal("migration backup missing", err)
			}
			var retained []byte
			if err = upgraded.DB.QueryRow("SELECT ciphertext FROM software_login_accounts WHERE software_id=? AND account_id=?", id, id).Scan(&retained); err != nil || !bytes.Equal(retained, ciphertext) {
				t.Fatal("migration changed historical ciphertext", err)
			}
			tx, err := upgraded.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, lookup := range []string{"", id} {
				resolved, account, version, err := upgraded.loadSoftwareLoginAccount(tx, id, lookup)
				if err != nil || resolved != id || account != original || version != oldVersion {
					t.Fatalf("historical account not preserved, version %d, error %v", version, err)
				}
			}
		})
	}
}
