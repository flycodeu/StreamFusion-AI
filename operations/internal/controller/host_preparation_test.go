package controller

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func targetPreparationFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := make([]byte, 64)
	copy(binary, "\x7fELF")
	binary[4], binary[5], binary[18] = 2, 1, 62
	script := []byte("#!/usr/bin/env bash\nset -Eeuo pipefail\n")
	for name, content := range map[string][]byte{"streamfusion-ops": binary, "install-target.sh": script} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := fmt.Sprintf("%x  streamfusion-ops\n%x  install-target.sh\n", sha256.Sum256(binary), sha256.Sum256(script))
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTargetPreparationRejectsWrongPlatformChangedBytesAndManifest(t *testing.T) {
	for _, test := range []struct {
		name string
		code string
		edit func(string)
	}{
		{"changed", "TARGET_PACKAGE_CHECKSUM_MISMATCH", func(dir string) { os.WriteFile(filepath.Join(dir, "install-target.sh"), []byte("changed"), 0755) }},
		{"windows", "TARGET_PACKAGE_PLATFORM_MISMATCH", func(dir string) {
			os.WriteFile(filepath.Join(dir, "streamfusion-ops"), []byte("MZ-Windows-program"), 0755)
		}},
		{"missing", "TARGET_PACKAGE_UNAVAILABLE", func(dir string) { os.Remove(filepath.Join(dir, "streamfusion-ops")) }},
		{"manifest", "TARGET_PACKAGE_MANIFEST_INVALID", func(dir string) {
			os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("invalid-checksum  streamfusion-ops\n"), 0644)
		}},
		{"directory", "TARGET_PACKAGE_UNSAFE", func(dir string) {
			os.Remove(filepath.Join(dir, "streamfusion-ops"))
			os.Mkdir(filepath.Join(dir, "streamfusion-ops"), 0755)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := targetPreparationFixture(t)
			test.edit(dir)
			if _, err := readTargetPackage(dir); err == nil || err.Error() != test.code {
				t.Fatalf("got %v, expected %s", err, test.code)
			}
		})
	}
}

func TestTargetPreparationArchiveBindsHostAndVerifiesAllFiles(t *testing.T) {
	dir := targetPreparationFixture(t)
	targetID, controllerID := uuid.NewString(), uuid.NewString()
	host := target{ID: targetID, sshTarget: sshTarget{Kind: "SSH", User: "ops"}, InstallRoot: "/srv/sfops-test", Version: "3", Capabilities: unknownHostCapabilities()}
	p, material := preparationForHost(host, controllerID, dir)
	if !p.Package.Available || !strings.Contains(p.Package.DownloadURL, "expectedVersion=3") || p.Commands.Apply != "bash ./install.sh" || p.Commands.Authorize != "bash ./install.sh" {
		t.Fatalf("incorrect host preparation: %+v", p)
	}
	if !strings.Contains(p.Commands.Upgrade, `--binary "$(realpath ./streamfusion-ops)"`) {
		t.Fatal("upgrade command does not provide the required absolute binary path")
	}
	archive, err := createTargetPreparationArchive(p, material)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil || header.Typeflag != tar.TypeReg {
			t.Fatal("invalid package entry", err)
		}
		files[header.Name], err = io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) != 4 || files["install-target.sh"] != nil || !strings.Contains(string(files["HOST-SETUP.md"]), controllerID) || !strings.Contains(string(files["HOST-SETUP.md"]), targetID) || !strings.Contains(string(files["install.sh"]), "SFOPS_SETUP_READY") {
		t.Fatal("host identity missing or unexpected package files")
	}
	if !strings.Contains(string(files["install.sh"]), "--authorize-only --apply") || !strings.Contains(string(files["install.sh"]), controllerID) || !strings.Contains(string(files["install.sh"]), targetID) || !strings.Contains(string(files["install.sh"]), "--account 'ops'") {
		t.Fatal("installer is missing the bound identity or existing authorization repair")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(files["SHA256SUMS"])), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fmt.Sprintf("%x", sha256.Sum256(files[fields[1]])) != fields[0] {
			t.Fatal("package checksum mismatch")
		}
	}
	host.User = "root"
	p, _ = preparationForHost(host, controllerID, dir)
	if strings.Contains(p.Commands.Apply, "sudo") || strings.Contains(p.Authorization.Command, "sudo") {
		t.Fatal("root preparation unnecessarily relies on sudo")
	}
	if err = os.WriteFile(material.Script.Path, []byte("changed-after-preflight"), 0755); err != nil {
		t.Fatal(err)
	}
	if file, err := createTargetPreparationArchive(p, material); err == nil {
		file.Close()
		os.Remove(file.Name())
		t.Fatal("changed material was packaged")
	}
}

