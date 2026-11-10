package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

// Includes connection checks, upload and installation. Slow but progressing
// links keep their budget; the separate idle watchdog still stops a dead upload.
const preparationTimeout = 15 * time.Minute
const preparationResponseTimeout = preparationTimeout + 30*time.Second
const preparationUploadIdleTimeout = 45 * time.Second

type preparationInput struct {
	AuthorizeCommands     bool   `json:"authorizeCommands,omitempty"`
	AuthorizeOnly         bool   `json:"authorizeOnly,omitempty"`
	UpdateOnly            bool   `json:"updateOnly,omitempty"`
	ExpectedCurrentSHA256 string `json:"expectedCurrentSHA256,omitempty"`
	ExpectedPackageSHA256 string `json:"expectedPackageSHA256,omitempty"`
	ExpectedVersion       string `json:"expectedVersion"`
	Key                   string `json:"idempotencyKey"`
	Confirmed             bool   `json:"confirmed"`
	SudoPassword          string `json:"sudoPassword,omitempty"`
}

type preparationRepairInput struct {
	JobID           string `json:"jobId"`
	ExpectedVersion string `json:"expectedVersion"`
	Confirmed       bool   `json:"confirmed"`
	SudoPassword    string `json:"sudoPassword,omitempty"`
}

type preparationSnapshot struct {
	AuthorizeCommands bool   `json:"authorizeCommands,omitempty"`
	AuthorizeOnly     bool   `json:"authorizeOnly,omitempty"`
	UpdateFromSHA     string `json:"updateFromSHA256,omitempty"`
	Host              target `json:"host"`
	BinarySHA         string `json:"binarySha256"`
	ScriptSHA         string `json:"scriptSha256"`
	RecoveryBinarySHA string `json:"recoveryBinarySha256,omitempty"`
	RecoveryScriptSHA string `json:"recoveryScriptSha256,omitempty"`
}

var preparationMessages = map[string]string{
	"PREPARATION_SUDO_DENIED":                "当前 SSH 账号没有安装所需的管理员权限，请服务器管理员为该账号开通 sudo。",
	"PREPARATION_UPDATE_UNKNOWN_BINARY":      "当前接入组件没有匹配的成功安装记录，请核对后手动更新。",
	"PREPARATION_UPDATE_CHANGED":             "接入组件状态已变化，请重新检查后继续。",
	"PREPARATION_UPDATE_ONLY":                "仅更新已有接入组件；目录或摘要已变化，已停止且未初始化或修复授权。",
	"PREPARATION_READY":                      "主机接入完成，可以继续安装软件。",
	"PREPARATION_CONNECT_FAILED":             "SSH 连接失败，请核对网络、账号、凭据和主机指纹。",
	"PREPARATION_PLATFORM_UNSUPPORTED":       "此安装包仅支持 Linux amd64（x86_64），请核对服务器架构。",
	"READ_ONLY_RESPONSE_INVALID":             "服务器系统信息返回异常，请检查 SSH 登录输出后重新检测。",
	"PREPARATION_UPLOAD_FAILED":              "组件上传未完成，请查看传输进度和错误详情。",
	"PREPARATION_UPLOAD_TIMEOUT":             "组件上传达到时限，安装尚未开始。请查看传输进度后重试，或使用离线接入包。",
	"PREPARATION_UPLOAD_STALLED":             "组件上传连续45秒无进度，已停止，安装尚未开始。请检查网络连接。",
	"PREPARATION_UPLOAD_INTERRUPTED":         "组件上传连接已中断，安装尚未开始。请检查网络后重试。",
	"PREPARATION_UPLOAD_NO_SPACE":            "目标服务器临时目录空间或配额不足，安装尚未开始。",
	"PREPARATION_UPLOAD_DENIED":              "目标服务器拒绝写入临时目录，安装尚未开始。请检查写入权限。",
	"PREPARATION_SUDO_REQUIRED":              "服务器需要 sudo 验证，请输入当前 SSH 账号的系统登录密码。",
	"PREPARATION_RESULT_UNKNOWN":             "接入状态待确认，请检查服务器。",
	"PREPARATION_REPAIR_UNCONFIRMED":         "权限修复结果尚未确认，原目录和作业仍受保护，请重新检查安装结果。",
	"PREPARATION_INPUT_DAMAGED":              "安装记录不完整，请保留原作业并检查控制器日志。",
	"PREPARATION_MATERIAL_CHANGED":           "安装材料已变化，请重新打开主机安装页面。",
	"PREPARATION_HOST_CHANGED":               "主机配置或维护状态已变化，安装尚未开始，请刷新后重试。",
	"PREPARATION_INSTALL_FAILED":             "接入安装脚本未完成，请检查安装结果后使用恢复接入继续原作业。",
	"PREPARATION_AUTHORIZATION_CONFLICT":     "服务器现有授权与本主机的账号或安装目录不匹配，已停止并保留原文件。",
	"PREPARATION_RECOVERY_IDENTITY_MISMATCH": "现有接入文件与原作业及本次恢复材料均不匹配，已停止恢复并保留原文件。",
	"PREPARATION_RECOVERY_MATERIAL_CHANGED":  "原作业已有已确认的恢复材料，请使用相同材料继续恢复，不能自动替换。",
	"PREPARATION_RECOVERY_MATERIAL_MISSING":  "原作业的接入恢复材料缺失，请恢复匹配的材料后继续原任务。",
	"PREPARATION_RECOVERY_MATERIAL_DAMAGED":  "原作业的接入恢复材料校验失败，请检查材料文件，原任务和服务器文件已保留。",
	"PREPARATION_MATERIAL_CACHE_FAILED":      "接入材料未能完整保存，安装尚未开始，请检查控制器存储空间和权限。",
	"CONTROLLER_RESTARTED":                   "控制器在安装期间重启，请核对原作业的安装结果。",
}

