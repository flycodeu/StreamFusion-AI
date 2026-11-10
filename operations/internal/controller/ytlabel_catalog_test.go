package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestYtlabelNativeDefinitionsUseFixedSourceAndFitSignedProtocol(t *testing.T) {
	bash := additionalTestBash(t)
	_, private, _ := ed25519.GenerateKey(nil)
	definitions := ytlabelDefinitions()
	if len(definitions) != 2 {
		t.Fatalf("expected native online and offline definitions, got %d", len(definitions))
	}
	for _, definition := range definitions {
		if err := definition.ValidateCommands(); err != nil {
			t.Fatal(definition.Key, err)
		}
		if definition.RuntimeModes[0] != "NATIVE" || definition.DefaultBindAddress != "0.0.0.0" || definition.Port != 9924 || definition.Web == nil || !definition.Web.Embed || definition.Web.URLParameter != "PUBLIC_URL" {
			t.Fatalf("wrong annotation defaults: %+v", definition)
		}
		for _, step := range definition.Commands.Steps {
			if strings.Contains(step.Script, "\r\n") {
				t.Fatalf("%s %s contains Windows line endings", definition.Key, step.Name)
			}
			command := exec.Command(bash, "-n")
			command.Stdin = strings.NewReader(step.Script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s %s: %v %s", definition.Key, step.Name, err, output)
			}
		}
		config := serviceinstall.Config{Name: definition.Name, RuntimeMode: "NATIVE", BindAddress: "127.0.0.1", HostPort: definition.Port, MemoryMiB: definition.DefaultMemoryMiB}
		values, err := serviceinstall.ResolveParameters(definition.Parameters, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		config.Parameters = values
		source := serviceinstall.Source{Type: definition.SourceModes[0]}
		if source.Type == "ARCHIVE" {
			source.SHA256, source.ByteSize = strings.Repeat("a", 64), 1024
		}
		installation := &serviceinstall.Request{SoftwareID: uuid.NewString(), BindingRevision: 1, Definition: definition, Config: config, Source: source}
		if err := installation.Validate(); err != nil {
			t.Fatal(err)
		}
		request := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString(), JobID: uuid.NewString(), Action: "SOFTWARE_INSTALL", SoftwareOperation: "SOFTWARE_INSTALL", DeadlineAt: "2026-11-10T00:00:00Z", Installation: installation}
		request.InputHash = deploy.CommandRequestDigest(request)
		if err := deploy.SignCommandRequest(&request, private); err != nil {
			t.Fatal(err)
		}
		wire, _ := json.Marshal(request)
		raw, _ := json.Marshal(definition)
		if len(raw) > 48<<10 || len(wire)+1 > 64<<10 {
			t.Fatalf("definition=%d request=%d", len(raw), len(wire))
		}
		if !strings.Contains(definition.Commands.Steps[1].Script, ytlabelSourceSHA256) || !strings.Contains(definition.Commands.Steps[1].Script, ytlabelCommit) {
			t.Fatal("source identity missing from visible installation script")
		}
		t.Logf("%s definition=%d signed request=%d", definition.Key, len(raw), len(wire)+1)
	}
}

func TestYtlabelPreparedScriptExecutesSharedHeader(t *testing.T) {
	script := ytlabelCommand("prepare")
	boundary := strings.Index(script, "for command in systemctl")
	if boundary < 0 {
		t.Fatal("ytlabel dependency check boundary missing")
	}
	command := exec.Command(additionalTestBash(t), "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", script[:boundary]+"printf '%s\\n' \"$app:$version\"")
	command.Env = append(os.Environ(), "OPS_INSTALL_ROOT=/tmp/streamfusion-ops-script-test", "OPS_SOFTWARE_ID=11111111-1111-4111-8111-111111111111")
	if out, err := command.CombinedOutput(); err != nil || string(out) != "ytlabel:4.0\n" {
		t.Fatalf("composed ytlabel header failed before dependency checks: %v: %s", err, out)
	}
}

func ytlabelTestPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if executable, err := exec.LookPath(name); err == nil {
			if exec.Command(executable, "-I", "-c", "import sys; assert sys.version_info.major==3").Run() == nil {
				return executable
			}
		}
	}
	t.Skip("Python 3 required for isolated configuration/archive checks")
	return ""
}

