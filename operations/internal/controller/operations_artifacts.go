package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) listArtifacts(w http.ResponseWriter, r *http.Request) {
	rows, e := h.store.DB.Query("SELECT id,name,archive_sha256,byte_size,image_id,created_at FROM artifacts ORDER BY created_at DESC LIMIT 100")
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, sha, image, created string
		var size int64
		if e = rows.Scan(&id, &name, &sha, &size, &image, &created); e != nil {
			h.internal(w, r, e)
			return
		}
		_, e := os.Stat(h.store.artifactPath(id))
		out = append(out, map[string]any{"id": id, "name": name, "archiveSha256": sha, "byteSize": size, "imageId": image, "platform": "linux/amd64", "available": e == nil, "createdAt": created})
	}
	h.success(w, r, out)
}
func (h *HTTP) acceptImport(w http.ResponseWriter, r *http.Request, s *session) {
	var in importInput
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	if !validName(in.Name) || !deploy.Hex.MatchString(in.SHA) || in.Size < 1 || in.Size > deploy.ArchiveLimit || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 80 {
		h.fail(w, r, 400, "IMPORT_INVALID", "请填写名称、预期SHA-256与大小，归档上限512MiB")
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
	raw := jsonText(in)
	var id, hash string
	e = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind='IMPORT' AND idempotency_key=?", s.AdminID, in.IdempotencyKey).Scan(&id, &hash)
	if e == nil {
		if hash != hashToken(raw) {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "同一提交标识的内容不一致")
		} else {
			h.success(w, r, map[string]string{"jobId": id})
		}
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	var count int
	_ = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count)
	if count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "未结束作业达到20个，请先处理")
		return
	}
	id = uuid.NewString()
	now := Now()
	_, e = tx.Exec("INSERT INTO jobs(id,actor_id,kind,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'IMPORT',?,?,'QUEUED','WAITING_UPLOAD',?,?,?,?)", id, s.AdminID, in.IdempotencyKey, hashToken(raw), raw, r.Context().Value(traceKey{}).(string), now, now)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	w.Header().Set("Location", api+"/jobs/"+id)
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": id})
}
func (h *HTTP) uploadImport(w http.ResponseWriter, r *http.Request, s *session, id string) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(3 * time.Minute))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Minute))
	h.store.mu.Lock()
	var raw string
	e := h.store.DB.QueryRow("SELECT input_json FROM jobs WHERE id=? AND kind='IMPORT' AND actor_id=? AND status='QUEUED' AND phase='WAITING_UPLOAD'", id, s.AdminID).Scan(&raw)
	if e == nil {
		_, e = h.store.DB.Exec("UPDATE jobs SET phase='UPLOADING',updated_at=? WHERE id=?", Now(), id)
	}
	h.store.mu.Unlock()
	if e != nil {
		h.fail(w, r, 409, "UPLOAD_NOT_ALLOWED", "作业不存在或不能重复上传")
		return
	}
	var in importInput
	_ = json.Unmarshal([]byte(raw), &in)
	dir := filepath.Join(h.store.Dir, "packages")
	e = os.MkdirAll(dir, 0700)
	if e == nil {
		e = privatePath(dir, true)
	}
	filename := h.store.uploadPath(id)
	var f *os.File
	if e == nil {
		f, e = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	if e == nil {
		var n int64
		n, e = io.Copy(f, http.MaxBytesReader(w, r.Body, in.Size+1))
		if e == nil && n != in.Size {
			e = errors.New("UPLOAD_SIZE_MISMATCH")
		}
		if e == nil {
			e = f.Sync()
		}
		f.Close()
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if e != nil {
		_ = os.Remove(filename)
		_, _ = h.store.DB.Exec("UPDATE jobs SET phase='WAITING_UPLOAD',updated_at=? WHERE id=?", Now(), id)
		h.fail(w, r, 400, "UPLOAD_FAILED", "上传未完成或大小不符，请重新上传")
		return
	}
	_, e = h.store.DB.Exec("UPDATE jobs SET phase='UPLOADED',updated_at=? WHERE id=?", Now(), id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, map[string]string{"jobId": id})
}
func (h *HTTP) completeImport(w http.ResponseWriter, r *http.Request, id string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	res, e := h.store.DB.Exec("UPDATE jobs SET phase='VERIFY',updated_at=? WHERE id=? AND kind='IMPORT' AND status='QUEUED' AND phase='UPLOADED'", Now(), id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var count int
		_ = h.store.DB.QueryRow("SELECT count(*) FROM jobs WHERE id=? AND kind='IMPORT' AND phase IN ('VERIFY','REGISTERED')", id).Scan(&count)
		if count == 0 {
			h.fail(w, r, 409, "UPLOAD_REQUIRED", "请先完成上传")
			return
		}
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": id})
}

func (h *HTTP) processImport(id, raw string) {
	var in importInput
	if deploy.StrictJSON([]byte(raw), &in) != nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	h.store.mu.Lock()
	_, e := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='VERIFY',updated_at=? WHERE id=? AND status='QUEUED'", Now(), id)
	h.store.mu.Unlock()
	if e != nil {
		return
	}
	image, e := deploy.ValidateArchive(h.store.uploadPath(id), in.SHA, in.Size)
	if e != nil {
		h.store.mu.Lock()
		_, _ = h.store.DB.Exec("UPDATE jobs SET status='FAILED',error_code=?,updated_at=? WHERE id=?", e.Error(), Now(), id)
		h.store.mu.Unlock()
		return
	}
	artifactID := uuid.NewString()
	if e = os.Rename(h.store.uploadPath(id), h.store.artifactPath(artifactID)); e != nil {
		h.jobUnknown(id, "ARTIFACT_COMMIT_FAILED")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.store.DB.Begin()
	if e != nil {
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO artifacts VALUES(?,?,?,?,?,?)", artifactID, in.Name, in.SHA, in.Size, image, Now())
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET status='SUCCEEDED',phase='REGISTERED',result_json=?,updated_at=? WHERE id=?", jsonText(map[string]string{"artifactId": artifactID, "imageId": image}), Now(), id)
	}
	if e == nil {
		e = auditTx(tx, "worker", "ARTIFACT_IMPORTED", id, "SUCCESS", "")
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.logger.Printf("jobId=%s artifactCommit=FAILED", id)
	}
}
