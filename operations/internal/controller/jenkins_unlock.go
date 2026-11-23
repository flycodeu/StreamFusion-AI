package controller

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/bcrypt"
)

const jenkinsUnlockAudit = "JENKINS_UNLOCK_READ"

var jenkinsUnlockMessages = map[string]string{
	"JENKINS_UNLOCK_BINDING_UNVERIFIED": "无法确认 Jenkins 实例归属，请检查软件状态和接入组件",
	"JENKINS_UNLOCK_SETUP_UNAVAILABLE":  "暂时无法确认 Jenkins 解锁页，请确认服务运行后重试",
	"JENKINS_UNLOCK_SETUP_INACTIVE":     "Jenkins 已离开首次解锁页面，请使用已配置的账号登录",
	"JENKINS_UNLOCK_SECRET_UNAVAILABLE": "无法读取此 Jenkins 实例的首次解锁密码，请核对软件状态和数据目录",
	"JENKINS_UNLOCK_INVALID":            "接入组件未接受此读取请求，请核对控制器与主机归属",
	"EXECUTOR_BUSY":                     "接入组件正在执行操作，请稍后重试",
	"TARGET_PENDING":                    "主机有待核对的作业，请先查看执行记录",
}

var jenkinsUnlockPassword = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (h *HTTP) jenkinsUnlockRoute(w http.ResponseWriter, r *http.Request, actor *session, path string) bool {
	p := strings.Split(path, "/")
	if len(p) != 4 || p[0] != "" || p[1] != "software-instances" || !deploy.ID.MatchString(p[2]) || p[3] != "jenkins-unlock" {
		return false
	}
	if r.Method != http.MethodPost {
		h.fail(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "首次解锁密码需要管理员确认后读取")
		return true
	}
	h.readJenkinsUnlock(w, r, actor, p[2])
	return true
}