func TestHostPreparationIsAuthenticatedReadOnlyAndVersionBound(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "prepare-host", "kind": "LOCAL"}, 201))
	id := host["id"].(string)
	var auditBefore, auditAfter int
	if err := s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	p := dataOf(t, b.call("GET", "/hosts/"+id+"/preparation", nil, 200))
	if p["expectedHostVersion"] != "1" || p["targetId"] != id || p["controllerId"] != s.controllerID() {
		t.Fatal(p)
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if auditBefore != auditAfter {
		t.Fatal("GET preparation wrote an audit or state mutation")
	}
	b.call("GET", "/hosts/"+id+"/preparation/download?expectedVersion=0", nil, 409)
	b.call("GET", "/hosts/"+id+"/preparation/download?expectedVersion=1", nil, 409)
	b.call("POST", "/auth/logout", nil, 200)
	b.call("GET", "/hosts/"+id+"/preparation", nil, 401)
}

func TestExecutorFailureClassificationDoesNotReturnTargetSecrets(t *testing.T) {
	for _, test := range []struct{ output, code string }{
		{"sudo: a password is required", "SUDO_AUTHORIZATION_REQUIRED"},
		{"sudo: user is not in the sudoers file", "SUDO_AUTHORIZATION_REQUIRED"},
		{"/bin/sh: /usr/bin/sudo: not found", "SUDO_UNAVAILABLE"},
		{"sudo: /opt/managed/executor: command not found", "EXECUTOR_MISSING"},
		{"sudo: unable to execute /opt/managed/executor: Permission denied", "EXECUTOR_NOT_EXECUTABLE"},
		{"EXECUTOR_IDENTITY_MISMATCH", "EXECUTOR_IDENTITY_MISMATCH"},
		{"EXECUTOR_INSTALL_REQUIRED", "EXECUTOR_INITIALIZATION_REQUIRED"},
		{"LINUX_EXECUTOR_REQUIRED", "EXECUTOR_PRIVILEGE_REQUIRED"},
		{"ENVELOPE_INVALID", "EXECUTOR_UPGRADE_REQUIRED"},
		{"UNKNOWN_ACTION", "EXECUTOR_UPGRADE_REQUIRED"},
		{"DOCKER_UNAVAILABLE", "DOCKER_ENGINE_UNAVAILABLE"},
		{"COMPOSE_UNAVAILABLE", "COMPOSE_UNAVAILABLE"},
		{"password=opaque-test-secret", "EXECUTOR_CALL_FAILED"},
	} {
		got := classifyExecutorInvocation(errors.New("ssh exit 1"), test.output)
		if got.Error() != test.code || strings.Contains(got.Error(), "opaque-test-secret") || executorFailureCode(got) != test.code {
			t.Fatalf("unsafe classification: %v", got)
		}
	}
	if code := executorFailureCode(context.DeadlineExceeded); code != "EXECUTOR_CHECK_TIMEOUT" {
		t.Fatal(code)
	}
	if code := executorFailureCode(errors.New("json: unknown field private-output")); code != "EXECUTOR_RESPONSE_INVALID" {
		t.Fatal(code)
	}
}

