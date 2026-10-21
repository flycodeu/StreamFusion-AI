package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const softwareDiscoveryLimit = 100

type SoftwareDiscovery struct {
	Candidates     []SoftwareCandidate      `json:"candidates"`
	Configurations []SoftwareConfiguration  `json:"configurations"`
	Checks         []SoftwareDiscoveryCheck `json:"checks"`
	ObservedAt     string                   `json:"observedAt"`
	Limited        bool                     `json:"limited"`
}

type SoftwareCandidate struct {
	SoftwareMetadata
	Name         string   `json:"name"`
	SoftwareType string   `json:"softwareType"`
	RuntimeKind  string   `json:"runtimeKind"`
	Locator      string   `json:"locator"`
	State        string   `json:"state"`
	Version      string   `json:"version"`
	Ports        []string `json:"ports"`
	ConfigPaths  []string `json:"configPaths"`
}

// Configuration is host-level evidence from a fixed common configuration
// location. It is not attributed to an arbitrary unit or declared effective:
// command-line overrides and include files may change the running settings.
type SoftwareConfiguration struct {
	SoftwareType string `json:"softwareType"`
	Path         string `json:"path"`
	Port         int    `json:"port,omitempty"`
	BindAddress  string `json:"bindAddress,omitempty"`
}

type SoftwareDiscoveryCheck struct {
	Source string `json:"source"`
	Status string `json:"status"`
	Code   string `json:"code"`
}

