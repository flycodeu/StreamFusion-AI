package controller

import (
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func systemFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return ""
	}
	return string(data)
}

func collectSystemMetrics(path string) (systemMetrics, cpuCounter) {
	info := newSystemMetrics(path)
	info.PlatformVersion = linuxRelease(systemFile("/etc/os-release"))
	for line := range strings.SplitSeq(systemFile("/proc/cpuinfo"), "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && (strings.TrimSpace(key) == "model name" || strings.TrimSpace(key) == "Hardware" || strings.TrimSpace(key) == "Processor") {
			info.CPU.Model = strings.TrimSpace(value)
			break
		}
	}
	info.Memory = parseLinuxMemory(systemFile("/proc/meminfo"))
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err == nil && stat.Bsize > 0 && stat.Blocks <= ^uint64(0)/uint64(stat.Bsize) && stat.Bfree <= stat.Blocks && stat.Bavail <= stat.Bfree {
		blockSize := uint64(stat.Bsize)
		info.Disk = diskUsage(path, stat.Blocks*blockSize, stat.Bfree*blockSize, stat.Bavail*blockSize)
	}
	return info, parseLinuxCPU(systemFile("/proc/stat"))
}
