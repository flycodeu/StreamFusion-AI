package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

const softwareLoginReadAudit = "SOFTWARE_LOGIN_READ"

type softwareLoginAccount struct {
	Username string `json:"username"`
	FullName string `json:"fullName"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginAccountRoute struct {
	instance, account    string
	legacy, copyPassword bool
}

func softwareLoginAccountPath(path string) (loginAccountRoute, bool) {
	p := strings.Split(path, "/")
	if len(p) < 4 || p[0] != "" || p[1] != "software-instances" || !deploy.ID.MatchString(p[2]) {
		return loginAccountRoute{}, false
	}
	route := loginAccountRoute{instance: p[2], legacy: p[3] == "login-account"}
	if route.legacy {
		route.copyPassword = len(p) == 5 && p[4] == "copy-password"
		return route, len(p) == 4 || route.copyPassword
	}
	if p[3] != "login-accounts" || len(p) > 6 {
		return route, false
	}
	if len(p) == 4 {
		return route, true
	}
	route.account = p[4]
	route.copyPassword = len(p) == 6 && p[5] == "copy-password"
	validID := deploy.ID.MatchString(route.account) || route.account == "mysql-root" || route.account == "mysql-user" || route.account == "redis-default"
	return route, validID && (len(p) == 5 || route.copyPassword)
}

func (h *HTTP) softwareLoginAccountRoute(w http.ResponseWriter, r *http.Request, actor *session, path string) bool {
	route, ok := softwareLoginAccountPath(path)
	if !ok {
		return false
	}
	if !h.sensitiveResponseAllowed(r) {
		h.fail(w, r, 403, "SOFTWARE_LOGIN_TLS_REQUIRED", "请通过 HTTPS 或本机回环地址访问软件登录账号")
		return true
	}
	switch {
	case r.Method == http.MethodGet && route.account == "" && !route.copyPassword:
		h.getSoftwareLoginAccount(w, r, actor, route)
	case r.Method == http.MethodPost && route.copyPassword:
		h.copySoftwareLoginPassword(w, r, actor, route)
	case r.Method == http.MethodPost && (route.account == "" || deploy.ID.MatchString(route.account)):
		h.saveSoftwareLoginAccount(w, r, actor, route)
	default:
		h.fail(w, r, 405, "METHOD_NOT_ALLOWED", "请求方法不支持")
	}
	return true
}

func softwareLoginSecretID(instance, account string) string {
	if instance == account {
		return "software-login:" + instance
	}
	return "software-login:" + instance + ":" + account
}

func (r loginAccountRoute) auditObject(account string) string {
	if r.legacy {
		return r.instance
	}
	return r.instance + "/" + account
}

func (a softwareLoginAccount) metadata(version int) map[string]any {
	return map[string]any{
		"username": a.Username, "fullName": a.FullName, "email": a.Email,
		"hasPassword": a.Password != "", "version": strconv.Itoa(version),
	}
}

func (s *Store) verifySoftwareLoginAccounts(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) error {
	rows, err := q.Query("SELECT software_id,account_id,version,ciphertext FROM software_login_accounts")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, account string
		var version int
		var ciphertext []byte
		if err = rows.Scan(&id, &account, &version, &ciphertext); err != nil {
			return err
		}
		plaintext, err := unseal(s.Key, softwareLoginSecretID(id, account), version, ciphertext)
		clear(plaintext)
		if err != nil {
			return errors.New("KEY_MISMATCH_OR_SOFTWARE_LOGIN_DAMAGED")
		}
	}
	return rows.Err()
}

func (s *Store) openSoftwareLoginAccount(instance, id string, version int, ciphertext []byte) (softwareLoginAccount, error) {
	var account softwareLoginAccount
	plaintext, err := unseal(s.Key, softwareLoginSecretID(instance, id), version, ciphertext)
	defer clear(plaintext)
	if err == nil {
		err = deploy.StrictJSON(plaintext, &account)
	}
	return account, err
}

const softwareLoginEligible = `s.runtime_kind<>'TOOL' AND s.management_mode<>'RELEASED'
		AND s.actual_state<>'REMOVED' AND s.installation_status<>'UNINSTALLED'`

// The original endpoint keeps addressing the first registered manual account.
func (s *Store) loadSoftwareLoginAccount(tx *sql.Tx, instance, requested string) (string, softwareLoginAccount, int, error) {
	var id string
	var version int
	var ciphertext []byte
	var account softwareLoginAccount
	err := tx.QueryRow(`SELECT coalesce(a.account_id,nullif(?,''),s.id),coalesce(a.version,0),a.ciphertext
	FROM software_instances s LEFT JOIN software_login_accounts a ON a.software_id=s.id AND a.account_id=
	coalesce(nullif(?,''),(SELECT x.account_id FROM software_login_accounts x WHERE x.software_id=s.id ORDER BY x.rowid LIMIT 1))
	WHERE s.id=? AND `+softwareLoginEligible, requested, requested, instance).Scan(&id, &version, &ciphertext)
	if err == nil && version > 0 {
		account, err = s.openSoftwareLoginAccount(instance, id, version, ciphertext)
	}
	return id, account, version, err
}

func (a softwareLoginAccount) row(id string, version int) map[string]any {
	row := a.metadata(version)
	row["id"], row["source"], row["label"] = id, "MANUAL", "手工登记"
	return row
}

func (h *HTTP) softwareLoginAccountError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		h.fail(w, r, 404, "NOT_FOUND", "软件或登录账号已不可用，请刷新软件详情")
		return
	}
	h.internal(w, r, err)
}

func (h *HTTP) getSoftwareLoginAccount(w http.ResponseWriter, r *http.Request, actor *session, route loginAccountRoute) {
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if !route.legacy {
		accounts, err := h.store.listSoftwareLoginAccounts(tx, route.instance)
		if err != nil {
			h.softwareLoginAccountError(w, r, err)
			return
		}
		h.success(w, r, map[string]any{"accounts": accounts})
		return
	}
	_, account, version, err := h.store.loadSoftwareLoginAccount(tx, route.instance, "")
	if err != nil {
		h.softwareLoginAccountError(w, r, err)
		return
	}
	h.success(w, r, account.metadata(version))
}

func softwareLoginVersion(raw string) (int, bool) {
	version, err := strconv.Atoi(raw)
	return version, err == nil && version >= 0 && strconv.Itoa(version) == raw
}

func (a *softwareLoginAccount) normalize() bool {
	a.Username, a.FullName, a.Email = strings.TrimSpace(a.Username), strings.TrimSpace(a.FullName), strings.TrimSpace(a.Email)
	if a.Username == "" || utf8.RuneCountInString(a.Username) > 100 || utf8.RuneCountInString(a.FullName) > 100 ||
		strings.ContainsFunc(a.Username+a.FullName+a.Email, unicode.IsControl) || len(a.Email) > 254 ||
		len(a.Password) > 1024 || strings.ContainsAny(a.Password, "\r\n\x00") {
		return false
	}
	if a.Email == "" {
		return true
	}
	address, err := mail.ParseAddress(a.Email)
	return err == nil && address.Address == a.Email
}

func (h *HTTP) saveSoftwareLoginAccount(w http.ResponseWriter, r *http.Request, actor *session, route loginAccountRoute) {
	var in struct {
		softwareLoginAccount
		ID              string `json:"id"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if h.decodeSensitive(w, r, actor, &in) != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	expected, validVersion := softwareLoginVersion(in.ExpectedVersion)
	create := !route.legacy && route.account == ""
	if !validVersion || !in.normalize() || create && (!deploy.ID.MatchString(in.ID) || expected != 0) || !create && in.ID != "" && in.ID != route.account {
		h.fail(w, r, 400, "INVALID_INPUT", "请填写有效的用户名、全名、邮箱和密码")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	requested := route.account
	if create {
		requested = in.ID
	}
	accountID, current, version, err := h.store.loadSoftwareLoginAccount(tx, route.instance, requested)
	if err != nil {
		h.softwareLoginAccountError(w, r, err)
		return
	}
	if !route.legacy && !create && version == 0 {
		h.softwareLoginAccountError(w, r, sql.ErrNoRows)
		return
	}
	if expected != version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "登录账号已变化，请刷新后重试")
		return
	}
	if in.Password == "" {
		in.Password = current.Password
	}
	if in.Password == "" {
		h.fail(w, r, 400, "INVALID_INPUT", "首次保存时请输入软件登录密码")
		return
	}
	version++
	plaintext, err := json.Marshal(in.softwareLoginAccount)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer clear(plaintext)
	ciphertext, err := seal(h.store.Key, softwareLoginSecretID(route.instance, accountID), version, plaintext)
	if err == nil {
		_, err = tx.Exec(`INSERT INTO software_login_accounts(software_id,account_id,version,ciphertext) VALUES(?,?,?,?)
			ON CONFLICT(software_id,account_id) DO UPDATE SET version=excluded.version,ciphertext=excluded.ciphertext`, route.instance, accountID, version, ciphertext)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, "SOFTWARE_LOGIN_SAVED", route.auditObject(accountID), "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if route.legacy {
		h.success(w, r, in.metadata(version))
		return
	}
	h.success(w, r, in.row(accountID, version))
}

