package deploy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLabelStudioDiscoveryKeepsSystemdAndContainerEvidenceSeparate(t *testing.T) {
	run := func(_ context.Context, command string, args ...string) ([]byte, error) {
		switch command {
		case "/usr/bin/systemctl":
			if args[0] == "list-unit-files" {
				return []byte("label-studio.service enabled enabled\nlabel-studio-helper.service enabled enabled\n"), nil
			}
			return []byte("label-studio.service loaded active running Label Studio\n"), nil
		case "/bin/sh":
			return []byte("FILE\tlabel-studio.service\t/etc/systemd/system/label-studio.service\nEXE\tlabel-studio.service\t/opt/anaconda3/bin/python3.11\nPACKAGE\tlabel-studio.service\tlabel-studio\t1.22.0\n"), nil
		case "/usr/bin/docker":
			return []byte(`{"id":"` + strings.Repeat("a", 64) + `","name":"labels","image":"heartexlabs/label-studio:1.22.0","state":"running","ports":"8080/tcp"}`), nil
		}
		return nil, errors.New("unexpected command")
	}
	units, _, _ := discoverSystemd(context.Background(), run)
	if len(units) != 1 || units[0].SoftwareType != "LABEL_STUDIO" || units[0].State != "RUNNING" || units[0].Version != "1.22.0" || units[0].PackageName != "label-studio" || units[0].VersionSource != "PACKAGE" || units[0].ExecutablePath != "/opt/anaconda3/bin/python3.11" {
		t.Fatal(units)
	}
	containers, _, _ := discoverDocker(context.Background(), run)
	if len(containers) != 1 || containers[0].SoftwareType != "LABEL_STUDIO" || containers[0].VersionSource != "IMAGE_REFERENCE" {
		t.Fatal(containers)
	}
	for _, name := range []string{"label-studio", "labelstudio", "label_studio"} {
		if knownSoftwareName(name) != "LABEL_STUDIO" {
			t.Fatal("canonical alias missing", name)
		}
	}
	if knownSoftwareName("label-studio-helper") != "" {
		t.Fatal("unrelated service classified as Label Studio")
	}
}

func TestLabelStudioPackageMetadataReadsOnlyTheLauncherEnvironment(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("requires a POSIX shell for the fixed metadata probe")
	}
	for _, scenario := range []string{"valid", "ambiguous", "oversized", "wrong-package", "symlink", "large-argv"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			posix := filepath.ToSlash(dir)
			if runtime.GOOS == "windows" {
				posix = "/" + strings.ToLower(posix[:1]) + posix[2:]
			}
			prefix := filepath.Join(dir, "env")
			metadata := filepath.Join(prefix, "lib/python3.11/site-packages/label_studio-1.22.0.dist-info/METADATA")
			launcher := filepath.Join(prefix, "bin/label-studio")
			for _, directory := range []string{filepath.Dir(metadata), filepath.Dir(launcher)} {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(file, value string) {
				t.Helper()
				if err := os.WriteFile(file, []byte(value), 0755); err != nil {
					t.Fatal(err)
				}
			}
			write(launcher, "#!/bin/sh\nprintf executed > "+posixSingleQuote(posix+"/application-executed")+"\n")
			content := "Name: label-studio\nVersion: 1.22.0\nSummary: private description must stay on the host\n"
			if scenario == "oversized" {
				content += strings.Repeat("x", 65537)
			}
			if scenario == "wrong-package" {
				content = strings.Replace(content, "Name: label-studio", "Name: another-app", 1)
			}
			write(metadata, content)
			if scenario == "ambiguous" {
				other := filepath.Join(prefix, "lib/python3.11/site-packages/label_studio-1.21.0.dist-info")
				if err := os.Mkdir(other, 0755); err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(other, "METADATA"), "Name: label-studio\nVersion: 1.21.0\n")
			}
			if scenario == "symlink" {
				actual := filepath.Join(dir, "foreign-metadata")
				if err := os.Rename(metadata, actual); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(actual, metadata); err != nil {
					t.Skip("symlink creation unavailable")
				}
			}
			write(filepath.Join(dir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$TEST_EXEC_START\"\n")
			start := "{ path=/opt/anaconda3/bin/python3.11 ; argv[]=" + posix + "/env/bin/python " + posix + "/env/bin/label-studio start --token=private-argument ; ignore_errors=no ; }"
			if scenario == "large-argv" {
				start += strings.Repeat("x", 16385)
			}
			script := strings.ReplaceAll(deployScript("observe/label-studio-package.sh"), "/usr/bin/systemctl", posixSingleQuote(posix+"/systemctl")) + "\nlabel_studio_package label-studio.service\n"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, shell, "-c", script)
			cmd.Env = append(os.Environ(), "TEST_EXEC_START="+start)
			data, err := cmd.CombinedOutput()
			want := ""
			if scenario == "valid" {
				want = "PACKAGE\tlabel-studio.service\tlabel-studio\t1.22.0\n"
			}
			// No metadata is a normal read-only result, including a nonzero shell
			// status from an unavailable candidate. Only stdout is meaningful.
			if ctx.Err() != nil || string(data) != want || scenario == "valid" && err != nil {
				t.Fatalf("metadata output=%q err=%v", data, err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "application-executed")); !os.IsNotExist(err) {
				t.Fatal("candidate launcher was executed", err)
			}
		})
	}
}
