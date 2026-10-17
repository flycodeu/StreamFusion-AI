package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (s *Store) readBinding(id string) (deploy.Binding, error) {
	var b deploy.Binding
	var raw string
	e := s.DB.QueryRow("SELECT config_json FROM component_bindings WHERE id=?", id).Scan(&raw)
	if e == nil {
		e = deploy.StrictJSON([]byte(raw), &b)
	}
	return b, e
}
func (h *HTTP) bindingsRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	if path != "/bindings" {
		return false
	}
	if r.Method == "GET" {
		rows, e := h.store.DB.Query("SELECT config_json FROM component_bindings ORDER BY created_at DESC LIMIT 100")
		if e != nil {
			h.internal(w, r, e)
			return true
		}
		defer rows.Close()
		items := []json.RawMessage{}
		for rows.Next() {
			var raw string
			if e = rows.Scan(&raw); e != nil {
				h.internal(w, r, e)
				return true
			}
			items = append(items, json.RawMessage(raw))
		}
		h.success(w, r, items)
		return true
	}
	if r.Method != "POST" {
		return false
	}
	var b deploy.Binding
	if e := decode(w, r, &b); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return true
	}
	b.ID = uuid.NewString()
	if b.Validate() != nil {
		h.fail(w, r, 400, "BINDING_INVALID", "请核对类型、地址、端口、数据库及凭据修订")
		return true
	}
	if b.CredentialID != "" {
		if _, e := h.store.secret(b.CredentialID, b.CredentialRevision, "PASSWORD"); e != nil {
			h.fail(w, r, 409, "CREDENTIAL_UNAVAILABLE", "密码修订不可用")
			return true
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return true
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO component_bindings VALUES(?, ?, nullif(?,''), nullif(?,''), ?)", b.ID, jsonText(b), b.CredentialID, b.CredentialRevision, Now())
	if e == nil {
		e = auditTx(tx, s.Username, "EXTERNAL_BINDING_REGISTERED", b.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
	} else {
		h.reply(w, r, 201, "SUCCESS", "", b)
	}
	return true
}
func (h *HTTP) probeBinding(w http.ResponseWriter, r *http.Request, id string) {
	b, e := h.store.readBinding(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	var in struct {
		TargetID string `json:"targetId"`
	}
	if e = decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	t, e := h.store.readTarget(in.TargetID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(b.Host, strconv.Itoa(b.Port)))
	controller := e == nil
	if conn != nil {
		conn.Close()
	}
	var result map[string]any
	e = h.store.callTarget(ctx, t, deploy.Request{Action: "BINDING_PROBE", Binding: &b}, "", &result)
	targetReachable := false
	if e == nil {
		targetReachable, _ = result["reachable"].(bool)
	}
	h.success(w, r, map[string]any{"controller": map[string]any{"layer": "TCP", "reachable": controller}, "targetHost": map[string]any{"layer": "TCP", "reachable": targetReachable, "executed": e == nil}, "applicationNetwork": nil, "authentication": nil, "observedAt": Now()})
}
func (s *Store) releaseSecrets(req *deploy.Request) error {
	req.Secrets = map[string]string{}
	for _, rel := range []*deploy.Release{req.Candidate, req.Baseline} {
		if rel == nil {
			continue
		}
		if rel.Config.ZLM != nil {
			z := rel.Config.ZLM
			secret, e := s.secret(z.SecretCredentialID, z.SecretRevision, "TOKEN")
			if e != nil {
				return e
			}
			if !deploy.Hex.MatchString(string(secret)) {
				return errors.New("ZLM_SECRET_INVALID")
			}
			req.Secrets[z.SecretCredentialID+":"+z.SecretRevision] = string(secret)
		}
		if rel.Config.InitialPasswordCredentialID != "" {
			secret, e := s.secret(rel.Config.InitialPasswordCredentialID, rel.Config.InitialPasswordRevision, "PASSWORD")
			if e != nil {
				return e
			}
			if len(secret) > 128 {
				return errors.New("INITIAL_PASSWORD_TOO_LARGE")
			}
			req.Secrets[rel.Config.InitialPasswordCredentialID+":"+rel.Config.InitialPasswordRevision] = string(secret)
		}
		for _, b := range rel.Bindings {
			if b.CredentialID != "" {
				secret, e := s.secret(b.CredentialID, b.CredentialRevision, "PASSWORD")
				if e != nil {
					return e
				}
				if len(secret) > 1024 {
					return errors.New("DEPENDENCY_PASSWORD_TOO_LARGE")
				}
				req.Secrets[b.ID] = string(secret)
			}
		}
	}
	return nil
}
func bindReleaseTx(tx *sql.Tx, r deploy.Release) error {
	if r.Config.ZLM != nil {
		if _, e := tx.Exec("INSERT INTO release_credentials VALUES(?,?,?)", r.ID, r.Config.ZLM.SecretCredentialID, r.Config.ZLM.SecretRevision); e != nil {
			return e
		}
	}
	if r.Config.InitialPasswordCredentialID != "" {
		if _, e := tx.Exec("INSERT INTO release_credentials VALUES(?,?,?)", r.ID, r.Config.InitialPasswordCredentialID, r.Config.InitialPasswordRevision); e != nil {
			return e
		}
	}
	for _, b := range r.Bindings {
		if _, e := tx.Exec("INSERT INTO release_bindings VALUES(?,?)", r.ID, b.ID); e != nil {
			return e
		}
	}
	return nil
}
