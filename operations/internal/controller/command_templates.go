package controller

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type commandTemplate struct {
	ID        string                     `json:"id"`
	Version   string                     `json:"version"`
	Revision  int                        `json:"revision"`
	Draft     serviceinstall.Definition  `json:"draft"`
	Published *serviceinstall.Definition `json:"published,omitempty"`
	UpdatedAt string                     `json:"updatedAt"`
}

const commandTemplateColumns = "id,CAST(version AS TEXT),revision,draft_json,published_json,updated_at"

var commandErrorMessages = map[string]string{
	"COMMAND_PYTHON_PATH_INVALID":         "请选择 Python 可执行文件的完整路径",
	"COMMAND_PYTHON_NOT_FOUND":            "所选 Python 不存在，请先安装兼容版本或选择已有解释器",
	"COMMAND_PYTHON_NOT_EXECUTABLE":       "所选 Python 不可执行，请检查文件与执行权限",
	"COMMAND_PYTHON_PATH_UNSAFE":          "请选择系统或 Ops 安装的 Python 解释器",
	"COMMAND_PYTHON_CHECK_UNAVAILABLE":    "Python 环境检查未完成，请检查解释器和读取权限",
	"COMMAND_PYTHON_VERSION_UNSUPPORTED":  "Python 版本与安装方案不匹配，请选择兼容解释器",
	"COMMAND_PYTHON_MODULE_MISSING":       "Python 缺少必要运行组件，请根据检查结果补齐",
	"COMMAND_PYTHON_LIBRARY_MISSING":      "软件需要的系统库尚未准备，请根据检查结果补齐",
	"COMMAND_PYTHON_SQLITE_UNSUPPORTED":   "Python 内置 SQLite 版本不满足安装要求，请选择兼容解释器",
	"SERVICE_WEB_ACCESS_INVALID":          "请检查访问协议、入口路径、TCP端口和访问地址参数",
	"SERVICE_WEB_URL_INVALID":             "访问地址须为不含账号、查询参数和片段的完整 HTTP 或 HTTPS 地址",
	"SERVICE_BIND_DEFAULT_INVALID":        "默认监听地址须为明确的 IPv4 地址，例如 127.0.0.1",
	"INSTALL_REQUEST_TOO_LARGE":           "命令与安装参数合计过长，请精简脚本后重新预检",
	"COMMAND_OBJECT_ALREADY_EXISTS":       "目标软件已存在，请检测并登记已有软件，或选择独立安装位置",
	"COMMAND_OBJECT_CHECK_UNAVAILABLE":    "无法确认安装位置是否已被使用，请检查主机连接和读取权限",
	"COMMAND_TRUST_REQUIRED":              "需要安装授权，请在当前安装窗口继续验证权限",
	"COMMAND_STEP_ORDER_INVALID":          "请把结果核对放在最后，卸载范围核对放在最前",
	"COMMAND_STEP_INVALID":                "请检查步骤名称、命令、执行权限与超时设置",
	"BUILTIN_TEMPLATE_CONTRACT_CHANGED":   "基础处理步骤已变化，请刷新安装配置后重新编辑命令",
	"COMMAND_SIGNATURE_INVALID":           "命令配置的签名校验失败，请核对主机与控制器归属",
	"COMMAND_ACCOUNT_REQUIRED":            "请选择用于执行普通权限步骤的服务器账号",
	"COMMAND_ACCOUNT_INVALID":             "所选服务器账号不可用，请核对账号与目录权限",
	"COMMAND_STEP_FAILED":                 "命令执行失败，请在安装输出中查看具体步骤",
	"COMMAND_SCRIPT_LINE_ENDINGS_INVALID": "安装脚本换行格式有误，请在软件配置中重新保存并发布后重试",
	"COMMAND_STEP_INTERRUPTED":            "命令执行被中断，请核对已执行步骤后处理原任务",
}

func scanCommandTemplate(row interface{ Scan(...any) error }) (commandTemplate, error) {
	var t commandTemplate
	var draft, published string
	err := row.Scan(&t.ID, &t.Version, &t.Revision, &draft, &published, &t.UpdatedAt)
	if err == nil {
		err = deploy.StrictJSON([]byte(draft), &t.Draft)
	}
	if err == nil && published != "" {
		t.Published = &serviceinstall.Definition{}
		err = deploy.StrictJSON([]byte(published), t.Published)
	}
	return t, err
}

