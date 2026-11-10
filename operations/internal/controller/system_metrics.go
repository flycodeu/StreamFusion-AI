package controller

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type systemCPU struct {
	Cores        int      `json:"cores"`
	Model        string   `json:"model"`
	UsagePercent *float64 `json:"usagePercent"`
	Available    bool     `json:"available"`
}

type systemMemory struct {
	TotalBytes   uint64  `json:"totalBytes"`
	UsedBytes    uint64  `json:"usedBytes"`
	UsagePercent float64 `json:"usagePercent"`
	Available    bool    `json:"available"`
}

type systemDisk struct {
	Path         string  `json:"path"`
	TotalBytes   uint64  `json:"totalBytes"`
	UsedBytes    uint64  `json:"usedBytes"`
	FreeBytes    uint64  `json:"freeBytes"`
	UsagePercent float64 `json:"usagePercent"`
	Available    bool    `json:"available"`
}

type systemMetrics struct {
	Hostname        string       `json:"hostname"`
	Platform        string       `json:"platform"`
	PlatformVersion string       `json:"platformVersion"`
	Arch            string       `json:"arch"`
	CPU             systemCPU    `json:"cpu"`
	Memory          systemMemory `json:"memory"`
	Disk            systemDisk   `json:"disk"`
	ObservedAt      string       `json:"observedAt"`
}

type cpuCounter struct {
	total, idle uint64
	valid       bool
}

func newSystemMetrics(path string) systemMetrics {
	hostname, _ := os.Hostname()
	return systemMetrics{Hostname: hostname, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		CPU: systemCPU{Cores: runtime.NumCPU()}, Disk: systemDisk{Path: path}}
}

// Sampling is request-driven and shared by this controller's authenticated
// clients. Repeated reads within two seconds reuse the same snapshot.
func (h *HTTP) systemSnapshot() systemMetrics {
	h.systemMu.Lock()
	defer h.systemMu.Unlock()
	now := time.Now()
	if !h.systemCachedAt.IsZero() && now.Sub(h.systemCachedAt) < 2*time.Second {
		return h.systemCache
	}
	info, counter := collectSystemMetrics(h.store.Dir)
	if !h.systemCachedAt.IsZero() && now.Sub(h.systemCachedAt) <= 30*time.Second {
		info.CPU.UsagePercent = cpuUsage(h.systemCPU, counter)
	}
	info.CPU.Available = counter.valid
	h.systemCPU = counter
	info.ObservedAt = now.UTC().Format(time.RFC3339Nano)
	h.systemCache, h.systemCachedAt = info, now
	return info
}

func cpuUsage(before, after cpuCounter) *float64 {
	if !before.valid || !after.valid || after.total <= before.total || after.idle < before.idle {
		return nil
	}
	total, idle := after.total-before.total, after.idle-before.idle
	if idle > total {
		return nil
	}
	usage := 100 * float64(total-idle) / float64(total)
	return &usage
}

func memoryUsage(total, available uint64) systemMemory {
	if total == 0 || available > total {
		return systemMemory{}
	}
	return systemMemory{TotalBytes: total, UsedBytes: total - available,
		UsagePercent: 100 * float64(total-available) / float64(total), Available: true}
}

func diskUsage(path string, total, free, available uint64) systemDisk {
	if total == 0 || free > total || available > free {
		return systemDisk{Path: path}
	}
	return systemDisk{Path: path, TotalBytes: total, UsedBytes: total - free, FreeBytes: available,
		UsagePercent: 100 * float64(total-free) / float64(total), Available: true}
}

func parseLinuxCPU(data string) cpuCounter {
	fields := strings.Fields(strings.SplitN(data, "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuCounter{}
	}
	var counter cpuCounter
	// guest and guest_nice are already included in user and nice.
	for index := 1; index < len(fields) && index <= 8; index++ {
		value, err := strconv.ParseUint(fields[index], 10, 64)
		if err != nil || value > math.MaxUint64-counter.total {
			return cpuCounter{}
		}
		counter.total += value
		if index == 4 || index == 5 {
			counter.idle += value
		}
	}
	counter.valid = counter.total >= counter.idle
	return counter
}

func parseLinuxMemory(data string) systemMemory {
	values := make(map[string]uint64, 2)
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "MemTotal:" && fields[0] != "MemAvailable:" {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return systemMemory{}
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value > math.MaxUint64/1024 {
			return systemMemory{}
		}
		if _, exists := values[fields[0]]; exists {
			return systemMemory{}
		}
		values[fields[0]] = value * 1024
	}
	available, found := values["MemAvailable:"]
	if !found {
		return systemMemory{}
	}
	return memoryUsage(values["MemTotal:"], available)
}

func linuxRelease(data string) string {
	for line := range strings.SplitSeq(data, "\n") {
		value, found := strings.CutPrefix(line, "PRETTY_NAME=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "\"") {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return ""
			}
			return unquoted
		}
		if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			return value[1 : len(value)-1]
		}
		return value
	}
	return ""
}