func TestHostDockerEngineDoesNotRequireCompose(t *testing.T) {
	s, b := setup(t)
	b.login()
	targetID := ""
	f := hostSSHServer(t, s, b, true, false, func(command string) ([]byte, bool) {
		if !strings.Contains(command, "target-exec") {
			return nil, false
		}
		return []byte(fmt.Sprintf(`{"owner":{"protocolVersion":1,"executorVersion":"0.2.0","controllerId":%q,"targetId":%q,"dockerSocket":"","dockerBinary":""},"platform":"linux/amd64","dockerAvailable":false,"dockerEngineAvailable":true,"dockerComposeAvailable":false,"softwareLifecycleVersion":1,"serviceInstallVersion":2,"serviceCatalogHash":"hash","nativeInstallVersion":1,"softwareConfigureVersion":1,"toolProbeVersion":1}`, s.controllerID(), targetID)), true
	})
	host := f.create(t, b, "engine-only")
	targetID = host["id"].(string)
	checked := dataOf(t, b.call("POST", "/hosts/"+targetID+"/checks", nil, 200))
	caps := checked["capabilities"].(map[string]any)
	if caps["docker"].(map[string]any)["status"] != "AVAILABLE" || caps["dockerCompose"].(map[string]any)["status"] != "UNAVAILABLE" || caps["mutation"].(map[string]any)["status"] != "AVAILABLE" {
		t.Fatal(caps)
	}
	if code := caps["dockerCompose"].(map[string]any)["code"]; code != "COMPOSE_UNAVAILABLE" {
		t.Fatal(code)
	}
	if command := executorSSHCommand(target{sshTarget: sshTarget{User: "root"}, InstallRoot: "/opt/managed"}); command != "'/opt/managed/executor' target-exec" {
		t.Fatal(command)
	}
	if command := executorSSHCommand(target{sshTarget: sshTarget{User: "ops"}, InstallRoot: "/opt/managed"}); command != "/usr/bin/sudo -n '/opt/managed/executor' target-exec" {
		t.Fatal(command)
	}
}

func TestHostPreparationExpiredCapabilitiesAreNotPresentedAsReady(t *testing.T) {
	host := target{ID: uuid.NewString(), sshTarget: sshTarget{Kind: "SSH", User: "ops"}, InstallRoot: "/opt/managed", Version: "1", Capabilities: unknownHostCapabilities()}
	host.Capabilities.Mutation = checkedCapability(true, "EXECUTOR_AUTHORIZED", "ready")
	host.Capabilities.ExpiresAt = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	p, _ := preparationForHost(host, uuid.NewString(), t.TempDir())
	for _, step := range p.Steps {
		if step.Key != "package" && step.Status == "AVAILABLE" {
			t.Fatal("expired capability presented as ready")
		}
	}
}

func TestHostPreparationLocalPackageMatchesSupportedPlatform(t *testing.T) {
	for _, test := range []struct {
		goos, goarch string
		want         bool
	}{
		{"linux", "amd64", true},
		{"linux", "arm64", false},
		{"darwin", "amd64", false},
		{"windows", "amd64", false},
	} {
		if got := localTargetPlatformSupported(test.goos, test.goarch); got != test.want {
			t.Fatalf("localTargetPlatformSupported(%q, %q)=%v, want %v", test.goos, test.goarch, got, test.want)
		}
	}
	host := target{ID: uuid.NewString(), sshTarget: sshTarget{Kind: "LOCAL", User: "root"}, InstallRoot: "/opt/managed", Version: "1", Capabilities: unknownHostCapabilities()}
	p, _ := preparationForHost(host, uuid.NewString(), targetPreparationFixture(t))
	supported := runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
	if p.Package.Available != supported {
		t.Fatalf("local package availability=%v for %s/%s", p.Package.Available, runtime.GOOS, runtime.GOARCH)
	}
	if !supported && (p.Package.Code != "LINUX_TARGET_REQUIRED" || p.Package.DownloadURL != "" || !strings.Contains(p.Package.Message, "Linux amd64")) {
		t.Fatalf("unsupported local platform has a downloadable package or misleading message: %+v", p.Package)
	}
	for _, step := range p.Steps {
		if step.Key == "package" && (step.Status == "AVAILABLE") != supported {
			t.Fatalf("local package step differs from platform support: %+v", step)
		}
	}
	readme := targetSetupReadme(p)
	if strings.Contains(readme, "非空目录") || !strings.Contains(readme, "未知已有目录") {
		t.Fatal("offline instructions do not describe the refusal of all unknown existing directories")
	}
}
