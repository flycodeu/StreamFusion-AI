package controller

import (
	"database/sql"
	"errors"
	"strconv"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

var errLoginVersionConflict = errors.New("software login version changed")

type installLoginAccount struct {
	id, label, username                     string
	credential, revision, material, version string
}

// Resolve fixed account roles from the committed installation snapshot. The
// caller supplies only an instance and row ID, never the credential to read.
func installedLoginAccounts(tx *sql.Tx, instance string) ([]installLoginAccount, error) {
	var kind, mode, state, status, snapshot string
	var binding int64
	err := tx.QueryRow(`SELECT s.software_type,s.management_mode,s.actual_state,s.installation_status,s.installation_json,s.binding_revision
		FROM software_instances s WHERE s.id=? AND `+softwareLoginEligible, instance).Scan(&kind, &mode, &state, &status, &snapshot, &binding)
	if err != nil {
		return nil, err
	}
	if mode != "MANAGED" || status != "READY" || state == "MISSING" || (kind != "MYSQL" && kind != "REDIS") || snapshot == "" || snapshot == "{}" {
		return nil, nil
	}
	var install serviceinstall.Request
	if err = deploy.StrictJSON([]byte(snapshot), &install); err != nil {
		return nil, err
	}
	if install.SoftwareID != instance || install.BindingRevision != binding || install.Official != nil || serviceinstall.IsOfficial(install.Definition) ||
		install.Definition.Commands != nil && !serviceinstall.IsBuiltinCommand(install.Definition) {
		return nil, nil
	}
	c := install.Config
	var candidates []installLoginAccount
	switch {
	case kind == "MYSQL" && install.Definition.Key == "mysql":
		candidates = append(candidates, installLoginAccount{id: "mysql-root", label: "root 初始密码", username: "root", credential: c.CredentialID, revision: c.CredentialRevision})
		if c.Username != "" {
			candidates = append(candidates, installLoginAccount{id: "mysql-user", label: "业务账号密码", username: c.Username, credential: c.UserCredentialID, revision: c.UserCredentialRevision})
		}
	case kind == "REDIS" && install.Definition.Key == "redis":
		candidates = append(candidates, installLoginAccount{id: "redis-default", label: "Redis 密码", username: "default", credential: c.CredentialID, revision: c.CredentialRevision})
	}
	bound := make([]installLoginAccount, 0, len(candidates))
	for _, row := range candidates {
		if !deploy.ID.MatchString(row.credential) || row.revision == "" {
			continue
		}
		err = tx.QueryRow("SELECT material FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND kind='PASSWORD' AND revoked_at IS NULL", row.credential, row.revision).Scan(&row.material)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		row.version = deploy.Digest([]string{instance, strconv.FormatInt(binding, 10), row.id, row.username, row.credential, row.revision})
		bound = append(bound, row)
	}
	return bound, nil
}

func (s *Store) listSoftwareLoginAccounts(tx *sql.Tx, instance string) ([]map[string]any, error) {
	installed, err := installedLoginAccounts(tx, instance)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(installed))
	for _, row := range installed {
		result = append(result, map[string]any{"id": row.id, "label": row.label, "source": "INSTALL", "username": row.username, "fullName": "", "email": "", "hasPassword": row.material != "", "version": row.version})
	}
	rows, err := tx.Query("SELECT account_id,version,ciphertext FROM software_login_accounts WHERE software_id=? ORDER BY rowid", instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var version int
		var ciphertext []byte
		if err = rows.Scan(&id, &version, &ciphertext); err != nil {
			return nil, err
		}
		account, err := s.openSoftwareLoginAccount(instance, id, version, ciphertext)
		if err != nil {
			return nil, err
		}
		result = append(result, account.row(id, version))
	}
	return result, rows.Err()
}

func (s *Store) softwareLoginPassword(tx *sql.Tx, route loginAccountRoute, expected string) ([]byte, error) {
	if route.legacy || deploy.ID.MatchString(route.account) {
		_, account, version, err := s.loadSoftwareLoginAccount(tx, route.instance, route.account)
		if err != nil {
			return nil, err
		}
		if version == 0 {
			return nil, sql.ErrNoRows
		}
		if strconv.Itoa(version) != expected {
			return nil, errLoginVersionConflict
		}
		return []byte(account.Password), nil
	}
	accounts, err := installedLoginAccounts(tx, route.instance)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if account.id != route.account {
			continue
		}
		if account.material == "" {
			return nil, sql.ErrNoRows
		}
		if account.version != expected {
			return nil, errLoginVersionConflict
		}
		return s.openCredentialMaterial(account.credential, account.revision, account.material)
	}
	return nil, sql.ErrNoRows
}
