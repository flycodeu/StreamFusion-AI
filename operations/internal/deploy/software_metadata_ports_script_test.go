package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestSoftwareMetadataFiltersLargeSocketSnapshotsBeforeResponseLimit(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		for _, candidate := range []string{`D:\Developer Tools\Git\bin\bash.exe`, `C:\Program Files\Git\bin\bash.exe`} {
			if _, statErr := os.Stat(candidate); statErr == nil {
				shell, err = candidate, nil
				break
			}
		}
		if err != nil {
			t.Skip("requires a POSIX shell")
		}
	}
	dir := t.TempDir()
	sockets := strings.Repeat("tcp LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:* users:((\"private-unrelated-argument\",pid=99999,fd=3))\n", 2000)
	sockets += "tcp LISTEN 0 4096 0.0.0.0:19924 0.0.0.0:* users:((\"private-selected-argument\",pid=88475,fd=3))\n"
	sockets += "udp UNCONN 0 0 127.0.0.1:5353 0.0.0.0:* users:((\"private-second-argument\",pid=88476,fd=4))\n"
	sockets += "tcp LISTEN 0 4096 0.0.0.0:29924 0.0.0.0:* users:((\"private-lookalike\",pid=188475,fd=5))\n"
	file, counter := filepath.Join(dir, "sockets"), filepath.Join(dir, "scans")
	if err := os.WriteFile(file, []byte(sockets), 0600); err != nil {
		t.Fatal(err)
	}
	prefix := `fixture_systemctl() {
 case "$*" in
  *MainPID*redis.service*) printf '88475\n';;
  *MainPID*nginx.service*) printf '88476\n';;
  *) printf '/tmp/unowned.service\n';;
 esac
}
fixture_ss() { printf 'scan\n' >> "$TEST_SCANS"; cat "$TEST_SOCKETS"; }
`
	script := strings.NewReplacer("/usr/bin/systemctl", "fixture_systemctl", "/usr/bin/timeout 2s /usr/bin/ss", "fixture_ss").Replace(softwareUnitMetadataScript)
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, shell, append([]string{"-c", prefix + script, "software-metadata"}, args[3:]...)...)
		cmd.Env = append(os.Environ(), "TEST_SOCKETS="+filepath.ToSlash(file), "TEST_SCANS="+filepath.ToSlash(counter))
		data, err := cmd.CombinedOutput()
		if strings.Contains(string(data), "private-") || len(data) > 64<<10 {
			t.Fatal("socket metadata exposed process arguments or exceeded its response bound")
		}
		return data, err
	}
	rows, err := readSoftwareUnitMetadata(context.Background(), []string{"redis.service", "nginx.service"}, run)
	if err != nil || len(rows["redis.service"].ListeningPorts) != 1 || rows["redis.service"].ListeningPorts[0] != "0.0.0.0:19924/tcp" || len(rows["nginx.service"].ListeningPorts) != 1 || rows["nginx.service"].ListeningPorts[0] != "127.0.0.1:5353/udp" {
		t.Fatal("large unrelated socket output hid the selected MainPID", rows, err)
	}
	if data, err := os.ReadFile(counter); err != nil || string(data) != "scan\n" {
		t.Fatal("metadata scanned sockets separately per unit", string(data), err)
	}
	x := executor{softwareRun: func(limit time.Duration, command string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		return run(ctx, command, args...)
	}}
	s := SoftwareRequest{Kind: "SYSTEMD", Locator: "redis.service"}
	wanted := serviceinstall.ResolvedResource{Type: "PORT", Value: "19924", BindAddress: "0.0.0.0", Protocol: "TCP"}
	if !x.softwareOwnsPort(context.Background(), s, wanted) {
		t.Fatal("management did not recognize its selected unit's port")
	}
	wanted.Value = "29924"
	if x.softwareOwnsPort(context.Background(), s, wanted) {
		t.Fatal("management borrowed a lookalike PID's port")
	}
}
