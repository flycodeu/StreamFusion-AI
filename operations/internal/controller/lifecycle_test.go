package controller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStopOnlyCurrentInvocationAndWaitsForLock(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup, err := s.LocalControl(ctx, "127.0.0.1:19090", cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	old, err := readLocalRuntime(filepath.Join(s.Dir, "controller.runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	old.Token = strings.Repeat("0", 64)
	if err = localRecord(filepath.Join(s.Dir, "controller.stop.json"), old); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("stale request stopped service")
	case <-time.After(400 * time.Millisecond):
	}
	result := make(chan error, 1)
	go func() { result <- Stop(s.Dir, 3*time.Second) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel service")
	}
	select {
	case err := <-result:
		t.Fatal("returned before lock released", err)
	case <-time.After(150 * time.Millisecond):
	}
	cleanup()
	s.Close()
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if err = Stop(s.Dir, time.Second); err != nil {
		t.Fatal("repeat stop", err)
	}
}

func TestStopDoesNotCreateStateOrKillUnidentifiedHolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if err := Stop(missing, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("stop created state")
	}
	s, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = Stop(s.Dir, time.Second); err == nil || !strings.Contains(err.Error(), "STOP_UNAVAILABLE") {
		t.Fatal("unknown holder", err)
	}
}
