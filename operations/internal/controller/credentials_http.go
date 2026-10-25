package controller

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

func (h *HTTP) credentials(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.Query("SELECT id,revision,name,kind,created_at,coalesce(revoked_at,'') FROM credential_refs ORDER BY created_at DESC LIMIT 500")
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]map[string]string, 0)
	for rows.Next() {
		var id, name, kind, created, revoked string
		var revision int
		if err = rows.Scan(&id, &revision, &name, &kind, &created, &revoked); err != nil {
			h.internal(w, r, err)
			return
		}
		items = append(items, map[string]string{"id": id, "revision": strconv.Itoa(revision), "name": name, "kind": kind, "createdAt": created, "revokedAt": revoked})
	}
	if err = rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, items)
}
func (h *HTTP) createCredential(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var input struct {
		Name             string `json:"name"`
		Kind             string `json:"kind"`
		Secret           string `json:"secret"`
		ExpectedRevision string `json:"expectedRevision"`
	}
	if err := h.decodeSensitive(w, r, s, &input); err != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 60 || len(input.Secret) < 1 || len(input.Secret) > 32<<10 || (input.Kind != "PASSWORD" && input.Kind != "TOKEN" && input.Kind != "SSH_KEY") {
		h.fail(w, r, 400, "INVALID_INPUT", "请填写有效名称、类型和凭据内容")
		return
	}
	if id != "" {
		if _, err := uuid.Parse(id); err != nil {
			h.fail(w, r, 400, "INVALID_INPUT", "凭据标识不正确")
			return
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM credential_refs").Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 500 {
		h.fail(w, r, 409, "CREDENTIAL_LIMIT", "凭据修订已达500条上限")
		return
	}
	revision := 1
	if id == "" {
		id = uuid.NewString()
	} else {
		var previous int
		var kind string
		var revoked sql.NullString
		err = tx.QueryRow("SELECT revision,kind,revoked_at FROM credential_refs WHERE id=? ORDER BY revision DESC LIMIT 1", id).Scan(&previous, &kind, &revoked)
		if errors.Is(err, sql.ErrNoRows) {
			h.fail(w, r, 404, "NOT_FOUND", "凭据不存在")
			return
		}
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if input.ExpectedRevision != strconv.Itoa(previous) || kind != input.Kind || revoked.Valid {
			h.fail(w, r, 409, "REVISION_CONFLICT", "当前修订已变化或已撤销，请刷新")
			return
		}
		revision = previous + 1
	}
	material := id + "-" + strconv.Itoa(revision) + ".enc"
	data, err := seal(h.store.Key, id, revision, []byte(input.Secret))
	if err != nil {
		h.internal(w, r, err)
		return
	}
	path := h.store.credentialFile(material)
	if err = writeExclusive(path, data); err != nil {
		h.internal(w, r, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			os.Remove(path)
		}
	}()
	if _, err = tx.Exec("INSERT INTO credential_refs(id,revision,name,kind,material,created_at) VALUES(?,?,?,?,?,?)", id, revision, input.Name, input.Kind, material, Now()); err == nil {
		err = auditTx(tx, s.Username, "CREDENTIAL_SAVED", id+":"+strconv.Itoa(revision), "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	committed = true
	w.Header().Set("Location", api+"/credentials/"+id)
	h.reply(w, r, 201, "SUCCESS", "", map[string]string{"id": id, "revision": strconv.Itoa(revision)})
}
func (h *HTTP) revokeCredential(w http.ResponseWriter, r *http.Request, s *session, id string, revision int) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var referenced int
	err = tx.QueryRow(`SELECT count(*) FROM (
 SELECT credential_id,credential_revision FROM targets
 UNION ALL
 SELECT rc.credential_id,rc.credential_revision FROM release_credentials rc
 WHERE rc.release_id IN (SELECT current_release_id FROM instances UNION SELECT previous_release_id FROM instances UNION SELECT release_id FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN') OR target_ack_pending=1)
 UNION ALL
 SELECT b.credential_id,b.credential_revision FROM release_bindings rb JOIN component_bindings b ON b.id=rb.binding_id
 WHERE rb.release_id IN (SELECT current_release_id FROM instances UNION SELECT previous_release_id FROM instances UNION SELECT release_id FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN') OR target_ack_pending=1)
 UNION ALL
 SELECT c.credential_id,c.credential_revision FROM software_install_credentials c JOIN software_install_plans p ON p.id=c.plan_id
 WHERE (p.status='DRAFT' AND p.expires_at>strftime('%Y-%m-%dT%H:%M:%SZ','now')) OR EXISTS(SELECT 1 FROM jobs j WHERE j.software_plan_id=p.id AND (j.status IN ('QUEUED','RUNNING','UNKNOWN') OR j.target_ack_pending=1)) OR EXISTS(SELECT 1 FROM software_instances i WHERE i.management_mode='MANAGED' AND (
 (json_extract(i.installation_json,'$.config.credentialId')=c.credential_id AND CAST(json_extract(i.installation_json,'$.config.credentialRevision') AS INTEGER)=c.credential_revision)
 OR (json_extract(i.installation_json,'$.config.userCredentialId')=c.credential_id AND CAST(json_extract(i.installation_json,'$.config.userCredentialRevision') AS INTEGER)=c.credential_revision)
 OR (json_extract(i.installation_json,'$.config.accessKeyCredentialId')=c.credential_id AND CAST(json_extract(i.installation_json,'$.config.accessKeyCredentialRevision') AS INTEGER)=c.credential_revision)))
 UNION ALL
 SELECT c.credential_id,c.credential_revision FROM software_configuration_credentials c JOIN software_configuration_plans p ON p.id=c.plan_id
 WHERE (p.status='DRAFT' AND p.expires_at>strftime('%Y-%m-%dT%H:%M:%SZ','now')) OR EXISTS(SELECT 1 FROM jobs j WHERE j.configuration_plan_id=p.id AND (j.status IN ('QUEUED','RUNNING','UNKNOWN') OR j.target_ack_pending=1)) OR EXISTS(SELECT 1 FROM software_instances i WHERE i.management_mode='MANAGED' AND (
 (json_extract(i.installation_json,'$.config.credentialId')=c.credential_id AND CAST(json_extract(i.installation_json,'$.config.credentialRevision') AS INTEGER)=c.credential_revision)
 OR (json_extract(i.installation_json,'$.config.userCredentialId')=c.credential_id AND CAST(json_extract(i.installation_json,'$.config.userCredentialRevision') AS INTEGER)=c.credential_revision)
 OR (json_extract(i.installation_json,'$.config.accessKeyCredentialId')=c.credential_id AND CAST(json_extract(i.installation_json,'$.config.accessKeyCredentialRevision') AS INTEGER)=c.credential_revision)))
) WHERE credential_id=? AND credential_revision=?`, id, revision).Scan(&referenced)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if referenced != 0 {
		h.fail(w, r, 409, "CREDENTIAL_IN_USE", "目标、成功版本或未决作业仍引用此修订，请先调整引用")
		return
	}
	result, err := tx.Exec("UPDATE credential_refs SET revoked_at=? WHERE id=? AND revision=? AND revoked_at IS NULL", Now(), id, revision)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		h.fail(w, r, 409, "REVISION_CONFLICT", "凭据不存在或已撤销")
		return
	}
	if err = auditTx(tx, s.Username, "CREDENTIAL_REVOKED", id+":"+strconv.Itoa(revision), "SUCCESS", r.Context().Value(traceKey{}).(string)); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, nil)
}
