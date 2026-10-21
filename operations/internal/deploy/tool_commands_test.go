package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestProgrammingVersionProbeUsesIsolatedNonRootAccount(t *testing.T) {
	for _, key := range []string{"CODEX", "CLAUDE"} {
		t.Run(key, func(t *testing.T) {
			s := SoftwareRequest{ID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", Kind: "TOOL", SoftwareKey: key, Ownership: &SoftwareOwnership{RuntimeMode: "NATIVE", BinaryPath: "/srv/software/instance/bin/" + strings.ToLower(key), BinaryHash: strings.Repeat("a", 64)}}
			s.Locator = s.Ownership.BinaryPath
			calls := 0
			run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
				calls++
				if command == "/usr/bin/sha256sum" {
					return []byte(s.Ownership.BinaryHash + "  " + s.Locator), nil
				}
				if command != "/usr/sbin/runuser" {
					t.Fatalf("tool executed directly as executor: %s", command)
				}
				joined := strings.Join(args, " ")
				for _, required := range []string{"-u " + nativeUserName(s.ID), "/usr/bin/env -i --chdir=/srv/software/instance/data", "HOME=/srv/software/instance/data", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "--version"} {
					if !strings.Contains(joined, required) {
						t.Fatalf("missing isolation %s: %s", required, joined)
					}
				}
				return []byte("fixture-cli 1.2.3"), nil
			}
			actual, err := observeInstalledTool(context.Background(), s, run)
			if err != nil || actual.State != "INSTALLED" || calls != 2 {
				t.Fatal(actual, err, calls)
			}
		})
	}
}

func TestNewToolProbesRequireRealCapabilities(t *testing.T) {
	command, args, err := installedToolCommand("PYTHON")
	if err != nil || command != "python3" || !strings.Contains(strings.Join(args, " "), "ssl,sqlite3,bz2,lzma,ctypes,venv,ensurepip") {
		t.Fatal(command, args, err)
	}
	s := SoftwareRequest{ID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", SoftwareKey: "FFMPEG", Ownership: &SoftwareOwnership{BinaryPath: "/srv/app/bin/ffmpeg"}}
	if _, err := nativeToolCommand(context.Background(), s, func(context.Context, string, ...string) ([]byte, error) { return []byte("h264_nvenc"), nil }, "-encoders"); err == nil {
		t.Fatal("missing H264 software encoder accepted")
	}
	calls := 0
	version, err := nativeToolCommand(context.Background(), s, func(ctx context.Context, command string, args ...string) ([]byte, error) {
		calls++
		if strings.Contains(strings.Join(args, " "), "/srv/app/bin/ffprobe") {
			return []byte("ffprobe version 9.0.2"), nil
		}
		if args[len(args)-1] == "-version" {
			return []byte("ffmpeg version 9.0.2"), nil
		}
		return []byte("libx264"), nil
	}, "-encoders")
	if err != nil || calls != 3 || !strings.Contains(string(version), "9.0.2") {
		t.Fatal(string(version), err, calls)
	}
	if _, err := nativeToolCommand(context.Background(), s, func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("missing") }); err == nil {
		t.Fatal("failed command accepted")
	}
	python, _ := serviceinstall.Find("python", "3.12.15")
	if serviceinstall.UsesJavaBridge(python) || !installedToolVersionMatches(python, "3.12.15 (main, fixture)") || installedToolVersionMatches(python, "3.11.0") {
		t.Fatal("Python bridge/version boundary")
	}
}

func TestMediaMTXAndMinIODiscoveryKeepsReadOnlyBoundary(t *testing.T) {
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		switch command {
		case "/usr/bin/systemctl":
			if args[0] == "list-unit-files" {
				return []byte("mediamtx.service enabled enabled\nrtsp-simple-server.service disabled enabled\nminio.service enabled enabled\n"), nil
			}
			return []byte("mediamtx.service loaded active running Media\nminio.service loaded inactive dead MinIO\n"), nil
		case "/usr/bin/docker":
			return []byte(`{"id":"` + strings.Repeat("a", 64) + `","name":"media","image":"bluenviron/mediamtx:1.21.2","state":"running","ports":"8554/tcp"}` + "\n" + `{"id":"` + strings.Repeat("b", 64) + `","name":"storage","image":"quay.io/minio/minio:historical","state":"exited","ports":"9000/tcp"}`), nil
		}
		return nil, errors.New("not installed")
	}
	report, err := DiscoverSoftware(context.Background(), run)
	if err != nil || len(report.Candidates) != 5 {
		t.Fatal(err, report)
	}
	for _, c := range report.Candidates {
		if c.SoftwareType != "MEDIAMTX" && c.SoftwareType != "MINIO" {
			t.Fatal(c)
		}
		if c.RuntimeKind == "SYSTEMD" && c.Version != "" {
			t.Fatal("invented unmanaged binary version", c)
		}
	}
}
