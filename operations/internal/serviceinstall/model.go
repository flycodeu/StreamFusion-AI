package serviceinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

const ArchiveLimit int64 = 2 << 30
const CapabilityVersion = 2

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var sha = regexp.MustCompile(`^[a-f0-9]{64}$`)
var revision = regexp.MustCompile(`^[1-9][0-9]{0,5}$`)
var identifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}$`)
var storagePath = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

// Config contains only public options and immutable credential references.
// Passwords and S3 access keys are resolved into Request.Secrets at transport.
type Config struct {
	RuntimeMode                 string         `json:"runtimeMode,omitempty"`
	StorageRoot                 string         `json:"storageRoot,omitempty"`
	Ports                       map[string]int `json:"ports,omitempty"`
	Name                        string         `json:"name"`
	BindAddress                 string         `json:"bindAddress"`
	HostPort                    int            `json:"hostPort"`
	MemoryMiB                   int            `json:"memoryMiB"`
	CredentialID                string         `json:"credentialId,omitempty"`
	CredentialRevision          string         `json:"credentialRevision,omitempty"`
	AccessKeyCredentialID       string         `json:"accessKeyCredentialId,omitempty"`
	AccessKeyCredentialRevision string         `json:"accessKeyCredentialRevision,omitempty"`
	Database                    string         `json:"database,omitempty"`
	Username                    string         `json:"username,omitempty"`
	UserCredentialID            string         `json:"userCredentialId,omitempty"`
	UserCredentialRevision      string         `json:"userCredentialRevision,omitempty"`
}

type Source struct {
	Type     string `json:"type"`
	SHA256   string `json:"sha256,omitempty"`
	ByteSize int64  `json:"byteSize,omitempty"`
}

type Request struct {
	SoftwareID      string     `json:"softwareId"`
	BindingRevision int64      `json:"bindingRevision"`
	Definition      Definition `json:"definition"`
	Config          Config     `json:"config"`
	Source          Source     `json:"source"`
}

type Evidence struct {
	BinaryPath      string `json:"binaryPath,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Locator         string `json:"locator,omitempty"`
	BinaryHash      string `json:"binaryHash,omitempty"`
	SoftwareID      string `json:"softwareId"`
	BindingRevision int64  `json:"bindingRevision"`
	RecipeKey       string `json:"recipeKey"`
	RecipeRevision  string `json:"recipeRevision"`
	ConfigHash      string `json:"configHash"`
	ImageID         string `json:"imageId"`
	ContainerID     string `json:"containerId"`
	IdentityHash    string `json:"identityHash"`
	ObservedVersion string `json:"observedVersion"`
	Health          string `json:"health"`
	ObservedAt      string `json:"observedAt"`
}

