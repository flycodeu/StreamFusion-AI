package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func installationLogFixture(t *testing.T) (*executor, Request, string) {
	t.Helper()
	x, req := serviceRequestFixture(t)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return x, req, filepath.Join(dir, "service-install.log")
}
func TestInstallationStreamingRedactsAcrossWritesAndDropsOversizedLines(t *testing.T) {
	x, req, file := installationLogFixture(t)
	x.secrets = map[string]string{"password": "known-private-value", "multiline": "line-one-private\nline-two-private"}
	ctx, closeLog := x.installationLogContext(context.Background(), req, "INSTALL")
	w := installationOutput(ctx)
	for _, part := range []string{"password=unk", "nown-secret value=known-", "private-value\n", "Bearer split-", "token\n", "line-one-pri", "vate\nline-two-private\n"} {
		if _, err := io.WriteString(w, part); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"unknown-secret", "known-private-value", "split-token", "line-one-private", "line-two-private"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("stream exposed %s", secret)
		}
	}
	// No suffix of a truncated token line may be emitted as an ordinary line.
	_, _ = io.WriteString(w, "token="+strings.Repeat("x", installationLogLineLimit)+"private-tail")
	_, _ = io.WriteString(w, "more-private-tail\nnext safe line\nlast partial")
	closeLog()
	raw, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private-tail")) || !bytes.Contains(raw, []byte("output line omitted")) || !bytes.Contains(raw, []byte("next safe line")) || !bytes.Contains(raw, []byte("last partial")) {
		t.Fatalf("unsafe or lost output: %s", raw)
	}
}
func TestInstallationStreamingTailIsAtomicAndBounded(t *testing.T) {
	x, req, file := installationLogFixture(t)
	x.appendServiceLog(req, "INSTALL", "initial")
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readerErrors := make(chan error, 1)
	readers.Add(1)
	go func() {
		defer readers.Done()
		if runtime.GOOS != "linux" {
			return
		}
		for {
			select {
			case <-stop:
				return
			default:
				raw, err := os.ReadFile(file)
				if err != nil || len(raw) > installationLogLimit || !utf8.Valid(raw) {
					select {
					case readerErrors <- fmt.Errorf("invalid concurrent tail size=%d err=%v", len(raw), err):
					default:
					}
					return
				}
			}
		}
	}()
	for i := 0; i < 80; i++ {
		x.appendServiceLog(req, "INSTALL", strings.Repeat("安装进度", 1000)+fmt.Sprintf(" end-%03d", i))
	}
	close(stop)
	readers.Wait()
	select {
	case err := <-readerErrors:
		t.Fatal(err)
	default:
	}
	raw, err := os.ReadFile(file)
	if err != nil || len(raw) > installationLogLimit || !bytes.Contains(raw, []byte("end-079")) || bytes.Contains(raw, []byte("end-000")) {
		t.Fatalf("tail failed to roll size=%d err=%v", len(raw), err)
	}
}
func TestInstallationLoggedRunnerKeepsOutputAndError(t *testing.T) {
	x, req, file := installationLogFixture(t)
	expected := errors.New("fixed command failed")
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
		return []byte("password=private failure detail\n"), expected
	}
	out, err := x.loggedSoftwareCommand(context.Background(), req, "INSTALL", "/usr/bin/apt-get", "update")
	if !errors.Is(err, expected) || string(out) != "password=private failure detail\n" {
		t.Fatal("runner result changed", string(out), err)
	}
	raw, _ := os.ReadFile(file)
	if bytes.Contains(raw, []byte("private")) || !bytes.Contains(raw, []byte("failure detail")) {
		t.Fatal(string(raw))
	}
}

func TestInstallationLogProcessHelper(t *testing.T) {
	if os.Getenv("SFOPS_INSTALL_LOG_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "install started")
	fmt.Fprintln(os.Stderr, "dependency done")
	_, _ = io.ReadFull(os.Stdin, make([]byte, 1))
	fmt.Fprint(os.Stdout, "last partial")
	os.Exit(7)
}
func TestInstallationCommandOutputVisibleBeforeExitAndPreservesCancellation(t *testing.T) {
	for _, cancelCommand := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelCommand), func(t *testing.T) {
			x, req, file := installationLogFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, closeLog := x.installationLogContext(ctx, req, "INSTALL")
			defer closeLog()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestInstallationLogProcessHelper$")
			cmd.Env = append(os.Environ(), "SFOPS_INSTALL_LOG_HELPER=1")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			captured := &cappedBuffer{limit: 64 << 10}
			cmd.Stdout, cmd.Stderr = commandOutputs(ctx, captured)
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			deadline := time.Now().Add(5 * time.Second)
			visible := false
			for time.Now().Before(deadline) {
				raw, _ := os.ReadFile(file)
				if bytes.Contains(raw, []byte("install started")) && bytes.Contains(raw, []byte("dependency done")) {
					visible = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !visible {
				cancel()
				<-done
				t.Fatal("running command output was not published")
			}
			select {
			case err := <-done:
				t.Fatalf("command exited before release: %v", err)
			default:
			}
			if cancelCommand {
				cancel()
			} else {
				_, _ = stdin.Write([]byte("x"))
			}
			err = <-done
			closeLog()
			if err == nil {
				t.Fatal("command failure lost")
			}
			if cancelCommand && ctx.Err() != context.Canceled {
				t.Fatal("context cancellation lost")
			}
			raw, _ := os.ReadFile(file)
			if !bytes.Contains(raw, []byte("install started")) || !bytes.Contains(captured.Bytes(), []byte("dependency done")) {
				t.Fatal("captured or streamed output lost")
			}
			if !cancelCommand && !bytes.Contains(raw, []byte("last partial")) {
				t.Fatal("final partial output lost on failed exit")
			}
		})
	}
}

func TestInstallationSeparateStreamsDoNotSplitKnownSecrets(t *testing.T) {
	x, req, file := installationLogFixture(t)
	x.secrets = map[string]string{"password": "known-private-value"}
	ctx, closeLog := x.installationLogContext(context.Background(), req, "INSTALL")
	defer closeLog()
	capture := &cappedBuffer{limit: 64 << 10}
	stdout, stderr := commandOutputs(ctx, capture)
	_, _ = io.WriteString(stdout, "value=known-")
	_, _ = io.WriteString(stderr, "warning\n")
	_, _ = io.WriteString(stdout, "private-value\n")
	closeLog()
	raw, _ := os.ReadFile(file)
	if bytes.Contains(raw, []byte("known-")) || bytes.Contains(raw, []byte("private-value")) || !bytes.Contains(raw, []byte("warning")) || !bytes.Contains(raw, []byte("[REDACTED]")) {
		t.Fatalf("interleaved streams leaked secret: %s", raw)
	}
}
