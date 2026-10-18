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

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type boundedArchiveWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedArchiveWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("RUNTIME_ARCHIVE_LIMIT")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func (s *Store) exportRuntime(ctx context.Context, t target, installation pluginInstallation, filename string) error {
	definition, err := installationDefinition(installation)
	if err != nil {
		return err
	}
	if t.Kind != "SSH" && runtime.GOOS != "linux" {
		return errors.New("LINUX_TARGET_REQUIRED")
	}
	if !deploy.ValidRoot(t.InstallRoot) {
		return errors.New("TARGET_PATH_INVALID")
	}
	req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: s.controllerID(), TargetID: t.ID, Action: "JAVA_RUNTIME_EXPORT", Plugin: pluginRequest(definition), PluginInstallationID: installation.ID}
	raw, _ := json.Marshal(req)
	input := bytes.NewReader(append(raw, '\n'))
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		file.Close()
		if !ok {
			os.Remove(filename)
		}
	}()
	out := &boundedArchiveWriter{file, deploy.ArchiveLimit}
	stderr := &limitedOutput{limit: 1024}
	if t.Kind == "SSH" {
		err = s.sshExecute(ctx, t, input, out, stderr)
	} else {
		cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", filepath.Join(t.InstallRoot, "executor"), "target-exec")
		cmd.Stdin = input
		cmd.Stdout = out
		cmd.Stderr = stderr
		err = cmd.Run()
	}
	if err != nil {
		return errors.New("RUNTIME_EXPORT_UNAVAILABLE")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	ok = true
	return file.Close()
}
