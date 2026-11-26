package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/projectconfig"
	"github.com/streamfusion-ai/operations/internal/projectinspect"
)

const projectSourceRootsEnv = "SFOPS_PROJECT_SOURCE_ROOTS"

type applicationProjectSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	root string
}

type applicationProjectInspectRequest struct {
	SourceID      string `json:"sourceId"`
	Directory     string `json:"directory"`
	ProjectID     string `json:"projectId"`
	RepositoryID  string `json:"repositoryId"`
	RepositoryURL string `json:"repositoryUrl"`
	Branch        string `json:"branch"`
	GitAccountID  string `json:"gitAccountId"`
}

type applicationGitCloneAuth struct {
	originURL     string
	username      string
	token         []byte
	sshHost       string
	sshPort       int
	sshKey        []byte
	hostPublicKey string
}

func (a *applicationGitCloneAuth) clear() {
	if a == nil {
		return
	}
	clear(a.token)
	clear(a.sshKey)
}

var cloneApplicationProjectRepo = defaultCloneApplicationProjectRepo

func applicationProjectSources(config string) []applicationProjectSource {
	items := []applicationProjectSource{}
	seen := map[string]bool{}
	for _, entry := range filepath.SplitList(config) {
		entry = strings.TrimSpace(entry)
		if !filepath.IsAbs(entry) || strings.HasPrefix(filepath.ToSlash(entry), "//") {
			continue
		}
		rootPath, err := filepath.EvalSymlinks(filepath.Clean(entry))
		if err != nil || strings.HasPrefix(filepath.ToSlash(rootPath), "//") {
			continue
		}
		root, err := projectinspect.OpenCheckout(rootPath)
		if err != nil {
			continue
		}
		root.Close()
		digest := sha256.Sum256([]byte(rootPath))
		id := hex.EncodeToString(digest[:16])
		if seen[id] {
			continue
		}
		seen[id] = true
		name := filepath.Base(rootPath)
		if !validProjectSourceLabel.MatchString(name) {
			name = "已登记源码 " + id[:6]
		}
		items = append(items, applicationProjectSource{ID: id, Name: name, root: rootPath})
		if len(items) == 16 {
			break
		}
	}
	names := map[string]int{}
	for _, item := range items {
		names[strings.ToLower(item.Name)]++
	}
	for i := range items {
		if names[strings.ToLower(items[i].Name)] > 1 {
			items[i].Name += " · " + items[i].ID[:6]
		}
	}
	return items
}

var validProjectSourceLabel = regexp.MustCompile(`^[\p{L}\p{N} ._-]{1,80}$`)

func (h *HTTP) listApplicationProjectSources(w http.ResponseWriter, r *http.Request) {
	h.success(w, r, map[string]any{"items": applicationProjectSources(os.Getenv(projectSourceRootsEnv))})
}

func (h *HTTP) inspectApplicationProject(w http.ResponseWriter, r *http.Request) {
	var in applicationProjectInspectRequest
	if decode(w, r, &in) != nil || len(in.Directory) > 320 {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "请填写有效的项目源码或仓库配置")
		return
	}
	in.SourceID = strings.TrimSpace(in.SourceID)
	in.Directory = strings.TrimSpace(in.Directory)
	in.ProjectID = strings.TrimSpace(in.ProjectID)
	in.RepositoryID = strings.TrimSpace(in.RepositoryID)
	in.RepositoryURL = strings.TrimSpace(in.RepositoryURL)
	in.Branch = strings.TrimSpace(in.Branch)
	in.GitAccountID = strings.TrimSpace(in.GitAccountID)

	if in.Directory != "" && in.Directory != "." && !projectconfig.ValidRelativePath(in.Directory) {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "仓库内目录须为相对路径")
		return
	}
	if in.SourceID != "" {
		for _, source := range applicationProjectSources(os.Getenv(projectSourceRootsEnv)) {
			if source.ID != in.SourceID {
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			items, err := inspectApplicationProjectSource(ctx, source.root, in.Directory)
			if err != nil {
				h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "无法读取所选源码目录，可手动填写运行环境")
				return
			}
			h.success(w, r, map[string]any{"modules": items})
			return
		}
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "请选择已登记且可读的源码")
		return
	}

	h.inspectRemoteGitRepository(w, r, in)
}

func (h *HTTP) inspectRemoteGitRepository(w http.ResponseWriter, r *http.Request, in applicationProjectInspectRequest) {
	items, ok := h.inspectRemoteGitModules(w, r, in)
	if !ok {
		return
	}
	h.success(w, r, map[string]any{"modules": items})
}