type Check struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PortMapping struct {
	Key           string `json:"key,omitempty"`
	BindAddress   string `json:"bindAddress"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

type Preflight struct {
	BinaryDirectory string        `json:"binaryDirectory"`
	LogsDirectory   string        `json:"logsDirectory"`
	Ready           bool          `json:"ready"`
	Root            string        `json:"root"`
	DataDirectory   string        `json:"dataDirectory"`
	ConfigDirectory string        `json:"configDirectory"`
	Endpoint        string        `json:"endpoint"`
	Checks          []Check       `json:"checks"`
	PortMappings    []PortMapping `json:"portMappings"`
}

func Digest(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func CatalogHash() string { return Digest(Catalog()) }

func ValidateDefinition(d Definition) error {
	expected, err := Find(d.Key, d.Version)
	if err == nil && reflect.DeepEqual(expected, d) {
		return nil
	}
	// V1 requests remain byte-for-byte valid for STATUS/ACK. Do not normalize
	// their serialized material or input hash after the catalog is extended.
	for _, legacy := range legacyDefinitions() {
		if reflect.DeepEqual(legacy, d) {
			return nil
		}
	}
	return errors.New("SERVICE_RECIPE_UNTRUSTED")
}

func (c Config) Mode() string {
	if c.RuntimeMode == "" {
		return "DOCKER"
	}
	return c.RuntimeMode
}

func InstallRoot(base, id string, c Config) string {
	if c.StorageRoot != "" {
		return path.Join(c.StorageRoot, id)
	}
	return path.Join(base, "software", id)
}

func ValidStorageRoot(value string) bool {
	if value == "" {
		return true
	}
	if !storagePath.MatchString(value) || !path.IsAbs(value) || path.Clean(value) != value || len(value) > 220 || strings.Contains(value, "..") {
		return false
	}
	for _, prefix := range []string{"/opt/", "/srv/", "/var/lib/", "/data/", "/mnt/"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

type ChangeRequest struct {
	Previous                Request `json:"previous"`
	Config                  Config  `json:"config"`
	ExpectedBindingRevision int64   `json:"expectedBindingRevision"`
}

func (c ChangeRequest) Validate() error {
	if err := c.Previous.Validate(); err != nil {
		return err
	}
	if c.ExpectedBindingRevision != c.Previous.BindingRevision || c.Config.StorageRoot != c.Previous.Config.StorageRoot || c.Config.Mode() != c.Previous.Config.Mode() || c.Previous.Definition.Kind == "TOOL" {
		return errors.New("SERVICE_CONFIGURATION_BOUNDARY_INVALID")
	}
	if c.Previous.Definition.Key == "mysql" {
		before, after := c.Previous.Config, c.Config
		if before.CredentialID != after.CredentialID || before.CredentialRevision != after.CredentialRevision || before.Database != after.Database || before.Username != after.Username || before.UserCredentialID != after.UserCredentialID || before.UserCredentialRevision != after.UserCredentialRevision {
			return errors.New("MYSQL_INITIALIZATION_FIELDS_IMMUTABLE")
		}
	}
	return c.Config.Validate(c.Previous.Definition)
}

func credential(id, rev string) bool { return uuid.MatchString(id) && revision.MatchString(rev) }

func (c Config) Validate(d Definition) error {
	ip := net.ParseIP(c.BindAddress)
	if strings.TrimSpace(c.Name) != c.Name || len(c.Name) < 1 || len(c.Name) > 120 || strings.ContainsAny(c.Name, "\r\n\x00") || !ValidStorageRoot(c.StorageRoot) || (c.Mode() != "NATIVE" && c.Mode() != "DOCKER") || c.MemoryMiB < 128 || c.MemoryMiB > 16384 {
		return errors.New("SERVICE_CONFIG_INVALID")
	}
	if c.Mode() == "NATIVE" && d.Native == nil {
		return errors.New("NATIVE_RECIPE_UNAVAILABLE")
	}
	if !slices.Contains(d.RuntimeModes, c.Mode()) {
		return errors.New("SERVICE_RUNTIME_UNSUPPORTED")
	}
	tool := d.Kind == "TOOL"
	if tool {
		if c.HostPort != 0 || len(c.Ports) > 0 {
			return errors.New("TOOL_PORT_UNEXPECTED")
		}
	} else if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() || c.HostPort < 1 || c.HostPort > 65535 {
		return errors.New("SERVICE_CONFIG_INVALID")
	}
	allowed := map[string]PortDescriptor{}
	for _, p := range d.Ports {
		allowed[p.Key] = p
	}
	for key, port := range c.Ports {
		p, ok := allowed[key]
		if !ok || port < 0 || port > 65535 || p.NativeOnly && c.Mode() != "NATIVE" || p.Required && port == 0 || key == "main" && port != c.HostPort {
			return errors.New("SERVICE_PORT_CONFIG_INVALID")
		}
	}
	if !tool && !credential(c.CredentialID, c.CredentialRevision) && d.Key != "nginx" {
		return errors.New("SERVICE_CREDENTIAL_REQUIRED")
	}
	if (tool || d.Key == "nginx") && (c.CredentialID != "" || c.CredentialRevision != "") {
		return errors.New("SERVICE_CREDENTIAL_UNEXPECTED")
	}
	if d.Key == "seaweedfs" {
		if !credential(c.AccessKeyCredentialID, c.AccessKeyCredentialRevision) {
			return errors.New("SERVICE_ACCESS_KEY_REQUIRED")
		}
	} else if c.AccessKeyCredentialID != "" || c.AccessKeyCredentialRevision != "" {
		return errors.New("SERVICE_ACCESS_KEY_UNEXPECTED")
	}
	if d.Key == "mysql" {
		if c.Database != "" && (!identifier.MatchString(c.Database) || strings.EqualFold(c.Database, "mysql") || strings.EqualFold(c.Database, "sys") || strings.EqualFold(c.Database, "information_schema") || strings.EqualFold(c.Database, "performance_schema")) {
			return errors.New("SERVICE_DATABASE_INVALID")
		}
		if c.Username != "" {
			if !identifier.MatchString(c.Username) || strings.EqualFold(c.Username, "root") || c.Database == "" || !credential(c.UserCredentialID, c.UserCredentialRevision) {
				return errors.New("SERVICE_DATABASE_USER_INVALID")
			}
		} else if c.UserCredentialID != "" || c.UserCredentialRevision != "" {
			return errors.New("SERVICE_DATABASE_USER_REQUIRED")
		}
	} else if c.Database != "" || c.UserCredentialID != "" || c.UserCredentialRevision != "" || c.Username != "" && d.Key != "mediamtx" {
		return errors.New("SERVICE_DATABASE_UNEXPECTED")
	}
	if d.Key == "mediamtx" && c.Username != "" && !identifier.MatchString(c.Username) {
		return errors.New("SERVICE_USERNAME_INVALID")
	}
	if d.Key == "zlm" {
		tcp, udp := PortValue(d, c, "rtpTcp"), PortValue(d, c, "rtpUdp")
		if tcp != udp {
			return errors.New("ZLM_RTP_TCP_UDP_MUST_MATCH")
		}
	}
	if d.Key == "mediamtx" {
		if (PortValue(d, c, "webrtc") > 0) != (PortValue(d, c, "webrtcUdp") > 0) {
			return errors.New("MEDIAMTX_WEBRTC_PORT_PAIR_REQUIRED")
		}
	}
	seen := map[string]bool{}
	for _, p := range PortMappings(d, c) {
		key := p.Protocol + ":" + p.BindAddress + ":" + fmt.Sprint(p.HostPort)
		if seen[key] {
			return errors.New("SERVICE_PORT_DUPLICATED")
		}
		seen[key] = true
	}
	return nil
}

func (s Source) ValidateFor(d Definition, c Config) error {
	if c.Mode() == "NATIVE" {
		if d.Native == nil {
			return errors.New("NATIVE_RECIPE_UNAVAILABLE")
		}
		if s.Type == "ONLINE" && s.SHA256 == "" && s.ByteSize == 0 {
			return nil
		}
		if (s.Type == "ARCHIVE" || s.Type == "ONLINE") && sha.MatchString(s.SHA256) && s.ByteSize > 0 && s.ByteSize <= ArchiveLimit {
			return nil
		}
		return errors.New("NATIVE_SOURCE_INVALID")
	}
	return s.Validate()
}

func (s Source) Validate() error {
	if s.Type == "REGISTRY" && s.SHA256 == "" && s.ByteSize == 0 {
		return nil
	}
	if s.Type == "ARCHIVE" && sha.MatchString(s.SHA256) && s.ByteSize > 0 && s.ByteSize <= ArchiveLimit {
		return nil
	}
	return errors.New("SERVICE_SOURCE_INVALID")
}

func (r Request) Validate() error {
	if !uuid.MatchString(r.SoftwareID) || r.BindingRevision < 1 {
		return errors.New("SERVICE_INSTALL_IDENTITY_INVALID")
	}
	if err := ValidateDefinition(r.Definition); err != nil {
		return err
	}
	if err := r.Config.Validate(r.Definition); err != nil {
		return err
	}
	return r.Source.ValidateFor(r.Definition, r.Config)
}

func SecretKey(id, rev string) string { return id + ":" + rev }

// ValidateSecrets is shared by HTTP preflight and the target executor. Values
// remain transport-only; callers must never serialize this map into a plan.
func ValidateSecrets(d Definition, c Config, values map[string]string) error {
	secret := values[SecretKey(c.CredentialID, c.CredentialRevision)]
	userSecret := values[SecretKey(c.UserCredentialID, c.UserCredentialRevision)]
	accessKey := values[SecretKey(c.AccessKeyCredentialID, c.AccessKeyCredentialRevision)]
	valid := func(value string) bool {
		return len(value) >= 8 && len(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00") && strings.TrimSpace(value) == value
	}
	if d.Key != "nginx" && d.Kind != "TOOL" && !valid(secret) {
		return errors.New("SERVICE_SECRET_INVALID")
	}
	if d.Key == "mysql" && c.Username != "" && !valid(userSecret) {
		return errors.New("SERVICE_USER_SECRET_INVALID")
	}
	if d.Key == "mysql" {
		// The vendor entrypoint uses these values in initialization SQL and
		// its option file. Reject syntax that this fixed recipe cannot encode.
		safe := regexp.MustCompile(`^[a-zA-Z0-9!@#$%^&*()_+=:,.?/-]{8,128}$`)
		if !safe.MatchString(secret) || c.Username != "" && !safe.MatchString(userSecret) {
			return errors.New("SERVICE_MYSQL_SECRET_CHARACTERS")
		}
	}
	if d.Key == "seaweedfs" && !regexp.MustCompile(`^[a-zA-Z0-9_+=.@-]{3,128}$`).MatchString(accessKey) {
		return errors.New("SERVICE_ACCESS_KEY_INVALID")
	}
	return nil
}

