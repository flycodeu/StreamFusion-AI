package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) listInstances(w http.ResponseWriter, r *http.Request) {
	rows, e := h.store.DB.Query("SELECT id FROM instances ORDER BY created_at DESC LIMIT 100")
	if e != nil {
		h.internal(w, r, e)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e != nil {
		h.internal(w, r, e)
		return
	}
	out := []instance{}
	for _, id := range ids {
		i, e := h.store.readInstance(id)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		out = append(out, i)
	}
	h.success(w, r, out)
}
func (h *HTTP) saveInstance(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Name     string        `json:"name"`
		TargetID string        `json:"targetId"`
		Version  string        `json:"version"`
		Config   deploy.Config `json:"config"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	if !validName(in.Name) || !deploy.ID.MatchString(in.TargetID) || in.Config.Validate() != nil {
		h.fail(w, r, 400, "CONFIG_INVALID", "请核对名称、目标、端口、配置和数据兼容标识")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	if id == "" {
		id = uuid.NewString()
		_, e = tx.Exec("INSERT INTO instances(id,name,target_id,draft_json,created_at) VALUES(?,?,?,?,?)", id, in.Name, in.TargetID, jsonText(in.Config), Now())
	} else {
		var result sql.Result
		result, e = tx.Exec("UPDATE instances SET name=?,draft_json=?,version=version+1,draft_revision=draft_revision+1 WHERE id=? AND CAST(version AS TEXT)=? AND target_id=?", in.Name, jsonText(in.Config), id, in.Version, in.TargetID)
		if e == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				h.fail(w, r, 409, "VERSION_CONFLICT", "实例已变更或目标不符，请刷新")
				return
			}
		}
	}
	if e == nil {
		e = auditTx(tx, s.Username, "INSTANCE_DRAFT_SAVED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, map[string]string{"id": id})
}

func (h *HTTP) listReleases(w http.ResponseWriter, r *http.Request, id string) {
	rows, e := h.store.DB.Query("SELECT snapshot_json,created_at FROM releases WHERE instance_id=? ORDER BY created_at DESC LIMIT 100", id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw, created string
		if e = rows.Scan(&raw, &created); e != nil {
			h.internal(w, r, e)
			return
		}
		var v map[string]any
		_ = json.Unmarshal([]byte(raw), &v)
		v["createdAt"] = created
		out = append(out, v)
	}
	h.success(w, r, out)
}
func (h *HTTP) observeInstance(w http.ResponseWriter, r *http.Request, id string) {
	i, e := h.store.readInstance(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if i.Current == "" {
		h.fail(w, r, 409, "NOT_DEPLOYED", "实例尚未成功发布")
		return
	}
	rel, e := h.store.readRelease(i.Current)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	t, e := h.store.readTarget(i.TargetID)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var actual deploy.Result
	e = h.store.callTarget(ctx, t, deploy.Request{Action: "INSPECT", Candidate: &rel}, "", &actual)
	if e != nil {
		actual.Health = "UNKNOWN"
		actual.ErrorCode = "OBSERVATION_FAILED"
		actual.ObservedAt = Now()
	}
	_, dbErr := h.store.DB.Exec("INSERT INTO observations VALUES(?,?,?) ON CONFLICT(instance_id) DO UPDATE SET payload_json=excluded.payload_json,observed_at=excluded.observed_at", id, jsonText(actual), Now())
	if dbErr != nil {
		h.internal(w, r, dbErr)
		return
	}
	h.success(w, r, actual)
}
func (h *HTTP) instanceLogs(w http.ResponseWriter, r *http.Request, id string) {
	i, e := h.store.readInstance(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if i.Current == "" {
		h.fail(w, r, 409, "NOT_DEPLOYED", "尚无已发布实例")
		return
	}
	rel, e := h.store.readRelease(i.Current)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	t, e := h.store.readTarget(i.TargetID)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out map[string]any
	if e = h.store.callTarget(ctx, t, deploy.Request{Action: "LOGS", Candidate: &rel}, "", &out); e != nil {
		h.fail(w, r, 409, "LOG_UNAVAILABLE", "实例日志不可读取，请核对实际状态")
		return
	}
	h.success(w, r, out)
}

// One bounded worker consumes persistent jobs; the queue is never an in-memory
// source of truth. Interrupted mutation calls are reconciled, never re-activated.
