package controller

import (
	"database/sql"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/projectconfig"
)

type applicationModuleCatalog struct {
	Kind              string                         `json:"kind"`
	Description       string                         `json:"description,omitempty"`
	Environment       string                         `json:"environment"`
	Dependencies      []string                       `json:"dependencies,omitempty"`
	StartCommand      string                         `json:"startCommand"`
	RuntimeMode       string                         `json:"runtimeMode"`
	AccessURL         string                         `json:"accessUrl,omitempty"`
	CheckCommand      string                         `json:"checkCommand,omitempty"`
	TestCommand       string                         `json:"testCommand,omitempty"`
	SkipTests         bool                           `json:"skipTests,omitempty"`
	BuildCommand      string                         `json:"buildCommand,omitempty"`
	ArtifactPath      string                         `json:"artifactPath,omitempty"`
	DefaultPort       int                            `json:"defaultPort,omitempty"`
	HealthPath        string                         `json:"healthPath,omitempty"`
	Profiles          []string                       `json:"profiles,omitempty"`
	DefaultProfile    string                         `json:"defaultProfile,omitempty"`
	EnvHints          []projectconfig.EnvHint        `json:"envHints,omitempty"`
	ProxyHints        []projectconfig.ProxyHint      `json:"proxyHints,omitempty"`
	ConfigTemplates   []projectconfig.ConfigTemplate `json:"configTemplates,omitempty"`
	SupportsContainer bool                           `json:"supportsContainer,omitempty"`
}

type applicationCatalogRepository struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	URL                 string  `json:"url"`
	Branch              string  `json:"branch"`
	GitAccountID        *string `json:"gitAccountId,omitempty"`
	JenkinsCredentialID *string `json:"jenkinsCredentialId,omitempty"`
}

type applicationCatalogModule struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	RepositoryID string                    `json:"repositoryId"`
	Directory    string                    `json:"directory"`
	Catalog      *applicationModuleCatalog `json:"catalog"`
}

type applicationCatalogInput struct {
	Version      string                         `json:"version"`
	Code         *string                        `json:"code"`
	Name         *string                        `json:"name"`
	Description  *string                        `json:"description"`
	JenkinsURL   *string                        `json:"jenkinsUrl"`
	Archived     *bool                          `json:"archived"`
	Repositories []applicationCatalogRepository `json:"repositories"`
	Modules      []applicationCatalogModule     `json:"modules"`
}

var applicationProjectCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

