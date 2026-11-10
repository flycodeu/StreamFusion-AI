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

// Package ownership of the selected unit is queried. Known Python services can
// also use the one launcher path in ExecStart to read their dist-info metadata;
// arguments stay on the host and no discovered executable or user script runs.
// Output is a tiny tab-separated record per unit; the complete unit is never
// returned. This works offline and does not request sudo.
var softwareUnitMetadataScript = deployScript("observe/label-studio-package.sh") + "\n" + deployScript("observe/software-unit-metadata.sh")

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
