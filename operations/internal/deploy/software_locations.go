package deploy

import (
	"context"
	"encoding/json"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Location enrichment runs with the existing observation, never while listing
// instances. Failure leaves lifecycle state and identity unchanged.
func EnrichSoftwareLocations(ctx context.Context, s SoftwareRequest, actual *SoftwareObservation, run SoftwareCommandRunner) {
	if actual == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	switch actual.Kind {
	case "DOCKER":
		actual.LocationStatus = "UNAVAILABLE"
		enrichContainerLocations(ctx, actual, run)
	case "SYSTEMD":
		actual.LocationStatus = "UNAVAILABLE"
		data, err := softwareCommand(ctx, run, "/usr/bin/systemctl", "show", "--no-pager", "--property=StandardOutput,StandardError", "--", actual.Locator)
		if err == nil {
			actual.LocationStatus = "AVAILABLE"
			for _, line := range strings.Split(string(data), "\n") {
				key, value, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				if key == "StandardOutput" || key == "StandardError" {
					if (value == "journal" || value == "journal+console") && actual.LogSource == "" {
						actual.LogSource = "journal"
					}
					prefix, file, fileOutput := strings.Cut(value, ":")
					if fileOutput && (prefix == "file" || prefix == "append" || prefix == "truncate") && validLocationPath(file) {
						actual.LogSource, actual.LogPath = "file", file
					}
				}
			}
		}
		if s.SoftwareKey == "DOCKER" && actual.Locator == "docker.service" {
			root, err := softwareCommand(ctx, run, "/usr/bin/docker", "info", "--format", "{{.DockerRootDir}}")
			if value := strings.TrimSpace(string(root)); err == nil && validLocationPath(value) {
				actual.DockerRootDir = value
			}
		}
	}
}

func validLocationPath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) == value && len(value) <= 1024 && !strings.ContainsAny(value, "\x00\r\n\t") && !strings.HasPrefix(value, "/proc/")
}

func enrichContainerLocations(ctx context.Context, actual *SoftwareObservation, run SoftwareCommandRunner) {
	// Project only the fields needed by the directory; inspect's environment,
	// command arguments, logging options and labels may contain credentials.
	format := `{"id":{{json .Id}},"imageId":{{json .Image}},"name":{{json .Name}},"mounts":{{json .Mounts}},"ports":{{json .NetworkSettings.Ports}},"logDriver":{{json .HostConfig.LogConfig.Type}},"logPath":{{json .LogPath}}}`
	data, err := softwareCommand(ctx, run, "/usr/bin/docker", "container", "inspect", "--format", format, "--", actual.Locator)
	if err != nil {
		return
	}
	var c struct {
		ID, ImageID, Name, LogDriver, LogPath string
		Mounts                                []struct {
			Source, Destination, Type, Name string
			RW                              bool
		}
		Ports map[string][]struct{ HostIP, HostPort string }
	}
	if json.Unmarshal(data, &c) != nil || c.ID != actual.Locator || Digest([]string{actual.Kind, c.ID, c.ImageID}) != actual.IdentityHash {
		return
	}
	actual.LocationStatus = "AVAILABLE"
	if name := strings.TrimPrefix(c.Name, "/"); softwareContainerPattern.MatchString(name) {
		actual.ContainerName = name
	}
	for _, mount := range c.Mounts {
		if len(actual.Mounts) >= 32 {
			break
		}
		if !validLocationPath(mount.Destination) {
			continue
		}
		entry := SoftwareMount{Destination: mount.Destination, ReadOnly: !mount.RW}
		if mount.Type == "volume" && softwareContainerPattern.MatchString(mount.Name) {
			entry.Volume = mount.Name
		}
		if validLocationPath(mount.Source) {
			entry.Source = mount.Source
		}
		actual.Mounts = append(actual.Mounts, entry)
	}
	targets := make([]string, 0, len(c.Ports))
	for target := range c.Ports {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		bindings := c.Ports[target]
		if !validDiscoveryPort(target) || len(bindings) > 32 {
			continue
		}
		for _, binding := range bindings {
			port, err := strconv.Atoi(binding.HostPort)
			if err != nil || port < 1 || port > 65535 || binding.HostIP != "" && net.ParseIP(binding.HostIP) == nil {
				continue
			}
			if len(actual.PortMappings) < 64 {
				actual.PortMappings = append(actual.PortMappings, net.JoinHostPort(binding.HostIP, binding.HostPort)+" → "+target)
			}
		}
	}
	sort.Strings(actual.PortMappings)
	if c.LogDriver != "" && len(c.LogDriver) <= 64 && !strings.ContainsAny(c.LogDriver, "\x00\r\n\t ") {
		actual.LogSource = "docker:" + c.LogDriver
	}
	if validLocationPath(c.LogPath) {
		actual.LogPath = c.LogPath
	}
}
