package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type terminalInstallInput struct {
	ExpectedVersion string `json:"expectedVersion"`
	Confirmed       bool   `json:"confirmed"`
	Key             string `json:"idempotencyKey"`
	SudoPassword    string `json:"sudoPassword,omitempty"`
}

func (h *HTTP) hostTerminalRoute(w http.ResponseWriter, r *http.Request, actor *session, parts []string) bool {
	id := parts[1]
	if len(parts) == 4 && r.Method == http.MethodPost {
		switch parts[3] {
		case "install":
			h.installHostTerminal(w, r, actor, id)
			return true
		case "reconcile":
			var in struct {
				JobID           string `json:"jobId"`
				ExpectedVersion string `json:"expectedVersion"`
			}
			if decode(w, r, &in) != nil || !deploy.ID.MatchString(in.JobID) || in.ExpectedVersion == "" {
				h.fail(w, r, 400, "INVALID_INPUT", "请刷新主机并选择原安装作业")
			} else {
				h.reconcileTerminalJob(w, r, actor, id, in.JobID, in.ExpectedVersion)
			}
			return true
		}
	}
	if r.Method != http.MethodGet || (len(parts) != 3 && !(len(parts) == 4 && parts[3] == "download")) {
		return false
	}
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return true
	}
	if len(parts) == 4 {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="install-sf-docker.sh"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, serviceinstall.HostTerminalInstaller())
		return true
	}
	job, err := h.latestHostSetupJob(id, true)
	if err != nil {
		h.internal(w, r, err)
		return true
	}
	view := map[string]any{"hostId": id, "expectedVersion": t.Version, "downloadUrl": api + "/hosts/" + id + "/terminal-tools/download", "onlineAvailable": t.Kind == "SSH" && !t.Maintenance}
	if job != nil {
		view["job"] = job
	}
	h.success(w, r, view)
	return true
}

func (h *HTTP) installHostTerminal(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in terminalInstallInput
	if h.decodeSensitive(w, r, actor, &in) != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	if !in.Confirmed || in.ExpectedVersion == "" || !deploy.ID.MatchString(in.Key) || len(in.SudoPassword) > 1024 || strings.ContainsAny(in.SudoPassword, "\r\n\x00") {
		h.fail(w, r, 400, "INVALID_INPUT", "请确认主机和管理员权限后准备终端工具")
		return
	}
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Kind != "SSH" {
		h.fail(w, r, 409, "SSH_TARGET_REQUIRED", "请下载脚本，在目标 Linux 主机终端运行")
		return
	}
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	installer := serviceinstall.HostTerminalInstaller()
	snapshot := preparationSnapshot{Host: t, TerminalTools: true, ScriptSHA: fmt.Sprintf("%x", sha256.Sum256([]byte(installer)))}
	// acceptPreparation checks retry identity before checking the current host version.
	snapshot.Host.Version = in.ExpectedVersion
	hash := deploy.Digest([]string{"HOST_TERMINAL_TOOLS", id, in.ExpectedVersion})
	job, reused, err := h.acceptPreparation(actor, in.Key, hash, snapshot, r.Context().Value(traceKey{}).(string))
	if err != nil {
		switch err.Error() {
		case "HOST_BUSY", "HOST_MAINTENANCE", "VERSION_CONFLICT", "IDEMPOTENCY_CONFLICT":
			h.fail(w, r, 409, err.Error(), "请刷新主机状态并核对已有作业")
		default:
			h.opsError(w, r, err)
		}
		return
	}
	if !reused {
		code, status := "PREPARATION_CONNECT_FAILED", "FAILED"
		err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(preparationResponseTimeout))
		if err == nil || errors.Is(err, http.ErrNotSupported) {
			ctx, cancel := context.WithTimeout(r.Context(), preparationTimeout)
			defer cancel()
			status, code = h.runHostTerminal(ctx, actor, job, snapshot, installer, in.SudoPassword)
		}
		in.SudoPassword = ""
		if err := h.finishPreparation(job, status, code); err != nil {
			h.jobUnknown(job, "TERMINAL_TOOLS_RESULT_UNKNOWN")
			h.internal(w, r, err)
			return
		}
	}
	h.success(w, r, map[string]string{"jobId": job})
}

