package controller

import (
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) listTargets(w http.ResponseWriter, r *http.Request) {
	rows, e := h.store.DB.Query("SELECT id,name,install_root,observation_json,observed_at,kind,address,ssh_port,ssh_user,host_fingerprint,coalesce(credential_id,''),coalesce(CAST(credential_revision AS TEXT),'') FROM targets ORDER BY created_at LIMIT 50")
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer rows.Close()
	out := []target{}
	for rows.Next() {
		var t target
		var raw string
		if e = rows.Scan(&t.ID, &t.Name, &t.InstallRoot, &raw, &t.ObservedAt, &t.Kind, &t.Address, &t.Port, &t.User, &t.Fingerprint, &t.CredentialID, &t.Revision); e != nil {
			h.internal(w, r, e)
			return
		}
		t.Observation = json.RawMessage(raw)
		out = append(out, t)
	}
	h.success(w, r, out)
}
func (h *HTTP) addTarget(w http.ResponseWriter, r *http.Request, s *session) {
	var in struct {
		sshTarget
		Name        string `json:"name"`
		InstallRoot string `json:"installRoot"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	if !validName(in.Name) || !deploy.ValidRoot(in.InstallRoot) {
		h.fail(w, r, 400, "INVALID_INPUT", "请填写名称及/opt、/srv或/var/lib下的规范目录")
		return
	}
	if in.Kind == "" {
		in.Kind = "LOCAL"
	}
	if in.Kind == "LOCAL" {
		in.Address = "127.0.0.1"
		in.Port = 22
		in.User = ""
		in.Fingerprint = ""
		in.CredentialID = ""
		in.Revision = ""
	} else if in.Kind != "SSH" || net.ParseIP(in.Address) == nil || in.Port < 1 || in.Port > 65535 || !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(in.User) || !strings.HasPrefix(in.Fingerprint, "SHA256:") || !deploy.ID.MatchString(in.CredentialID) || len(in.Revision) == 0 {
		h.fail(w, r, 400, "SSH_CONFIG_INVALID", "请填写明确IP、SSH用户、经可信渠道核对的指纹及私钥修订")
		return
	}
	if in.Kind == "SSH" {
		if _, e := h.store.secret(in.CredentialID, in.Revision, "SSH_KEY"); e != nil {
			h.fail(w, r, 409, "CREDENTIAL_UNAVAILABLE", "SSH私钥修订不可用")
			return
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRow("SELECT count(*) FROM targets WHERE kind=?", in.Kind).Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count > 0 {
		if in.Kind == "LOCAL" || count >= 16 {
			h.fail(w, r, 409, "TARGET_LIMIT", "最多支持1个本机和16个SSH目标")
			return
		}
	}
	if in.Kind == "SSH" {
		var duplicate int
		if e = tx.QueryRow("SELECT count(*) FROM targets WHERE kind='SSH' AND address=? AND ssh_port=? AND install_root=?", in.Address, in.Port, in.InstallRoot).Scan(&duplicate); e != nil {
			h.internal(w, r, e)
			return
		}
		if duplicate != 0 {
			h.fail(w, r, 409, "TARGET_DUPLICATE", "该服务器与安装目录已登记")
			return
		}
	}
	id := uuid.NewString()
	if _, e = tx.Exec("INSERT INTO targets(id,name,install_root,kind,address,ssh_port,ssh_user,host_fingerprint,credential_id,credential_revision,created_at) VALUES(?,?,?,?,?,?,?,?,nullif(?,''),nullif(?,''),?)", id, strings.TrimSpace(in.Name), in.InstallRoot, in.Kind, in.Address, in.Port, in.User, in.Fingerprint, in.CredentialID, in.Revision, Now()); e == nil {
		e = auditTx(tx, s.Username, "TARGET_REGISTERED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	w.Header().Set("Location", api+"/targets/"+id)
	h.reply(w, r, 201, "SUCCESS", "", map[string]string{"id": id, "installRoot": in.InstallRoot})
}
