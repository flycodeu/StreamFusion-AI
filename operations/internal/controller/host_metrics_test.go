package controller

import (
	"context"
	"fmt"
	"os/exec"
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
	for _, bad := range []string{"bad", strings.Repeat("x", hostMetricsOutputLimit+1), strings.Replace(hostMetricFixture, "CORES\t8", "CORES\tNaN", 1)} {
		if _, e = parseHostMetrics(bad); e == nil {
			t.Fatal("invalid response accepted")
		}
	}
	m, e = parseHostMetrics(strings.Replace(hostMetricFixture, "MEMAVAILABLE\t4294967296", "MEMAVAILABLE\t99999999999", 1))
	if e != nil || m.MemoryTotalBytes != nil || m.Code != "PARTIAL" {
		t.Fatal("invalid memory accepted", m, e)
	}
}

func TestHostMetricsDetailsKeepOptionalFieldsAndBoundedLists(t *testing.T) {
	output := hostMetricFixture + "ARCH\tx86_64\nBOOT\t1700000000\nSWAPTOTAL\t8192\nSWAPFREE\t2048\nPRIMARY\teth0\nPHYSICAL\teth0\nPROCESS_CPU_BASIS\tLIFETIME_AVERAGE\n"
	for i := 1; i <= 6; i++ {
		output += fmt.Sprintf("NETADDR\teth0\t192.0.2.%d/24\n", i)
	}
	for i := 1; i <= 25; i++ {
		output += fmt.Sprintf("PROCESS\t%d\tubuntu\t300.5\t5242880\tworker name\n", i)
	}
	output += "PROCESS\t1\tother\t0\t0\tduplicate\nNETADDR\teth0\t192.0.2.1/24\n"
	m, err := parseHostMetrics(output)
	if err != nil || m.Code != "READY" || m.Arch != "x86_64" || m.BootTime != "2023-11-14T22:13:20Z" || m.SwapTotalBytes == nil || *m.SwapTotalBytes != 8192 || m.SwapFreeBytes == nil || *m.SwapFreeBytes != 2048 {
		t.Fatal(m, err)
	}
	if m.PrimaryInterface != "eth0" || len(m.Network) != 1 || m.Network[0].Kind != "PHYSICAL" || len(m.Network[0].Addresses) != 4 || m.Network[0].Addresses[0] != "192.0.2.1/24" {
		t.Fatal("interface information changed", m.Network)
	}
	if m.ProcessCPUBasis != "LIFETIME_AVERAGE" || len(m.Processes) != 20 || m.Processes[0].CPUPercent != 300.5 || m.Processes[0].MemoryBytes != 5242880 || m.Processes[0].Name != "worker name" || m.Processes[0].User != "ubuntu" {
		t.Fatal("process information lost or unbounded", m.Processes)
	}
}

func TestHostMetricsInvalidOptionalDataDoesNotLoseCoreResources(t *testing.T) {
	output := hostMetricFixture + "BOOT\t253402300800\nSWAPTOTAL\t100\nSWAPFREE\t101\nPRIMARY\tmissing0\nNETADDR\teth0\tbad-address\nNETADDR\t../other\t192.0.2.1/24\nPROCESS_CPU_BASIS\tLIFETIME_AVERAGE\n"
	for _, row := range []string{
		"0\tubuntu\t1\t1\tzero-pid", "4294967296\tubuntu\t1\t1\tlarge-pid",
		"1\tubuntu\tNaN\t1\tbad-cpu", "2\tubuntu\t1\t-1\tbad-memory", "3\tubuntu\t1\t1\tbad\x1bname",
		"4\t" + strings.Repeat("u", 65) + "\t1\t1\tlong-user", "5\tubuntu\t1\t1\t" + strings.Repeat("x", 129),
	} {
		output += "PROCESS\t" + row + "\n"
	}
	output += "PROCESS\t6\tubuntu\t1\t1\tvalid\nPROCESS\t6\tother\t3\t3\tduplicate\n"
	m, err := parseHostMetrics(output)
	if err != nil || m.Code != "READY" || m.BootTime != "" || m.SwapTotalBytes != nil || m.SwapFreeBytes != nil || m.PrimaryInterface != "" || len(m.Network[0].Addresses) != 0 || len(m.Processes) != 1 || m.Processes[0].PID != 6 || m.Processes[0].Name != "valid" {
		t.Fatal("malformed optional data affected valid resources", m, err)
	}
	m, err = parseHostMetrics(strings.Replace(output, "PROCESS_CPU_BASIS\tLIFETIME_AVERAGE", "PROCESS_CPU_BASIS\tUNKNOWN", 1))
	if err != nil || len(m.Processes) != 0 || m.ProcessCPUBasis != "" {
		t.Fatal("process CPU accepted without a known basis", m, err)
	}
	m, err = parseHostMetrics(hostMetricFixture + "SWAPTOTAL\t0\nSWAPFREE\t0\n")
	if err != nil || m.SwapTotalBytes == nil || *m.SwapTotalBytes != 0 || m.SwapFreeBytes == nil || *m.SwapFreeBytes != 0 {
		t.Fatal("disabled swap was reported as unknown", m, err)
	}
}

func TestHostMetricsOutputRemainsBounded(t *testing.T) {
	output := hostMetricFixture + strings.Repeat("IGNORED\t0\n", 2000)
	if len(output) <= 16<<10 || len(output) >= hostMetricsOutputLimit {
		t.Fatal("fixture does not exercise the expanded bounded output")
	}
	if _, err := parseHostMetrics(output); err != nil {
		t.Fatal(err)
	}
	if _, err := parseHostMetrics(output + strings.Repeat("x", hostMetricsOutputLimit)); err == nil {
		t.Fatal("oversized metrics accepted")
	}
}

func TestHostMetricsInterfacesAndAddressesHaveGlobalBounds(t *testing.T) {
	output := hostMetricFixture
	for i := 0; i < 80; i++ {
		output += fmt.Sprintf("NET1\tnic%d\t1\t2\nNETADDR\tnic%d\t192.0.2.1/24\nPHYSICAL\tnic%d\n", i, i, i)
	}
	m, err := parseHostMetrics(output)
	if err != nil || len(m.Network) != 64 {
		t.Fatal("unbounded network interfaces", m, err)
	}
	count := 0
	for _, nic := range m.Network {
		count += len(nic.Addresses)
	}
	if count > 64 {
		t.Fatal("unbounded network addresses", count)
	}
}

func TestHostMetricsSSHReadOnlyPersistenceAndCredentialFailure(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
		return []byte(hostMetricFixture + strings.Repeat("IGNORED\t0\n", 2000)), command == hostMetricsCommand
	})
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
	if m.Arch == "" || m.BootTime == "" || m.SwapTotalBytes == nil || m.SwapFreeBytes == nil || *m.SwapFreeBytes > *m.SwapTotalBytes {
		t.Fatal("Linux system details missing", m)
	}
	if _, err := exec.LookPath("ps"); err == nil && (m.ProcessCPUBasis != "LIFETIME_AVERAGE" || len(m.Processes) == 0 || len(m.Processes) > 20) {
		t.Fatal("Linux process snapshot missing or unbounded", m.Processes)
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
