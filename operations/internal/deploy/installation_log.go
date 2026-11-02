package deploy

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const installationLogLimit = 128 << 10
const installationLogLineLimit = 16 << 10

type installationOutputKey struct{}

// Commands keep their bounded return buffer for existing parsers. Only callers
// which already expose installation output attach this additional log writer.
type installationLogStreams struct{ stdout, stderr *installationLogWriter }

func installationOutput(ctx context.Context) io.Writer {
	streams, _ := ctx.Value(installationOutputKey{}).(*installationLogStreams)
	if streams == nil {
		return nil
	}
	return streams.stdout
}

type synchronizedCommandOutput struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *synchronizedCommandOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
func commandOutputs(ctx context.Context, captured io.Writer) (io.Writer, io.Writer) {
	streams, _ := ctx.Value(installationOutputKey{}).(*installationLogStreams)
	if streams == nil {
		return captured, captured
	}
	shared := &synchronizedCommandOutput{writer: captured}
	return io.MultiWriter(shared, streams.stdout), io.MultiWriter(shared, streams.stderr)
}
func (x *executor) loggedSoftwareCommand(ctx context.Context, req Request, phase, command string, args ...string) ([]byte, error) {
	ctx, closeLog := x.installationLogContext(ctx, req, phase)
	defer closeLog()
	return x.softwareRunner(ctx, command, args...)
}
func (x *executor) installationLogContext(ctx context.Context, req Request, phase string) (context.Context, func()) {
	streams := &installationLogStreams{stdout: &installationLogWriter{x: x, req: req, phase: phase}, stderr: &installationLogWriter{x: x, req: req, phase: phase}}
	x.appendServiceLog(req, phase, "[command started]")
	return context.WithValue(ctx, installationOutputKey{}, streams), func() { streams.stdout.Close(); streams.stderr.Close() }
}

// Never publish raw Write fragments: secrets and UTF-8 characters may straddle
// writes. An oversized line is discarded until its delimiter, rather than
// exposing a suffix whose password/token prefix was dropped.
type installationLogWriter struct {
	mu       sync.Mutex
	x        *executor
	req      Request
	phase    string
	pending  []byte
	ready    []byte
	dropping bool
}

func (w *installationLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	size := len(data)
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		segment := data
		if end >= 0 {
			segment = data[:end+1]
		}
		if !w.dropping {
			if len(w.pending)+len(segment) > installationLogLineLimit {
				w.pending = nil
				w.dropping = true
				w.ready = append(w.ready, []byte("[output line omitted: exceeds 16 KiB]\n")...)
			} else {
				w.pending = append(w.pending, segment...)
			}
		}
		if end < 0 {
			break
		}
		if !w.dropping {
			w.flush()
		}
		w.dropping = false
		data = data[end+1:]
	}
	w.publish()
	return size, nil
}
func (w *installationLogWriter) flush() {
	if len(w.pending) == 0 {
		return
	}
	text := string(w.pending)
	// A configured multiline credential must not leak one line at a time.
	for _, secret := range w.x.secrets {
		for _, part := range strings.FieldsFunc(secret, func(r rune) bool { return r == '\n' || r == '\r' }) {
			if part != "" {
				text = strings.ReplaceAll(text, part, "[REDACTED]")
			}
		}
	}
	w.ready = append(w.ready, []byte(text)...)
	w.pending = nil
	if len(w.ready) >= installationLogLineLimit {
		w.publish()
	}
}
func (w *installationLogWriter) publish() {
	if len(w.ready) > 0 {
		w.x.appendServiceLog(w.req, w.phase, string(w.ready))
		w.ready = nil
	}
}
func (w *installationLogWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.dropping {
		w.flush()
	}
	w.publish()
}

func (x *executor) appendServiceLog(req Request, phase, text string) {
	x.serviceLogMu.Lock()
	defer x.serviceLogMu.Unlock()
	file := filepath.Join(x.root, "jobs", req.JobID, "service-install.log")
	// Publish only a complete, already redacted tail. Concurrent readers retain
	// either the old file or the new file; no in-place truncate exposes a gap.
	previous := []byte(nil)
	if f, err := os.Open(file); err == nil {
		if info, e := f.Stat(); e == nil {
			_, _ = f.Seek(max(int64(0), info.Size()-installationLogLimit), io.SeekStart)
			previous, _ = io.ReadAll(io.LimitReader(f, installationLogLimit))
		}
		_ = f.Close()
	}
	entry := []byte(stamp() + " phase=" + phase + "\n" + x.redactSoftwareSecrets(text) + "\n")
	tail := append(previous, entry...)
	if len(tail) > installationLogLimit {
		tail = tail[len(tail)-installationLogLimit:]
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
		}
	}
	staged, err := os.CreateTemp(filepath.Dir(file), ".service-log-")
	if err != nil {
		return
	}
	name := staged.Name()
	defer os.Remove(name)
	if _, err = staged.Write(tail); err != nil {
		_ = staged.Close()
		return
	}
	if staged.Close() != nil {
		return
	}
	_ = os.Rename(name, file)
}
