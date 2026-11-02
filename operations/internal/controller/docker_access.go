package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type dockerAccessInput struct {
	Account           string `json:"account"`
	Action            string `json:"action"`
	ExpectedStateHash string `json:"expectedStateHash"`
}

func dockerAccessEligible(i softwareInstance) bool {
	return i.ManagementMode == "MANAGED" && i.SoftwareType == "DOCKER" && i.RuntimeKind == "SYSTEMD" && i.Installation != nil && i.Installation.Definition.Key == "docker" && i.Installation.Config.Mode() == "NATIVE" && installationBound(i)
}

var dockerAccessMessages = map[string]string{
	"DOCKER_ACCESS_NOT_MANAGED":               "仅支持管理由Ops安装并确认身份的Docker Engine；容器和外部软件不能分配权限",
	"DOCKER_ACCESS_CONFIRMATION_REQUIRED":     "请明确确认：docker组可通过Docker获得服务器root级权限",
	"DOCKER_ACCESS_STATE_CHANGED":             "账号、Docker组或引擎状态已变化，请重新检查后确认",
	"DOCKER_ACCESS_RESULT_MISMATCH":           "权限变更回执与冻结账号身份不一致，请核对原作业",
	"DOCKER_ACCESS_INPUT_INVALID":             "请选择已有账号、授予或收回操作，并携带当前检查结果",
	"DOCKER_ACCESS_ACCOUNT_INELIGIBLE":        "该账号不符合普通本地登录账号条件",
	"DOCKER_ACCESS_PRIMARY_GROUP":             "docker是该账号的主组，不能按补充组方式收回",
	"DOCKER_ACCESS_SOCKET_NOT_READY":          "标准Docker socket尚未就绪，不能授予账号访问权限",
	"DOCKER_ACCESS_ALREADY_SET":               "该账号已处于所选权限状态，请重新检查",
	"DOCKER_ACCESS_ENGINE_REQUIRED":           "请先确认这是Ops安装并登记的Docker Engine",
	"DOCKER_ACCESS_ENGINE_IDENTITY_MISMATCH":  "Docker Engine身份与安装记录不一致，请核对原安装记录",
	"DOCKER_ACCESS_CUSTOM_SOCKET_UNSUPPORTED": "当前仅支持标准系统Docker socket，自定义socket和rootless不在本次权限管理范围内",
	"DOCKER_ACCOUNT_RANGE_UNAVAILABLE":        "无法确认本地普通账号范围，请检查/etc/login.defs中的UID_MIN与UID_MAX",
	"DOCKER_LOCAL_GROUPS_UNAVAILABLE":         "本地组信息未能完整读取，请检查getent -s files group的结果",
	"DOCKER_LOCAL_GROUPS_INVALID":             "本地组信息格式或身份有冲突，请由管理员核对现有组记录",
	"DOCKER_LOCAL_GROUP_MISSING":              "尚未确认本地docker组，请核对Docker Engine安装结果",
	"DOCKER_LOCAL_ACCOUNTS_UNAVAILABLE":       "本地账号信息未能完整读取，请检查getent -s files passwd的结果",
	"DOCKER_LOCAL_ACCOUNTS_INVALID":           "本地普通账号记录重复或格式不完整，请由管理员核对现有账号",
	"DOCKER_SOCKET_OBSERVATION_INVALID":       "标准Docker socket信息未能确认，请检查socket类型、所有者和权限",
	"DOCKER_ACCESS_OBSERVATION_LIMIT":         "账号权限快照超过安全大小限制，本次未执行变更",
	"DOCKER_ACCESS_OBSERVATION_INVALID":       "账号权限快照格式未能确认，请重新检查",
	"DOCKER_ACCESS_SOCKET_INVALID":            "Docker socket身份与组信息不一致，请重新检查",
	"DOCKER_ACCESS_ACCOUNT_INVALID":           "账号身份或登录条件未能确认，请重新检查",
	"DOCKER_ACCESS_GROUP_INVALID":             "账号组身份未能确认，请重新检查",
	"DOCKER_ACCESS_MEMBERSHIP_INVALID":        "账号组成员关系未能确认，请重新检查",
	"EXECUTOR_DOCKER_ACCESS_UNSUPPORTED":      "接入组件尚未支持Docker账号权限，请在此更新组件后重新检查",
}

