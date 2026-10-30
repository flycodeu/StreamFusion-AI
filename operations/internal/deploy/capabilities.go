package deploy

import (
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"time"
)

func inspectExecutorCapabilities(owner Owner, platform string, command func(time.Duration, ...string) ([]byte, error)) map[string]any {
	_, engineErr := command(2*time.Second, "version", "--format", "{{.Server.Version}}")
	_, composeErr := command(2*time.Second, "compose", "version", "--short")
	return map[string]any{
		"owner":    owner,
		"platform": platform,
		// Existing deployment recipes require both Engine and Compose. Keep
		// this established field unchanged for F01 and installation preflight.
		"dockerAvailable": engineErr == nil && composeErr == nil,
		// Adopting an existing container needs only Engine, not Compose.
		"dockerEngineAvailable":    engineErr == nil,
		"dockerComposeAvailable":   composeErr == nil,
		"nativeInstallVersion":     1,
		"softwareConfigureVersion": 1,
		"softwareUninstallVersion": 1,
		"toolProbeVersion":         1,
		// F02 is independently gated: older 0.2.0 executors do not understand
		// software requests even though their original protocol still matches.
		"softwareLifecycleVersion": 1,
		"firewallVersion":          FirewallCapabilityVersion,
		"serviceInstallVersion":    serviceinstall.CapabilityVersion,
		"serviceCatalogHash":       serviceinstall.CatalogHash(),
	}
}
