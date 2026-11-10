//go:build !linux && !windows

package controller

func collectSystemMetrics(path string) (systemMetrics, cpuCounter) {
	return newSystemMetrics(path), cpuCounter{}
}