func applicationArtifactPathValid(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 240 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\r\n\t\x00:?") || strings.Contains(value, "..") {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func (c *applicationModuleCatalog) normalize() error {
	if c == nil {
		return nil
	}
	c.Kind, c.RuntimeMode = strings.TrimSpace(c.Kind), strings.TrimSpace(c.RuntimeMode)
	c.Description = strings.TrimSpace(c.Description)
	c.Environment, c.StartCommand = strings.TrimSpace(c.Environment), strings.TrimSpace(c.StartCommand)
	c.CheckCommand = strings.TrimSpace(c.CheckCommand)
	c.TestCommand = strings.TrimSpace(c.TestCommand)
	c.BuildCommand = strings.TrimSpace(c.BuildCommand)
	c.ArtifactPath = strings.TrimSpace(c.ArtifactPath)
	c.HealthPath = strings.TrimSpace(c.HealthPath)
	c.DefaultProfile = strings.TrimSpace(c.DefaultProfile)
	for _, value := range []string{c.Kind, c.RuntimeMode} {
		if !applicationText(value, 240) || strings.ContainsAny(value, "\r\n") {
			return applicationInvalid("模块类型和运行方式最多240字节，请各填一行")
		}
	}
	if !applicationText(c.Description, 500) {
		return applicationInvalid("模块描述最多500字节")
	}
	if !applicationText(c.Environment, 2000) || !applicationText(c.StartCommand, 8192) {
		return applicationInvalid("运行环境最多2000字节，启动命令最多8192字节")
	}
	for _, cmd := range []string{c.CheckCommand, c.TestCommand, c.BuildCommand} {
		if !applicationText(cmd, 4096) || projectconfig.ContainsSecret(cmd) {
			return applicationInvalid("检查、测试或构建命令最多4096字节，请勿包含明文凭据")
		}
	}
	if !applicationArtifactPathValid(c.ArtifactPath) {
		return applicationInvalid("产物路径须为模块目录下的有效相对路径")
	}
	if c.DefaultPort < 0 || c.DefaultPort > 65535 {
		return applicationInvalid("模块默认端口须在 1–65535 之间")
	}
	if c.HealthPath != "" && (!strings.HasPrefix(c.HealthPath, "/") || len(c.HealthPath) > 240 || strings.ContainsAny(c.HealthPath, "\r\n\x00")) {
		return applicationInvalid("健康检查路径须以 / 开头且不超过240字节")
	}
	if c.DefaultProfile != "" && !projectconfig.ValidProfile(c.DefaultProfile) {
		return applicationInvalid("默认 Profile 格式不正确")
	}
	if projectconfig.ContainsSecret(c.Description) || projectconfig.ContainsSecret(c.Environment) || projectconfig.ContainsSecret(c.StartCommand) {
		return applicationInvalid("请通过环境变量引用凭据，避免在模块说明或命令中填写明文凭据")
	}
	if len(c.Dependencies) > 0 {
		seen := make(map[string]bool, len(c.Dependencies))
		cleaned := make([]string, 0, len(c.Dependencies))
		for _, dep := range c.Dependencies {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if !applicationText(dep, 64) || strings.ContainsAny(dep, "\r\n") || projectconfig.ContainsSecret(dep) {
				return applicationInvalid("每个依赖软件最多64字节，且不能包含换行或明文凭据")
			}
			key := strings.ToLower(dep)
			if seen[key] {
				continue
			}
			seen[key] = true
			cleaned = append(cleaned, dep)
			if len(cleaned) > 20 {
				return applicationInvalid("单个模块最多登记20项依赖软件")
			}
		}
		c.Dependencies = cleaned
	}
	if len(c.Profiles) > 0 {
		seen := make(map[string]bool, len(c.Profiles))
		cleaned := make([]string, 0, len(c.Profiles))
		for _, profile := range c.Profiles {
			profile = strings.TrimSpace(profile)
			if profile == "" {
				continue
			}
			if !projectconfig.ValidProfile(profile) {
				return applicationInvalid("环境 Profile 名称格式不正确")
			}
			key := strings.ToLower(profile)
			if seen[key] {
				continue
			}
			seen[key] = true
			cleaned = append(cleaned, profile)
			if len(cleaned) > 20 {
				return applicationInvalid("单个模块最多登记20个 Profile")
			}
		}
		c.Profiles = cleaned
	}
	if len(c.EnvHints) > 0 {
		seen := make(map[string]bool, len(c.EnvHints))
		cleaned := make([]projectconfig.EnvHint, 0, len(c.EnvHints))
		for _, hint := range c.EnvHints {
			hint.Name = strings.TrimSpace(hint.Name)
			hint.Profile = strings.TrimSpace(hint.Profile)
			if hint.Name == "" {
				continue
			}
			if !applicationVariableName.MatchString(hint.Name) || (hint.Profile != "" && !projectconfig.ValidProfile(hint.Profile)) {
				return applicationInvalid("环境变量提示名称或 Profile 格式不正确")
			}
			if seen[hint.Name] {
				continue
			}
			seen[hint.Name] = true
			hint.Sensitive = hint.Sensitive || projectconfig.IsSensitiveName(hint.Name)
			if hint.Sensitive {
				hint.DefaultValue = ""
			} else if !applicationText(hint.DefaultValue, 512) || strings.ContainsAny(hint.DefaultValue, "\r\n") || projectconfig.ContainsSecret(hint.DefaultValue) {
				return applicationInvalid("环境变量提示默认值过长或包含疑似凭据")
			}
			cleaned = append(cleaned, hint)
			if len(cleaned) > 40 {
				return applicationInvalid("单个模块最多保留40项环境变量提示")
			}
		}
		c.EnvHints = cleaned
	}
	if len(c.ProxyHints) > 0 {
		cleaned := make([]projectconfig.ProxyHint, 0, len(c.ProxyHints))
		for _, hint := range c.ProxyHints {
			hint.Location = strings.TrimSpace(hint.Location)
			if hint.Location == "" {
				continue
			}
			if !strings.HasPrefix(hint.Location, "/") || len(hint.Location) > 120 || strings.ContainsAny(hint.Location, " \t\r\n\x00") || hint.TargetPort < 0 || hint.TargetPort > 65535 {
				return applicationInvalid("识别的代理路径或端口提示格式不正确")
			}
			cleaned = append(cleaned, hint)
			if len(cleaned) > 15 {
				return applicationInvalid("单个模块最多保留15条代理路径提示")
			}
		}
		c.ProxyHints = cleaned
	}
	if len(c.ConfigTemplates) > 0 {
		seen := make(map[string]bool, len(c.ConfigTemplates))
		cleaned := make([]projectconfig.ConfigTemplate, 0, len(c.ConfigTemplates))
		for _, tpl := range c.ConfigTemplates {
			tpl.Filename = strings.TrimSpace(tpl.Filename)
			tpl.Profile = strings.TrimSpace(tpl.Profile)
			tpl.Format = strings.TrimSpace(strings.ToLower(tpl.Format))
			if tpl.Filename == "" {
				continue
			}
			if !deploy.ValidAppConfigFilename(tpl.Filename) || seen[tpl.Filename] ||
				(tpl.Profile != "" && !projectconfig.ValidProfile(tpl.Profile)) ||
				len(tpl.Content) > 32<<10 || strings.ContainsRune(tpl.Content, '\x00') {
				return applicationInvalid("模块配置模板文件名或内容超出限制")
			}
			seen[tpl.Filename] = true
			tpl.Content = deploy.NormalizeConfigText(tpl.Content)
			cleaned = append(cleaned, tpl)
			if len(cleaned) >= 8 {
				break
			}
		}
		c.ConfigTemplates = cleaned
	}
	if strings.TrimSpace(c.AccessURL) != "" {
		access := c.AccessURL
		if err := normalizeHTTPURLWithMessage(&access, "模块访问地址须为 HTTP 或 HTTPS 地址，且不能包含账号、查询参数或片段"); err != nil {
			return err
		}
		c.AccessURL = access
	} else {
		c.AccessURL = ""
	}
	return nil
}

