package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestLabelStudioDefinitionsPinEveryRuntimeAndSource(t *testing.T) {
	bash := managementTestBash(t)
	_, private, _ := ed25519.GenerateKey(nil)
	definitions := labelStudioDefinitions()
	if len(definitions) != 4 {
		t.Fatal(len(definitions))
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		if err := definition.ValidateCommands(); err != nil {
			t.Fatal(definition.Key, err)
		}
		pair := definition.RuntimeModes[0] + "/" + definition.SourceModes[0]
		if seen[pair] || definition.Web == nil || definition.Web.Embed || definition.Web.URLParameter != "PUBLIC_URL" || definition.DefaultBindAddress != "0.0.0.0" {
			t.Fatal("invalid source matrix or access defaults", definition.Key)
		}
		seen[pair] = true
		for _, step := range definition.Commands.Steps {
			command := exec.Command(bash, "-n")
			command.Stdin = strings.NewReader(step.Script)
			if output, err := command.CombinedOutput(); err != nil || strings.Contains(step.Script, "@@") {
				t.Fatal(definition.Key, step.Name, err, string(output))
			}
		}
		config := serviceinstall.Config{Name: definition.Name, RuntimeMode: definition.RuntimeModes[0], BindAddress: "127.0.0.1", HostPort: definition.Port, MemoryMiB: definition.DefaultMemoryMiB}
		config.Parameters, _ = serviceinstall.ResolveParameters(definition.Parameters, nil, nil)
		source := serviceinstall.Source{Type: definition.SourceModes[0]}
		if source.Type == "ARCHIVE" {
			source.SHA256, source.ByteSize = strings.Repeat("a", 64), 1024
		}
		install := serviceinstall.Request{Definition: definition, Config: config, Source: source, SoftwareID: uuid.NewString(), BindingRevision: 1}
		if err := install.Validate(); err != nil {
			t.Fatal(definition.Key, err)
		}
		request := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString(), JobID: uuid.NewString(), Action: "SOFTWARE_INSTALL", SoftwareOperation: "SOFTWARE_INSTALL", DeadlineAt: "2026-11-10T00:00:00Z", Installation: &install}
		request.InputHash = deploy.CommandRequestDigest(request)
		if err := deploy.SignCommandRequest(&request, private); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(request)
		if len(data)+1 > 64<<10 {
			t.Fatal("request too large", definition.Key, len(data))
		}
		t.Logf("%s request=%d", definition.Key, len(data)+1)
	}
	for _, pair := range []string{"DOCKER/REGISTRY", "DOCKER/ARCHIVE", "NATIVE/ONLINE", "NATIVE/ARCHIVE"} {
		if !seen[pair] {
			t.Fatal("missing", pair)
		}
	}
}

