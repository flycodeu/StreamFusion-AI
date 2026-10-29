package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type preparationInput struct {
	ExpectedVersion string `json:"expectedVersion"`
	Key             string `json:"idempotencyKey"`
	Confirmed       bool   `json:"confirmed"`
	SudoPassword    string `json:"sudoPassword,omitempty"`
}

type preparationSnapshot struct {
	Host      target `json:"host"`
	BinarySHA string `json:"binarySha256"`
	ScriptSHA string `json:"scriptSha256"`
}

var preparationMessages = map[string]string{
	"PREPARATION_READY":                "主机接入完成，可以继续安装软件。",
	"PREPARATION_CONNECT_FAILED":       "SSH 连接失败，请核对网络、账号、凭据和主机指纹。",
	"PREPARATION_PLATFORM_UNSUPPORTED": "此安装包仅支持 Linux amd64（x86_64），请核对服务器架构。",
	"PREPARATION_UPLOAD_FAILED":        "安装材料上传未完成，请检查目标临时目录空间和写入权限后重试。",
	"PREPARATION_SUDO_REQUIRED":        "需要管理员权限：请在页面输入当前连接账号的 sudo 密码，或使用已有管理员权限的连接。",
	"PREPARATION_RESULT_UNKNOWN":       "安装结果尚未确认，请点击核对安装结果。原作业已保留，避免重复安装。",
	"PREPARATION_TARGET_NOT_READY":     "执行器或授权尚未就绪，请保留原目录，在高级诊断中核对归属和未决作业。",
	"PREPARATION_INPUT_DAMAGED":        "安装记录不完整，请保留原作业并检查控制器日志。",
	"PREPARATION_MATERIAL_CHANGED":     "安装材料已变化，请重新打开主机安装页面。",
	"PREPARATION_HOST_CHANGED":         "主机配置或维护状态已变化，安装尚未开始，请刷新后重试。",
	"CONTROLLER_RESTARTED":             "控制器在安装期间重启，请核对原作业的安装结果。",
}

