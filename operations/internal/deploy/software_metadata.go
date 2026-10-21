package deploy

import (
	"context"
	"errors"
	"net"
	"path"
	"regexp"
	"strings"
)

// Metadata is evidence about the installation, separate from lifecycle identity.
// A package version does not prove the version loaded by a running process.
type SoftwareMetadata struct {
	VersionSource  string   `json:"versionSource,omitempty"`
	VersionStatus  string   `json:"versionStatus,omitempty"`
	PackageName    string   `json:"packageName,omitempty"`
	ExecutablePath string   `json:"executablePath,omitempty"`
	ServiceFile    string   `json:"serviceFile,omitempty"`
	ListeningPorts []string `json:"listeningPorts,omitempty"`
}

type softwareUnitMetadata struct {
	SoftwareMetadata
	Version string
}

// Only package ownership of the selected unit is queried. No discovered
// executable, service arguments, configuration contents or user script is run.
// Output is a tiny tab-separated record per unit; the complete unit is never
// returned. This works offline and does not request sudo.
const softwareUnitMetadataScript = `
sockets=$(/usr/bin/timeout 2s /usr/bin/ss -H -lntup 2>/dev/null | /usr/bin/head -c 65536)
if [ "${#sockets}" -ge 65536 ]; then sockets=; fi
for unit do
  file=$(/usr/bin/systemctl show --no-pager --property=FragmentPath --value -- "$unit" 2>/dev/null)
  pid=$(/usr/bin/systemctl show --no-pager --property=MainPID --value -- "$unit" 2>/dev/null)
  exe=
  case "$pid" in ''|*[!0-9]*|0) ;; *) exe=$(/usr/bin/readlink -f -- "/proc/$pid/exe" 2>/dev/null);; esac
  printf 'FILE\t%s\t%s\n' "$unit" "$file"
  printf 'EXE\t%s\t%s\n' "$unit" "$exe"
  case "$pid" in ''|*[!0-9]*|0) ;; *)
    printf '%s\n' "$sockets" | /usr/bin/awk -v pid="$pid" -v unit="$unit" '
      $0 ~ "pid="pid"[,)]" && ($1=="tcp" || $1=="udp") {printf "PORT\t%s\t%s/%s\n",unit,$5,$1}'
  ;; esac
  case "$file" in /etc/systemd/system/*.service|/lib/systemd/system/*.service|/usr/lib/systemd/system/*.service) ;; *) continue;; esac
  if [ -x /usr/bin/dpkg-query ]; then
    owner=$(/usr/bin/dpkg-query -S -- "$file" 2>/dev/null)
    if [ -z "$owner" ]; then
      alternate=
      case "$file" in /usr/lib/systemd/*) alternate="/lib/${file#/usr/lib/}";; /lib/systemd/*) alternate="/usr/lib/${file#/lib/}";; esac
      if [ -n "$alternate" ] && [ "$(/usr/bin/readlink -f -- "$file" 2>/dev/null)" = "$(/usr/bin/readlink -f -- "$alternate" 2>/dev/null)" ]; then
        owner=$(/usr/bin/dpkg-query -S -- "$alternate" 2>/dev/null)
      fi
    fi
    owner=${owner%%: /*}
    case "$owner" in ''|*[!a-zA-Z0-9+:._-]*) continue;; esac
    /usr/bin/dpkg-query -W '-f=${binary:Package}\t${Version}\t${db:Status-Status}\n' -- "$owner" 2>/dev/null |
      /usr/bin/awk -F '\t' -v unit="$unit" '$3=="installed" {printf "PACKAGE\t%s\t%s\t%s\n",unit,$1,$2}'
  elif [ -x /usr/bin/rpm ]; then
    /usr/bin/rpm -qf --qf '%{NAME}\t%{VERSION}-%{RELEASE}\n' -- "$file" 2>/dev/null |
      /usr/bin/awk -F '\t' -v unit="$unit" 'NF==2 {printf "PACKAGE\t%s\t%s\t%s\n",unit,$1,$2}'
  fi
done
`

