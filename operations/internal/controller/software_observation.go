package controller

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

// One observation keeps one authenticated SSH connection. Every fixed probe
// uses its own bounded session, so a command timeout closes that session while
// the request deadline and final cleanup own the connection lifetime.
func (h *HTTP) withSoftwareRunner(ctx context.Context, t target, observe func(deploy.SoftwareCommandRunner) error) error {
	var client *ssh.Client
	if t.Kind == "SSH" {
		connected, closeClient, e := h.store.connectSSH(ctx, t)
		if e != nil {
			return e
		}
		client = connected
		defer closeClient()
	} else if runtime.GOOS != "linux" {
		return errors.New("LINUX_OBSERVATION_REQUIRED")
	}
	return observe(func(commandCtx context.Context, command string, args ...string) ([]byte, error) {
		out := &softwareOutput{output: limitedOutput{limit: 64 << 10}}
		errOut := &softwareOutput{output: limitedOutput{limit: 4 << 10}}
		stderr := errOut
		if command == "/usr/bin/java" || command == "/usr/bin/python3" || command == "/usr/bin/ffmpeg" || command == "/usr/bin/journalctl" || (command == "/usr/bin/docker" && len(args) > 0 && args[0] == "logs") {
			stderr = out
		}
		if client != nil {
			parts := []string{"/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=", shellLiteral(command)}
			for _, a := range args {
				parts = append(parts, shellLiteral(a))
			}
			session, e := client.NewSession()
			if e != nil {
				return nil, e
			}
			defer session.Close()
			stop := context.AfterFunc(commandCtx, func() { _ = session.Close() })
			defer stop()
			session.Stdout, session.Stderr = out, stderr
			e = session.Run(strings.Join(parts, " "))
			if _, overflow := out.Result(); overflow != nil {
				return nil, overflow
			}
			if commandCtx.Err() != nil {
				return nil, commandCtx.Err()
			}
			if e != nil {
				return nil, e
			}
			return out.Result()
		}
		cmd := exec.CommandContext(commandCtx, command, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER="}
		cmd.Stdout = out
		cmd.Stderr = stderr
		e := cmd.Run()
		if _, overflow := out.Result(); overflow != nil {
			return nil, overflow
		}
		if e != nil {
			return nil, e
		}
		return out.Result()
	})
}

// SSH and local commands can write stdout and stderr concurrently (Java emits
// its version on stderr). Keep that merged bounded stream race-free.
type softwareOutput struct {
	mu       sync.Mutex
	output   limitedOutput
	overflow bool
}

func (b *softwareOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.output.limit-b.output.Len() {
		b.overflow = true
	}
	return b.output.Write(p)
}

func (b *softwareOutput) Result() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return nil, errors.New("SOFTWARE_OBSERVATION_TOO_LARGE")
	}
	return b.output.Bytes(), nil
}

func shellLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (h *HTTP) observeSoftware(w http.ResponseWriter, r *http.Request, s *session, id string) {
	i, e := h.store.readSoftware(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if i.ManagementMode == "RELEASED" {
		h.fail(w, r, 409, "SOFTWARE_RELEASED", "该记录已解除管理")
		return
	}
	if i.Installation != nil && !installationBound(i) {
		h.fail(w, r, 409, "INSTALLATION_NOT_BOUND", "安装尚无已确认容器，请先查看或核对安装作业")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	release, e := h.acquireObservation(ctx, i.HostID, false)
	if e != nil {
		h.fail(w, r, 429, "HOST_CHECK_BUSY", "主机正在检测，请稍后重试")
		return
	}
	defer release()
	t, e := h.store.readTarget(i.HostID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	var obs softwareObservation
	runnerErr := h.withSoftwareRunner(ctx, t, func(run deploy.SoftwareCommandRunner) error { obs = h.probeSoftware(ctx, t, i, run); return nil })
	if obs.SoftwareObservation == nil {
		code := "SOFTWARE_OBSERVATION_UNAVAILABLE"
		if runnerErr != nil {
			code, _ = hostSSHError(runnerErr)
		}
		obs = failedSoftwareObservation(i, code)
	}
	if e = h.saveSoftwareObservation(t, i, obs, r.Context().Value(traceKey{}).(string), observationPrincipal{Session: s}); e != nil {
		h.fail(w, r, 409, "OBSERVATION_CONFLICT", "检测期间配置已变化，请刷新")
		return
	}
	i, e = h.store.readSoftware(id)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, i)
}
func failedSoftwareObservation(i softwareInstance, code string) softwareObservation {
	return softwareObservation{SoftwareObservation: &deploy.SoftwareObservation{Kind: i.RuntimeKind, Locator: i.Locator, State: "UNKNOWN", ObservedAt: Now()}, ErrorCode: code, Message: "无法确认实际状态，请检查主机连接、服务标识与读取权限"}
}
func (h *HTTP) probeSoftware(ctx context.Context, t target, i softwareInstance, run deploy.SoftwareCommandRunner) softwareObservation {
	if i.Installation != nil && !installationBound(i) {
		return failedSoftwareObservation(i, "INSTALLATION_NOT_BOUND")
	}
	if i.Installation != nil {
		var fact deploy.SoftwareObservation
		if err := h.store.callTarget(ctx, t, deploy.Request{Action: "SOFTWARE_OBSERVE", Software: softwareRequest(i)}, "", &fact); err != nil {
			return failedSoftwareObservation(i, executorFailureCode(err))
		}
		return softwareObservation{SoftwareObservation: &fact, Source: "EXECUTOR_READ_ONLY"}
	}
	probe := softwareRequest(i)
	if i.ManagementMode == "OBSERVE" {
		probe.IdentityHash = ""
		probe.Locator = i.Locator
	}
	fact, e := deploy.ObserveSoftware(ctx, *probe, run)
	if e != nil {
		code := "SOFTWARE_OBSERVATION_UNAVAILABLE"
		if c := e.Error(); strings.HasPrefix(c, "SOFTWARE_") && len(c) < 90 && !strings.ContainsAny(c, " \r\n\t:") {
			code = c
		}
		return failedSoftwareObservation(i, code)
	}
	deploy.EnrichSoftwareObservation(ctx, fact, run)
	source := "SSH_READ_ONLY"
	if t.Kind == "LOCAL" {
		source = "LOCAL_READ_ONLY"
	}
	return softwareObservation{SoftwareObservation: fact, Source: source}
}

func (h *HTTP) softwareLogs(w http.ResponseWriter, r *http.Request, id string) {
	if !h.acquireHostCheck(w, r) {
		return
	}
	defer func() { <-h.hostCheckSlots }()
	i, e := h.store.readSoftware(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if i.ManagementMode == "RELEASED" || i.RuntimeKind == "TOOL" || i.IdentityHash == "" {
		h.fail(w, r, 409, "SOFTWARE_LOG_UNAVAILABLE", "该记录没有运行日志")
		return
	}
	t, e := h.store.readTarget(i.HostID)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out *deploy.SoftwareObservation
	if i.Installation != nil {
		out = &deploy.SoftwareObservation{}
		e = h.store.callTarget(ctx, t, deploy.Request{Action: "SOFTWARE_LOGS", Software: softwareRequest(i)}, "", out)
	} else {
		e = h.withSoftwareRunner(ctx, t, func(run deploy.SoftwareCommandRunner) error {
			var err error
			out, err = deploy.ReadSoftwareLogs(ctx, *softwareRequest(i), run)
			return err
		})
	}
	if e != nil {
		h.fail(w, r, 409, "SOFTWARE_LOG_UNAVAILABLE", "日志未确认，请检查服务身份和读取权限")
		return
	}
	if i.Installation != nil {
		for _, ref := range installCredentials(i.Installation.Config) {
			if secret, err := h.store.secret(ref.ID, ref.Revision, ref.Kind); err == nil {
				out.Log = strings.ReplaceAll(out.Log, string(secret), "[REDACTED]")
			}
		}
	}
	h.success(w, r, map[string]any{"text": out.Log, "limited": out.Truncated, "observedAt": out.ObservedAt})
}
