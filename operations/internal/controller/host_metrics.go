package controller

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/netip"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This probe reads Linux procfs and the root filesystem only. It neither uses
// sudo nor interpolates host input. Both samples share one SSH session.
const hostMetricsCommand = "/bin/sh -s"
const hostMetricsOutputLimit = 32 << 10

var hostMetricsScript = controllerScript("observe/host-metrics.sh")

type hostNetworkMetric struct {
	Name                  string   `json:"name"`
	Kind                  string   `json:"kind"`
	Addresses             []string `json:"addresses,omitempty"`
	ReceivedBytes         float64  `json:"receivedBytes"`
	SentBytes             float64  `json:"sentBytes"`
	ReceiveBytesPerSecond *float64 `json:"receiveBytesPerSecond,omitempty"`
	SendBytesPerSecond    *float64 `json:"sendBytesPerSecond,omitempty"`
}

type hostProcessMetric struct {
	PID         int     `json:"pid"`
	User        string  `json:"user"`
	CPUPercent  float64 `json:"cpuPercent"`
	MemoryBytes float64 `json:"memoryBytes"`
	Name        string  `json:"name"`
}

type hostMetrics struct {
	Code                 string              `json:"code"`
	ObservedAt           string              `json:"observedAt"`
	Hostname             string              `json:"hostname"`
	OS                   string              `json:"os"`
	Kernel               string              `json:"kernel"`
	Arch                 string              `json:"arch,omitempty"`
	BootTime             string              `json:"bootTime,omitempty"`
	CPUModel             string              `json:"cpuModel"`
	CPUCores             int                 `json:"cpuCores"`
	CPUPercent           *float64            `json:"cpuPercent,omitempty"`
	UptimeSeconds        *float64            `json:"uptimeSeconds,omitempty"`
	MemoryTotalBytes     *float64            `json:"memoryTotalBytes,omitempty"`
	MemoryAvailableBytes *float64            `json:"memoryAvailableBytes,omitempty"`
	SwapTotalBytes       *float64            `json:"swapTotalBytes,omitempty"`
	SwapFreeBytes        *float64            `json:"swapFreeBytes,omitempty"`
	RootTotalBytes       *float64            `json:"rootTotalBytes,omitempty"`
	RootUsedBytes        *float64            `json:"rootUsedBytes,omitempty"`
	Load                 []float64           `json:"load,omitempty"`
	Network              []hostNetworkMetric `json:"network,omitempty"`
	PrimaryInterface     string              `json:"primaryInterface,omitempty"`
	ProcessCPUBasis      string              `json:"processCpuBasis,omitempty"`
	Processes            []hostProcessMetric `json:"processes,omitempty"`
}