// ValidateFor checks mode-specific receipt shape before controller commit.
func (e Evidence) ValidateFor(r Request) error {
	if e.SoftwareID != r.SoftwareID || e.BindingRevision != r.BindingRevision || e.RecipeKey != r.Definition.Key || e.RecipeRevision != r.Definition.Revision || e.ConfigHash != Digest(r.Config) || !sha.MatchString(e.IdentityHash) {
		return errors.New("SERVICE_INSTALL_RECEIPT_MISMATCH")
	}
	kind := e.Kind
	locator := e.Locator
	if kind == "" && r.Config.Mode() == "DOCKER" && r.Definition.Kind != "TOOL" {
		kind = "DOCKER"
		locator = e.ContainerID
	}
	if r.Config.Mode() == "NATIVE" {
		if !sha.MatchString(e.BinaryHash) || e.ImageID != "" || e.ContainerID != "" {
			return errors.New("NATIVE_RECEIPT_INVALID")
		}
		if r.Definition.Kind == "TOOL" {
			if kind != "TOOL" || !path.IsAbs(locator) || strings.ContainsAny(locator, "\r\n\x00") {
				return errors.New("TOOL_RECEIPT_INVALID")
			}
		} else if kind != "SYSTEMD" || locator != "sfops-service-"+r.SoftwareID+".service" {
			return errors.New("NATIVE_RECEIPT_INVALID")
		}
	} else if r.Definition.Kind == "TOOL" {
		if kind != "TOOL" || locator != "image:"+r.Definition.ImageID || e.ImageID != r.Definition.ImageID || e.ContainerID != "" {
			return errors.New("TOOL_RECEIPT_INVALID")
		}
	} else if kind != "DOCKER" || !sha.MatchString(locator) || e.ContainerID != locator || e.ImageID != r.Definition.ImageID {
		return errors.New("SERVICE_INSTALL_RECEIPT_MISMATCH")
	}
	return nil
}

func NativeBinaryRelative(d Definition) string {
	switch d.Key {
	case "java":
		return "runtime/bin/java"
	case "maven":
		return "bin/mvn"
	case "mysql":
		return "runtime/mysql/bin/mysqld"
	case "redis":
		return "bin/redis-server"
	case "nginx":
		return "bin/nginx"
	case "seaweedfs":
		return "bin/weed"
	case "mediamtx":
		return "bin/mediamtx"
	case "zlm":
		return "bin/MediaServer"
	case "python":
		return "runtime/python/bin/python3.12"
	case "ffmpeg":
		return "bin/ffmpeg"
	case "codex":
		return "bin/codex"
	case "claude":
		return "bin/claude"
	}
	return ""
}
