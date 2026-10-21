package controller

import (
	"context"
	"errors"
	"math"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// This probe reads Linux procfs and the root filesystem only. It neither uses
// sudo nor interpolates host input. Both samples share one SSH session.
const hostMetricsCommand = "/bin/sh -s"
const hostMetricsScript = `export LC_ALL=C
printf "SFOPS_METRICS_V1\n"
printf "HOST\t"; hostname
printf "KERNEL\t"; uname -r
awk -F= '/^PRETTY_NAME=/ {gsub(/"/,"",$2); print "OS\t" $2}' /etc/os-release 2>/dev/null
printf "CORES\t"; getconf _NPROCESSORS_ONLN
awk -F: '/^model name/ {sub(/^ +/,"",$2); print "MODEL\t" $2; exit}' /proc/cpuinfo
sample() {
  awk -v phase="$1" '/^cpu / {total=0; for(i=2;i<=9;i++) total+=$i; printf("CPU%s\t%.0f\t%.0f\n",phase,total,$5+$6)}' /proc/stat
  awk -v phase="$1" '{print "TIME" phase "\t" $1}' /proc/uptime
  awk -v phase="$1" 'NR>2 {sub(/^ +/,""); split($0,a,/[: ]+/); if(a[1]!="lo") print "NET" phase "\t" a[1] "\t" a[2] "\t" a[10]}' /proc/net/dev
}
for nic in /sys/class/net/*; do
  if [ -e "$nic/device" ]; then printf "PHYSICAL\t%s\n" "${nic##*/}"; fi
done
sample 0
sleep 1
sample 1
awk '/^MemTotal:/ {printf("MEMTOTAL\t%.0f\n",$2*1024)} /^MemAvailable:/ {printf("MEMAVAILABLE\t%.0f\n",$2*1024)}' /proc/meminfo
awk '{print "LOAD\t" $1 "\t" $2 "\t" $3}' /proc/loadavg
df -Pk / 2>/dev/null | awk 'NR==2 {printf("DISK\t%.0f\t%.0f\n",$2*1024,$3*1024)}'
`

type hostNetworkMetric struct {
	Name                  string   `json:"name"`
	Kind                  string   `json:"kind"`
	ReceivedBytes         float64  `json:"receivedBytes"`
	SentBytes             float64  `json:"sentBytes"`
	ReceiveBytesPerSecond *float64 `json:"receiveBytesPerSecond,omitempty"`
	SendBytesPerSecond    *float64 `json:"sendBytesPerSecond,omitempty"`
}

type hostMetrics struct {
	Code                 string              `json:"code"`
	ObservedAt           string              `json:"observedAt"`
	Hostname             string              `json:"hostname"`
	OS                   string              `json:"os"`
	Kernel               string              `json:"kernel"`
	CPUModel             string              `json:"cpuModel"`
	CPUCores             int                 `json:"cpuCores"`
	CPUPercent           *float64            `json:"cpuPercent,omitempty"`
	UptimeSeconds        *float64            `json:"uptimeSeconds,omitempty"`
	MemoryTotalBytes     *float64            `json:"memoryTotalBytes,omitempty"`
	MemoryAvailableBytes *float64            `json:"memoryAvailableBytes,omitempty"`
	RootTotalBytes       *float64            `json:"rootTotalBytes,omitempty"`
	RootUsedBytes        *float64            `json:"rootUsedBytes,omitempty"`
	Load                 []float64           `json:"load,omitempty"`
	Network              []hostNetworkMetric `json:"network,omitempty"`
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
	if len(output) > 16384 || len(lines) < 5 || lines[0] != "SFOPS_METRICS_V1" {
		return nil, errors.New("HOST_METRICS_INVALID")
	}
	m := &hostMetrics{Code: "PARTIAL", ObservedAt: Now()}
	values := map[string][]float64{}
	physical := map[string]bool{}
	network := [2]map[string][]float64{{}, {}}
	for _, line := range lines[1:] {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 || len(line) > 1024 {
			continue
		}
		key, text := fields[0], fields[1]
		switch key {
		case "PHYSICAL":
			physical[text] = true
		case "HOST":
			m.Hostname = text
		case "OS":
			m.OS = text
		case "KERNEL":
			m.Kernel = text
		case "MODEL":
			m.CPUModel = text
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
				if len(network[phase]) < 64 && len(n) == 2 && len(text) <= 64 {
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
	if a, b := values["CPU0"], values["CPU1"]; len(a) == 2 && len(b) == 2 && b[0] > a[0] && b[1] >= a[1] && b[1]-a[1] <= b[0]-a[0] {
		percent := 100 * (1 - (b[1]-a[1])/(b[0]-a[0]))
		m.CPUPercent = &percent
	}
	if total, available := values["MEMTOTAL"], values["MEMAVAILABLE"]; len(total) == 1 && len(available) == 1 && total[0] > 0 && available[0] <= total[0] {
		m.MemoryTotalBytes = &total[0]
		m.MemoryAvailableBytes = &available[0]
	}
	if disk := values["DISK"]; len(disk) == 2 && disk[0] > 0 && disk[1] <= disk[0] {
		m.RootTotalBytes = &disk[0]
		m.RootUsedBytes = &disk[1]
	}
	if load := values["LOAD"]; len(load) == 3 {
		m.Load = load
	}
	for name, n := range network[1] {
		metric := hostNetworkMetric{Name: name, ReceivedBytes: n[0], SentBytes: n[1], Kind: "VIRTUAL_OR_UNKNOWN"}
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
	out, errOut := &limitedOutput{limit: 16385}, &limitedOutput{limit: 1024}
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
	out, errOut := &limitedOutput{limit: 16385}, &limitedOutput{limit: 1024}
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