func (h *HTTP) latestPreparationJob(id string) any {
	var job, status, phase, code, trace, at string
	if h.store.DB.QueryRow("SELECT id,status,phase,error_code,trace_id,updated_at FROM jobs WHERE target_id=? AND kind='HOST_PREPARE' ORDER BY created_at DESC,id DESC LIMIT 1", id).Scan(&job, &status, &phase, &code, &trace, &at) != nil {
		return nil
	}
	return map[string]string{"id": job, "status": status, "phase": phase, "errorCode": code, "message": preparationMessages[code], "traceId": trace, "updatedAt": at}
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
	p, material := preparationForHost(t, h.store.controllerID(), targetReleaseDirectory())
	if !p.Package.Available {
		h.fail(w, r, 409, p.Package.Code, p.Package.Message)
		return
	}
	archive, err := createTargetPreparationArchive(p, material)
	if err != nil {
		h.fail(w, r, 409, "TARGET_PACKAGE_CHANGED", "安装材料已变化，请刷新后重试")
		return
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	snapshot := preparationSnapshot{Host: t, BinarySHA: material.Binary.Hash, ScriptSHA: material.Script.Hash}
	// Observation timestamps are not part of the user's repeat-submit identity.
	requestHash := deploy.Digest([]string{t.ID, t.Version, material.Binary.Hash, material.Script.Hash})
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
	if err = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(210 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		if err = h.finishPreparation(job, "FAILED", "PREPARATION_CONNECT_FAILED"); err != nil {
			h.internal(w, r, err)
			return
		}
		h.getJob(w, r, job)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	code, uncertain := h.runHostPreparation(ctx, actor, job, t, archive, material.Binary.Hash, in.SudoPassword)
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
	_, err := tx.Exec("UPDATE jobs SET status=?,error_code=?,phase='PREPARATION_RESULT',result_json=?,updated_at=? WHERE id=? AND kind='HOST_PREPARE'", status, code, jsonText(map[string]string{"message": preparationMessages[code]}), Now(), job)
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

func (h *HTTP) runHostPreparation(ctx context.Context, actor *session, job string, t target, archive *os.File, binarySHA, password string) (string, bool) {
	client, closeClient, err := h.store.connectSSH(ctx, t)
	if err != nil {
		return "PREPARATION_CONNECT_FAILED", false
	}
	defer closeClient()
	out, stderr := &limitedOutput{limit: 16 << 10}, &limitedOutput{limit: 4 << 10}
	if runSSHCommand(client, hostReadOnlyCommand, nil, out, stderr) != nil {
		return "PREPARATION_CONNECT_FAILED", false
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out.String(), "\r\n", "\n")), "\n")
	if len(lines) != 4 || lines[0] != "SFOPS_HOST_V1" || lines[1] != "Linux" || lines[2] != "x86_64" {
		return "PREPARATION_PLATFORM_UNSUPPORTED", false
	}
	stage := "/tmp/sfops-upload-" + job
	defer func() {
		// Remove only the exact temporary upload created by this job. A canceled
		// request also closes the SSH connection, so use a short independent
		// cleanup connection instead of leaving a partial archive in /tmp.
		cleanup := "/bin/sh -c " + shellQuote("rm -f -- "+shellQuote(stage+"/bundle.tar.gz")+"; rmdir -- "+shellQuote(stage)+" 2>/dev/null")
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
	command := "umask 077; mkdir -m 700 -- " + shellQuote(stage) + " && cat > " + shellQuote(stage+"/bundle.tar.gz")
	if runSSHCommand(client, "/bin/sh -c "+shellQuote(command), archive, io.Discard, stderr) != nil || ctx.Err() != nil {
		return "PREPARATION_UPLOAD_FAILED", false
	}
	// Revalidate the actor immediately before privilege elevation.
	h.store.mu.Lock()
	tx, authErr := h.beginAuthorized(actor)
	if authErr == nil {
		var currentVersion string
		var maintenance bool
		authErr = tx.QueryRow("SELECT CAST(version AS TEXT),maintenance FROM targets WHERE id=?", t.ID).Scan(&currentVersion, &maintenance)
		if authErr == nil && (currentVersion != t.Version || maintenance) {
			authErr = errors.New("HOST_CHANGED")
		}
		if authErr == nil {
			authErr = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	h.store.mu.Unlock()
	if authErr != nil {
		return "PREPARATION_HOST_CHANGED", false
	}
	h.preparationPhase(job, "INSTALLING_EXECUTOR")
	command = remotePreparationCommand(stage, fmt.Sprintf("%x", hash.Sum(nil)), t.User == "root", password != "")
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
			return "PREPARATION_RESULT_UNKNOWN", true
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
	if !h.verifyPreparation(ctx, t, binarySHA) {
		return "PREPARATION_RESULT_UNKNOWN", true
	}
	return "PREPARATION_READY", false
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// A fixed bootstrap over the existing SSH transport, not a general shell API.
// Root stages a private copy and verifies the entire archive before executing.
func remotePreparationCommand(stage, digest string, root, password bool) string {
	script := `set -eu
printf 'SFOPS_SETUP_ENTERED\n' >&2
umask 077
work=$(mktemp -d /var/tmp/sfops-setup-XXXXXX)
cleanup(){ rm -f -- "$work/bundle.tar.gz" "$work/streamfusion-ops" "$work/install-target.sh" "$work/install.sh" "$work/SHA256SUMS" "$work/HOST-SETUP.md"; rmdir -- "$work"; }
trap cleanup EXIT
test -f ` + shellQuote(stage+"/bundle.tar.gz") + ` && test ! -L ` + shellQuote(stage+"/bundle.tar.gz") + `
cat -- ` + shellQuote(stage+"/bundle.tar.gz") + ` > "$work/bundle.tar.gz"
printf '%s  %s\n' ` + shellQuote(digest) + ` "$work/bundle.tar.gz" | sha256sum -c - --status
tar -xzf "$work/bundle.tar.gz" -C "$work" --no-same-owner
cd "$work"
bash ./install.sh --non-interactive
`
	command := "/bin/bash -c " + shellQuote(script)
	if root {
		return command
	}
	if password {
		return "/usr/bin/sudo -S -p '' -- " + command
	}
	return "/usr/bin/sudo -n -- " + command
}

func (h *HTTP) verifyPreparation(ctx context.Context, t target, sha string) bool {
	var caps executorCapabilities
	if h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &caps) != nil || caps.Owner.ControllerID != h.store.controllerID() || caps.Owner.TargetID != t.ID || caps.Owner.ProtocolVersion != 1 || caps.Owner.ExecutorVersion != deploy.Version || caps.Platform != "linux/amd64" {
		return false
	}
	if sha == "" {
		return true
	}
	client, closeClient, err := h.store.connectSSH(ctx, t)
	if err != nil {
		return false
	}
	defer closeClient()
	out := &limitedOutput{limit: 512}
	if runSSHCommand(client, "sha256sum -- "+shellQuote(t.InstallRoot+"/executor"), nil, out, io.Discard) != nil {
		return false
	}
	parts := strings.Fields(out.String())
	return len(parts) == 2 && parts[0] == sha
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
	if !h.verifyPreparation(ctx, t, snapshot.BinarySHA) {
		h.fail(w, r, 409, "PREPARATION_TARGET_NOT_READY", preparationMessages["PREPARATION_TARGET_NOT_READY"])
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
