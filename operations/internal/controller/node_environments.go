package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func (h *HTTP) nodeEnvironmentRoute(w http.ResponseWriter, r *http.Request, s *session, route string) bool {
	if h.nodeRegistryRoute(w, r, s, route) {
		return true
	}
	if route == "/node-environment-recipes" && r.Method == "GET" {
		all, err := h.store.publishedDefinitions()
		if err != nil {
			h.internal(w, r, err)
			return true
		}
		nodes, tools := []serviceinstall.Definition{}, []serviceinstall.Definition{}
		for _, d := range all {
			if d.Key == "node" {
				nodes = append(nodes, d)
			} else if nodeRecipe(d) {
				tools = append(tools, d)
			}
		}
		h.success(w, r, map[string]any{"node": nodes, "tools": tools})
		return true
	}
	p := strings.Split(strings.Trim(route, "/"), "/")
	if len(p) >= 3 && p[0] == "hosts" && deploy.ID.MatchString(p[1]) {
		if len(p) == 3 && p[2] == "node-environments" && r.Method == "GET" {
			h.listNodeEnvironments(w, r, p[1])
			return true
		}
		if len(p) == 3 && p[2] == "node-environment-plans" && r.Method == "POST" {
			h.createNodeEnvironmentPlan(w, r, s, p[1])
			return true
		}
		if len(p) == 4 && p[2] == "node-environments" && r.Method == "POST" {
			if p[3] == "discover" {
				h.discoverNodeEnvironments(w, r, p[1])
				return true
			}
			if p[3] == "register" {
				h.registerNodeEnvironment(w, r, s, p[1])
				return true
			}
		}
	}
	if len(p) >= 2 && p[0] == "node-environments" && deploy.ID.MatchString(p[1]) {
		if len(p) == 3 && p[2] == "jobs" && r.Method == "GET" {
			h.listNodeEnvironmentJobs(w, r, p[1])
			return true
		}
		if len(p) == 2 && r.Method == "GET" {
			h.getNodeEnvironment(w, r, p[1])
			return true
		}
		if len(p) == 3 && r.Method == "POST" {
			if p[2] == "observe" {
				h.observeNodeEnvironment(w, r, s, p[1])
				return true
			}
			if p[2] == "release" {
				h.releaseNodeEnvironment(w, r, s, p[1])
				return true
			}
		}
	}
	if len(p) == 3 && p[0] == "node-environment-plans" && deploy.ID.MatchString(p[1]) && p[2] == "submit" && r.Method == "POST" {
		h.submitNodeEnvironmentPlan(w, r, s, p[1])
		return true
	}
	return false
}

