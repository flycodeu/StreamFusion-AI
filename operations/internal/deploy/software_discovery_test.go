package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func discoveryFixture(command string, args []string) ([]byte, error) {
	switch command {
	case "/usr/bin/systemctl":
		if args[0] == "list-unit-files" {
			return []byte("mysql.service enabled enabled\nredis-server.service disabled enabled\nnginx.service enabled enabled\nredis@.service disabled enabled\nsshd.service enabled enabled\nmysql-exporter.service enabled enabled\n"), nil
		}
		return []byte("mysql.service loaded active running MySQL Server\nnginx.service loaded failed failed Web Server\n"), nil
	case "/usr/bin/docker":
		return []byte(`{"id":"` + strings.Repeat("a", 64) + `","name":"cache-prod","image":"docker.io/library/redis:7.4","state":"exited","ports":"127.0.0.1:6379->6379/tcp, [::1]:6380->6379/tcp, 65536/tcp"}` + "\n" + `{"id":"` + strings.Repeat("b", 64) + `","name":"unknown","image":"example/redis-exporter:1","state":"running","ports":""}`), nil
	case "/usr/bin/java":
		return []byte(`openjdk version "21.0.1"`), nil
	case "/usr/bin/python3":
		return []byte("Python 3.12.3"), nil
	case "/usr/bin/ffmpeg":
		return nil, errors.New("not found")
	case "/bin/sh":
		return []byte("FOUND\t/etc/mysql/mysql.conf.d/mysqld.cnf\nPORT\t/etc/mysql/mysql.conf.d/mysqld.cnf\t3307\nBIND\t/etc/mysql/mysql.conf.d/mysqld.cnf\t127.0.0.1\nFOUND\t/etc/redis/redis.conf\nPORT\t/etc/redis/redis.conf\t6380\nBIND\t/etc/redis/redis.conf\t127.0.0.1,-::1\n"), nil
	}
	return nil, errors.New("unexpected command")
}

func TestDiscoverSoftwareFindsInstalledStoppedAndRunningWithoutMutation(t *testing.T) {
	var mu sync.Mutex
	calls := []string{}
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 8*time.Second {
			t.Error("command is not bounded by eight seconds")
		}
		mu.Lock()
		calls = append(calls, command+" "+strings.Join(args, " "))
		mu.Unlock()
		return discoveryFixture(command, args)
	}
	report, err := DiscoverSoftware(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 6 || len(report.Checks) != 4 || len(report.Configurations) != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	states := map[string]string{}
	for _, candidate := range report.Candidates {
		states[candidate.Locator] = candidate.State
		if candidate.RuntimeKind == "SYSTEMD" && candidate.Version != "" {
			t.Fatal("native software version was invented", candidate)
		}
		if len(candidate.ConfigPaths) != 0 {
			t.Fatal("host configuration was guessed to belong to a unit", candidate)
		}
		if candidate.RuntimeKind == "DOCKER" && len(candidate.Ports) != 2 {
			t.Fatal("invalid container port was exposed", candidate)
		}
	}
	if states["mysql.service"] != "RUNNING" || states["redis-server.service"] != "INSTALLED" || states["nginx.service"] != "FAILED" || states[strings.Repeat("a", 64)] != "STOPPED" {
		t.Fatal("lifecycle state mismatch", states)
	}
	for _, call := range calls {
		for _, forbidden := range []string{"sudo ", "docker exec", " start ", " stop ", "ExecStart", "Config.Env", "cat "} {
			if strings.Contains(call, forbidden) {
				t.Fatal("discovery used an unsafe operation", call)
			}
		}
	}
	if report.Configurations[1].Port != 6380 || report.Configurations[1].BindAddress != "127.0.0.1, ::1" {
		t.Fatal("safe explicit configuration fields missing", report.Configurations)
	}
}

