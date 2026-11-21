package serviceinstall

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostTerminalProbeUsesPinnedLauncherAndAllFourClients(t *testing.T) {
	f := newTerminalFixture(t)
	before, rest, ok := strings.Cut(HostTerminalInstaller(), "cat > \"$temporary\" <<'SFOPS_HOST_TERMINAL_LAUNCHER_EOF'\n")
	launcher, _, end := strings.Cut(rest, "\nSFOPS_HOST_TERMINAL_LAUNCHER_EOF\n")
	if before == "" || !ok || !end || fmt.Sprintf("%x", sha256.Sum256([]byte(launcher+"\n"))) != HostTerminalLauncherSHA() {
		t.Fatal("probe and embedded launcher disagree on the installed byte hash")
	}
	for _, tc := range []struct{ name, osRelease, launcher, want string }{
		{"ready", "ID=ubuntu\nVERSION_ID=24.04", launcher, "READY"},
		{"manual-update", "ID=ubuntu\nVERSION_ID=24.04", launcher, "READY"},
		{"redis-missing", "ID=ubuntu\nVERSION_ID=24.04", launcher, "MISSING"},
		{"partial-dpkg", "ID=ubuntu\nVERSION_ID=24.04", launcher, "MISSING"},
		{"foreign-launcher", "ID=ubuntu\nVERSION_ID=24.04", "echo something-else", "MISSING"},
		{"not-ubuntu", "ID=debian\nVERSION_ID=12", "", "NOT_APPLICABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, path := range []string{"usr/local/bin", "usr/bin"} {
				if err := os.MkdirAll(filepath.Join(dir, path), 0755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0755); err != nil {
					t.Fatal(err)
				}
			}
			write("os-release", tc.osRelease+"\n")
			write("usr/local/bin/sf-docker", tc.launcher+"\n")
			for _, name := range []string{"mysql", "redis-cli", "psql", "curl"} {
				if tc.name != "redis-missing" || name != "redis-cli" {
					write("usr/bin/"+name, "#!/bin/sh\nexit 0\n")
				}
			}
			sha := HostTerminalLauncherSHA()
			if tc.name == "manual-update" {
				sha = strings.Repeat("9", 64)
			}
			body := strings.NewReplacer("/etc/os-release", "./os-release", "/usr/local/bin", "./usr/local/bin", "/usr/local ", "./usr/local ", "in /usr ", "in ./usr ", "/usr/bin/${clients[i]}", "./usr/bin/${clients[i]}").Replace(HostTerminalProbe(sha))
			prefix := `stat(){ echo '0 755'; }
flock(){ return 0; }
dpkg-query(){ if [[ $SCENARIO == partial-dpkg ]]; then echo unpacked; else echo installed; fi; }
timeout(){ shift; "$@"; }
`
			cmd := exec.Command(f.bash, "-c", prefix+body)
			cmd.Dir, cmd.Env = dir, append(os.Environ(), "SCENARIO="+tc.name)
			data, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(data)) != "SFOPS_TERMINAL_"+tc.want {
				t.Fatalf("unexpected probe output: %s (%v)", data, err)
			}
		})
	}
}
