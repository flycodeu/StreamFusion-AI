package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

const projectSourceRootsEnv = "SFOPS_PROJECT_SOURCE_ROOTS"

type applicationProjectSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	root string
}

type applicationEnvHint struct {
	Name         string `json:"name"`
	DefaultValue string `json:"defaultValue,omitempty"`
	Sensitive    bool   `json:"sensitive,omitempty"`
	Profile      string `json:"profile,omitempty"`
}

type applicationProxyHint struct {
	Location    string `json:"location"`
	StripPrefix bool   `json:"stripPrefix,omitempty"`
	TargetPort  int    `json:"targetPort,omitempty"`
}

type applicationConfigTemplate struct {
	Filename string `json:"filename"`
	Profile  string `json:"profile,omitempty"`
	Format   string `json:"format,omitempty"`
	Content  string `json:"content"`
}

type applicationProjectRecognition struct {
	Directory              string                      `json:"directory"`
	Name                   string                      `json:"name"`
	Kind                   string                      `json:"kind"`
	Environment            string                      `json:"environment"`
	StartCommand           string                      `json:"startCommand"`
	Evidence               []string                    `json:"evidence"`
	Dependencies           []string                    `json:"dependencies,omitempty"`
	BuildCommand           string                      `json:"buildCommand,omitempty"`
	TestCommand            string                      `json:"testCommand,omitempty"`
	ArtifactPath           string                      `json:"artifactPath,omitempty"`
	RecommendedRuntimeMode string                      `json:"recommendedRuntimeMode,omitempty"`
	DefaultProfile         string                      `json:"defaultProfile,omitempty"`
	Profiles               []string                    `json:"profiles,omitempty"`
	DefaultPort            int                         `json:"defaultPort,omitempty"`
	HealthPath             string                      `json:"healthPath,omitempty"`
	EnvHints               []applicationEnvHint        `json:"envHints,omitempty"`
	ProxyHints             []applicationProxyHint      `json:"proxyHints,omitempty"`
	ConfigTemplates        []applicationConfigTemplate `json:"configTemplates,omitempty"`
	SupportsContainer      bool                        `json:"supportsContainer,omitempty"`
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
		root, err := openApplicationProjectSource(rootPath)
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

var (
	validProjectSourceLabel = regexp.MustCompile(`^[\p{L}\p{N} ._-]{1,80}$`)
	validProjectDirectory   = regexp.MustCompile(`^[\p{L}\p{N} _.-]{1,80}$`)
	projectVersion          = regexp.MustCompile(`^[vV]?[0-9]{1,4}(?:\.(?:[0-9]{1,4}|[xX*])){0,3}$`)
	projectVersionRange     = regexp.MustCompile(`^[0-9<>=~^., |*xXvV-]{1,48}$`)
)

func openApplicationProjectSource(rootPath string) (*os.Root, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	git, err := root.Lstat(".git")
	if err != nil || !(git.IsDir() || git.Mode().IsRegular()) {
		root.Close()
		return nil, os.ErrInvalid
	}
	return root, nil
}

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

	if in.Directory != "" && in.Directory != "." && !applicationRelative(in.Directory) {
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

func (h *HTTP) inspectRemoteGitModules(w http.ResponseWriter, r *http.Request, in applicationProjectInspectRequest) ([]applicationProjectRecognition, bool) {
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

func inspectApplicationProjectSource(ctx context.Context, rootPath, selected string) ([]applicationProjectRecognition, error) {
	root, err := openApplicationProjectSource(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	items := []applicationProjectRecognition{}
	if selected == "" {
		return discoverApplicationProjectModules(ctx, root), ctx.Err()
	}
	if selected != "." {
		if path.Clean(selected) != selected {
			return nil, os.ErrInvalid
		}
		parts := strings.Split(selected, "/")
		if len(parts) > 8 {
			return nil, os.ErrInvalid
		}
		for index, name := range parts {
			if !projectVisibleDirectory(name) || !projectSourceFile(root, strings.Join(parts[:index+1], "/"), true) {
				return nil, os.ErrInvalid
			}
		}
	}
	if module, ok := recognizeApplicationProjectModule(root, selected); ok {
		items = append(items, module)
	}
	return items, ctx.Err()
}

func discoverApplicationProjectModules(ctx context.Context, root *os.Root) []applicationProjectRecognition {
	type directory struct {
		path  string
		depth int
	}
	queue := []directory{{path: "."}}
	items := []applicationProjectRecognition{}
	for i := 0; i < len(queue) && i < 160 && len(items) < 32 && ctx.Err() == nil; i++ {
		current := queue[i]
		if module, ok := recognizeApplicationProjectModule(root, current.path); ok {
			items = append(items, module)
		}
		if current.depth == 2 || len(queue) >= 160 {
			continue
		}
		file, err := root.Open(current.path)
		if err != nil {
			continue
		}
		entries, _ := file.ReadDir(256)
		file.Close()
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() && projectVisibleDirectory(entry.Name()) {
				queue = append(queue, directory{path.Join(current.path, entry.Name()), current.depth + 1})
				if len(queue) == 160 {
					break
				}
			}
		}
	}
	return items
}

func projectVisibleDirectory(name string) bool {
	if !validProjectDirectory.MatchString(name) || strings.HasPrefix(name, ".") {
		return false
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "target", "build", "dist", "venv", "state", "coverage", "__pycache__":
		return false
	}
	return true
}

func projectSourceFile(root *os.Root, name string, directory bool) bool {
	info, err := root.Lstat(name)
	return err == nil && (directory && info.IsDir() || !directory && info.Mode().IsRegular())
}

func projectSourceText(root *os.Root, name string) []byte {
	const maxSize = 128 << 10
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil
	}
	f, err := root.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil
	}
	content, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || len(content) > maxSize {
		return nil
	}
	return content
}

func recognizeApplicationProjectModule(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	for _, recognize := range []func(*os.Root, string) (applicationProjectRecognition, bool){
		recognizeProjectJava, recognizeProjectNode, recognizeProjectPython,
	} {
		if module, ok := recognize(root, directory); ok {
			module.Directory = directory
			if directory != "." {
				module.Name = path.Base(directory)
			}
			return module, true
		}
	}
	return applicationProjectRecognition{}, false
}

type projectMavenArtifact struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

func appendUniqueStrings(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, existing := range dst {
		seen[strings.ToLower(existing)] = true
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[strings.ToLower(item)] {
			continue
		}
		seen[strings.ToLower(item)] = true
		dst = append(dst, item)
	}
	return dst
}

var (
	springConfigFilePattern  = regexp.MustCompile(`^application(?:-([a-zA-Z0-9_-]{1,40}))?\.(yml|yaml|properties)$`)
	springEnvExprPattern     = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]{1,63})(?::([^}]*))?\}`)
	springPropPortPattern    = regexp.MustCompile(`^server\.port\s*[:=]\s*(.+)$`)
	springPropDefaultProfile = regexp.MustCompile(`^spring\.profiles\.default\s*[:=]\s*([a-zA-Z0-9_-]{1,40})\b`)
	viteOutDirPattern        = regexp.MustCompile(`outDir\s*:\s*['"]([a-zA-Z0-9_./-]{1,64})['"]`)
	viteProxyKeyPattern      = regexp.MustCompile(`['"]\^?(/[a-zA-Z0-9_-]+)(?:\(\?:[^'"]*)?['"]\s*:`)
	viteLocalTargetPort      = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost):([0-9]{2,5})`)
)

func parsePortSpec(raw string) int {
	raw = strings.TrimSpace(raw)
	if match := springEnvExprPattern.FindStringSubmatch(raw); len(match) == 3 {
		raw = strings.TrimSpace(match[2])
	}
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return 0
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

type springConfigDetails struct {
	profiles       []string
	defaultProfile string
	defaultPort    int
	envHints       []applicationEnvHint
	templates      []applicationConfigTemplate
	evidence       []string
}

var (
	privateKeyBlockPattern   = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	inlineSecretTokenPattern = regexp.MustCompile(`\b(ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{20,})\b`)
)

func sanitizeConfigTemplateContent(raw string) string {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	text = privateKeyBlockPattern.ReplaceAllString(text, "-----BEGIN PRIVATE KEY-----\n# [REDACTED]\n-----END PRIVATE KEY-----")
	text = inlineSecretTokenPattern.ReplaceAllString(text, "${SFOPS_CRED:token}")
	return text
}

func collectDirectoryConfigTemplates(root *os.Root, directory string, filenames []string) []applicationConfigTemplate {
	out := make([]applicationConfigTemplate, 0, len(filenames))
	for _, name := range filenames {
		if len(out) >= 4 {
			break
		}
		rel := path.Join(directory, name)
		raw := projectSourceText(root, rel)
		if len(raw) == 0 || len(raw) > 16<<10 {
			continue
		}
		format := "env"
		if ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), "."); ext == "yml" || ext == "yaml" || ext == "json" || ext == "properties" {
			format = ext
		}
		profile := ""
		if strings.HasPrefix(name, ".env.") && name != ".env.example" {
			profile = strings.TrimPrefix(name, ".env.")
		}
		out = append(out, applicationConfigTemplate{
			Filename: name,
			Profile:  profile,
			Format:   format,
			Content:  sanitizeConfigTemplateContent(string(raw)),
		})
	}
	return out
}

func inspectSpringConfigFiles(root *os.Root, directory string) springConfigDetails {
	var out springConfigDetails
	resDir := path.Join(directory, "src/main/resources")
	dirFile, err := root.Open(resDir)
	if err != nil {
		return out
	}
	entries, _ := dirFile.ReadDir(128)
	dirFile.Close()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	type configEntry struct {
		relPath string
		profile string
		ext     string
		body    string
	}
	var configs []configEntry
	profileSet := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || len(configs) >= 12 {
			continue
		}
		match := springConfigFilePattern.FindStringSubmatch(entry.Name())
		if len(match) == 0 {
			continue
		}
		rel := path.Join(resDir, entry.Name())
		raw := projectSourceText(root, rel)
		if len(raw) == 0 {
			continue
		}
		profile := match[1]
		if profile != "" {
			profileSet[profile] = true
		}
		configs = append(configs, configEntry{
			relPath: rel,
			profile: profile,
			ext:     match[2],
			body:    strings.ReplaceAll(string(raw), "\r\n", "\n"),
		})
		out.evidence = append(out.evidence, rel)
	}
	for profile := range profileSet {
		out.profiles = append(out.profiles, profile)
	}
	sort.Strings(out.profiles)

	// Extract profile-specific files first (test > prod > dev > others > base) so test/prod defaults win over common fallback.
	profilePriority := func(p string) int {
		switch strings.ToLower(p) {
		case "test":
			return 0
		case "prod":
			return 1
		case "dev":
			return 2
		case "":
			return 4
		default:
			return 3
		}
	}
	sortedConfigs := append([]configEntry(nil), configs...)
	sort.SliceStable(sortedConfigs, func(i, j int) bool {
		pi, pj := profilePriority(sortedConfigs[i].profile), profilePriority(sortedConfigs[j].profile)
		if pi != pj {
			return pi < pj
		}
		return sortedConfigs[i].relPath < sortedConfigs[j].relPath
	})

	hintIndex := map[string]int{}
	for _, cfg := range sortedConfigs {
		if len(out.templates) < 8 && len(cfg.body) <= 32<<10 {
			out.templates = append(out.templates, applicationConfigTemplate{
				Filename: path.Base(cfg.relPath),
				Profile:  cfg.profile,
				Format:   strings.ToLower(cfg.ext),
				Content:  sanitizeConfigTemplateContent(cfg.body),
			})
		}
		inServer, serverIndent := false, -1
		inSpring, springIndent := false, -1
		inProfiles, profilesIndent := false, -1
		for line := range strings.SplitSeq(cfg.body, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if cfg.ext == "properties" {
				if m := springPropDefaultProfile.FindStringSubmatch(trimmed); len(m) == 2 && out.defaultProfile == "" {
					out.defaultProfile = m[1]
				}
				if m := springPropPortPattern.FindStringSubmatch(trimmed); len(m) == 2 && (out.defaultPort == 0 || cfg.profile == "test") {
					if port := parsePortSpec(m[1]); port > 0 {
						out.defaultPort = port
					}
				}
			} else {
				if indent <= serverIndent {
					inServer = false
				}
				if indent <= profilesIndent {
					inProfiles = false
				}
				if indent <= springIndent {
					inSpring, inProfiles = false, false
				}
				if key, val, ok := strings.Cut(trimmed, ":"); ok && !strings.HasPrefix(trimmed, "-") {
					k := strings.TrimSpace(key)
					v := strings.TrimSpace(strings.Split(val, " #")[0])
					switch {
					case indent == 0 && k == "server":
						inServer, serverIndent = true, indent
					case indent == 0 && k == "spring":
						inSpring, springIndent = true, indent
					case inSpring && indent > springIndent && k == "profiles":
						inProfiles, profilesIndent = true, indent
					case inProfiles && indent > profilesIndent && k == "default" && out.defaultProfile == "":
						cand := strings.Trim(v, `"'`)
						if profilePatternSafe(cand) {
							out.defaultProfile = cand
						}
					case inServer && indent > serverIndent && k == "port" && out.defaultPort == 0:
						if port := parsePortSpec(v); port > 0 {
							out.defaultPort = port
						}
					}
				}
			}
			for _, m := range springEnvExprPattern.FindAllStringSubmatch(line, 16) {
				name := m[1]
				defVal := strings.TrimSpace(m[2])
				sensitive := applicationSensitiveName.MatchString(name)
				if sensitive || applicationSecretValue(defVal) || len(defVal) > 180 || strings.Contains(defVal, "${") || strings.ContainsAny(defVal, "\r\n\x00") {
					defVal = ""
				}
				if idx, exists := hintIndex[name]; exists {
					if out.envHints[idx].DefaultValue == "" && defVal != "" {
						out.envHints[idx].DefaultValue = defVal
					}
					if out.envHints[idx].Profile == "" && cfg.profile != "" {
						out.envHints[idx].Profile = cfg.profile
					}
					out.envHints[idx].Sensitive = out.envHints[idx].Sensitive || sensitive
					continue
				}
				if len(out.envHints) >= 40 {
					continue
				}
				hintIndex[name] = len(out.envHints)
				out.envHints = append(out.envHints, applicationEnvHint{
					Name:         name,
					DefaultValue: defVal,
					Sensitive:    sensitive,
					Profile:      cfg.profile,
				})
			}
		}
	}
	if out.defaultProfile == "" && slices.Contains(out.profiles, "test") {
		out.defaultProfile = "test"
	} else if out.defaultProfile == "" && len(out.profiles) > 0 {
		out.defaultProfile = out.profiles[0]
	}
	return out
}