func TestDiscoverSoftwareSourceFailurePreservesOtherSources(t *testing.T) {
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/docker" {
			return nil, errors.New("permission denied")
		}
		if command == "/usr/bin/systemctl" && args[0] == "list-units" {
			return []byte("mysql.service loaded inactive dead forged-after-error"), errors.New("systemd offline")
		}
		return discoveryFixture(command, args)
	}
	report, err := DiscoverSoftware(context.Background(), run)
	if err != nil || !report.Limited || len(report.Configurations) != 2 {
		t.Fatalf("partial result lost: %+v %v", report, err)
	}
	if report.Checks[0].Status != "AVAILABLE" || report.Checks[0].Code != "SOFTWARE_SOURCE_PARTIAL" || report.Checks[1].Status != "UNAVAILABLE" {
		t.Fatal("source availability conflated", report.Checks)
	}
	for _, candidate := range report.Candidates {
		if candidate.Locator == "mysql.service" && candidate.State != "INSTALLED" {
			t.Fatal("failed command output was treated as evidence", candidate)
		}
	}
}

func TestDiscoverSoftwareCapsCandidatesAndRejectsUntrustedImages(t *testing.T) {
	lines := []string{}
	for i := range 110 {
		data, _ := json.Marshal(map[string]string{"id": strings.Repeat("0", 60) + fmtDiscoveryID(i), "name": "cache-" + strconv.Itoa(i), "image": "redis:7", "state": "running", "ports": "6379/tcp"})
		lines = append(lines, string(data))
	}
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/docker" {
			return []byte(strings.Join(lines, "\n")), nil
		}
		return nil, errors.New("not installed")
	}
	report, err := DiscoverSoftware(context.Background(), run)
	if err != nil || len(report.Candidates) != 100 || !report.Limited {
		t.Fatalf("candidate limit not enforced: %d %v", len(report.Candidates), err)
	}
	for _, image := range []string{"user:password@registry.example/mysql:8", "https://registry.example/redis:7", "redis-exporter:1", "example/mysql-helper:8", "redis:7\nsecret", "mysql@sha256:wrong"} {
		if kind := softwareImageType(image); kind != "" {
			t.Error("unsupported/credential image classified", image, kind)
		}
	}
	for _, image := range []string{"registry:5000/mysql:8", "mariadb:11", "bitnami/redis:7", "docker.io/library/nginx@sha256:" + strings.Repeat("a", 64)} {
		if kind := softwareImageType(image); kind == "" {
			t.Error("known repository basename not discovered", image)
		}
	}
}

func fmtDiscoveryID(i int) string {
	return strings.Repeat("0", 4-len(strconv.FormatInt(int64(i), 16))) + strconv.FormatInt(int64(i), 16)
}

func TestDiscoverSoftwareOutputLimitIsExplicit(t *testing.T) {
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "/usr/bin/docker" {
			return []byte(strings.Repeat("x", (64<<10)+1)), nil
		}
		return discoveryFixture(command, args)
	}
	report, err := DiscoverSoftware(context.Background(), run)
	if err != nil || report.Checks[1].Status != "UNAVAILABLE" || report.Checks[1].Code != "SOFTWARE_DISCOVERY_OUTPUT_LIMIT" || !report.Limited {
		t.Fatalf("overflow was presented as empty success: %+v %v", report, err)
	}
}

func TestDiscoverSoftwareConfigurationReportsOnlyValidatedHostMetadata(t *testing.T) {
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		return []byte("FOUND\t/etc/my.cnf\nPORT\t/etc/my.cnf\t3306\nPORT\t/etc/my.cnf\t99999\nBIND\t/etc/my.cnf\t127.0.0.1,password-secret,::1\nFOUND\t/tmp/stolen-config\nPORT\t/tmp/stolen-config\t1234\nPASSWORD\t/etc/my.cnf\tshould-never-appear\nDENIED\t/etc/redis.conf\n"), nil
	}
	rows, check := discoverConfigurations(context.Background(), run)
	if len(rows) != 1 || rows[0].Port != 3306 || rows[0].BindAddress != "127.0.0.1, ::1" || check.Code != "CONFIG_PARTIAL_READ" {
		t.Fatalf("unsafe/missing config report: %+v %+v", rows, check)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "should-never-appear") || strings.Contains(string(encoded), "stolen") {
		t.Fatal("configuration secrets escaped", string(encoded))
	}
}