func terminalReceiptScript(name, owner, job, sha string, deadline int64) string {
	return strings.NewReplacer("@OWNER@", owner, "@IDENTITY@", shellQuote(job+" "+sha), "@DEADLINE@", strconv.FormatInt(deadline, 10)).Replace(
		controllerScript("preparation/terminal-receipt.sh.tmpl") + controllerScript("preparation/terminal-"+name+".sh.tmpl"))
}

// SSH output is untrusted, including apt diagnostics. Only translate installer markers.
func terminalFailureCode(marker string) string {
	if marker == "SFOPS_TERMINAL_FAILED" {
		return "TERMINAL_TOOLS_INSTALL_FAILED"
	}
	stage, ok := strings.CutPrefix(marker, "SFOPS_TERMINAL_FAILED_")
	if ok {
		switch stage {
		case "INTEGRITY", "PLATFORM", "LAUNCHER", "DPKG", "APT_UPDATE", "APT_PLAN", "APT_INSTALL", "VERIFY":
			return "TERMINAL_TOOLS_" + stage
		}
	}
	return ""
}

func (h *HTTP) runHostTerminal(ctx context.Context, actor *session, job string, snapshot preparationSnapshot, installer, password string) (string, string) {
	t := snapshot.Host
	controller := h.store.controllerID()
	if !deploy.ID.MatchString(controller) {
		return "FAILED", "PREPARATION_INPUT_DAMAGED"
	}
	client, closeClient, err := h.store.connectSSH(ctx, t)
	if err != nil {
		return "FAILED", "PREPARATION_CONNECT_FAILED"
	}
	defer closeClient()
	if code := checkPreparationPrivilege(client, t, password); code != "" {
		return "FAILED", code
	}
	stage := "/tmp/sfops-terminal-" + job
	file := stage + "/installer.sh"
	cleanup := "/bin/bash -c " + shellQuote(strings.ReplaceAll(controllerScript("preparation/terminal-cleanup.sh.tmpl"), "@STAGE@", shellQuote(stage)))
	defer func() {
		if ctx.Err() == nil && runSSHCommand(client, cleanup, nil, io.Discard, io.Discard) == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, closeSSH, err := h.store.connectSSH(cleanupCtx, t)
		if err == nil {
			defer closeSSH()
			_ = runSSHCommand(c, cleanup, nil, io.Discard, io.Discard)
		}
	}()
	upload := controllerTemplate("preparation/upload.sh.tmpl", shellQuote(stage), shellQuote(file))
	if runSSHCommand(client, "/bin/sh -c "+shellQuote(upload), strings.NewReader(installer), io.Discard, io.Discard) != nil {
		return "FAILED", "PREPARATION_UPLOAD_FAILED"
	}
	// Use the target's clock for the start deadline, including subsequent read-only verification.
	out := &limitedOutput{limit: 128}
	if runSSHCommand(client, "/bin/date +%s", nil, out, io.Discard) != nil {
		return "FAILED", "PREPARATION_CONNECT_FAILED"
	}
	now, err := strconv.ParseInt(strings.TrimSpace(out.String()), 10, 64)
	if err != nil || now < 1 || now > 1<<50 {
		return "FAILED", "READ_ONLY_RESPONSE_INVALID"
	}
	deadline := now + 120
	if !h.preparationActorCurrent(actor, t, job, false) {
		return "FAILED", "PREPARATION_HOST_CHANGED"
	}
	res, err := h.store.DB.Exec(`UPDATE jobs SET phase='TERMINAL_INSTALLING',result_json=json_set(result_json,'$.terminalStartBefore',?),updated_at=?
        WHERE id=? AND status='RUNNING' AND phase='TERMINAL_CONNECTING'
        AND EXISTS(SELECT 1 FROM targets WHERE id=jobs.target_id AND CAST(version AS TEXT)=? AND maintenance=0)
        AND EXISTS(SELECT 1 FROM operation_locks WHERE job_id=jobs.id AND resource_key='target:'||jobs.target_id)`, deadline, Now(), job, t.Version)
	if err != nil {
		return "FAILED", "PREPARATION_INPUT_DAMAGED"
	}
	if count, _ := res.RowsAffected(); count != 1 {
		return "FAILED", "PREPARATION_HOST_CHANGED"
	}
	script := terminalReceiptScript("install", controller+"-"+t.ID, job, snapshot.ScriptSHA, deadline)
	script = strings.NewReplacer("@STAGED@", shellQuote(file), "@SHA@", shellQuote(snapshot.ScriptSHA), "@LIMIT@", strconv.Itoa(len(installer)+1)).Replace(script)
	if t.User == "root" {
		password = ""
	}
	out.Reset()
	err = runSSHCommand(client, preparationPrivilegeCommand(t, password != "", script), preparationPassword(password), out, io.Discard)
	marker := strings.TrimSpace(out.String())
	if err == nil && marker == "SFOPS_TERMINAL_READY" {
		return "SUCCEEDED", "TERMINAL_TOOLS_READY"
	}
	if code := terminalFailureCode(marker); code != "" {
		return "FAILED", code
	}
	return "UNKNOWN", "TERMINAL_TOOLS_RESULT_UNKNOWN"
}

