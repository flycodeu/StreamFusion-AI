//go:build linux

package deploy

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"
)

func TestInstallationNativeBuildStreamsWithUnprivilegedAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to launch isolated non-root build")
	}
	account, err := user.Lookup("nobody")
	if err != nil || account.Uid == "0" {
		t.Skip("non-root nobody account unavailable")
	}
	directory, err := os.MkdirTemp("/var/tmp", "sfops-install-log-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	if err = os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	x, req, file := installationLogFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, closeLog := x.installationLogContext(ctx, req, "NATIVE_BUILD")
	defer closeLog()
	output, err := runNativeBuild(ctx, "nobody", directory, "/bin/sh", "-c", "printf 'native-start\\n'; id -u; printf 'native-error\\n' >&2; exit 7")
	closeLog()
	if err == nil || !strings.Contains(string(output), "\n"+account.Uid+"\n") {
		t.Fatalf("expected failed unprivileged command, got %q %v", output, err)
	}
	raw, readErr := os.ReadFile(file)
	if readErr != nil || !bytes.Contains(raw, []byte("native-start")) || !bytes.Contains(raw, []byte("native-error")) {
		t.Fatalf("native output missing %q %v", raw, readErr)
	}
}