func (h *HTTP) inspectRemoteGitModules(w http.ResponseWriter, r *http.Request, in applicationProjectInspectRequest) ([]projectinspect.Module, bool) {
	if in.ProjectID != "" && !deploy.ID.MatchString(in.ProjectID) {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "项目标识无效")
		return nil, false
	}
	if in.RepositoryID != "" && !deploy.ID.MatchString(in.RepositoryID) {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "仓库标识无效")
		return nil, false
	}
	if in.GitAccountID != "" && !deploy.ID.MatchString(in.GitAccountID) {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "Git 账号标识无效")
		return nil, false
	}
	repoURL, branch, gitAccountID := in.RepositoryURL, in.Branch, in.GitAccountID
	if repoURL == "" && in.ProjectID != "" {
		rows, err := h.store.DB.QueryContext(r.Context(), "SELECT id,url,coalesce(git_account_id,''),branch FROM application_repositories WHERE project_id=? ORDER BY position,id", in.ProjectID)
		if err != nil {
			h.internal(w, r, err)
			return nil, false
		}
		for rows.Next() {
			var rid, rurl, racc, rbranch string
			if err = rows.Scan(&rid, &rurl, &racc, &rbranch); err != nil {
				break
			}
			if in.RepositoryID != "" && rid != in.RepositoryID {
				continue
			}
			repoURL = strings.TrimSpace(rurl)
			if branch == "" {
				branch = strings.TrimSpace(rbranch)
			}
			if gitAccountID == "" {
				gitAccountID = strings.TrimSpace(racc)
			}
			break
		}
		rows.Close()
		if err != nil {
			h.internal(w, r, err)
			return nil, false
		}
	}
	if repoURL == "" {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "请先填写项目 Git 仓库地址或选择已登记的本地源码")
		return nil, false
	}
	endpoint, err := applicationRepositoryEndpoint(repoURL)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", err.Error())
		return nil, false
	}
	if !applicationText(branch, 240) || strings.ContainsFunc(branch, unicode.IsControl) ||
		strings.ContainsAny(branch, "\r\n\t *?[:^~\\") || strings.HasPrefix(branch, "-") ||
		strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "分支、标签或提交格式不正确")
		return nil, false
	}
	var auth *applicationGitCloneAuth
	if gitAccountID != "" {
		account, err := scanGitAccount(h.store.DB.QueryRowContext(r.Context(), "SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=? AND deleted_at=''", gitAccountID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "所选 Git 账号不存在，请刷新")
				return nil, false
			}
			h.internal(w, r, err)
			return nil, false
		}
		if !strings.EqualFold(endpoint.Host, account.SSHHost) || endpoint.Method != account.AuthMethod {
			h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "仓库地址与所选 Git 账号的服务地址或认证方式不匹配")
			return nil, false
		}
		if endpoint.Method == "HTTPS" && (endpoint.Scheme != "https" || endpoint.Port != account.SSHPort) {
			h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "HTTPS 账号仅支持匹配端口的 HTTPS 仓库地址")
			return nil, false
		}
		if endpoint.Method == "SSH" && (endpoint.Port != 0 && endpoint.Port != account.SSHPort || endpoint.User != "" && endpoint.User != account.SSHUser) {
			h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "SSH 账号与仓库地址中的端口或登录用户不匹配")
			return nil, false
		}
		if account.AuthMethod == "HTTPS" {
			token, err := h.store.secret(account.CredentialID, account.CredentialRevision, "TOKEN")
			if err != nil {
				h.fail(w, r, http.StatusConflict, "GIT_CREDENTIAL_UNAVAILABLE", "Git 访问 Token 不可用或已撤销")
				return nil, false
			}
			origin := endpoint.Scheme + "://" + endpoint.Host
			if (endpoint.Scheme == "https" && endpoint.Port != 443) || (endpoint.Scheme == "http" && endpoint.Port != 80) {
				origin = fmt.Sprintf("%s://%s:%d", endpoint.Scheme, endpoint.Host, endpoint.Port)
			}
			auth = &applicationGitCloneAuth{originURL: origin, username: account.Username, token: token}
		} else {
			if account.KeySource == "TARGET" {
				h.fail(w, r, http.StatusBadRequest, "PROJECT_REPO_AUTH_UNSUPPORTED", "该 SSH 账号私钥仅保存在目标服务器，无法在控制器直接拉取；请改用 HTTPS 账号或公开仓库地址")
				return nil, false
			}
			key, err := h.store.secret(account.CredentialID, account.CredentialRevision, "SSH_KEY")
			if err != nil {
				h.fail(w, r, http.StatusConflict, "GIT_CREDENTIAL_UNAVAILABLE", "Git SSH 私钥不可用或已撤销")
				return nil, false
			}
			auth = &applicationGitCloneAuth{sshHost: account.SSHHost, sshPort: account.SSHPort, sshKey: key, hostPublicKey: account.HostPublicKey}
		}
	}
	defer auth.clear()

	select {
	case h.hostCheckSlots <- struct{}{}:
		defer func() { <-h.hostCheckSlots }()
	default:
		h.fail(w, r, http.StatusTooManyRequests, "PROJECT_INSPECT_BUSY", "源码识别并发较多，请稍后再试")
		return nil, false
	}

	tmpDir, err := os.MkdirTemp("", "sfops-repo-inspect-*")
	if err != nil {
		h.internal(w, r, err)
		return nil, false
	}
	defer os.RemoveAll(tmpDir)

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if code, msg, err := cloneApplicationProjectRepo(ctx, repoURL, branch, tmpDir, auth); err != nil {
		status := http.StatusBadGateway
		if code == "PROJECT_REPO_BRANCH_NOT_FOUND" || code == "PROJECT_REPO_AUTH_FAILED" {
			status = http.StatusBadRequest
		}
		h.fail(w, r, status, code, msg)
		return nil, false
	}
	items, err := inspectApplicationProjectSource(ctx, tmpDir, in.Directory)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "仓库已拉取但无法解析源码目录")
		return nil, false
	}
	return items, true
}

