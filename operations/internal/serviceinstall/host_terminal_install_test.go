package serviceinstall

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Exercise the APT portion of the actual downloadable installer with package
// candidates and archives supplied independently, as they are on Ubuntu.
func TestHostTerminalClientInstallation(t *testing.T) {
	f := newTerminalFixture(t)
	installer := HostTerminalInstaller()
	start := strings.Index(installer, "\nclients=(mysql ")
	if start < 0 || strings.Contains(installer, "@TOOL_IMAGES@") || strings.Contains(installer, "@LAUNCHER@") {
		t.Fatal("downloaded installer is incomplete")
	}
	clientSteps := strings.NewReplacer("/usr/bin/${clients[i]}", "./clients/${clients[i]}",
		"/usr/bin/apt-get", "test_apt", "/usr/bin/dpkg-query", "test_query",
		"/usr/bin/dpkg-deb", "test_archive", "/usr/bin/dpkg", "test_dpkg").Replace(installer[start:])
	for _, tc := range []struct {
		mode  string
		ready bool
		stage string
	}{
		{"fresh", true, "APT_INSTALL"}, {"already-ready", true, "VERIFY"},
		{"broken-dpkg", false, "DPKG"}, {"unexpected-server", false, "APT_PLAN"},
		{"removal", false, "APT_PLAN"}, {"changed-version", false, "APT_INSTALL"},
		{"partial", false, "VERIFY"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cmd := exec.Command(f.bash, "--noprofile", "--norc", "-s")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "FIXTURE_MODE="+tc.mode)
			cmd.Stdin = strings.NewReader(terminalPackageFixture + clientSteps)
			out, err := cmd.CombinedOutput()
			s := string(out)
			if (err == nil) != tc.ready || strings.Contains(s, "SFOPS_TERMINAL_READY") != tc.ready || !strings.Contains(s, "stage="+tc.stage) {
				t.Fatalf("unexpected result %v: %s", err, s)
			}
			if tc.mode == "already-ready" && strings.Contains(s, "stage=APT_") {
				t.Fatalf("already prepared host contacted APT: %s", s)
			}
		})
	}
}

const terminalPackageFixture = `
set -euo pipefail
stage() { echo "stage=$1"; }
fail() { echo "$*"; exit 1; }
make_clients() {
    mkdir -p clients
    for cmd in mysql redis-cli psql curl; do
        printf '#!/bin/sh\nexit 0\n' > "clients/$cmd"
        chmod +x "clients/$cmd"
    done
}
if [[ $FIXTURE_MODE == already-ready ]]; then make_clients; fi
test_dpkg() { if [[ $FIXTURE_MODE == broken-dpkg ]]; then echo incomplete; fi; }
test_query() { if [[ $FIXTURE_MODE == partial ]]; then echo unpacked; else echo installed; fi; }
test_archive() { if [[ $FIXTURE_MODE == changed-version ]]; then echo perl-base=2.1; else echo perl-base=2.0; fi; }
test_apt() {
    local arg hook=
    for arg in "$@"; do
        case $arg in
            update) return 0 ;;
            --simulate)
                printf '%s\n' 'Inst perl-base [1.0] (2.0 Ubuntu [amd64])' 'Inst readline-common (8.2 Ubuntu [all])' 'Inst postgresql-client-16 (16 Ubuntu [amd64])'
                case $FIXTURE_MODE in
                    unexpected-server) echo 'Inst postgresql-16 (16 Ubuntu [amd64])' ;;
                    removal) echo 'Remv redis-server [7]' ;;
                esac
                return 0 ;;
            DPkg::Pre-Install-Pkgs::=*) hook="${arg#DPkg::Pre-Install-Pkgs::=}" ;;
        esac
    done
    [[ -n $hook ]]
    (eval "$hook") <<< fixture.deb || return 1
    make_clients
}
`
