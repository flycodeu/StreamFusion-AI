package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type projectDeploymentSnapshot struct {
	ProjectID      string                   `json:"projectId"`
	ProjectVersion string                   `json:"projectVersion"`
	HostID         string                   `json:"hostId"`
	HostVersion    string                   `json:"hostVersion"`
	ModuleID       string                   `json:"moduleId"`
	BuildNumber    int                      `json:"buildNumber"`
	Artifact       projectBuildArtifact     `json:"artifact"`
	Spec           deploy.ProjectDeployment `json:"spec"`
}

type projectDeploymentJob struct {
	JobID       string `json:"jobId"`
	Action      string `json:"action"`
	Status      string `json:"status"`
	Phase       string `json:"phase"`
	ErrorCode   string `json:"errorCode,omitempty"`
	Health      string `json:"health,omitempty"`
	ObservedAt  string `json:"observedAt"`
	TraceID     string `json:"traceId"`
	BuildNumber int    `json:"buildNumber,omitempty"`
	Commit      string `json:"commit,omitempty"`
	ReleaseID   string `json:"releaseId,omitempty"`
}

type projectDeploymentVersion struct {
	ReleaseID      string `json:"releaseId"`
	BuildNumber    int    `json:"buildNumber"`
	Commit         string `json:"commit"`
	ObservedAt     string `json:"observedAt"`
	Kind           string `json:"kind"`
	TargetID       string `json:"targetId"`
	HostName       string `json:"hostName"`
	Address        string `json:"address,omitempty"`
	Port           int    `json:"port"`
	ServerName     string `json:"serverName,omitempty"`
	SiteHealthPath string `json:"siteHealthPath,omitempty"`
}

const projectJobSummaryFields = `id,action,status,phase,error_code,health,updated_at,trace_id,build_number,commit_id,release_id`
const projectObservationValidity = 5 * time.Minute

func scanProjectJob(row *sql.Row) (job projectDeploymentJob, err error) {
	err = row.Scan(&job.JobID, &job.Action, &job.Status, &job.Phase, &job.ErrorCode, &job.Health,
		&job.ObservedAt, &job.TraceID, &job.BuildNumber, &job.Commit, &job.ReleaseID)
	return
}

func readProjectVersion(q applicationReader, id string) (*projectDeploymentVersion, error) {
	if id == "" {
		return nil, nil
	}
	var v projectDeploymentVersion
	err := q.QueryRow(`SELECT id,build_number,commit_id,updated_at,coalesce(target_id,''),host_name,
		coalesce(json_extract(snapshot_json,'$.spec.kind'),''),coalesce(json_extract(snapshot_json,'$.spec.address'),''),
		coalesce(json_extract(snapshot_json,'$.spec.port'),0),coalesce(json_extract(snapshot_json,'$.spec.site.serverName'),''),
		coalesce(json_extract(snapshot_json,'$.spec.site.healthPath'),'')
		FROM project_deployment_jobs WHERE id=? AND action='DEPLOY' AND status='SUCCEEDED'`, id).
		Scan(&v.ReleaseID, &v.BuildNumber, &v.Commit, &v.ObservedAt, &v.TargetID, &v.HostName, &v.Kind, &v.Address, &v.Port, &v.ServerName, &v.SiteHealthPath)
	return &v, err
}

