package controller

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type installationMaterial struct {
	RuntimeMode    string                    `json:"runtimeMode"`
	ID             string                    `json:"id"`
	Name           string                    `json:"name"`
	RecipeKey      string                    `json:"recipeKey"`
	RecipeVersion  string                    `json:"recipeVersion"`
	RecipeRevision string                    `json:"recipeRevision"`
	Definition     serviceinstall.Definition `json:"-"`
	SHA256         string                    `json:"sha256"`
	ByteSize       int64                     `json:"byteSize"`
	Status         string                    `json:"status"`
	ImageID        string                    `json:"imageId"`
	Platform       string                    `json:"platform"`
	ErrorCode      string                    `json:"errorCode"`
	Available      bool                      `json:"available"`
	CreatedAt      string                    `json:"createdAt"`
	UpdatedAt      string                    `json:"updatedAt"`
}

const materialColumns = "id,name,definition_json,sha256,byte_size,status,image_id,platform,error_code,created_at,updated_at,runtime_mode"

func scanInstallationMaterial(row interface{ Scan(...any) error }) (installationMaterial, error) {
	var m installationMaterial
	var raw string
	err := row.Scan(&m.ID, &m.Name, &raw, &m.SHA256, &m.ByteSize, &m.Status, &m.ImageID, &m.Platform, &m.ErrorCode, &m.CreatedAt, &m.UpdatedAt, &m.RuntimeMode)
	if err == nil {
		err = deploy.StrictJSON([]byte(raw), &m.Definition)
	}
	m.RecipeKey, m.RecipeVersion, m.RecipeRevision = m.Definition.Key, m.Definition.Version, m.Definition.Revision
	return m, err
}

func (s *Store) installationMaterialPath(id string, partial bool) string {
	name := id + ".tar"
	if partial {
		name = id + ".partial"
	}
	return filepath.Join(s.Dir, "installation-materials", name)
}

func (s *Store) readInstallationMaterial(id string) (installationMaterial, error) {
	m, err := scanInstallationMaterial(s.DB.QueryRow("SELECT "+materialColumns+" FROM installation_materials WHERE id=?", id))
	if err == nil && m.Status == "AVAILABLE" {
		s.installationMaterialAvailability(&m)
	}
	return m, err
}

func (h *HTTP) installationMaterialRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	if path == "/installation-materials/official" && r.Method == "POST" {
		h.prepareOfficialMaterial(w, r, s)
		return true
	}
	if path == "/installation-materials" {
		if r.Method == "GET" {
			h.listInstallationMaterials(w, r)
			return true
		}
		if r.Method == "POST" {
			h.createInstallationMaterial(w, r, s)
			return true
		}
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) < 2 || p[0] != "installation-materials" || !deploy.ID.MatchString(p[1]) {
		return false
	}
	if len(p) == 2 && r.Method == "DELETE" {
		h.deleteInstallationMaterial(w, r, s, p[1])
		return true
	}
	if len(p) == 3 && p[2] == "content" && r.Method == "PUT" {
		h.uploadInstallationMaterial(w, r, s, p[1])
		return true
	}
	if len(p) == 3 && p[2] == "confirm" && r.Method == "POST" {
		h.confirmInstallationMaterial(w, r, s, p[1])
		return true
	}
	return false
}

