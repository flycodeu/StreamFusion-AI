package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

const defaultHostRoot = "/opt/streamfusion-ops-target"
const hostColumns = "id,name,install_root,observation_json,observed_at,kind,address,ssh_port,ssh_user,host_fingerprint,coalesce(credential_id,''),coalesce(CAST(credential_revision AS TEXT),''),CAST(version AS TEXT),maintenance,capabilities_json"

type hostInput struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Address          string `json:"address"`
	Port             int    `json:"sshPort"`
	User             string `json:"sshUser"`
	CredentialID     string `json:"credentialId"`
	FingerprintToken string `json:"fingerprintToken"`
	InstallRoot      string `json:"installRoot"`
	ExpectedVersion  string `json:"expectedVersion"`
}

type fingerprintInput struct {
	Address      string `json:"address"`
	Port         int    `json:"sshPort"`
	User         string `json:"sshUser"`
	CredentialID string `json:"credentialId"`
}

func scanHost(row interface{ Scan(...any) error }) (target, error) {
	var t target
	var raw, capabilities string
	err := row.Scan(&t.ID, &t.Name, &t.InstallRoot, &raw, &t.ObservedAt, &t.Kind, &t.Address, &t.Port, &t.User, &t.Fingerprint, &t.CredentialID, &t.Revision, &t.Version, &t.Maintenance, &capabilities)
	t.Observation = json.RawMessage(raw)
	t.Capabilities = unknownHostCapabilities()
	if err == nil {
		err = json.Unmarshal([]byte(capabilities), &t.Capabilities)
	}
	return t, err
}

func (h *HTTP) hostsRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	switch r.Method + " " + path {
	case "GET /hosts":
		h.listHosts(w, r)
		return true
	case "POST /hosts":
		h.saveHost(w, r, s, "")
		return true
	case "POST /hosts/fingerprint":
		h.discoverHostFingerprint(w, r, s)
		return true
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) < 2 || p[0] != "hosts" || !deploy.ID.MatchString(p[1]) {
		return false
	}
	if len(p) == 2 {
		switch r.Method {
		case "GET":
			t, e := h.store.readTarget(p[1])
			if e != nil {
				h.opsError(w, r, e)
			} else {
				h.success(w, r, t)
			}
			return true
		case "PATCH":
			h.saveHost(w, r, s, p[1])
			return true
		case "DELETE":
			h.deleteHost(w, r, s, p[1])
			return true
		}
	}
	if len(p) == 3 {
		if p[2] == "preparation" && r.Method == "GET" {
			h.hostPreparation(w, r, p[1])
			return true
		}
		if p[2] == "metrics" && r.Method == "GET" {
			h.readHostMetrics(w, r, p[1])
			return true
		}
		if p[2] == "software-discovery" && r.Method == "POST" {
			h.discoverHostSoftware(w, r, s, p[1])
			return true
		}
		if p[2] == "checks" && r.Method == "POST" {
			h.checkHost(w, r, s, p[1])
			return true
		}
		if p[2] == "maintenance" && r.Method == "PATCH" {
			h.setHostMaintenance(w, r, s, p[1])
			return true
		}
	}
	if len(p) == 4 && p[2] == "preparation" && p[3] == "download" && r.Method == "GET" {
		h.downloadHostPreparation(w, r, p[1])
		return true
	}
	return false
}

func (h *HTTP) listHosts(w http.ResponseWriter, r *http.Request) {
	rows, e := h.store.DB.Query("SELECT " + hostColumns + " FROM targets ORDER BY created_at LIMIT 50")
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer rows.Close()
	out := []target{}
	for rows.Next() {
		t, e := scanHost(rows)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		out = append(out, t)
	}
	if e = rows.Err(); e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, out)
}

var sshUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
var hostnamePattern = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?))*$`)

func normalizeHostAddress(address string) (string, bool) {
	address = strings.TrimSpace(address)
	if ip := net.ParseIP(address); ip != nil {
		return ip.String(), !ip.IsUnspecified() && !ip.IsMulticast()
	}
	address = strings.ToLower(strings.TrimSuffix(address, "."))
	return address, len(address) > 0 && len(address) <= 253 && hostnamePattern.MatchString(address)
}

func normalizeSSHInput(in *fingerprintInput) bool {
	var valid bool
	in.Address, valid = normalizeHostAddress(in.Address)
	return valid && in.Port >= 1 && in.Port <= 65535 && sshUserPattern.MatchString(in.User) && deploy.ID.MatchString(in.CredentialID)
}

// Revision selection is server-owned. Existing hosts keep their pinned revision
// until the user saves their connection again; historical releases are untouched.
func latestHostCredential(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (string, error) {
	var revision string
	e := q.QueryRow("SELECT CAST(revision AS TEXT) FROM credential_refs WHERE id=? AND kind IN ('SSH_KEY','PASSWORD') AND revoked_at IS NULL ORDER BY revision DESC LIMIT 1", id).Scan(&revision)
	return revision, e
}

func (h *HTTP) discoverHostFingerprint(w http.ResponseWriter, r *http.Request, s *session) {
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	var in fingerprintInput
	if e := decode(w, r, &in); e != nil || !normalizeSSHInput(&in) {
		h.fail(w, r, 400, "SSH_CONFIG_INVALID", "请填写有效地址、SSH端口、用户并选择连接凭据")
		return
	}
	revision, e := latestHostCredential(h.store.DB, in.CredentialID)
	if e != nil {
		h.fail(w, r, 409, "CREDENTIAL_UNAVAILABLE", "连接凭据不可用，请重新选择")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	fingerprint, e := discoverSSHKey(ctx, in.Address, in.Port)
	if e != nil {
		h.fail(w, r, 409, "SSH_FINGERPRINT_UNAVAILABLE", "无法获取SSH指纹，请检查地址、端口和SSH服务")
		return
	}
	token, expires := randomToken(), time.Now().UTC().Add(5*time.Minute).Format("2006-01-02T15:04:05.000000000Z")
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("DELETE FROM host_fingerprint_challenges WHERE expires_at<=?", Now())
	var count int
	if e == nil {
		e = tx.QueryRow("SELECT count(*) FROM host_fingerprint_challenges").Scan(&count)
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if count >= 100 {
		h.fail(w, r, 429, "FINGERPRINT_LIMIT", "待确认连接过多，请稍后重试")
		return
	}
	current, e := latestHostCredential(tx, in.CredentialID)
	if e != nil || current != revision {
		h.fail(w, r, 409, "CREDENTIAL_CHANGED", "凭据已变化，请重新获取指纹")
		return
	}
	_, e = tx.Exec("INSERT INTO host_fingerprint_challenges VALUES(?,?,?,?,?,?,?,?,?)", hashToken(token), s.AdminID, in.Address, in.Port, in.User, in.CredentialID, revision, fingerprint, expires)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, map[string]any{"fingerprintToken": token, "hostFingerprint": fingerprint, "address": in.Address, "sshPort": in.Port, "expiresAt": expires})
}

func hostBusy(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (bool, error) {
	var n int
	e := q.QueryRow("SELECT (SELECT count(*) FROM jobs WHERE (target_id=? OR (kind='IMPORT' AND json_extract(CASE WHEN json_valid(input_json) THEN input_json ELSE '{}' END,'$.runtimeEnvironment.targetId')=?)) AND (status IN ('QUEUED','RUNNING','UNKNOWN') OR target_ack_pending=1)) + (SELECT count(*) FROM operation_locks WHERE resource_key=?)", id, id, "target:"+id).Scan(&n)
	return n > 0, e
}

func (h *HTTP) saveHost(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in hostInput
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Kind == "" {
		in.Kind = "SSH"
	}
	var old target
	var e error
	if id != "" {
		old, e = h.store.readTarget(id)
		if e != nil {
			h.opsError(w, r, e)
			return
		}
	}
	if in.InstallRoot == "" {
		in.InstallRoot = defaultHostRoot
		if id != "" {
			in.InstallRoot = old.InstallRoot
		}
	}
	if !validName(in.Name) || !deploy.ValidRoot(in.InstallRoot) || (in.Kind != "LOCAL" && in.Kind != "SSH") {
		h.fail(w, r, 400, "INVALID_INPUT", "请填写名称与项目目录：限/opt、/srv或/var/lib下的规范子目录，最长240字符")
		return
	}
	var revision, fingerprint string
	if in.Kind == "SSH" {
		connection := fingerprintInput{in.Address, in.Port, in.User, in.CredentialID}
		if !normalizeSSHInput(&connection) {
			h.fail(w, r, 400, "SSH_CONFIG_INVALID", "请填写有效地址、SSH端口、用户并选择连接凭据")
			return
		}
		in.Address = connection.Address
		revision, e = latestHostCredential(h.store.DB, in.CredentialID)
		if e != nil {
			h.fail(w, r, 409, "CREDENTIAL_UNAVAILABLE", "连接凭据不可用")
			return
		}
		unchanged := id != "" && old.Kind == in.Kind && old.Address == in.Address && old.Port == in.Port && old.User == in.User && old.CredentialID == in.CredentialID && old.Revision == revision
		if unchanged && in.FingerprintToken == "" {
			fingerprint = old.Fingerprint
		} else {
			if !h.acquireHostCheck(w, r) {
				return
			}
			defer func() { <-h.hostCheckSlots }()
			var expires string
			e = h.store.DB.QueryRow("SELECT host_fingerprint,expires_at FROM host_fingerprint_challenges WHERE token_hash=? AND actor_id=? AND address=? AND ssh_port=? AND ssh_user=? AND credential_id=? AND CAST(credential_revision AS TEXT)=?", hashToken(in.FingerprintToken), s.AdminID, in.Address, in.Port, in.User, in.CredentialID, revision).Scan(&fingerprint, &expires)
			if e != nil || expires <= Now() {
				h.fail(w, r, 409, "FINGERPRINT_CONFIRMATION_REQUIRED", "请重新获取并核对SSH指纹后保存")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			actual, err := discoverSSHKey(ctx, in.Address, in.Port)
			cancel()
			if err != nil || actual != fingerprint {
				h.fail(w, r, 409, "SSH_HOST_KEY_CHANGED", "SSH指纹已变化或无法核对，请重新获取")
				return
			}
		}
	} else {
		in.Address = "127.0.0.1"
		in.Port = 22
		in.User = ""
		in.CredentialID = ""
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	if id != "" {
		current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", id))
		if err != nil {
			h.opsError(w, r, err)
			return
		}
		if in.ExpectedVersion != current.Version {
			h.fail(w, r, 409, "VERSION_CONFLICT", "主机已变化，请刷新后重试")
			return
		}
		busy, err := hostBusy(tx, id)
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if busy {
			h.fail(w, r, 409, "HOST_BUSY", "主机仍有未决作业，请先核对或收尾")
			return
		}
		old = current
		identityChanged := old.Kind != in.Kind || old.Address != in.Address || old.Port != in.Port || old.InstallRoot != in.InstallRoot
		if identityChanged {
			var deployed int
			e = tx.QueryRow("SELECT (SELECT count(*) FROM instances i WHERE i.target_id=? AND (i.current_release_id IS NOT NULL OR i.previous_release_id IS NOT NULL OR EXISTS(SELECT 1 FROM releases WHERE instance_id=i.id)))+(SELECT count(*) FROM plugin_installations WHERE target_id=?)+(SELECT count(*) FROM software_instances WHERE host_id=?)", id, id, id).Scan(&deployed)
			if e != nil {
				h.internal(w, r, e)
				return
			}
			if deployed > 0 {
				h.fail(w, r, 409, "HOST_IDENTITY_IN_USE", "主机已有部署记录，不能变更服务器地址、连接类型或受管目录")
				return
			}
		}
	}
	var count int
	connectionChanged := id == "" || old.Kind != in.Kind || old.Address != in.Address || old.Port != in.Port
	e = tx.QueryRow("SELECT count(*) FROM targets WHERE id<>? AND (name=? OR (kind='LOCAL' AND ?='LOCAL') OR (? AND kind='SSH' AND ?='SSH' AND address=? AND ssh_port=?))", id, in.Name, in.Kind, connectionChanged, in.Kind, in.Address, in.Port).Scan(&count)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "HOST_DUPLICATE", "名称或服务器连接已登记")
		return
	}
	if id == "" {
		e = tx.QueryRow("SELECT count(*) FROM targets WHERE kind=?", in.Kind).Scan(&count)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		if in.Kind == "SSH" && count >= 16 {
			h.fail(w, r, 409, "HOST_LIMIT", "最多支持16个SSH主机")
			return
		}
	}
	if in.Kind == "SSH" {
		current, err := latestHostCredential(tx, in.CredentialID)
		if err != nil || current != revision {
			h.fail(w, r, 409, "CREDENTIAL_CHANGED", "凭据已变化，请重新获取指纹")
			return
		}
		if in.FingerprintToken != "" {
			result, err := tx.Exec("DELETE FROM host_fingerprint_challenges WHERE token_hash=? AND actor_id=? AND address=? AND ssh_port=? AND ssh_user=? AND credential_id=? AND CAST(credential_revision AS TEXT)=? AND host_fingerprint=? AND expires_at>?", hashToken(in.FingerprintToken), s.AdminID, in.Address, in.Port, in.User, in.CredentialID, revision, fingerprint, Now())
			if err != nil {
				h.internal(w, r, err)
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				h.fail(w, r, 409, "FINGERPRINT_CONFIRMATION_REQUIRED", "指纹确认已失效，请重新获取")
				return
			}
		}
	}
	created := id == ""
	if created {
		id = uuid.NewString()
		_, e = tx.Exec("INSERT INTO targets(id,name,install_root,kind,address,ssh_port,ssh_user,host_fingerprint,credential_id,credential_revision,created_at) VALUES(?,?,?,?,?,?,?,?,nullif(?,''),nullif(?,''),?)", id, in.Name, in.InstallRoot, in.Kind, in.Address, in.Port, in.User, fingerprint, in.CredentialID, revision, Now())
	} else {
		_, e = tx.Exec("UPDATE targets SET name=?,install_root=?,kind=?,address=?,ssh_port=?,ssh_user=?,host_fingerprint=?,credential_id=nullif(?,''),credential_revision=nullif(?,''),version=version+1,capabilities_json='{}',observation_json='{}',observed_at='' WHERE id=?", in.Name, in.InstallRoot, in.Kind, in.Address, in.Port, in.User, fingerprint, in.CredentialID, revision, id)
	}
	if e == nil {
		e = auditTx(tx, s.Username, "HOST_SAVED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	t, e := h.store.readTarget(id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if created {
		w.Header().Set("Location", api+"/hosts/"+id)
		h.reply(w, r, 201, "SUCCESS", "", t)
	} else {
		h.success(w, r, t)
	}
}

func (h *HTTP) setHostMaintenance(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Maintenance     bool   `json:"maintenance"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
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
	res, e := tx.Exec("UPDATE targets SET maintenance=?,version=version+1 WHERE id=? AND CAST(version AS TEXT)=?", in.Maintenance, id, in.ExpectedVersion)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机不存在或已变化，请刷新")
		return
	}
	if e = auditTx(tx, s.Username, "HOST_MAINTENANCE_CHANGED", id, "SUCCESS", r.Context().Value(traceKey{}).(string)); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	t, e := h.store.readTarget(id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, t)
}