func TestYtlabelLauncherAppliesUploadLimitAndPreservesSocketIOWithoutBrowser(t *testing.T) {
	python := ytlabelTestPython(t)
	root := t.TempDir()
	for _, dir := range []string{"bin", "config", "data", "runtime/source"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/ytlabel-launch.py", ytlabelSource("launch.py"))
	write("config/ytlabel.json", `{"host":"127.0.0.1","port":19924,"maxUploadMiB":73}`)
	write("runtime/source/app.py", `import json,os
from pathlib import Path
class App:
    config={'MAX_CONTENT_LENGTH':500*1024*1024}
app=App()
class Socket:
    def run(self,app,**options):
        Path('launch-result.json').write_text(json.dumps({'limit':app.config['MAX_CONTENT_LENGTH'],'options':options,'cwd':os.getcwd()}))
socketio=Socket()
if __name__=='__main__':
    raise AssertionError('must not run upstream browser entry point')
`)
	command := exec.Command(python, "-I", filepath.Join(root, "bin", "ytlabel-launch.py"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	data, err := os.ReadFile(filepath.Join(root, "data", "launch-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Limit   int            `json:"limit"`
		Options map[string]any `json:"options"`
		CWD     string         `json:"cwd"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	actualDirectory, actualErr := os.Stat(result.CWD)
	expectedDirectory, expectedErr := os.Stat(filepath.Join(root, "data"))
	if result.Limit != 73*1024*1024 || result.Options["host"] != "127.0.0.1" || result.Options["port"] != float64(19924) || result.Options["debug"] != false || result.Options["use_reloader"] != false || result.Options["allow_unsafe_werkzeug"] != true || actualErr != nil || expectedErr != nil || !os.SameFile(actualDirectory, expectedDirectory) {
		t.Fatalf("wrong actual launcher configuration: %s", data)
	}
}

func TestYtlabelUnitImportsExistingImagesAndUninstallProtectsMounts(t *testing.T) {
	bash, python := additionalTestBash(t), ytlabelTestPython(t)
	root, external := t.TempDir(), t.TempDir()
	for _, dir := range []string{"bin", "config", "data", "logs", "work", "runtime", "test-units"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	posix := func(path string) string {
		t.Helper()
		data, err := exec.Command(bash, "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(path)).CombinedOutput()
		if err != nil {
			t.Fatal(err, string(data))
		}
		return strings.TrimSpace(string(data))
	}
	posixRoot, posixExternal := posix(root), posix(external)
	id := uuid.NewString()
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "config", "ops-owner"), "ytlabel:"+id)
	write(filepath.Join(root, "config", "python-binary"), filepath.ToSlash(python)+"\n")
	write(filepath.Join(root, "runtime", "sentinel"), "runtime")
	write(filepath.Join(external, "image.png"), "keep external bytes")
	prefix := `[[ "$OPS_INSTALL_ROOT" == "$TEST_ROOT" ]] || exit 77
id() { printf 'fixture\n'; }
chown() { :; }
systemctl() { printf '%s\n' "$*" >> "$OPS_INSTALL_ROOT/systemctl-calls"; }
install() {
 while [[ $# -gt 0 ]]; do
  case "$1" in -d) shift;; -m|-o|-g) shift 2;; *) mkdir -p -- "$1"; shift;; esac
 done
}
`
	environment := []string{"OPS_INSTALL_ROOT=" + posixRoot, "TEST_ROOT=" + posixRoot, "OPS_SOFTWARE_ID=" + id, "OPS_JOB_ID=" + uuid.NewString(), "OPS_BIND_ADDRESS=127.0.0.1", "OPS_HOST_PORT=19924", "OPS_MEMORY_MIB=2048", "OPS_PARAM_MAX_UPLOAD_MIB=73", "OPS_PARAM_SOURCE_DIRECTORY=" + posixExternal}
	prelude := ytlabelManagementDefinition().ScriptPrelude
	if runtime.GOOS == "windows" {
		// The Windows fixture checks copying, not Linux UID enforcement.
		prelude = strings.ReplaceAll(prelude, "os.chown(", "skip_chown(")
		prelude = strings.Replace(prelude, "import hashlib, ipaddress", "def skip_chown(*args): pass\nimport hashlib, ipaddress", 1)
	}
	run := func(script string) ([]byte, error) {
		command := exec.Command(bash, "--noprofile", "--norc")
		command.Env = append(os.Environ(), environment...)
		command.Stdin = strings.NewReader(prefix + prelude + "\n" + script)
		return command.CombinedOutput()
	}
	configure := strings.ReplaceAll(ytlabelCommand("configure"), `unit_path="/etc/systemd/system/$unit"`, `unit_path="$root/test-units/$unit"`)
	if output, err := run(configure); err != nil {
		t.Fatal(err, string(output))
	}
	unitPath := filepath.Join(root, "test-units", "ops-app-"+id+".service")
	unitBytes, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(unitBytes)
	for _, required := range []string{"User=ops", "ProtectSystem=strict", "ProtectHome=true", "PrivateTmp=true", "ReadWritePaths=" + posixRoot + "/data " + posixRoot + "/logs"} {
		if !strings.Contains(unit, required) {
			t.Fatalf("unit lacks %s: %s", required, unit)
		}
	}
	if strings.Contains(unit, "BindReadOnlyPaths=") {
		t.Fatal("source mount made the upload directory read-only")
	}
	if data, err := os.ReadFile(filepath.Join(root, "data/uploads/samples/image.png")); err != nil || string(data) != "keep external bytes" {
		t.Fatal("source image was not imported", string(data), err)
	}
	configuration, _ := os.ReadFile(filepath.Join(root, "config", "ytlabel.json"))
	if !strings.Contains(string(configuration), `"maxUploadMiB": 73`) || !strings.Contains(string(configuration), `"port": 19924`) {
		t.Fatal(string(configuration))
	}
	uninstall := strings.ReplaceAll(ytlabelCommand("uninstall"), `path="/etc/systemd/system/$unit"`, `path="$root/test-units/$unit"`)
	if output, err := run("findmnt() { printf '%s\\n' \"$OPS_INSTALL_ROOT/runtime/nested-mount\"; }\n" + uninstall); err == nil || !strings.Contains(string(output), "挂载点") {
		t.Fatalf("nested runtime mount not blocked: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "sentinel")); err != nil {
		t.Fatal("runtime deleted before mount check", err)
	}
	if output, err := run("findmnt() { :; }\n" + uninstall); err != nil {
		t.Fatal(err, string(output))
	}
	if _, err := os.Stat(filepath.Join(root, "runtime")); !os.IsNotExist(err) {
		t.Fatal("runtime not removed")
	}
	for _, path := range []string{filepath.Join(root, "config", "ytlabel.json"), filepath.Join(root, "data", "uploads", "samples"), filepath.Join(external, "image.png")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("uninstall removed retained data or external source", path, err)
		}
	}
}

func TestYtlabelOfflineBundleRejectsIncompleteManifestLinksAndWrongSourceLayout(t *testing.T) {
	python := ytlabelTestPython(t)
	for _, scenario := range []string{"valid", "duplicate-manifest", "symlink", "traversal", "valid-source-root"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			script := strings.ReplaceAll(ytlabelSource("extract.py"), "@@COMMIT@@", ytlabelCommit)
			if err := os.WriteFile(filepath.Join(root, "extract.py"), []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			gzipWriter := gzip.NewWriter(&archive)
			tarWriter := tar.NewWriter(gzipWriter)
			add := func(name, content string, kind byte) {
				t.Helper()
				header := &tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), Typeflag: kind}
				if kind == tar.TypeSymlink {
					header.Size, header.Linkname = 0, "/etc"
				}
				if err := tarWriter.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if header.Size > 0 {
					if _, err := tarWriter.Write([]byte(content)); err != nil {
						t.Fatal(err)
					}
				}
			}
			mode := "offline"
			if scenario == "valid-source-root" {
				mode = "source"
				add("ytlabel-"+ytlabelCommit+"/", "", tar.TypeDir)
				add("ytlabel-"+ytlabelCommit+"/app.py", "# upstream fixture", tar.TypeReg)
			} else {
				add("source.tar.gz", "source-fixture", tar.TypeReg)
				add("wheelhouse/dependency-1-py3-none-any.whl", "wheel-fixture", tar.TypeReg)
				manifest := fmt.Sprintf("%x  source.tar.gz\n%x  wheelhouse/dependency-1-py3-none-any.whl\n", sha256.Sum256([]byte("source-fixture")), sha256.Sum256([]byte("wheel-fixture")))
				if scenario == "duplicate-manifest" {
					manifest = strings.Split(manifest, "\n")[0] + "\n" + strings.Split(manifest, "\n")[0] + "\n"
				}
				add("SHA256SUMS", manifest, tar.TypeReg)
				if scenario == "symlink" {
					add("wheelhouse/danger.whl", "", tar.TypeSymlink)
				}
				if scenario == "traversal" {
					add("../outside", "never write", tar.TypeReg)
				}
			}
			if err := tarWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gzipWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "bundle.tar.gz"), archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(python, "-I", filepath.Join(root, "extract.py"), filepath.Join(root, "bundle.tar.gz"), filepath.Join(root, "output"), mode)
			output, err := command.CombinedOutput()
			valid := scenario == "valid" || scenario == "valid-source-root"
			if valid != (err == nil) {
				t.Fatalf("%s: err=%v output=%s", scenario, err, output)
			}
			if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
				t.Fatal("archive escaped extraction directory")
			}
		})
	}
}

func TestYtlabelOfflinePipUsesReadableRuntimeAndRejectsHiddenInterpreters(t *testing.T) {
	bash := additionalTestBash(t)
	root := t.TempDir()
	for _, directory := range []string{"config", "work", "data", "logs"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := exec.Command(bash, "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(root)).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	posix := strings.TrimSpace(string(data))
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("config/ops-owner", "ytlabel:fixture-id")
	write("config/python-binary", posix+"/fake-python")
	// Archive integrity has separate real-Python tests. This fake interpreter
	// exposes the paths used by low-privilege pip without installing packages.
	write("fake-python", `#!/bin/bash
set -euo pipefail
if [[ "$2" == *ytlabel-extract.py ]]; then
 if [[ "$5" == offline ]]; then
  mkdir -p "$4/wheelhouse"; printf wheel > "$4/wheelhouse/dependency.whl"; printf source > "$4/source.tar.gz"
 else mkdir -p "$4"; printf dependency > "$4/requirements.txt"; fi
elif [[ "$*" == *'-m venv'* ]]; then
 destination=${!#}; mkdir -p "$destination/bin"; cp "$OPS_INSTALL_ROOT/fake-python" "$destination/bin/python"; chmod 755 "$destination/bin/python"
elif [[ "$*" == *'-m pip'* ]]; then
 for arg in "$@"; do [[ "$arg" != "$OPS_INSTALL_ROOT/work"* ]] || { echo private-work-in-child >&2; exit 92; }; done
 if [[ "$*" == *--find-links* ]]; then [[ -f "$OPS_INSTALL_ROOT/runtime/wheelhouse/dependency.whl" ]]; fi
 printf '%s\n' "$*" >> "$OPS_INSTALL_ROOT/child-arguments"
fi
`)
	prefix := `id() { printf 'fixture\n'; }
chown() { :; }
sha256sum() { cat >/dev/null; }
runuser() { shift 3; "$@"; }
install() { while [[ $# -gt 0 ]]; do case "$1" in -d) shift;; -m|-o|-g) shift 2;; *) mkdir -p -- "$1"; shift;; esac; done; }
`
	command := exec.Command(bash, "--noprofile", "--norc")
	command.Env = append(os.Environ(), "OPS_INSTALL_ROOT="+posix, "OPS_SOFTWARE_ID=fixture-id", "OPS_SOURCE=ARCHIVE", "OPS_ARTIFACT_PATH="+posix+"/artifact")
	command.Stdin = strings.NewReader(prefix + ytlabelCommand("material"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	arguments, err := os.ReadFile(filepath.Join(root, "child-arguments"))
	if err != nil || !strings.Contains(string(arguments), "--no-index --find-links "+posix+"/runtime/wheelhouse") || strings.Contains(string(arguments), posix+"/work") {
		t.Fatalf("low-privilege pip traverses wrong material path: %v %s", err, arguments)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, "work"))
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("private executor work permissions changed", err)
		}
	}
	for _, interpreter := range []string{"/home/user/python", "/root/python", "/run/user/1000/python"} {
		prepared := strings.ReplaceAll(ytlabelCommand("prepare"), `[[ ! -e "$root/config/ops-owner" ]]`, `[[ true ]]`)
		command := exec.Command(bash, "--noprofile", "--norc")
		command.Env = append(os.Environ(), "OPS_INSTALL_ROOT="+posix, "OPS_SOFTWARE_ID=fixture-id", "OPS_PARAM_PYTHON_BINARY="+interpreter, "TEST_INTERPRETER="+interpreter)
		command.Stdin = strings.NewReader("systemctl() { printf 'systemd 249\\n'; }\nreadlink() { printf '%s\\n' \"$TEST_INTERPRETER\"; }\nuseradd() { :; }\nrunuser() { :; }\nfindmnt() { :; }\n" + prepared)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "系统或 Ops 运行目录") {
			t.Fatalf("hidden interpreter accepted: %s %v %s", interpreter, err, output)
		}
	}
}