func (h *HTTP) nodeError(w http.ResponseWriter, r *http.Request, err error) {
	var compatibility *installCompatibilityError
	if errors.As(err, &compatibility) {
		code, message := installTargetError(err)
		h.fail(w, r, 409, code, message)
		return
	}
	if err != nil && commandErrorMessages[err.Error()] != "" {
		h.fail(w, r, 409, err.Error(), commandErrorMessages[err.Error()])
		return
	}
	code := "NODE_OPERATION_UNAVAILABLE"
	messages := map[string]string{
		"NODE_CAPABILITY_REQUIRED":           "主机已接入，需更新接入组件以管理 Node；更新后重新检查即可",
		"NODE_TERMINAL_CAPABILITY_REQUIRED":  "接入组件尚未支持终端接入，请更新后重新检查",
		"NODE_TERMINAL_CHANGED":              "Bash配置已变化，请重新预检后确认接入",
		"NODE_TERMINAL_UNCONFIRMED":          "终端接入结果尚未确认，请检查原操作结果",
		"NODE_TERMINAL_CONFLICT":             "Bash配置已有其他nvm设置或被修改的Ops片段，请核对后重新预检；原配置未覆盖",
		"NODE_TERMINAL_UNSUPPORTED":          "此账号未使用受支持的Bash登录环境，暂不能自动接入终端",
		"NODE_REGISTRY_CAPABILITY_REQUIRED":  "接入组件尚未支持npm镜像管理，请更新后重新检查",
		"NODE_REGISTRY_CHANGED":              "镜像列表或账号配置已变化，请重新预检并确认",
		"NODE_REGISTRY_RESULT_MISMATCH":      "npm账号镜像回读与所选地址不一致，请核对原作业",
		"NODE_REGISTRY_NPM_REQUIRED":         "请先在此环境安装带有npm的Node版本",
		"NODE_REGISTRY_PATH_UNSAFE":          "账号的.npmrc路径或权限不符合要求，请检查文件归属和链接",
		"NODE_REGISTRY_READ_FAILED":          "无法读取账号的npm配置，请检查.npmrc读取权限和npm程序",
		"NODE_REGISTRY_CONFIG_UNSUPPORTED":   "现有registry包含变量或认证地址，请先在服务器核对.npmrc",
		"NODE_REGISTRY_CONFIG_REJECTED":      "npm拒绝此配置，原文件未替换；请检查.npmrc格式和认证配置",
		"NODE_REGISTRY_OTHER_CONFIG_CHANGED": "npm准备的新配置改变了其他设置，原文件未替换；请核对.npmrc",
		"NODE_REGISTRY_RESULT_UNCONFIRMED":   "镜像切换结果待确认，请核对原作业",
		"NODE_REGISTRY_ACTOR_CHANGED":        "操作账号状态已变化，请重新登录并预检",
		"NODE_REGISTRY_LOCK_MISMATCH":        "操作锁状态不一致，请检查原作业",
		"NODE_ACCOUNT_CHANGED":               "执行账号身份已变化，请重新核对账号",
		"NODE_ENVIRONMENT_CHANGED":           "环境已变化，请刷新后重新检查",
		"NODE_ENVIRONMENT_DRIFT":             "服务器环境已被修改，请重新检测",
		"NODE_ENVIRONMENT_READ_ONLY":         "这是自行安装的环境，只支持检测和解除登记",
		"NODE_PLAN_STALE":                    "计划已变化或过期，请重新预检",
		"NODE_RECIPE_UNAVAILABLE":            "此版本的安装配置尚未发布，请先在安装配置中发布",
		"NODE_MATERIAL_MISSING":              "所选版本离线材料未就绪，请选择已有材料或补充材料",
		"NODE_MATERIAL_MISMATCH":             "材料与所选软件版本不一致，请重新选择",
		"NODE_CLEAR_DEFAULT_REQUIRED":        "这是最后一个默认版本，请明确确认清空环境默认版本",
		"NODE_VERSION_EXISTS":                "该版本已存在，请查看现有安装记录",
		"NODE_VERSION_NOT_MANAGED":           "请选择此环境中已安装且由 Ops 管理的版本",
		"NODE_DEFAULT_IN_USE":                "该 Node 版本仍是环境默认，请先切换默认版本",
		"NODE_VERSION_REFERENCED":            "该 Node 版本仍被使用，请先处理引用",
		"NODE_ENVIRONMENT_NOT_EMPTY":         "环境仍有已安装版本，请先逐个卸载",
		"NODE_ALREADY_INSTALLED":             "该 Node 版本已安装，请查看现有版本",
		"NODE_CONFIRMATION_REQUIRED":         "请确认操作影响并输入环境名称",
		"NODE_RESULT_UNCONFIRMED":            "操作结果待确认，请检查原操作结果",
		"OPERATION_LOCKED":                   "主机有未决操作，请先检查原操作结果",
		"HOST_CHANGED":                       "主机已变化，请刷新后重试",
		"QUEUE_FULL":                         "未结束作业已达到上限，请稍后重试",
	}
	if err != nil {
		if _, ok := messages[err.Error()]; ok {
			code = err.Error()
		} else if validNodeErrorCode(err.Error()) {
			code = err.Error()
		}
	}
	message := messages[code]
	if message == "" {
		message = "Node 操作未完成，请检查账号、环境目录和安装材料"
	}
	h.fail(w, r, 409, code, message)
}

func (h *HTTP) nodeTargetReady(ctx context.Context, t target, terminal ...bool) error {
	cap, err := h.nodeTargetCapabilities(ctx, t)
	if err != nil {
		return err
	}
	if len(terminal) > 0 && terminal[0] && cap.NodeTerminalVersion != 1 {
		return errors.New("NODE_TERMINAL_CAPABILITY_REQUIRED")
	}
	if len(terminal) > 1 && terminal[1] && cap.NodeRegistryVersion != 1 {
		return errors.New("NODE_REGISTRY_CAPABILITY_REQUIRED")
	}
	return nil
}

