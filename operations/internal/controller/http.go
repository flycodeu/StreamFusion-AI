package controller

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const api = "/ops/api/v1"
const cookieName = "sf_ops_session"

type traceKey struct{}
type session struct {
	Hash, AdminID, Username, CSRF string
	Generation                    int
}
type HTTP struct {
	store            *Store
	origin           *url.URL
	assets           http.Handler
	started          time.Time
	logger           *log.Logger
	rateMu           sync.Mutex
	rateStart        time.Time
	attempts         int
	hashSlots        chan struct{}
	dummyHash        []byte
	requestCipher    requestCipher
	platformToken    []byte
	hostCheckSlots   chan struct{}
	observationMu    sync.Mutex
	observationHosts map[string]struct{}
}
type Envelope struct {
	Code      string `json:"code"`
	Msg       string `json:"msg"`
	Data      any    `json:"data"`
	TraceID   string `json:"traceId"`
	Timestamp string `json:"timestamp"`
}

func NewHTTP(s *Store, origin string, assets http.Handler, logger *log.Logger) (*HTTP, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Path != "" {
		return nil, errors.New("INVALID_ORIGIN")
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte(randomToken()), 12)
	if err != nil {
		return nil, err
	}
	return &HTTP{store: s, origin: u, assets: assets, started: time.Now(), logger: logger, hashSlots: make(chan struct{}, 2), dummyHash: dummy, hostCheckSlots: make(chan struct{}, 4)}, nil
}

// ConfigurePlatformTokenFile enables the server-to-server preview catalog.
// An empty path keeps the integration disabled; no browser session can enable it.
func (h *HTTP) ConfigurePlatformTokenFile(path string) error {
	if path == "" {
		return nil
	}
	if h.origin.Scheme != "https" {
		ip := net.ParseIP(h.origin.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return errors.New("PLATFORM_TOKEN_REQUIRES_TLS")
		}
	}
	if runtime.GOOS != "windows" {
		parent, err := os.Lstat(filepath.Dir(path))
		if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0077 != 0 {
			return errors.New("PLATFORM_TOKEN_DIRECTORY_INVALID")
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 32 || info.Size() > 256 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("PLATFORM_TOKEN_FILE_INVALID")
	}
	if err := validatePlatformTokenAccess(path); err != nil {
		return err
	}
	token, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	token = []byte(strings.TrimSpace(string(token)))
	if len(token) < 32 || len(token) > 128 || strings.ContainsAny(string(token), " \t\r\n") {
		return errors.New("PLATFORM_TOKEN_FILE_INVALID")
	}
	h.platformToken = token
	return nil
}
func (h *HTTP) reply(w http.ResponseWriter, r *http.Request, status int, code, msg string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{code, msg, data, r.Context().Value(traceKey{}).(string), Now()})
}
func (h *HTTP) fail(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	h.reply(w, r, status, code, msg, nil)
}
func (h *HTTP) success(w http.ResponseWriter, r *http.Request, data any) {
	h.reply(w, r, 200, "SUCCESS", "", data)
}
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("需要 application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return errors.New("请求字段或内容不正确")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("请求必须为单个 JSON 对象")
	}
	return nil
}
func (h *HTTP) internal(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errSessionRevoked) {
		h.fail(w, r, 401, "AUTH_REQUIRED", "请重新登录")
		return
	}
	h.logger.Printf("traceId=%s error=STATE_OPERATION_FAILED", r.Context().Value(traceKey{}))
	h.fail(w, r, 500, "STATE_OPERATION_FAILED", "状态操作失败，请检查控制器日志和磁盘")
}

var errSessionRevoked = errors.New("SESSION_REVOKED")

