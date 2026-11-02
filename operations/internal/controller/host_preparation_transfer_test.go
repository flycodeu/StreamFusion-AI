package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

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
	interrupted := make(chan struct{})
	w := newPreparationUploadWatchdog(25*time.Millisecond, func() { close(interrupted) })
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("silent upload was not interrupted")
	}
	if !w.stop() {
		t.Fatal("missing stalled classification")
	}
}

func TestPreparationUploadWatchdogAllowsProgressAndStopsBeforeInstallation(t *testing.T) {
	interrupted := make(chan struct{})
	w := newPreparationUploadWatchdog(150*time.Millisecond, func() { close(interrupted) })
	for i := 0; i < 12; i++ {
		select {
		case <-interrupted:
			t.Fatal("progressing slow stream was interrupted")
		case <-time.After(25 * time.Millisecond):
			w.touch()
		}
	}
	if w.stop() {
		t.Fatal("progressing stream marked stalled")
	}
	select {
	case <-interrupted:
		t.Fatal("upload timer leaked into installation")
	case <-time.After(200 * time.Millisecond):
	}
}
