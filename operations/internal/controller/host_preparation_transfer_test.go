package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestPreparationTimeoutKeepsSlowUploadBounded(t *testing.T) {
	if preparationUploadTimeout != 2*time.Hour || preparationUploadIdleTimeout != 15*time.Minute || preparationResponseTimeout != 15*time.Minute+30*time.Second {
		t.Fatal("unexpected transfer limits or standalone maintenance allowance")
	}
	for _, tc := range []struct {
		name     string
		snapshot preparationSnapshot
		want     time.Duration
	}{
		{"install", preparationSnapshot{LauncherSHA: "launcher"}, 148 * time.Minute},
		{"update", preparationSnapshot{LauncherSHA: "launcher", UpdateFromSHA: "old"}, 148 * time.Minute},
		{"legacy repair", preparationSnapshot{}, 135 * time.Minute},
		{"authorize only", preparationSnapshot{AuthorizeOnly: true}, 15 * time.Minute},
		{"terminal only", preparationSnapshot{TerminalTools: true}, 15 * time.Minute},
	} {
		if got := preparationBudget(tc.snapshot); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestPreparationTransferReaderBoundsWritesAndKeepsFinalCount(t *testing.T) {
	var snapshots []preparationTransfer
	p := newPreparationTransferReader(bytes.NewReader(make([]byte, 2048)), 2048, func(v preparationTransfer) { snapshots = append(snapshots, v) })
	buf := make([]byte, 1)
	for i := 0; i < 1024; i++ {
		if _, err := p.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	if len(snapshots) != 1 || snapshots[0].SentBytes != 0 {
		t.Fatal("unbounded per-byte database updates", len(snapshots))
	}
	p.last = time.Now().Add(-2 * time.Second)
	if _, err := p.Read(buf); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || snapshots[1].SentBytes != 1025 {
		t.Fatal("missing periodic progress", snapshots)
	}
	if _, err := io.Copy(io.Discard, p); err != nil {
		t.Fatal(err)
	}
	p.publish(true)
	last := snapshots[len(snapshots)-1]
	if last.SentBytes != 2048 || last.TotalBytes != 2048 || last.StartedAt != snapshots[0].StartedAt || last.UpdatedAt == "" {
		t.Fatal("invalid final progress", last)
	}
}

func TestPreparationUploadFailureSeparatesTimeoutFromPermissions(t *testing.T) {
	deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	canceled, stop := context.WithCancel(context.Background())
	stop()
	for _, tc := range []struct {
		name         string
		ctx          context.Context
		err          error
		stderr, code string
	}{
		{"deadline", deadline, errors.New("closed"), "", "PREPARATION_UPLOAD_TIMEOUT"},
		{"canceled", canceled, errors.New("closed"), "", "PREPARATION_UPLOAD_INTERRUPTED"},
		{"network", context.Background(), &net.OpError{Op: "write", Err: io.ErrUnexpectedEOF}, "", "PREPARATION_UPLOAD_INTERRUPTED"},
		{"missing-exit", context.Background(), &ssh.ExitMissingError{}, "", "PREPARATION_UPLOAD_INTERRUPTED"},
		{"space", context.Background(), fmt.Errorf("wrapped: %w", &ssh.ExitError{}), "cat: No space left on device secret-value", "PREPARATION_UPLOAD_NO_SPACE"},
		{"permission", context.Background(), &ssh.ExitError{}, "Permission denied secret-value", "PREPARATION_UPLOAD_DENIED"},
		{"untrusted-message", context.Background(), errors.New("other"), "Permission denied secret-value", "PREPARATION_UPLOAD_FAILED"},
		{"unknown", context.Background(), &ssh.ExitError{}, "unrecognized secret-value", "PREPARATION_UPLOAD_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := preparationUploadFailure(tc.ctx, tc.err, tc.stderr); got != tc.code {
				t.Fatalf("got %s, want %s", got, tc.code)
			}
		})
	}
}

func TestPreparationUploadWatchdogStopsSilentStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		interrupted := make(chan struct{})
		w := newPreparationUploadWatchdog(preparationUploadIdleTimeout, preparationUploadTimeout, func() { close(interrupted) })
		time.Sleep(14 * time.Minute)
		select {
		case <-interrupted:
			t.Fatal("silent transfer was interrupted before its grace period")
		default:
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		select {
		case <-interrupted:
		default:
			t.Fatal("silent upload was not interrupted")
		}
		if got := w.stop(); got != "PREPARATION_UPLOAD_STALLED" {
			t.Fatalf("silent transfer returned %q", got)
		}
	})
}

func TestPreparationUploadWatchdogAllowsProgressAndStopsBeforeInstallation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		interrupted := make(chan struct{})
		w := newPreparationUploadWatchdog(preparationUploadIdleTimeout, preparationUploadTimeout, func() { close(interrupted) })
		// Replay the 7.6 MiB archive at about 4.3 KiB/s, which exceeds the old
		// 28 minute total budget. Allow the final SSH exit status to arrive late.
		const size = 8015640
		p := newPreparationTransferReader(bytes.NewReader(make([]byte, size)), size, func(preparationTransfer) {})
		p.activity = w.touch
		buf := make([]byte, 4400)
		for {
			_, err := p.Read(buf)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Second)
		}
		time.Sleep(10 * time.Minute)
		if got := w.stop(); got != "" || p.value.SentBytes != size {
			t.Fatalf("late SSH receipt: code %q, sent %d", got, p.value.SentBytes)
		}
		time.Sleep(preparationUploadTimeout)
		select {
		case <-interrupted:
			t.Fatal("upload deadline interrupted installation")
		default:
		}
	})
}

func TestPreparationUploadWatchdogCapsProgressAtTwoHours(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		interrupted := make(chan struct{})
		w := newPreparationUploadWatchdog(preparationUploadIdleTimeout, preparationUploadTimeout, func() { close(interrupted) })
		for elapsed := time.Minute; elapsed < preparationUploadTimeout; elapsed += time.Minute {
			time.Sleep(time.Minute)
			w.touch()
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		select {
		case <-interrupted:
		default:
			t.Fatal("absolute deadline did not interrupt the active upload")
		}
		if got := w.stop(); got != "PREPARATION_UPLOAD_TIMEOUT" {
			t.Fatalf("active transfer at hard deadline returned %q", got)
		}
	})
}