func preparationMessage(code string) string {
	if message, ok := preparationMessages[code]; ok {
		return message
	}
	return preparationVerificationMessage(code)
}

func preparationScriptFailure(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		code := strings.TrimPrefix(strings.TrimSpace(line), "SFOPS_ERROR=")
		switch code {
		case "PREPARATION_INSTALL_FAILED", "PREPARATION_AUTHORIZATION_CONFLICT", "PREPARATION_RECOVERY_IDENTITY_MISMATCH", "PREPARATION_UPDATE_ONLY":
			return code
		}
	}
	return "PREPARATION_RESULT_UNKNOWN"
}

func (h *HTTP) latestPreparationJob(id string) any {
	var job, status, phase, code, trace, created, updated, raw string
	if h.store.DB.QueryRow("SELECT id,status,phase,error_code,trace_id,created_at,updated_at,result_json FROM jobs WHERE target_id=? AND kind='HOST_PREPARE' ORDER BY created_at DESC,id DESC LIMIT 1", id).Scan(&job, &status, &phase, &code, &trace, &created, &updated, &raw) != nil {
		return nil
	}
	response := map[string]any{"id": job, "status": status, "phase": phase, "errorCode": code, "message": preparationMessage(code), "traceId": trace, "createdAt": created, "updatedAt": updated}
	var result struct {
		Message  string               `json:"message,omitempty"`
		Transfer *preparationTransfer `json:"transfer,omitempty"`
	}
	if json.Unmarshal([]byte(raw), &result) == nil {
		response["result"] = result
	}
	return response
}

func (h *HTTP) preparationRepairSHA(t target) string {
	snapshot := h.preparationRepairSnapshot(t)
	if snapshot == nil {
		return ""
	}
	return snapshot.BinarySHA
}

func (h *HTTP) preparationRepairSnapshot(t target) *preparationSnapshot {
	var raw, status string
	if h.store.DB.QueryRow(`SELECT j.input_json,j.status FROM jobs j JOIN operation_locks l ON l.job_id=j.id AND l.resource_key='target:'||j.target_id WHERE j.target_id=? AND j.kind='HOST_PREPARE' ORDER BY j.created_at DESC,j.id DESC LIMIT 1`, t.ID).Scan(&raw, &status) != nil || status != "UNKNOWN" {
		return nil
	}
	var snapshot preparationSnapshot
	if deploy.StrictJSON([]byte(raw), &snapshot) != nil || !validSHA256(snapshot.BinarySHA) || snapshot.Host.ID != t.ID || snapshot.Host.sshTarget != t.sshTarget || snapshot.Host.InstallRoot != t.InstallRoot {
		return nil
	}
	return &snapshot
}

