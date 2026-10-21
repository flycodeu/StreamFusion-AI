package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSoftwareLocatorValidationRejectsShellAndProtectedServices(t *testing.T) {
	for _, locator := range []string{"ssh.service", "sshd.service", "docker.service", "systemd-journald.service", "streamfusion-ops.service", "mysql@prod.service", "../mysql.service", "mysql;id.service", "*.service", "-redis.service"} {
		if (SoftwareRequest{Kind: "SYSTEMD", Locator: locator}).Validate(true) == nil {
			t.Errorf("unsafe unit accepted: %q", locator)
		}
	}
	for _, locator := range []string{"-all", "a;id", "../container", "c$(id)", "a b"} {
		if (SoftwareRequest{Kind: "DOCKER", Locator: locator}).Validate(true) == nil {
			t.Errorf("unsafe container accepted: %q", locator)
		}
	}
	if err := (SoftwareRequest{Kind: "DOCKER", Locator: "redis-prod"}).Validate(true); err != nil {
		t.Fatal(err)
	}
	if err := (SoftwareRequest{Kind: "DOCKER", Locator: "redis-prod"}).Validate(false); err == nil {
		t.Fatal("a mutable container name was accepted for mutation")
	}
	if err := (SoftwareRequest{Kind: "TOOL", Locator: "/tmp/python"}).Validate(true); err == nil {
		t.Fatal("arbitrary tool path accepted")
	}
}

func unitSoftwareRunner(state, digest string, calls *[]string) SoftwareCommandRunner {
	return func(ctx context.Context, command string, args ...string) ([]byte, error) {
		*calls = append(*calls, command+" "+strings.Join(args, " "))
		switch command {
		case "/usr/bin/systemctl":
			return []byte("Id=redis.service\nLoadState=loaded\nActiveState=" + state + "\nFragmentPath=/usr/lib/systemd/system/redis.service\nDropInPaths=/etc/systemd/system/redis.service.d/override.conf\nTransient=no\nNeedDaemonReload=no\n"), nil
		case "/usr/bin/stat":
			lines := []string{}
			for _, file := range args[3:] {
				line := "0:755:directory:4096"
				if strings.HasSuffix(file, ".service") || strings.HasSuffix(file, ".conf") {
					line = "0:644:regular file:300"
				}
				lines = append(lines, line)
			}
			return []byte(strings.Join(lines, "\n")), nil
		case "/usr/bin/readlink":
			return []byte(strings.Join(args[2:], "\n")), nil
		case "/usr/bin/sha256sum":
			lines := []string{}
			for _, file := range args[1:] {
				lines = append(lines, digest+"  "+file)
			}
			return []byte(strings.Join(lines, "\n")), nil
		case "/usr/bin/journalctl":
			return []byte("password=hidden rtsp://viewer:camera-pass@host/video Authorization: Bearer accesssecret\n"), nil
		}
		return nil, errors.New("unexpected executable")
	}
}

func TestSoftwareReadOnlyUnitObservationDoesNotUseDockerOrSudo(t *testing.T) {
	calls := []string{}
	run := unitSoftwareRunner("active", strings.Repeat("a", 64), &calls)
	s := SoftwareRequest{Kind: "SYSTEMD", Locator: "redis.service"}
	actual, err := ObserveSoftware(context.Background(), s, run)
	if err != nil || actual.State != "RUNNING" || !Hex.MatchString(actual.IdentityHash) {
		t.Fatalf("unexpected observation: %+v %v", actual, err)
	}
	for _, command := range calls {
		if strings.Contains(command, "docker") || strings.Contains(command, "sudo") || strings.Contains(command, " start ") || strings.Contains(command, " stop ") {
			t.Fatal("read-only probe invoked mutation authority", command)
		}
	}
	s.IdentityHash = actual.IdentityHash
	otherCalls := []string{}
	if _, err = ObserveSoftware(context.Background(), s, unitSoftwareRunner("active", strings.Repeat("b", 64), &otherCalls)); err == nil || err.Error() != "SOFTWARE_IDENTITY_DRIFT" {
		t.Fatalf("changed service definition accepted: %v", err)
	}
	// Runtime state is excluded from the identity, so a legitimate stop does
	// not invalidate the original adoption binding.
	other, err := ObserveSoftware(context.Background(), s, unitSoftwareRunner("inactive", strings.Repeat("a", 64), &otherCalls))
	if err != nil || other.State != "STOPPED" || other.IdentityHash != s.IdentityHash {
		t.Fatalf("runtime state changed the lifecycle identity: %+v %v", other, err)
	}
}