func (h *HTTP) beginAuthorized(s *session) (*sql.Tx, error) {
	tx, err := h.store.DB.Begin()
	if err != nil {
		return nil, err
	}
	var count int
	err = tx.QueryRow(`SELECT count(*) FROM sessions s JOIN admins a ON a.id=s.admin_id WHERE s.token_hash=? AND s.generation=a.session_generation AND s.expires_at>? AND s.last_seen>?`, s.Hash, time.Now().Unix(), time.Now().Add(-30*time.Minute).Unix()).Scan(&count)
	if err != nil || count != 1 {
		tx.Rollback()
		if err != nil {
			return nil, err
		}
		return nil, errSessionRevoked
	}
	return tx, nil
}
func (h *HTTP) validHost(host string) bool {
	if host == h.origin.Host {
		return true
	}
	return h.origin.Hostname() == "127.0.0.1" && host == "localhost:"+h.origin.Port()
}
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	trace := randomToken()[:32]
	r = r.WithContext(context.WithValue(r.Context(), traceKey{}, trace))
	w.Header().Set("X-Trace-Id", trace)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
	w.Header().Set("Cache-Control", "no-store")
	if !h.validHost(r.Host) {
		h.fail(w, r, 403, "HOST_REJECTED", "访问地址不在允许范围")
		return
	}
	if !strings.HasPrefix(r.URL.Path, api+"/") {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.fail(w, r, 405, "METHOD_NOT_ALLOWED", "请求方法不支持")
			return
		}
		h.assets.ServeHTTP(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, api)
	if r.Method == "GET" && path == "/health" {
		h.success(w, r, map[string]string{"status": "UP"})
		return
	}
	if strings.HasPrefix(path, "/platform/") {
		h.platformRoute(w, r, path)
		return
	}
	if r.Method != "GET" {
		origin, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || origin.Scheme != h.origin.Scheme || !h.validHost(origin.Host) || origin.Path != "" || origin.RawQuery != "" || origin.User != nil {
			h.fail(w, r, 403, "ORIGIN_REJECTED", "请求来源不允许")
			return
		}
	}
	if r.Method == "GET" && path == "/auth/csrf" {
		h.csrf(w, r)
		return
	}
	auth, err := h.getSession(r)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	if auth == nil {
		h.fail(w, r, 401, "AUTH_REQUIRED", "请重新登录")
		return
	}
	if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(auth.CSRF)) != 1 {
		h.fail(w, r, 403, "CSRF_INVALID", "登录状态已变化，请刷新后重试")
		return
	}
	if r.Method == "POST" && path == "/auth/login" {
		h.login(w, r, auth)
		return
	}
	if r.Method == "POST" && path == "/auth/secure-challenge" {
		h.secureChallenge(w, r, auth)
		return
	}
	if auth.AdminID == "" {
		h.fail(w, r, 401, "AUTH_REQUIRED", "请先登录")
		return
	}
	if h.operationsRoute(w, r, auth, path) {
		return
	}
	switch r.Method + " " + path {
	case "GET /auth/me":
		h.success(w, r, map[string]string{"id": auth.AdminID, "username": auth.Username})
	case "POST /auth/logout":
		h.logout(w, r, auth)
	case "POST /auth/password":
		h.password(w, r, auth)
	case "GET /settings":
		h.settings(w, r)
	case "PUT /settings":
		h.updateSettings(w, r, auth)
	case "GET /status":
		h.status(w, r)
	case "GET /audit":
		h.audit(w, r)
	case "GET /credentials":
		h.credentials(w, r)
	case "POST /credentials":
		h.createCredential(w, r, auth, "")
	case "POST /backups":
		h.downloadBackup(w, r, auth)
	default:
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if r.Method == "POST" && len(parts) == 3 && parts[0] == "credentials" && parts[2] == "revisions" {
			h.createCredential(w, r, auth, parts[1])
			return
		}
		if r.Method == "POST" && len(parts) == 5 && parts[0] == "credentials" && parts[2] == "revisions" && parts[4] == "revoke" {
			revision, e := strconv.Atoi(parts[3])
			if e == nil && revision > 0 {
				h.revokeCredential(w, r, auth, parts[1], revision)
				return
			}
		}
		h.fail(w, r, 404, "NOT_FOUND", "接口不存在")
	}
}
func (h *HTTP) settings(w http.ResponseWriter, r *http.Request) {
	var name, id, created string
	var version int64
	if err := h.store.DB.QueryRow("SELECT name,controller_id,version,created_at FROM controller_state WHERE id=1").Scan(&name, &id, &version, &created); err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, map[string]string{"name": name, "controllerId": id, "version": strconv.FormatInt(version, 10), "createdAt": created})
}
func (h *HTTP) updateSettings(w http.ResponseWriter, r *http.Request, s *session) {
	var input struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := decode(w, r, &input); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	version, err := strconv.ParseInt(input.Version, 10, 64)
	if err != nil || version < 1 || len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 60 {
		h.fail(w, r, 400, "INVALID_INPUT", "名称须为1–60字，版本须有效")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE controller_state SET name=?,version=version+1 WHERE id=1 AND version=?", input.Name, version)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		h.fail(w, r, 409, "VERSION_CONFLICT", "配置已被修改，请刷新")
		return
	}
	if err = auditTx(tx, s.Username, "SETTINGS_UPDATED", "controller", "SUCCESS", r.Context().Value(traceKey{}).(string)); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, map[string]string{"version": strconv.FormatInt(version+1, 10)})
}
func (h *HTTP) status(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	h.success(w, r, map[string]any{"status": "UP", "release": Version, "schemaVersion": SchemaVersion, "goVersion": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "listen": h.origin.Host, "stateDirectory": h.store.Dir, "startedAt": h.started.UTC().Format(time.RFC3339), "uptimeSeconds": int64(time.Since(h.started).Seconds()), "heapBytes": mem.HeapAlloc, "goroutines": runtime.NumGoroutine()})
}
func (h *HTTP) audit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if x := r.URL.Query().Get("limit"); x != "" {
		n, err := strconv.Atoi(x)
		if err != nil || n < 1 || n > 100 {
			h.fail(w, r, 400, "INVALID_INPUT", "limit 范围为1–100")
			return
		}
		limit = n
	}
	before := r.URL.Query().Get("before")
	if len(before) > 120 {
		h.fail(w, r, 400, "INVALID_INPUT", "无效分页位置")
		return
	}
	rows, err := h.store.DB.Query("SELECT id,actor,action,object_id,result,trace_id,created_at FROM audit WHERE (?='' OR created_at||'|'||id<?) ORDER BY created_at DESC,id DESC LIMIT ?", before, before, limit)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]map[string]string, 0)
	cursor := ""
	for rows.Next() {
		var id, actor, action, object, result, trace, created string
		if err = rows.Scan(&id, &actor, &action, &object, &result, &trace, &created); err != nil {
			h.internal(w, r, err)
			return
		}
		items = append(items, map[string]string{"id": id, "actor": actor, "action": action, "objectId": object, "result": result, "traceId": trace, "createdAt": created})
		cursor = created + "|" + id
	}
	if err = rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	if len(items) < limit {
		cursor = ""
	}
	h.success(w, r, map[string]any{"items": items, "nextCursor": cursor})
}

func OriginFor(listen string, tls bool) string {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, listen)
}
