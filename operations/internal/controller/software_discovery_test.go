package controller

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func discoveryFixtureResponse(command string) ([]byte, bool) {
	switch {
	case strings.Contains(command, "'list-unit-files'"):
		return []byte("mysql.service enabled enabled\nredis-server.service disabled enabled\n"), true
	case strings.Contains(command, "'list-units'"):
		return []byte("mysql.service loaded active running MySQL\nredis-server.service loaded inactive dead Redis\n"), true
	case strings.Contains(command, "'/usr/bin/docker'"):
		return []byte(`{"id":"` + strings.Repeat("a", 64) + `","name":"redis","image":"redis:7.4","state":"running","ports":"0.0.0.0:6379->6379/tcp"}`), true
	case strings.Contains(command, "'/usr/bin/java'"):
		return []byte("openjdk version 21.0.6\n"), true
	case strings.Contains(command, "'/usr/bin/python3'"):
		return []byte("Python 3.12.9\n"), true
	case strings.Contains(command, "'/usr/bin/ffmpeg'"):
		return []byte("ffmpeg version 7.1\n"), true
	case strings.Contains(command, "'/bin/sh'"):
		if strings.Contains(command, "'software-metadata'") {
			return []byte("FILE\tmysql.service\t/lib/systemd/system/mysql.service\nPACKAGE\tmysql.service\tmysql-server-8.0\t8.0.43\nEXE\tmysql.service\t/usr/sbin/mysqld\nPORT\tmysql.service\t0.0.0.0:3306/tcp\nFILE\tredis-server.service\t/lib/systemd/system/redis-server.service\nPACKAGE\tredis-server.service\tredis-server\t7:7.0.15-1\n"), true
		}
		return []byte("FOUND\t/etc/mysql/mysql.conf.d/mysqld.cnf\nPORT\t/etc/mysql/mysql.conf.d/mysqld.cnf\t3307\nBIND\t/etc/mysql/mysql.conf.d/mysqld.cnf\t127.0.0.1\n"), true
	}
	return nil, false
}