func (h *HTTP) dockerAccessError(w http.ResponseWriter, r *http.Request, err error) {
	code := err.Error()
	message, known := dockerAccessMessages[code]
	if !known {
		switch code {
		case "VERSION_CONFLICT", "HOST_CHANGED", "HOST_MAINTENANCE", "OPERATION_LOCKED", "QUEUE_FULL", "IDEMPOTENCY_CONFLICT":
			message = "主机、软件或作业状态已变化，请刷新并核对原操作"
		default:
			code = executorFailureCode(err)
			message = executorFailureMessages[code]
			if message == "" {
				message = "Docker账号权限检查未完成，请查看错误详情"
			}
		}
	}
	h.fail(w, r, 409, code, message)
}

func (h *HTTP) readDockerAccess(ctx context.Context, t target, i softwareInstance) (deploy.DockerAccessObservation, error) {
	var o deploy.DockerAccessObservation
	if !dockerAccessEligible(i) {
		return o, errors.New("DOCKER_ACCESS_NOT_MANAGED")
	}
	var cap executorCapabilities
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &cap); err != nil {
		return o, err
	}
	if cap.Owner.ControllerID != h.store.controllerID() || cap.Owner.TargetID != t.ID || cap.Owner.ProtocolVersion != 1 || cap.Owner.ExecutorVersion != deploy.Version {
		return o, errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if cap.DockerAccessVersion != deploy.DockerAccessCapabilityVersion {
		return o, errors.New("EXECUTOR_DOCKER_ACCESS_UNSUPPORTED")
	}
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "SOFTWARE_DOCKER_ACCESS_CHECK", Software: softwareRequest(i)}, "", &o); err != nil {
		return o, err
	}
	if o.Validate() != nil || o.SoftwareID != i.ID || o.EngineIdentityHash != i.IdentityHash {
		return o, errors.New("DOCKER_ACCESS_RESULT_MISMATCH")
	}
	return o, nil
}

