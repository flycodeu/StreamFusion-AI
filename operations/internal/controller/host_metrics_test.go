package controller

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

const hostMetricFixture = `SFOPS_METRICS_V1
HOST	node-a
OS	Ubuntu 24.04 LTS
KERNEL	6.8.0
MODEL	Test CPU
CORES	8
CPU0	10000	7000
CPU1	10800	7400
TIME0	100
TIME1	101
NET0	eth0	1000	3000
NET1	eth0	2024	5048
MEMTOTAL	8589934592
MEMAVAILABLE	4294967296
DISK	107374182400	21474836480
LOAD	1.2	2.3	3.4
`

func TestHostMetricsParsingAndReset(t *testing.T) {
	m, e := parseHostMetrics(hostMetricFixture)
	if e != nil || m.Code != "READY" || m.CPUCores != 8 || m.CPUPercent == nil || *m.CPUPercent != 50 || len(m.Network) != 1 || *m.Network[0].ReceiveBytesPerSecond != 1024 || *m.Network[0].SendBytesPerSecond != 2048 {
		t.Fatal(m, e)
	}
	for _, change := range []struct{ old, new string }{
		{"CPU1\t10800\t7400", "CPU1\t800\t400"},
		{"CPU1\t10800\t7400", "CPU1\t10000\t7000"},
		{"CPU1\t10800\t7400", "CPU1\t10800\t9000"},
	} {
		m, e = parseHostMetrics(strings.Replace(hostMetricFixture, change.old, change.new, 1))
		if e != nil || m.CPUPercent != nil || m.Code != "PARTIAL" {
			t.Fatal("invalid CPU delta was accepted", m, e)
		}
	}
	m, e = parseHostMetrics(strings.Replace(hostMetricFixture, "NET1\teth0\t2024\t5048", "NET1\teth0\t24\t48", 1))
	if e != nil || m.Network[0].ReceiveBytesPerSecond != nil {
		t.Fatal("counter reset accepted", m, e)
	}
	for _, bad := range []string{"bad", strings.Repeat("x", 16385), strings.Replace(hostMetricFixture, "CORES\t8", "CORES\tNaN", 1)} {
		if _, e = parseHostMetrics(bad); e == nil {
			t.Fatal("invalid response accepted")
		}
	}
	m, e = parseHostMetrics(strings.Replace(hostMetricFixture, "MEMAVAILABLE\t4294967296", "MEMAVAILABLE\t99999999999", 1))
	if e != nil || m.MemoryTotalBytes != nil || m.Code != "PARTIAL" {
		t.Fatal("invalid memory accepted", m, e)
	}
}

func TestHostMetricsSSHReadOnlyPersistenceAndCredentialFailure(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) { return []byte(hostMetricFixture), command == hostMetricsCommand })
	host := f.create(t, b, "metric-host")
	id := host["id"].(string)
	empty := dataOf(t, b.call("GET", "/hosts/"+id+"/metrics", nil, 200))
	if empty["freshness"] != "UNOBSERVED" || f.Commands.Load() != 0 {
		t.Fatal(empty)
	}
	target, _ := s.readTarget(id)
	m := b.h.probeHostMetrics(context.Background(), target)
	if e := b.h.saveHostObservation(target, m, "metric-test"); e != nil {
		t.Fatal(e)
	}
	result := dataOf(t, b.call("GET", "/hosts/"+id+"/metrics", nil, 200))
	if result["lastConfirmed"].(map[string]any)["cpuPercent"] != float64(50) {
		t.Fatal(result)
	}
	before := f.Commands.Load()
	b.call("GET", "/hosts/"+id+"/metrics", nil, 200)
	if f.Commands.Load() != before {
		t.Fatal("GET triggered SSH")
	}
	b.call("PATCH", "/hosts/"+id+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": "1"}, 200)
	result = dataOf(t, b.call("GET", "/hosts/"+id+"/metrics", nil, 200))
	if result["lastConfirmed"] == nil {
		t.Fatal("maintenance lost history")
	}
	s.DB.Exec("UPDATE credential_refs SET revoked_at=? WHERE id=?", Now(), f.CredentialID)
	m = b.h.probeHostMetrics(context.Background(), target)
	if e := b.h.saveHostObservation(target, m, "revoked-test"); e != nil {
		t.Fatal(e)
	}
	result = dataOf(t, b.call("GET", "/hosts/"+id+"/metrics", nil, 200))
	if result["lastConfirmed"] == nil || result["latestAttempt"].(map[string]any)["code"] != "CREDENTIAL_UNAVAILABLE" {
		t.Fatal(result)
	}
	var locks int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks").Scan(&locks)
	if locks != 0 {
		t.Fatal("probe acquired mutation lock")
	}
}

func TestHostMetricsLocalLinuxProbe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux procfs and df")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := localHostMetrics(ctx)
	if m.Code != "READY" || m.CPUPercent == nil || m.CPUCores < 1 {
		t.Fatal(m)
	}
}

func TestHostMetricsSeparateHostsAndBoundedProbes(t *testing.T) {
	s, b := setup(t)
	b.login()
	for _, cores := range []string{"8", "16"} {
		f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
			return []byte(strings.Replace(hostMetricFixture, "CORES\t8", "CORES\t"+cores, 1)), command == hostMetricsCommand
		})
		host := f.create(t, b, "node-"+cores)
		target, _ := s.readTarget(host["id"].(string))
		m := b.h.probeHostMetrics(context.Background(), target)
		if m.Code != "READY" {
			t.Fatalf("host %s probe failed before persistence: %+v", cores, m)
		}
		if e := b.h.saveHostObservation(target, m, "isolation"); e != nil {
			t.Fatal(e)
		}
		saved, e := s.readHostObservation(target)
		if e != nil || saved.Confirmed == nil || saved.Confirmed.CPUCores != m.CPUCores {
			t.Fatal(saved, e)
		}
	}
	release, e := b.h.acquireObservation(context.Background(), "one", true)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if _, e = b.h.acquireObservation(context.Background(), "one", true); e == nil {
		t.Fatal("same host concurrently probed")
	}
	release2, _ := b.h.acquireObservation(context.Background(), "two", true)
	defer release2()
	release3, _ := b.h.acquireObservation(context.Background(), "three", true)
	defer release3()
	if _, e = b.h.acquireObservation(context.Background(), "four", true); e == nil {
		t.Fatal("background consumed reserved slot")
	}
	release4, e := b.h.acquireObservation(context.Background(), "four", false)
	if e != nil {
		t.Fatal(e)
	}
	release4()
}