func commandTemplateKey(key, version string) string {
	if strings.HasPrefix(key, "custom-") {
		return key
	}
	return "builtin/" + key + "/" + version
}

func (s *Store) seedBuiltinCommandTemplates() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range serviceinstall.BuiltinCommandCatalog() {
		if err := d.ValidateCommands(); err != nil {
			return err
		}
		key := commandTemplateKey(d.Key, d.Version)
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("streamfusion-ops/"+key)).String()
		if _, err = tx.Exec("INSERT OR IGNORE INTO command_templates(id,recipe_key,draft_json,published_json,revision,updated_at,builtin) VALUES(?,?,?,?,1,?,1)", id, key, jsonText(d), jsonText(d), Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) publishedDefinitions() ([]serviceinstall.Definition, error) {
	rows, err := s.DB.Query("SELECT published_json FROM command_templates WHERE published_json<>'' ORDER BY recipe_key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []serviceinstall.Definition{}
	for rows.Next() {
		var raw string
		var d serviceinstall.Definition
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = deploy.StrictJSON([]byte(raw), &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	order := map[string]int{}
	for i, d := range serviceinstall.BuiltinCommandCatalog() {
		order[commandTemplateKey(d.Key, d.Version)] = i + 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := order[commandTemplateKey(out[i].Key, out[i].Version)], order[commandTemplateKey(out[j].Key, out[j].Version)]
		return a != 0 && (b == 0 || a < b)
	})
	return out, rows.Err()
}

func nodeRecipe(d serviceinstall.Definition) bool {
	return d.Key == "node" || d.Key == "nvm" || d.Key == "npm" || d.Key == "pnpm"
}

func (s *Store) publishedRecipes() ([]serviceinstall.Definition, error) {
	all, err := s.publishedDefinitions()
	out := []serviceinstall.Definition{}
	for _, d := range all {
		if !serviceinstall.IsOfficial(d) && !nodeRecipe(d) {
			out = append(out, d)
		}
	}
	return out, err
}

func (s *Store) publishedOfficialRecipes() ([]serviceinstall.Definition, error) {
	all, err := s.publishedDefinitions()
	out := []serviceinstall.Definition{}
	for _, d := range all {
		if serviceinstall.IsOfficial(d) {
			out = append(out, d)
		}
	}
	return out, err
}

func (s *Store) findInstallRecipe(key, version string) (serviceinstall.Definition, error) {
	return findInstallRecipe(s.DB, key, version)
}

func findInstallRecipe(q interface{ QueryRow(string, ...any) *sql.Row }, key, version string) (serviceinstall.Definition, error) {
	var raw string
	var d serviceinstall.Definition
	err := q.QueryRow("SELECT published_json FROM command_templates WHERE recipe_key=? AND published_json<>''", commandTemplateKey(key, version)).Scan(&raw)
	if err == nil {
		err = deploy.StrictJSON([]byte(raw), &d)
	}
	if err == nil && d.Version != version {
		err = errors.New("INSTALL_RECIPE_INVALID")
	}
	return d, err
}

// A command edit cannot invalidate an unchanged vendor package. Custom archives
// still retain their original complete template identity.
func sameMaterialDefinition(a, b serviceinstall.Definition) bool {
	return serviceinstall.Digest(serviceinstall.BareDefinition(a)) == serviceinstall.Digest(serviceinstall.BareDefinition(b))
}

func (h *HTTP) commandTemplateRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	if path == "/command-template-examples" && r.Method == "GET" {
		h.success(w, r, serviceinstall.CommandExamples())
		return true
	}
	if path == "/command-templates" && r.Method == "GET" {
		rows, err := h.store.DB.Query("SELECT " + commandTemplateColumns + " FROM command_templates ORDER BY builtin DESC,recipe_key")
		if err != nil {
			h.internal(w, r, err)
			return true
		}
		defer rows.Close()
		out := []commandTemplate{}
		for rows.Next() {
			t, e := scanCommandTemplate(rows)
			if e != nil {
				h.internal(w, r, e)
				return true
			}
			out = append(out, t)
		}
		if err = rows.Err(); err != nil {
			h.internal(w, r, err)
		} else {
			h.success(w, r, out)
		}
		return true
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) == 0 || p[0] != "command-templates" {
		return false
	}
	create := len(p) == 1 && r.Method == "POST"
	edit := len(p) == 2 && deploy.ID.MatchString(p[1]) && r.Method == "PUT"
	publish := len(p) == 3 && deploy.ID.MatchString(p[1]) && p[2] == "publish" && r.Method == "POST"
	withdraw := len(p) == 2 && deploy.ID.MatchString(p[1]) && r.Method == "DELETE"
	if !create && !edit && !publish && !withdraw {
		return false
	}
	var in struct {
		ExpectedVersion string                    `json:"expectedVersion"`
		Definition      serviceinstall.Definition `json:"definition"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", err.Error())
		return true
	}
	if create && serviceinstall.IsBuiltinCommand(in.Definition) {
		h.fail(w, r, 400, "COMMAND_BUILTIN_ALREADY_EXISTS", "内置配置已在列表中，请直接编辑")
		return true
	}
	if create || edit {
		in.Definition = serviceinstall.NormalizeCommandScripts(in.Definition)
		if !serviceinstall.IsBuiltinCommand(in.Definition) {
			in.Definition.Revision = "1"
			if in.Definition.Commands != nil {
				in.Definition.Commands.FlowVersion = 1
			}
		}
		if err := in.Definition.ValidateCommands(); err != nil {
			message := commandErrorMessages[err.Error()]
			if message == "" {
				message = "请补全安装和卸载步骤，并检查每步权限与超时设置"
			}
			h.fail(w, r, 400, err.Error(), message)
			return true
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return true
	}
	defer tx.Rollback()
	id := uuid.NewString()
	action := "COMMAND_TEMPLATE_SAVED"
	if create {
		var count int
		err = tx.QueryRow("SELECT count(*) FROM command_templates").Scan(&count)
		if err == nil && count >= 200 {
			h.fail(w, r, 409, "COMMAND_TEMPLATE_LIMIT", "命令配置已达到200项")
			return true
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO command_templates(id,recipe_key,draft_json,updated_at) VALUES(?,?,?,?)", id, in.Definition.Key, jsonText(in.Definition), Now())
		}
	} else {
		id = p[1]
		old, e := scanCommandTemplate(tx.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE id=?", id))
		err = e
		if err == nil && old.Version != in.ExpectedVersion {
			h.fail(w, r, 409, "VERSION_CONFLICT", "配置已变化，请刷新后重试")
			return true
		}
		if err == nil {
			switch {
			case edit:
				if old.Draft.Key != in.Definition.Key || serviceinstall.IsBuiltinCommand(old.Draft) && old.Draft.Version != in.Definition.Version {
					h.fail(w, r, 400, "COMMAND_KEY_IMMUTABLE", "已有配置的软件标识不能修改")
					return true
				}
				_, err = tx.Exec("UPDATE command_templates SET draft_json=?,version=version+1,updated_at=? WHERE id=?", jsonText(in.Definition), Now(), id)
			case publish:
				d := serviceinstall.NormalizeCommandScripts(old.Draft)
				if serviceinstall.IsBuiltinCommand(d) {
					d.Commands.Revision = int64(old.Revision + 1)
				} else {
					d.Revision = strconv.Itoa(old.Revision + 1)
				}
				if err = d.ValidateCommands(); err == nil {
					_, err = tx.Exec("UPDATE command_templates SET published_json=?,revision=revision+1,version=version+1,updated_at=? WHERE id=?", jsonText(d), Now(), id)
				}
				action = "COMMAND_TEMPLATE_PUBLISHED"
			case withdraw:
				_, err = tx.Exec("UPDATE command_templates SET published_json='',version=version+1,updated_at=? WHERE id=?", Now(), id)
				action = "COMMAND_TEMPLATE_WITHDRAWN"
			}
		}
	}
	if err == nil {
		err = auditTx(tx, s.Username, action, id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.fail(w, r, 404, "COMMAND_TEMPLATE_NOT_FOUND", "配置不存在")
		} else if strings.Contains(err.Error(), "UNIQUE") {
			h.fail(w, r, 409, "COMMAND_TEMPLATE_DUPLICATED", "软件标识已存在，请编辑原配置或使用其他标识")
		} else {
			h.internal(w, r, err)
		}
		return true
	}
	saved, err := scanCommandTemplate(h.store.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE id=?", id))
	if err != nil {
		h.internal(w, r, err)
	} else {
		h.success(w, r, saved)
	}
	return true
}
