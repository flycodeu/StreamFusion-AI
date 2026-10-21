package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// All target invocations use one protected executable with one fixed argument.
// No client input is evaluated by a shell or passed as a Docker option.
func (s *Store) callTarget(ctx context.Context, t target, req deploy.Request, archive string, dst any) error {
	if t.Kind != "SSH" && runtime.GOOS != "linux" {
		return errors.New("LINUX_TARGET_REQUIRED")
	}
	if !deploy.ValidRoot(t.InstallRoot) {
		return errors.New("TARGET_PATH_INVALID")
	}
	req.ProtocolVersion = 1
	req.ExecutorVersion = deploy.Version
	req.ControllerID = s.controllerID()
	req.TargetID = t.ID
	if err := s.releaseSecrets(&req); err != nil {
		return err
	}
	b, _ := json.Marshal(req)
	input := io.Reader(bytes.NewReader(append(b, '\n')))
	var f *os.File
	if archive != "" {
		var e error
		f, e = os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		input = io.MultiReader(input, f)
	}
	out := &limitedOutput{limit: 128 << 10}
	errOut := &limitedOutput{limit: 1024}
	if t.Kind == "SSH" {
		if e := s.sshExecute(ctx, t, input, out, errOut); e != nil {
			return e
		}
		return deploy.StrictJSON(out.Bytes(), dst)
	}
	command, args := "/usr/bin/sudo", []string{"-n", filepath.Join(t.InstallRoot, "executor"), "target-exec"}
	if os.Geteuid() == 0 {
		command, args = filepath.Join(t.InstallRoot, "executor"), []string{"target-exec"}
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = input
	cmd.Stdout = out
	cmd.Stderr = errOut
	if e := cmd.Run(); e != nil {
		return classifyExecutorInvocation(e, errOut.String())
	}
	return deploy.StrictJSON(out.Bytes(), dst)
}

type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if left := b.limit - b.Len(); left > 0 {
		if left > n {
			left = n
		}
		b.Buffer.Write(p[:left])
	}
	return n, nil
}
func (h *HTTP) probe(t target) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return h.probeContext(ctx, t)
}
func (h *HTTP) probeContext(ctx context.Context, t target) (json.RawMessage, error) {
	var out json.RawMessage
	e := h.store.callTarget(ctx, t, deploy.Request{Action: "PROBE"}, "", &out)
	if e == nil {
		res, err := h.store.DB.Exec("UPDATE targets SET observation_json=?,observed_at=? WHERE id=? AND CAST(version AS TEXT)=?", string(out), Now(), t.ID, t.Version)
		e = err
		if e == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				e = errors.New("HOST_CHANGED")
			}
		}
	}
	return out, e
}