func readDiscovery(t *testing.T, b *browser, hostID, version string) hostSoftwareDiscovery {
	t.Helper()
	response := b.call("POST", "/hosts/"+hostID+"/software-discovery", map[string]string{"expectedVersion": version}, 200)
	var envelope struct {
		Data hostSoftwareDiscovery `json:"data"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	return envelope.Data
}

func discoveryCheckFor(t *testing.T, report hostSoftwareDiscovery, source string) deploy.SoftwareDiscoveryCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Source == source {
			return check
		}
	}
	t.Fatal("missing check", source)
	return deploy.SoftwareDiscoveryCheck{}
}

func TestHostSoftwareDiscoveryReadsWithoutExecutorAndReusesSSH(t *testing.T) {
	s, b := setup(t)
	b.login()
	var mu sync.Mutex
	commands := []string{}
	f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
		mu.Lock()
		commands = append(commands, command)
		mu.Unlock()
		return discoveryFixtureResponse(command)
	})
	host := f.create(t, b, "inventory host")
	hid := host["id"].(string)
	registered := registerTestSoftware(t, b, hid, "SYSTEMD", "mysql.service", "MYSQL")
	registerTestSoftware(t, b, hid, "DOCKER", "redis", "REDIS")
	before := f.Authentications.Load()
	report := readDiscovery(t, b, hid, "1")
	if report.HostID != hid || report.HostVersion != "1" || report.ObservedAt == "" || len(report.Candidates) != 6 || report.Limited {
		t.Fatal(report)
	}
	if f.Authentications.Load()-before != 1 {
		t.Fatal("discovery reopened SSH per probe", f.Authentications.Load()-before)
	}
	var mysqlFound, redisFound bool
	for _, candidate := range report.Candidates {
		if candidate.RuntimeKind == "SYSTEMD" && candidate.Locator == "mysql.service" {
			mysqlFound = true
			if candidate.RegisteredID != registered["id"] || candidate.State != "RUNNING" || candidate.Version != "8.0.43" || candidate.VersionSource != "PACKAGE" || len(candidate.ListeningPorts) != 1 {
				t.Fatal(candidate)
			}
		}
		if candidate.RuntimeKind == "DOCKER" {
			redisFound = true
			if candidate.RegisteredID != "" || candidate.Locator != strings.Repeat("a", 64) || len(candidate.Ports) != 1 {
				t.Fatal("name was treated as proven container identity", candidate)
			}
		}
	}
	if !mysqlFound || !redisFound || len(report.Configurations) != 1 || report.Configurations[0].Port != 3307 {
		t.Fatal(report)
	}
	for _, source := range []string{"SYSTEMD", "DOCKER", "TOOLS", "CONFIG"} {
		if check := discoveryCheckFor(t, report, source); check.Status != "AVAILABLE" {
			t.Fatal(check)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, command := range commands {
		if strings.Contains(command, "sudo") || strings.Contains(command, " start ") || strings.Contains(command, " stop ") || strings.Contains(command, "container inspect") {
			t.Fatal("discovery invoked a mutation or unbounded inspection", command)
		}
	}
	var jobs, locks, software, audits int
	s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs)
	s.DB.QueryRow("SELECT count(*) FROM operation_locks").Scan(&locks)
	s.DB.QueryRow("SELECT count(*) FROM software_instances").Scan(&software)
	s.DB.QueryRow("SELECT count(*) FROM audit WHERE action='HOST_SOFTWARE_DISCOVERED' AND object_id=? AND result LIKE 'SUCCESS candidates=6 %'", hid).Scan(&audits)
	if jobs != 0 || locks != 0 || software != 2 || audits != 1 {
		t.Fatal(jobs, locks, software, audits)
	}
}

func TestHostSoftwareDiscoveryPreservesPartialSourcesAndRejectsOversize(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
		if strings.Contains(command, "'/usr/bin/docker'") {
			return []byte(strings.Repeat("x", (64<<10)+1)), true
		}
		if strings.Contains(command, "'/bin/sh'") {
			return []byte("DENIED\t/etc/redis/redis.conf\n"), true
		}
		return discoveryFixtureResponse(command)
	})
	host := f.create(t, b, "partial inventory")
	report := readDiscovery(t, b, host["id"].(string), "1")
	if len(report.Candidates) != 5 || !report.Limited {
		t.Fatal(report)
	}
	if check := discoveryCheckFor(t, report, "DOCKER"); check.Status != "UNAVAILABLE" || check.Code != "SOFTWARE_DISCOVERY_OUTPUT_LIMIT" {
		t.Fatal(check)
	}
	if check := discoveryCheckFor(t, report, "CONFIG"); check.Status != "UNAVAILABLE" || check.Code != "CONFIG_READ_DENIED" {
		t.Fatal(check)
	}
	if check := discoveryCheckFor(t, report, "SYSTEMD"); check.Status != "AVAILABLE" {
		t.Fatal(check)
	}
}

func TestHostSoftwareDiscoveryDistinguishesEmptyFromUnavailable(t *testing.T) {
	for _, empty := range []bool{true, false} {
		name := "unavailable"
		if empty {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
				if !empty {
					return nil, false
				}
				if strings.Contains(command, "'list-unit-files'") || strings.Contains(command, "'list-units'") || strings.Contains(command, "'/usr/bin/docker'") {
					return []byte{}, true
				}
				if strings.Contains(command, "'/bin/sh'") {
					return []byte("MISSING\t/etc/my.cnf\n"), true
				}
				return nil, false
			})
			host := f.create(t, b, name)
			report := readDiscovery(t, b, host["id"].(string), "1")
			if len(report.Candidates) != 0 {
				t.Fatal(report)
			}
			for _, source := range []string{"SYSTEMD", "DOCKER"} {
				check := discoveryCheckFor(t, report, source)
				if empty {
					if check.Status != "AVAILABLE" || check.Code != "NO_SUPPORTED_SOFTWARE_FOUND" {
						t.Fatal(check)
					}
				} else if check.Status != "UNAVAILABLE" {
					t.Fatal(check)
				}
			}
		})
	}
}

func TestHostSoftwareDiscoveryVersionAndSessionRechecked(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		name := "host version changed"
		if revoke {
			name = "session revoked"
		}
		t.Run(name, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			var changed atomic.Bool
			var hid string
			f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
				if strings.Contains(command, "'list-unit-files'") && changed.CompareAndSwap(false, true) {
					query, args := "UPDATE targets SET version=version+1 WHERE id=?", []any{hid}
					if revoke {
						query, args = "DELETE FROM sessions", nil
					}
					if _, e := s.DB.Exec(query, args...); e != nil {
						t.Error(e)
					}
				}
				return discoveryFixtureResponse(command)
			})
			host := f.create(t, b, name)
			hid = host["id"].(string)
			status, code := 409, "HOST_CHANGED"
			if revoke {
				status, code = 401, "AUTH_REQUIRED"
			}
			response := b.call("POST", "/hosts/"+hid+"/software-discovery", map[string]string{"expectedVersion": "1"}, status)
			if !strings.Contains(response.Body.String(), code) {
				t.Fatal(response.Body.String())
			}
			var audits int
			s.DB.QueryRow("SELECT count(*) FROM audit WHERE action='HOST_SOFTWARE_DISCOVERED'").Scan(&audits)
			if audits != 0 {
				t.Fatal("stale report audited as success")
			}
		})
	}
}

func TestHostSoftwareDiscoveryRejectsInvalidOrUnauthorisedBeforeSSH(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, discoveryFixtureResponse)
	host := f.create(t, b, "restricted")
	hid := host["id"].(string)
	path := "/hosts/" + hid + "/software-discovery"
	b.call("POST", path, map[string]string{"expectedVersion": "0"}, 400)
	b.call("POST", path, map[string]string{"expectedVersion": "2"}, 409)
	b.call("POST", path, map[string]string{"expectedVersion": "1", "command": "whoami"}, 400)
	anonymous := &browser{t: t, h: b.h}
	anonymous.call("POST", path, map[string]string{"expectedVersion": "1"}, 401)
	old := b.csrf
	b.csrf = "invalid"
	b.call("POST", path, map[string]string{"expectedVersion": "1"}, 403)
	b.csrf = old
	for n := 0; n < cap(b.h.hostCheckSlots); n++ {
		b.h.hostCheckSlots <- struct{}{}
	}
	b.call("POST", path, map[string]string{"expectedVersion": "1"}, 429)
	for n := 0; n < cap(b.h.hostCheckSlots); n++ {
		<-b.h.hostCheckSlots
	}
	if f.Authentications.Load() != 0 || f.Commands.Load() != 0 {
		t.Fatal("rejected request reached target")
	}
	if runtime.GOOS != "linux" {
		local := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "Windows local", "kind": "LOCAL"}, 201))
		response := b.call("POST", "/hosts/"+local["id"].(string)+"/software-discovery", map[string]string{"expectedVersion": "1"}, 409)
		if !strings.Contains(response.Body.String(), "LINUX_REQUIRED") {
			t.Fatal(response.Body.String())
		}
	}
}

func TestSoftwareSharedRunnerClosesTimedOutSession(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
		if strings.Contains(command, "'/usr/bin/java'") {
			time.Sleep(150 * time.Millisecond)
			return []byte("version\n"), true
		}
		return nil, false
	})
	host := f.create(t, b, "timeout host")
	target, e := s.readTarget(host["id"].(string))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	e = b.h.withSoftwareRunner(ctx, target, func(run deploy.SoftwareCommandRunner) error {
		short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		defer stop()
		_, err := run(short, "/usr/bin/java", "-version")
		return err
	})
	if e == nil || !strings.Contains(e.Error(), "deadline") || time.Since(started) > 500*time.Millisecond {
		t.Fatal("command deadline was not enforced", e, time.Since(started))
	}
}