var softwarePackageValue = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9+:._~%-]{0,159}$`)

func readSoftwareUnitMetadata(ctx context.Context, units []string, run SoftwareCommandRunner) (map[string]softwareUnitMetadata, error) {
	if len(units) == 0 {
		return map[string]softwareUnitMetadata{}, nil
	}
	if len(units) > 24 {
		return nil, errors.New("SOFTWARE_METADATA_LIMIT")
	}
	allowed := map[string]bool{}
	for _, unit := range units {
		if (SoftwareRequest{Kind: "SYSTEMD", Locator: unit}).Validate(true) != nil {
			return nil, errors.New("SOFTWARE_UNIT_FORBIDDEN")
		}
		allowed[unit] = true
	}
	args := append([]string{"-c", softwareUnitMetadataScript, "software-metadata"}, units...)
	data, err := softwareCommand(ctx, run, "/bin/sh", args...)
	if err != nil {
		return nil, err
	}
	result := map[string]softwareUnitMetadata{}
	packages := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 || !allowed[parts[1]] {
			continue
		}
		unit := parts[1]
		item := result[unit]
		switch parts[0] {
		case "FILE":
			if len(parts) == 3 && softwareUnitFile(parts[2]) && strings.HasSuffix(parts[2], ".service") {
				item.ServiceFile = parts[2]
			}
		case "EXE":
			if len(parts) == 3 && softwareMetadataPath(parts[2]) {
				item.ExecutablePath = parts[2]
			}
		case "PACKAGE":
			if len(parts) != 4 || !softwarePackageValue.MatchString(parts[2]) || !softwarePackageValue.MatchString(parts[3]) || packages[unit] {
				continue
			}
			packages[unit] = true
			item.PackageName, item.Version = parts[2], parts[3]
			item.VersionSource, item.VersionStatus = "PACKAGE", "INSTALLED_PACKAGE"
		case "PORT":
			if len(parts) == 3 && len(item.ListeningPorts) < 32 && validSoftwareSocket(parts[2]) {
				if !containsSoftwareValue(item.ListeningPorts, parts[2]) {
					item.ListeningPorts = append(item.ListeningPorts, parts[2])
				}
			}
		}
		result[unit] = item
	}
	for unit, item := range result {
		if item.ServiceFile == "" {
			item.Version, item.VersionSource, item.VersionStatus, item.PackageName = "", "", "NO_PACKAGE_EVIDENCE", ""
		} else if item.Version == "" {
			item.VersionStatus = "NO_PACKAGE_EVIDENCE"
		}
		result[unit] = item
	}
	return result, nil
}

func containsSoftwareValue(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func validSoftwareSocket(value string) bool {
	address, protocol, ok := strings.Cut(value, "/")
	if !ok || protocol != "tcp" && protocol != "udp" {
		return false
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "*" && net.ParseIP(host) == nil {
		return false
	}
	return validDiscoveryPort(port + "/" + protocol)
}

func softwareMetadataPath(value string) bool {
	return strings.HasPrefix(value, "/") && path.Clean(value) == value && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n\t ") && !strings.HasPrefix(value, "/proc/")
}

// Enrichment is optional: missing package permissions must not turn a healthy
// service into UNKNOWN or change the frozen lifecycle binding.
func EnrichSoftwareObservation(ctx context.Context, actual *SoftwareObservation, run SoftwareCommandRunner) {
	if actual == nil || actual.Kind != "SYSTEMD" {
		return
	}
	rows, err := readSoftwareUnitMetadata(ctx, []string{actual.Locator}, run)
	if err != nil {
		actual.VersionStatus = "METADATA_UNAVAILABLE"
		return
	}
	metadata, ok := rows[actual.Locator]
	if !ok {
		actual.VersionStatus = "NO_PACKAGE_EVIDENCE"
		return
	}
	actual.SoftwareMetadata, actual.Version = metadata.SoftwareMetadata, metadata.Version
}