func readProjectVersions(q applicationReader, id string) (current, previous string, err error) {
	err = q.QueryRow(`SELECT current_id,coalesce(previous_id,'') FROM project_deployment_running WHERE deployment_id=?`, id).Scan(&current, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

var projectDeploymentErrors = map[string]string{
	"PROJECT_MODE_UNSUPPORTED":                           "当前模式不支持 Jenkins 部署，请使用 Java 原生服务或前端静态站点",
	"PROJECT_HOST_UNAVAILABLE":                           "请为此环境配置已接入且未处于维护模式的目标主机",
	"PROJECT_HOST_BUSY":                                  "此主机已有部署或维护作业，请核对作业记录",
	"PROJECT_DEPLOYMENT_BUSY":                            "此部署有未结束的作业，请先核对运行状态",
	"PROJECT_QUEUE_FULL":                                 "部署队列已满，请等待现有作业结束",
	"PROJECT_PLAN_EXPIRED":                               "部署确认已过期，请重新预检",
	"PROJECT_PLAN_CHANGED":                               "项目、主机或当前发布版本已变化，请重新预检",
	"PROJECT_CURRENT_ARTIFACT_CHANGED":                   "当前构建的归档与在运行的制品不同，请核对 Jenkins 后重新选择构建",
	"PROJECT_NO_ROLLBACK":                                "没有可回滚的上一个版本",
	"PROJECT_ROLLBACK_PREVIOUS_CHANGED":                  "可回滚版本已变化，请刷新后重新确认",
	"PROJECT_CREDENTIAL_UNAVAILABLE":                     "配置引用的凭据不可用，请检查草稿和凭据中心",
	"PROJECT_NGINX_CONFIG_INVALID":                       "请填写有效的站点域名和监听端口",
	"PROJECT_NGINX_NATIVE_REQUIRED":                      "选择同一主机的系统 Nginx，要求 nginx.service 和 conf.d 配置",
	"PROJECT_NGINX_SITE_CONFLICT":                        "同一主机已有使用相同监听端口和域名的站点",
	"PROJECT_NGINX_CONF_D_REQUIRED":                      "目标主机 Nginx 主配置未加载 /etc/nginx/conf.d/*.conf",
	"PROJECT_NGINX_EXISTING_CONFIG_INVALID":              "目标主机当前 Nginx 配置检查未通过",
	"PROJECT_NGINX_SERVICE_REQUIRED":                     "目标主机 nginx.service 未运行",
	"PROJECT_PROXY_TARGET_UNAVAILABLE":                   "同环境后端未发布或地址不可达，请核对后端运行版本及监听地址",
	"PROJECT_PROXY_TARGET_AMBIGUOUS":                     "同环境有多个已发布的后端实例，请使用明确的上游地址",
	"PROJECT_STREAMFUSION_API_UNHEALTHY":                 "站点反代未返回后端健康状态，请检查 /api/ 路由、后端地址和运行状态",
	"PROJECT_STREAMFUSION_API_ROUTE_REQUIRED":            "StreamFusion 站点需要 /api/ 去前缀反代，并关联同环境的 platform-api",
	"PROJECT_STREAMFUSION_SITE_NAME_REQUIRED":            "请在站点草稿中填写实际访问使用的域名或 IP 地址",
	"PROJECT_STREAMFUSION_ROLLBACK_CONTRACT_CHANGED":     "上一版本缺少当前的 /api 健康检查。请修正站点配置并重新发布要恢复的构建",
	"PROJECT_STREAMFUSION_ROLLBACK_UPSTREAM_CHANGED":     "上一版本的 /api 地址与当前运行后端不同。请用当前后端配置重新发布要恢复的构建",
	"PROJECT_STREAMFUSION_WEB_ARTIFACT_REBUILD_REQUIRED": "此制品未确认支持 /api 反代。请用新版 Jenkins 任务重新构建 Platform Web 并归档后再发布",
	"PROJECT_ACTIVE_SITE_USES_BACKEND":                   "同环境站点仍使用此后端，请先将站点切换到新的后端或下线站点",
	"PROJECT_ACTIVE_SITE_USES_OLD_ENDPOINT":              "同环境站点仍使用后端当前地址，请先切换站点，再更改端口或监听地址",
	"PROJECT_PROXY_INVALID":                              "请检查反向代理路径和 HTTP/HTTPS 上游地址",
	"PROJECT_PROXY_TRAILING_SLASH_REQUIRED":              "反代需要去掉前缀时，匹配路径应以 / 结尾",
	"PROJECT_JAVA_CONFIG_INVALID":                        "请检查 Java 服务的 IPv4 监听地址、端口、内存、Profile 和健康检查路径",
	"PROJECT_JAVA_VARIABLE_INVALID":                      "请检查 Java 环境变量的名称、长度及凭据引用",
	"PROJECT_JAVA_21_REQUIRED":                           "目标主机 /usr/bin/java 须指向 Java 21",
	"PROJECT_SYSTEMD_REQUIRED":                           "目标主机需要 systemd",
	"PROJECT_DEPENDENCY_ENDPOINT_MISSING":                "关联软件缺少唯一可探测端口，请在软件状态中核对",
	"PROJECT_DEPENDENCY_UNREACHABLE":                     "目标主机不能连通登记的依赖，请检查地址、端口和防火墙",
	"PROJECT_EXECUTOR_UPGRADE_REQUIRED":                  "请在主机管理中更新受管执行器至项目发布能力 v3；旧版不能保证检查只读",
	"PROJECT_JOB_INTERRUPTED":                            "控制端未确认作业终态，请先检查现场；如有待恢复作业，再单独确认恢复",
	"PROJECT_CHECK_INTERRUPTED":                          "上次检查未完成，请重新检查",
	"PROJECT_EXECUTOR_NOT_TRUSTED":                       "主机未信任当前控制器，请先完成执行器接入",
	"PROJECT_TARGET_UNAVAILABLE":                         "无法读取目标主机的执行结果，请检查 SSH 与受管执行器",
	"PROJECT_RECOVERY_REQUIRED":                          "目标尚有未确认的部署变更，请核对现场后确认恢复",
	"PROJECT_RECOVERY_JOB_CHANGED":                       "待恢复作业已变化，请刷新后重新确认",
	"PROJECT_DECOMMISSION_RELEASE_CHANGED":               "当前版本已变化，请刷新后重新确认下线",
	"PROJECT_NOT_DEPLOYED":                               "此部署还没有运行版本",
	"PROJECT_LOGS_UNAVAILABLE":                           "目标主机无法读取服务日志",
	"PROJECT_RELEASE_CHANGED":                            "目标主机的实际版本与 Ops 记录不同，请先核对状态",
	"PROJECT_ARTIFACT_INVALID":                           "构建制品超出此部署支持的大小或格式",
}

func (h *HTTP) projectDeployError(w http.ResponseWriter, r *http.Request, err error) {
	if msg := projectDeploymentErrors[err.Error()]; msg != "" {
		h.fail(w, r, http.StatusConflict, err.Error(), msg)
		return
	}
	h.jenkinsError(w, r, err)
}

func (h *HTTP) projectDeploymentRoute(w http.ResponseWriter, r *http.Request, actor *session, projectID, id string, rest []string) bool {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		h.projectDeploymentStatus(w, r, projectID, id)
	case len(rest) == 1 && rest[0] == "logs" && r.Method == http.MethodGet:
		h.projectDeploymentLogs(w, r, projectID, id)
	case len(rest) == 1 && rest[0] == "preflight" && r.Method == http.MethodPost:
		h.projectDeploymentPreflight(w, r, actor, projectID, id)
	case len(rest) == 1 && rest[0] == "submit" && r.Method == http.MethodPost:
		var input struct {
			Token string `json:"token"`
		}
		if decode(w, r, &input) != nil || !deploy.ID.MatchString(input.Token) {
			h.fail(w, r, 400, "INVALID_INPUT", "请使用当前预检返回的确认单")
			return true
		}
		h.queueProjectDeployment(w, r, actor, projectID, id, "DEPLOY", input.Token)
	case len(rest) == 1 && rest[0] == "verify" && r.Method == http.MethodPost:
		var input struct{}
		if decode(w, r, &input) != nil {
			h.fail(w, r, 400, "INVALID_INPUT", "请提交检查请求")
			return true
		}
		h.queueProjectDeployment(w, r, actor, projectID, id, "VERIFY", "")
	case len(rest) == 1 && rest[0] == "rollback" && r.Method == http.MethodPost:
		var input struct {
			PreviousReleaseID string `json:"previousReleaseId"`
		}
		if decode(w, r, &input) != nil || !deploy.ID.MatchString(input.PreviousReleaseID) {
			h.fail(w, r, 400, "INVALID_INPUT", "请刷新部署记录，确认要恢复的版本")
			return true
		}
		h.queueProjectDeployment(w, r, actor, projectID, id, "ROLLBACK", input.PreviousReleaseID)
	case len(rest) == 1 && rest[0] == "recover" && r.Method == http.MethodPost:
		var input struct {
			ExpectedJobID string `json:"expectedJobId"`
		}
		if decode(w, r, &input) != nil || !deploy.ID.MatchString(input.ExpectedJobID) {
			h.fail(w, r, 400, "INVALID_INPUT", "请确认要恢复的待核对作业")
			return true
		}
		h.queueProjectDeployment(w, r, actor, projectID, id, "RECOVER", input.ExpectedJobID)
	case len(rest) == 1 && rest[0] == "decommission" && r.Method == http.MethodPost:
		var input struct {
			ExpectedReleaseID string `json:"expectedReleaseId"`
		}
		if decode(w, r, &input) != nil || !deploy.ID.MatchString(input.ExpectedReleaseID) {
			h.fail(w, r, 400, "INVALID_INPUT", "请确认要下线的当前版本")
			return true
		}
		h.queueProjectDeployment(w, r, actor, projectID, id, "DECOMMISSION", input.ExpectedReleaseID)
	default:
		return false
	}
	return true
}

func (h *HTTP) projectDeploymentStatus(w http.ResponseWriter, r *http.Request, projectID, id string) {
	tx, err := h.store.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	kind, err := readProjectDeploymentKind(tx, projectID, id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	data := map[string]any{"health": "NOT_DEPLOYED"}
	if kind != "" {
		data["kind"] = kind
	}
	current, previous, err := readProjectVersions(tx, id)
	if err == nil && current != "" {
		data["health"] = "UNKNOWN"
		data["current"], err = readProjectVersion(tx, current)
	}
	if previous != "" && err == nil {
		data["previous"], err = readProjectVersion(tx, previous)
	}
	if err == nil {
		err = readProjectDeploymentActivity(tx, id, current, data)
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, data)
}

// Status polling needs the kind and the release records for one registration,
// not the full project configuration used when planning changes.
func readProjectDeploymentKind(q applicationReader, projectID, id string) (string, error) {
	var moduleKind, deployMode, managedInstanceID string
	err := q.QueryRow(`SELECT coalesce(json_extract(m.config_json,'$.catalog.kind'),''),
		coalesce(json_extract(d.config_json,'$.deployMode'),''),
		coalesce(json_extract(d.config_json,'$.managedInstanceId'),'')
		FROM application_deployments d
		JOIN application_modules m ON m.project_id=d.project_id AND m.id=d.module_id
		WHERE d.project_id=? AND d.id=?`, projectID, id).Scan(&moduleKind, &deployMode, &managedInstanceID)
	if err != nil {
		return "", err
	}
	return projectDeploymentKind(moduleKind, deployMode, managedInstanceID), nil
}

func readProjectDeploymentActivity(q applicationReader, id, current string, data map[string]any) error {
	read := func(field, conditions string, args ...any) (projectDeploymentJob, error) {
		job, err := scanProjectJob(q.QueryRow("SELECT "+projectJobSummaryFields+` FROM project_deployment_jobs
			WHERE deployment_id=? `+conditions, append([]any{id}, args...)...))
		if err == nil {
			data[field] = job
		} else if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		return job, err
	}
	if _, err := read("latest", "ORDER BY created_at DESC,id DESC LIMIT 1"); err != nil {
		return err
	}
	pending, err := read("pending", "AND action<>'VERIFY' AND status IN ('QUEUED','RUNNING','UNKNOWN')")
	if err != nil {
		return err
	}
	check, err := read("lastCheck", `AND action='VERIFY' AND baseline_id=? AND status IN ('SUCCEEDED','FAILED')
		ORDER BY updated_at DESC,id DESC LIMIT 1`, current)
	if err != nil {
		return err
	}
	if _, err = read("activeCheck", "AND action='VERIFY' AND status IN ('QUEUED','RUNNING')"); err != nil {
		return err
	}
	if pending.JobID != "" {
		data["health"] = "UNKNOWN"
	}
	if current == "" {
		delete(data, "lastCheck")
		if pending.JobID != "" && check.JobID != "" && check.ReleaseID == pending.ReleaseID {
			data["lastCheck"] = check
			data["healthObservedAt"] = check.ObservedAt
		}
		return nil
	}
	var health, observed string
	err = q.QueryRow(`SELECT health,updated_at FROM project_deployment_jobs WHERE deployment_id=?
		AND release_id=? AND action IN ('DEPLOY','ROLLBACK') AND status='SUCCEEDED'
		ORDER BY updated_at DESC,id DESC LIMIT 1`, id, current).Scan(&health, &observed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if check.JobID != "" && check.ObservedAt >= observed {
		health, observed = check.Health, check.ObservedAt
		if check.Status == "FAILED" {
			health = "UNKNOWN"
		}
	} else {
		delete(data, "lastCheck")
	}
	if observed != "" {
		data["healthObservedAt"] = observed
	}
	if pending.JobID == "" && (health == "READY" || health == "UNHEALTHY") {
		t, parseErr := time.Parse(time.RFC3339Nano, observed)
		if parseErr != nil || time.Since(t) > projectObservationValidity {
			health = "STALE"
		}
		data["health"] = health
	}
	return nil
}

func (h *HTTP) projectTargetCapabilities(ctx context.Context, host target) error {
	var cap executorCapabilities
	if err := h.store.callTarget(ctx, host, deploy.Request{Action: "CAPABILITIES"}, "", &cap); err != nil {
		if err.Error() == "EXECUTOR_UPGRADE_REQUIRED" {
			return errors.New("PROJECT_EXECUTOR_UPGRADE_REQUIRED")
		}
		return errors.New("PROJECT_TARGET_UNAVAILABLE")
	}
	if cap.ProjectDeployVersion < deploy.ProjectDeployVersion {
		return errors.New("PROJECT_EXECUTOR_UPGRADE_REQUIRED")
	}
	if cap.Owner.ControllerID != h.store.controllerID() || cap.Owner.TargetID != host.ID ||
		cap.CommandTrustFingerprint != deploy.CommandPublicKeyFingerprint(h.store.commandPublicKey()) {
		return errors.New("PROJECT_EXECUTOR_NOT_TRUSTED")
	}
	return nil
}

func projectTargetRequest(action, jobID, trace string, spec deploy.ProjectDeployment) deploy.Request {
	if action != deploy.ProjectApply {
		spec.Files, spec.Variables = nil, nil
	}
	return deploy.Request{Action: action, JobID: jobID, TraceID: trace, ProjectDeployment: &spec}
}

func (h *HTTP) projectDeploymentPreflight(w http.ResponseWriter, r *http.Request, actor *session, projectID, id string) {
	var input struct {
		BuildNumber      int    `json:"buildNumber"`
		CurrentReleaseID string `json:"currentReleaseId"`
	}
	if decode(w, r, &input) != nil || input.BuildNumber < 1 || input.BuildNumber > 1<<31-1 ||
		input.CurrentReleaseID != "" && !deploy.ID.MatchString(input.CurrentReleaseID) {
		h.fail(w, r, 400, "INVALID_INPUT", "请指定一个已成功的 Jenkins 构建")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 80*time.Second)
	defer cancel()
	p, spec, err := h.loadProjectDeployPlan(ctx, projectID, id)
	if err != nil {
		h.projectDeployError(w, r, err)
		return
	}
	streamFusionWeb := spec.Site != nil && spec.Site.HealthPath == deploy.StreamFusionSiteHealthPath
	var runningArtifact projectBuildArtifact
	if input.CurrentReleaseID != "" {
		runningArtifact, err = currentStaticProjectArtifact(h.store.DB, spec, input.CurrentReleaseID, input.BuildNumber)
		if err == nil && streamFusionWeb {
			err = validateStreamFusionWebArtifact(runningArtifact)
		}
		if err != nil {
			h.projectDeployError(w, r, err)
			return
		}
	}
	artifact, err := h.fetchProjectReleaseArtifact(ctx, p.project, p.module, input.BuildNumber, nil)
	if streamFusionWeb {
		if err == nil {
			err = validateStreamFusionWebArtifact(artifact)
		} else if errors.Is(err, errJenkinsMissing) {
			err = errStreamFusionWebArtifactRebuildRequired
		}
	}
	if err != nil {
		h.projectDeployError(w, r, err)
		return
	}
	if input.CurrentReleaseID != "" && artifact != runningArtifact {
		h.projectDeployError(w, r, errors.New("PROJECT_CURRENT_ARTIFACT_CHANGED"))
		return
	}
	trace := r.Context().Value(traceKey{}).(string)
	token := uuid.NewString()
	spec.ReleaseID = uuid.NewString()
	spec.ArtifactSHA256, spec.ArtifactSize = artifact.SHA256, artifact.Size
	if err = deploy.ValidateProjectDeployment(&spec, deploy.ProjectApply); err == nil {
		err = h.probeProjectDeployment(ctx, p.host, spec, token, trace)
	}
	if err != nil {
		h.projectDeployError(w, r, err)
		return
	}
	snap := projectDeploymentSnapshot{projectID, p.project.Version, p.host.ID, p.host.Version, p.module.ID,
		input.BuildNumber, artifact, spec}
	if err = h.storeProjectDeployPlan(actor, id, token, trace, snap); err != nil {
		h.projectDeployError(w, r, err)
		return
	}
	h.success(w, r, map[string]any{"token": token, "kind": spec.Kind, "buildNumber": input.BuildNumber,
		"commit": artifact.Commit, "hostId": p.host.ID, "hostName": p.host.Name, "environment": p.env.Name,
		"moduleName": p.module.Name, "summary": projectSpecSummary(spec)})
}

func currentStaticProjectArtifact(q applicationReader, spec deploy.ProjectDeployment, releaseID string, build int) (projectBuildArtifact, error) {
	if spec.Kind != "STATIC" || spec.ExpectedRelease != releaseID {
		return projectBuildArtifact{}, errors.New("PROJECT_PLAN_CHANGED")
	}
	running, err := readProjectJobSnapshot(q, releaseID)
	if err != nil {
		return projectBuildArtifact{}, err
	}
	if running.BuildNumber != build || running.Spec.ID != spec.ID || running.Spec.Kind != "STATIC" {
		return projectBuildArtifact{}, errors.New("PROJECT_PLAN_CHANGED")
	}
	return running.Artifact, nil
}

func (h *HTTP) loadProjectDeployPlan(ctx context.Context, projectID, id string) (p projectDeploymentContext, spec deploy.ProjectDeployment, err error) {
	tx, err := h.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, spec, err
	}
	defer tx.Rollback()
	if p, err = readProjectDeployment(tx, projectID, id); err != nil {
		return
	}
	if p.project.Archived {
		return p, spec, errJenkinsArchived
	}
	if err = checkProjectDeploymentLocks(tx, id, p.host.ID, true); err != nil {
		return
	}
	current, _, err := readProjectVersions(tx, id)
	if err != nil {
		return p, spec, err
	}
	spec, err = projectDeploySpec(tx, p, current)
	return
}

func (h *HTTP) probeProjectDeployment(ctx context.Context, t target, spec deploy.ProjectDeployment, id, trace string) error {
	select {
	case h.hostCheckSlots <- struct{}{}:
		defer func() { <-h.hostCheckSlots }()
	default:
		return errJenkinsBusy
	}
	if err := h.projectTargetCapabilities(ctx, t); err != nil {
		return err
	}
	var result deploy.ProjectDeployResult
	if err := h.store.callTarget(ctx, t, projectTargetRequest(deploy.ProjectProbe, id, trace, spec), "", &result); err != nil {
		return errors.New("PROJECT_TARGET_UNAVAILABLE")
	}
	if result.Status != "SUCCEEDED" || result.ReleaseID != spec.ExpectedRelease {
		if projectDeploymentErrors[result.ErrorCode] != "" {
			return errors.New(result.ErrorCode)
		}
		return errors.New("PROJECT_RELEASE_CHANGED")
	}
	return nil
}

func (h *HTTP) storeProjectDeployPlan(actor *session, id, token, trace string, snap projectDeploymentSnapshot) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = validateProjectDeploySnapshot(tx, snap); err != nil {
		return err
	}
	if err = checkProjectDeploymentLocks(tx, id, snap.HostID, true); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM project_deployment_plans WHERE expires_at<=? AND job_id IS NULL", Now()); err == nil {
		expiry := time.Now().UTC().Add(5 * time.Minute).Format("2006-01-02T15:04:05.000000000Z")
		_, err = tx.Exec(`INSERT INTO project_deployment_plans(token_hash,actor_id,deployment_id,snapshot_json,expires_at)
			VALUES(?,?,?,?,?)`, hashToken(token), actor.AdminID, id, jsonText(snap), expiry)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, "PROJECT_DEPLOY_PREFLIGHT", id, "SUCCESS", trace)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func validateProjectDeploySnapshot(q applicationReader, snap projectDeploymentSnapshot) (projectDeploymentContext, error) {
	p, err := readProjectDeployment(q, snap.ProjectID, snap.Spec.ID)
	if err != nil {
		return p, err
	}
	if p.project.Archived || p.project.Version != snap.ProjectVersion || p.host.ID != snap.HostID ||
		p.host.Version != snap.HostVersion || p.host.Maintenance || p.module.ID != snap.ModuleID {
		return p, errors.New("PROJECT_PLAN_CHANGED")
	}
	current, _, err := readProjectVersions(q, snap.Spec.ID)
	if err != nil {
		return p, err
	}
	if current != snap.Spec.ExpectedRelease {
		return p, errors.New("PROJECT_PLAN_CHANGED")
	}
	spec, err := projectDeploySpec(q, p, current)
	if err != nil {
		return p, err
	}
	spec.ReleaseID, spec.ArtifactSHA256, spec.ArtifactSize = snap.Spec.ReleaseID, snap.Spec.ArtifactSHA256, snap.Spec.ArtifactSize
	if deploy.Digest(spec) != deploy.Digest(snap.Spec) {
		return p, errors.New("PROJECT_PLAN_CHANGED")
	}
	return p, nil
}

func checkProjectDeploymentLocks(q applicationReader, id, hostID string, mutate bool) error {
	var locked bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_deployment_jobs
		WHERE deployment_id=? AND (status IN ('QUEUED','RUNNING') OR (? AND action<>'VERIFY' AND status='UNKNOWN')))`, id, mutate).Scan(&locked)
	if err != nil {
		return err
	}
	if locked {
		return errors.New("PROJECT_DEPLOYMENT_BUSY")
	}
	if !mutate {
		return nil
	}
	err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM operation_locks WHERE resource_key='target:'||?) OR EXISTS(
		SELECT 1 FROM project_deployment_jobs WHERE target_id=? AND action<>'VERIFY'
		AND status IN ('QUEUED','RUNNING','UNKNOWN'))`, hostID, hostID).Scan(&locked)
	if locked {
		return errors.New("PROJECT_HOST_BUSY")
	}
	return err
}
