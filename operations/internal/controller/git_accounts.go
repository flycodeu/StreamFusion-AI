package controller

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

type gitAccount struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	SSHHost            string `json:"sshHost"`
	SSHPort            int    `json:"sshPort"`
	SSHUser            string `json:"sshUser"`
	AuthorName         string `json:"authorName"`
	AuthorEmail        string `json:"authorEmail"`
	CredentialID       string `json:"credentialId"`
	CredentialRevision string `json:"credentialRevision"`
	HostPublicKey      string `json:"hostPublicKey"`
	KeyFingerprint     string `json:"keyFingerprint"`
	Version            string `json:"version"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

const gitAccountColumns = "id,name,ssh_host,ssh_port,ssh_user,author_name,author_email,credential_id,CAST(credential_revision AS TEXT),host_public_key,key_fingerprint,CAST(version AS TEXT),created_at,updated_at"

func scanGitAccount(row interface{ Scan(...any) error }) (gitAccount, error) {
	var a gitAccount
	err := row.Scan(&a.ID, &a.Name, &a.SSHHost, &a.SSHPort, &a.SSHUser, &a.AuthorName, &a.AuthorEmail, &a.CredentialID, &a.CredentialRevision, &a.HostPublicKey, &a.KeyFingerprint, &a.Version, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}
func (a gitAccount) request() deploy.GitRepositoryRequest {
	return deploy.GitRepositoryRequest{Action: "BIND", BindingID: uuid.NewString(), BindingVersion: "1", ProfileID: a.ID, ProfileVersion: a.Version, CredentialID: a.CredentialID, CredentialRevision: a.CredentialRevision, Account: "gitcheck", RepoPath: "/home/gitcheck/repository", GitBinary: "/usr/bin/git", SSHHost: a.SSHHost, SSHPort: a.SSHPort, SSHUser: a.SSHUser, HostPublicKey: a.HostPublicKey, KeyFingerprint: a.KeyFingerprint, AuthorName: a.AuthorName, AuthorEmail: a.AuthorEmail}
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
	if route == "/git-host-key" && r.Method == "POST" {
		h.gitHostKey(w, r)
		return true
	}
	if len(p) == 3 && p[0] == "hosts" && deploy.ID.MatchString(p[1]) {
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
		ExpectedVersion  string `json:"expectedVersion"`
		HostKeyConfirmed bool   `json:"hostKeyConfirmed"`
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
		secret, err := h.store.secret(in.CredentialID, in.CredentialRevision, "SSH_KEY")
		if err != nil {
			h.gitError(w, r, errors.New("GIT_CREDENTIAL_UNAVAILABLE"))
			return
		}
		signer, err := ssh.ParsePrivateKey(secret)
		if err != nil {
			h.gitError(w, r, errors.New("GIT_PRIVATE_KEY_UNSUPPORTED"))
			return
		}
		in.KeyFingerprint = ssh.FingerprintSHA256(signer.PublicKey())
		if len(in.Name) == 0 || len(in.Name) > 120 || strings.ContainsAny(in.Name, "\x00\r\n") || !in.HostKeyConfirmed || in.request().Validate() != nil {
			h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
			return
		}
		key, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(in.HostPublicKey))
		in.HostPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
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
		err = tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND revision=? AND kind='SSH_KEY' AND revoked_at IS NULL", in.CredentialID, in.CredentialRevision).Scan(&valid)
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if valid != 1 {
			h.gitError(w, r, errors.New("GIT_CREDENTIAL_UNAVAILABLE"))
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
			_, err = tx.Exec("INSERT INTO git_accounts(id,name,ssh_host,ssh_port,ssh_user,author_name,author_email,credential_id,credential_revision,host_public_key,key_fingerprint,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", in.ID, in.Name, in.SSHHost, in.SSHPort, in.SSHUser, in.AuthorName, in.AuthorEmail, in.CredentialID, in.CredentialRevision, in.HostPublicKey, in.KeyFingerprint, now, now)
		} else {
			_, err = tx.Exec("UPDATE git_accounts SET name=?,ssh_host=?,ssh_port=?,ssh_user=?,author_name=?,author_email=?,credential_id=?,credential_revision=?,host_public_key=?,key_fingerprint=?,version=version+1,updated_at=? WHERE id=?", in.Name, in.SSHHost, in.SSHPort, in.SSHUser, in.AuthorName, in.AuthorEmail, in.CredentialID, in.CredentialRevision, in.HostPublicKey, in.KeyFingerprint, now, id)
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

// A captured key is a candidate, never a trust decision. Saving it requires the
// operator to confirm it against their Git server's independent key source.
func (h *HTTP) gitHostKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SSHHost string `json:"sshHost"`
		SSHPort int    `json:"sshPort"`
	}
	if decode(w, r, &in) != nil || len(in.SSHHost) > 253 || in.SSHHost == "" || strings.ContainsAny(in.SSHHost, " /\\\x00\r\n") || in.SSHPort < 1 || in.SSHPort > 65535 {
		h.gitError(w, r, errors.New("GIT_ACCOUNT_INPUT_INVALID"))
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	address := net.JoinHostPort(in.SSHHost, strconv.Itoa(in.SSHPort))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		h.gitError(w, r, errors.New("GIT_HOST_KEY_UNAVAILABLE"))
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	var key ssh.PublicKey
	_, _, _, _ = ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: "git", HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error { key = k; return errors.New("KEY_CAPTURED") }})
	if key == nil {
		h.gitError(w, r, errors.New("GIT_HOST_KEY_UNAVAILABLE"))
		return
	}
	h.success(w, r, map[string]string{"hostPublicKey": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), "fingerprint": ssh.FingerprintSHA256(key)})
}

var gitMessages = map[string]string{
	"GIT_ACCOUNT_INPUT_INVALID":      "请填写账号信息、选择SSH私钥，并核对Git服务器公钥",
	"GIT_CREDENTIAL_UNAVAILABLE":     "SSH凭据不存在或已撤销，请重新选择",
	"GIT_PRIVATE_KEY_UNSUPPORTED":    "请选择可读取的SSH私钥；当前不支持需要口令解锁的密钥",
	"GIT_PROFILE_CHANGED":            "Git账号配置已变化，请刷新后重新检查",
	"GIT_PROFILE_IN_USE":             "账号仍有仓库绑定，请先改绑或解除绑定",
	"GIT_ACCOUNT_LIMIT":              "Git账号已达100个，请整理现有配置",
	"GIT_HOST_KEY_UNAVAILABLE":       "未能读取Git服务器公钥，请检查地址或手动填写可信公钥",
	"EXECUTOR_GIT_UNSUPPORTED":       "接入组件需要更新才能配置Git仓库",
	"GIT_PLAN_STALE":                 "仓库配置或计划已变化，请重新检查",
	"GIT_BINDING_CHANGED":            "仓库绑定已变化，请刷新后重试",
	"GIT_REPOSITORY_EXISTS":          "该目录已有绑定，请从仓库列表修改",
	"GIT_REPOSITORY_LIMIT":           "当前主机仓库绑定已达200个",
	"GIT_RESULT_UNCONFIRMED":         "配置结果待确认，请核对原操作，不要重复创建绑定",
	"GIT_RESULT_MISMATCH":            "仓库配置回执不一致，请核对原操作",
	"GIT_PATH_UNSAFE":                "仓库或账号目录的归属、权限不安全，或使用了符号链接，请检查目录",
	"GIT_ACCOUNT_CHANGED":            "服务器账号不存在或身份已变化，请核对账号",
	"GIT_CONFIG_INCLUDE_UNSUPPORTED": "当前不支持带 include/includeIf 的仓库配置，请保留原配置并改用独立仓库",
	"GIT_WORKTREE_UNSUPPORTED":       "当前仅支持普通 Git 仓库，不支持 linked worktree",
	"GIT_REMOTE_IDENTITY_MISMATCH":   "origin 地址与所选 Git 账号不匹配，请核对 SSH 地址、用户和服务器",
	"GIT_REMOTE_READ_FAILED":         "无法读取远端仓库，请核对密钥授权、服务器公钥及网络",
	"GIT_BINDING_DRIFT":              "仓库受管配置已被修改，请先核对现有配置",
	"GIT_KEY_DRIFT":                  "仓库密钥文件或权限已变化，请先检查服务器上的绑定文件",
	"GIT_CONFIRMATION_REQUIRED":      "请先检查仓库并确认配置变更",
	"OPERATION_LOCKED":               "主机存在未完成操作，请先核对原操作结果",
	"HOST_CHANGED":                   "主机配置已变化，请刷新后重新检查",
	"QUEUE_FULL":                     "待处理作业较多，请稍后再试",
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