// The request owns the transient sudo password. Only a frozen, secret-free
// snapshot enters the existing durable job/lock chain; interrupted jobs are
// UNKNOWN and are never dispatched or replayed by the general worker.
func (h *HTTP) installHostPreparation(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in preparationInput
	if h.decodeSensitive(w, r, actor, &in) != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	defer func() { in.SudoPassword = "" }()
	if in.AuthorizeCommands && !in.UpdateOnly || in.UpdateOnly && in.AuthorizeOnly || (in.UpdateOnly || in.AuthorizeOnly) && (!validSHA256(in.ExpectedCurrentSHA256) || !validSHA256(in.ExpectedPackageSHA256)) || !in.UpdateOnly && !in.AuthorizeOnly && (in.ExpectedCurrentSHA256 != "" || in.ExpectedPackageSHA256 != "") {
		h.fail(w, r, 400, "INVALID_INPUT", "安装准备需要已核对的组件摘要和单一操作模式")
		return
	}
	if !in.Confirmed || !deploy.ID.MatchString(in.Key) || len(in.SudoPassword) > 1024 || strings.ContainsAny(in.SudoPassword, "\r\n\x00") {
		h.fail(w, r, 400, "INVALID_INPUT", "请确认目标、安装目录和管理员权限后开始安装")
		return
	}
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Version != in.ExpectedVersion {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机连接已变化，请刷新后重试")
		return
	}
	if t.Kind != "SSH" {
		h.fail(w, r, 409, "SSH_TARGET_REQUIRED", "在线安装请选择 SSH 主机；本机请使用离线安装包")
		return
	}
	p, material := preparationForHost(t, h.store.controllerID(), h.targetPackageDir)
	p.CommandPublicKey = h.store.commandPublicKey()
	if !p.Package.Available {
		h.fail(w, r, 409, p.Package.Code, p.Package.Message)
		return
	}
	if in.UpdateOnly || in.AuthorizeOnly {
		if material.Binary.Hash != in.ExpectedPackageSHA256 || in.AuthorizeOnly && in.ExpectedCurrentSHA256 != material.Binary.Hash {
			h.fail(w, r, 409, "PREPARATION_MATERIAL_CHANGED", preparationMessage("PREPARATION_MATERIAL_CHANGED"))
			return
		}
		if in.UpdateOnly {
			p.UpdateFromSHA = in.ExpectedCurrentSHA256
		}
	}
	material, err = h.store.cachePreparationMaterial(material)
	if err != nil {
		h.fail(w, r, 409, err.Error(), preparationMessage(err.Error()))
		return
	}
	var archive *os.File
	if !in.AuthorizeOnly {
		archive, err = createTargetPreparationArchive(p, material)
		if err != nil {
			h.fail(w, r, 409, "TARGET_PACKAGE_CHANGED", "安装材料已变化，请刷新后重试")
			return
		}
		defer os.Remove(archive.Name())
		defer archive.Close()
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	snapshot := preparationSnapshot{Host: t, BinarySHA: material.Binary.Hash, ScriptSHA: material.Script.Hash, AuthorizeOnly: in.AuthorizeOnly, AuthorizeCommands: in.AuthorizeCommands}
	// Observation timestamps are not part of the user's repeat-submit identity.
	requestHash := deploy.Digest([]string{t.ID, t.Version, material.Binary.Hash, material.Script.Hash})
	if in.UpdateOnly {
		snapshot.UpdateFromSHA = in.ExpectedCurrentSHA256
		requestHash = deploy.Digest([]string{t.ID, t.Version, material.Binary.Hash, material.Script.Hash, "UPDATE_ONLY", in.ExpectedCurrentSHA256})
	}
	if in.AuthorizeOnly {
		requestHash = deploy.Digest([]string{requestHash, "AUTHORIZE_ONLY"})
	}
	if in.AuthorizeCommands {
		requestHash = deploy.Digest([]string{requestHash, "AUTHORIZE_COMMANDS"})
	}
	job, reused, err := h.acceptPreparation(actor, in.Key, requestHash, snapshot, r.Context().Value(traceKey{}).(string))
	if err != nil {
		switch err.Error() {
		case "VERSION_CONFLICT", "HOST_MAINTENANCE", "HOST_BUSY", "IDEMPOTENCY_CONFLICT":
			h.fail(w, r, 409, err.Error(), "主机正在操作、维护中，或重复请求内容已变化；请刷新并查看原作业")
		default:
			h.internal(w, r, err)
		}
		return
	}
	if reused {
		h.getJob(w, r, job)
		return
	}
	if err = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(preparationResponseTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		if err = h.finishPreparation(job, "FAILED", "PREPARATION_CONNECT_FAILED"); err != nil {
			h.internal(w, r, err)
			return
		}
		h.getJob(w, r, job)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), preparationTimeout)
	defer cancel()
	var code string
	var uncertain bool
	if in.AuthorizeOnly {
		code, uncertain = h.runCommandAuthorization(ctx, actor, job, t, snapshot, in.SudoPassword)
	} else {
		code, uncertain = h.runHostPreparation(ctx, actor, job, t, archive, material.Binary.Hash, in.SudoPassword, false, snapshot)
	}
	if code == "PREPARATION_READY" && in.AuthorizeCommands {
		code, uncertain = h.runCommandAuthorization(ctx, actor, job, t, snapshot, in.SudoPassword)
	}
	in.SudoPassword = ""
	status := "FAILED"
	if code == "PREPARATION_READY" {
		status = "SUCCEEDED"
	} else if uncertain {
		status = "UNKNOWN"
	}
	if err = h.finishPreparation(job, status, code); err != nil {
		h.jobUnknown(job, "PREPARATION_RESULT_UNKNOWN")
		h.internal(w, r, err)
		return
	}
	h.getJob(w, r, job)
}

func (h *HTTP) acceptPreparation(actor *session, key, hash string, snapshot preparationSnapshot, trace string) (string, bool, error) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var old, oldHash string
	err = tx.QueryRow("SELECT id,request_hash FROM jobs WHERE actor_id=? AND kind='HOST_PREPARE' AND idempotency_key=?", actor.AdminID, key).Scan(&old, &oldHash)
	if err == nil {
		if oldHash != hash {
			return "", false, errors.New("IDEMPOTENCY_CONFLICT")
		}
		return old, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", snapshot.Host.ID))
	if err != nil {
		return "", false, err
	}
	if current.Version != snapshot.Host.Version {
		return "", false, errors.New("VERSION_CONFLICT")
	}
	if current.Maintenance {
		return "", false, errors.New("HOST_MAINTENANCE")
	}
	busy, err := hostBusy(tx, current.ID)
	if err != nil {
		return "", false, err
	}
	if busy {
		return "", false, errors.New("HOST_BUSY")
	}
	job, now := uuid.NewString(), Now()
	_, err = tx.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'HOST_PREPARE',?,?,?,'RUNNING','CONNECTING',?,?,?,?)", job, actor.AdminID, current.ID, key, hash, jsonText(snapshot), trace, now, now)
	if err == nil {
		_, err = tx.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+current.ID, job)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, "HOST_PREPARATION_ACCEPTED", job, "SUCCESS", trace)
	}
	if err == nil {
		err = tx.Commit()
	}
	return job, false, err
}

