package controller

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

func preparationPrivilegeCommand(t target, password bool, script string) string {
	command := "/bin/bash -c " + shellQuote(script)
	if t.User == "root" {
		return command
	}
	if password {
		return "/usr/bin/sudo -S -p '' -- " + command
	}
	return "/usr/bin/sudo -n -- " + command
}

func preparationPassword(password string) io.Reader {
	if password == "" {
		return nil
	}
	return strings.NewReader(password + "\n")
}

// Check the same fixed privileged shell before transferring any component.
// The password stays in this request and is sent only through SSH stdin.
func checkPreparationPrivilege(client *ssh.Client, t target, password string) string {
	if t.User == "root" {
		return ""
	}
	out, stderr := &limitedOutput{limit: 128}, &limitedOutput{limit: 1024}
	timer := time.AfterFunc(20*time.Second, func() { _ = client.Close() })
	defer timer.Stop()
	command := preparationPrivilegeCommand(t, password != "", controllerScript("preparation/check-privilege.sh"))
	if err := runSSHCommand(client, command, preparationPassword(password), out, stderr); err != nil {
		diagnostic := strings.ToLower(stderr.String())
		if strings.Contains(diagnostic, "not in the sudoers") || strings.Contains(diagnostic, "not allowed to execute") || strings.Contains(diagnostic, "may not run sudo") {
			return "PREPARATION_SUDO_DENIED"
		}
		if strings.Contains(diagnostic, "sudo") || strings.Contains(diagnostic, "password") {
			return "PREPARATION_SUDO_REQUIRED"
		}
		return "PREPARATION_CONNECT_FAILED"
	}
	if strings.TrimSpace(out.String()) != "SFOPS_PRIVILEGE_READY" {
		return "PREPARATION_CONNECT_FAILED"
	}
	return ""
}

func (h *HTTP) preparationActorCurrent(actor *session, t target, job string, repair bool) bool {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	var version string
	var maintenance bool
	if tx.QueryRow("SELECT CAST(version AS TEXT),maintenance FROM targets WHERE id=?", t.ID).Scan(&version, &maintenance) != nil || version != t.Version || maintenance {
		return false
	}
	status := "RUNNING"
	if repair {
		status = "UNKNOWN"
	}
	var held int
	if tx.QueryRow(`SELECT count(*) FROM jobs j JOIN operation_locks l ON l.job_id=j.id AND l.resource_key='target:'||j.target_id WHERE j.id=? AND j.target_id=? AND j.kind='HOST_PREPARE' AND j.status=?`, job, t.ID, status).Scan(&held) != nil || held != 1 {
		return false
	}
	return tx.Commit() == nil
}

func (h *HTTP) commandAuthorizationReady(ctx context.Context, t target) bool {
	var capabilities executorCapabilities
	return h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &capabilities) == nil &&
		capabilities.Owner.ControllerID == h.store.controllerID() && capabilities.Owner.TargetID == t.ID &&
		capabilities.CommandTrustFingerprint == deploy.CommandPublicKeyFingerprint(h.store.commandPublicKey())
}

func (h *HTTP) runCommandAuthorization(ctx context.Context, actor *session, job string, t target, snapshot preparationSnapshot, password string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	checked := h.checkPreparationUpdate(ctx, t, job)
	if checked.Status != "CURRENT" || checked.CurrentSHA256 != snapshot.BinarySHA || checked.PackageSHA256 != snapshot.BinarySHA {
		if checked.Code != "" {
			return checked.Code, false
		}
		return "PREPARATION_UPDATE_CHANGED", false
	}
	if h.commandAuthorizationReady(ctx, t) {
		return "PREPARATION_READY", false
	}
	client, closeClient, err := h.store.connectSSH(ctx, t)
	if err != nil {
		return "PREPARATION_CONNECT_FAILED", false
	}
	defer closeClient()
	if code := checkPreparationPrivilege(client, t, password); code != "" {
		return code, false
	}
	if !h.preparationActorCurrent(actor, t, job, false) {
		return "PREPARATION_HOST_CHANGED", false
	}
	h.preparationPhase(job, "AUTHORIZING_INSTALLATION")
	file := shellQuote(t.InstallRoot + "/executor")
	identity := " --root " + shellQuote(t.InstallRoot) + " --controller-id " + shellQuote(h.store.controllerID()) + " --target-id " + shellQuote(t.ID)
	script := strings.NewReplacer("@FILE@", file, "@SHA@", shellQuote(snapshot.BinarySHA), "@IDENTITY@", identity, "@KEY@", shellQuote(h.store.commandPublicKey())).Replace(controllerScript("preparation/authorize.sh.tmpl"))
	out, stderr := &limitedOutput{limit: 512}, &limitedOutput{limit: 1024}
	if err := runSSHCommand(client, preparationPrivilegeCommand(t, password != "", script), preparationPassword(password), out, stderr); err != nil || !strings.HasSuffix(strings.TrimSpace(out.String()), "SFOPS_AUTHORIZATION_READY") {
		return "PREPARATION_RESULT_UNKNOWN", true
	}
	if !h.commandAuthorizationReady(ctx, t) {
		return "PREPARATION_RESULT_UNKNOWN", true
	}
	return "PREPARATION_READY", false
}
