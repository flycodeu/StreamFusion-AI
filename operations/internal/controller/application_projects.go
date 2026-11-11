package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) applicationProjectsRoute(w http.ResponseWriter, r *http.Request, s *session, route string) bool {
	if route == "/application-projects" {
		switch r.Method {
		case "GET":
			h.listApplicationProjects(w, r)
		case "POST":
			h.saveApplicationProject(w, r, s, "", false)
		default:
			return false
		}
		return true
	}
	p := strings.Split(strings.Trim(route, "/"), "/")
	if len(p) < 2 || p[0] != "application-projects" || !deploy.ID.MatchString(p[1]) {
		return false
	}
	if len(p) == 3 && p[2] == "archive" && r.Method == "POST" {
		h.saveApplicationProject(w, r, s, p[1], true)
		return true
	}
	if len(p) != 2 {
		return false
	}
	switch r.Method {
	case "GET":
		tx, err := h.store.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			h.internal(w, r, err)
			return true
		}
		defer tx.Rollback()
		project, err := readApplicationProject(tx, p[1])
		if err != nil {
			h.opsError(w, r, err)
		} else {
			h.success(w, r, project)
		}
	case "PATCH":
		h.saveApplicationProject(w, r, s, p[1], false)
	default:
		return false
	}
	return true
}