func (h *HTTP) sensitiveResponseAllowed(r *http.Request) bool {
	if h.origin.Scheme == "https" {
		return true
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	host := h.origin.Hostname()
	return err == nil && net.ParseIP(peer).IsLoopback() && (host == "localhost" || net.ParseIP(host).IsLoopback())
}

func (h *HTTP) readJenkinsUnlock(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	if !h.sensitiveResponseAllowed(r) {
		h.fail(w, r, 403, "JENKINS_UNLOCK_TLS_REQUIRED", "请通过 HTTPS 或本机回环地址复制首次解锁密码")
		return
	}
	var in struct {
		Password        string `json:"password"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if h.decodeSensitive(w, r, actor, &in) != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	version, err := strconv.ParseInt(in.ExpectedVersion, 10, 64)
	if err != nil || version < 1 || strconv.FormatInt(version, 10) != in.ExpectedVersion {
		h.fail(w, r, 409, "VERSION_CONFLICT", "请刷新软件详情后重试")
		return
	}
	if len(in.Password) == 0 || len(in.Password) > 64 {
		h.fail(w, r, 400, "JENKINS_UNLOCK_PASSWORD_FORMAT", "请输入登录 Ops 的管理员密码，最多 64 字节")
		return
	}
	if !h.confirmSensitiveAdminPassword(w, r, actor, id, in.Password, jenkinsUnlockAudit, "JENKINS_UNLOCK") {
		return
	}
	item, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if item.Version != in.ExpectedVersion {
		h.fail(w, r, 409, "VERSION_CONFLICT", "软件记录已变化，请刷新详情后重试")
		return
	}
	if !installationBound(item) || item.InstallationStatus != "READY" || item.ActualState != "RUNNING" || item.Installation == nil ||
		item.InstallationEvidence == nil || item.InstallationEvidence.ValidateFor(*item.Installation) != nil || !deploy.JenkinsUnlockRecipe(item.Installation.Definition) {
		h.fail(w, r, 409, "JENKINS_UNLOCK_BINDING_UNVERIFIED", jenkinsUnlockMessages["JENKINS_UNLOCK_BINDING_UNVERIFIED"])
		return
	}
	host, err := h.store.readTarget(item.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	busy, err := softwareBusy(h.store.DB, id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if busy {
		h.fail(w, r, 409, "OPERATION_LOCKED", "软件有进行中的操作，请完成后重试")
		return
	}
	select {
	case h.hostCheckSlots <- struct{}{}:
		defer func() { <-h.hostCheckSlots }()
	default:
		h.fail(w, r, 429, "HOST_CHECK_BUSY", "主机检查请求较多，请稍后重试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.JenkinsUnlockReadResult
	err = h.store.callTarget(ctx, host, deploy.Request{Action: deploy.JenkinsUnlockReadAction, Software: softwareRequest(item), TraceID: r.Context().Value(traceKey{}).(string)}, "", &result)
	code := "SUCCESS"
	if err != nil {
		code = executorFailureCode(err)
		switch code {
		case "SOFTWARE_INPUT_INVALID", "SOFTWARE_JOB_IDENTITY_INVALID", "EXECUTOR_UPGRADE_REQUIRED":
			code = "JENKINS_UNLOCK_UNSUPPORTED"
		}
	} else if result.ControllerID != item.ControllerID || result.TargetID != host.ID || result.SoftwareID != item.ID ||
		result.BindingRevision != item.BindingRevision || result.IdentityHash != item.IdentityHash || !jenkinsUnlockPassword.MatchString(result.Password) {
		code = "JENKINS_UNLOCK_BINDING_UNVERIFIED"
	}
	if r.Context().Err() == nil {
		h.finishJenkinsUnlock(w, r, actor, item, host, code, result.Password)
	}
}

func (h *HTTP) confirmSensitiveAdminPassword(w http.ResponseWriter, r *http.Request, actor *session, id, password, auditAction, codePrefix string) bool {
	if !h.takeAttempt() {
		h.fail(w, r, 429, "LOGIN_RATE_LIMIT", "身份验证请求过多，请稍后重试")
		return false
	}
	select {
	case h.hashSlots <- struct{}{}:
		defer func() { <-h.hashSlots }()
	default:
		h.fail(w, r, 429, "LOGIN_BUSY", "身份验证处理中，请稍后重试")
		return false
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return false
	}
	defer tx.Rollback()
	var hash []byte
	var attempts int
	var locked int64
	if err = tx.QueryRow("SELECT password_hash,failed_attempts,locked_until FROM admins WHERE id=?", actor.AdminID).Scan(&hash, &attempts, &locked); err != nil {
		h.internal(w, r, err)
		return false
	}
	now := time.Now()
	if locked > now.Unix() {
		h.fail(w, r, 403, codePrefix+"_REAUTH_LOCKED", "管理员验证已锁定，请稍后重试")
		return false
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil {
		if attempts != 0 || locked != 0 {
			if _, err = tx.Exec("UPDATE admins SET failed_attempts=0,locked_until=0 WHERE id=?", actor.AdminID); err == nil {
				err = tx.Commit()
			}
			if err != nil {
				h.internal(w, r, err)
				return false
			}
		}
		return true
	}
	if err = recordAdminPasswordFailure(tx, actor.AdminID, attempts, locked, now); err == nil {
		err = auditTx(tx, actor.Username, auditAction, id, codePrefix+"_REAUTH_FAILED", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return false
	}
	h.fail(w, r, 403, codePrefix+"_REAUTH_FAILED", "当前 Ops 管理员密码不正确")
	return false
}

func (h *HTTP) finishJenkinsUnlock(w http.ResponseWriter, r *http.Request, actor *session, item softwareInstance, host target, code, password string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if code == "SUCCESS" {
		var current bool
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM software_instances s JOIN targets t ON t.id=s.host_id
			WHERE s.id=? AND CAST(s.version AS TEXT)=? AND s.identity_hash=? AND s.binding_revision=?
			AND s.installation_status='READY' AND s.management_mode='MANAGED' AND s.actual_state='RUNNING'
			AND t.id=? AND CAST(t.version AS TEXT)=?
			AND NOT EXISTS (SELECT 1 FROM operation_locks WHERE resource_key=?))`, item.ID, item.Version,
			item.IdentityHash, item.BindingRevision, host.ID, host.Version, "software:"+item.ID).Scan(&current)
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if !current {
			code = "VERSION_CONFLICT"
		}
	}
	if err = auditTx(tx, actor.Username, jenkinsUnlockAudit, item.ID, code, r.Context().Value(traceKey{}).(string)); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	switch code {
	case "SUCCESS":
		h.success(w, r, map[string]string{"password": password})
	case "VERSION_CONFLICT":
		h.fail(w, r, 409, code, "读取期间软件或主机记录已变化，请刷新后重试")
	case "JENKINS_UNLOCK_UNSUPPORTED":
		h.fail(w, r, 409, code, "请在主机安装准备中更新接入组件后再复制密码")
	default:
		message := jenkinsUnlockMessages[code]
		if message == "" {
			message = executorFailureMessages[code]
		}
		if message == "" {
			message = "读取失败，请检查主机连接和接入组件"
		}
		h.fail(w, r, 409, code, message)
	}
}
