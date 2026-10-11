package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPassword = "Test12"

func TestPasswordLengthBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password string
		valid    bool
	}{
		{"five bytes", "Test1", false},
		{"six bytes", "Test12", true},
		{"eight bytes", "Test1234", true},
		{"previous long password", "Local-test-password-123", true},
		{"64 bytes", strings.Repeat("a", 63) + "1", true},
		{"65 bytes", strings.Repeat("a", 64) + "1", false},
		{"letters only", "abcdef", false},
		{"digits only", "123456", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePassword(tc.password); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

type browser struct {
	t      *testing.T
	h      *HTTP
	cookie *http.Cookie
	csrf   string
}

func setup(t *testing.T) (*Store, *browser) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.InitAdmin("admin", testPassword); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTP(s, "http://127.0.0.1:19090", http.NotFoundHandler(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return s, &browser{t: t, h: h}
}
func (b *browser) call(method, path string, data any, want int) *httptest.ResponseRecorder {
	b.t.Helper()
	var body []byte
	if data != nil {
		body, _ = json.Marshal(data)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:19090"+api+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:19090")
	r.Header.Set("X-CSRF-Token", b.csrf)
	if b.cookie != nil {
		r.AddCookie(b.cookie)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	if w.Code != want {
		b.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		b.cookie = c
	}
	if len(w.Header().Get("X-Trace-Id")) != 32 {
		b.t.Fatal("missing traceId")
	}
	return w
}
func dataOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result struct {
		Data    map[string]any
		TraceID string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.TraceID != w.Header().Get("X-Trace-Id") {
		t.Fatal("traceId mismatch")
	}
	return result.Data
}
func (b *browser) prepare() {
	d := dataOf(b.t, b.call("GET", "/auth/csrf", nil, 200))
	b.csrf = d["csrfToken"].(string)
}
func (b *browser) login() {
	b.prepare()
	d := dataOf(b.t, b.call("POST", "/auth/login", map[string]string{"username": "admin", "password": testPassword}, 200))
	b.csrf = d["csrfToken"].(string)
}

func TestAuthenticationAndWriteProtection(t *testing.T) {
	s, b := setup(t)
	b.call("GET", "/status", nil, 401)
	b.prepare()
	oldCookie := b.cookie
	b.csrf = "wrong"
	b.call("POST", "/auth/login", map[string]string{"username": "admin", "password": testPassword}, 403)
	b.prepare()
	request := httptest.NewRequest("POST", "http://127.0.0.1:19090"+api+"/auth/login", strings.NewReader("{}"))
	request.Header.Set("Origin", "http://attacker.invalid")
	request.AddCookie(b.cookie)
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("cross-origin accepted")
	}
	request = httptest.NewRequest("GET", "http://attacker.invalid"+api+"/health", nil)
	w = httptest.NewRecorder()
	b.h.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("untrusted Host accepted")
	}
	b.login()
	b.call("GET", "/auth/me", nil, 200)
	cookie := b.cookie
	b.cookie = oldCookie
	b.call("GET", "/auth/me", nil, 401)
	b.cookie = cookie
	b.call("PUT", "/settings", map[string]string{"name": "Ops lab", "version": "1"}, 200)
	b.call("PUT", "/settings", map[string]string{"name": "Overwrite", "version": "1"}, 409)
	b.call("PUT", "/settings", map[string]string{"name": "bad", "version": "2", "unexpected": "x"}, 400)
	b.call("GET", "/audit?limit=101", nil, 400)
	before := dataOf(t, b.call("GET", "/settings", nil, 200))
	s.Close()
	reopened, err := Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	b.h.store = reopened
	b.call("GET", "/auth/me", nil, 200)
	after := dataOf(t, b.call("GET", "/settings", nil, 200))
	if before["controllerId"] != after["controllerId"] || after["name"] != "Ops lab" {
		t.Fatal("restart lost state")
	}
	b.call("POST", "/auth/logout", nil, 200)
	b.call("GET", "/auth/me", nil, 401)
}

func TestPasswordChangeRevokesEverySession(t *testing.T) {
	_, b := setup(t)
	b.login()
	second := &browser{t: t, h: b.h}
	second.login()
	stale, err := b.h.getSession(httptestRequest(second.cookie))
	if err != nil {
		t.Fatal(err)
	}
	b.call("POST", "/auth/password", map[string]string{"currentPassword": testPassword, "newPassword": "Next1"}, 400)
	b.call("GET", "/auth/me", nil, 200)
	b.call("POST", "/auth/password", map[string]string{"currentPassword": testPassword, "newPassword": "Next12"}, 200)
	second.call("GET", "/auth/me", nil, 401)
	b.call("GET", "/auth/me", nil, 401)
	if tx, err := b.h.beginAuthorized(stale); err == nil {
		tx.Rollback()
		t.Fatal("stale in-flight session can still write")
	}
	b.prepare()
	b.call("POST", "/auth/login", map[string]string{"username": "admin", "password": testPassword}, 401)
	b.call("POST", "/auth/login", map[string]string{"username": "admin", "password": "Next12"}, 200)
}
func httptestRequest(cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "http://127.0.0.1:19090", nil)
	r.AddCookie(cookie)
	return r
}
func TestLoginLockoutAndSessionExpiry(t *testing.T) {
	s, b := setup(t)
	b.prepare()
	for i := 0; i < 5; i++ {
		b.call("POST", "/auth/login", map[string]string{"username": "admin", "password": "incorrect-password"}, 401)
	}
	b.call("POST", "/auth/login", map[string]string{"username": "admin", "password": testPassword}, 401)
	var locked int64
	if err := s.DB.QueryRow("SELECT locked_until FROM admins").Scan(&locked); err != nil || locked <= time.Now().Unix() {
		t.Fatal("lock not persisted", err)
	}
	if _, err := s.DB.Exec("UPDATE admins SET locked_until=?,failed_attempts=5", time.Now().Unix()-1); err != nil {
		t.Fatal(err)
	}
	b.login()
	if _, err := s.DB.Exec("UPDATE sessions SET last_seen=?", time.Now().Add(-31*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	b.call("GET", "/auth/me", nil, 401)
}
func TestAnonymousLimitAndLoginRate(t *testing.T) {
	s, b := setup(t)
	b.prepare()
	s.DB.Exec("WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<2000) INSERT INTO sessions SELECT printf('%064d',x),NULL,'csrf',0,?,?,? FROM n", time.Now().Unix(), time.Now().Unix(), time.Now().Add(time.Hour).Unix())
	fresh := &browser{t: t, h: b.h}
	fresh.call("GET", "/auth/csrf", nil, 429)
	b.h.rateStart = time.Now()
	b.h.attempts = 60
	b.call("POST", "/auth/login", map[string]string{"username": "admin", "password": testPassword}, 429)
}
func TestStoreLockSchemaAndInitialAccount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("duplicate directory allowed")
	}
	if err = s.InitAdmin("admin", testPassword); err != nil {
		t.Fatal(err)
	}
	if err = s.InitAdmin("other", testPassword); err == nil {
		t.Fatal("repeat initialization allowed")
	}
	s.DB.Exec("PRAGMA user_version=2")
	s.Close()
	if other, err := Open(dir); err == nil || !strings.Contains(err.Error(), "SCHEMA_TOO_NEW") {
		if other != nil {
			other.Close()
		}
		t.Fatal("newer schema accepted", err)
	}
}
func TestBackupRecoveryAndCredentials(t *testing.T) {
	s, b := setup(t)
	b.login()
	secret := "never-log-this-token-87d7"
	d := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "Git access", "kind": "TOKEN", "secret": secret}, 201))
	id := d["id"].(string)
	b.call("POST", "/credentials/"+id+"/revisions", map[string]string{"name": "Git access", "kind": "TOKEN", "secret": secret + "-new", "expectedRevision": "1"}, 201)
	b.call("POST", "/credentials/"+id+"/revisions", map[string]string{"name": "stale", "kind": "TOKEN", "secret": "other", "expectedRevision": "1"}, 409)
	metadata := b.call("GET", "/credentials", nil, 200).Body.String()
	if strings.Contains(metadata, secret) || strings.Contains(metadata, "material") {
		t.Fatal("secret leaked to metadata")
	}
	b.call("POST", "/credentials/"+id+"/revisions/1/revoke", nil, 200)
	b.call("PUT", "/settings", map[string]string{"name": "Latest WAL state", "version": "1"}, 200)
	root := t.TempDir()
	archive := filepath.Join(root, "backup.tar.gz")
	if err := s.BackupTo(archive); err != nil {
		t.Fatal(err)
	}
	if err := s.BackupTo(archive); err == nil {
		t.Fatal("backup overwrote file")
	}
	key := filepath.Join(root, "recovery.key")
	if err := os.WriteFile(key, s.Key, 0600); err != nil {
		t.Fatal(err)
	}
	files := readArchive(t, archive)
	if _, ok := files["master.key"]; ok {
		t.Fatal("backup includes master key")
	}
	for _, body := range files {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatal("backup contains plaintext credential")
		}
	}
	b.call("POST", "/backups", nil, 200)
	restoredPath := filepath.Join(root, "restored")
	if err := Restore(archive, key, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var name string
	restored.DB.QueryRow("SELECT name FROM controller_state").Scan(&name)
	if name != "Latest WAL state" {
		t.Fatal("backup lost latest WAL writes")
	}
	var count int
	restored.DB.QueryRow("SELECT count(*) FROM sessions").Scan(&count)
	if count != 0 {
		t.Fatal("restored sessions not invalidated")
	}
	if err = restored.verifySecrets(); err != nil {
		t.Fatal(err)
	}
	if err = Restore(archive, key, restoredPath); err == nil {
		t.Fatal("restore replaced existing state")
	}
	wrongKey := filepath.Join(root, "wrong.key")
	os.WriteFile(wrongKey, bytes.Repeat([]byte{3}, 32), 0600)
	if err = Restore(archive, wrongKey, filepath.Join(root, "wrong-key")); err == nil {
		t.Fatal("wrong recovery key accepted")
	}
	// Losing the existing key must not silently generate a replacement.
	s.Close()
	os.Remove(filepath.Join(s.Dir, "master.key"))
	if opened, err := Open(s.Dir); err == nil || !strings.Contains(err.Error(), "KEY_MISSING") {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("missing key accepted", err)
	}
}
func TestRejectDamagedArchives(t *testing.T) {
	s, _ := setup(t)
	root := t.TempDir()
	archive := filepath.Join(root, "good.tar.gz")
	s.BackupTo(archive)
	key := filepath.Join(root, "key")
	os.WriteFile(key, s.Key, 0600)
	files := readArchive(t, archive)
	files["controller.sqlite"][0] ^= 1
	bad := filepath.Join(root, "bad.tar.gz")
	writeArchive(t, bad, files)
	if err := Restore(bad, key, filepath.Join(root, "bad-restore")); err == nil {
		t.Fatal("bad checksum accepted")
	}
	traversal := filepath.Join(root, "traversal.tar.gz")
	writeArchive(t, traversal, map[string][]byte{"../outside": []byte("attack")})
	if err := Restore(traversal, key, filepath.Join(root, "traversal-restore")); err == nil {
		t.Fatal("path traversal accepted")
	}
	bytes, _ := os.ReadFile(archive)
	trunc := filepath.Join(root, "truncated.tar.gz")
	os.WriteFile(trunc, bytes[:len(bytes)-8], 0600)
	if err := Restore(trunc, key, filepath.Join(root, "truncated-restore")); err == nil {
		t.Fatal("truncated gzip accepted")
	}
}
func readArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	result := map[string][]byte{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		result[h.Name], err = io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}
func writeArchive(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tr := tar.NewWriter(gz)
	for name, data := range files {
		if err = tr.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tr.Write(data)
	}
	tr.Close()
	gz.Close()
	f.Close()
}
func TestUnrecognizedDatabaseIsPreserved(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE unrelated(value TEXT); INSERT INTO unrelated VALUES('keep')")
	db.Close()
	if s, err := Open(dir); err == nil {
		s.Close()
		t.Fatal("unrecognized schema accepted")
	}
	db, _ = sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	defer db.Close()
	var value string
	if err = db.QueryRow("SELECT value FROM unrelated").Scan(&value); err != nil || value != "keep" {
		t.Fatal("existing table changed")
	}
}
func TestLogRotation(t *testing.T) {
	r, err := NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	block := bytes.Repeat([]byte("a"), 1<<20)
	for i := 0; i < 110; i++ {
		if _, err = r.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(r.path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected bounded five logs, got %d", len(entries))
	}
	for _, entry := range entries {
		info, _ := entry.Info()
		if info.Size() > 20<<20 {
			t.Fatal("log exceeded limit")
		}
	}
}

func TestRecoveryNeverReplacesEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staging")
	dest := filepath.Join(root, "existing")
	os.Mkdir(source, 0700)
	os.Mkdir(dest, 0700)
	if err := publishState(source, dest); err == nil {
		t.Fatal("existing empty directory replaced")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("staging lost after rejection")
	}
}

func TestConcurrentConfigurationWrites(t *testing.T) {
	_, b := setup(t)
	b.login()
	statuses := make(chan int, 2)
	for _, name := range []string{"First", "Second"} {
		go func(name string) {
			body, _ := json.Marshal(map[string]string{"name": name, "version": "1"})
			r := httptest.NewRequest("PUT", "http://127.0.0.1:19090"+api+"/settings", bytes.NewReader(body))
			r.Header.Set("Origin", "http://127.0.0.1:19090")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", b.csrf)
			r.AddCookie(b.cookie)
			w := httptest.NewRecorder()
			b.h.ServeHTTP(w, r)
			statuses <- w.Code
		}(name)
	}
	a, c := <-statuses, <-statuses
	if !((a == 200 && c == 409) || (a == 409 && c == 200)) {
		t.Fatalf("write outcomes=%d,%d", a, c)
	}
}