type applicationReader interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func readApplicationProject(q applicationReader, id string) (applicationProject, error) {
	p := applicationProject{Repositories: []applicationRepository{}, Modules: []applicationModule{}, Environments: []applicationEnvironment{}}
	err := q.QueryRow("SELECT id,name,description,CAST(version AS TEXT),archived,created_at,updated_at FROM application_projects WHERE id=?", id).Scan(&p.ID, &p.Name, &p.Description, &p.Version, &p.Archived, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	rows, err := q.Query("SELECT id,name,url,coalesce(git_account_id,''),branch FROM application_repositories WHERE project_id=? ORDER BY position,id", id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var item applicationRepository
		if err = rows.Scan(&item.ID, &item.Name, &item.URL, &item.GitAccountID, &item.Branch); err != nil {
			break
		}
		p.Repositories = append(p.Repositories, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return p, err
	}
	rows, err = q.Query("SELECT config_json FROM application_modules WHERE project_id=? ORDER BY position,id", id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var raw string
		var item applicationModule
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &item)
		}
		if err != nil {
			break
		}
		p.Modules = append(p.Modules, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return p, err
	}
	rows, err = q.Query("SELECT id,name FROM application_environments WHERE project_id=? ORDER BY position,id", id)
	if err != nil {
		return p, err
	}
	positions := map[string]int{}
	for rows.Next() {
		item := applicationEnvironment{Deployments: []applicationDeployment{}}
		if err = rows.Scan(&item.ID, &item.Name); err != nil {
			break
		}
		positions[item.ID] = len(p.Environments)
		p.Environments = append(p.Environments, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return p, err
	}
	rows, err = q.Query("SELECT environment_id,config_json FROM application_deployments WHERE project_id=? ORDER BY position,id", id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var environment, raw string
		var item applicationDeployment
		if err = rows.Scan(&environment, &raw); err == nil {
			err = json.Unmarshal([]byte(raw), &item)
		}
		if err != nil {
			break
		}
		position, ok := positions[environment]
		if !ok {
			err = errors.New("APPLICATION_ENVIRONMENT_MISSING")
			break
		}
		p.Environments[position].Deployments = append(p.Environments[position].Deployments, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return p, err
}

func (h *HTTP) listApplicationProjects(w http.ResponseWriter, r *http.Request) {
	p, err := parseListPage(r)
	if err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return
	}
	if !p.Enabled {
		p.Enabled = true
		p.PageSize = 10
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	archived := r.URL.Query().Get("archived")
	if len(query) > 240 || archived != "" && archived != "all" && archived != "true" && archived != "false" {
		h.fail(w, r, 400, "INVALID_INPUT", "搜索或归档筛选不正确")
		return
	}
	where := " WHERE instr(lower(name||' '||description),lower(?))>0"
	args := []any{query}
	if archived != "all" {
		where += " AND archived=?"
		args = append(args, archived == "true")
	}
	var total int
	tx, err := h.store.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(r.Context(), "SELECT count(*) FROM application_projects"+where, args...).Scan(&total); err != nil {
		h.internal(w, r, err)
		return
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT id FROM application_projects"+where+" ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?", append(args, p.PageSize, p.Offset)...)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	items := []applicationProject{}
	for _, id := range ids {
		item, e := readApplicationProject(tx, id)
		if e != nil {
			h.internal(w, r, e)
			return
		}
		items = append(items, item)
	}
	h.success(w, r, map[string]any{"items": items, "total": total, "page": p.Page, "pageSize": p.PageSize})
}

func (h *HTTP) saveApplicationProject(w http.ResponseWriter, r *http.Request, s *session, id string, archive bool) {
	var in applicationProject
	if archive {
		var body struct {
			Version string `json:"version"`
		}
		if decode(w, r, &body) != nil {
			h.fail(w, r, 400, "INVALID_INPUT", "请携带当前项目版本")
			return
		}
		in.Version = body.Version
	} else {
		if decode(w, r, &in) != nil {
			h.fail(w, r, 400, "INVALID_INPUT", "项目配置字段或内容不正确，最多64 KiB")
			return
		}
		if in.ID != "" && id != "" && in.ID != id {
			h.fail(w, r, 400, "INVALID_INPUT", "项目标识不匹配")
			return
		}
		if err := normalizeApplicationProject(&in); err != nil {
			h.fail(w, r, 400, "APPLICATION_CONFIG_INVALID", err.Error())
			return
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
	now := Now()
	action := "APPLICATION_PROJECT_SAVED"
	if id == "" {
		id = uuid.NewString()
		in.ID = id
		in.Version = "1"
		in.CreatedAt = now
		_, err = tx.Exec("INSERT INTO application_projects(id,name,description,archived,created_at,updated_at) VALUES(?,?,?,?,?,?)", id, in.Name, in.Description, in.Archived, now, now)
	} else {
		var old applicationProject
		old, err = readApplicationProject(tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			h.fail(w, r, 404, "NOT_FOUND", "项目不存在")
			return
		}
		if err != nil {
			h.internal(w, r, err)
			return
		}
		if old.Version != in.Version {
			h.fail(w, r, 409, "VERSION_CONFLICT", "项目已被修改，请刷新后重试")
			return
		}
		if archive {
			in = old
			in.Archived = true
			action = "APPLICATION_PROJECT_ARCHIVED"
		}
		in.ID = id
		in.CreatedAt = old.CreatedAt
		var revision int
		revision, _ = strconv.Atoi(old.Version)
		in.Version = strconv.Itoa(revision + 1)
		_, err = tx.Exec("UPDATE application_projects SET name=?,description=?,archived=?,version=version+1,updated_at=? WHERE id=?", in.Name, in.Description, in.Archived, now, id)
	}
	if err == nil && !archive {
		err = validateApplicationReferences(tx, in)
	}
	if err == nil && !archive {
		err = saveApplicationChildren(tx, in)
	}
	if err == nil {
		err = auditTx(tx, s.Username, action, id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			h.fail(w, r, 409, "APPLICATION_DUPLICATED", "项目名称或配置条目已存在，请调整后重试")
		} else if applicationConfigError(err) {
			h.fail(w, r, 400, "APPLICATION_CONFIG_INVALID", err.Error())
		} else {
			h.internal(w, r, err)
		}
		return
	}
	in.UpdatedAt = now
	if in.Repositories == nil {
		in.Repositories = []applicationRepository{}
	}
	if in.Modules == nil {
		in.Modules = []applicationModule{}
	}
	if in.Environments == nil {
		in.Environments = []applicationEnvironment{}
	}
	h.success(w, r, in)
}

type applicationInvalid string

func (e applicationInvalid) Error() string { return string(e) }

func applicationConfigError(err error) bool {
	var invalid applicationInvalid
	return errors.As(err, &invalid)
}

func saveApplicationChildren(tx *sql.Tx, p applicationProject) error {
	kept := map[string]map[string]bool{}
	for _, table := range []string{"application_repositories", "application_modules", "application_environments", "application_deployments"} {
		kept[table] = map[string]bool{}
	}
	for _, environment := range p.Environments {
		for _, deployment := range environment.Deployments {
			kept["application_deployments"][deployment.ID] = true
			var oldEnvironment, oldModule string
			err := tx.QueryRow("SELECT environment_id,module_id FROM application_deployments WHERE id=? AND project_id=?", deployment.ID, p.ID).Scan(&oldEnvironment, &oldModule)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && (oldEnvironment != environment.ID || oldModule != deployment.ModuleID) {
				return applicationInvalid("改变部署所属模块或环境时，请删除原配置后新增")
			}
		}
	}
	// Remove explicitly discarded bindings before their replacements claim the
	// same environment/module pair. Other rows keep their stable identities.
	if err := removeApplicationDrafts(tx, "application_deployments", p.ID, kept["application_deployments"]); err != nil {
		return err
	}
	claim := func(table, id string) error {
		var project string
		err := tx.QueryRow("SELECT project_id FROM "+table+" WHERE id=?", id).Scan(&project)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && project != p.ID {
			return applicationInvalid("配置条目属于其他项目，请刷新后重试")
		}
		kept[table][id] = true
		return nil
	}
	for index, r := range p.Repositories {
		if err := claim("application_repositories", r.ID); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO application_repositories(id,project_id,name,url,git_account_id,branch,position) VALUES(?,?,?,?,nullif(?,''),?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,git_account_id=excluded.git_account_id,branch=excluded.branch,position=excluded.position", r.ID, p.ID, r.Name, r.URL, r.GitAccountID, r.Branch, index)
		if err != nil {
			return err
		}
	}
	for index, m := range p.Modules {
		if err := claim("application_modules", m.ID); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO application_modules(id,project_id,name,repository_id,directory,build_host_id,build_software_id,config_json,position) VALUES(?,?,?,nullif(?,''),?,nullif(?,''),nullif(?,''),?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,repository_id=excluded.repository_id,directory=excluded.directory,build_host_id=excluded.build_host_id,build_software_id=excluded.build_software_id,config_json=excluded.config_json,position=excluded.position", m.ID, p.ID, m.Name, m.RepositoryID, m.Directory, m.Build.HostID, m.Build.SoftwareID, jsonText(m), index)
		if err != nil {
			return err
		}
	}
	for index, e := range p.Environments {
		if err := claim("application_environments", e.ID); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO application_environments(id,project_id,name,position) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,position=excluded.position", e.ID, p.ID, e.Name, index)
		if err != nil {
			return err
		}
		for position, d := range e.Deployments {
			if err := claim("application_deployments", d.ID); err != nil {
				return err
			}
			_, err = tx.Exec("INSERT INTO application_deployments(id,project_id,environment_id,module_id,host_id,runtime_software_id,config_json,position) VALUES(?,?,?,?,nullif(?,''),nullif(?,''),?,?) ON CONFLICT(id) DO UPDATE SET environment_id=excluded.environment_id,module_id=excluded.module_id,host_id=excluded.host_id,runtime_software_id=excluded.runtime_software_id,config_json=excluded.config_json,position=excluded.position", d.ID, p.ID, e.ID, d.ModuleID, d.HostID, d.RuntimeSoftwareID, jsonText(d), position)
			if err != nil {
				return err
			}
		}
	}
	// Delete only explicitly removed drafts. Retained rows are updated in place;
	// future deployment references can protect them with ordinary foreign keys.
	for _, table := range []string{"application_deployments", "application_modules", "application_environments", "application_repositories"} {
		if err := removeApplicationDrafts(tx, table, p.ID, kept[table]); err != nil {
			return err
		}
	}
	return nil
}

func removeApplicationDrafts(tx *sql.Tx, table, project string, kept map[string]bool) error {
	rows, err := tx.Query("SELECT id FROM "+table+" WHERE project_id=?", project)
	if err != nil {
		return err
	}
	removed := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		if !kept[id] {
			removed = append(removed, id)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range removed {
		if _, err = tx.Exec("DELETE FROM "+table+" WHERE id=? AND project_id=?", id, project); err != nil {
			return err
		}
	}
	return nil
}
