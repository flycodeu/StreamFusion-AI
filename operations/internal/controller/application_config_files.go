package controller

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/projectconfig"
)

func (h *HTTP) applicationModuleConfigFilesRoute(w http.ResponseWriter, r *http.Request, actor *session, projectID, moduleID string, rest []string) bool {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		h.listApplicationConfigFiles(w, r, projectID, moduleID)
		return true
	case len(rest) == 1 && r.Method == http.MethodGet:
		filename, err := url.PathUnescape(rest[0])
		if err != nil {
			h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "配置文件名格式不正确")
			return true
		}
		h.readApplicationConfigFile(w, r, projectID, moduleID, filename)
		return true
	case len(rest) == 1 && r.Method == http.MethodPut:
		filename, err := url.PathUnescape(rest[0])
		if err != nil {
			h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "配置文件名格式不正确")
			return true
		}
		h.saveApplicationConfigFile(w, r, actor, projectID, moduleID, filename)
		return true
	case len(rest) == 1 && r.Method == http.MethodDelete:
		filename, err := url.PathUnescape(rest[0])
		if err != nil {
			h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "配置文件名格式不正确")
			return true
		}
		h.deleteApplicationConfigFile(w, r, actor, projectID, moduleID, filename)
		return true
	default:
		return false
	}
}

func (h *HTTP) loadApplicationProjectAndModule(ctx context.Context, projectID, moduleID string) (applicationProject, applicationModule, error) {
	tx, err := h.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return applicationProject{}, applicationModule{}, err
	}
	defer tx.Rollback()
	project, err := readApplicationProject(tx, projectID)
	if err != nil {
		return applicationProject{}, applicationModule{}, err
	}
	for _, m := range project.Modules {
		if m.ID == moduleID {
			return project, m, nil
		}
	}
	return project, applicationModule{}, sql.ErrNoRows
}

func sanitizeSlugPart(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 20 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func applicationConfigSlug(project applicationProject, module applicationModule, env string) string {
	pSlug := sanitizeSlugPart(project.Code)
	if pSlug == "" {
		pSlug = sanitizeSlugPart(project.Name)
	}
	mSlug := sanitizeSlugPart(module.Name)
	eSlug := sanitizeSlugPart(env)
	if eSlug == "" {
		eSlug = "default"
	}
	parts := make([]string, 0, 4)
	if pSlug != "" {
		parts = append(parts, pSlug)
	}
	if mSlug != "" {
		parts = append(parts, mSlug)
	}
	parts = append(parts, eSlug)
	cleanID := strings.ReplaceAll(module.ID, "-", "")
	if len(cleanID) >= 6 {
		parts = append(parts, strings.ToLower(cleanID[:6]))
	}
	slug := strings.Join(parts, "-")
	if len(slug) > 64 {
		slug = strings.TrimRight(slug[:64], "-")
	}
	if !deploy.ValidAppConfigSlug(slug) {
		slug = "app-" + strings.ToLower(cleanID[:min(12, len(cleanID))])
	}
	return slug
}

func appConfigFailure(err error) (int, string, string) {
	code := executorFailureCode(err)
	if code == "EXECUTOR_UPGRADE_REQUIRED" || code == "EXECUTOR_RESPONSE_INVALID" {
		return http.StatusConflict, "APP_CONFIG_UPGRADE_REQUIRED", "目标服务器接入组件版本较旧，请前往「服务器管理」点击「更新接入组件」后重试"
	}
	if msg, ok := configCapabilityMessages[code]; ok {
		return http.StatusConflict, code, msg
	}
	if msg, ok := executorFailureMessages[code]; ok {
		return http.StatusConflict, code, msg
	}
	return http.StatusConflict, code, "读取或同步服务器配置文件失败，请核对目标主机连接与接入组件"
}

func (s *Store) resolveAppConfigCredentialRefs(content string) (map[string]string, []string, []string) {
	refs := deploy.ExtractAppConfigCredentialRefs(content)
	if len(refs) == 0 {
		return nil, nil, nil
	}
	resolved := make(map[string]string, len(refs))
	var missing []string
	for _, ref := range refs {
		var id string
		var rev int
		if credID, credRev, ok := strings.Cut(ref, ":"); ok && deploy.ID.MatchString(credID) {
			if err := s.DB.QueryRow(
				"SELECT id,revision FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND revoked_at IS NULL",
				credID, credRev,
			).Scan(&id, &rev); err == nil {
				resolved[ref] = fmt.Sprintf("%s:%d", id, rev)
				continue
			}
		}
		if deploy.ID.MatchString(ref) {
			if err := s.DB.QueryRow(
				"SELECT id,revision FROM credential_refs WHERE id=? AND revoked_at IS NULL ORDER BY revision DESC LIMIT 1",
				ref,
			).Scan(&id, &rev); err == nil {
				resolved[ref] = fmt.Sprintf("%s:%d", id, rev)
				continue
			}
		}
		if err := s.DB.QueryRow(
			"SELECT id,revision FROM credential_refs WHERE name=? AND revoked_at IS NULL ORDER BY CASE kind WHEN 'PASSWORD' THEN 0 WHEN 'TOKEN' THEN 1 ELSE 2 END, revision DESC LIMIT 1",
			ref,
		).Scan(&id, &rev); err == nil {
			resolved[ref] = fmt.Sprintf("%s:%d", id, rev)
			continue
		}
		missing = append(missing, ref)
	}
	return resolved, refs, missing
}

func (h *HTTP) pullApplicationModuleConfigTemplates(w http.ResponseWriter, r *http.Request, projectID, moduleID string) {
	project, module, err := h.loadApplicationProjectAndModule(r.Context(), projectID, moduleID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if len(project.Repositories) == 0 {
		if module.Catalog != nil && len(module.Catalog.ConfigTemplates) > 0 {
			h.success(w, r, map[string]any{
				"templates":      module.Catalog.ConfigTemplates,
				"profiles":       module.Catalog.Profiles,
				"defaultProfile": module.Catalog.DefaultProfile,
				"defaultPort":    module.Catalog.DefaultPort,
				"envHints":       module.Catalog.EnvHints,
			})
			return
		}
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "当前项目未登记 Git 仓库地址，无法从仓库拉取配置模板")
		return
	}
	dir := strings.TrimSpace(module.Directory)
	if dir == "" {
		dir = "."
	}
	items, ok := h.inspectRemoteGitModules(w, r, applicationProjectInspectRequest{
		ProjectID:    projectID,
		RepositoryID: module.RepositoryID,
		Directory:    dir,
	})
	if !ok {
		return
	}
	if len(items) == 0 {
		if module.Catalog != nil && len(module.Catalog.ConfigTemplates) > 0 {
			h.success(w, r, map[string]any{
				"templates":      module.Catalog.ConfigTemplates,
				"profiles":       module.Catalog.Profiles,
				"defaultProfile": module.Catalog.DefaultProfile,
				"defaultPort":    module.Catalog.DefaultPort,
				"envHints":       module.Catalog.EnvHints,
			})
			return
		}
		h.success(w, r, map[string]any{
			"templates": []projectconfig.ConfigTemplate{},
		})
		return
	}
	recognized := items[0]
	h.success(w, r, map[string]any{
		"templates":      recognized.ConfigTemplates,
		"profiles":       recognized.Profiles,
		"defaultProfile": recognized.DefaultProfile,
		"defaultPort":    recognized.DefaultPort,
		"envHints":       recognized.EnvHints,
	})
}

func (h *HTTP) resolveAppConfigHost(w http.ResponseWriter, r *http.Request, hostID, environment string, project applicationProject, module applicationModule) (target, string, bool) {
	hostID = strings.TrimSpace(hostID)
	environment = strings.TrimSpace(environment)
	if !deploy.ID.MatchString(hostID) {
		h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "请先选择运行服务器")
		return target{}, "", false
	}
	if environment == "" || !validName(environment) {
		h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "请先选择部署环境")
		return target{}, "", false
	}
	host, err := h.store.readTarget(hostID)
	if err != nil {
		h.opsError(w, r, err)
		return target{}, "", false
	}
	slug := applicationConfigSlug(project, module, environment)
	return host, slug, true
}