func (h *HTTP) copySoftwareLoginPassword(w http.ResponseWriter, r *http.Request, actor *session, route loginAccountRoute) {
	var in struct {
		ExpectedVersion string `json:"expectedVersion"`
		Password        string `json:"password"`
	}
	if h.decodeSensitive(w, r, actor, &in) != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	if route.legacy || deploy.ID.MatchString(route.account) {
		version, valid := softwareLoginVersion(in.ExpectedVersion)
		if !valid || version == 0 {
			h.fail(w, r, 400, "INVALID_INPUT", "登录账号版本无效，请刷新后重试")
			return
		}
	}
	if in.ExpectedVersion == "" || len(in.ExpectedVersion) > 128 || len(in.Password) == 0 || len(in.Password) > 64 {
		h.fail(w, r, 400, "INVALID_INPUT", "请输入登录 Ops 的管理员密码，最多 64 字节")
		return
	}
	obj := route.auditObject(route.account)
	if !h.confirmSensitiveAdminPassword(w, r, actor, obj, in.Password, softwareLoginReadAudit, "SOFTWARE_LOGIN") {
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	password, err := h.store.softwareLoginPassword(tx, route, in.ExpectedVersion)
	if err != nil {
		if errors.Is(err, errLoginVersionConflict) {
			h.fail(w, r, 409, "VERSION_CONFLICT", "登录账号已变化，请刷新后重试")
			return
		}
		h.softwareLoginAccountError(w, r, err)
		return
	}
	defer clear(password)
	if err = auditTx(tx, actor.Username, softwareLoginReadAudit, obj, "SUCCESS", r.Context().Value(traceKey{}).(string)); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, map[string]string{"password": string(password)})
}
