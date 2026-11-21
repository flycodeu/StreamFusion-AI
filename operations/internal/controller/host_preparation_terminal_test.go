package controller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestBundledTerminalStageUsesTargetOSAndPreservesInstallerExit(t *testing.T) {
	bash := additionalTestBash(t)
	const fixture = `printf 'SFOPS_TERMINAL_STAGE=APT_UPDATE\n' >&2
echo "private-apt-diagnostic" >&2
case "$MOCK_APT" in
	ok) echo SFOPS_TERMINAL_READY ;;
	fail) exit 100 ;;
	interrupted) exit 143 ;;
	fake-ready) echo SFOPS_TERMINAL_READY; exit 100 ;;
	*) exit 1 ;;
esac`
	for _, tc := range []struct {
		name, distro, osVersion, mode, want string
		success                             bool
	}{
		{"new-host", "ubuntu", "24.04", "ok", "宿主机终端工具已就绪", true},
		{"unavailable-apt", "ubuntu", "24.04", "fail", "SFOPS_ERROR=TERMINAL_TOOLS_APT_UPDATE", false},
		{"false-ready", "ubuntu", "24.04", "fake-ready", "SFOPS_ERROR=TERMINAL_TOOLS_VERIFY", false},
		{"stopped-shell", "ubuntu", "24.04", "interrupted", "终端工具安装中断", false},
		{"other-version", "ubuntu", "22.04", "fail", "继续安装接入组件", true},
		{"other-os", "debian", "12", "fail", "继续安装接入组件", true},
		{"missing-os", "", "", "fail", "SFOPS_ERROR=TERMINAL_TOOLS_PLATFORM", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "os-release"), []byte("ID="+tc.distro+"\nVERSION_ID="+tc.osVersion+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			script := strings.NewReplacer("/etc/os-release", "./os-release", "@INSTALLER@", shellQuote(fixture)).Replace(controllerScript("preparation/bundled-terminal.sh.tmpl"))
			cmd := exec.Command(bash, "-c", "set -Eeuo pipefail\n"+script)
			cmd.Dir, cmd.Env = dir, append(os.Environ(), "MOCK_APT="+tc.mode)
			out, err := cmd.CombinedOutput()
			if tc.success != (err == nil) || !strings.Contains(string(out), tc.want) || strings.Contains(string(out), "private-apt-diagnostic") {
				t.Fatalf("result %s: %s (%v)", tc.name, out, err)
			}
		})
	}
}

func TestHostPreparationArchiveKeepsLegacyRepairScope(t *testing.T) {
	p := hostPreparation{Package: targetPreparationPackage{SHA256: strings.Repeat("a", 64)}}
	if strings.Contains(targetInstaller(p, "exit 0"), "SFOPS_TERMINAL_STAGE=") {
		t.Fatal("legacy snapshot repair must not introduce package installation")
	}
	p.IncludeHostTerminal = true
	full := targetInstaller(p, "exit 0")
	installer := shellQuote(serviceinstall.HostTerminalInstaller())
	if !strings.Contains(full, installer) || !strings.Contains(full, `bash "$helper" "${args[@]}" --authorize-only
  prepare_host_terminal`) || !strings.Contains(full, `prepare_host_terminal
  bash "$helper" "${args[@]}" --apply`) {
		t.Fatal("fresh and existing target paths must prepare tools before applying changes")
	}
	p.UpdateFromSHA = strings.Repeat("b", 64)
	update := targetInstaller(p, "exit 0")
	if !strings.Contains(update, installer) || strings.Index(update, installer) > strings.Index(update, "target-upgrade") {
		t.Fatal("update must preserve the previous executor if terminal setup fails")
	}
}
