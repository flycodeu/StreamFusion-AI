package controller

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

type gitAccount struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AuthMethod         string `json:"authMethod"`
	Username           string `json:"username"`
	ConfiguredVersion  string `json:"configuredVersion"`
	SSHHost            string `json:"sshHost"`
	SSHPort            int    `json:"sshPort"`
	SSHUser            string `json:"sshUser"`
	AuthorName         string `json:"authorName"`
	AuthorEmail        string `json:"authorEmail"`
	CredentialID       string `json:"credentialId"`
	CredentialRevision string `json:"credentialRevision"`
	HostPublicKey      string `json:"hostPublicKey"`
	KeyFingerprint     string `json:"keyFingerprint"`
	KeySource          string `json:"keySource"`
	HostID             string `json:"hostId"`
	SystemAccount      string `json:"systemAccount"`
	AccountUID         string `json:"accountUid"`
	KeyID              string `json:"keyId"`
	KeyPath            string `json:"keyPath"`
	PublicKey          string `json:"publicKey"`
	Version            string `json:"version"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

const gitAccountColumns = "id,name,ssh_host,ssh_port,ssh_user,author_name,author_email,coalesce(credential_id,''),coalesce(CAST(credential_revision AS TEXT),''),host_public_key,key_fingerprint,CAST(version AS TEXT),created_at,updated_at,key_source,coalesce(host_id,''),system_account,account_uid,key_id,key_path,public_key,auth_method,login_username,CAST(configured_version AS TEXT)"

func scanGitAccount(row interface{ Scan(...any) error }) (gitAccount, error) {
	var a gitAccount
	err := row.Scan(&a.ID, &a.Name, &a.SSHHost, &a.SSHPort, &a.SSHUser, &a.AuthorName, &a.AuthorEmail, &a.CredentialID, &a.CredentialRevision, &a.HostPublicKey, &a.KeyFingerprint, &a.Version, &a.CreatedAt, &a.UpdatedAt, &a.KeySource, &a.HostID, &a.SystemAccount, &a.AccountUID, &a.KeyID, &a.KeyPath, &a.PublicKey, &a.AuthMethod, &a.Username, &a.ConfiguredVersion)
	return a, err
}
func (a gitAccount) request() deploy.GitRepositoryRequest {
	g := deploy.GitRepositoryRequest{Action: "BIND", BindingID: uuid.NewString(), BindingVersion: "1", ProfileID: a.ID, ProfileVersion: a.Version, CredentialID: a.CredentialID, CredentialRevision: a.CredentialRevision, Account: "gitcheck", RepoPath: "/home/gitcheck/repository", GitBinary: "/usr/bin/git", SSHHost: a.SSHHost, SSHPort: a.SSHPort, SSHUser: a.SSHUser, HostPublicKey: a.HostPublicKey, KeyFingerprint: a.KeyFingerprint, AuthorName: a.AuthorName, AuthorEmail: a.AuthorEmail, KeySource: a.KeySource, KeyPath: a.KeyPath}
	if a.KeySource == "TARGET" {
		g.Account, g.AccountUID = a.SystemAccount, a.AccountUID
	} else {
		// Omit new fields so existing v1 executors can still read legacy jobs.
		g.KeySource = ""
	}
	return g
}

func (h *HTTP) gitRoute(w http.ResponseWriter, r *http.Request, s *session, route string) bool {
	p := strings.Split(strings.Trim(route, "/"), "/")
	if route == "/git-accounts" && r.Method == "GET" {
		h.listGitAccounts(w, r)
		return true
	}
	if route == "/git-accounts" && r.Method == "POST" {
		h.saveGitAccount(w, r, s, "")
		return true
	}
	if len(p) == 2 && p[0] == "git-accounts" && deploy.ID.MatchString(p[1]) && (r.Method == "PATCH" || r.Method == "DELETE") {
		h.saveGitAccount(w, r, s, p[1])
		return true
	}
	if len(p) == 3 && p[0] == "git-accounts" && deploy.ID.MatchString(p[1]) && (p[2] == "configure" || p[2] == "remove-configuration") && r.Method == "POST" {
		h.configureGitAccount(w, r, s, p[1], p[2] == "remove-configuration")
		return true
	}
	if len(p) == 3 && p[0] == "hosts" && deploy.ID.MatchString(p[1]) {
		if p[2] == "git-keys" && r.Method == "POST" {
			h.prepareGitKey(w, r, s, p[1])
			return true
		}
		if p[2] == "git-repositories" && r.Method == "GET" {
			h.listGitRepositories(w, r, p[1])
			return true
		}
		if p[2] == "git-repository-plans" && r.Method == "POST" {
			h.createGitPlan(w, r, s, p[1])
			return true
		}
	}
	if len(p) == 3 && p[0] == "git-repository-plans" && deploy.ID.MatchString(p[1]) && p[2] == "submit" && r.Method == "POST" {
		h.submitGitPlan(w, r, s, p[1])
		return true
	}
	if len(p) == 3 && p[0] == "git-repositories" && deploy.ID.MatchString(p[1]) {
		if r.Method == "POST" && (p[2] == "observe" || p[2] == "test") {
			h.observeGitRepository(w, r, s, p[1], p[2] == "test")
			return true
		}
		if r.Method == "GET" && p[2] == "jobs" {
			h.gitRepositoryJobs(w, r, p[1])
			return true
		}
	}
	return false
}

func (h *HTTP) listGitAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.Query("SELECT " + gitAccountColumns + " FROM git_accounts WHERE deleted_at='' ORDER BY created_at,id")
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer rows.Close()
	items := []gitAccount{}
	for rows.Next() {
		a, e := scanGitAccount(rows)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		items = append(items, a)
	}
	if err = rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, items)
}

func (h *HTTP) saveGitAccount(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		gitAccount
		ExpectedVersion     string `json:"expectedVersion"`
		HostKeyConfirmed    bool   `json:"hostKeyConfirmed"`
		ExpectedHostVersion string `json:"expectedHostVersion"`
	}
	if decode(w, r, &in) != nil {
		h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
		return
	}
	if r.Method != "DELETE" {
		in.Name = strings.TrimSpace(in.Name)
		in.SSHHost = strings.ToLower(strings.TrimSpace(in.SSHHost))
		in.AuthorName = strings.TrimSpace(in.AuthorName)
		in.AuthorEmail = strings.TrimSpace(in.AuthorEmail)
		in.ID = id
		if id == "" {
			in.ID = uuid.NewString()
		}
		in.Version = "1"
		if in.AuthMethod == "HTTPS" {
			if _, err := h.store.secret(in.CredentialID, in.CredentialRevision, "TOKEN"); err != nil {
				h.gitError(w, r, errors.New("GIT_CREDENTIAL_UNAVAILABLE"))
				return
			}
			t, err := h.store.readTarget(in.HostID)
			if err != nil || t.Maintenance || t.Version != in.ExpectedHostVersion {
				h.gitError(w, r, errors.New("HOST_CHANGED"))
				return
			}
			if !gitProfileUser.MatchString(in.SystemAccount) || in.SystemAccount == "root" || !gitProfileLogin.MatchString(in.Username) {
				h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
				return
			}
			in.KeySource = "OPS"
			in.SSHUser = "git"
			in.KeyID, in.KeyPath, in.PublicKey, in.KeyFingerprint, in.HostPublicKey = "", "", "", "", ""
			in.AccountUID = ""
		} else if in.AuthMethod == "SSH" && in.KeySource == "TARGET" {
			in.AuthMethod = "SSH"
			t, err := h.store.readTarget(in.HostID)
			if err != nil || t.Maintenance || t.Version != in.ExpectedHostVersion {
				h.gitError(w, r, errors.New("HOST_CHANGED"))
				return
			}
			if !deploy.ID.MatchString(in.KeyID) || in.SystemAccount == "root" || in.SystemAccount == "" {
				h.gitError(w, r, errors.New("GIT_KEY_INPUT_INVALID"))
				return
			}
			if !h.acquireHostCheck(w, r) {
				return
			}
			defer func() { <-h.hostCheckSlots }()
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			evidence, err := h.targetGitKey(ctx, t, "GIT_KEY_READ", deploy.GitKeyRequest{ID: in.KeyID, Account: in.SystemAccount})
			if err != nil {
				h.gitError(w, r, err)
				return
			}
			in.CredentialID, in.CredentialRevision = "", ""
			in.KeyPath, in.PublicKey, in.KeyFingerprint, in.AccountUID = evidence.KeyPath, evidence.PublicKey, evidence.Fingerprint, evidence.AccountUID
		} else {
			h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
			return
		}
		if !validGitAccount(in.gitAccount) {
			h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
			return
		}
		if in.HostPublicKey != "" {
			key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(in.HostPublicKey))
			if !in.HostKeyConfirmed || err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
				h.gitError(w, r, errors.New("GIT_HOST_KEY_INVALID"))
				return
			}
			in.HostPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
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
	var previous gitAccount
	if id != "" {
		previous, err = scanGitAccount(tx.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", id))
		if err != nil {
			h.opsError(w, r, err)
			return
		}
		if previous.Version != in.ExpectedVersion {
			h.gitError(w, r, errors.New("GIT_PROFILE_CHANGED"))
			return
		}
		if r.Method != "DELETE" && (previous.HostID != in.HostID || previous.SystemAccount != in.SystemAccount || previous.AuthMethod != in.AuthMethod || previous.SSHHost != in.SSHHost || previous.SSHPort != in.SSHPort || previous.Username != in.Username) {
			h.gitError(w, r, errors.New("GIT_ACCOUNT_IDENTITY_IMMUTABLE"))
			return
		}
		if in.AuthMethod == "HTTPS" {
			in.AccountUID = previous.AccountUID
		}
		var count int
		err = tx.QueryRow("SELECT count(*) FROM git_repositories b LEFT JOIN jobs j ON j.id=b.last_job_id WHERE b.profile_id=? AND (b.status<>'RELEASED' OR j.status IN ('QUEUED','RUNNING','UNKNOWN') OR j.target_ack_pending=1)", id).Scan(&count)
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if r.Method == "DELETE" && count > 0 {
			h.gitError(w, r, errors.New("GIT_PROFILE_IN_USE"))
			return
		}
		if r.Method == "DELETE" && previous.ConfiguredVersion != "0" {
			h.gitError(w, r, errors.New("GIT_ACCOUNT_CONFIGURATION_IN_USE"))
			return
		}
		// Freeze profile metadata for queued operations; existing applied bindings
		// keep their snapshot until the operator explicitly reapplies a new version.
		err = tx.QueryRow("SELECT count(*) FROM jobs j JOIN git_repository_plans p ON p.job_id=j.id WHERE json_extract(p.snapshot_json,'$.change.profileId')=? AND (j.status IN ('QUEUED','RUNNING','UNKNOWN') OR j.target_ack_pending=1)", id).Scan(&count)
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if count > 0 {
			h.gitError(w, r, errors.New("OPERATION_LOCKED"))
			return
		}
	}
	now := Now()
	action := "GIT_ACCOUNT_SAVED"
	if r.Method == "DELETE" {
		_, err = tx.Exec("UPDATE git_accounts SET deleted_at=?,version=version+1,updated_at=? WHERE id=?", now, now, id)
		action = "GIT_ACCOUNT_DELETED"
		in.ID = id
	} else {
		var valid int
		if in.KeySource == "TARGET" {
			err = tx.QueryRow("SELECT count(*) FROM targets WHERE id=? AND CAST(version AS TEXT)=? AND maintenance=0", in.HostID, in.ExpectedHostVersion).Scan(&valid)
		} else {
			kind := "SSH_KEY"
			if in.AuthMethod == "HTTPS" {
				kind = "TOKEN"
			}
			err = tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND revision=? AND kind=? AND revoked_at IS NULL", in.CredentialID, in.CredentialRevision, kind).Scan(&valid)
		}
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if valid != 1 {
			code := "GIT_CREDENTIAL_UNAVAILABLE"
			if in.KeySource == "TARGET" {
				code = "HOST_CHANGED"
			}
			h.gitError(w, r, errors.New(code))
			return
		}
		if id == "" {
			var count int
			err = tx.QueryRow("SELECT count(*) FROM git_accounts").Scan(&count)
			if err != nil {
				h.internal(w, r, err)
				return
			}
			if count >= 100 {
				h.gitError(w, r, errors.New("GIT_ACCOUNT_LIMIT"))
				return
			}
			_, err = tx.Exec("INSERT INTO git_accounts(id,name,ssh_host,ssh_port,ssh_user,author_name,author_email,credential_id,credential_revision,host_public_key,key_fingerprint,created_at,updated_at,key_source,host_id,system_account,account_uid,key_id,key_path,public_key) VALUES(?,?,?,?,?,?,?,nullif(?,''),nullif(?,''),?,?,?,?,?,nullif(?,''),?,?,?,?,?)", in.ID, in.Name, in.SSHHost, in.SSHPort, in.SSHUser, in.AuthorName, in.AuthorEmail, in.CredentialID, in.CredentialRevision, in.HostPublicKey, in.KeyFingerprint, now, now, in.KeySource, in.HostID, in.SystemAccount, in.AccountUID, in.KeyID, in.KeyPath, in.PublicKey)
		} else {
			_, err = tx.Exec("UPDATE git_accounts SET name=?,ssh_host=?,ssh_port=?,ssh_user=?,author_name=?,author_email=?,credential_id=nullif(?,''),credential_revision=nullif(?,''),host_public_key=?,key_fingerprint=?,key_source=?,host_id=nullif(?,''),system_account=?,account_uid=?,key_id=?,key_path=?,public_key=?,version=version+1,updated_at=? WHERE id=?", in.Name, in.SSHHost, in.SSHPort, in.SSHUser, in.AuthorName, in.AuthorEmail, in.CredentialID, in.CredentialRevision, in.HostPublicKey, in.KeyFingerprint, in.KeySource, in.HostID, in.SystemAccount, in.AccountUID, in.KeyID, in.KeyPath, in.PublicKey, now, id)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE git_accounts SET auth_method=?,login_username=? WHERE id=?", in.AuthMethod, in.Username, in.ID)
		}
	}
	if err == nil {
		err = auditTx(tx, s.Username, action, in.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if r.Method == "DELETE" {
		h.success(w, r, map[string]string{"id": id})
		return
	}
	a, err := scanGitAccount(h.store.DB.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", in.ID))
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, a)
}

var gitProfileHost = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)
var gitProfileUser = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)
var gitProfileLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)

// Account authentication is reusable by future projects. A repository path,
// commit author and host trust decision are not prerequisites for saving it.
func validGitAccount(a gitAccount) bool {
	if len(a.Name) > 120 || len(a.AuthorName) > 200 || len(a.AuthorEmail) > 254 || strings.ContainsAny(a.Name+a.AuthorName+a.AuthorEmail, "\x00\r\n") {
		return false
	}
	return a.Name != "" && (gitProfileHost.MatchString(a.SSHHost) || net.ParseIP(a.SSHHost) != nil) && a.SSHPort > 0 && a.SSHPort <= 65535 && gitProfileUser.MatchString(a.SSHUser) && (a.AuthorEmail == "" || strings.Contains(a.AuthorEmail, "@")) && len(a.HostPublicKey) <= 16<<10
}

var gitMessages = map[string]string{
	"GIT_ACCOUNT_IDENTITY_IMMUTABLE":      "服务器、系统账号和认证地址已确定；请新增另一个账号配置",
	"GIT_ACCOUNT_CONFIGURATION_FAILED":    "服务器账号配置未确认，请检查目录归属、Git 和 SSH 配置；重试会复用原配置",
	"GIT_ACCOUNT_CONFIGURATION_IN_USE":    "请先移除该账号在服务器上的配置，再删除账号记录",
	"GIT_ACCOUNT_INPUT_INVALID":           "请填写账号名称、Git 服务地址、系统账号及所选认证信息",
	"GIT_KEY_INPUT_INVALID":               "请使用所选服务器的普通账号生成 Git 密钥",
	"GIT_KEY_UNAVAILABLE":                 "无法生成或读取目标账号的 Git 密钥，请检查账号目录权限与 ssh-keygen",
	"GIT_KEY_SCOPE_MISMATCH":              "此 Git 账号属于另一台服务器或系统账号，请重新选择",
	"GIT_HOST_KEY_INVALID":                "请核对 Git 服务主机公钥；也可以先留空保存账号，连接时再核验",
	"GIT_LEGACY_CONFIGURATION_INCOMPLETE": "历史仓库绑定还需要提交姓名、邮箱和已核验的 Git 主机公钥，请在账号可选配置中补充",
	"GIT_CREDENTIAL_UNAVAILABLE":          "认证凭据不存在或已撤销，请重新设置",
	"GIT_PRIVATE_KEY_UNSUPPORTED":         "请选择可读取的SSH私钥；当前不支持需要口令解锁的密钥",
	"GIT_PROFILE_CHANGED":                 "Git账号配置已变化，请刷新后重新检查",
	"GIT_PROFILE_IN_USE":                  "账号仍有仓库绑定，请先改绑或解除绑定",
	"GIT_ACCOUNT_LIMIT":                   "Git账号已达100个，请整理现有配置",
	"GIT_HOST_KEY_UNAVAILABLE":            "未能读取Git服务器公钥，请检查地址或手动填写可信公钥",
	"EXECUTOR_GIT_UNSUPPORTED":            "接入组件需要更新才能配置 Git 账号",
	"GIT_PLAN_STALE":                      "仓库配置或计划已变化，请重新检查",
	"GIT_BINDING_CHANGED":                 "仓库绑定已变化，请刷新后重试",
	"GIT_REPOSITORY_EXISTS":               "该目录已有绑定，请从仓库列表修改",
	"GIT_REPOSITORY_LIMIT":                "当前主机仓库绑定已达200个",
	"GIT_RESULT_UNCONFIRMED":              "配置结果待确认，请核对原操作，不要重复创建绑定",
	"GIT_RESULT_MISMATCH":                 "仓库配置回执不一致，请核对原操作",
	"GIT_PATH_UNSAFE":                     "仓库或账号目录的归属、权限不安全，或使用了符号链接，请检查目录",
	"GIT_ACCOUNT_CHANGED":                 "服务器账号不存在或身份已变化，请核对账号",
	"GIT_CONFIG_INCLUDE_UNSUPPORTED":      "当前不支持带 include/includeIf 的仓库配置，请保留原配置并改用独立仓库",
	"GIT_WORKTREE_UNSUPPORTED":            "当前仅支持普通 Git 仓库，不支持 linked worktree",
	"GIT_REMOTE_IDENTITY_MISMATCH":        "origin 地址与所选 Git 账号不匹配，请核对 SSH 地址、用户和服务器",
	"GIT_REMOTE_READ_FAILED":              "无法读取远端仓库，请核对密钥授权、服务器公钥及网络",
	"GIT_BINDING_DRIFT":                   "仓库受管配置已被修改，请先核对现有配置",
	"GIT_KEY_DRIFT":                       "仓库密钥文件或权限已变化，请先检查服务器上的绑定文件",
	"GIT_CONFIRMATION_REQUIRED":           "请先检查仓库并确认配置变更",
	"OPERATION_LOCKED":                    "主机存在未完成操作，请先核对原操作结果",
	"HOST_CHANGED":                        "主机配置已变化，请刷新后重新检查",
	"QUEUE_FULL":                          "待处理作业较多，请稍后再试",
}

func (h *HTTP) gitError(w http.ResponseWriter, r *http.Request, err error) {
	code := err.Error()
	message := gitMessages[code]
	if message == "" {
		if !strings.HasPrefix(code, "GIT_") || len(code) > 96 || strings.ContainsAny(code, " \r\n:") {
			code = executorFailureCode(err)
		}
		message = executorFailureMessages[code]
		if message == "" {
			message = "Git操作未完成，请检查仓库归属、Git程序、配置和读取权限"
		}
	}
	h.fail(w, r, 409, code, message)
}

// Query helpers accept both a database and the existing authorization transaction.
type gitQuery interface{ QueryRow(string, ...any) *sql.Row }
