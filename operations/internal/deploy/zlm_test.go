package deploy

import (
	"strings"
	"testing"
)

func zlmTestConfig() Config {
	return Config{
		Environment: "zlm", BindAddress: "10.75.216.216", HostPort: 18080,
		ContainerPort: 8080, MemoryMiB: 4096, HealthPath: "/index/api/getServerConfig",
		HealthSeconds: 30, DataCompatibility: ZLMRecipe,
		ZLM: &ZLMConfig{
			APIPort: 18080, RTCPort: 18000, MediaAddress: "10.75.216.216",
			PlatformSourceIPs: []string{"10.75.216.25"}, SecretCredentialID: "11111111-1111-4111-8111-111111111111",
			SecretRevision: "1", CPUs: 4, MaxSources: 64, MaxViewers: 64,
		},
	}
}

func TestZLMDirectRecipeBoundaries(t *testing.T) {
	good := zlmTestConfig()
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if good.Recipe() != ZLMRecipe || ArchiveLimitFor(ZLMRecipe) != PreviewArchiveLimit {
		t.Fatal("independent recipe was not selected")
	}
	baseline := &Release{Config: zlmTestConfig()}
	candidate := &Release{Config: zlmTestConfig()}
	candidate.Config.ZLM.APIPort++
	if ValidatePreviewTransition(candidate, baseline, "") == nil {
		t.Fatal("ZLM API endpoint changed in place")
	}
	candidate.Config.ZLM.APIPort--
	candidate.Config.ZLM.PlatformSourceIPs = []string{"10.75.216.26"}
	if err := ValidatePreviewTransition(candidate, baseline, ""); err != nil {
		t.Fatal("source policy update blocked", err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.ZLM.PlatformSourceIPs = nil },
		func(c *Config) { c.ZLM.PlatformSourceIPs = []string{"0.0.0.0"} },
		func(c *Config) { c.ZLM.PlatformSourceIPs = []string{"192.0.2.25", "192.0.2.25"} },
		func(c *Config) { c.ZLM.MaxSources = 65 },
		func(c *Config) { c.ZLM.MaxViewers = 65 },
		func(c *Config) { c.ZLM.APIPort = c.ZLM.RTCPort },
		func(c *Config) { c.InitialPasswordCredentialID = "secret" },
		func(c *Config) { c.Preview = &PreviewConfig{} },
		func(c *Config) { c.BindingIDs = []string{"secret"} },
	} {
		candidate := zlmTestConfig()
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("unsafe ZLM configuration accepted: %+v", candidate)
		}
	}
	ini := zlmINI("11111111-1111-4111-8111-111111111111", *good.ZLM, strings.Repeat("a", 64))
	if !strings.Contains(ini, "allow_ip_range=127.0.0.1,::1,10.75.216.25") ||
		!strings.Contains(ini, "externIP=10.75.216.216") ||
		!strings.Contains(ini, "port=18000") ||
		!strings.Contains(ini, "secret="+strings.Repeat("a", 64)) {
		t.Fatal("ZLM private configuration omitted required access or media settings")
	}
}
