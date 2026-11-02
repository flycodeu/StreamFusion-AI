package controller

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type nodeRegistry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Version string `json:"version"`
	BuiltIn bool   `json:"builtIn"`
}

const nodeRegistryColumns = "id,name,url,CAST(version AS TEXT),built_in"

func scanNodeRegistry(row interface{ Scan(...any) error }) (nodeRegistry, error) {
	var r nodeRegistry
	err := row.Scan(&r.ID, &r.Name, &r.URL, &r.Version, &r.BuiltIn)
	return r, err
}
func (h *HTTP) nodeRegistryRoute(w http.ResponseWriter, r *http.Request, s *session, route string) bool {
	if route == "/node-registries" {
		switch r.Method {
		case "GET":
			h.listNodeRegistries(w, r)
			return true
		case "POST":
			h.changeNodeRegistry(w, r, s, "")
			return true
		}
	}
	p := strings.Split(strings.Trim(route, "/"), "/")
	if len(p) == 2 && p[0] == "node-registries" && deploy.ID.MatchString(p[1]) && (r.Method == "PATCH" || r.Method == "DELETE") {
		h.changeNodeRegistry(w, r, s, p[1])
		return true
	}
	return false
}
func (h *HTTP) listNodeRegistries(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.Query("SELECT " + nodeRegistryColumns + " FROM node_registries ORDER BY built_in DESC,created_at,id")
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer rows.Close()
	items := []nodeRegistry{}
	for rows.Next() {
		item, e := scanNodeRegistry(rows)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, items)
}
func (h *HTTP) changeNodeRegistry(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Version string `json:"expectedVersion"`
	}
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "请核对镜像名称和地址")
		return
	}
	if id != "" && in.Version == "" {
		h.fail(w, r, 400, "VERSION_REQUIRED", "请携带当前镜像版本")
		return
	}
	if id == "" && in.Version != "" {
		h.fail(w, r, 400, "INVALID_INPUT", "新增镜像无需版本")
		return
	}
	if r.Method != "DELETE" {
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || len([]rune(in.Name)) > 80 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
			h.fail(w, r, 400, "NODE_REGISTRY_NAME_INVALID", "镜像名称须为1至80个字符")
			return
		}
		if serviceinstall.ValidateNodeRegistryURL(in.URL) != nil {
			h.fail(w, r, 400, "NODE_REGISTRY_URL_INVALID", "请填写不含认证信息、查询参数或片段的HTTP或HTTPS仓库地址")
			return
		}
	} else if in.Name != "" || in.URL != "" {
		h.fail(w, r, 400, "INVALID_INPUT", "删除镜像只需当前版本")
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
	item := nodeRegistry{}
	if id != "" {
		item, err = scanNodeRegistry(tx.QueryRow("SELECT "+nodeRegistryColumns+" FROM node_registries WHERE id=?", id))
		if err != nil {
			h.opsError(w, r, err)
			return
		}
		if item.BuiltIn {
			h.fail(w, r, 409, "NODE_REGISTRY_BUILT_IN", "npm官方项用于恢复默认，不能修改或删除")
			return
		}
		if item.Version != in.Version {
			h.fail(w, r, 409, "VERSION_CONFLICT", "镜像列表已变化，请刷新后重试")
			return
		}
	}
	if r.Method != "DELETE" {
		var duplicate int
		if err = tx.QueryRow("SELECT count(*) FROM node_registries WHERE url=? AND id<>?", in.URL, id).Scan(&duplicate); err != nil {
			h.internal(w, r, err)
			return
		}
		if duplicate > 0 {
			h.fail(w, r, 409, "NODE_REGISTRY_DUPLICATE", "该镜像地址已在列表中")
			return
		}
	}
	action, now := "NODE_REGISTRY_UPDATED", Now()
	switch r.Method {
	case "POST":
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM node_registries").Scan(&count); err != nil {
			h.internal(w, r, err)
			return
		}
		if count >= 20 {
			h.fail(w, r, 409, "NODE_REGISTRY_LIMIT", "镜像列表最多保留20项")
			return
		}
		id = uuid.NewString()
		action = "NODE_REGISTRY_CREATED"
		_, err = tx.Exec("INSERT INTO node_registries(id,name,url,created_at,updated_at) VALUES(?,?,?,?,?)", id, in.Name, in.URL, now, now)
	case "PATCH":
		_, err = tx.Exec("UPDATE node_registries SET name=?,url=?,version=version+1,updated_at=? WHERE id=?", in.Name, in.URL, now, id)
	case "DELETE":
		action = "NODE_REGISTRY_DELETED"
		_, err = tx.Exec("DELETE FROM node_registries WHERE id=?", id)
	}
	if err == nil {
		err = auditTx(tx, s.Username, action, id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil && r.Method != "DELETE" {
		item, err = scanNodeRegistry(tx.QueryRow("SELECT "+nodeRegistryColumns+" FROM node_registries WHERE id=?", id))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if r.Method == "DELETE" {
		h.success(w, r, map[string]string{"id": id})
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	h.reply(w, r, status, "SUCCESS", "", item)
}

func checkNodeRegistrySnapshot(tx *sql.Tx, registry *nodeRegistry) error {
	if registry == nil {
		return errors.New("NODE_REGISTRY_CHANGED")
	}
	current, err := scanNodeRegistry(tx.QueryRow("SELECT "+nodeRegistryColumns+" FROM node_registries WHERE id=?", registry.ID))
	if err != nil || current != *registry {
		return errors.New("NODE_REGISTRY_CHANGED")
	}
	return nil
}

func nodeRegistryAccountKey(host, uid string) string { return "account:" + host + ":" + uid }
func nodeRegistryAccountUnlocked(tx *sql.Tx, host, uid string) error {
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", nodeRegistryAccountKey(host, uid)).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("OPERATION_LOCKED")
	}
	return nil
}

// Registry changes are privileged account operations. Recheck their accepted
// actor, host/environment identities and account lock before any target write.
// The preset row is deliberately not read here: an accepted job owns its URL.
func (h *HTTP) startNodeRegistryJob(id string, req deploy.Request) bool {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	var state, raw, planRaw string
	var generation int
	err = tx.QueryRow("SELECT j.status,j.input_json,p.snapshot_json,a.session_generation FROM jobs j JOIN node_environment_plans p ON p.id=j.node_environment_plan_id JOIN admins a ON a.id=j.actor_id WHERE j.id=?", id).Scan(&state, &raw, &planRaw, &generation)
	if err != nil || state != "QUEUED" || raw != jsonText(req) {
		return false
	}
	var p nodeEnvironmentPlan
	code := ""
	if deploy.StrictJSON([]byte(planRaw), &p) != nil || p.Action != "SET_NPM_REGISTRY" || deploy.Digest(p.Change) != deploy.Digest(req.NodeEnvironment) {
		code = "INPUT_DAMAGED"
	}
	if code == "" && generation != p.ActorGeneration {
		code = "NODE_REGISTRY_ACTOR_CHANGED"
	}
	target, e := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", req.TargetID))
	if code == "" && (e != nil || target.Maintenance || pluginTargetSnapshot(target) != p.HostSnapshot) {
		code = "HOST_CHANGED"
	}
	n, e := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", req.NodeEnvironment.Context.EnvironmentID))
	if code == "" && (e != nil || n.LastJobID != id || n.Version != req.NodeEnvironment.Context.EnvironmentVersion || n.ManagementMode != "MANAGED" || n.AccountUID != req.NodeEnvironment.Context.AccountUID || n.RootPath != req.NodeEnvironment.Context.RootPath) {
		code = "NODE_ENVIRONMENT_CHANGED"
	}
	var locks int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=? AND resource_key IN (?,?)", id, "target:"+req.TargetID, nodeRegistryAccountKey(req.TargetID, req.NodeEnvironment.Context.AccountUID)).Scan(&locks); err != nil {
		return false
	}
	if locks != 2 {
		code = "NODE_REGISTRY_LOCK_MISMATCH"
	}
	if code != "" {
		_, err = tx.Exec("UPDATE jobs SET status='FAILED',phase='NOT_STARTED',error_code=?,updated_at=? WHERE id=?", code, Now(), id)
		if err == nil {
			_, err = tx.Exec("DELETE FROM operation_locks WHERE job_id=?", id)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE node_environments SET error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=?", code, Now(), req.NodeEnvironment.Context.EnvironmentID, id)
		}
		if err == nil {
			err = auditTx(tx, "worker", "NODE_REGISTRY_NOT_STARTED", req.NodeEnvironment.Context.EnvironmentID, code, req.TraceID)
		}
		if err == nil {
			err = tx.Commit()
		}
		return false
	}
	_, err = tx.Exec("UPDATE jobs SET status='RUNNING',phase='NODE_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED'", Now(), id)
	if err != nil {
		return false
	}
	return tx.Commit() == nil
}
