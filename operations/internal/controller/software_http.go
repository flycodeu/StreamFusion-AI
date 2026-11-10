package controller

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) softwareRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	if path == "/software-instances" {
		switch r.Method {
		case "GET":
			h.listSoftware(w, r)
			return true
		case "POST":
			h.registerSoftware(w, r, s)
			return true
		}
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) < 2 || p[0] != "software-instances" || !deploy.ID.MatchString(p[1]) {
		return false
	}
	if len(p) == 2 && r.Method == "GET" {
		i, e := h.store.readSoftware(p[1])
		if e == nil {
			i, e = h.store.softwareWebView(i)
		}
		if e != nil {
			h.opsError(w, r, e)
		} else {
			h.success(w, r, i)
		}
		return true
	}
	if len(p) == 3 {
		switch r.Method + " " + p[2] {
		case "GET docker-access":
			h.dockerAccess(w, r, p[1])
			return true
		case "POST observe":
			h.observeSoftware(w, r, s, p[1])
			return true
		case "GET logs":
			h.softwareLogs(w, r, p[1])
			return true
		case "POST adopt", "POST actions", "POST unmanage":
			h.changeSoftware(w, r, s, p[1], p[2])
			return true
		}
	}
	return false
}

func (h *HTTP) listSoftware(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("hostId")
	rows, e := h.store.DB.Query("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE (?='' OR s.host_id=?) ORDER BY s.created_at DESC LIMIT 1000", host, host)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer rows.Close()
	out := []softwareInstance{}
	for rows.Next() {
		i, e := scanSoftware(rows)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		out = append(out, i)
	}
	if e = rows.Err(); e != nil {
		h.internal(w, r, e)
		return
	}
	rows.Close()
	for index := range out {
		out[index], e = h.store.softwareWebView(out[index])
		if e != nil {
			h.internal(w, r, e)
			return
		}
	}
	h.success(w, r, out)
}

func (h *HTTP) registerSoftware(w http.ResponseWriter, r *http.Request, s *session) {
	var in struct {
		Name    string `json:"name"`
		HostID  string `json:"hostId"`
		Type    string `json:"softwareType"`
		Version string `json:"softwareVersion"`
		Kind    string `json:"runtimeKind"`
		Locator string `json:"locator"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Locator = strings.TrimSpace(in.Locator)
	allowed := map[string]bool{"JAVA": true, "MAVEN": true, "MEDIAMTX": true, "MINIO": true, "CODEX": true, "CLAUDE": true, "GIT": true, "SEAWEEDFS": true, "MYSQL": true, "REDIS": true, "ZLM": true, "PYTHON": true, "FFMPEG": true, "NGINX": true, "JENKINS": true, "LABEL_STUDIO": true, "OTHER": true}
	probe := deploy.SoftwareRequest{Kind: in.Kind, Locator: in.Locator, SoftwareKey: in.Type, ExpectedVersion: in.Version}
	if !validName(in.Name) || !deploy.ID.MatchString(in.HostID) || !allowed[in.Type] || probe.Validate(true) != nil {
		h.fail(w, r, 400, "SOFTWARE_CONFIG_INVALID", "请选择软件、主机和有效的运行标识")
		return
	}
	if in.Kind == "TOOL" && strings.ToLower(in.Type) != in.Locator {
		h.fail(w, r, 400, "SOFTWARE_TOOL_MISMATCH", "工具类型和固定命令不一致")
		return
	}
	var javaPath, checkedHostVersion string
	if in.Kind == "TOOL" && in.Type == "JAVA" {
		target, err := h.store.readTarget(in.HostID)
		if err == nil && !target.Maintenance {
			ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			_ = h.withSoftwareRunner(ctx, target, func(run deploy.SoftwareCommandRunner) error {
				javaPath = deploy.CanonicalJavaPath(ctx, run)
				return nil
			})
			cancel()
			checkedHostVersion = target.Version
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
	if e = hostMutationAllowed(tx, in.HostID); e != nil {
		h.fail(w, r, 409, "HOST_UNAVAILABLE", "主机不存在或处于维护中")
		return
	}
	if javaPath != "" {
		var currentVersion string
		if e = tx.QueryRow("SELECT CAST(version AS TEXT) FROM targets WHERE id=?", in.HostID).Scan(&currentVersion); e != nil {
			h.internal(w, r, e)
			return
		}
		if currentVersion != checkedHostVersion {
			h.fail(w, r, 409, "HOST_CHANGED", "主机配置已变化，请刷新后重新登记")
			return
		}
	}
	var count int
	if e = tx.QueryRow("SELECT count(*) FROM software_instances WHERE host_id=? AND runtime_kind=? AND (locator=? OR locator=? OR (?='TOOL' AND locator='/usr/bin/'||CASE WHEN ?='python' THEN 'python3' ELSE ? END)) AND management_mode<>'RELEASED'", in.HostID, in.Kind, in.Locator, javaPath, in.Kind, in.Locator, in.Locator).Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "SOFTWARE_ALREADY_REGISTERED", "该生命周期对象已登记")
		return
	}
	id, now := uuid.NewString(), Now()
	_, e = tx.Exec("INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", id, in.Name, in.HostID, in.Type, in.Version, in.Kind, in.Locator, now, now)
	if e == nil {
		e = auditTx(tx, s.Username, "SOFTWARE_REGISTERED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	i, e := h.store.readSoftware(id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	w.Header().Set("Location", api+"/software-instances/"+id)
	h.reply(w, r, 201, "SUCCESS", "", i)
}