func (h *HTTP) nodeTargetCapabilities(ctx context.Context, t target) (executorCapabilities, error) {
	var cap executorCapabilities
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &cap); err != nil {
		return cap, err
	}
	if cap.Owner.ControllerID != h.store.controllerID() || cap.Owner.TargetID != t.ID || cap.Owner.ProtocolVersion != 1 || cap.Owner.ExecutorVersion != deploy.Version {
		return cap, errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if cap.Platform != "linux/amd64" {
		return cap, errors.New("NODE_PLATFORM_UNSUPPORTED")
	}
	if cap.NodeEnvironmentVersion != deploy.NodeEnvironmentCapabilityVersion {
		return cap, errors.New("NODE_CAPABILITY_REQUIRED")
	}
	return cap, nil
}

func (h *HTTP) listNodeEnvironments(w http.ResponseWriter, r *http.Request, hostID string) {
	if _, err := h.store.readTarget(hostID); err != nil {
		h.opsError(w, r, err)
		return
	}
	rows, err := h.store.DB.Query("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE host_id=? AND management_mode<>'RELEASED' ORDER BY created_at DESC", hostID)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	out := []nodeEnvironment{}
	for rows.Next() {
		n, e := scanNodeEnvironment(rows)
		if e != nil {
			rows.Close()
			h.internal(w, r, e)
			return
		}
		out = append(out, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, out)
}

func (h *HTTP) getNodeEnvironment(w http.ResponseWriter, r *http.Request, id string) {
	n, err := h.store.readNodeEnvironment(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	n.Versions, err = h.store.nodeEnvironmentVersions(id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, n)
}

func (h *HTTP) observeNodeEnvironment(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Version string `json:"expectedVersion"`
	}
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "请携带当前环境版本")
		return
	}
	n, err := h.store.readNodeEnvironment(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if n.Version != in.Version || n.ManagementMode == "RELEASED" {
		h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_CHANGED"))
		return
	}
	t, err := h.store.readTarget(n.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	capabilities, err := h.nodeTargetCapabilities(ctx, t)
	var observed deploy.NodeEnvironmentObservation
	if err == nil {
		action := "NODE_ENVIRONMENT_OBSERVE"
		if n.ManagementMode == "OBSERVE" {
			action = "NODE_ENVIRONMENT_DISCOVER"
		}
		nodeContext := n.context()
		nodeContext.ConnectTerminal = n.ManagementMode == "MANAGED" && capabilities.NodeTerminalVersion == 1
		nodeContext.ObserveRegistry = n.ManagementMode == "MANAGED" && capabilities.NodeRegistryVersion == 1
		err = h.store.callTarget(ctx, t, deploy.Request{Action: action, NodeEnvironment: &deploy.NodeEnvironmentRequest{Context: nodeContext, Action: strings.TrimPrefix(action, "NODE_ENVIRONMENT_")}}, "", &observed)
	}
	if err == nil && !nodeObservationMatches(n, observed) {
		err = errors.New("NODE_ENVIRONMENT_DRIFT")
	}
	h.store.mu.Lock()
	tx, writeErr := h.beginAuthorized(s)
	if writeErr == nil {
		defer tx.Rollback()
		writeErr = nodeEnvironmentCurrent(tx, n)
		if writeErr == nil {
			current, e := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", t.ID))
			if e != nil {
				writeErr = e
			} else if pluginTargetSnapshot(current) != pluginTargetSnapshot(t) {
				writeErr = errors.New("HOST_CHANGED")
			}
		}
		if writeErr == nil {
			writeErr = nodeTargetUnlocked(tx, n.HostID)
		}
		if writeErr == nil {
			if err == nil {
				writeErr = storeNodeObservation(tx, id, observed)
			} else {
				_, writeErr = tx.Exec("UPDATE node_environments SET error_code='NODE_OBSERVATION_FAILED',updated_at=? WHERE id=?", Now(), id)
			}
		}
		if writeErr == nil {
			writeErr = auditTx(tx, s.Username, "NODE_ENVIRONMENT_OBSERVED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
		}
		if writeErr == nil {
			writeErr = tx.Commit()
		}
	}
	h.store.mu.Unlock()
	if writeErr != nil {
		h.nodeError(w, r, writeErr)
		return
	}
	if err != nil {
		h.nodeError(w, r, err)
		return
	}
	h.getNodeEnvironment(w, r, id)
}

func (h *HTTP) listNodeEnvironmentJobs(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := h.store.readNodeEnvironment(id); err != nil {
		h.opsError(w, r, err)
		return
	}
	page, err := parseListPage(r)
	if err != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "分页参数无效")
		return
	}
	where := ` FROM jobs j WHERE j.node_environment_plan_id IN (SELECT id FROM node_environment_plans WHERE environment_id=?) OR CASE WHEN json_valid(j.input_json) THEN json_extract(j.input_json,'$.installation.node.environmentId') END=? OR CASE WHEN json_valid(j.input_json) THEN json_extract(j.input_json,'$.uninstallation.installation.node.environmentId') END=?`
	limit, total := 100, 0
	if page.Enabled {
		limit = page.PageSize
		if err = h.store.DB.QueryRow("SELECT count(*)"+where, id, id, id).Scan(&total); err != nil {
			h.internal(w, r, err)
			return
		}
	}
	rows, err := h.store.DB.Query(`SELECT j.id,j.kind,coalesce(j.target_id,''),j.status,j.phase,j.error_code,j.target_ack_pending,j.trace_id,j.result_json,j.created_at,j.updated_at`+where+` ORDER BY j.created_at DESC,j.id DESC LIMIT ? OFFSET ?`, id, id, id, limit, page.Offset)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var job, kind, target, status, phase, code, trace, raw, created, updated string
		var pending bool
		if err = rows.Scan(&job, &kind, &target, &status, &phase, &code, &pending, &trace, &raw, &created, &updated); err != nil {
			h.internal(w, r, err)
			return
		}
		out = append(out, map[string]any{"id": job, "kind": kind, "targetId": target, "status": status, "phase": phase, "errorCode": code, "targetAckPending": pending, "traceId": trace, "result": json.RawMessage(raw), "createdAt": created, "updatedAt": updated})
	}
	if err = rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, page.reply(out, total))
}

