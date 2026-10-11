package controller

import (
	"database/sql"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
	"time"
)

func (h *HTTP) setCookie(w http.ResponseWriter, token string, age int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: h.origin.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func (h *HTTP) getSession(r *http.Request) (*session, error) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || len(cookie.Value) != 64 {
		return nil, nil
	}
	row := h.store.DB.QueryRow(`SELECT s.token_hash,coalesce(s.admin_id,''),coalesce(a.username,''),s.csrf,s.generation FROM sessions s LEFT JOIN admins a ON a.id=s.admin_id WHERE s.token_hash=? AND s.expires_at>? AND s.last_seen>? AND (s.admin_id IS NULL OR s.generation=a.session_generation)`, hashToken(cookie.Value), time.Now().Unix(), time.Now().Add(-30*time.Minute).Unix())
	result := new(session)
	if err = row.Scan(&result.Hash, &result.AdminID, &result.Username, &result.CSRF, &result.Generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	_, err = h.store.DB.Exec("UPDATE sessions SET last_seen=? WHERE token_hash=?", time.Now().Unix(), result.Hash)
	return result, err
}
func (h *HTTP) csrf(w http.ResponseWriter, r *http.Request) {
	existing, err := h.getSession(r)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if existing != nil {
		h.success(w, r, map[string]string{"csrfToken": existing.CSRF})
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err = tx.Exec("DELETE FROM sessions WHERE expires_at<=? OR last_seen<?", now, now-1800); err != nil {
		h.internal(w, r, err)
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 2000 {
		h.fail(w, r, 429, "SESSION_LIMIT", "会话已达上限，请稍后重试")
		return
	}
	token, csrf := randomToken(), randomToken()
	if _, err = tx.Exec("INSERT INTO sessions VALUES(?,NULL,?,0,?,?,?)", hashToken(token), csrf, now, now, now+600); err != nil {
		h.internal(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		h.internal(w, r, err)
		return
	}
	h.setCookie(w, token, 600)
	h.success(w, r, map[string]string{"csrfToken": csrf})
}
func (h *HTTP) takeAttempt() bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()
	if time.Since(h.rateStart) >= time.Minute {
		h.rateStart = time.Now()
		h.attempts = 0
	}
	h.attempts++
	return h.attempts <= 60
}
func (h *HTTP) login(w http.ResponseWriter, r *http.Request, anonymous *session) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &input); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	if len(input.Username) > 32 || len(input.Password) > 64 {
		h.fail(w, r, 400, "INVALID_INPUT", "账号或密码长度不正确")
		return
	}
	if !h.takeAttempt() {
		h.fail(w, r, 429, "LOGIN_RATE_LIMIT", "登录请求过多，请稍后重试")
		return
	}
	select {
	case h.hashSlots <- struct{}{}:
		defer func() { <-h.hashSlots }()
	default:
		h.fail(w, r, 429, "LOGIN_BUSY", "登录处理中，请稍后重试")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var validSession int
	err = tx.QueryRow(`SELECT count(*) FROM sessions s LEFT JOIN admins a ON a.id=s.admin_id WHERE s.token_hash=? AND s.expires_at>? AND s.last_seen>? AND (s.admin_id IS NULL OR s.generation=a.session_generation)`, anonymous.Hash, time.Now().Unix(), time.Now().Add(-30*time.Minute).Unix()).Scan(&validSession)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if validSession != 1 {
		h.fail(w, r, 401, "AUTH_REQUIRED", "请刷新登录页")
		return
	}
	var id, username string
	var hash []byte
	var generation, attempts int
	var locked int64
	err = tx.QueryRow("SELECT id,username,password_hash,session_generation,failed_attempts,locked_until FROM admins WHERE username=?", strings.ToLower(strings.TrimSpace(input.Username))).Scan(&id, &username, &hash, &generation, &attempts, &locked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	if id == "" {
		hash = h.dummyHash
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(input.Password)) == nil
	if !match || id == "" || locked > time.Now().Unix() {
		if id != "" && locked <= time.Now().Unix() {
			if locked > 0 {
				attempts = 0
			}
			attempts++
			locked = 0
			if attempts >= 5 {
				locked = time.Now().Add(15 * time.Minute).Unix()
			}
			if _, err = tx.Exec("UPDATE admins SET failed_attempts=?,locked_until=? WHERE id=?", attempts, locked, id); err != nil {
				h.internal(w, r, err)
				return
			}
		}
		if err = auditTx(tx, "anonymous", "LOGIN_FAILED", "", "AUTH_FAILED", r.Context().Value(traceKey{}).(string)); err != nil {
			h.internal(w, r, err)
			return
		}
		if err = tx.Commit(); err != nil {
			h.internal(w, r, err)
			return
		}
		h.fail(w, r, 401, "AUTH_FAILED", "账号或密码不正确，连续失败将锁定15分钟")
		return
	}
	now := time.Now().Unix()
	token, csrf := randomToken(), randomToken()
	if _, err = tx.Exec("DELETE FROM sessions WHERE token_hash=?", anonymous.Hash); err == nil {
		_, err = tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?,?)", hashToken(token), id, csrf, generation, now, now, now+8*3600)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE admins SET failed_attempts=0,locked_until=0 WHERE id=?", id)
	}
	if err == nil {
		err = auditTx(tx, username, "LOGIN", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.setCookie(w, token, 8*3600)
	h.success(w, r, map[string]string{"username": username, "csrfToken": csrf})
}
func (h *HTTP) logout(w http.ResponseWriter, r *http.Request, s *session) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM sessions WHERE token_hash=?", s.Hash); err == nil {
		err = auditTx(tx, s.Username, "LOGOUT", s.AdminID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.setCookie(w, "", -1)
	h.success(w, r, nil)
}
func (h *HTTP) password(w http.ResponseWriter, r *http.Request, s *session) {
	var input struct {
		Current string `json:"currentPassword"`
		Next    string `json:"newPassword"`
	}
	if err := decode(w, r, &input); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	if err := ValidatePassword(input.Next); err != nil {
		h.fail(w, r, 400, "INVALID_PASSWORD", err.Error())
		return
	}
	if !h.takeAttempt() {
		h.fail(w, r, 429, "LOGIN_RATE_LIMIT", "请求过多，请稍后重试")
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
	var hash []byte
	if err = tx.QueryRow("SELECT password_hash FROM admins WHERE id=? AND session_generation=?", s.AdminID, s.Generation).Scan(&hash); err != nil {
		h.fail(w, r, 401, "AUTH_REQUIRED", "请重新登录")
		return
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(input.Current)) != nil {
		h.fail(w, r, 400, "PASSWORD_MISMATCH", "当前密码不正确")
		return
	}
	hash, err = bcrypt.GenerateFromPassword([]byte(input.Next), 12)
	if err == nil {
		_, err = tx.Exec("UPDATE admins SET password_hash=?,session_generation=session_generation+1 WHERE id=?", hash, s.AdminID)
	}
	if err == nil {
		_, err = tx.Exec("DELETE FROM sessions WHERE admin_id=?", s.AdminID)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "PASSWORD_CHANGED", s.AdminID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.setCookie(w, "", -1)
	h.success(w, r, nil)
}