// DiscoverSoftware inventories known software, never installs or adopts it.
// Each source has its own failure status; one unavailable source cannot erase
// successful observations from the others.
func DiscoverSoftware(ctx context.Context, run SoftwareCommandRunner) (*SoftwareDiscovery, error) {
	if run == nil {
		return nil, errors.New("SOFTWARE_OBSERVER_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	report := &SoftwareDiscovery{Candidates: []SoftwareCandidate{}, Configurations: []SoftwareConfiguration{}, Checks: []SoftwareDiscoveryCheck{}, ObservedAt: stamp()}
	seen := map[string]bool{}
	add := func(candidate SoftwareCandidate) {
		key := candidate.RuntimeKind + ":" + candidate.Locator
		if seen[key] {
			return
		}
		seen[key] = true
		if len(report.Candidates) >= softwareDiscoveryLimit {
			report.Limited = true
			return
		}
		if candidate.Ports == nil {
			candidate.Ports = []string{}
		}
		if candidate.ConfigPaths == nil {
			candidate.ConfigPaths = []string{}
		}
		report.Candidates = append(report.Candidates, candidate)
	}
	type sourceResult struct {
		index          int
		candidates     []SoftwareCandidate
		configurations []SoftwareConfiguration
		check          SoftwareDiscoveryCheck
		limited        bool
	}
	// Four bounded source plans share the existing SSH connection but use
	// separate sessions. A stalled Docker probe cannot consume the time budget
	// before native service/configuration discovery has even started.
	results := make(chan sourceResult, 4)
	go func() {
		rows, check, limited := discoverSystemd(ctx, run)
		results <- sourceResult{index: 0, candidates: rows, check: check, limited: limited}
	}()
	go func() {
		rows, check, limited := discoverDocker(ctx, run)
		results <- sourceResult{index: 1, candidates: rows, check: check, limited: limited}
	}()
	go func() {
		rows, check := discoverTools(ctx, run)
		results <- sourceResult{index: 2, candidates: rows, check: check, limited: check.Status == "UNAVAILABLE"}
	}()
	go func() {
		rows, check := discoverConfigurations(ctx, run)
		results <- sourceResult{index: 3, configurations: rows, check: check, limited: check.Status == "UNAVAILABLE" || check.Code == "CONFIG_PARTIAL_READ"}
	}()
	ordered := make([]sourceResult, 4)
	for range 4 {
		result := <-results
		ordered[result.index] = result
	}
	for _, result := range ordered {
		report.Checks = append(report.Checks, result.check)
		report.Limited = report.Limited || result.limited
		for _, candidate := range result.candidates {
			add(candidate)
		}
		if result.index == 3 {
			report.Configurations = result.configurations
		}
	}
	if ctx.Err() != nil {
		report.Limited = true
	}
	sort.Slice(report.Candidates, func(i, j int) bool {
		a, b := report.Candidates[i], report.Candidates[j]
		if a.SoftwareType != b.SoftwareType {
			return a.SoftwareType < b.SoftwareType
		}
		if a.RuntimeKind != b.RuntimeKind {
			return a.RuntimeKind < b.RuntimeKind
		}
		return a.Locator < b.Locator
	})
	// The combined report is bounded as well as each command response. Long
	// image references must not turn a 100-row cap into an oversized payload.
	for {
		encoded, _ := json.Marshal(report)
		if len(encoded) <= 64<<10 || len(report.Candidates) == 0 {
			break
		}
		report.Limited = true
		report.Candidates = report.Candidates[:len(report.Candidates)-1]
	}
	return report, nil
}

func discoveryCheck(source string, err error, count int) SoftwareDiscoveryCheck {
	if err != nil {
		code := "SOFTWARE_SOURCE_UNAVAILABLE"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "TIMEOUT") {
			code = "SOFTWARE_DISCOVERY_TIMEOUT"
		}
		if strings.Contains(err.Error(), "TOO_LARGE") {
			code = "SOFTWARE_DISCOVERY_OUTPUT_LIMIT"
		}
		return SoftwareDiscoveryCheck{Source: source, Status: "UNAVAILABLE", Code: code}
	}
	code := "SOFTWARE_SOURCE_AVAILABLE"
	if count == 0 {
		code = "NO_SUPPORTED_SOFTWARE_FOUND"
	}
	return SoftwareDiscoveryCheck{Source: source, Status: "AVAILABLE", Code: code}
}

func discoverSystemd(ctx context.Context, run SoftwareCommandRunner) ([]SoftwareCandidate, SoftwareDiscoveryCheck, bool) {
	installed, listErr := softwareCommand(ctx, run, "/usr/bin/systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager")
	active, stateErr := softwareCommand(ctx, run, "/usr/bin/systemctl", "list-units", "--all", "--type=service", "--no-legend", "--no-pager", "--plain")
	if listErr != nil {
		installed = nil
	}
	if stateErr != nil {
		active = nil
	}
	states := map[string]string{}
	validLines, invalidLines := 0, 0
	for _, line := range strings.Split(string(active), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") || !knownSystemdLoadState(fields[1]) || !knownSystemdActiveState(fields[2]) {
			invalidLines++
			continue
		}
		validLines++
		if (SoftwareRequest{Kind: "SYSTEMD", Locator: fields[0]}).Validate(true) != nil {
			continue
		}
		state := "UNKNOWN"
		switch fields[2] {
		case "active":
			state = "RUNNING"
		case "inactive":
			state = "STOPPED"
		case "failed":
			state = "FAILED"
		}
		states[fields[0]] = state
	}
	rows := []SoftwareCandidate{}
	seen := map[string]bool{}
	for sourceIndex, data := range [][]byte{installed, active} {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if sourceIndex == 0 {
				if len(fields) < 2 || !strings.HasSuffix(fields[0], ".service") || !knownSystemdFileState(fields[1]) {
					invalidLines++
					continue
				}
				validLines++
			} else if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") || !knownSystemdLoadState(fields[1]) || !knownSystemdActiveState(fields[2]) {
				continue
			}
			unit := fields[0]
			if fields[1] == "alias" || seen[unit] || (SoftwareRequest{Kind: "SYSTEMD", Locator: unit}).Validate(true) != nil {
				continue
			}
			kind := knownSoftwareName(strings.TrimSuffix(unit, ".service"))
			if kind == "" {
				continue
			}
			seen[unit] = true
			state := "INSTALLED"
			if value, ok := states[unit]; ok {
				state = value
			}
			rows = append(rows, SoftwareCandidate{Name: unit, SoftwareType: kind, RuntimeKind: "SYSTEMD", Locator: unit, State: state})
		}
	}
	err := listErr
	if err == nil {
		err = stateErr
	}
	// Package queries run in one bounded SSH session, capped separately from
	// the 100-row inventory. Losing enrichment never loses discovered units.
	units := []string{}
	for i := range rows {
		rows[i].VersionStatus = "METADATA_LIMIT"
		if len(units) < 24 {
			units = append(units, rows[i].Locator)
		}
	}
	metadata, metadataErr := readSoftwareUnitMetadata(ctx, units, run)
	for i := range rows {
		if item, ok := metadata[rows[i].Locator]; ok {
			rows[i].SoftwareMetadata, rows[i].Version = item.SoftwareMetadata, item.Version
		} else if i < 24 {
			rows[i].VersionStatus = "NO_PACKAGE_EVIDENCE"
			if metadataErr != nil {
				rows[i].VersionStatus = "METADATA_UNAVAILABLE"
			}
		}
	}
	check := discoveryCheck("SYSTEMD", err, len(rows))
	if err != nil && len(rows) > 0 {
		check.Status, check.Code = "AVAILABLE", "SOFTWARE_SOURCE_PARTIAL"
	}
	if invalidLines > 0 {
		check.Code = "SOFTWARE_SOURCE_PARTIAL"
		if validLines == 0 {
			check.Status, check.Code = "UNAVAILABLE", "SOFTWARE_SOURCE_RESPONSE_INVALID"
		}
	}
	return rows, check, err != nil || invalidLines > 0
}