func (h *HTTP) discoverNodeEnvironments(w http.ResponseWriter, r *http.Request, hostID string) {
	var in struct {
		HostVersion string `json:"expectedHostVersion"`
		Account     string `json:"account"`
		RootPath    string `json:"rootPath"`
	}
	if decode(w, r, &in) != nil || !serviceinstall.NodeAccountPattern.MatchString(in.Account) || in.Account == "root" {
		h.fail(w, r, 400, "NODE_ACCOUNT_INVALID", "请选择有效的非 root 执行账号")
		return
	}
	t, err := h.store.readTarget(hostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Version != in.HostVersion {
		h.nodeError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err = h.nodeTargetReady(ctx, t); err != nil {
		h.nodeError(w, r, err)
		return
	}
	var candidate deploy.NodeEnvironmentObservation
	c := serviceinstall.NodeContext{EnvironmentID: uuid.NewString(), EnvironmentVersion: "1", Account: in.Account, RootPath: in.RootPath}
	if err = h.store.callTarget(ctx, t, deploy.Request{Action: "NODE_ENVIRONMENT_DISCOVER", NodeEnvironment: &deploy.NodeEnvironmentRequest{Context: c, Action: "DISCOVER"}}, "", &candidate); err != nil {
		h.nodeError(w, r, err)
		return
	}
	candidates := []deploy.NodeEnvironmentObservation{}
	if candidate.Exists {
		candidates = append(candidates, candidate)
	}
	h.success(w, r, map[string]any{"candidates": candidates, "expiresAt": time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)})
}

func (h *HTTP) registerNodeEnvironment(w http.ResponseWriter, r *http.Request, s *session, hostID string) {
	var in struct {
		HostVersion string                            `json:"expectedHostVersion"`
		Name        string                            `json:"name"`
		Candidate   deploy.NodeEnvironmentObservation `json:"candidate"`
	}
	if decode(w, r, &in) != nil || strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 || !deploy.ID.MatchString(in.Candidate.EnvironmentID) || !in.Candidate.Exists || !deploy.Hex.MatchString(in.Candidate.Fingerprint) {
		h.fail(w, r, 400, "INVALID_INPUT", "请选择检测结果并填写环境名称")
		return
	}
	t, err := h.store.readTarget(hostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Version != in.HostVersion {
		h.nodeError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err = h.nodeTargetReady(ctx, t); err != nil {
		h.nodeError(w, r, err)
		return
	}
	var actual deploy.NodeEnvironmentObservation
	c := serviceinstall.NodeContext{EnvironmentID: in.Candidate.EnvironmentID, EnvironmentVersion: "1", Account: in.Candidate.Account, AccountUID: in.Candidate.AccountUID, RootPath: in.Candidate.RootPath}
	if err = h.store.callTarget(ctx, t, deploy.Request{Action: "NODE_ENVIRONMENT_DISCOVER", NodeEnvironment: &deploy.NodeEnvironmentRequest{Context: c, Action: "DISCOVER"}}, "", &actual); err != nil {
		h.nodeError(w, r, err)
		return
	}
	if !nodeObservationMatches(nodeEnvironment{ID: c.EnvironmentID, Account: c.Account, AccountUID: c.AccountUID, RootPath: c.RootPath, ManagerType: actual.ManagerType, ManagementMode: "OBSERVE"}, actual) || !actual.Exists || actual.Fingerprint != in.Candidate.Fingerprint || actual.EnvironmentID != c.EnvironmentID || actual.Account != c.Account || actual.AccountUID != c.AccountUID || actual.RootPath != c.RootPath || actual.ManagerType != "SYSTEM" && actual.ManagerType != "NVM" {
		h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_DRIFT"))
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
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", hostID))
	if err != nil || current.Version != t.Version {
		h.nodeError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	if err = nodeTargetUnlocked(tx, hostID); err != nil {
		h.nodeError(w, r, err)
		return
	}
	var existing string
	if tx.QueryRow("SELECT id FROM node_environments WHERE host_id=? AND account_uid=? AND root_path=? AND management_mode<>'RELEASED'", hostID, actual.AccountUID, actual.RootPath).Scan(&existing) == nil {
		h.success(w, r, map[string]string{"environmentId": existing})
		return
	}
	now := Now()
	_, err = tx.Exec("INSERT INTO node_environments(id,host_id,name,account,account_uid,root_path,manager_type,management_mode,nvm_version,nvm_hash,observed_default_version,observation_json,observed_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'OBSERVE',?,?,?,?,?,?,?)", actual.EnvironmentID, hostID, in.Name, actual.Account, actual.AccountUID, actual.RootPath, actual.ManagerType, actual.NVMVersion, actual.NVMHash, actual.DefaultVersion, jsonText(actual), actual.ObservedAt, now, now)
	for _, v := range actual.Versions {
		if err != nil {
			break
		}
		_, err = tx.Exec("INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,desired_state,actual_state,created_at,updated_at,node_environment_id) VALUES(?,?,?,'NODE',?,'TOOL',?,'OBSERVE','UNCHANGED','INSTALLED',?,?,?)", uuid.NewString(), in.Name+" "+v.Version, hostID, v.Version, v.BinaryPath, now, now, actual.EnvironmentID)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "NODE_ENVIRONMENT_REGISTERED", actual.EnvironmentID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, map[string]string{"environmentId": actual.EnvironmentID})
}

func (h *HTTP) releaseNodeEnvironment(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		Version   string `json:"expectedVersion"`
		Confirmed bool   `json:"confirmed"`
		Name      string `json:"confirmationName"`
	}
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "请确认解除环境管理")
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
	n, err := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", id))
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if n.ManagementMode == "RELEASED" {
		h.success(w, r, map[string]string{"environmentId": id})
		return
	}
	if n.Version != in.Version {
		h.nodeError(w, r, errors.New("NODE_ENVIRONMENT_CHANGED"))
		return
	}
	if !in.Confirmed || in.Name != n.Name {
		h.nodeError(w, r, errors.New("NODE_CONFIRMATION_REQUIRED"))
		return
	}
	if err = nodeTargetUnlocked(tx, n.HostID); err != nil {
		h.nodeError(w, r, err)
		return
	}
	_, err = tx.Exec("UPDATE software_instances SET management_mode='RELEASED',desired_state='UNCHANGED',version=version+1,updated_at=? WHERE node_environment_id=? AND management_mode<>'RELEASED'", Now(), id)
	if err == nil {
		_, err = tx.Exec("UPDATE node_environments SET management_mode='RELEASED',desired_default_software_id=NULL,version=version+1,updated_at=? WHERE id=?", Now(), id)
	}
	if err == nil {
		err = auditTx(tx, s.Username, "NODE_ENVIRONMENT_RELEASED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, map[string]string{"environmentId": id})
}

func validNodeErrorCode(code string) bool {
	return strings.HasPrefix(code, "NODE_") && validDiagnosticCode(code)
}