func TestSoftwareUnitRejectsAliasesUnprotectedFilesAndMissingPermission(t *testing.T) {
	baseCalls := []string{}
	base := unitSoftwareRunner("inactive", strings.Repeat("a", 64), &baseCalls)
	for _, mode := range []string{"alias", "unprotected", "reload", "permission", "outside", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
				data, err := base(ctx, command, args...)
				if command == "/usr/bin/systemctl" {
					switch mode {
					case "alias":
						data = []byte(strings.ReplaceAll(string(data), "Id=redis.service", "Id=redis-server.service"))
					case "reload":
						data = []byte(strings.ReplaceAll(string(data), "NeedDaemonReload=no", "NeedDaemonReload=yes"))
					case "permission":
						return nil, errors.New("access denied")
					case "outside":
						data = []byte(strings.ReplaceAll(string(data), "/usr/lib/systemd/system/redis.service", "/tmp/redis.service"))
					}
				}
				if command == "/usr/bin/stat" && mode == "unprotected" {
					data = []byte("1000:666:regular file:1")
				}
				if command == "/usr/bin/stat" && mode == "symlink" {
					data = []byte("0:777:symbolic link:40")
				}
				return data, err
			}
			if _, err := ObserveSoftware(context.Background(), SoftwareRequest{Kind: "SYSTEMD", Locator: "redis.service"}, run); err == nil {
				t.Fatal("unsafe or unreadable unit was accepted")
			}
		})
	}
}

func TestSoftwareContainerDiscoveryPinsRealIDAndRejectsManagedObjects(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, labels := range []string{`{}`, `{"com.streamfusion.ops.instance":"another"}`, `{"sfops.instance":"another"}`} {
		run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
			if command != "/usr/bin/docker" || args[0] != "container" || args[1] != "inspect" || args[len(args)-2] != "--" || args[len(args)-1] != "redis-prod" {
				t.Fatalf("unexpected command: %s %v", command, args)
			}
			return []byte(`{"id":"` + id + `","name":"/redis-prod","imageId":"sha256:` + strings.Repeat("b", 64) + `","image":"redis:7","labels":` + labels + `,"state":"running"}`), nil
		}
		actual, err := ObserveSoftware(context.Background(), SoftwareRequest{Kind: "DOCKER", Locator: "redis-prod"}, run)
		if labels == `{}` {
			if err != nil || actual.Locator != id || actual.State != "RUNNING" || actual.Version != "redis:7" {
				t.Fatalf("container identity not frozen: %+v %v", actual, err)
			}
		} else if err == nil || err.Error() != "SOFTWARE_CONTAINER_ALREADY_MANAGED" {
			t.Fatalf("managed container accepted: %v", err)
		}
	}
}

func TestSoftwareToolsHaveNoLifecycleAndUseOnlyVersionCommands(t *testing.T) {
	for key, command := range map[string]string{"java": "/usr/bin/java", "python": "/usr/bin/python3", "ffmpeg": "/usr/bin/ffmpeg"} {
		run := func(ctx context.Context, executable string, args ...string) ([]byte, error) {
			if command != executable || len(args) != 1 || args[0] != "-version" && args[0] != "--version" {
				t.Fatal("unexpected tool command", executable, args)
			}
			return []byte("version 3.12.0\nextra details"), nil
		}
		actual, err := ObserveSoftware(context.Background(), SoftwareRequest{Kind: "TOOL", Locator: key}, run)
		if err != nil || actual.State != "INSTALLED" {
			t.Fatalf("tool observation: %+v %v", actual, err)
		}
		if _, err = ReadSoftwareLogs(context.Background(), SoftwareRequest{Kind: "TOOL", Locator: key}, run); err == nil {
			t.Fatal("tool was treated as a daemon")
		}
	}
}

func TestSoftwareLogsAreBoundedRedactedAndIdentityChecked(t *testing.T) {
	calls := []string{}
	run := unitSoftwareRunner("active", strings.Repeat("a", 64), &calls)
	actual, err := ReadSoftwareLogs(context.Background(), SoftwareRequest{Kind: "SYSTEMD", Locator: "redis.service"}, run)
	if err != nil || !actual.Truncated || strings.Contains(actual.Log, "hidden") || strings.Contains(actual.Log, "camera-pass") || strings.Contains(actual.Log, "accesssecret") {
		t.Fatalf("unsafe log result: %+v %v", actual, err)
	}
	if len(RedactSoftwareLogs(strings.Repeat("x", 50000))) != 24000 {
		t.Fatal("log tail is unbounded")
	}
	jsonLog := RedactSoftwareLogs(`{"password":"json-secret","api_key":"json-key"}`)
	if strings.Contains(jsonLog, "json-secret") || strings.Contains(jsonLog, "json-key") {
		t.Fatal("JSON log credentials were not redacted")
	}
	s := SoftwareRequest{Kind: "SYSTEMD", Locator: "redis.service", IdentityHash: strings.Repeat("c", 64)}
	before := len(calls)
	if _, err = ReadSoftwareLogs(context.Background(), s, run); err == nil {
		t.Fatal("logs read despite drift")
	}
	for _, command := range calls[before:] {
		if strings.Contains(command, "journalctl") {
			t.Fatal("drifted unit logs were read")
		}
	}
}

func TestSoftwareTimeoutAndOversizedObservationDoNotBecomeStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("timeout") }
	if fact, err := ObserveSoftware(ctx, SoftwareRequest{Kind: "TOOL", Locator: "python"}, run); err == nil || fact != nil {
		t.Fatalf("timeout produced known state: %+v %v", fact, err)
	}
	run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(strings.Repeat("x", (64<<10)+1)), nil
	}
	if fact, err := ObserveSoftware(context.Background(), SoftwareRequest{Kind: "TOOL", Locator: "python"}, run); err == nil || fact != nil {
		t.Fatalf("oversized output accepted: %+v %v", fact, err)
	}
}