func (h *HTTP) listInstallationMaterials(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.Query("SELECT id FROM installation_materials WHERE status<>'DELETED' ORDER BY created_at DESC LIMIT 100")
	if err != nil {
		h.internal(w, r, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	out := []installationMaterial{}
	for _, id := range ids {
		m, e := h.store.readInstallationMaterial(id)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		if key := r.URL.Query().Get("recipeKey"); (key == "" || key == m.RecipeKey) && (r.URL.Query().Get("runtimeMode") == "" || r.URL.Query().Get("runtimeMode") == m.RuntimeMode) {
			out = append(out, m)
		}
	}
	h.success(w, r, out)
}

func (h *HTTP) createInstallationMaterial(w http.ResponseWriter, r *http.Request, s *session) {
	var in struct {
		RecipeKey      string `json:"recipeKey"`
		RecipeVersion  string `json:"recipeVersion"`
		RecipeRevision string `json:"recipeRevision"`
		Name           string `json:"name"`
		SHA256         string `json:"sha256"`
		ByteSize       int64  `json:"byteSize"`
		Key            string `json:"idempotencyKey"`
		RuntimeMode    string `json:"runtimeMode,omitempty"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	mode := in.RuntimeMode
	if mode == "" {
		mode = "DOCKER"
	}
	d, err := serviceinstall.Find(in.RecipeKey, in.RecipeVersion)
	if err != nil || !slices.Contains(d.RuntimeModes, mode) || d.Revision != in.RecipeRevision || !validName(in.Name) || !deploy.Hex.MatchString(in.SHA256) || in.ByteSize < 1 || in.ByteSize > serviceinstall.ArchiveLimit || len(in.Key) < 8 || len(in.Key) > 80 {
		h.fail(w, r, 400, "INSTALL_MATERIAL_INVALID", "请选择内置配方、文件名称、SHA-256及大小；单文件最大2GiB")
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
	hash := deploy.Digest(in)
	var id, old string
	err = tx.QueryRow("SELECT id,request_hash FROM installation_materials WHERE actor_id=? AND idempotency_key=?", s.AdminID, in.Key).Scan(&id, &old)
	if err == nil {
		if old != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他材料")
			return
		}
		m, e := scanInstallationMaterial(tx.QueryRow("SELECT "+materialColumns+" FROM installation_materials WHERE id=?", id))
		if e != nil {
			h.internal(w, r, e)
			return
		}
		h.store.installationMaterialAvailability(&m)
		h.success(w, r, m)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	var count int
	var total int64
	if err = tx.QueryRow("SELECT count(*),coalesce(sum(byte_size),0) FROM installation_materials WHERE status<>'DELETED'").Scan(&count, &total); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 100 || total+in.ByteSize > 16<<30 {
		h.fail(w, r, 409, "INSTALL_MATERIAL_QUOTA", "安装材料达到100个或16GiB限额，请先清理无引用材料")
		return
	}
	id = uuid.NewString()
	now := Now()
	_, err = tx.Exec("INSERT INTO installation_materials(id,actor_id,name,definition_json,sha256,byte_size,status,idempotency_key,request_hash,created_at,updated_at,runtime_mode) VALUES(?,?,?,?,?,?,'WAITING_UPLOAD',?,?,?,?,?)", id, s.AdminID, strings.TrimSpace(in.Name), jsonText(d), in.SHA256, in.ByteSize, in.Key, hash, now, now, mode)
	if err == nil {
		err = auditTx(tx, s.Username, "INSTALL_MATERIAL_CREATED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	m, err := h.store.readInstallationMaterial(id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.reply(w, r, 201, "SUCCESS", "", m)
}

func (s *Store) installationMaterialAvailability(m *installationMaterial) {
	if m.Status != "AVAILABLE" {
		return
	}
	info, err := os.Lstat(s.installationMaterialPath(m.ID, false))
	m.Available = err == nil && info.Mode().IsRegular() && info.Size() == m.ByteSize
	if !m.Available {
		m.Status, m.ErrorCode = "MISSING", "INSTALL_MATERIAL_MISSING"
	}
}

func (h *HTTP) uploadInstallationMaterial(w http.ResponseWriter, r *http.Request, s *session, id string) {
	reset, deadlineErr := installationDeadline(w, true)
	if deadlineErr != nil {
		h.fail(w, r, 500, "INSTALL_UPLOAD_TIMEOUT_UNAVAILABLE", "当前HTTP连接不支持有界大文件上传，请检查代理和控制器")
		return
	}
	defer reset()
	h.store.mu.Lock()
	m, err := h.store.readInstallationMaterial(id)
	if err == nil && m.Status != "WAITING_UPLOAD" && m.Status != "FAILED" {
		err = errors.New("UPLOAD_NOT_ALLOWED")
	}
	if err == nil {
		var count int
		err = h.store.DB.QueryRow("SELECT count(*) FROM installation_materials WHERE status IN ('UPLOADING','VERIFYING','DOWNLOADING')").Scan(&count)
		if err == nil && count >= 2 {
			err = errors.New("INSTALL_TRANSFER_BUSY")
		}
	}
	if err == nil {
		var res sql.Result
		res, err = h.store.DB.Exec("UPDATE installation_materials SET status='UPLOADING',error_code='',updated_at=? WHERE id=? AND actor_id=? AND status IN ('WAITING_UPLOAD','FAILED')", Now(), id, s.AdminID)
		if err == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				err = errors.New("UPLOAD_NOT_ALLOWED")
			}
		}
	}
	h.store.mu.Unlock()
	if err != nil {
		if err.Error() == "INSTALL_TRANSFER_BUSY" {
			h.fail(w, r, 429, "INSTALL_TRANSFER_BUSY", "已有两项材料传输或校验，请稍后重试")
			return
		}
		h.fail(w, r, 409, "INSTALL_UPLOAD_NOT_ALLOWED", "材料不存在、非本人创建或当前不能上传")
		return
	}
	dir := filepath.Dir(h.store.installationMaterialPath(id, true))
	if err = os.MkdirAll(dir, 0700); err == nil {
		err = privatePath(dir, true)
	}
	partial := h.store.installationMaterialPath(id, true)
	if err == nil {
		if e := os.Remove(partial); e != nil && !os.IsNotExist(e) {
			err = e
		}
	}
	var f *os.File
	if err == nil {
		f, err = os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	if err == nil {
		var n int64
		n, err = io.Copy(f, http.MaxBytesReader(w, r.Body, m.ByteSize+1))
		if err == nil && n != m.ByteSize {
			err = errors.New("UPLOAD_SIZE_MISMATCH")
		}
		if err == nil {
			err = f.Sync()
		}
		if e := f.Close(); err == nil {
			err = e
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if err != nil {
		_ = os.Remove(partial)
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='FAILED',error_code='UPLOAD_INTERRUPTED',updated_at=? WHERE id=? AND status='UPLOADING'", Now(), id)
		h.fail(w, r, 400, "INSTALL_UPLOAD_FAILED", "上传未完成或大小不符，请重新上传整个文件")
		return
	}
	tx, err := h.beginAuthorized(s)
	if err != nil {
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='FAILED',error_code='AUTH_REVOKED',updated_at=? WHERE id=?", Now(), id)
		_ = os.Remove(partial)
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE installation_materials SET status='UPLOADED',updated_at=? WHERE id=? AND status='UPLOADING'", Now(), id); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, map[string]string{"id": id, "status": "UPLOADED"})
}

func (h *HTTP) confirmInstallationMaterial(w http.ResponseWriter, r *http.Request, s *session, id string) {
	reset, deadlineErr := installationDeadline(w, false)
	if deadlineErr != nil {
		h.fail(w, r, 500, "INSTALL_UPLOAD_TIMEOUT_UNAVAILABLE", "当前HTTP连接不支持有界归档校验，请检查代理和控制器")
		return
	}
	defer reset()
	h.store.mu.Lock()
	m, err := h.store.readInstallationMaterial(id)
	if err == nil && m.Available {
		h.store.mu.Unlock()
		h.success(w, r, m)
		return
	}
	if err == nil {
		var count int
		err = h.store.DB.QueryRow("SELECT count(*) FROM installation_materials WHERE status IN ('UPLOADING','VERIFYING','DOWNLOADING')").Scan(&count)
		if err == nil && count >= 2 {
			err = errors.New("INSTALL_TRANSFER_BUSY")
		}
	}
	if err == nil {
		var res sql.Result
		res, err = h.store.DB.Exec("UPDATE installation_materials SET status='VERIFYING',updated_at=? WHERE id=? AND actor_id=? AND status='UPLOADED'", Now(), id, s.AdminID)
		if err == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				err = errors.New("UPLOAD_REQUIRED")
			}
		}
	}
	h.store.mu.Unlock()
	if err != nil {
		if err.Error() == "INSTALL_TRANSFER_BUSY" {
			h.fail(w, r, 429, "INSTALL_TRANSFER_BUSY", "已有两项材料传输或校验，请稍后重试")
			return
		}
		h.fail(w, r, 409, "INSTALL_UPLOAD_REQUIRED", "请先完成本人创建材料的上传")
		return
	}
	filename := h.store.installationMaterialPath(id, true)
	if _, e := os.Lstat(filename); os.IsNotExist(e) {
		filename = h.store.installationMaterialPath(id, false)
	}
	image, err := serviceinstall.ValidateMaterial(filename, m.SHA256, m.ByteSize, m.Definition, m.RuntimeMode)
	if m.RuntimeMode == "NATIVE" {
		image = ""
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if err != nil {
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='FAILED',error_code='ARCHIVE_VALIDATION_FAILED',updated_at=? WHERE id=?", Now(), id)
		h.fail(w, r, 400, "INSTALL_ARCHIVE_REJECTED", "归档摘要、运行方式、程序/镜像身份或内容不符合所选官方配方，请核对后重新上传")
		return
	}
	if r.Context().Err() != nil {
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='UPLOADED',updated_at=? WHERE id=?", Now(), id)
		return
	}
	tx, err := h.beginAuthorized(s)
	if err != nil {
		_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='UPLOADED',updated_at=? WHERE id=?", Now(), id)
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	final := h.store.installationMaterialPath(id, false)
	if filename != final {
		if err = os.Rename(filename, final); err != nil {
			tx.Rollback()
			_, _ = h.store.DB.Exec("UPDATE installation_materials SET status='UPLOADED',updated_at=? WHERE id=?", Now(), id)
			h.internal(w, r, err)
			return
		}
	}
	_, err = tx.Exec("UPDATE installation_materials SET status='AVAILABLE',image_id=?,platform=?,error_code='',updated_at=? WHERE id=? AND status='VERIFYING'", image, m.Definition.Platform, Now(), id)
	if err == nil {
		err = auditTx(tx, s.Username, "INSTALL_MATERIAL_VERIFIED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	m, err = h.store.readInstallationMaterial(id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, m)
}

func (h *HTTP) deleteInstallationMaterial(w http.ResponseWriter, r *http.Request, s *session, id string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow("SELECT status FROM installation_materials WHERE id=?", id).Scan(&status); err != nil {
		h.opsError(w, r, err)
		return
	}
	if status == "UPLOADING" || status == "VERIFYING" || status == "DOWNLOADING" {
		h.fail(w, r, 409, "INSTALL_MATERIAL_BUSY", "材料正在上传或校验")
		return
	}
	var count int
	err = tx.QueryRow(`SELECT count(*) FROM software_install_plans p WHERE p.material_id=? AND ((p.status='DRAFT' AND p.expires_at>?) OR EXISTS(SELECT 1 FROM jobs j WHERE j.software_plan_id=p.id AND (j.status IN ('QUEUED','RUNNING','UNKNOWN') OR j.target_ack_pending=1)) OR EXISTS(SELECT 1 FROM software_instances s WHERE s.install_plan_id=p.id AND s.management_mode='MANAGED'))`, id, time.Now().UTC().Format(time.RFC3339)).Scan(&count)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "INSTALL_MATERIAL_IN_USE", "安装计划、未决作业或当前安装仍引用材料")
		return
	}
	// Persist deletion before unlinking. A replay retries file cleanup, and no
	// plan can consume a DELETED record even if the filesystem unlink fails.
	_, err = tx.Exec("UPDATE installation_materials SET status='DELETED',updated_at=? WHERE id=?", Now(), id)
	if err == nil {
		err = auditTx(tx, s.Username, "INSTALL_MATERIAL_DELETED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	for _, partial := range []bool{false, true} {
		if e := os.Remove(h.store.installationMaterialPath(id, partial)); e != nil && !os.IsNotExist(e) {
			h.fail(w, r, 500, "INSTALL_MATERIAL_CLEANUP_FAILED", "材料已禁用，但文件清理失败；检查磁盘后重试删除")
			return
		}
	}
	h.success(w, r, map[string]string{"id": id})
}

func (s *Store) recoverInstallationMaterials() {
	_, _ = s.DB.Exec("UPDATE installation_materials SET status='FAILED',error_code='UPLOAD_INTERRUPTED',updated_at=? WHERE status IN ('UPLOADING','DOWNLOADING')", Now())
	_, _ = s.DB.Exec("UPDATE installation_materials SET status='UPLOADED',updated_at=? WHERE status='VERIFYING'", Now())
}

func installationDeadline(w http.ResponseWriter, read bool) (func(), error) {
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(15 * time.Minute)
	if read {
		if err := controller.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if err := controller.SetWriteDeadline(deadline); err != nil {
		if read {
			_ = controller.SetReadDeadline(time.Time{})
		}
		return nil, err
	}
	return func() {
		if read {
			_ = controller.SetReadDeadline(time.Time{})
		}
		_ = controller.SetWriteDeadline(time.Time{})
	}, nil
}
