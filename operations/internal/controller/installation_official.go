package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Prepare immutable vendor material on the controller. The target receives the
// same verified bytes as an offline upload and needs no external network.
func (h *HTTP) prepareOfficialMaterial(w http.ResponseWriter, r *http.Request, s *session) {
	var in struct {
		RecipeKey      string `json:"recipeKey"`
		RecipeVersion  string `json:"recipeVersion"`
		RecipeRevision string `json:"recipeRevision"`
		Key            string `json:"idempotencyKey"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	d, err := serviceinstall.Find(in.RecipeKey, in.RecipeVersion)
	if err != nil || d.Revision != in.RecipeRevision || d.Native == nil || len(in.Key) < 8 || len(in.Key) > 80 {
		h.fail(w, r, 400, "NATIVE_RECIPE_INVALID", "请选择有固定官方原生材料的当前配方")
		return
	}
	reset, err := installationDeadline(w, false)
	if err != nil {
		h.fail(w, r, 500, "INSTALL_DOWNLOAD_TIMEOUT_UNAVAILABLE", "连接不支持有界材料下载，请检查代理与控制器")
		return
	}
	defer reset()
	hash := serviceinstall.Digest(struct {
		Purpose    string
		Input      any
		Definition serviceinstall.Definition
	}{"OFFICIAL_NATIVE", in, d})
	estimated := int64(1 << 20)
	for _, part := range d.Native.Parts {
		if part.ByteSize > 0 {
			estimated += part.ByteSize
		} else {
			estimated += 64 << 20
		}
	}
	if estimated > serviceinstall.ArchiveLimit {
		h.fail(w, r, 409, "INSTALL_MATERIAL_QUOTA", "官方组合材料超过当前单包上限")
		return
	}
	h.store.mu.Lock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.store.mu.Unlock()
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	id, oldHash := "", ""
	err = tx.QueryRow("SELECT id,request_hash FROM installation_materials WHERE actor_id=? AND idempotency_key=?", s.AdminID, in.Key).Scan(&id, &oldHash)
	if err == nil {
		if oldHash != hash {
			h.store.mu.Unlock()
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他材料")
			return
		}
		m, e := scanInstallationMaterial(tx.QueryRow("SELECT "+materialColumns+" FROM installation_materials WHERE id=?", id))
		if e != nil {
			h.store.mu.Unlock()
			h.internal(w, r, e)
			return
		}
		h.store.installationMaterialAvailability(&m)
		h.store.installationMaterialOrigin(&m)
		if m.Status == "AVAILABLE" || m.Status == "DOWNLOADING" {
			h.store.mu.Unlock()
			h.success(w, r, m)
			return
		}
		if m.Status != "FAILED" {
			h.store.mu.Unlock()
			h.fail(w, r, 409, "INSTALL_MATERIAL_REIMPORT_REQUIRED", "材料已失效或缺失，请用新的材料记录重新准备")
			return
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.store.mu.Unlock()
		h.internal(w, r, err)
		return
	}
	var active, count int
	var total int64
	if err = tx.QueryRow("SELECT count(*),coalesce(sum(byte_size),0),coalesce(sum(CASE WHEN status IN ('UPLOADING','VERIFYING','DOWNLOADING') THEN 1 ELSE 0 END),0) FROM installation_materials WHERE status<>'DELETED'").Scan(&count, &total, &active); err != nil {
		h.store.mu.Unlock()
		h.internal(w, r, err)
		return
	}
	if active >= 2 {
		h.store.mu.Unlock()
		h.fail(w, r, 429, "INSTALL_TRANSFER_BUSY", "已有两项材料传输或校验，请稍后重试")
		return
	}
	extra, countExtra := estimated, 1
	if id != "" {
		extra, countExtra = 0, 0
	}
	if count+countExtra > 100 || total+extra > 16<<30 {
		h.store.mu.Unlock()
		h.fail(w, r, 409, "INSTALL_MATERIAL_QUOTA", "安装材料达到容量或数量上限")
		return
	}
	if id == "" {
		id = uuid.NewString()
		_, err = tx.Exec("INSERT INTO installation_materials(id,actor_id,name,definition_json,sha256,byte_size,status,idempotency_key,request_hash,created_at,updated_at,runtime_mode) VALUES(?,?,?,?,'',?,'DOWNLOADING',?,?,?,?, 'NATIVE')", id, s.AdminID, d.Name+" "+d.Version+" 官方原生材料", jsonText(d), estimated, in.Key, hash, Now(), Now())
	} else {
		_, err = tx.Exec("UPDATE installation_materials SET status='DOWNLOADING',error_code='',updated_at=? WHERE id=? AND status='FAILED'", Now(), id)
	}
	if err == nil {
		err = tx.Commit()
	}
	h.store.mu.Unlock()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	partial := h.store.installationMaterialPath(id, true)
	final := h.store.installationMaterialPath(id, false)
	downloadCtx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	var size int64
	var digest string
	reused := false
	if _, e := os.Lstat(final); e == nil {
		// A filesystem publish can outlive a lost DB commit. Recheck the fixed
		// vendor bytes instead of downloading again or overwriting the file.
		digest, size, err = inspectOfficialMaterial(final, d)
		reused = err == nil
	} else if !os.IsNotExist(e) {
		err = e
	}
	if err == nil && !reused {
		if err = os.MkdirAll(filepath.Dir(partial), 0700); err == nil {
			err = privatePath(filepath.Dir(partial), true)
		}
		if err == nil {
			if e := os.Remove(partial); e != nil && !os.IsNotExist(e) {
				err = e
			}
		}
		if err == nil {
			err = serviceinstall.PrepareNativeMaterial(downloadCtx, d, partial)
		}
		if err == nil {
			digest, size, err = inspectOfficialMaterial(partial, d)
		}
	}
	if err == nil && size > estimated {
		err = errors.New("NATIVE_MATERIAL_RESERVATION_EXCEEDED")
	}
	if err == nil {
		err = downloadCtx.Err()
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if err != nil {
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='FAILED',error_code='OFFICIAL_DOWNLOAD_FAILED',updated_at=? WHERE id=? AND status='DOWNLOADING'", Now(), id)
		_ = os.Remove(partial)
		h.fail(w, r, 409, "OFFICIAL_DOWNLOAD_FAILED", "官方材料未完整下载或摘要不匹配；检查Ops外网连接后使用原提交重试，或离线上传")
		return
	}
	commit, err := h.beginAuthorized(s)
	if err != nil {
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='FAILED',error_code='AUTH_REVOKED',updated_at=? WHERE id=?", Now(), id)
		_ = os.Remove(partial)
		h.internal(w, r, err)
		return
	}
	defer commit.Rollback()
	if err = commit.QueryRow("SELECT coalesce(sum(byte_size),0) FROM installation_materials WHERE status<>'DELETED' AND id<>?", id).Scan(&total); err == nil && total+size > 16<<30 {
		err = errors.New("INSTALL_MATERIAL_QUOTA")
	}
	if err == nil && !reused {
		err = os.Rename(partial, final)
	}
	if err == nil {
		var result sql.Result
		result, err = commit.Exec("UPDATE installation_materials SET sha256=?,byte_size=?,status='AVAILABLE',platform=?,error_code='',updated_at=? WHERE id=? AND status='DOWNLOADING'", digest, size, d.Platform, Now(), id)
		if err == nil {
			n, e := result.RowsAffected()
			if e != nil || n != 1 {
				err = errors.New("INSTALL_MATERIAL_STATE_CONFLICT")
			}
		}
	}
	if err == nil {
		err = h.store.writeMaterialOrigin(id, materialOrigin{Kind: "OFFICIAL_DOWNLOAD", ImportedAt: Now(), FileName: d.Name + " " + d.Version})
	}
	if err == nil {
		err = auditTx(commit, s.Username, "OFFICIAL_MATERIAL_PREPARED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = commit.Commit()
	}
	if err != nil {
		_ = commit.Rollback()
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='FAILED',error_code='OFFICIAL_PUBLISH_FAILED',updated_at=? WHERE id=? AND status='DOWNLOADING'", Now(), id)
		h.internal(w, r, err)
		return
	}
	m, err := h.store.readInstallationMaterial(id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, m)
}

// A published material must remain a regular vendor-verified file. This also
// validates a recovered file after a controller crash between rename and commit.
func inspectOfficialMaterial(file string, d serviceinstall.Definition) (string, int64, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > serviceinstall.ArchiveLimit {
		return "", 0, errors.New("NATIVE_MATERIAL_SIZE_LIMIT")
	}
	digest, err := fileDigest(file)
	if err == nil {
		_, err = serviceinstall.ValidateMaterial(file, digest, info.Size(), d, "NATIVE")
	}
	return digest, info.Size(), err
}
