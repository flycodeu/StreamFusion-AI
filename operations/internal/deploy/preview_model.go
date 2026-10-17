package deploy

import (
	"errors"
	"net"
	"regexp"
	"strconv"
)

const PreviewRecipe = "preview-node-v1"
const PreviewArchiveLimit int64 = 2 << 30

// Only fields consumed by the preview appliance are configurable. Ports, mounts,
// commands, capabilities and credential paths belong to the fixed recipe.
type PreviewConfig struct {
	NodeID          string   `json:"nodeId"`
	ControlHost     string   `json:"controlHost"`
	MediaAddress    string   `json:"mediaAddress"`
	AllowedTargets  []string `json:"allowedTargets"`
	CPUs            int      `json:"cpus"`
	MaxSources      int      `json:"maxSources"`
	MaxViewers      int      `json:"maxViewers"`
	MaxTranscodes   int      `json:"maxTranscodes"`
	MaxStarting     int      `json:"maxStarting"`
	IngressKbps     int64    `json:"ingressKbps"`
	EgressKbps      int64    `json:"egressKbps"`
	SourceUpperKbps int64    `json:"sourceUpperKbps"`
	OutputUpperKbps int64    `json:"outputUpperKbps"`
}

func (c Config) Recipe() string {
	if c.Preview != nil {
		return PreviewRecipe
	}
	return "java-compose-v1"
}

func ArchiveLimitFor(recipe string) int64 {
	if recipe == PreviewRecipe {
		return PreviewArchiveLimit
	}
	return ArchiveLimit
}

func (c Config) validatePreview() error {
	p := c.Preview
	ip, media := net.ParseIP(c.BindAddress), net.ParseIP(p.MediaAddress)
	if c.InitialPasswordCredentialID != "" || c.InitialPasswordRevision != "" || len(c.BindingIDs) != 0 || c.Profile != "" || c.Environment != "preview" || c.HostPort != 9443 || c.ContainerPort != 9443 || c.HealthPath != "/preview-control/v1/snapshot" || c.HealthSeconds < 10 || c.HealthSeconds > 120 || c.MemoryMiB < 512 || c.MemoryMiB > 65536 || c.DataCompatibility != "preview-ownership-v1" || ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() || media == nil || media.To4() == nil || media.IsUnspecified() || media.IsMulticast() || !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(p.NodeID) || p.NodeID == "local" || !regexp.MustCompile(`^[a-zA-Z0-9.-]{1,253}$`).MatchString(p.ControlHost) || p.CPUs < 1 || p.CPUs > 64 || len(p.AllowedTargets) < 1 || len(p.AllowedTargets) > 256 {
		return errors.New("PREVIEW_CONFIG_INVALID")
	}
	if p.MaxSources < 1 || p.MaxSources > 64 || p.MaxViewers < 1 || p.MaxViewers > 64 || p.MaxTranscodes < 0 || p.MaxTranscodes > 64 || p.MaxStarting < 1 || p.MaxStarting > 4 || p.SourceUpperKbps < 1 || p.SourceUpperKbps > p.OutputUpperKbps || p.OutputUpperKbps > 1000000 || p.IngressKbps < 1 || p.IngressKbps > 100000000 || p.EgressKbps < 1 || p.EgressKbps > 100000000 {
		return errors.New("PREVIEW_BUDGET_INVALID")
	}
	seen := map[string]bool{}
	for _, endpoint := range p.AllowedTargets {
		host, port, err := net.SplitHostPort(endpoint)
		parsed := net.ParseIP(host)
		number, pe := strconv.Atoi(port)
		if err != nil || pe != nil || parsed == nil || parsed.IsUnspecified() || parsed.IsMulticast() || number < 1 || number > 65535 || seen[endpoint] {
			return errors.New("PREVIEW_SOURCE_POLICY_INVALID")
		}
		seen[endpoint] = true
	}
	return nil
}

func ValidatePreviewTransition(candidate, baseline *Release, permit string) error {
	if candidate == nil {
		return errors.New("RELEASE_INVALID")
	}
	if baseline == nil {
		return nil
	}
	if candidate.Config.Recipe() != baseline.Config.Recipe() {
		return errors.New("RECIPE_CHANGE_FORBIDDEN")
	}
	if candidate.Config.Preview == nil {
		return nil
	}
	a, b := candidate.Config.Preview, baseline.Config.Preview
	if !ID.MatchString(permit) {
		return errors.New("PREVIEW_MAINTENANCE_PERMIT_REQUIRED")
	}
	if a.NodeID != b.NodeID || a.ControlHost != b.ControlHost || a.MediaAddress != b.MediaAddress || candidate.Config.BindAddress != baseline.Config.BindAddress {
		return errors.New("PREVIEW_IDENTITY_CHANGE_FORBIDDEN")
	}
	return nil
}