func defaultCloneApplicationProjectRepo(ctx context.Context, repoURL, branch, destDir string, auth *applicationGitCloneAuth) (string, string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "GIT_BINARY_MISSING", "控制器所在主机未安装 git，无法拉取远程仓库，请先安装 git 或改用手动添加模块", err
	}
	args := []string{
		"-c", "core.symlinks=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "protocol.file.allow=never",
		"-c", "credential.helper=",
		"clone", "--depth=1", "--no-tags", "--single-branch",
	}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", repoURL, destDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	if temp := os.Getenv("TEMP"); temp != "" {
		env = append(env, "TEMP="+temp)
	}
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		env = append(env, "SystemRoot="+systemRoot)
	}
	if auth != nil && len(auth.token) > 0 && auth.originURL != "" {
		basic := base64.StdEncoding.EncodeToString([]byte(auth.username + ":" + string(auth.token)))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+auth.originURL+"/.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	if auth != nil && len(auth.sshKey) > 0 {
		sshDir, err := os.MkdirTemp("", "sfops-git-ssh-*")
		if err != nil {
			return "PROJECT_REPO_UNREACHABLE", "准备 Git SSH 临时工作区失败", err
		}
		defer os.RemoveAll(sshDir)
		keyPath := filepath.Join(sshDir, "id_key")
		knownPath := filepath.Join(sshDir, "known_hosts")
		keyBody := auth.sshKey
		if len(keyBody) > 0 && keyBody[len(keyBody)-1] != '\n' {
			keyBody = append(append([]byte(nil), keyBody...), '\n')
		}
		if err := os.WriteFile(keyPath, keyBody, 0600); err != nil {
			return "PROJECT_REPO_UNREACHABLE", "准备 Git SSH 密钥文件失败", err
		}
		hostEntry := auth.sshHost
		if auth.sshPort > 0 && auth.sshPort != 22 {
			hostEntry = fmt.Sprintf("[%s]:%d", auth.sshHost, auth.sshPort)
		}
		if err := os.WriteFile(knownPath, []byte(hostEntry+" "+strings.TrimSpace(auth.hostPublicKey)+"\n"), 0600); err != nil {
			return "PROJECT_REPO_UNREACHABLE", "准备 Git 主机指纹失败", err
		}
		env = append(env, fmt.Sprintf(`GIT_SSH_COMMAND=ssh -i "%s" -o UserKnownHostsFile="%s" -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o BatchMode=yes`, filepath.ToSlash(keyPath), filepath.ToSlash(knownPath)))
	}
	cmd.Env = env
	var output limitedOutput
	output.limit = 4096
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "PROJECT_INSPECT_TIMEOUT", "拉取 Git 仓库超时（45秒），请检查网络或配置镜像仓库地址", err
		}
		text := strings.ToLower(output.String())
		switch {
		case strings.Contains(text, "authentication failed") || strings.Contains(text, "permission denied") || strings.Contains(text, "could not read username"):
			return "PROJECT_REPO_AUTH_FAILED", "Git 仓库认证失败，请检查绑定的 Git 账号与访问令牌权限", err
		case strings.Contains(text, "remote branch") && strings.Contains(text, "not found") || strings.Contains(text, "did not match any"):
			return "PROJECT_REPO_BRANCH_NOT_FOUND", "指定分支不存在，请核对仓库分支名称", err
		default:
			return "PROJECT_REPO_UNREACHABLE", "无法连接或浅克隆 Git 仓库，请检查仓库地址、网络连通性或账号配置", err
		}
	}
	return "", "", nil
}

// inspectApplicationProjectSource receives only a registered root or a fresh
// clone from inspectRemoteGitModules.
func inspectApplicationProjectSource(ctx context.Context, rootPath, selected string) ([]projectinspect.Module, error) {
	root, err := projectinspect.OpenCheckout(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return projectinspect.Scan(ctx, root, selected)
}
