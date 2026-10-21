package controller

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type observationBatchItem struct {
	BatchID    string `json:"batchId"`
	ObjectID   string `json:"objectId"`
	HostID     string `json:"hostId"`
	Kind       string `json:"kind"`
	ConfigHash string `json:"-"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	ErrorCode  string `json:"errorCode"`
	ObservedAt string `json:"observedAt"`
}
type observationBatch struct {
	ID        string                 `json:"id"`
	Status    string                 `json:"status"`
	TraceID   string                 `json:"traceId"`
	CreatedAt string                 `json:"createdAt"`
	Items     []observationBatchItem `json:"items"`
}
type observationBatchInput struct {
	Kind     string `json:"kind"`
	HostID   string `json:"hostId"`
	ObjectID string `json:"objectId"`
	Query    string `json:"query"`
	Mode     string `json:"mode"`
	Key      string `json:"idempotencyKey"`
}

func (h *HTTP) observationRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	if path == "/host-observations" && r.Method == "GET" {
		h.listHostObservations(w, r)
		return true
	}
	if path == "/observation-batches" {
		if r.Method == "POST" {
			h.createObservationBatch(w, r, s)
			return true
		}
		if r.Method == "GET" {
			var id string
			e := h.store.DB.QueryRow("SELECT id FROM observation_batches WHERE actor_id=? ORDER BY created_at DESC LIMIT 1", s.AdminID).Scan(&id)
			if errors.Is(e, sql.ErrNoRows) {
				h.success(w, r, nil)
			} else if e != nil {
				h.internal(w, r, e)
			} else {
				h.replyObservationBatch(w, r, s, id)
			}
			return true
		}
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) == 2 && p[0] == "observation-batches" && deploy.ID.MatchString(p[1]) && r.Method == "GET" {
		h.replyObservationBatch(w, r, s, p[1])
		return true
	}
	if len(p) == 3 && p[0] == "observation-batches" && deploy.ID.MatchString(p[1]) && p[2] == "cancel" && r.Method == "POST" {
		h.cancelObservationBatch(w, r, s, p[1])
		return true
	}
	if len(p) == 3 && p[0] == "hosts" && deploy.ID.MatchString(p[1]) {
		if p[2] == "observation" && r.Method == "GET" {
			h.readHostMetrics(w, r, p[1])
			return true
		}
		if p[2] == "monitoring" && r.Method == "PATCH" {
			h.setMonitoring(w, r, s, p[1])
			return true
		}
	}
	return false
}
func (h *HTTP) listHostObservations(w http.ResponseWriter, r *http.Request) {
	plans, e := h.observationPlans()
	if e != nil {
		h.internal(w, r, e)
		return
	}
	out := []hostObservation{}
	for _, p := range plans {
		if id := r.URL.Query().Get("hostId"); id != "" && id != p.Host.ID {
			continue
		}
		o, e := h.store.readHostObservation(p.Host)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		out = append(out, o)
	}
	h.success(w, r, out)
}
func (h *HTTP) setMonitoring(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Enabled         bool   `json:"enabled"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if decode(w, r, &in) != nil || in.ExpectedVersion == "" {
		h.fail(w, r, 400, "INVALID_INPUT", "请携带主机版本和监测设置")
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
	var n int
	e = tx.QueryRow("SELECT count(*) FROM targets WHERE id=? AND CAST(version AS TEXT)=?", id, in.ExpectedVersion).Scan(&n)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if n != 1 {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机已变化，请刷新")
		return
	}
	_, e = tx.Exec("INSERT INTO observation_plans(host_id,enabled) VALUES(?,?) ON CONFLICT(host_id) DO UPDATE SET enabled=excluded.enabled,resource_due=0,software_due=0,auth_paused=0", id, in.Enabled)
	if e == nil {
		e = auditTx(tx, s.Username, "MONITORING_CHANGED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, map[string]bool{"enabled": in.Enabled})
}
func (h *HTTP) createObservationBatch(w http.ResponseWriter, r *http.Request, s *session) {
	var in observationBatchInput
	if decode(w, r, &in) != nil || len(in.Key) < 8 || len(in.Key) > 80 || len(in.Query) > 120 || (in.Kind != "HOST" && in.Kind != "SOFTWARE") || (in.HostID != "" && !deploy.ID.MatchString(in.HostID)) || (in.ObjectID != "" && !deploy.ID.MatchString(in.ObjectID)) {
		h.fail(w, r, 400, "INVALID_INPUT", "检测范围或提交标识无效")
		return
	}
	if in.Mode == "" {
		in.Mode = "ACTIVE"
	}
	if !strings.Contains("|ACTIVE|ALL|OBSERVE|MANAGED|RELEASED|", "|"+in.Mode+"|") {
		h.fail(w, r, 400, "INVALID_INPUT", "管理模式无效")
		return
	}
	hash := deploy.Digest(struct{ Kind, Host, Object, Query, Mode string }{in.Kind, in.HostID, in.ObjectID, in.Query, in.Mode})
	h.store.mu.Lock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.store.mu.Unlock()
		h.internal(w, r, e)
		return
	}
	defer func() { tx.Rollback(); h.store.mu.Unlock() }()
	var id, oldHash string
	e = tx.QueryRow("SELECT batch_id,request_hash FROM observation_batch_requests WHERE actor_id=? AND idempotency_key=?", s.AdminID, in.Key).Scan(&id, &oldHash)
	if e == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他检测范围")
		} else {
			h.success(w, r, map[string]string{"id": id})
		}
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	e = nil
	// Prune completed batches before counting their cascading idempotency receipts.
	// Unfinished work and the receipt being replayed above are never evicted.
	_, e = tx.Exec("DELETE FROM observation_batches WHERE status NOT IN ('QUEUED','RUNNING') AND (created_at<? OR id NOT IN (SELECT id FROM observation_batches ORDER BY created_at DESC LIMIT 999))", time.Now().UTC().Add(-7*24*time.Hour).Format(time.RFC3339Nano))
	if e != nil {
		h.internal(w, r, e)
		return
	}
	var requests int
	if e = tx.QueryRow("SELECT count(*) FROM observation_batch_requests").Scan(&requests); e != nil {
		h.internal(w, r, e)
		return
	}
	if requests >= 100000 {
		h.fail(w, r, 429, "OBSERVATION_QUEUE_FULL", "检测请求记录已达上限，请稍后再试")
		return
	}
	var items []observationBatchItem
	if in.Kind == "HOST" {
		rows, err := tx.Query("SELECT "+hostColumns+" FROM targets WHERE (?='' OR id=?) ORDER BY id", in.HostID, in.HostID)
		if err != nil {
			h.internal(w, r, err)
			return
		}
		for rows.Next() {
			t, err := scanHost(rows)
			if err != nil {
				e = err
				break
			}
			items = append(items, observationBatchItem{ObjectID: t.ID, HostID: t.ID, Kind: "HOST", ConfigHash: observationIdentity(t), Name: t.Name})
		}
		if e = rowsError(rows, e); e != nil {
			h.internal(w, r, e)
			return
		}
	} else {
		rows, err := tx.Query("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE (?='' OR s.host_id=?) AND (?='' OR s.id=?) AND s.management_mode<>'RELEASED' AND (? IN ('ALL','ACTIVE') OR s.management_mode=?) AND instr(lower(s.name||' '||CASE s.software_type WHEN 'OTHER' THEN '其他服务' WHEN 'ZLM' THEN 'ZLMediaKit' ELSE s.software_type END||' '||s.locator),lower(?))>0 ORDER BY s.id LIMIT 1001", in.HostID, in.HostID, in.ObjectID, in.ObjectID, in.Mode, in.Mode, strings.TrimSpace(in.Query))
		if err != nil {
			h.internal(w, r, err)
			return
		}
		var all []softwareInstance
		for rows.Next() {
			i, err := scanSoftware(rows)
			if err != nil {
				e = err
				break
			}
			all = append(all, i)
		}
		if e = rowsError(rows, e); e != nil {
			h.internal(w, r, e)
			return
		}
		for _, i := range all {
			t, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", i.HostID))
			if err != nil {
				h.internal(w, r, err)
				return
			}
			items = append(items, observationBatchItem{ObjectID: i.ID, HostID: i.HostID, Kind: "SOFTWARE", ConfigHash: softwareObservationIdentity(t, i), Name: i.Name})
		}
	}
	if len(items) == 0 || len(items) > 1000 {
		h.fail(w, r, 400, "OBSERVATION_RANGE_INVALID", "请选择1–1000个检测对象")
		return
	}
	scopeParts := []string{}
	for _, item := range items {
		scopeParts = append(scopeParts, item.Kind, item.ObjectID, item.ConfigHash)
	}
	scope := deploy.Digest(scopeParts)
	e = tx.QueryRow("SELECT id FROM observation_batches WHERE actor_id=? AND actor_generation=? AND scope_hash=? AND status IN ('QUEUED','RUNNING')", s.AdminID, s.Generation, scope).Scan(&id)
	if e == nil {
		_, e = tx.Exec("INSERT INTO observation_batch_requests VALUES(?,?,?,?)", s.AdminID, in.Key, hash, id)
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			h.internal(w, r, e)
			return
		}
		h.success(w, r, map[string]string{"id": id})
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	// Bound retained work after pruning completed batches above.
	e = nil
	var pending, retained int
	if e == nil {
		e = tx.QueryRow("SELECT count(*) FROM observation_batches WHERE status IN ('QUEUED','RUNNING')").Scan(&pending)
	}
	if e == nil {
		e = tx.QueryRow("SELECT count(*) FROM observation_batch_items").Scan(&retained)
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if pending >= 100 || retained+len(items) > 100000 {
		h.fail(w, r, 429, "OBSERVATION_QUEUE_FULL", "检测记录已达容量上限，请稍后再试")
		return
	}
	id = uuid.NewString()
	trace := r.Context().Value(traceKey{}).(string)
	_, e = tx.Exec("INSERT INTO observation_batches(id,actor_id,actor_generation,idempotency_key,request_hash,scope_hash,trace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", id, s.AdminID, s.Generation, in.Key, hash, scope, trace, Now(), Now())
	if e == nil {
		_, e = tx.Exec("INSERT INTO observation_batch_requests VALUES(?,?,?,?)", s.AdminID, in.Key, hash, id)
	}
	for _, item := range items {
		if e != nil {
			break
		}
		_, e = tx.Exec("INSERT INTO observation_batch_items(batch_id,object_id,host_id,kind,config_hash,name) VALUES(?,?,?,?,?,?)", id, item.ObjectID, item.HostID, item.Kind, item.ConfigHash, item.Name)
	}
	if e == nil {
		e = auditTx(tx, s.Username, "OBSERVATION_BATCH_ACCEPTED", id, "SUCCESS", trace)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"id": id})
}
func rowsError(rows *sql.Rows, previous error) error {
	if previous == nil {
		previous = rows.Err()
	}
	rows.Close()
	return previous
}
func (h *HTTP) replyObservationBatch(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var b observationBatch
	e := h.store.DB.QueryRow("SELECT id,status,trace_id,created_at FROM observation_batches WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&b.ID, &b.Status, &b.TraceID, &b.CreatedAt)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	rows, e := h.store.DB.Query("SELECT batch_id,object_id,host_id,kind,config_hash,name,status,error_code,observed_at FROM observation_batch_items WHERE batch_id=? ORDER BY host_id,name", id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	b.Items = []observationBatchItem{}
	for rows.Next() {
		item, e := scanObservationItem(rows)
		if e != nil {
			rows.Close()
			h.internal(w, r, e)
			return
		}
		b.Items = append(b.Items, item)
	}
	if e = rowsError(rows, nil); e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, b)
}
func scanObservationItem(row interface{ Scan(...any) error }) (observationBatchItem, error) {
	var i observationBatchItem
	e := row.Scan(&i.BatchID, &i.ObjectID, &i.HostID, &i.Kind, &i.ConfigHash, &i.Name, &i.Status, &i.ErrorCode, &i.ObservedAt)
	return i, e
}
func (h *HTTP) pendingObservationItems(host string) ([]observationBatchItem, error) {
	rows, e := h.store.DB.Query("SELECT i.batch_id,i.object_id,i.host_id,i.kind,i.config_hash,i.name,i.status,i.error_code,i.observed_at FROM observation_batch_items i JOIN observation_batches b ON b.id=i.batch_id WHERE i.host_id=? AND i.status='QUEUED' ORDER BY b.created_at,i.object_id LIMIT 100", host)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []observationBatchItem{}
	for rows.Next() {
		i, e := scanObservationItem(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
func (h *HTTP) observationActorValid(batch string) bool {
	var n int
	e := h.store.DB.QueryRow("SELECT count(*) FROM observation_batches b JOIN admins a ON a.id=b.actor_id WHERE b.id=? AND b.actor_generation=a.session_generation", batch).Scan(&n)
	return e == nil && n == 1
}
func (h *HTTP) startObservationItem(i observationBatchItem) bool {
	if !h.observationActorValid(i.BatchID) {
		h.completeObservationItem(i, "SKIPPED", "AUTH_REVOKED")
		return false
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	res, e := h.store.DB.Exec("UPDATE observation_batch_items SET status='RUNNING',error_code='' WHERE batch_id=? AND object_id=? AND status='QUEUED'", i.BatchID, i.ObjectID)
	if e != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}
func (h *HTTP) completeObservationItem(i observationBatchItem, status, code string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	_, _ = h.store.DB.Exec("UPDATE observation_batch_items SET status=?,error_code=?,observed_at=? WHERE batch_id=? AND object_id=? AND status IN ('QUEUED','RUNNING')", status, code, Now(), i.BatchID, i.ObjectID)
}
func (h *HTTP) finishObservationBatches() {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	// A host deleted after submission has no scheduler slot; finish it explicitly.
	_, _ = h.store.DB.Exec("UPDATE observation_batch_items SET status='SKIPPED',error_code='HOST_REMOVED',observed_at=? WHERE status='QUEUED' AND NOT EXISTS(SELECT 1 FROM targets t WHERE t.id=host_id)", Now())
	_, _ = h.store.DB.Exec(`UPDATE observation_batches SET status=CASE
WHEN EXISTS(SELECT 1 FROM observation_batch_items i WHERE i.batch_id=observation_batches.id AND i.status IN ('QUEUED','RUNNING')) THEN 'RUNNING'
WHEN EXISTS(SELECT 1 FROM observation_batch_items i WHERE i.batch_id=observation_batches.id AND i.status IN ('FAILED','SKIPPED')) THEN 'PARTIAL'
WHEN EXISTS(SELECT 1 FROM observation_batch_items i WHERE i.batch_id=observation_batches.id AND i.status='CANCELLED') THEN 'CANCELLED' ELSE 'SUCCEEDED' END,updated_at=? WHERE status IN ('QUEUED','RUNNING')`, Now())
}
func (h *HTTP) cancelObservationBatch(w http.ResponseWriter, r *http.Request, s *session, id string) {
	h.store.mu.Lock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.store.mu.Unlock()
		h.internal(w, r, e)
		return
	}
	var n int
	e = tx.QueryRow("SELECT count(*) FROM observation_batches WHERE id=? AND actor_id=?", id, s.AdminID).Scan(&n)
	if e == nil && n == 1 {
		_, e = tx.Exec("UPDATE observation_batch_items SET status='CANCELLED',error_code='CANCELLED',observed_at=? WHERE batch_id=? AND status='QUEUED'", Now(), id)
		if e == nil {
			e = auditTx(tx, s.Username, "OBSERVATION_BATCH_CANCELLED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
		}
		if e == nil {
			e = tx.Commit()
		}
	}
	tx.Rollback()
	h.store.mu.Unlock()
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if n != 1 {
		h.fail(w, r, 404, "NOT_FOUND", "检测批次不存在")
		return
	}
	h.finishObservationBatches()
	h.replyObservationBatch(w, r, s, id)
}
