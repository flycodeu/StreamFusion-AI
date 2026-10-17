package deploy

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"path"
	"regexp"
	"strings"
)

const Version = "0.2.0"
const ArchiveLimit int64 = 512 << 20

var Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var rootPattern = regexp.MustCompile(`^/(opt|srv|var/lib)/[a-zA-Z0-9_./-]+$`)
var profilePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{0,40}$`)

func ValidRoot(root string) bool {
	return rootPattern.MatchString(root) && path.Clean(root) == root && len(root) > 10 && !strings.Contains(root, "..")
}

type Config struct {
	Preview                     *PreviewConfig `json:"preview,omitempty"`
	InitialPasswordCredentialID string         `json:"initialPasswordCredentialId"`
	InitialPasswordRevision     string         `json:"initialPasswordRevision"`
	BindingIDs                  []string       `json:"bindingIds"`
	Environment                 string         `json:"environment"`
	Profile                     string         `json:"profile"`
	BindAddress                 string         `json:"bindAddress"`
	HostPort                    int            `json:"hostPort"`
	ContainerPort               int            `json:"containerPort"`
	MemoryMiB                   int            `json:"memoryMiB"`
	HealthPath                  string         `json:"healthPath"`
	HealthSeconds               int            `json:"healthSeconds"`
	DataCompatibility           string         `json:"dataCompatibility"`
}

func (c Config) Validate() error {
	if c.Preview != nil {
		return c.validatePreview()
	}
	if (c.InitialPasswordCredentialID == "") != (c.InitialPasswordRevision == "") || c.InitialPasswordCredentialID != "" && (!ID.MatchString(c.InitialPasswordCredentialID) || !regexp.MustCompile(`^[1-9][0-9]{0,3}$`).MatchString(c.InitialPasswordRevision)) {
		return errors.New("INITIAL_PASSWORD_REFERENCE_INVALID")
	}
	if len(c.BindingIDs) > 2 {
		return errors.New("BINDING_LIMIT")
	}
	for _, id := range c.BindingIDs {
		if !ID.MatchString(id) {
			return errors.New("BINDING_ID_INVALID")
		}
	}
	if len(c.BindingIDs) == 2 && c.BindingIDs[0] == c.BindingIDs[1] {
		return errors.New("BINDING_DUPLICATE")
	}
	ip := net.ParseIP(c.BindAddress)
	if !profilePattern.MatchString(c.Environment) || c.Environment == "" || !profilePattern.MatchString(c.Profile) || ip == nil || ip.To4() == nil || ip.IsUnspecified() || c.HostPort < 1024 || c.HostPort > 65535 || c.ContainerPort < 1024 || c.ContainerPort > 65535 || c.MemoryMiB < 256 || c.MemoryMiB > 8192 || c.HealthSeconds < 5 || c.HealthSeconds > 120 || !regexp.MustCompile(`^/[a-zA-Z0-9/_-]{0,100}$`).MatchString(c.HealthPath) || len(c.DataCompatibility) < 1 || len(c.DataCompatibility) > 80 {
		return errors.New("CONFIG_INVALID: 请核对环境、明确IPv4、非特权端口、内存、健康路径、超时与数据兼容标识")
	}
	return nil
}

type Release struct {
	ID            string    `json:"id"`
	InstanceID    string    `json:"instanceId"`
	ArtifactID    string    `json:"artifactId"`
	ArchiveSHA256 string    `json:"archiveSha256"`
	ByteSize      int64     `json:"byteSize"`
	ImageID       string    `json:"imageId"`
	Config        Config    `json:"config"`
	Bindings      []Binding `json:"bindings,omitempty"`
}
type Binding struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Database           string `json:"database"`
	Username           string `json:"username"`
	CredentialID       string `json:"credentialId"`
	CredentialRevision string `json:"credentialRevision"`
}

func (b Binding) Validate() error {
	if !ID.MatchString(b.ID) || (b.Kind != "MYSQL" && b.Kind != "REDIS") || !regexp.MustCompile(`^[a-zA-Z0-9.-]{1,253}$`).MatchString(b.Host) || b.Port < 1 || b.Port > 65535 || !regexp.MustCompile(`^[a-zA-Z0-9_-]{0,64}$`).MatchString(b.Database) || !regexp.MustCompile(`^[a-zA-Z0-9_@.-]{0,64}$`).MatchString(b.Username) || (b.CredentialID != "" && (!ID.MatchString(b.CredentialID) || !regexp.MustCompile(`^[1-9][0-9]{0,3}$`).MatchString(b.CredentialRevision))) {
		return errors.New("BINDING_INVALID")
	}
	if b.Kind == "MYSQL" && (b.Database == "" || b.Username == "") {
		return errors.New("MYSQL_BINDING_INCOMPLETE")
	}
	return nil
}

type Request struct {
	MaintenancePermit    string            `json:"maintenancePermit,omitempty"`
	ProtocolVersion      int               `json:"protocolVersion"`
	ExecutorVersion      string            `json:"executorVersion"`
	ControllerID         string            `json:"controllerId"`
	TargetID             string            `json:"targetId"`
	JobID                string            `json:"jobId"`
	TraceID              string            `json:"traceId"`
	Action               string            `json:"action"`
	InputHash            string            `json:"inputHash"`
	Candidate            *Release          `json:"candidate,omitempty"`
	Baseline             *Release          `json:"baseline,omitempty"`
	ResultHash           string            `json:"resultHash,omitempty"`
	DeadlineAt           string            `json:"deadlineAt,omitempty"`
	Binding              *Binding          `json:"binding,omitempty"`
	Plugin               *PluginRequest    `json:"plugin,omitempty"`
	PluginInstallationID string            `json:"pluginInstallationId,omitempty"`
	PluginOperation      string            `json:"pluginOperation,omitempty"`
	Secrets              map[string]string `json:"secrets,omitempty"`
}
type PluginRequest struct {
	Key            string `json:"key"`
	Version        string `json:"version"`
	Kind           string `json:"kind"`
	Platform       string `json:"platform"`
	Image          string `json:"image"`
	Digest         string `json:"digest"`
	RecipeRevision string `json:"recipeRevision"`
	Command        string `json:"command"`
	Argument       string `json:"argument"`
}
type Owner struct {
	ProtocolVersion int    `json:"protocolVersion"`
	ExecutorVersion string `json:"executorVersion"`
	ControllerID    string `json:"controllerId"`
	TargetID        string `json:"targetId"`
	DockerSocket    string `json:"dockerSocket"`
	DockerBinary    string `json:"dockerBinary"`
}
type Result struct {
	Preview           *PreviewObservation `json:"preview,omitempty"`
	ProtocolVersion   int                 `json:"protocolVersion"`
	ExecutorVersion   string              `json:"executorVersion"`
	ControllerID      string              `json:"controllerId"`
	TargetID          string              `json:"targetId"`
	JobID             string              `json:"jobId"`
	InputHash         string              `json:"inputHash"`
	Status            string              `json:"status"`
	Phase             string              `json:"phase"`
	ErrorCode         string              `json:"errorCode"`
	ReleaseID         string              `json:"releaseId"`
	ImageID           string              `json:"imageId"`
	ConfigHash        string              `json:"configHash"`
	ContainerID       string              `json:"containerId"`
	Health            string              `json:"health"`
	PluginKey         string              `json:"pluginKey,omitempty"`
	PluginVersion     string              `json:"pluginVersion,omitempty"`
	PluginKind        string              `json:"pluginKind,omitempty"`
	PluginImageDigest string              `json:"pluginImageDigest,omitempty"`
	ObservedVersion   string              `json:"observedVersion,omitempty"`
	Platform          string              `json:"platform,omitempty"`
	ObservedAt        string              `json:"observedAt"`
	Ack               bool                `json:"ack"`
}

func StrictJSON(data []byte, dst any) error {
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if d.Decode(new(any)) == io.EOF {
		return nil
	}
	return errors.New("JSON_TRAILING_DATA")
}