func (h *HTTP) deleteHost(w http.ResponseWriter, r *http.Request, s *session, id string) {
	version := r.URL.Query().Get("expectedVersion")
	if _, e := strconv.ParseUint(version, 10, 64); e != nil {
		h.fail(w, r, 400, "VERSION_REQUIRED", "请携带当前主机版本")
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
	var count int
	e = tx.QueryRow("SELECT (SELECT count(*) FROM instances WHERE target_id=?)+(SELECT count(*) FROM jobs WHERE target_id=?)+(SELECT count(*) FROM plugin_installations WHERE target_id=?)+(SELECT count(*) FROM plugin_install_plans WHERE target_id=?)+(SELECT count(*) FROM software_instances WHERE host_id=?)+(SELECT count(*) FROM software_install_plans WHERE target_id=?)+(SELECT count(*) FROM firewall_plans WHERE target_id=?)", id, id, id, id, id, id, id).Scan(&count)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "HOST_IN_USE", "主机仍被实例、软件或作业历史引用，不能删除")
		return
	}
	res, e := tx.Exec("DELETE FROM targets WHERE id=? AND CAST(version AS TEXT)=?", id, version)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机不存在或已变化，请刷新")
		return
	}
	if e = auditTx(tx, s.Username, "HOST_DELETED", id, "SUCCESS", r.Context().Value(traceKey{}).(string)); e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, nil)
}

func hostMutationAllowed(q interface{ QueryRow(string, ...any) *sql.Row }, id string) error {
	var maintenance bool
	if e := q.QueryRow("SELECT maintenance FROM targets WHERE id=?", id).Scan(&maintenance); e != nil {
		return e
	}
	if maintenance {
		return errors.New("HOST_MAINTENANCE")
	}
	return nil
}
