package deploy

import (
	"errors"
	"net"
	"regexp"
)

const ZLMRecipe = "zlm-direct-v1"

// ZLMConfig describes one independently managed ZLM process. The API secret is
// a protected Ops credential reference, never part of an instance snapshot.
type ZLMConfig struct {
	APIPort            int      `json:"apiPort"`
	RTCPort            int      `json:"rtcPort"`
	MediaAddress       string   `json:"mediaAddress"`
	PlatformSourceIPs  []string `json:"platformSourceIps"`
	SecretCredentialID string   `json:"secretCredentialId"`
	SecretRevision     string   `json:"secretRevision"`
	CPUs               int      `json:"cpus"`
	MaxSources         int      `json:"maxSources"`
	MaxViewers         int      `json:"maxViewers"`
}

func (c Config) validateZLM() error {
	z := c.ZLM
	bind, media := net.ParseIP(c.BindAddress), net.ParseIP(z.MediaAddress)
	if c.Preview != nil || c.InitialPasswordCredentialID != "" || c.InitialPasswordRevision != "" || len(c.BindingIDs) != 0 || c.Profile != "" || c.Environment != "zlm" || c.HostPort != z.APIPort || c.ContainerPort != 8080 || c.HealthPath != "/index/api/getServerConfig" || c.HealthSeconds < 10 || c.HealthSeconds > 120 || c.MemoryMiB < 512 || c.MemoryMiB > 65536 || c.DataCompatibility != ZLMRecipe || bind == nil || bind.To4() == nil || bind.IsUnspecified() || bind.IsMulticast() || media == nil || media.To4() == nil || !media.Equal(bind) || media.IsUnspecified() || media.IsMulticast() || z.APIPort < 1024 || z.APIPort > 65535 || z.RTCPort < 1024 || z.RTCPort > 65535 || z.APIPort == z.RTCPort || z.CPUs < 1 || z.CPUs > 64 || z.MaxSources < 1 || z.MaxSources > 64 || z.MaxViewers < 1 || z.MaxViewers > 64 || !ID.MatchString(z.SecretCredentialID) || !regexp.MustCompile(`^[1-9][0-9]{0,3}$`).MatchString(z.SecretRevision) || len(z.PlatformSourceIPs) < 1 || len(z.PlatformSourceIPs) > 8 {
		return errors.New("ZLM_CONFIG_INVALID")
	}
	seen := map[string]bool{}
	for _, address := range z.PlatformSourceIPs {
		ip := net.ParseIP(address)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() || seen[address] {
			return errors.New("ZLM_PLATFORM_SOURCE_INVALID")
		}
		seen[address] = true
	}
	return nil
}