func TestDiscoverSoftwareConfigurationAbsentDiffersFromDenied(t *testing.T) {
	for _, test := range []struct{ output, status, code string }{
		{"MISSING\t/etc/my.cnf\n", "AVAILABLE", "NO_SUPPORTED_CONFIG_FOUND"},
		{"DENIED\t/etc/my.cnf\n", "UNAVAILABLE", "CONFIG_READ_DENIED"},
		{"FOUND\t/etc/my.cnf\nDENIED\t/etc/my.cnf\n", "UNAVAILABLE", "CONFIG_READ_DENIED"},
		{"FOUND\t/etc/my.cnf\nPORT\t/etc/my.cnf\t3306\nDENIED\t/etc/my.cnf\nFOUND\t/etc/my.cnf\nPORT\t/etc/my.cnf\t3307\n", "UNAVAILABLE", "CONFIG_READ_DENIED"},
		{"LIMIT\t/etc/my.cnf\n", "UNAVAILABLE", "CONFIG_READ_DENIED"},
	} {
		rows, check := discoverConfigurations(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return []byte(test.output), nil })
		if len(rows) != 0 || check.Status != test.status || check.Code != test.code {
			t.Fatalf("wrong config availability: %+v %+v", rows, check)
		}
	}
}

func TestDiscoverSoftwareRunsSourcesIndependentlyWithinParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	configStarted := make(chan struct{})
	var once sync.Once
	run := func(commandCtx context.Context, command string, args ...string) ([]byte, error) {
		if command == "/bin/sh" {
			once.Do(func() { close(configStarted) })
			return discoveryFixture(command, args)
		}
		if command == "/usr/bin/docker" {
			<-commandCtx.Done()
			return nil, commandCtx.Err()
		}
		return discoveryFixture(command, args)
	}
	started := time.Now()
	report, err := DiscoverSoftware(ctx, run)
	if err != nil || !report.Limited || len(report.Configurations) != 2 {
		t.Fatal("one source starved successful config discovery", report, err)
	}
	select {
	case <-configStarted:
	default:
		t.Fatal("configuration source was never attempted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("parent deadline ignored")
	}
	if report.Checks[1].Code != "SOFTWARE_DISCOVERY_TIMEOUT" {
		t.Fatal("timeout not distinguishable", report.Checks)
	}
}

func TestDiscoveryPortValidation(t *testing.T) {
	for _, value := range []string{"3306/tcp", "127.0.0.1:6379->6379/tcp", "[::]:6380->6379/tcp", "10000-10010/udp"} {
		if !validDiscoveryPort(value) {
			t.Error("valid port not recognized", value)
		}
	}
	for _, value := range []string{"99999/tcp", "127.999.0.1:80->80/tcp", "0/tcp", "65536->3306/tcp", "200-100/tcp", "secret:3306->3306/tcp"} {
		if validDiscoveryPort(value) {
			t.Error("unsafe port accepted", value)
		}
	}
}

func TestDiscoverSystemdMalformedOutputDoesNotBecomeEmptySuccess(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte("mysql.service unexpected-state\ninvalid text\n"), nil
	}
	rows, check, limited := discoverSystemd(context.Background(), run)
	if len(rows) != 0 || check.Status != "UNAVAILABLE" || check.Code != "SOFTWARE_SOURCE_RESPONSE_INVALID" || !limited {
		t.Fatalf("malformed response became healthy empty result: %+v %+v", rows, check)
	}
}

func TestDiscoverToolsRedactsUnexpectedCredentialOutput(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Python 3.12 password=unexpected-secret token=unexpected-token"), nil
	}
	rows, _ := discoverTools(context.Background(), run)
	data, _ := json.Marshal(rows)
	if strings.Contains(string(data), "unexpected-secret") || strings.Contains(string(data), "unexpected-token") {
		t.Fatal("tool output leaked credentials", string(data))
	}
}

func TestDiscoverSoftwarePartialConfigurationSetsReportLimit(t *testing.T) {
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "/bin/sh" {
			return []byte("FOUND\t/etc/my.cnf\nPORT\t/etc/my.cnf\t3306\nDENIED\t/etc/redis.conf\n"), nil
		}
		return discoveryFixture(command, args)
	}
	report, err := DiscoverSoftware(context.Background(), run)
	if err != nil || !report.Limited || report.Checks[3].Status != "AVAILABLE" || report.Checks[3].Code != "CONFIG_PARTIAL_READ" {
		t.Fatalf("partial config was marked complete: %+v %v", report, err)
	}
}