func profilePatternSafe(value string) bool {
	if value == "" || len(value) > 40 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func recognizeProjectJava(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	file := path.Join(directory, "pom.xml")
	var pom struct {
		XMLName      xml.Name               `xml:"project"`
		ArtifactID   string                 `xml:"artifactId"`
		Packaging    string                 `xml:"packaging"`
		Parent       projectMavenArtifact   `xml:"parent"`
		Plugins      []projectMavenArtifact `xml:"build>plugins>plugin"`
		Dependencies []projectMavenArtifact `xml:"dependencies>dependency"`
		Properties   struct {
			Java    string `xml:"java.version"`
			Release string `xml:"maven.compiler.release"`
			Target  string `xml:"maven.compiler.target"`
		} `xml:"properties"`
	}
	if xml.Unmarshal(projectSourceText(root, file), &pom) != nil || strings.TrimSpace(pom.ArtifactID) == "" || pom.Packaging == "pom" {
		return applicationProjectRecognition{}, false
	}
	module := applicationProjectRecognition{
		Name:                   "Java 后端",
		Kind:                   "BACKEND",
		Evidence:               []string{file},
		RecommendedRuntimeMode: "NATIVE",
	}
	environment := []string{"Java"}
	deps := []string{}
	javaLabel := "Java"
	for _, version := range []string{pom.Properties.Release, pom.Properties.Java, pom.Properties.Target} {
		if version = strings.TrimSpace(version); projectVersion.MatchString(version) {
			environment[0] = "JDK " + version
			javaLabel = "Java " + version
			break
		}
	}
	deps = append(deps, javaLabel)
	bootParent := pom.Parent.GroupID == "org.springframework.boot" && pom.Parent.ArtifactID == "spring-boot-starter-parent"
	bootPlugin := false
	for _, plugin := range pom.Plugins {
		bootPlugin = bootPlugin || plugin.GroupID == "org.springframework.boot" && plugin.ArtifactID == "spring-boot-maven-plugin"
	}
	if bootParent || bootPlugin {
		boot := "Spring Boot"
		if bootParent && projectVersion.MatchString(pom.Parent.Version) {
			boot += " " + pom.Parent.Version
		}
		environment = append(environment, boot)
	}
	maven, command := "Maven", "mvn"
	if name := path.Join(directory, "mvnw"); projectSourceFile(root, name, false) {
		maven, command = "Maven Wrapper", "sh mvnw"
		module.Evidence = append(module.Evidence, name)
	} else if name := path.Join(directory, "mvnw.cmd"); projectSourceFile(root, name, false) {
		maven, command = "Maven Wrapper", ".\\mvnw.cmd"
		module.Evidence = append(module.Evidence, name)
	}
	environment = append(environment, maven)
	deps = appendUniqueStrings(deps, "Maven")
	module.TestCommand = command + " -B -ntp test"
	module.BuildCommand = command + " -B -ntp -DskipTests package"
	module.ArtifactPath = "target/*.jar"
	if bootPlugin || bootParent {
		module.StartCommand = command + " spring-boot:run"
		if evidence := projectDocumentedCommand(root, directory, module.StartCommand+" -Dspring-boot.run.profiles=local"); evidence != "" {
			module.StartCommand += " -Dspring-boot.run.profiles=local"
			module.Evidence = append(module.Evidence, evidence)
		}
	}
	var services []string
	var extraDeps []string
	hasActuator := false
	for _, dep := range pom.Dependencies {
		coord := dep.GroupID + ":" + dep.ArtifactID
		switch coord {
		case "com.mysql:mysql-connector-j", "mysql:mysql-connector-java":
			services = append(services, "MySQL")
		case "org.springframework.boot:spring-boot-starter-data-redis", "io.lettuce:lettuce-core", "redis.clients:jedis":
			services = append(services, "Redis")
		case "org.postgresql:postgresql":
			extraDeps = append(extraDeps, "PostgreSQL")
		case "software.amazon.awssdk:s3", "io.minio:minio":
			extraDeps = append(extraDeps, "SeaweedFS")
		case "org.springframework.boot:spring-boot-starter-amqp":
			extraDeps = append(extraDeps, "RabbitMQ")
		case "org.springframework.kafka:spring-kafka":
			extraDeps = append(extraDeps, "Kafka")
		case "org.springframework.boot:spring-boot-starter-data-mongodb":
			extraDeps = append(extraDeps, "MongoDB")
		case "org.springframework.boot:spring-boot-starter-actuator":
			hasActuator = true
		}
	}
	if len(services) > 0 {
		environment = append(environment, "运行依赖线索："+strings.Join(services, "、"))
	}
	deps = appendUniqueStrings(deps, append(services, extraDeps...)...)
	module.Dependencies = deps
	module.Environment = strings.Join(environment, "、")
	if bootParent || bootPlugin {
		spring := inspectSpringConfigFiles(root, directory)
		module.Profiles = spring.profiles
		module.DefaultProfile = spring.defaultProfile
		module.DefaultPort = spring.defaultPort
		if module.DefaultPort == 0 {
			module.DefaultPort = 8080
		}
		module.EnvHints = spring.envHints
		module.ConfigTemplates = spring.templates
		if len(spring.evidence) > 0 {
			module.Evidence = append(module.Evidence, spring.evidence...)
		}
		if hasActuator {
			module.HealthPath = "/actuator/health"
		}
	}
	if projectSourceFile(root, path.Join(directory, "Dockerfile"), false) {
		module.SupportsContainer = true
	}
	return module, true
}

func recognizeProjectNode(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	file := path.Join(directory, "package.json")
	var pkg struct {
		PackageManager  string                     `json:"packageManager"`
		Engines         map[string]string          `json:"engines"`
		Scripts         map[string]string          `json:"scripts"`
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if json.Unmarshal(projectSourceText(root, file), &pkg) != nil || len(pkg.Scripts) == 0 && len(pkg.Dependencies) == 0 && len(pkg.DevDependencies) == 0 {
		return applicationProjectRecognition{}, false
	}
	module := applicationProjectRecognition{
		Name:                   "Node.js 模块",
		Kind:                   "OTHER",
		Evidence:               []string{file},
		RecommendedRuntimeMode: "NATIVE",
	}
	var environment []string
	var deps []string
	for _, entry := range []struct{ dependency, label string }{{"vue", "Vue"}, {"vite", "Vite"}, {"react", "React"}} {
		if pkg.Dependencies[entry.dependency] != nil || pkg.DevDependencies[entry.dependency] != nil {
			environment = append(environment, entry.label)
			deps = appendUniqueStrings(deps, entry.label)
			module.Kind = "FRONTEND"
			module.Name = "前端"
		}
	}
	node := "Node.js"
	if version := strings.TrimSpace(pkg.Engines["node"]); projectVersionRange.MatchString(version) {
		node += " " + version
	}
	nvm := path.Join(directory, ".nvmrc")
	if version := strings.TrimSpace(string(projectSourceText(root, nvm))); projectVersion.MatchString(version) {
		node += "（.nvmrc " + version + "）"
		module.Evidence = append(module.Evidence, nvm)
	}
	environment = append(environment, node)
	deps = appendUniqueStrings(deps, "Node.js")
	manager, version, _ := strings.Cut(pkg.PackageManager, "@")
	version, _, _ = strings.Cut(version, "+")
	if manager != "pnpm" && manager != "npm" && manager != "yarn" {
		manager, version = "npm", ""
		for _, lock := range []struct{ name, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"package-lock.json", "npm"}} {
			if filename := path.Join(directory, lock.name); projectSourceFile(root, filename, false) {
				manager = lock.manager
				module.Evidence = append(module.Evidence, filename)
				break
			}
		}
	}
	tool := manager
	if projectVersion.MatchString(version) {
		tool += " " + version
	}
	environment = append(environment, tool)
	deps = appendUniqueStrings(deps, manager)
	for _, script := range []string{"dev", "start"} {
		if strings.TrimSpace(pkg.Scripts[script]) != "" {
			module.StartCommand = manager + " " + script
			if manager == "npm" && script == "dev" {
				module.StartCommand = "npm run dev"
			}
			break
		}
	}
	if strings.TrimSpace(pkg.Scripts["build"]) != "" {
		if manager == "yarn" {
			module.BuildCommand = "yarn build"
		} else {
			module.BuildCommand = manager + " run build"
		}
	}
	if strings.TrimSpace(pkg.Scripts["test"]) != "" {
		if manager == "pnpm" {
			module.TestCommand = "pnpm run test"
		} else {
			module.TestCommand = manager + " test"
		}
	}
	if module.Kind == "FRONTEND" {
		module.RecommendedRuntimeMode = "STATIC"
		module.ArtifactPath = "dist"
		deps = appendUniqueStrings(deps, "Nginx")
		for _, viteName := range []string{"vite.config.ts", "vite.config.mts", "vite.config.js", "vite.config.mjs"} {
			vitePath := path.Join(directory, viteName)
			viteText := string(projectSourceText(root, vitePath))
			if viteText == "" {
				continue
			}
			module.Evidence = append(module.Evidence, vitePath)
			if match := viteOutDirPattern.FindStringSubmatch(viteText); len(match) == 2 && applicationRelative(match[1]) {
				module.ArtifactPath = match[1]
			}
			lines := strings.Split(strings.ReplaceAll(viteText, "\r\n", "\n"), "\n")
			seenLoc := map[string]bool{}
			for i, line := range lines {
				m := viteProxyKeyPattern.FindStringSubmatch(line)
				if len(m) != 2 || seenLoc[m[1]] || len(module.ProxyHints) >= 8 {
					continue
				}
				seenLoc[m[1]] = true
				hint := applicationProxyHint{Location: m[1]}
				end := i + 20
				if end > len(lines) {
					end = len(lines)
				}
				block := strings.Join(lines[i:end], "\n")
				if strings.Contains(block, "rewrite") {
					hint.StripPrefix = true
				}
				if pm := viteLocalTargetPort.FindStringSubmatch(block); len(pm) == 2 {
					if port, convErr := strconv.Atoi(pm[1]); convErr == nil && port >= 1 && port <= 65535 {
						hint.TargetPort = port
					}
				}
				module.ProxyHints = append(module.ProxyHints, hint)
			}
			break
		}
	}
	if projectSourceFile(root, path.Join(directory, "Dockerfile"), false) {
		module.SupportsContainer = true
	}
	module.ConfigTemplates = collectDirectoryConfigTemplates(root, directory, []string{".env.production", ".env.test", ".env.development", ".env.local", ".env", ".env.example"})
	module.Dependencies = deps
	module.Environment = strings.Join(environment, "、")
	return module, true
}

var projectPyTable = regexp.MustCompile(`\A\[([a-zA-Z0-9_.-]+)\][ \t]*(?:#.*)?\z`)
var projectPyValue = regexp.MustCompile(`\A([a-z-]+)[ \t]*=[ \t]*["']([^"']*)["'][ \t]*(?:#.*)?\z`)

func recognizeProjectPython(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	file := path.Join(directory, "pyproject.toml")
	var table, version string
	project := false
	for line := range strings.SplitSeq(string(projectSourceText(root, file)), "\n") {
		line = strings.TrimSpace(line)
		if match := projectPyTable.FindStringSubmatch(line); len(match) != 0 {
			table = match[1]
			project = project || table == "project"
		} else if strings.HasPrefix(line, "[") {
			table = ""
		}
		if table == "project" {
			if match := projectPyValue.FindStringSubmatch(line); len(match) != 0 && match[1] == "requires-python" && projectVersionRange.MatchString(match[2]) {
				version = match[2]
			}
		}
	}
	if !project {
		return applicationProjectRecognition{}, false
	}
	module := applicationProjectRecognition{
		Name:                   "Python 模块",
		Kind:                   "OTHER",
		Environment:            "Python",
		Evidence:               []string{file},
		RecommendedRuntimeMode: "NATIVE",
		ArtifactPath:           ".",
		Dependencies:           []string{"Python"},
	}
	for segment := range strings.SplitSeq(strings.ToLower(directory), "/") {
		if strings.HasPrefix(segment, "algorithm") || strings.HasPrefix(segment, "algo-") || segment == "node-agent" || segment == "inference" {
			module.Kind = "ALGORITHM"
			module.Name = "算法模块"
			break
		}
	}
	if version != "" {
		module.Environment += " " + version
	}
	entry := path.Join(directory, "app/__main__.py")
	runnable := projectSourceFile(root, path.Join(directory, "app"), true) && projectSourceFile(root, entry, false)
	if lock := path.Join(directory, "uv.lock"); projectSourceFile(root, lock, false) {
		module.Environment += "、uv"
		module.Dependencies = appendUniqueStrings(module.Dependencies, "uv")
		module.Evidence = append(module.Evidence, lock)
		module.BuildCommand = "uv sync --locked"
		module.TestCommand = "uv run --locked python -m pytest -q"
		if runnable {
			module.StartCommand = "uv run --locked python -m app"
		}
	} else if runnable {
		module.StartCommand = "python -m app"
	}
	if runnable {
		module.Evidence = append(module.Evidence, entry)
		if readme := projectDocumentedCommand(root, directory, module.StartCommand); readme != "" {
			module.Evidence = append(module.Evidence, readme)
		}
	}
	if projectSourceFile(root, path.Join(directory, "Dockerfile"), false) {
		module.SupportsContainer = true
	}
	module.ConfigTemplates = collectDirectoryConfigTemplates(root, directory, []string{"config.yaml", "config.yml", ".env", ".env.example"})
	return module, true
}

// Only fixed, detector-generated commands can be matched. README text is never returned or run.
func projectDocumentedCommand(root *os.Root, directory, command string) string {
	if command == "" {
		return ""
	}
	for _, dir := range []string{directory, path.Dir(directory), "."} {
		readme := path.Join(dir, "README.md")
		previous := ""
		for line := range strings.SplitSeq(string(projectSourceText(root, readme)), "\n") {
			ownsCommand := dir == directory || strings.Contains(line, path.Base(directory)) || strings.Contains(previous, path.Base(directory))
			matches := strings.TrimSpace(line) == command || strings.Contains(line, "`"+command+"`")
			if ownsCommand && matches {
				return readme
			}
			previous = line
		}
	}
	return ""
}