func (h *HTTP) listApplicationConfigFiles(w http.ResponseWriter, r *http.Request, projectID, moduleID string) {
	project, module, err := h.loadApplicationProjectAndModule(r.Context(), projectID, moduleID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	host, slug, ok := h.resolveAppConfigHost(w, r, r.URL.Query().Get("hostId"), r.URL.Query().Get("environment"), project, module)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.AppConfigResult
	err = h.store.callTarget(ctx, host, deploy.Request{
		Action:    deploy.AppConfigListAction,
		AppConfig: &deploy.AppConfigRequest{AppSlug: slug},
		TraceID:   r.Context().Value(traceKey{}).(string),
	}, "", &result)
	if err != nil {
		status, code, msg := appConfigFailure(err)
		h.fail(w, r, status, code, msg)
		return
	}
	h.success(w, r, map[string]any{
		"appSlug":   slug,
		"configDir": result.ConfigDir,
		"files":     result.Files,
	})
}

func (h *HTTP) readApplicationConfigFile(w http.ResponseWriter, r *http.Request, projectID, moduleID, filename string) {
	if !deploy.ValidAppConfigFilename(filename) {
		h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "配置文件名仅支持字母、数字、点、下划线和连字符")
		return
	}
	project, module, err := h.loadApplicationProjectAndModule(r.Context(), projectID, moduleID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	host, slug, ok := h.resolveAppConfigHost(w, r, r.URL.Query().Get("hostId"), r.URL.Query().Get("environment"), project, module)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.AppConfigResult
	err = h.store.callTarget(ctx, host, deploy.Request{
		Action:    deploy.AppConfigReadAction,
		AppConfig: &deploy.AppConfigRequest{AppSlug: slug, Filename: filename},
		TraceID:   r.Context().Value(traceKey{}).(string),
	}, "", &result)
	if err != nil {
		status, code, msg := appConfigFailure(err)
		h.fail(w, r, status, code, msg)
		return
	}
	h.success(w, r, result)
}

func (h *HTTP) saveApplicationConfigFile(w http.ResponseWriter, r *http.Request, actor *session, projectID, moduleID, filename string) {
	if !deploy.ValidAppConfigFilename(filename) {
		h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "配置文件名仅支持字母、数字、点、下划线和连字符")
		return
	}
	var in struct {
		HostID           string `json:"hostId"`
		Environment      string `json:"environment"`
		Content          string `json:"content"`
		ExpectedRevision string `json:"expectedRevision"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, http.StatusBadRequest, "INVALID_INPUT", err.Error())
		return
	}
	project, module, err := h.loadApplicationProjectAndModule(r.Context(), projectID, moduleID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	host, slug, ok := h.resolveAppConfigHost(w, r, in.HostID, in.Environment, project, module)
	if !ok {
		return
	}
	if host.Maintenance {
		h.fail(w, r, http.StatusConflict, "HOST_MAINTENANCE", "目标服务器处于维护模式，暂不允许写入配置文件")
		return
	}
	normalized := deploy.NormalizeConfigText(in.Content)
	if line, err := deploy.ValidateAppConfigContent(filename, normalized); err != nil {
		code := err.Error()
		msg := configCapabilityMessages[code]
		if line > 0 && msg != "" {
			msg = fmt.Sprintf("%s（第 %d 行）", msg, line)
		}
		h.reply(w, r, http.StatusBadRequest, code, msg, map[string]int{"line": line})
		return
	}
	credRefs, credNames, missing := h.store.resolveAppConfigCredentialRefs(normalized)
	if len(missing) > 0 {
		h.reply(w, r, http.StatusBadRequest, "CREDENTIAL_NOT_FOUND",
			fmt.Sprintf("凭据库中找不到以下有效凭据：%s", strings.Join(missing, "、")),
			map[string][]string{"missing": missing},
		)
		return
	}
	sha := deploy.ContentSHA256Hex(normalized)
	envelope, err := seal(h.store.Key, "app-config/"+slug+"/"+filename, 1, []byte(normalized))
	if err != nil {
		h.internal(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.AppConfigResult
	err = h.store.callTarget(ctx, host, deploy.Request{
		Action: deploy.AppConfigWriteAction,
		AppConfig: &deploy.AppConfigRequest{
			AppSlug:          slug,
			Filename:         filename,
			ExpectedRevision: strings.TrimSpace(in.ExpectedRevision),
			ContentSHA256:    sha,
			ContentEnvelope:  envelope,
			CredentialRefs:   credRefs,
		},
		TraceID: r.Context().Value(traceKey{}).(string),
	}, "", &result)
	if err != nil {
		status, code, msg := appConfigFailure(err)
		h.fail(w, r, status, code, msg)
		return
	}
	h.recordAudit(actor, "APPLICATION_CONFIG_FILE_SAVED", moduleID+":"+slug+"/"+filename, "SUCCESS", r.Context().Value(traceKey{}).(string))
	h.success(w, r, map[string]any{
		"configDir":       result.ConfigDir,
		"path":            result.Path,
		"filename":        result.Filename,
		"exists":          result.Exists,
		"content":         result.Content,
		"revision":        result.Revision,
		"credentialNames": credNames,
		"message":         result.Message,
	})
}

func (h *HTTP) deleteApplicationConfigFile(w http.ResponseWriter, r *http.Request, actor *session, projectID, moduleID, filename string) {
	if !deploy.ValidAppConfigFilename(filename) {
		h.fail(w, r, http.StatusBadRequest, "APP_CONFIG_INPUT_INVALID", "配置文件名格式不正确")
		return
	}
	project, module, err := h.loadApplicationProjectAndModule(r.Context(), projectID, moduleID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	hostID := r.URL.Query().Get("hostId")
	environment := r.URL.Query().Get("environment")
	expectedRev := r.URL.Query().Get("expectedRevision")
	host, slug, ok := h.resolveAppConfigHost(w, r, hostID, environment, project, module)
	if !ok {
		return
	}
	if host.Maintenance {
		h.fail(w, r, http.StatusConflict, "HOST_MAINTENANCE", "目标服务器处于维护模式，暂不允许删除配置文件")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.AppConfigResult
	err = h.store.callTarget(ctx, host, deploy.Request{
		Action: deploy.AppConfigDeleteAction,
		AppConfig: &deploy.AppConfigRequest{
			AppSlug:          slug,
			Filename:         filename,
			ExpectedRevision: strings.TrimSpace(expectedRev),
		},
		TraceID: r.Context().Value(traceKey{}).(string),
	}, "", &result)
	if err != nil {
		status, code, msg := appConfigFailure(err)
		h.fail(w, r, status, code, msg)
		return
	}
	h.recordAudit(actor, "APPLICATION_CONFIG_FILE_DELETED", moduleID+":"+slug+"/"+filename, "SUCCESS", r.Context().Value(traceKey{}).(string))
	h.success(w, r, result)
}
