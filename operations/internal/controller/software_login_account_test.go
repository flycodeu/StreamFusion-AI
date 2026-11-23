package controller

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const softwareLoginTestPeer = "127.0.0.1:49100"

func callSoftwareLogin(t *testing.T, b *browser, method, path, peer string, payload any, status int, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:19090"+api+path, bytes.NewReader(body))
	r.RemoteAddr = peer
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:19090")
	r.Header.Set("X-CSRF-Token", b.csrf)
	if b.cookie != nil {
		r.AddCookie(b.cookie)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	var response Envelope
	err = json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil || w.Code != status || response.Code != code || (status >= 400 && response.Data != nil) {
		t.Fatalf("%s %s returned status=%d code=%s, parse error=%v", method, path, w.Code, response.Code, err)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("software login response must not be cached")
	}
	return w
}

func setupSoftwareLogin(t *testing.T) (*Store, *browser, string) {
	t.Helper()
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "account fixture", "kind": "LOCAL"}, http.StatusCreated))
	return s, b, host["id"].(string)
}

func TestSoftwareLoginAccountProtectedSaveAndCopy(t *testing.T) {
	s, b, host := setupSoftwareLogin(t)
	var logs bytes.Buffer
	b.h.logger = log.New(&logs, "", 0)
	item := registerTestSoftware(t, b, host, "DOCKER", "jenkins-ci", "JENKINS")
	other := registerTestSoftware(t, b, host, "SYSTEMD", "other-login.service", "OTHER")
	tool := registerTestSoftware(t, b, host, "TOOL", "git", "GIT")
	id := item["id"].(string)
	path := "/software-instances/" + id + "/login-account"
	otherPath := "/software-instances/" + other["id"].(string) + "/login-account"
	copyPath := path + "/copy-password"
	call := func(method, path string, payload any, status int, code string) *httptest.ResponseRecorder {
		t.Helper()
		return callSoftwareLogin(t, b, method, path, softwareLoginTestPeer, payload, status, code)
	}
	get := func(path string) map[string]any {
		t.Helper()
		return dataOf(t, call("GET", path, nil, 200, "SUCCESS"))
	}
	post := func(path string, payload map[string]string, status int, code string) *httptest.ResponseRecorder {
		t.Helper()
		return call("POST", path, b.encrypt(path, payload), status, code)
	}
	var anonymous browser
	anonymous.h = b.h
	callSoftwareLogin(t, &anonymous, "GET", path, softwareLoginTestPeer, nil, 401, "AUTH_REQUIRED")
	callSoftwareLogin(t, b, "GET", path, "198.51.100.1:49100", nil, 403, "SOFTWARE_LOGIN_TLS_REQUIRED")
	call("GET", "/software-instances/"+tool["id"].(string)+"/login-account", nil, 404, "NOT_FOUND")
	for _, p := range []string{path, otherPath} {
		if empty := get(p); empty["version"] != "0" || empty["hasPassword"] != false || empty["username"] != "" || empty["fullName"] != "" || empty["email"] != "" {
			t.Fatal("an unfilled account must return empty metadata")
		}
	}

	const password = " Test fixture secret ║ 20 "
	const username = "ci-local-test-owner"
	const fullName = "Fixture Name for CI"
	const email = "jenkins-fixture@example.test"
	input := map[string]string{"expectedVersion": "0", "username": username, "password": password, "fullName": fullName, "email": email}
	call("POST", path, input, 400, "SECURE_REQUEST_INVALID")
	post(path, map[string]string{"expectedVersion": "0", "username": username}, 400, "INVALID_INPUT")
	post(path, map[string]string{"expectedVersion": "0", "username": username, "password": password, "email": "Bad Email"}, 400, "INVALID_INPUT")
	encrypted := b.encrypt(path, input)
	call("POST", path, encrypted, 200, "SUCCESS")
	call("POST", path, encrypted, 400, "SECURE_REQUEST_INVALID")
	if saved := get(path); saved["username"] != username || saved["fullName"] != fullName || saved["email"] != email || saved["version"] != "1" || saved["hasPassword"] != true || saved["password"] != nil {
		t.Fatal("saved metadata or its password projection is incorrect")
	}
	if empty := get(otherPath); empty["version"] != "0" || empty["hasPassword"] != false {
		t.Fatal("account was visible on another software instance")
	}
	post(path, input, 409, "VERSION_CONFLICT")
	post(path, map[string]string{"expectedVersion": "1", "username": username, "fullName": "Edited name", "email": ""}, 200, "SUCCESS")
	if saved := get(path); saved["version"] != "2" || saved["fullName"] != "Edited name" || saved["email"] != "" || saved["hasPassword"] != true || saved["password"] != nil {
		t.Fatal("a metadata edit must retain the existing password and advance the version")
	}
	confirmation := map[string]string{"expectedVersion": "2", "password": testPassword}
	call("GET", copyPath, nil, 405, "METHOD_NOT_ALLOWED")
	callSoftwareLogin(t, b, "POST", copyPath, "198.51.100.1:49100", nil, 403, "SOFTWARE_LOGIN_TLS_REQUIRED")
	call("POST", copyPath, confirmation, 400, "SECURE_REQUEST_INVALID")
	post(copyPath, map[string]string{"expectedVersion": "2", "password": "wrong-current-password"}, 403, "SOFTWARE_LOGIN_REAUTH_FAILED")
	post(copyPath, map[string]string{"expectedVersion": "1", "password": testPassword}, 409, "VERSION_CONFLICT")
	if copied := dataOf(t, post(copyPath, confirmation, 200, "SUCCESS")); len(copied) != 1 || copied["password"] != password {
		t.Fatal("copy must return only the password and preserve its exact value")
	}

	var ciphertext []byte
	if err := s.DB.QueryRow("SELECT ciphertext FROM software_login_accounts WHERE software_id=?", id).Scan(&ciphertext); err != nil || len(ciphertext) == 0 {
		t.Fatalf("ciphertext unavailable: %v", err)
	}
	for _, field := range []string{username, "Edited name", password} {
		if bytes.Contains(ciphertext, []byte(field)) {
			t.Fatal("account field stored in cleartext")
		}
	}
	for _, p := range []string{"/audit", "/credentials", "/software-instances/" + id} {
		body := b.call("GET", p, nil, 200).Body.String()
		if strings.Contains(body, password) || strings.Contains(body, username) {
			t.Fatal("unrelated endpoint exposed the stored account")
		}
	}
	if strings.Contains(logs.String(), username) || strings.Contains(logs.String(), password) || strings.Contains(logs.String(), testPassword) {
		t.Fatal("controller log contains account contents")
	}
	var savedEvents, copiedEvents, jobs int
	if err := s.DB.QueryRow(`SELECT (SELECT count(*) FROM audit WHERE action='SOFTWARE_LOGIN_SAVED' AND object_id=? AND result='SUCCESS'),
		(SELECT count(*) FROM audit WHERE action='SOFTWARE_LOGIN_READ' AND object_id=? AND result='SUCCESS'), (SELECT count(*) FROM jobs)`, id, id).Scan(&savedEvents, &copiedEvents, &jobs); err != nil || savedEvents != 2 || copiedEvents != 1 || jobs != 0 {
		t.Fatalf("unexpected save/read audit counts or jobs: %d/%d/%d, %v", savedEvents, copiedEvents, jobs, err)
	}
	if _, err := s.DB.Exec("UPDATE software_instances SET management_mode='RELEASED' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	call("GET", path, nil, 404, "NOT_FOUND")
	post(copyPath, confirmation, 404, "NOT_FOUND")
}

func TestSoftwareLoginAccountV28MigrationAndMissingKey(t *testing.T) {
	s, b, host := setupSoftwareLogin(t)
	item := registerTestSoftware(t, b, host, "SYSTEMD", "old-ci.service", "JENKINS")
	id := item["id"].(string)
	dir := s.Dir
	if _, err := s.DB.Exec("DROP TRIGGER application_qa_scope_changed; DROP TABLE application_qa_requests; DROP TABLE application_qa_monitors; DROP TABLE application_jenkins_monitors; DROP TABLE software_login_accounts; DELETE FROM schema_migrations WHERE version>=29; PRAGMA user_version=28"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })
	b.h.store = upgraded
	var version int
	if err = upgraded.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("expected schema %d after migration, got %d (%v)", SchemaVersion, version, err)
	}
	if preserved, err := upgraded.readSoftware(id); err != nil || preserved.Locator != "old-ci.service" || preserved.HostID != host {
		t.Fatalf("migration changed existing software binding: %v", err)
	}
	if backups, err := filepath.Glob(filepath.Join(dir, "before-schema-29-*.tar.gz")); err != nil || len(backups) != 1 {
		t.Fatalf("expected one migration backup, found %d (%v)", len(backups), err)
	}
	path := "/software-instances/" + id + "/login-account"
	input := map[string]string{"expectedVersion": "0", "username": "upgrade-fixture", "password": "Fixture password after upgrade"}
	callSoftwareLogin(t, b, "POST", path, softwareLoginTestPeer, b.encrypt(path, input), 200, "SUCCESS")
	if err = upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	b.h.store = reopened
	result := dataOf(t, callSoftwareLogin(t, b, "GET", path, softwareLoginTestPeer, nil, 200, "SUCCESS"))
	if result["username"] != "upgrade-fixture" || result["hasPassword"] != true || result["version"] != "1" {
		t.Fatal("restart lost the saved software login account")
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "master.key")
	if err = os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(dir); err == nil || !strings.Contains(err.Error(), "KEY_MISSING") {
		if reopened != nil {
			reopened.Close()
		}
		t.Fatal("missing encryption key was accepted for an encrypted software account", err)
	}
	if _, err = os.Stat(key); !os.IsNotExist(err) {
		t.Fatalf("missing key was replaced: %v", err)
	}
}