func (h *HTTP) reconcileTerminalJob(w http.ResponseWriter, r *http.Request, actor *session, id, job, version string) {
	var raw, status, phase string
	var deadline int64
	err := h.store.DB.QueryRow("SELECT input_json,status,phase,coalesce(json_extract(result_json,'$.terminalStartBefore'),0) FROM jobs WHERE id=? AND target_id=? AND kind='HOST_PREPARE' AND json_extract(input_json,'$.terminalTools')=1", job, id).Scan(&raw, &status, &phase, &deadline)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if status == "RUNNING" {
		h.fail(w, r, 409, "HOST_BUSY", "安装仍在执行，请稍后刷新")
		return
	}
	if status != "UNKNOWN" {
		h.success(w, r, map[string]string{"jobId": job})
		return
	}
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	var snapshot preparationSnapshot
	controller := h.store.controllerID()
	if deploy.StrictJSON([]byte(raw), &snapshot) != nil || !snapshot.TerminalTools || snapshot.Host.ID != id || !validSHA256(snapshot.ScriptSHA) || !deploy.ID.MatchString(controller) {
		h.fail(w, r, 409, "PREPARATION_INPUT_DAMAGED", preparationMessage("PREPARATION_INPUT_DAMAGED"))
		return
	}
	if snapshot.Host.sshTarget != t.sshTarget || version != "" && t.Version != version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机连接已变化，请刷新并核对原作业")
		return
	}
	// The durable dispatch phase precedes the first privileged installer command.
	finalStatus, code := "FAILED", "TERMINAL_TOOLS_INSTALL_FAILED"
	if phase != "TERMINAL_CONNECTING" {
		if phase != "TERMINAL_INSTALLING" || deadline < 1 {
			h.fail(w, r, 409, "PREPARATION_INPUT_DAMAGED", preparationMessage("PREPARATION_INPUT_DAMAGED"))
			return
		}
		if !h.acquireHostCheck(w, r) {
			return
		}
		defer func() { <-h.hostCheckSlots }()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		client, closeClient, err := h.store.connectSSH(ctx, t)
		if err != nil {
			h.fail(w, r, 409, "PREPARATION_CONNECT_FAILED", preparationMessage("PREPARATION_CONNECT_FAILED"))
			return
		}
		defer closeClient()
		script := terminalReceiptScript("check", controller+"-"+id, job, snapshot.ScriptSHA, deadline)
		out := &limitedOutput{limit: 128}
		if runSSHCommand(client, "/bin/bash -c "+shellQuote(script), nil, out, io.Discard) != nil {
			h.fail(w, r, 409, "TERMINAL_TOOLS_RESULT_UNKNOWN", preparationMessage("TERMINAL_TOOLS_RESULT_UNKNOWN"))
			return
		}
		marker := strings.TrimSpace(out.String())
		switch failure := terminalFailureCode(marker); {
		case marker == "SFOPS_TERMINAL_READY":
			finalStatus, code = "SUCCEEDED", "TERMINAL_TOOLS_READY"
		case failure != "":
			code = failure
		default:
			h.fail(w, r, 409, "TERMINAL_TOOLS_RESULT_UNKNOWN", preparationMessage("TERMINAL_TOOLS_RESULT_UNKNOWN"))
			return
		}
	}
	if err = h.finishCheckedHostJob(actor, t, job, finalStatus, code); err != nil {
		if err.Error() == "HOST_CHANGED" {
			h.fail(w, r, 409, "VERSION_CONFLICT", "主机在核对期间变化，请刷新并重新核对原作业")
		} else {
			h.opsError(w, r, err)
		}
		return
	}
	h.success(w, r, map[string]string{"jobId": job})
}