var applicationJenkinsHostLabel = regexp.MustCompile(`^[0-9A-Za-z](?:[0-9A-Za-z-]{0,61}[0-9A-Za-z])?$`)

func normalizeApplicationJenkinsURL(value *string) error {
	return normalizeHTTPURLWithMessage(value, "Jenkins 作业链接须为 HTTP 或 HTTPS 地址，且不能包含账号、查询参数或片段")
}

func normalizeHTTPURLWithMessage(value *string, msg string) error {
	*value = strings.TrimSpace(*value)
	if *value == "" {
		return nil
	}
	invalid := applicationInvalid(msg)
	u, err := url.Parse(*value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || strings.Contains(*value, "#") || len(*value) > 2048 {
		return invalid
	}
	if strings.ContainsFunc(*value, unicode.IsSpace) || strings.ContainsFunc(u.Path, unicode.IsControl) || strings.Contains(u.Path, `\`) {
		return invalid
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return invalid
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if strings.HasPrefix(u.Host, "[") && (ip == nil || !strings.Contains(host, ":")) {
		return invalid
	}
	if ip == nil {
		if len(host) > 253 {
			return invalid
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if !applicationJenkinsHostLabel.MatchString(label) {
				return invalid
			}
		}
	}
	return nil
}

func preserveApplicationCatalog(p *applicationProject, old applicationProject) {
	if p.JenkinsURL == "" {
		p.JenkinsURL = old.JenkinsURL
	}
	catalogs := make(map[string]*applicationModuleCatalog, len(old.Modules))
	for _, module := range old.Modules {
		catalogs[module.ID] = module.Catalog
	}
	for i := range p.Modules {
		if p.Modules[i].Catalog == nil {
			p.Modules[i].Catalog = catalogs[p.Modules[i].ID]
		}
	}
	previousRepos := map[string]applicationRepository{}
	for _, repo := range old.Repositories {
		previousRepos[repo.ID] = repo
	}
	for i := range p.Repositories {
		repo := &p.Repositories[i]
		if old := previousRepos[repo.ID]; repo.JenkinsCredentialID == "" && old.URL == repo.URL {
			repo.JenkinsCredentialID = old.JenkinsCredentialID
		}
	}
	deployments := map[string]applicationDeployment{}
	for _, environment := range old.Environments {
		for _, deployment := range environment.Deployments {
			deployments[deployment.ID] = deployment
		}
	}
	for i := range p.Environments {
		for j := range p.Environments[i].Deployments {
			deployment := &p.Environments[i].Deployments[j]
			if deployment.RecordedVersion == "" {
				deployment.RecordedVersion = deployments[deployment.ID].RecordedVersion
			}
			if deployment.ConfigDrafts == nil {
				deployment.ConfigDrafts = deployments[deployment.ID].ConfigDrafts
			}
		}
	}
}

func (h *HTTP) saveApplicationCatalog(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in applicationCatalogInput
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "项目档案字段或内容不正确，最多64 KiB")
		return
	}
	if len(in.Repositories) > 20 || len(in.Modules) > 40 {
		h.fail(w, r, 400, "APPLICATION_CONFIG_INVALID", "每项目最多20个仓库和40个模块")
		return
	}

	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()

	project := applicationProject{ID: id}
	if id != "" {
		project, err = readApplicationProject(tx, id)
		if err != nil {
			h.opsError(w, r, err)
			return
		}
		if project.Version != in.Version {
			h.fail(w, r, 409, "VERSION_CONFLICT", "项目已被修改，请刷新后重试")
			return
		}
	}
	if in.Name != nil {
		project.Name = strings.TrimSpace(*in.Name)
	}
	if in.Code != nil {
		code := strings.ToLower(strings.TrimSpace(*in.Code))
		if code != "" {
			if !applicationProjectCodePattern.MatchString(code) {
				h.fail(w, r, 400, "APPLICATION_CONFIG_INVALID", "项目简称仅支持小写字母、数字、点、短横线或下划线（1～63 位）")
				return
			}
			project.Code = code
		}
	}
	if in.Description != nil {
		project.Description = strings.TrimSpace(*in.Description)
	}
	if !validName(project.Name) || !applicationText(project.Description, 2000) {
		h.fail(w, r, 400, "APPLICATION_CONFIG_INVALID", "请填写项目名称，说明最多2000字节")
		return
	}
	if in.JenkinsURL != nil {
		project.JenkinsURL = *in.JenkinsURL
		if err := normalizeApplicationJenkinsURL(&project.JenkinsURL); err != nil {
			h.applicationSaveError(w, r, err)
			return
		}
	}
	action := "APPLICATION_PROJECT_SAVED"
	if in.Archived != nil && project.Archived != *in.Archived {
		project.Archived = *in.Archived
		if project.Archived {
			action = "APPLICATION_PROJECT_ARCHIVED"
		} else {
			action = "APPLICATION_PROJECT_RESTORED"
		}
	}
	now := Now()
	if id == "" {
		project.ID = uuid.NewString()
		if project.Code == "" {
			project.Code = "project-" + project.ID
		}
		_, err = tx.Exec("INSERT INTO application_projects(id,code,name,description,jenkins_url,archived,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", project.ID, project.Code, project.Name, project.Description, project.JenkinsURL, project.Archived, now, now)
	} else {
		var result sql.Result
		result, err = tx.Exec("UPDATE application_projects SET code=?,name=?,description=?,jenkins_url=?,archived=?,version=version+1,updated_at=? WHERE id=? AND version=?", project.Code, project.Name, project.Description, project.JenkinsURL, project.Archived, now, id, in.Version)
		if err == nil {
			var count int64
			if count, err = result.RowsAffected(); err == nil && count == 0 {
				h.fail(w, r, 409, "VERSION_CONFLICT", "项目已被修改，请刷新后重试")
				return
			}
		}
	}
	previous := project
	if err == nil {
		err = saveApplicationCatalogChildren(tx, project, in)
	}
	if err == nil {
		project, err = readApplicationProject(tx, project.ID)
	}
	if err == nil {
		err = validateApplicationCatalogChildren(project)
	}
	if err == nil {
		err = validateApplicationDeploymentModes(project, previous)
	}
	if err == nil {
		err = auditTx(tx, s.Username, action, project.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.applicationSaveError(w, r, err)
		return
	}
	h.success(w, r, project)
}

func (h *HTTP) deleteApplicationModule(w http.ResponseWriter, r *http.Request, s *session, projectID, moduleID string) {
	var in struct {
		Version string `json:"version"`
	}
	if decode(w, r, &in) != nil || in.Version == "" {
		h.fail(w, r, 400, "INVALID_INPUT", "请携带当前项目版本")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()

	var version string
	var exists, referenced bool
	err = tx.QueryRow(`SELECT CAST(p.version AS TEXT),
		EXISTS(SELECT 1 FROM application_modules m WHERE m.project_id=p.id AND m.id=?),
		EXISTS(SELECT 1 FROM application_deployments d WHERE d.project_id=p.id AND d.module_id=?)
		FROM application_projects p WHERE p.id=?`, moduleID, moduleID, projectID).Scan(&version, &exists, &referenced)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if version != in.Version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "项目已被修改，请刷新后重试")
		return
	}
	if !exists {
		h.fail(w, r, 404, "NOT_FOUND", "项目模块不存在")
		return
	}
	if !referenced {
		// Keep any new foreign-key references protected too, without rewriting them.
		_, err = tx.Exec("DELETE FROM application_modules WHERE project_id=? AND id=?", projectID, moduleID)
	}
	if referenced || err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		h.fail(w, r, 409, "APPLICATION_MODULE_IN_USE", "请先解除该模块关联的环境或服务实例")
		return
	}
	if err == nil {
		_, err = tx.Exec("UPDATE application_projects SET version=version+1,updated_at=? WHERE id=?", Now(), projectID)
	}
	var project applicationProject
	if err == nil {
		project, err = readApplicationProject(tx, projectID)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "APPLICATION_MODULE_DELETED", moduleID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, project)
}

func saveApplicationCatalogChildren(tx *sql.Tx, project applicationProject, in applicationCatalogInput) error {
	if len(in.Repositories) == 0 && len(in.Modules) == 0 {
		return nil
	}
	seen := map[string]bool{}
	assign := func(id *string) error {
		if *id == "" {
			*id = uuid.NewString()
		}
		if !deploy.ID.MatchString(*id) || seen[*id] {
			return applicationInvalid("配置条目标识重复或无效，请刷新后重试")
		}
		seen[*id] = true
		return nil
	}
	var repositoryPosition, modulePosition int
	if err := tx.QueryRow(`SELECT coalesce((SELECT max(position)+1 FROM application_repositories WHERE project_id=?),0),
		coalesce((SELECT max(position)+1 FROM application_modules WHERE project_id=?),0)`, project.ID, project.ID).Scan(&repositoryPosition, &modulePosition); err != nil {
		return err
	}
	repositories := make(map[string]applicationRepository, len(project.Repositories)+len(in.Repositories))
	for _, repository := range project.Repositories {
		repositories[repository.ID] = repository
	}
	for _, item := range in.Repositories {
		if err := assign(&item.ID); err != nil {
			return err
		}
		old, exists := repositories[item.ID]
		gitAccountID := old.GitAccountID
		if item.GitAccountID != nil {
			gitAccountID = strings.TrimSpace(*item.GitAccountID)
		}
		jenkinsCredentialID := old.JenkinsCredentialID
		if item.JenkinsCredentialID != nil {
			jenkinsCredentialID = *item.JenkinsCredentialID
		} else if strings.TrimSpace(item.URL) != old.URL {
			jenkinsCredentialID = ""
		}
		repository := applicationRepository{ID: item.ID, Name: item.Name, URL: item.URL, Branch: item.Branch, GitAccountID: gitAccountID, JenkinsCredentialID: jenkinsCredentialID}
		if err := normalizeApplicationRepository(&repository); err != nil {
			return applicationInvalid(err.Error())
		}
		if repository.GitAccountID != "" && (!exists || old.GitAccountID != repository.GitAccountID || old.URL != repository.URL) {
			if err := validateApplicationReferences(tx, applicationProject{Repositories: []applicationRepository{repository}}); err != nil {
				return err
			}
		}
		var err error
		if exists {
			_, err = tx.Exec("UPDATE application_repositories SET name=?,url=?,git_account_id=nullif(?,''),branch=?,jenkins_credential_id=? WHERE id=? AND project_id=?", repository.Name, repository.URL, repository.GitAccountID, repository.Branch, repository.JenkinsCredentialID, repository.ID, project.ID)
		} else {
			_, err = tx.Exec(`INSERT INTO application_repositories(id,project_id,name,url,git_account_id,branch,position,jenkins_credential_id)
				VALUES(?,?,?,?,nullif(?,''),?,?,?)`, repository.ID, project.ID, repository.Name, repository.URL, repository.GitAccountID, repository.Branch, repositoryPosition, repository.JenkinsCredentialID)
			repositoryPosition++
		}
		if err != nil {
			return err
		}
		repositories[repository.ID] = repository
	}

	modules := make(map[string]bool, len(project.Modules))
	for _, module := range project.Modules {
		modules[module.ID] = true
	}
	for _, item := range in.Modules {
		if err := assign(&item.ID); err != nil {
			return err
		}
		item.Name, item.Directory = strings.TrimSpace(item.Name), strings.TrimSpace(item.Directory)
		if item.Directory == "" {
			item.Directory = "."
		}
		if !validName(item.Name) || !projectconfig.ValidRelativePath(item.Directory) {
			return applicationInvalid("请填写模块名称和仓库内相对目录，仓库根目录填写 .")
		}
		if _, exists := repositories[item.RepositoryID]; item.RepositoryID != "" && !exists {
			return applicationInvalid("模块只能使用本项目的仓库")
		}
		if err := item.Catalog.normalize(); err != nil {
			return err
		}
		var err error
		if modules[item.ID] {
			config := `json_set(config_json,'$.name',?,'$.repositoryId',?,'$.directory',?)`
			args := []any{item.Name, item.RepositoryID, item.Directory, item.Name, item.RepositoryID, item.Directory}
			if item.Catalog != nil {
				config = `json_set(` + config + `,'$.catalog',json(?))`
				args = append(args, jsonText(item.Catalog))
			}
			_, err = tx.Exec("UPDATE application_modules SET name=?,repository_id=nullif(?,''),directory=?,config_json="+config+" WHERE id=? AND project_id=?", append(args, item.ID, project.ID)...)
		} else {
			if item.Catalog == nil {
				item.Catalog = &applicationModuleCatalog{}
			}
			module := applicationModule{
				ID: item.ID, Name: item.Name, RepositoryID: item.RepositoryID, Directory: item.Directory, Catalog: item.Catalog,
				Build:   applicationBuild{Kind: "COMMAND", Context: ".", Steps: []applicationStep{}},
				Runtime: applicationRuntime{Kind: "NATIVE", Health: applicationHealth{Kind: "NONE"}},
			}
			_, err = tx.Exec("INSERT INTO application_modules(id,project_id,name,repository_id,directory,config_json,position) VALUES(?,?,?,nullif(?,''),?,?,?)", module.ID, project.ID, module.Name, module.RepositoryID, module.Directory, jsonText(module), modulePosition)
			modulePosition++
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func validateApplicationCatalogChildren(project applicationProject) error {
	if len(project.Repositories) > 20 || len(project.Modules) > 40 {
		return applicationInvalid("每项目最多20个仓库和40个模块")
	}
	ids := map[string]bool{}
	for _, environment := range project.Environments {
		ids[environment.ID] = true
		for _, deployment := range environment.Deployments {
			ids[deployment.ID] = true
		}
	}
	repositories := map[string]bool{}
	for _, repository := range project.Repositories {
		name := strings.ToLower(repository.Name)
		if ids[repository.ID] || repositories[name] {
			return applicationInvalid("仓库标识或名称不能重复")
		}
		ids[repository.ID], repositories[name] = true, true
	}
	modules := map[string]bool{}
	for _, module := range project.Modules {
		name := strings.ToLower(module.Name)
		if ids[module.ID] || modules[name] {
			return applicationInvalid("模块标识或名称不能重复")
		}
		ids[module.ID], modules[name] = true, true
	}
	return validateApplicationDeploymentBindings(project)
}