func labelStudioFixture(t *testing.T) (string, string, func(string, string), func(string, []string) (string, error)) {
	t.Helper()
	bash, root := managementTestBash(t), t.TempDir()
	for _, directory := range []string{"work", "config", "data", "logs", "bin", "fake-bin"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(bash, "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(root))
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	posix := strings.TrimSpace(string(data))
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(script string, environment []string) (string, error) {
		t.Helper()
		command := exec.Command(bash, "--noprofile", "--norc")
		command.Env = append(os.Environ(), "OPS_INSTALL_ROOT="+posix, "OPS_SOFTWARE_ID=fixture", "OPS_BIND_ADDRESS=127.0.0.1", "OPS_HOST_PORT=18080", "OPS_MEMORY_MIB=8192", "OPS_PARAM_MAX_UPLOAD_MIB=500", "OPS_PARAM_PUBLIC_URL=http://labels.example.test:18080", "OPS_PARAM_SOURCE_DIRECTORY=", "OPS_RUNTIME_MODE=DOCKER")
		command.Env = append(command.Env, environment...)
		command.Stdin = strings.NewReader("export PATH=\"$OPS_INSTALL_ROOT/fake-bin:/usr/bin:/bin\"; hash -r\n" + script)
		data, err := command.CombinedOutput()
		return string(data), err
	}
	return root, posix, write, run
}

func TestLabelStudioDockerMapsUploadDataAndReadonlyDatasets(t *testing.T) {
	root, posix, write, run := labelStudioFixture(t)
	write("fake-bin/curl", "#!/bin/bash\nexit 0\n")
	write("fake-bin/chown", "#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$OPS_INSTALL_ROOT/chown-log\"\n")
	write("fake-bin/findmnt", "#!/bin/bash\nprintf '/unrelated-mount\\n'\n")
	write("fake-bin/docker", `#!/bin/bash
set -e
printf '%s\n' "$@" >> "$OPS_INSTALL_ROOT/docker-log"
case "$1" in
 run) printf 'fixture-container\n';;
 exec) if [[ "$2" == --user && -f "$OPS_INSTALL_ROOT/deny-dataset" ]]; then exit 1; fi;;
 inspect) printf 'fixture\n';;
 stop|rm) ;;
 *) exit 1;;
esac
`)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "sample.txt"), []byte("external-data"), 0600); err != nil {
		t.Fatal(err)
	}
	bash := managementTestBash(t)
	command := exec.Command(bash, "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(external))
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	path := strings.TrimSpace(string(data))
	environment := []string{"OPS_PARAM_SOURCE_DIRECTORY=" + path}
	if output, err := run(labelStudioScript("check", "docker-create"), environment); err != nil {
		t.Fatal(err, output)
	}
	args, _ := os.ReadFile(filepath.Join(root, "docker-log"))
	for _, expected := range []string{posix + "/data:/label-studio/data", path + ":/datasets:ro", "127.0.0.1:18080:8080", "DATA_UPLOAD_MAX_MEMORY_SIZE=524288000", "--entrypoint\n/label-studio/.venv/bin/label-studio", "--internal-host\n0.0.0.0", "LABEL_STUDIO_HOST=http://labels.example.test:18080"} {
		if !strings.Contains(string(args), expected) {
			t.Fatal("missing Docker setting", expected, string(args))
		}
	}
	changes, _ := os.ReadFile(filepath.Join(root, "chown-log"))
	if strings.Contains(string(changes), path) {
		t.Fatal("external dataset ownership changed", string(changes))
	}
	write("data/annotation.json", "saved-label")
	if output, err := run(labelStudioScript("mount-check", "uninstall"), environment); err != nil {
		t.Fatal(err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "data/annotation.json")); err != nil {
		t.Fatal("default uninstall removed annotations", err)
	}
	if output, err := run(labelStudioScript("mount-check", "delete-data"), environment); err != nil {
		t.Fatal(err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Fatal("explicit data deletion did not remove owned data")
	}
	if data, err := os.ReadFile(filepath.Join(external, "sample.txt")); err != nil || string(data) != "external-data" {
		t.Fatal("external dataset changed", err)
	}
}

func TestLabelStudioNativeUnitEnforcesReadonlyDatasetWithoutChangingItsOwner(t *testing.T) {
	root, posix, write, run := labelStudioFixture(t)
	write("config/ops-owner", "label-studio:fixture")
	write("fake-bin/chown", "#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$OPS_INSTALL_ROOT/chown-log\"\n")
	write("fake-bin/getent", "#!/bin/bash\nprintf 'lsfixture:x:1001:1001::%s:/usr/sbin/nologin\\n' \"$OPS_INSTALL_ROOT\"\n")
	write("fake-bin/useradd", "#!/bin/bash\nexit 1\n")
	write("fake-bin/id", "#!/bin/bash\nprintf 'fixture-group\\n'\n")
	write("fake-bin/runuser", "#!/bin/bash\nshift 3\nexec \"$@\"\n")
	write("fake-bin/systemctl", "#!/bin/bash\nexit 0\n")
	external := t.TempDir()
	command := exec.Command(managementTestBash(t), "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(external))
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	path := strings.TrimSpace(string(data))
	script := strings.ReplaceAll(labelStudioScript("native-create"), `path="/etc/systemd/system/$unit"`, `path="$OPS_INSTALL_ROOT/config/fixture.service"`)
	if output, err := run(script, []string{"OPS_RUNTIME_MODE=NATIVE", "OPS_PARAM_SOURCE_DIRECTORY=" + path}); err != nil {
		t.Fatal(err, output)
	}
	unit, err := os.ReadFile(filepath.Join(root, "config/fixture.service"))
	if err != nil || !strings.Contains(string(unit), "ReadOnlyPaths="+path) || !strings.Contains(string(unit), "User=lsfixture") {
		t.Fatal("readonly service directive absent", err, string(unit))
	}
	configuration, _ := os.ReadFile(filepath.Join(root, "config/runtime.env"))
	if !strings.Contains(string(configuration), "DATA_UPLOAD_MAX_MEMORY_SIZE=524288000") || !strings.Contains(string(configuration), "LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT="+path) {
		t.Fatal("native configuration does not apply selected inputs", string(configuration))
	}
	ownerChanges, _ := os.ReadFile(filepath.Join(root, "chown-log"))
	if strings.Contains(string(ownerChanges), path) || !strings.Contains(string(ownerChanges), posix+"/data") {
		t.Fatal("changed external dataset ownership", string(ownerChanges))
	}
}

func TestLabelStudioVerificationUsesCanonicalHealthAndRealUploadReadback(t *testing.T) {
	_, _, write, run := labelStudioFixture(t)
	write("config/ops-owner", "label-studio:fixture")
	write("fake-bin/docker", `#!/bin/bash
set -e
if [[ "$1" == inspect ]]; then
  printf 'fixture|`+labelStudioImageID+`|true\n'
elif [[ "$1" == exec && "$3" == /bin/sh ]]; then
  printf '%s' "${FAKE_UPLOAD_BYTES:-524288000}"
elif [[ "$1" == exec && "$3" == /label-studio/.venv/bin/python ]]; then
  exit 0
else exit 1; fi
`)
	write("fake-bin/curl", "#!/bin/bash\n[[ ${!#} == http://127.0.0.1:18080/health/ ]] || exit 1\nprintf 200\n")
	write("fake-bin/sleep", "#!/bin/bash\nexit 0\n")
	if output, err := run(labelStudioScript("verify"), nil); err != nil {
		t.Fatal("healthy instance rejected", err, output)
	}
	if output, err := run(labelStudioScript("verify"), []string{"FAKE_UPLOAD_BYTES=1"}); err == nil || !strings.Contains(output, "上传大小配置未生效") {
		t.Fatal("wrong runtime configuration accepted", err, output)
	}
}

func TestLabelStudioOfflineWheelhouseChecksCompleteManifestAndLockedRequirements(t *testing.T) {
	for _, scenario := range []string{"complete", "duplicate-omits-dependency", "pip-option", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			root, posix, write, run := labelStudioFixture(t)
			write("config/ops-owner", "label-studio:fixture")
			write("fake-bin/python3.12", `#!/bin/bash
set -e
if [[ "$1" == -m && "$2" == venv ]]; then
 mkdir -p "$3/bin"
 printf '#!/bin/bash\nexit 0\n' > "$3/bin/python"
 chmod 755 "$3/bin/python"
fi
`)
			hash := func(text string) string { sum := sha256.Sum256([]byte(text)); return hex.EncodeToString(sum[:]) }
			wheel := "wheels/label_studio-" + labelStudioVersion + "-py3-none-any.whl"
			dependency := "wheels/fixture_dependency-1.0-py3-none-any.whl"
			requirements := "label-studio==" + labelStudioVersion + "\nfixture-dependency==1.0\n"
			if scenario == "pip-option" {
				requirements += "--index-url=https://invalid.example.test\n"
			}
			manifest := fmt.Sprintf("%s  requirements.txt\n%s  %s\n%s  %s\n", hash(requirements), hash("official-wheel-fixture"), wheel, hash("dependency"), dependency)
			if scenario == "duplicate-omits-dependency" {
				manifest = fmt.Sprintf("%s  requirements.txt\n%s  %s\n%s  %s\n", hash(requirements), hash("official-wheel-fixture"), wheel, hash("official-wheel-fixture"), wheel)
			}
			var archive bytes.Buffer
			gzipWriter := gzip.NewWriter(&archive)
			tarWriter := tar.NewWriter(gzipWriter)
			for _, file := range []struct{ name, contents string }{{"requirements.txt", requirements}, {"SHA256SUMS", manifest}, {wheel, "official-wheel-fixture"}, {dependency, "dependency"}} {
				header := &tar.Header{Name: file.name, Mode: 0600, Size: int64(len(file.contents))}
				if scenario == "symlink" && file.name == dependency {
					header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, "/etc/passwd", 0
				}
				if err := tarWriter.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if header.Typeflag != tar.TypeSymlink {
					_, _ = tarWriter.Write([]byte(file.contents))
				}
			}
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			if err := os.WriteFile(filepath.Join(root, "wheelhouse.tar.gz"), archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			script := strings.ReplaceAll(labelStudioScript("native-material"), labelStudioWheelSHA, hash("official-wheel-fixture"))
			output, err := run(script, []string{"OPS_SOURCE=ARCHIVE", "OPS_RUNTIME_MODE=NATIVE", "OPS_ARTIFACT_PATH=" + posix + "/wheelhouse.tar.gz", "OPS_PARAM_PYTHON_BINARY=" + posix + "/fake-bin/python3.12"})
			if scenario == "complete" && err != nil {
				t.Fatal("complete wheelhouse rejected", err, output)
			}
			if scenario != "complete" && err == nil {
				t.Fatal("unsafe wheelhouse accepted", scenario, output)
			}
		})
	}
}