func metricText(text string, limit int) bool {
	return text != "" && len(text) <= limit && strings.IndexFunc(text, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func metricInterface(name string) bool {
	return len(name) > 0 && len(name) <= 64 && strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:-@") == ""
}

func metricNumber(raw string) (float64, error) {
	n, e := strconv.ParseFloat(raw, 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1e18 {
		return 0, errors.New("HOST_METRIC_NUMBER_INVALID")
	}
	return n, nil
}

func parseHostMetrics(output string) (*hostMetrics, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(output) > hostMetricsOutputLimit || len(lines) < 5 || lines[0] != "SFOPS_METRICS_V1" {
		return nil, errors.New("HOST_METRICS_INVALID")
	}
	m := &hostMetrics{Code: "PARTIAL", ObservedAt: Now()}
	values := map[string][]float64{}
	physical := map[string]bool{}
	addresses := map[string][]string{}
	addressCount := 0
	processes := []hostProcessMetric{}
	processIDs := map[int]bool{}
	network := [2]map[string][]float64{{}, {}}
	for _, line := range lines[1:] {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 || len(line) > 1024 {
			continue
		}
		key, text := fields[0], fields[1]
		switch key {
		case "PHYSICAL":
			if metricInterface(text) && len(physical) < 64 {
				physical[text] = true
			}
		case "HOST":
			if metricText(text, 255) {
				m.Hostname = text
			}
		case "OS":
			if metricText(text, 256) {
				m.OS = text
			}
		case "KERNEL":
			if metricText(text, 128) {
				m.Kernel = text
			}
		case "MODEL":
			if metricText(text, 512) {
				m.CPUModel = text
			}
		case "ARCH":
			if metricText(text, 64) {
				m.Arch = text
			}
		case "PRIMARY":
			if metricInterface(text) {
				m.PrimaryInterface = text
			}
		case "NETADDR":
			if len(fields) != 3 || !metricInterface(text) || addressCount >= 64 || len(addresses[text]) >= 4 || len(fields[2]) > 64 {
				continue
			}
			prefix, err := netip.ParsePrefix(fields[2])
			if err != nil {
				continue
			}
			address := prefix.String()
			duplicate := false
			for _, existing := range addresses[text] {
				duplicate = duplicate || existing == address
			}
			if !duplicate {
				addresses[text] = append(addresses[text], address)
				addressCount++
			}
		case "PROCESS_CPU_BASIS":
			if text == "LIFETIME_AVERAGE" {
				m.ProcessCPUBasis = text
			}
		case "PROCESS":
			if len(fields) != 6 || len(processes) >= 20 || !metricText(fields[2], 64) || !metricText(fields[5], 128) {
				continue
			}
			pid, err := strconv.Atoi(text)
			cpu, cpuErr := metricNumber(fields[3])
			memory, memoryErr := metricNumber(fields[4])
			if err != nil || pid <= 0 || int64(pid) > 1<<32-1 || processIDs[pid] || cpuErr != nil || cpu > 6553600 || memoryErr != nil {
				continue
			}
			processIDs[pid] = true
			processes = append(processes, hostProcessMetric{PID: pid, User: fields[2], CPUPercent: cpu, MemoryBytes: memory, Name: fields[5]})
		default:
			numbers := fields[1:]
			isNetwork := key == "NET0" || key == "NET1"
			if isNetwork {
				numbers = fields[2:]
			}
			n := []float64{}
			for _, raw := range numbers {
				value, e := metricNumber(raw)
				if e != nil {
					n = nil
					break
				}
				n = append(n, value)
			}
			if len(n) == 0 {
				continue
			}
			if isNetwork {
				phase := int(key[3] - '0')
				if len(network[phase]) < 64 && len(n) == 2 && metricInterface(text) {
					network[phase][text] = n
				}
			} else {
				values[key] = n
			}
		}
	}
	if n := values["CORES"]; len(n) == 1 && n[0] >= 1 && n[0] <= 65536 && math.Trunc(n[0]) == n[0] {
		m.CPUCores = int(n[0])
	}
	if m.Hostname == "" || m.CPUCores == 0 {
		return nil, errors.New("HOST_METRICS_INCOMPLETE")
	}
	if n := values["TIME1"]; len(n) == 1 {
		m.UptimeSeconds = &n[0]
	}
	if n := values["BOOT"]; len(n) == 1 && n[0] >= 1 && n[0] <= 253402300799 && math.Trunc(n[0]) == n[0] {
		m.BootTime = time.Unix(int64(n[0]), 0).UTC().Format(time.RFC3339)
	}
	if a, b := values["CPU0"], values["CPU1"]; len(a) == 2 && len(b) == 2 && b[0] > a[0] && b[1] >= a[1] && b[1]-a[1] <= b[0]-a[0] {
		percent := 100 * (1 - (b[1]-a[1])/(b[0]-a[0]))
		m.CPUPercent = &percent
	}
	if total, available := values["MEMTOTAL"], values["MEMAVAILABLE"]; len(total) == 1 && len(available) == 1 && total[0] > 0 && available[0] <= total[0] {
		m.MemoryTotalBytes = &total[0]
		m.MemoryAvailableBytes = &available[0]
	}
	if total, free := values["SWAPTOTAL"], values["SWAPFREE"]; len(total) == 1 && len(free) == 1 && free[0] <= total[0] {
		m.SwapTotalBytes, m.SwapFreeBytes = &total[0], &free[0]
	}
	if m.ProcessCPUBasis == "LIFETIME_AVERAGE" {
		m.Processes = processes
	}
	if disk := values["DISK"]; len(disk) == 2 && disk[0] > 0 && disk[1] <= disk[0] {
		m.RootTotalBytes = &disk[0]
		m.RootUsedBytes = &disk[1]
	}
	if load := values["LOAD"]; len(load) == 3 {
		m.Load = load
	}
	for name, n := range network[1] {
		metric := hostNetworkMetric{Name: name, Addresses: addresses[name], ReceivedBytes: n[0], SentBytes: n[1], Kind: "VIRTUAL_OR_UNKNOWN"}
		if physical[name] {
			metric.Kind = "PHYSICAL"
		}
		old, start, end := network[0][name], values["TIME0"], values["TIME1"]
		if len(old) == 2 && len(start) == 1 && len(end) == 1 && end[0] > start[0] && end[0]-start[0] <= 10 && n[0] >= old[0] && n[1] >= old[1] {
			rx, tx := (n[0]-old[0])/(end[0]-start[0]), (n[1]-old[1])/(end[0]-start[0])
			metric.ReceiveBytesPerSecond, metric.SendBytesPerSecond = &rx, &tx
		}
		m.Network = append(m.Network, metric)
	}
	if _, exists := network[1][m.PrimaryInterface]; !exists {
		m.PrimaryInterface = ""
	}
	sort.Slice(m.Network, func(i, j int) bool {
		a, b := m.Network[i], m.Network[j]
		if (a.Kind == "PHYSICAL") != (b.Kind == "PHYSICAL") {
			return a.Kind == "PHYSICAL"
		}
		return a.Name < b.Name
	})
	if m.CPUPercent != nil && m.MemoryTotalBytes != nil && m.RootTotalBytes != nil {
		m.Code = "READY"
	}
	return m, nil
}

func localHostMetrics(ctx context.Context) *hostMetrics {
	if runtime.GOOS != "linux" {
		return &hostMetrics{Code: "PLATFORM_UNSUPPORTED", ObservedAt: Now(), OS: runtime.GOOS, CPUCores: runtime.NumCPU()}
	}
	out, errOut := &limitedOutput{limit: hostMetricsOutputLimit + 1}, &limitedOutput{limit: 1024}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(hostMetricsScript)
	cmd.Stdout, cmd.Stderr = out, errOut
	if cmd.Run() != nil {
		return &hostMetrics{Code: "PROBE_FAILED", ObservedAt: Now()}
	}
	m, e := parseHostMetrics(out.String())
	if e != nil {
		return &hostMetrics{Code: "RESPONSE_INVALID", ObservedAt: Now()}
	}
	return m
}

// GET is deliberately side-effect free. Collection is owned by the coordinator.
func (h *HTTP) readHostMetrics(w http.ResponseWriter, r *http.Request, id string) {
	t, e := h.store.readTarget(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	o, e := h.store.readHostObservation(t)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, o)
}
func (h *HTTP) probeHostMetrics(ctx context.Context, t target) *hostMetrics {
	if t.Kind == "LOCAL" {
		return localHostMetrics(ctx)
	}
	m := &hostMetrics{ObservedAt: Now()}
	client, closeClient, e := h.store.connectSSH(ctx, t)
	if e != nil {
		m.Code, _ = hostSSHError(e)
		return m
	}
	defer closeClient()
	out, errOut := &limitedOutput{limit: hostMetricsOutputLimit + 1}, &limitedOutput{limit: 1024}
	if runSSHCommand(client, hostMetricsCommand, strings.NewReader(hostMetricsScript), out, errOut) != nil {
		m.Code = "PROBE_FAILED"
		return m
	}
	parsed, e := parseHostMetrics(out.String())
	if e != nil {
		m.Code = "RESPONSE_INVALID"
		return m
	}
	return parsed
}
