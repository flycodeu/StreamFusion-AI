package projectinspect

import (
	"github.com/streamfusion-ai/operations/internal/projectconfig"
)

// Module reports source evidence and suggested settings without applying them.
type Module struct {
	Directory              string                         `json:"directory"`
	Name                   string                         `json:"name"`
	Kind                   string                         `json:"kind"`
	Environment            string                         `json:"environment"`
	StartCommand           string                         `json:"startCommand"`
	Evidence               []string                       `json:"evidence"`
	Dependencies           []string                       `json:"dependencies,omitempty"`
	BuildCommand           string                         `json:"buildCommand,omitempty"`
	TestCommand            string                         `json:"testCommand,omitempty"`
	ArtifactPath           string                         `json:"artifactPath,omitempty"`
	RecommendedRuntimeMode string                         `json:"recommendedRuntimeMode,omitempty"`
	DefaultProfile         string                         `json:"defaultProfile,omitempty"`
	Profiles               []string                       `json:"profiles,omitempty"`
	DefaultPort            int                            `json:"defaultPort,omitempty"`
	HealthPath             string                         `json:"healthPath,omitempty"`
	EnvHints               []projectconfig.EnvHint        `json:"envHints,omitempty"`
	ProxyHints             []projectconfig.ProxyHint      `json:"proxyHints,omitempty"`
	ConfigTemplates        []projectconfig.ConfigTemplate `json:"configTemplates,omitempty"`
	SupportsContainer      bool                           `json:"supportsContainer,omitempty"`
}