func (h *HTTP) preparationPhase(job, phase string) {
	_, _ = h.store.DB.Exec("UPDATE jobs SET phase=?,updated_at=? WHERE id=? AND kind='HOST_PREPARE' AND status='RUNNING'", phase, Now(), job)
}

func (h *HTTP) finishPreparation(job, status, code string) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = h.finishPreparationTx(tx, job, status, code); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *HTTP) finishPreparationTx(tx *sql.Tx, job, status, code string) error {
	var trace, current string
	if err := tx.QueryRow("SELECT trace_id,status FROM jobs WHERE id=? AND kind='HOST_PREPARE'", job).Scan(&trace, &current); err != nil {
		return err
	}
	if current == "SUCCEEDED" || current == "FAILED" {
		return nil
	}
	// Keep the last confirmed execution phase. The terminal status and error code
	// already describe the outcome; replacing the phase with a generic result
	// marker would erase the only durable progress evidence after a disconnect.
	_, err := tx.Exec("UPDATE jobs SET status=?,error_code=?,result_json=json_set(CASE WHEN json_valid(result_json) THEN result_json ELSE '{}' END,'$.message',?),updated_at=? WHERE id=? AND kind='HOST_PREPARE'", status, code, preparationMessage(code), Now(), job)
	if err == nil && status != "UNKNOWN" {
		_, err = tx.Exec("DELETE FROM operation_locks WHERE job_id=?", job)
	}
	if err == nil {
		err = auditTx(tx, "worker", "HOST_PREPARATION_RESULT", job, status, trace)
	}
	if err != nil {
		h.logger.Printf("traceId=%s jobId=%s event=PREPARATION_COMMIT_FAILED", trace, job)
	}
	return err
}

