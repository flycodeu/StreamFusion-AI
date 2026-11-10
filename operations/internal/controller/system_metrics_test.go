package controller

import (
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSystemMetricsCPUUsesDeltaAndRejectsCounterReset(t *testing.T) {
	before := cpuCounter{total: 1000, idle: 400, valid: true}
	for _, tc := range []struct {
		name   string
		before cpuCounter
		after  cpuCounter
		want   *float64
	}{
		{"first sample", cpuCounter{}, before, nil},
		{"missing sample", before, cpuCounter{}, nil},
		{"no elapsed ticks", before, before, nil},
		{"counter reset", before, cpuCounter{total: 900, idle: 300, valid: true}, nil},
		{"idle counter reset", before, cpuCounter{total: 1200, idle: 300, valid: true}, nil},
		{"invalid delta", before, cpuCounter{total: 1100, idle: 600, valid: true}, nil},
		{"half busy", before, cpuCounter{total: 1200, idle: 500, valid: true}, floatPointer(50)},
		{"fully idle", before, cpuCounter{total: 1200, idle: 600, valid: true}, floatPointer(0)},
		{"fully busy", before, cpuCounter{total: 1200, idle: 400, valid: true}, floatPointer(100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cpuUsage(tc.before, tc.after)
			if tc.want == nil && got != nil || tc.want != nil && (got == nil || *got != *tc.want) {
				t.Fatalf("unexpected CPU usage: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestSystemMetricsLinuxCPUExcludesGuestAndRejectsInvalidInput(t *testing.T) {
	got := parseLinuxCPU("cpu 100 10 50 400 20 5 5 10 99 77\ncpu0 1 2 3 4\n")
	if !got.valid || got.total != 600 || got.idle != 420 {
		t.Fatalf("guest time double-counted or wrong idle calculation: %+v", got)
	}
	for _, input := range []string{"", "cpu0 1 2 3 4", "cpu 1 2 3", "cpu 1 bad 3 4", "cpu -1 2 3 4", "cpu 18446744073709551615 1 0 0"} {
		if parseLinuxCPU(input).valid {
			t.Fatalf("invalid CPU input accepted: %q", input)
		}
	}
}

func TestSystemMetricsMemoryUsesAvailableRatherThanFree(t *testing.T) {
	got := parseLinuxMemory("MemTotal: 1024 kB\nMemFree: 1 kB\nMemAvailable: 512 kB\nCached: 500 kB\n")
	if !got.Available || got.TotalBytes != 1024*1024 || got.UsedBytes != 512*1024 || got.UsagePercent != 50 {
		t.Fatalf("cached memory incorrectly counted as unavailable: %+v", got)
	}
	for _, input := range []string{"MemTotal: 1024 kB", "MemTotal: 1024 kB\nMemAvailable: 2048 kB", "MemTotal: 0 kB\nMemAvailable: 0 kB", "MemTotal: -1 kB\nMemAvailable: 1 kB", "MemTotal: 1024 MB\nMemAvailable: 1 kB", "MemTotal: 18446744073709551615 kB\nMemAvailable: 1 kB", "MemTotal: 1024 kB\nMemAvailable: 1 kB\nMemAvailable: 2 kB"} {
		if parseLinuxMemory(input).Available {
			t.Fatalf("invalid memory input accepted: %q", input)
		}
	}
}

func TestSystemMetricsLinuxReleaseAndDiskAccounting(t *testing.T) {
	for input, want := range map[string]string{
		"NAME=Ubuntu\nPRETTY_NAME=\"Ubuntu 24.04.3 LTS\"\n": "Ubuntu 24.04.3 LTS",
		"PRETTY_NAME='CentOS Stream 9'":                     "CentOS Stream 9",
		"PRETTY_NAME=Debian":                                "Debian",
		"PRETTY_NAME=\"invalid":                             "",
		"NAME=Linux":                                        "",
	} {
		if got := linuxRelease(input); got != want {
			t.Fatalf("release got=%q want=%q", got, want)
		}
	}
	got := diskUsage("/state", 1000, 300, 200)
	if !got.Available || got.UsedBytes != 700 || got.FreeBytes != 200 || got.UsagePercent != 70 {
		t.Fatalf("reserved space should remain separate from user-available space: %+v", got)
	}
	for _, input := range [][3]uint64{{0, 0, 0}, {100, 110, 80}, {100, 50, 80}} {
		if diskUsage("/state", input[0], input[1], input[2]).Available {
			t.Fatalf("invalid disk counters accepted: %v", input)
		}
	}
}

func TestSystemMetricsStatusRequiresLoginAndOptIn(t *testing.T) {
	_, browser := setup(t)
	browser.call("GET", "/status?system=1", nil, 401)
	browser.login()
	ordinary := dataOf(t, browser.call("GET", "/status", nil, 200))
	if _, present := ordinary["system"]; present || !browser.h.systemCachedAt.IsZero() {
		t.Fatal("ordinary status unexpectedly collected system metrics")
	}
	if ordinary["status"] != "UP" || ordinary["heapBytes"] == nil || ordinary["listen"] != "127.0.0.1:19090" {
		t.Fatal("existing status fields changed")
	}
	withMetrics := dataOf(t, browser.call("GET", "/status?system=1", nil, 200))
	system, ok := withMetrics["system"].(map[string]any)
	if !ok || system["hostname"] == nil || system["observedAt"] == nil || system["arch"] != runtime.GOARCH {
		t.Fatal("system snapshot missing")
	}
	if _, present := dataOf(t, browser.call("GET", "/status?system=0", nil, 200))["system"]; present {
		t.Fatal("system monitoring opt-in ignored")
	}
}

func TestSystemMetricsNativeSnapshotAndIndependentFailures(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("native system counters supported on Windows and Linux")
	}
	path := t.TempDir()
	info, counter := collectSystemMetrics(path)
	if !counter.valid || info.CPU.Cores < 1 || info.Hostname == "" || info.PlatformVersion == "" || !info.Memory.Available || info.Memory.TotalBytes == 0 || !info.Disk.Available || info.Disk.TotalBytes == 0 {
		t.Fatalf("native system information incomplete: %+v cpu=%+v", info, counter)
	}
	if info.Memory.UsedBytes > info.Memory.TotalBytes || info.Disk.UsedBytes > info.Disk.TotalBytes {
		t.Fatal("invalid native capacity counters")
	}
	missing, counter := collectSystemMetrics(filepath.Join(path, "does-not-exist"))
	if missing.Disk.Available || !missing.Memory.Available || !counter.valid {
		t.Fatal("one unavailable filesystem disabled unrelated metrics")
	}
	t.Logf("native system=%s cpu=%s cores=%d memory=%.2f%% disk=%.2f%%", info.PlatformVersion, info.CPU.Model, info.CPU.Cores, info.Memory.UsagePercent, info.Disk.UsagePercent)
}

func TestSystemMetricsCacheSharesSnapshotWithoutCrossControllerState(t *testing.T) {
	h := &HTTP{store: &Store{Dir: t.TempDir()}}
	first := h.systemSnapshot()
	if first.CPU.UsagePercent != nil {
		t.Fatal("first sample must not claim a CPU percentage")
	}
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			if h.systemSnapshot().ObservedAt != first.ObservedAt {
				t.Error("concurrent reads bypassed snapshot cache")
			}
		})
	}
	group.Wait()
	if other := (&HTTP{store: h.store}).systemSnapshot(); other.CPU.UsagePercent != nil {
		t.Fatal("another HTTP instance inherited the CPU baseline")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return
	}
	h.systemCachedAt = time.Now().Add(-time.Minute)
	if resumed := h.systemSnapshot(); resumed.CPU.UsagePercent != nil || time.Since(h.systemCachedAt) >= time.Second {
		t.Fatal("a long sampling gap must start a fresh baseline")
	}
	time.Sleep(25 * time.Millisecond)
	h.systemCachedAt = time.Now().Add(-3 * time.Second)
	next := h.systemSnapshot()
	if next.ObservedAt == first.ObservedAt || next.CPU.UsagePercent == nil || math.IsNaN(*next.CPU.UsagePercent) || *next.CPU.UsagePercent < 0 || *next.CPU.UsagePercent > 100 {
		t.Fatalf("second native sample invalid: %+v", next.CPU)
	}
}
