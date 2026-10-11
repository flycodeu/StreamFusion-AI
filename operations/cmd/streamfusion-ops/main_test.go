package main

import (
	"github.com/streamfusion-ai/operations/internal/controller"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupGuards(t *testing.T) {
	if err := run([]string{"serve", "--listen", "192.0.2.1:19090", "--state", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "TLS_REQUIRED") {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := run([]string{"check-state", "--state", dir}); err == nil || !strings.Contains(err.Error(), "ADMIN_REQUIRED") {
		t.Fatal(err)
	}
	s, err := controller.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InitAdmin("admin", "test-password-123"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = run([]string{"serve", "--listen", listener.Addr().String(), "--state", dir}); err == nil || !strings.Contains(err.Error(), "LISTEN_FAILED") {
		t.Fatal("occupied port accepted", err)
	}
	if err = run([]string{"export-key", "--state", dir, "--out", filepath.Join(dir, "master.key")}); err == nil {
		t.Fatal("key overwrote existing file")
	}
	file := filepath.Join(t.TempDir(), "password")
	os.WriteFile(file, []byte("weak"), 0600)
	if err = run([]string{"init-admin", "--state", t.TempDir(), "--password-file", file}); err == nil {
		t.Fatal("weak password accepted")
	}
}
