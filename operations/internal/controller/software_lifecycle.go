package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) changeSoftware(w http.ResponseWriter, r *http.Request, s *session, id, operation string) {
	var in struct {
		ExpectedVersion  string `json:"expectedVersion"`
		IdentityHash     string `json:"identityHash"`
		Confirmed        bool   `json:"confirmed"`
		Key              string `json:"idempotencyKey"`
		Action           string `json:"action"`
		DeleteData       bool   `json:"deleteData"`
		ConfirmationName string `json:"confirmationName"`
	}
	if e := decode(w, r, &in); e != nil {
		h.fail(w, r, 400, "INVALID_INPUT", e.Error())
		return
	}
	if _, e := strconv.ParseUint(in.ExpectedVersion, 10, 64); e != nil || len(strings.TrimSpace(in.Key)) < 8 || len(in.Key) > 80 {
		h.fail(w, r, 400, "VERSION_AND_IDEMPOTENCY_REQUIRED", "请携带当前版本与8–80字节提交标识")
		return
	}
	action := "SOFTWARE_" + strings.ToUpper(operation)
	if operation == "actions" {
		if in.Action != "START" && in.Action != "STOP" && in.Action != "UNINSTALL" {
			h.fail(w, r, 400, "SOFTWARE_ACTION_INVALID", "仅支持启动、停止或卸载")
			return
		}
		action = "SOFTWARE_" + in.Action
	}
	if action != "SOFTWARE_UNINSTALL" && (in.DeleteData || in.ConfirmationName != "") {
		h.fail(w, r, 400, "SOFTWARE_DELETE_DATA_ACTION_INVALID", "数据删除选项仅适用于卸载")
		return
	}
	hash := deploy.Digest(struct {
		ID, Version, Action, Identity string
		Confirmed                     bool
	}{id, in.ExpectedVersion, action, in.IdentityHash, in.Confirmed})
	if action == "SOFTWARE_UNINSTALL" {
		hash = deploy.Digest(struct {
			Base             string
			DeleteData       bool
			ConfirmationName string
		}{hash, in.DeleteData, in.ConfirmationName})
	}
	// Detect already-accepted requests before network preflight. A replay must
	// still return its original receipt when the host is now unavailable.
	if h.replaySoftwareChange(w, r, s, id, action, in.Key, hash) {
		return
	}
	prepared, e := h.store.readSoftware(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if action == "SOFTWARE_UNINSTALL" && (prepared.Installation == nil || !installationBound(prepared)) {
		h.fail(w, r, 409, "SOFTWARE_UNINSTALL_NOT_OWNED", "仅支持卸载由Ops安装且身份已确认的软件；外部接管对象请使用解除管理")
		return
	}
	if action == "SOFTWARE_UNINSTALL" && (!in.Confirmed || in.DeleteData && in.ConfirmationName != prepared.Name) {
		h.fail(w, r, 409, "SOFTWARE_UNINSTALL_CONFIRMATION_REQUIRED", "请确认卸载；删除数据还需输入当前软件名称")
		return
	}
	var checkedHostVersion string
	if prepared.Version == in.ExpectedVersion && (prepared.ManagementMode == "MANAGED" || (operation == "adopt" && in.Confirmed && prepared.ManagementMode == "OBSERVE" && prepared.Freshness == "FRESH" && prepared.IdentityHash == in.IdentityHash && prepared.ErrorCode == "" && prepared.RuntimeKind != "TOOL")) {
		t, e := h.store.readTarget(prepared.HostID)
		if e != nil {
			h.opsError(w, r, e)
			return
		}
		if t.Maintenance {
			h.fail(w, r, 409, "HOST_MAINTENANCE", "主机维护中，请恢复后再提交")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		var capability executorCapabilities
		e = h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &capability)
		cancel()
		if e != nil || capability.Owner.ControllerID != h.store.controllerID() || capability.Owner.TargetID != t.ID || capability.Owner.ExecutorVersion != deploy.Version {
			if e == nil {
				e = errors.New("EXECUTOR_IDENTITY_MISMATCH")
			}
			code, message := h.executorFailure(r.Context(), t, e)
			h.fail(w, r, 409, code, message)
			return
		}
		if capability.SoftwareLifecycleVersion != 1 {
			h.fail(w, r, 409, "EXECUTOR_SOFTWARE_UNSUPPORTED", "执行器尚未支持软件生命周期管理，请先升级目标执行器")
			return
		}
		if action == "SOFTWARE_UNINSTALL" && capability.SoftwareUninstallVersion != 1 {
			h.fail(w, r, 409, "EXECUTOR_SOFTWARE_UNINSTALL_UNSUPPORTED", "执行器尚未支持软件卸载，请先升级目标执行器")
			return
		}
		if prepared.RuntimeKind == "DOCKER" && operation != "unmanage" && !capability.DockerEngine {
			h.fail(w, r, 409, "DOCKER_NOT_READY", "执行器无法访问Docker，请检查主机能力")
			return
		}
		checkedHostVersion = t.Version
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	var old, oldHash string
	e = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind=? AND idempotency_key=?", s.AdminID, action, in.Key).Scan(&old, &oldHash)
	if e == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他操作")
		} else {
			h.success(w, r, map[string]string{"jobId": old, "softwareInstanceId": id})
		}
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	}
	if action == "SOFTWARE_UNMANAGE" {
		var priorID, priorHash string
		e = tx.QueryRow("SELECT software_instance_id,request_hash FROM software_unmanage_receipts WHERE actor_id=? AND idempotency_key=?", s.AdminID, in.Key).Scan(&priorID, &priorHash)
		if e == nil {
			if priorID != id || priorHash != hash {
				h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他解除操作")
			} else {
				h.success(w, r, map[string]string{"softwareInstanceId": id})
			}
			return
		}
		if !errors.Is(e, sql.ErrNoRows) {
			h.internal(w, r, e)
			return
		}
	}
	i, e := scanSoftware(tx.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", id))
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if i.Version != in.ExpectedVersion {
		h.fail(w, r, 409, "VERSION_CONFLICT", "软件已变化，请刷新")
		return
	}
	if action == "SOFTWARE_UNINSTALL" && (i.Installation == nil || !installationBound(i) || !in.Confirmed || in.DeleteData && in.ConfirmationName != i.Name) {
		h.fail(w, r, 409, "SOFTWARE_UNINSTALL_CONFIRMATION_REQUIRED", "软件安装身份或名称已变化，请刷新后重新确认卸载")
		return
	}
	if i.ManagementMode == "RELEASED" {
		h.fail(w, r, 409, "SOFTWARE_RELEASED", "记录已解除管理")
		return
	}
	if i.Installation != nil && i.ManagementMode != "MANAGED" && operation != "unmanage" {
		h.fail(w, r, 409, "INSTALLATION_NOT_BOUND", "安装尚无已确认容器，请先查看或核对安装作业")
		return
	}
	if e = hostMutationAllowed(tx, i.HostID); e != nil {
		h.fail(w, r, 409, "HOST_MAINTENANCE", "主机维护中，请恢复后再提交")
		return
	}
	if checkedHostVersion != "" {
		var currentHostVersion string
		if e = tx.QueryRow("SELECT CAST(version AS TEXT) FROM targets WHERE id=?", i.HostID).Scan(&currentHostVersion); e != nil {
			h.internal(w, r, e)
			return
		}
		if currentHostVersion != checkedHostVersion {
			h.fail(w, r, 409, "HOST_CHANGED", "预检期间主机连接已变化，请刷新并重新检查")
			return
		}
	}
	var count int
	if e = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key IN (?,?)", "target:"+i.HostID, "software:"+id).Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count > 0 {
		h.fail(w, r, 409, "OPERATION_LOCKED", "主机或软件有未决作业，请先核对")
		return
	}
	if operation == "unmanage" && i.ManagementMode == "OBSERVE" {
		_, e = tx.Exec("UPDATE software_instances SET management_mode='RELEASED',version=version+1,updated_at=? WHERE id=?", Now(), id)
		if e == nil {
			_, e = tx.Exec("INSERT INTO software_unmanage_receipts VALUES(?,?,?,?,?)", s.AdminID, in.Key, id, hash, Now())
		}
		if e == nil {
			e = auditTx(tx, s.Username, "SOFTWARE_UNMANAGED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			h.internal(w, r, e)
			return
		}
		h.success(w, r, map[string]string{"softwareInstanceId": id})
		return
	}
	if i.RuntimeKind == "TOOL" && (operation == "unmanage" || action == "SOFTWARE_UNINSTALL") && i.Installation != nil {
		bridge, e := scanPlugin(tx.QueryRow("SELECT "+pluginColumns+" FROM plugin_installations WHERE id=?", i.ID))
		if e == nil {
			refs, e := pluginReferences(tx, bridge)
			if e != nil {
				h.internal(w, r, e)
				return
			}
			if refs > 0 {
				h.fail(w, r, 409, "SOFTWARE_TOOL_IN_USE", "该运行环境仍被应用制品或未决作业引用，不能解除")
				return
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			h.internal(w, r, e)
			return
		}
	}
	if i.RuntimeKind == "TOOL" && operation != "unmanage" && action != "SOFTWARE_UNINSTALL" {
		h.fail(w, r, 409, "SOFTWARE_TOOL_READ_ONLY", "工具没有常驻进程，仅支持观察与解除登记")
		return
	}
	if operation == "adopt" {
		if i.ManagementMode != "OBSERVE" || !in.Confirmed || i.Freshness != "FRESH" || i.ErrorCode != "" || i.ActualState == "UNKNOWN" || i.IdentityHash == "" || in.IdentityHash != i.IdentityHash {
			h.fail(w, r, 409, "SOFTWARE_ADOPTION_CONFIRMATION_REQUIRED", "请先观察并明确确认当前服务身份")
			return
		}
		i.BindingRevision++
		if i.Observation.SoftwareObservation != nil && i.Observation.Locator != "" {
			i.Locator = i.Observation.Locator
		}
		var duplicates int
		if e = tx.QueryRow("SELECT count(*) FROM software_instances WHERE id<>? AND host_id=? AND runtime_kind=? AND locator=? AND management_mode<>'RELEASED'", id, i.HostID, i.RuntimeKind, i.Locator).Scan(&duplicates); e != nil {
			h.internal(w, r, e)
			return
		}
		if duplicates > 0 {
			h.fail(w, r, 409, "SOFTWARE_ALREADY_REGISTERED", "实际对象已由另一条记录登记")
			return
		}
	} else if i.ManagementMode != "MANAGED" {
		h.fail(w, r, 409, "SOFTWARE_NOT_MANAGED", "请先明确接管该服务，再执行启停")
		return
	}
	if i.SoftwareType == "DOCKER" && i.Installation != nil && i.Installation.Definition.Key == "docker" && (action == "SOFTWARE_STOP" || action == "SOFTWARE_UNINSTALL") {
		dependencies, dependencyErr := softwareDockerEngineDependencies(tx, i.HostID, i.ID)
		if dependencyErr != nil {
			h.internal(w, r, dependencyErr)
			return
		}
		if dependencies > 0 {
			h.fail(w, r, 409, "DOCKER_ENGINE_IN_USE", "该主机仍有依赖Docker的软件、工具或应用，请先处理这些依赖")
			return
		}
	}
	if e = tx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('QUEUED','RUNNING','UNKNOWN')").Scan(&count); e != nil {
		h.internal(w, r, e)
		return
	}
	if count >= 20 {
		h.fail(w, r, 409, "QUEUE_FULL", "未结束作业达到20个")
		return
	}
	job, now := uuid.NewString(), Now()
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: h.storeIDTx(tx), TargetID: i.HostID, JobID: job, TraceID: r.Context().Value(traceKey{}).(string), Action: action, SoftwareOperation: action, Software: softwareRequest(i), DeadlineAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	if action == "SOFTWARE_UNINSTALL" {
		req.Uninstallation = &deploy.SoftwareUninstallRequest{Installation: *i.Installation, DeleteData: in.DeleteData}
	}
	req.InputHash = deploy.Digest(req)
	desired := i.DesiredState
	if action == "SOFTWARE_START" {
		desired = "RUNNING"
	}
	if action == "SOFTWARE_STOP" {
		desired = "STOPPED"
	}
	if action == "SOFTWARE_UNINSTALL" {
		desired = "REMOVED"
	}
	_, e = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,'QUEUED','PREFLIGHT',?,?,?,?)", job, s.AdminID, action, i.HostID, in.Key, hash, jsonText(req), req.TraceID, now, now)
	if e == nil {
		_, e = tx.Exec("INSERT INTO operation_locks VALUES(?,?),(?,?)", "target:"+i.HostID, job, "software:"+id, job)
	}
	if e == nil {
		_, e = tx.Exec("UPDATE software_instances SET locator=?,identity_hash=?,desired_state=?,binding_revision=?,last_job_id=?,error_code='',version=version+1,updated_at=? WHERE id=?", i.Locator, i.IdentityHash, desired, i.BindingRevision, job, now, id)
	}
	if e == nil {
		e = auditTx(tx, s.Username, action+"_ACCEPTED", id, "SUCCESS", req.TraceID)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	w.Header().Set("Location", api+"/jobs/"+job)
	h.reply(w, r, 202, "SUCCESS", "", map[string]string{"jobId": job, "softwareInstanceId": id})
}

func (h *HTTP) replaySoftwareChange(w http.ResponseWriter, r *http.Request, s *session, id, action, key, hash string) bool {
	var job, oldHash string
	e := h.store.DB.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind=? AND idempotency_key=?", s.AdminID, action, key).Scan(&job, &oldHash)
	if e == nil {
		if oldHash != hash {
			h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他操作")
		} else {
			h.success(w, r, map[string]string{"jobId": job, "softwareInstanceId": id})
		}
		return true
	}
	if !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return true
	}
	if action != "SOFTWARE_UNMANAGE" {
		return false
	}
	var prior string
	e = h.store.DB.QueryRow("SELECT software_instance_id,request_hash FROM software_unmanage_receipts WHERE actor_id=? AND idempotency_key=?", s.AdminID, key).Scan(&prior, &oldHash)
	if errors.Is(e, sql.ErrNoRows) {
		return false
	}
	if e != nil {
		h.internal(w, r, e)
		return true
	}
	if prior != id || oldHash != hash {
		h.fail(w, r, 409, "IDEMPOTENCY_CONFLICT", "提交标识已用于其他解除操作")
	} else {
		h.success(w, r, map[string]string{"softwareInstanceId": id})
	}
	return true
}

func softwareDockerEngineDependencies(q interface{ QueryRow(string, ...any) *sql.Row }, hostID, softwareID string) (int, error) {
	var count int
	err := q.QueryRow(`SELECT
	(SELECT count(*) FROM software_instances WHERE host_id=? AND id<>? AND management_mode<>'RELEASED' AND (runtime_kind='DOCKER' OR (runtime_kind='TOOL' AND coalesce(nullif(json_extract(installation_json,'$.config.runtimeMode'),''),'DOCKER')='DOCKER' AND json_extract(installation_json,'$.definition.imageId')<>''))) +
	(SELECT count(*) FROM plugin_installations WHERE target_id=?) +
	(SELECT count(*) FROM instances WHERE target_id=? AND (coalesce(current_release_id,'')<>'' OR coalesce(previous_release_id,'')<>''))`, hostID, softwareID, hostID, hostID).Scan(&count)
	return count, err
}