func (h *HTTP) dockerAccess(w http.ResponseWriter, r *http.Request, id string) {
	i, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if !dockerAccessEligible(i) {
		h.dockerAccessError(w, r, errors.New("DOCKER_ACCESS_NOT_MANAGED"))
		return
	}
	t, err := h.store.readTarget(i.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	o, err := h.readDockerAccess(ctx, t, i)
	if err != nil {
		h.dockerAccessError(w, r, err)
		return
	}
	current, err := h.store.readSoftware(id)
	if err != nil || current.Version != i.Version {
		h.dockerAccessError(w, r, errors.New("VERSION_CONFLICT"))
		return
	}
	currentHost, err := h.store.readTarget(t.ID)
	if err != nil || currentHost.Version != t.Version {
		h.dockerAccessError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	h.success(w, r, map[string]any{"softwareId": id, "expectedVersion": i.Version, "expectedHostVersion": t.Version, "sshAccount": t.User, "lastJobId": i.LastJobID, "observation": o})
}

func (h *HTTP) changeDockerAccess(w http.ResponseWriter, r *http.Request, actor *session, id, version, hostVersion, key string, confirmed bool, in *dockerAccessInput) {
	if in == nil || !deploy.Hex.MatchString(in.ExpectedStateHash) || (in.Action != "GRANT" && in.Action != "REVOKE") || in.Account == "" || hostVersion == "" {
		h.dockerAccessError(w, r, errors.New("DOCKER_ACCESS_INPUT_INVALID"))
		return
	}
	if !confirmed {
		h.dockerAccessError(w, r, errors.New("DOCKER_ACCESS_CONFIRMATION_REQUIRED"))
		return
	}
	hash := deploy.Digest(struct {
		ID, Version, HostVersion string
		Confirmed                bool
		Change                   dockerAccessInput
	}{id, version, hostVersion, confirmed, *in})
	const action = "SOFTWARE_DOCKER_ACCESS"
	if h.replaySoftwareChange(w, r, actor, id, action, key, hash) {
		return
	}
	i, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if !dockerAccessEligible(i) {
		h.dockerAccessError(w, r, errors.New("DOCKER_ACCESS_NOT_MANAGED"))
		return
	}
	if i.Version != version {
		h.dockerAccessError(w, r, errors.New("VERSION_CONFLICT"))
		return
	}
	t, err := h.store.readTarget(i.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Version != hostVersion {
		h.dockerAccessError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	if t.Maintenance {
		h.dockerAccessError(w, r, errors.New("HOST_MAINTENANCE"))
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	o, err := h.readDockerAccess(ctx, t, i)
	if err != nil {
		h.dockerAccessError(w, r, err)
		return
	}
	if o.Fingerprint != in.ExpectedStateHash {
		h.dockerAccessError(w, r, errors.New("DOCKER_ACCESS_STATE_CHANGED"))
		return
	}
	nextVersion, err := strconv.ParseUint(i.Version, 10, 64)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	change := &deploy.DockerAccessChange{Account: in.Account, Action: in.Action, Expected: o, ActorGeneration: actor.Generation, ExpectedHostVersion: t.Version, ExpectedSoftwareVersion: strconv.FormatUint(nextVersion+1, 10)}
	if err = change.Validate(); err != nil {
		h.dockerAccessError(w, r, err)
		return
	}
	for _, account := range o.Accounts {
		if account.Name == in.Account && account.Member == (in.Action == "GRANT") {
			h.dockerAccessError(w, r, errors.New("DOCKER_ACCESS_ALREADY_SET"))
			return
		}
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	var old, oldHash string
	err = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind=? AND idempotency_key=?", actor.AdminID, action, key).Scan(&old, &oldHash)
	if err == nil {
		if oldHash != hash {
			h.dockerAccessError(w, r, errors.New("IDEMPOTENCY_CONFLICT"))
		} else {
			h.success(w, r, map[string]string{"jobId": old, "softwareInstanceId": id})
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	current, err := scanSoftware(tx.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", id))
	if err != nil || current.Version != version || !dockerAccessEligible(current) || current.IdentityHash != i.IdentityHash {
		h.dockerAccessError(w, r, errors.New("VERSION_CONFLICT"))
		return
	}
	var actualHostVersion string
	var maintenance bool
	if err = tx.QueryRow("SELECT CAST(version AS TEXT),maintenance FROM targets WHERE id=?", t.ID).Scan(&actualHostVersion, &maintenance); err != nil {
		h.internal(w, r, err)
		return
	}
	if actualHostVersion != hostVersion || maintenance {
		h.dockerAccessError(w, r, errors.New("HOST_CHANGED"))
		return
	}
	busy, err := hostBusy(tx, t.ID)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if busy {
		h.dockerAccessError(w, r, errors.New("OPERATION_LOCKED"))
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); err != nil {
		h.internal(w, r, err)
		return
	}
	if count >= 20 {
		h.dockerAccessError(w, r, errors.New("QUEUE_FULL"))
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: t.ID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: action, SoftwareOperation: action, Software: softwareRequest(i), DockerAccess: change, DeadlineAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	req.InputHash = deploy.Digest(req)
	_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", job, actor.AdminID, action, t.ID, key, hash, jsonText(req), req.TraceID, now, now)
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?),(?,?)", "target:"+t.ID, job, "software:"+id, job)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE software_instances SET last_job_id=?,version=version+1,error_code='',updated_at=? WHERE id=?", job, now, id)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, action+"_"+in.Action+"_ACCEPTED", id, "SUCCESS", req.TraceID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job, "softwareInstanceId": id})
}