func knownSystemdLoadState(state string) bool {
	switch state {
	case "loaded", "not-found", "bad-setting", "error", "masked", "stub", "merged":
		return true
	}
	return false
}

func knownSystemdFileState(state string) bool {
	switch state {
	case "enabled", "enabled-runtime", "disabled", "static", "alias", "masked", "masked-runtime", "indirect", "generated", "transient", "linked", "linked-runtime", "bad":
		return true
	}
	return false
}

func knownSystemdActiveState(state string) bool {
	switch state {
	case "active", "reloading", "inactive", "failed", "activating", "deactivating", "maintenance", "refreshing":
		return true
	}
	return false
}

var discoveryImageReference = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:-]{0,511}(?:@sha256:[a-f0-9]{64})?$`)
var discoveryPort = regexp.MustCompile(`^(?:(?:[0-9.]+|\[[a-fA-F0-9:]+\]):)?[0-9]{1,5}(?:-[0-9]{1,5})?(?:->[0-9]{1,5}(?:-[0-9]{1,5})?)?/(?:tcp|udp|sctp)$`)

func discoverDocker(ctx context.Context, run SoftwareCommandRunner) ([]SoftwareCandidate, SoftwareDiscoveryCheck, bool) {
	data, err := softwareCommand(ctx, run, "/usr/bin/docker", "ps", "-a", "--no-trunc", "--format", `{"id":{{json .ID}},"name":{{json .Names}},"image":{{json .Image}},"state":{{json .State}},"ports":{{json .Ports}}}`)
	if err != nil {
		return nil, discoveryCheck("DOCKER", err, 0), true
	}
	rows := []SoftwareCandidate{}
	invalid := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var container struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Image string `json:"image"`
			State string `json:"state"`
			Ports string `json:"ports"`
		}
		if json.Unmarshal([]byte(line), &container) != nil || !Hex.MatchString(container.ID) || !softwareContainerPattern.MatchString(container.Name) {
			invalid = true
			continue
		}
		kind := softwareImageType(container.Image)
		if kind == "" {
			continue
		}
		if strings.HasPrefix(container.Name, "sfops-") || strings.Contains(strings.ToLower(container.Name), "streamfusion-ops") {
			continue
		}
		state := "UNKNOWN"
		switch container.State {
		case "running":
			state = "RUNNING"
		case "created", "exited":
			state = "STOPPED"
		case "dead":
			state = "FAILED"
		}
		ports := []string{}
		for _, port := range strings.Split(container.Ports, ",") {
			port = strings.TrimSpace(port)
			if validDiscoveryPort(port) {
				ports = append(ports, port)
			}
		}
		rows = append(rows, SoftwareCandidate{SoftwareMetadata: SoftwareMetadata{VersionSource: "IMAGE_REFERENCE", VersionStatus: "IMAGE_ONLY"}, Name: container.Name, SoftwareType: kind, RuntimeKind: "DOCKER", Locator: container.ID, State: state, Version: container.Image, Ports: ports})
	}
	check := discoveryCheck("DOCKER", nil, len(rows))
	if invalid {
		check.Code = "SOFTWARE_SOURCE_PARTIAL"
	}
	return rows, check, invalid
}

func validDiscoveryPort(value string) bool {
	if !discoveryPort.MatchString(value) {
		return false
	}
	endpoint := strings.SplitN(value, "/", 2)[0]
	if colon := strings.LastIndex(endpoint, ":"); colon >= 0 {
		host := strings.Trim(endpoint[:colon], "[]")
		if net.ParseIP(host) == nil {
			return false
		}
		endpoint = endpoint[colon+1:]
	}
	for _, side := range strings.Split(endpoint, "->") {
		ports := strings.Split(side, "-")
		previous := 0
		for _, raw := range ports {
			port, err := strconv.Atoi(raw)
			if err != nil || port < 1 || port > 65535 || port < previous {
				return false
			}
			previous = port
		}
	}
	return true
}

func softwareImageType(image string) string {
	if !discoveryImageReference.MatchString(image) || strings.Contains(image, "://") {
		return ""
	}
	name := strings.SplitN(image, "@", 2)[0]
	name = path.Base(name)
	name = strings.SplitN(name, ":", 2)[0]
	return knownSoftwareName(name)
}

func knownSoftwareName(name string) string {
	switch strings.ToLower(name) {
	case "mysql", "mysqld", "mariadb":
		return "MYSQL"
	case "redis", "redis-server":
		return "REDIS"
	case "zlm", "zlmediakit", "mediaserver":
		return "ZLM"
	case "mediamtx", "rtsp-simple-server":
		return "MEDIAMTX"
	case "minio":
		return "MINIO"
	case "seaweedfs", "weed":
		return "SEAWEEDFS"
	case "nginx":
		return "NGINX"
	case "jenkins":
		return "JENKINS"
	case "java", "openjdk", "eclipse-temurin", "amazoncorretto":
		return "JAVA"
	case "python", "python3":
		return "PYTHON"
	case "ffmpeg":
		return "FFMPEG"
	}
	return ""
}

func discoverTools(ctx context.Context, run SoftwareCommandRunner) ([]SoftwareCandidate, SoftwareDiscoveryCheck) {
	rows := []SoftwareCandidate{}
	for _, key := range []string{"java", "python", "ffmpeg"} {
		actual, err := ObserveSoftware(ctx, SoftwareRequest{Kind: "TOOL", Locator: key}, run)
		if err != nil {
			continue
		}
		rows = append(rows, SoftwareCandidate{SoftwareMetadata: SoftwareMetadata{VersionSource: "VERSION_COMMAND"}, Name: key, SoftwareType: knownSoftwareName(key), RuntimeKind: "TOOL", Locator: key, State: actual.State, Version: RedactSoftwareLogs(actual.Version)})
	}
	check := discoveryCheck("TOOLS", nil, len(rows))
	if len(rows) == 0 {
		check.Status, check.Code = "UNAVAILABLE", "SOFTWARE_TOOLS_NOT_FOUND_OR_UNREADABLE"
	}
	if ctx.Err() != nil {
		check = discoveryCheck("TOOLS", ctx.Err(), len(rows))
	}
	return rows, check
}

var commonSoftwareConfiguration = map[string]string{
	"/etc/mysql/my.cnf":                       "MYSQL",
	"/etc/mysql/mysql.conf.d/mysqld.cnf":      "MYSQL",
	"/etc/mysql/mariadb.conf.d/50-server.cnf": "MYSQL",
	"/etc/my.cnf":                             "MYSQL",
	"/etc/redis/redis.conf":                   "REDIS",
	"/etc/redis.conf":                         "REDIS",
}

// The script and paths are compile-time literals. awk emits only a port and
// numeric binding addresses; passwords, includes, commands and full lines never
// cross the SSH channel. Go validates every emitted value a second time.
const softwareConfigurationScript = `
for file in /etc/mysql/my.cnf /etc/mysql/mysql.conf.d/mysqld.cnf /etc/mysql/mariadb.conf.d/50-server.cnf /etc/my.cnf /etc/redis/redis.conf /etc/redis.conf; do
  if [ ! -e "$file" ]; then printf 'MISSING\t%s\n' "$file"; continue; fi
  if [ ! -f "$file" ] || [ ! -r "$file" ]; then printf 'DENIED\t%s\n' "$file"; continue; fi
  size=$(/usr/bin/stat -L -c %s -- "$file" 2>/dev/null)
  case "$size" in ''|*[!0-9]*) printf 'DENIED\t%s\n' "$file"; continue;; esac
  if [ "${#size}" -gt 7 ] || [ "$size" -gt 1048576 ]; then printf 'LIMIT\t%s\n' "$file"; continue; fi
  case "$file" in /etc/redis*) kind=REDIS;; *) kind=MYSQL;; esac
  printf 'FOUND\t%s\n' "$file"
  /usr/bin/awk -v kind="$kind" -v file="$file" '
    /^[[:space:]]*[#;]/ {next}
    /^[[:space:]]*\[/ {section=$0; sub(/[[:space:]]*[#;].*$/,"",section); gsub(/[[:space:]]/,"",section); next}
    { line=$0; sub(/[[:space:]]*[#;].*$/,"",line)
      if (kind=="MYSQL") {
        if(section!="[mysqld]") next
        if(line ~ /^[[:space:]]*port[[:space:]]*=/) { sub(/^[^=]*=[[:space:]]*/,"",line); gsub(/[[:space:]"\047]/,"",line); if(line ~ /^[0-9]+$/ && line+0>=1 && line+0<=65535) printf "PORT\t%s\t%s\n",file,line }
        if(line ~ /^[[:space:]]*bind-address[[:space:]]*=/) { sub(/^[^=]*=[[:space:]]*/,"",line); gsub(/[[:space:]"\047]/,"",line); if(line ~ /^[0-9a-fA-F:.*,-]+$/) printf "BIND\t%s\t%s\n",file,line }
      } else {
        if(line ~ /^[[:space:]]*port[[:space:]]+/) { sub(/^[[:space:]]*port[[:space:]]+/,"",line); gsub(/[[:space:]]/,"",line); if(line ~ /^[0-9]+$/ && line+0>=1 && line+0<=65535) printf "PORT\t%s\t%s\n",file,line }
        if(line ~ /^[[:space:]]*bind[[:space:]]+/) { sub(/^[[:space:]]*bind[[:space:]]+/,"",line); gsub(/[[:space:]]+/,",",line); if(line ~ /^[0-9a-fA-F:.*,-]+$/) printf "BIND\t%s\t%s\n",file,line }
      }
    }' "$file" || printf 'DENIED\t%s\n' "$file"
done
`

func discoverConfigurations(ctx context.Context, run SoftwareCommandRunner) ([]SoftwareConfiguration, SoftwareDiscoveryCheck) {
	data, err := softwareCommand(ctx, run, "/bin/sh", "-c", softwareConfigurationScript)
	if err != nil {
		return []SoftwareConfiguration{}, discoveryCheck("CONFIG", err, 0)
	}
	configs := map[string]*SoftwareConfiguration{}
	blocked := map[string]bool{}
	denied, missing := false, 0
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		file := fields[1]
		kind, known := commonSoftwareConfiguration[file]
		if !known {
			continue
		}
		switch fields[0] {
		case "MISSING":
			missing++
		case "DENIED", "LIMIT":
			denied = true
			delete(configs, file)
			blocked[file] = true
		case "FOUND":
			if blocked[file] {
				continue
			}
			configs[file] = &SoftwareConfiguration{SoftwareType: kind, Path: file}
		case "PORT":
			if len(fields) != 3 || configs[file] == nil || blocked[file] {
				continue
			}
			port, e := strconv.Atoi(fields[2])
			if e == nil && port > 0 && port <= 65535 {
				configs[file].Port = port
			}
		case "BIND":
			if len(fields) != 3 || configs[file] == nil || blocked[file] || len(fields[2]) > 256 {
				continue
			}
			values := []string{}
			for _, value := range strings.Split(fields[2], ",") {
				value = strings.TrimPrefix(value, "-")
				if net.ParseIP(value) != nil || value == "*" {
					values = append(values, value)
				}
			}
			configs[file].BindAddress = strings.Join(values, ", ")
		}
	}
	rows := []SoftwareConfiguration{}
	for _, config := range configs {
		rows = append(rows, *config)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	check := discoveryCheck("CONFIG", nil, len(rows))
	if len(rows) == 0 {
		check.Code = "NO_SUPPORTED_CONFIG_FOUND"
	}
	if denied {
		check.Code = "CONFIG_PARTIAL_READ"
		if len(rows) == 0 {
			check.Status, check.Code = "UNAVAILABLE", "CONFIG_READ_DENIED"
		}
	}
	if missing == 0 && len(rows) == 0 && !denied {
		check.Status, check.Code = "UNAVAILABLE", "CONFIG_RESPONSE_INVALID"
	}
	return rows, check
}