// Keep the final actor/host check, result and lock release in one transaction.
// A maintenance change during verification advances the host version and must
// keep the UNKNOWN job locked. Already-maintained hosts may still be checked.
func (h *HTTP) finishReconciledPreparation(actor *session, observed target, job string) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", observed.ID))
	if err != nil {
		return err
	}
	if current.Version != observed.Version || current.sshTarget != observed.sshTarget || current.InstallRoot != observed.InstallRoot {
		return errors.New("HOST_CHANGED")
	}
	var targetID, status string
	if err = tx.QueryRow("SELECT target_id,status FROM jobs WHERE id=? AND kind='HOST_PREPARE'", job).Scan(&targetID, &status); err != nil {
		return err
	}
	if targetID != observed.ID {
		return errors.New("HOST_CHANGED")
	}
	if status == "SUCCEEDED" || status == "FAILED" {
		return nil
	}
	if status != "UNKNOWN" {
		return errors.New("HOST_CHANGED")
	}
	if err = h.finishPreparationTx(tx, job, "SUCCEEDED", "PREPARATION_READY"); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *HTTP) runHostPreparation(ctx context.Context, actor *session, job string, t target, archive *os.File, binarySHA, password string, repair bool, snapshots ...preparationSnapshot) (string, bool) {
	var update *preparationSnapshot
	if len(snapshots) > 0 && snapshots[0].UpdateFromSHA != "" {
		update = &snapshots[0]
		if code := h.verifyPreparationUpdate(ctx, t, job, *update); code != "" {
			return code, false
		}
	}
	client, closeClient, err := h.store.connectSSH(ctx, t)
	if err != nil {
		return "PREPARATION_CONNECT_FAILED", false
	}
	defer closeClient()
	out, stderr := &limitedOutput{limit: 16 << 10}, &limitedOutput{limit: 4 << 10}
	if runSSHCommand(client, hostReadOnlyCommand, nil, out, stderr) != nil {
		return "PREPARATION_CONNECT_FAILED", false
	}
	facts := unknownHostCapabilities()
	applyHostReadOnly(&facts, out.String())
	if facts.ReadOnly.Status != "AVAILABLE" {
		return "READ_ONLY_RESPONSE_INVALID", false
	}
	if facts.Platform != "linux/x86_64" {
		return "PREPARATION_PLATFORM_UNSUPPORTED", false
	}
	if code := checkPreparationPrivilege(client, t, password); code != "" {
		return code, false
	}
	stage := "/tmp/sfops-upload-" + job
	if repair {
		stage = "/tmp/sfops-repair-" + job
	}
	defer func() {
		// Remove only the exact temporary upload created by this job. A canceled
		// request also closes the SSH connection, so use a short independent
		// cleanup connection instead of leaving a partial archive in /tmp.
		cleanup := "/bin/sh -c " + shellQuote(controllerTemplate("preparation/cleanup.sh.tmpl", shellQuote(stage+"/bundle.tar.gz"), shellQuote(stage)))
		if ctx.Err() == nil {
			if runSSHCommand(client, cleanup, nil, io.Discard, io.Discard) == nil {
				return
			}
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanupClient, closeCleanup, cleanupErr := h.store.connectSSH(cleanupCtx, t)
		if cleanupErr != nil {
			return
		}
		defer closeCleanup()
		_ = runSSHCommand(cleanupClient, cleanup, nil, io.Discard, io.Discard)
	}()
	h.preparationPhase(job, "UPLOADING_EXECUTOR")
	if _, err = archive.Seek(0, 0); err != nil {
		return "PREPARATION_UPLOAD_FAILED", false
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, archive); err != nil {
		return "PREPARATION_UPLOAD_FAILED", false
	}
	if _, err = archive.Seek(0, 0); err != nil {
		return "PREPARATION_UPLOAD_FAILED", false
	}
	command := controllerTemplate("preparation/upload.sh.tmpl", shellQuote(stage), shellQuote(stage+"/bundle.tar.gz"))
	info, err := archive.Stat()
	if err != nil {
		return "PREPARATION_UPLOAD_FAILED", false
	}
	progress := newPreparationTransferReader(archive, info.Size(), func(value preparationTransfer) {
		_, _ = h.store.DB.Exec("UPDATE jobs SET result_json=json_set(CASE WHEN json_valid(result_json) THEN result_json ELSE '{}' END,'$.transfer',json(?)) WHERE id=? AND kind='HOST_PREPARE' AND status IN ('RUNNING','UNKNOWN')", jsonText(value), job)
	})
	stderr.Reset()
	watchdog := newPreparationUploadWatchdog(preparationUploadIdleTimeout, func() { _ = client.Close() })
	progress.activity = watchdog.touch
	err = runSSHCommand(client, "/bin/sh -c "+shellQuote(command), progress, io.Discard, stderr)
	stalled := watchdog.stop()
	progress.publish(true)
	if err != nil || ctx.Err() != nil || stalled {
		code := preparationUploadFailure(ctx, err, stderr.String())
		if stalled && ctx.Err() == nil {
			code = "PREPARATION_UPLOAD_STALLED"
		}
		h.logPreparationFailure(job, "UPLOADING_EXECUTOR", code)
		return code, false
	}
	if update != nil {
		if code := h.verifyPreparationUpdate(ctx, t, job, *update); code != "" {
			return code, false
		}
	}
	if !h.preparationActorCurrent(actor, t, job, repair) {
		return "PREPARATION_HOST_CHANGED", false
	}
	h.preparationPhase(job, "INSTALLING_EXECUTOR")
	command = remotePreparationCommand(stage, fmt.Sprintf("%x", hash.Sum(nil)), t.User == "root", password != "", repair)
	var input io.Reader
	if t.User != "root" && password != "" {
		input = strings.NewReader(password + "\n")
	}
	out.Reset()
	stderr.Reset()
	err = runSSHCommand(client, command, input, out, stderr)
	if err != nil {
		// Only failures known to precede privileged script execution release the lock.
		if strings.Contains(stderr.String(), "SFOPS_SETUP_ENTERED") {
			code := preparationScriptFailure(stderr.String())
			h.logPreparationFailure(job, "INSTALLING_EXECUTOR", code)
			return code, true
		}
		sudoDiagnostic := strings.ToLower(stderr.String())
		if t.User != "root" && (strings.Contains(sudoDiagnostic, "not in the sudoers") || strings.Contains(sudoDiagnostic, "not allowed to execute") || strings.Contains(sudoDiagnostic, "may not run sudo")) {
			return "PREPARATION_SUDO_DENIED", false
		}
		if t.User != "root" && (strings.Contains(strings.ToLower(stderr.String()), "sudo") || strings.Contains(strings.ToLower(stderr.String()), "password")) {
			return "PREPARATION_SUDO_REQUIRED", false
		}
		return "PREPARATION_RESULT_UNKNOWN", true
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "SFOPS_SETUP_READY") {
		return "PREPARATION_RESULT_UNKNOWN", true
	}
	h.preparationPhase(job, "VERIFYING_EXECUTOR")
	if ready, code := h.verifyPreparation(ctx, t, binarySHA); !ready {
		h.logPreparationFailure(job, "VERIFYING_EXECUTOR", code)
		return code, true
	}
	return "PREPARATION_READY", false
}

// This counts bytes handed to SSH, not a claim that the target verified them.
// The archive digest is still checked by the privileged installation step.
type preparationTransfer struct {
	SentBytes  int64  `json:"sentBytes"`
	TotalBytes int64  `json:"totalBytes"`
	StartedAt  string `json:"startedAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type preparationTransferReader struct {
	io.Reader
	value    preparationTransfer
	last     time.Time
	report   func(preparationTransfer)
	activity func()
}

func newPreparationTransferReader(reader io.Reader, total int64, report func(preparationTransfer)) *preparationTransferReader {
	p := &preparationTransferReader{Reader: reader, value: preparationTransfer{TotalBytes: total, StartedAt: Now()}, report: report}
	p.publish(true)
	return p
}

func (p *preparationTransferReader) Read(buf []byte) (int, error) {
	n, err := p.Reader.Read(buf)
	p.value.SentBytes += int64(n)
	if n > 0 && p.activity != nil {
		p.activity()
	}
	p.publish(false)
	return n, err
}

// The watchdog only covers streaming. It is stopped before privilege elevation,
// so a late timer cannot interrupt installation or misclassify its outcome.
type preparationUploadWatchdog struct {
	mu               sync.Mutex
	timer            *time.Timer
	last             time.Time
	limit            time.Duration
	stopped, stalled bool
	interrupt        func()
}

func newPreparationUploadWatchdog(limit time.Duration, interrupt func()) *preparationUploadWatchdog {
	w := &preparationUploadWatchdog{last: time.Now(), limit: limit, interrupt: interrupt}
	w.timer = time.AfterFunc(limit, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.stopped {
			return
		}
		if remaining := w.limit - time.Since(w.last); remaining > 0 {
			w.timer.Reset(remaining)
			return
		}
		w.stalled = true
		w.interrupt()
	})
	return w
}

func (w *preparationUploadWatchdog) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped && !w.stalled {
		w.last = time.Now()
		w.timer.Reset(w.limit)
	}
}

func (w *preparationUploadWatchdog) stop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	w.timer.Stop()
	return w.stalled
}

func (p *preparationTransferReader) publish(force bool) {
	now := time.Now()
	if !force && now.Sub(p.last) < time.Second {
		return
	}
	p.last = now
	p.value.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	p.report(p.value)
}

func preparationUploadFailure(ctx context.Context, err error, stderr string) string {
	var network net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return "PREPARATION_UPLOAD_TIMEOUT"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "PREPARATION_UPLOAD_INTERRUPTED"
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		diagnostic := strings.ToLower(stderr)
		if strings.Contains(diagnostic, "no space left on device") || strings.Contains(diagnostic, "disk quota exceeded") {
			return "PREPARATION_UPLOAD_NO_SPACE"
		}
		if strings.Contains(diagnostic, "permission denied") || strings.Contains(diagnostic, "read-only file system") {
			return "PREPARATION_UPLOAD_DENIED"
		}
	}
	var missing *ssh.ExitMissingError
	if errors.As(err, &network) || errors.As(err, &missing) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "PREPARATION_UPLOAD_INTERRUPTED"
	}
	return "PREPARATION_UPLOAD_FAILED"
}

func (h *HTTP) logPreparationFailure(job, phase, code string) {
	var trace string
	_ = h.store.DB.QueryRow("SELECT trace_id FROM jobs WHERE id=?", job).Scan(&trace)
	h.logger.Printf("traceId=%s jobId=%s phase=%s code=%s", trace, job, phase, code)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// A fixed bootstrap over the existing SSH transport, not a general shell API.
// Root stages a private copy and verifies the entire archive before executing.
func remotePreparationCommand(stage, digest string, root, password, repair bool) string {
	mode := "--non-interactive"
	if repair {
		mode = "--repair-only --non-interactive"
	}
	script := strings.NewReplacer("@BUNDLE@", shellQuote(stage+"/bundle.tar.gz"), "@DIGEST@", shellQuote(digest), "@MODE@", mode).Replace(controllerScript("preparation/remote-install.sh.tmpl"))
	command := "/bin/bash -c " + shellQuote(script)
	if root {
		return command
	}
	if password {
		return "/usr/bin/sudo -S -p '' -- " + command
	}
	return "/usr/bin/sudo -n -- " + command
}

func (h *HTTP) repairHostPreparation(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in preparationRepairInput
	if h.decodeSensitive(w, r, actor, &in) != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	defer func() { in.SudoPassword = "" }()
	if !in.Confirmed || !deploy.ID.MatchString(in.JobID) || len(in.SudoPassword) > 1024 || strings.ContainsAny(in.SudoPassword, "\r\n\x00") {
		h.fail(w, r, 400, "INVALID_INPUT", "请确认原安装记录和管理员权限后再修复")
		return
	}
	var raw, status, locked string
	err := h.store.DB.QueryRow(`SELECT j.input_json,j.status,coalesce(l.job_id,'') FROM jobs j LEFT JOIN operation_locks l ON l.job_id=j.id AND l.resource_key='target:'||j.target_id WHERE j.id=? AND j.target_id=? AND j.kind='HOST_PREPARE'`, in.JobID, id).Scan(&raw, &status, &locked)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if status != "UNKNOWN" || locked != in.JobID {
		h.fail(w, r, 409, "PREPARATION_REPAIR_NOT_ALLOWED", "只有结果待确认且仍受保护的原安装记录可以修复")
		return
	}
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	var snapshot preparationSnapshot
	if deploy.StrictJSON([]byte(raw), &snapshot) != nil || !validSHA256(snapshot.BinarySHA) || snapshot.Host.ID != id || snapshot.Host.sshTarget != t.sshTarget || snapshot.Host.InstallRoot != t.InstallRoot || in.ExpectedVersion != t.Version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机连接、安装目录或原安装记录已变化，请停止修复并核对")
		return
	}
	if snapshot.AuthorizeOnly || snapshot.AuthorizeCommands {
		h.fail(w, r, 409, "PREPARATION_REPAIR_NOT_ALLOWED", "请检查原授权任务结果，不能重复执行授权")
		return
	}
	p, material, err := h.recoveryPreparation(t, snapshot)
	if err != nil {
		h.fail(w, r, 409, err.Error(), preparationMessage(err.Error()))
		return
	}
	if !p.Package.Available {
		h.fail(w, r, 409, p.Package.Code, p.Package.Message)
		return
	}
	material, err = h.store.cachePreparationMaterial(material)
	if err != nil {
		h.fail(w, r, 409, err.Error(), preparationMessage(err.Error()))
		return
	}
	archive, err := createTargetPreparationArchive(p, material)
	if err != nil {
		h.fail(w, r, 409, "TARGET_PACKAGE_CHANGED", "修复材料已变化，请刷新后重试")
		return
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	release, err := h.acquireObservation(r.Context(), id, false)
	if err != nil {
		h.fail(w, r, 409, "HOST_BUSY", "这台主机正在检查或恢复，请稍后重试。")
		return
	}
	defer release()
	if err = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(preparationResponseTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		h.internal(w, r, err)
		return
	}
	if err = h.acceptPreparationRepair(actor, t, in.JobID, raw, material, r.Context().Value(traceKey{}).(string)); err != nil {
		h.opsError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), preparationTimeout)
	defer cancel()
	code, _ := h.runHostPreparation(ctx, actor, in.JobID, t, archive, material.Binary.Hash, in.SudoPassword, true)
	in.SudoPassword = ""
	if code != "PREPARATION_READY" {
		if code == "PREPARATION_RESULT_UNKNOWN" {
			code = "PREPARATION_REPAIR_UNCONFIRMED"
		}
		_, _ = h.store.DB.Exec("INSERT INTO audit VALUES(?,?,?,?,?,?,?)", uuid.NewString(), actor.Username, "HOST_PREPARATION_REPAIR_RESULT", in.JobID, code, r.Context().Value(traceKey{}).(string), Now())
		h.fail(w, r, 409, code, preparationMessage(code))
		return
	}
	if err = h.finishReconciledPreparation(actor, t, in.JobID); err != nil {
		h.internal(w, r, err)
		return
	}
	_, _ = h.store.DB.Exec("INSERT INTO audit VALUES(?,?,?,?,?,?,?)", uuid.NewString(), actor.Username, "HOST_PREPARATION_REPAIR_RESULT", in.JobID, "SUCCESS", r.Context().Value(traceKey{}).(string), Now())
	h.getJob(w, r, in.JobID)
}

// Freeze the recovery materials before elevation while retaining the original
// immutable digests. Reconciliation can verify either accepted result after a
// lost response; an unrelated release can never silently replace the recovery.
func (h *HTTP) acceptPreparationRepair(actor *session, observed target, job, expectedRaw string, material verifiedTargetPackage, trace string) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", observed.ID))
	if err != nil {
		return err
	}
	if current.Maintenance || current.Version != observed.Version || current.sshTarget != observed.sshTarget || current.InstallRoot != observed.InstallRoot {
		return errors.New("VERSION_CONFLICT")
	}
	var raw, status, locked string
	if err = tx.QueryRow(`SELECT j.input_json,j.status,coalesce(l.job_id,'') FROM jobs j LEFT JOIN operation_locks l ON l.job_id=j.id AND l.resource_key='target:'||j.target_id WHERE j.id=? AND j.target_id=? AND j.kind='HOST_PREPARE'`, job, observed.ID).Scan(&raw, &status, &locked); err != nil {
		return err
	}
	if raw != expectedRaw || status != "UNKNOWN" || locked != job {
		return errors.New("VERSION_CONFLICT")
	}
	var snapshot preparationSnapshot
	if deploy.StrictJSON([]byte(raw), &snapshot) != nil {
		return errors.New("VERSION_CONFLICT")
	}
	if snapshot.RecoveryBinarySHA != "" && !targetPackageMatches(material, snapshot.RecoveryBinarySHA, snapshot.RecoveryScriptSHA) {
		return errors.New("PREPARATION_RECOVERY_MATERIAL_CHANGED")
	}
	snapshot.RecoveryBinarySHA, snapshot.RecoveryScriptSHA = material.Binary.Hash, material.Script.Hash
	if _, err = tx.Exec("UPDATE jobs SET input_json=? WHERE id=?", jsonText(snapshot), job); err != nil {
		return err
	}
	if err = auditTx(tx, actor.Username, "HOST_PREPARATION_REPAIR_ACCEPTED", job, "SUCCESS", trace); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *HTTP) verifyPreparation(ctx context.Context, t target, sha string, alternatives ...string) (bool, string) {
	var caps executorCapabilities
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &caps); err != nil {
		code, _ := h.executorFailure(ctx, t, err)
		if code != "EXECUTOR_RESPONSE_INVALID" {
			return false, code
		}
		// Only repeat this read-only check. A truncated reply must never replay
		// installation or release the protected job without a valid response.
		caps = executorCapabilities{}
		if err = h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &caps); err != nil {
			code, _ = h.executorFailure(ctx, t, err)
			return false, code
		}
	}
	if caps.Owner.ControllerID != h.store.controllerID() || caps.Owner.TargetID != t.ID {
		return false, "EXECUTOR_IDENTITY_MISMATCH"
	}
	if caps.Owner.ProtocolVersion != 1 || caps.Owner.ExecutorVersion != deploy.Version || caps.Platform != "linux/amd64" {
		return false, "EXECUTOR_UPGRADE_REQUIRED"
	}
	if sha == "" {
		return true, ""
	}
	client, closeClient, err := h.store.connectSSH(ctx, t)
	if err != nil {
		code, _ := hostSSHError(err)
		return false, code
	}
	defer closeClient()
	out := &limitedOutput{limit: 512}
	if runSSHCommand(client, controllerTemplate("preparation/executor-sha256.command.tmpl", shellQuote(t.InstallRoot+"/executor")), nil, out, io.Discard) != nil {
		return false, "EXECUTOR_HASH_UNAVAILABLE"
	}
	parts := strings.Fields(out.String())
	if len(parts) != 2 {
		return false, "EXECUTOR_HASH_MISMATCH"
	}
	for _, expected := range append([]string{sha}, alternatives...) {
		if validSHA256(expected) && parts[0] == expected {
			return true, ""
		}
	}
	return false, "EXECUTOR_HASH_MISMATCH"
}

func preparationVerificationMessage(code string) string {
	switch code {
	case "EXECUTOR_PATH_UNSAFE":
		return "接入目录不满足安全要求，请让服务器管理员检查符号链接、所有者和写入权限。"
	case "EXECUTOR_IDENTITY_MISMATCH":
		return "接入目录不属于当前主机，不能直接覆盖，请先核对目录归属。"
	case "EXECUTOR_HASH_MISMATCH", "EXECUTOR_UPGRADE_REQUIRED":
		return "接入文件与当前版本不一致，请先核对现有目录。"
	case "EXECUTOR_MISSING":
		return "未找到接入目录或执行器。请使用下方“恢复接入并继续安装”，Ops 会核对原作业后重新准备接入。"
	case "EXECUTOR_HASH_UNAVAILABLE", "EXECUTOR_PATH_UNREADABLE":
		return "当前 SSH 账号无法读取接入文件，安装目录或执行器的读取权限未正确设置。请使用下方恢复接入修复并重新检查。"
	case "EXECUTOR_NOT_EXECUTABLE":
		return "接入文件存在但无法执行，请使用下方恢复接入核对文件权限和可信摘要。"
	case "EXECUTOR_INITIALIZATION_REQUIRED":
		return "接入目录中的归属文件缺失或不可读取。恢复会先核对归属，无法确认时保留原文件并停止。"
	case "EXECUTOR_RESPONSE_INVALID":
		return "安装结果检查收到无效回执，请点击“检查安装结果”重新核对。"
	case "SUDO_UNAVAILABLE", "SUDO_AUTHORIZATION_REQUIRED", "EXECUTOR_PRIVILEGE_REQUIRED":
		return "服务器授权尚未完成，请让服务器管理员检查受限授权。"
	case "EXECUTOR_CHECK_TIMEOUT", "SSH_TIMEOUT":
		return "服务器检查超时，请检查连接后重试。"
	case "SSH_UNAVAILABLE", "SSH_AUTH_FAILED", "SSH_HOST_KEY_CHANGED", "CREDENTIAL_UNAVAILABLE":
		return "无法连接服务器，请检查 SSH 连接。"
	default:
		return "接入状态仍未确认，请根据接入检查处理后重试。"
	}
}

func (h *HTTP) reconcileHostPreparation(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in struct {
		JobID           string `json:"jobId"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if decode(w, r, &in) != nil || !deploy.ID.MatchString(in.JobID) {
		h.fail(w, r, 400, "INVALID_INPUT", "请刷新安装记录后重试")
		return
	}
	h.reconcilePreparation(w, r, actor, id, in.JobID, in.ExpectedVersion)
}

func (h *HTTP) reconcilePreparation(w http.ResponseWriter, r *http.Request, actor *session, id, jobID, expectedVersion string) {
	var raw, status string
	if err := h.store.DB.QueryRow("SELECT input_json,status FROM jobs WHERE id=? AND target_id=? AND kind='HOST_PREPARE'", jobID, id).Scan(&raw, &status); err != nil {
		h.opsError(w, r, err)
		return
	}
	if status == "RUNNING" {
		h.fail(w, r, 409, "HOST_BUSY", "安装仍在执行，请稍后刷新")
		return
	}
	if status != "UNKNOWN" {
		h.getJob(w, r, jobID)
		return
	}
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	var snapshot preparationSnapshot
	if deploy.StrictJSON([]byte(raw), &snapshot) != nil || snapshot.Host.ID != id || !validSHA256(snapshot.BinarySHA) {
		h.fail(w, r, 409, "PREPARATION_INPUT_DAMAGED", preparationMessages["PREPARATION_INPUT_DAMAGED"])
		return
	}
	if snapshot.Host.sshTarget != t.sshTarget || snapshot.Host.InstallRoot != t.InstallRoot || expectedVersion != "" && expectedVersion != t.Version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机配置或安装记录已变化，请保留原作业核对")
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if ready, code := h.verifyPreparation(ctx, t, snapshot.BinarySHA, snapshot.RecoveryBinarySHA); !ready {
		h.fail(w, r, 409, code, preparationVerificationMessage(code))
		return
	}
	if (snapshot.AuthorizeOnly || snapshot.AuthorizeCommands) && !h.commandAuthorizationReady(ctx, t) {
		h.fail(w, r, 409, "COMMAND_TRUST_REQUIRED", "安装权限尚未确认，请核对原授权任务")
		return
	}
	if err = h.finishReconciledPreparation(actor, t, jobID); err != nil {
		if err.Error() != "HOST_CHANGED" {
			h.internal(w, r, err)
			return
		}
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机配置在核对期间变化，请刷新后重新核对原作业")
		return
	}
	h.getJob(w, r, jobID)
}
