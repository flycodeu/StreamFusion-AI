package serviceinstall

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Commands are administrator-published scripts. The target additionally verifies
// the controller signature; structural validation alone never authorizes scripts.
type CommandRecipe struct {
	FlowVersion   int           `json:"flowVersion,omitempty"`
	Builtin       bool          `json:"builtin,omitempty"`
	Revision      int64         `json:"revision,omitempty"`
	Kind          string        `json:"kind"`
	Locator       string        `json:"locator"`
	BinaryPath    string        `json:"binaryPath,omitempty"`
	Distributions []string      `json:"distributions,omitempty"`
	Steps         []CommandStep `json:"steps"`
}

type CommandStep struct {
	Kind             string   `json:"kind,omitempty"`
	Action           string   `json:"action,omitempty"`
	ID               string   `json:"id,omitempty"`
	RuntimeMode      string   `json:"runtimeMode,omitempty"`
	SourceModes      []string `json:"sourceModes,omitempty"`
	Phase            string   `json:"phase"`
	Name             string   `json:"name"`
	Script           string   `json:"script"`
	RunAs            string   `json:"runAs"`
	WorkingDirectory string   `json:"workingDirectory,omitempty"`
	TimeoutSeconds   int      `json:"timeoutSeconds"`
}

// NormalizeScript canonicalizes Windows checkout/editor newlines before a
// command is published or signed. Frozen execution requests retain their bytes.
func NormalizeScript(script string) string {
	return strings.ReplaceAll(script, "\r\n", "\n")
}

// NormalizeCommandScripts returns an authoring copy without changing snapshots
// that may share the input definition's command or management slices.
func NormalizeCommandScripts(d Definition) Definition {
	normalize := func(steps []CommandStep) []CommandStep {
		steps = slices.Clone(steps)
		for i := range steps {
			steps[i].Script = NormalizeScript(steps[i].Script)
		}
		return steps
	}
	if d.Commands != nil {
		commands := *d.Commands
		commands.Steps = normalize(commands.Steps)
		d.Commands = &commands
	}
	if d.Management != nil {
		management := *d.Management
		management.ScriptPrelude = NormalizeScript(management.ScriptPrelude)
		management.Operations = slices.Clone(management.Operations)
		for i := range management.Operations {
			op := &management.Operations[i]
			op.Steps = normalize(op.Steps)
			op.VerifySteps = normalize(op.VerifySteps)
			op.RollbackSteps = normalize(op.RollbackSteps)
		}
		d.Management = &management
	}
	return d
}

var commandKey = regexp.MustCompile(`^custom-[a-z0-9][a-z0-9-]{0,55}$`)
var catalogKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var commandAccount = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (d Definition) ValidateCommands() error {
	if err := d.ValidateManagement(); err != nil {
		return err
	}
	if IsBuiltinCommand(d) {
		return d.validateBuiltinCommands()
	}
	c := d.Commands
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > 48<<10 {
		return errors.New("COMMAND_TEMPLATE_TOO_LARGE")
	}
	if c == nil || c.FlowVersion < 0 || c.FlowVersion > 1 || !commandKey.MatchString(d.Key) || strings.TrimSpace(d.Name) == "" || len(d.Name) > 120 || len(d.Version) == 0 || len(d.Version) > 80 || !revision.MatchString(d.Revision) || d.Platform != "linux/amd64" || d.Native != nil || len(d.RuntimeModes) != 1 || len(d.SourceModes) != 1 || len(c.Steps) < 2 || len(c.Steps) > 80 {
		return errors.New("COMMAND_TEMPLATE_INVALID")
	}
	if d.Port < 0 || d.Port > 65535 || len(d.Ports) > 16 || c.Kind == "TOOL" && (d.Port != 0 || len(d.Ports) != 0) {
		return errors.New("SERVICE_PORT_CONFIG_INVALID")
	}
	ports := map[string]bool{}
	for _, port := range d.Ports {
		environmentKey := strings.ToUpper(port.Key)
		if !parameterKey.MatchString(environmentKey) || ports[environmentKey] || environmentKey == "MAIN" && (port.Key != "main" || d.Port == 0) || strings.TrimSpace(port.Label) == "" || !slices.Contains([]string{"TCP", "UDP"}, port.Protocol) || port.DefaultPort < 0 || port.DefaultPort > 65535 || port.ContainerPort < 1 || port.ContainerPort > 65535 {
			return errors.New("SERVICE_PORT_CONFIG_INVALID")
		}
		ports[environmentKey] = true
	}
	if d.Port > 0 && len(d.Ports) > 0 && !ports["MAIN"] {
		return errors.New("SERVICE_PORT_CONFIG_INVALID")
	}
	if d.CatalogKey != "" && !catalogKey.MatchString(d.CatalogKey) {
		return errors.New("COMMAND_CATALOG_KEY_INVALID")
	}
	if c.Kind == "DOCKER" {
		if d.Kind != "SERVICE" || d.RuntimeModes[0] != "DOCKER" || !slices.Contains([]string{"REGISTRY", "ARCHIVE"}, d.SourceModes[0]) {
			return errors.New("COMMAND_TEMPLATE_INVALID")
		}
	} else if !slices.Contains([]string{"TOOL", "SYSTEMD"}, c.Kind) || d.RuntimeModes[0] != "NATIVE" || !slices.Contains([]string{"ONLINE", "ARCHIVE"}, d.SourceModes[0]) || !slices.Contains([]string{"TOOL", "SERVICE"}, d.Kind) || (c.Kind == "TOOL") != (d.Kind == "TOOL") {
		return errors.New("COMMAND_TEMPLATE_INVALID")
	}
	if len(c.Locator) == 0 || len(c.Locator) > 300 || strings.ContainsAny(c.Locator+c.BinaryPath, "\x00\r\n") {
		return errors.New("COMMAND_LOCATOR_INVALID")
	}
	if d.OfficialURL != "" {
		u, e := url.Parse(d.OfficialURL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return errors.New("COMMAND_SOURCE_URL_INVALID")
		}
	}
	phases := map[string]bool{}
	total := 0
	for _, s := range c.Steps {
		if s.Kind != "" && s.Kind != "COMMAND" || s.Action != "" {
			return errors.New("COMMAND_STEP_INVALID")
		}
		if s.RuntimeMode != "" && s.RuntimeMode != d.RuntimeModes[0] {
			return errors.New("COMMAND_STEP_INVALID")
		}
		for _, source := range s.SourceModes {
			if source != d.SourceModes[0] {
				return errors.New("COMMAND_STEP_INVALID")
			}
		}
		if !slices.Contains([]string{"INSTALL", "VERIFY", "UNINSTALL", "DELETE_DATA"}, s.Phase) || strings.TrimSpace(s.Name) == "" || len(s.Name) > 120 || strings.TrimSpace(s.Script) == "" || len(s.Script) > 16384 || strings.ContainsRune(s.Script, 0) || !slices.Contains([]string{"ROOT", "ACCOUNT"}, s.RunAs) || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 3600 || len(s.WorkingDirectory) > 300 || strings.ContainsAny(s.WorkingDirectory, "\x00\r\n") {
			return errors.New("COMMAND_STEP_INVALID")
		}
		total += len(s.Script)
		phases[s.Phase] = true
	}
	if total > 48<<10 || !phases["INSTALL"] || c.FlowVersion == 0 && !phases["VERIFY"] || !phases["UNINSTALL"] {
		return errors.New("COMMAND_PHASE_REQUIRED")
	}
	for _, os := range c.Distributions {
		if !slices.Contains([]string{"ubuntu", "debian", "centos", "rhel", "rocky", "almalinux", "fedora", "opensuse", "alpine"}, os) {
			return errors.New("COMMAND_DISTRIBUTION_INVALID")
		}
	}
	return nil
}

func (c Config) validateCommands(d Definition) error {
	if err := d.ValidateCommands(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 120 || strings.ContainsAny(c.Name, "\r\n\x00") || !ValidStorageRoot(c.StorageRoot) || !slices.Contains(d.RuntimeModes, c.Mode()) || c.MemoryMiB < 128 || c.MemoryMiB > 16384 {
		return errors.New("SERVICE_CONFIG_INVALID")
	}
	if c.CredentialID != "" || c.CredentialRevision != "" || c.AccessKeyCredentialID != "" || c.AccessKeyCredentialRevision != "" || c.UserCredentialID != "" || c.UserCredentialRevision != "" || c.Username != "" || c.Database != "" {
		return errors.New("COMMAND_SECRET_UNEXPECTED")
	}
	if d.Port == 0 && c.HostPort != 0 || len(c.Ports) > len(d.Ports) {
		return errors.New("SERVICE_PORT_CONFIG_INVALID")
	}
	for key, value := range c.Ports {
		known := false
		for _, p := range d.Ports {
			if p.Key == key {
				known = true
			}
		}
		if !known || value < 0 || value > 65535 {
			return errors.New("SERVICE_PORT_CONFIG_INVALID")
		}
	}
	for _, port := range d.Ports {
		if port.Required && PortValue(d, c, port.Key) == 0 {
			return errors.New("SERVICE_PORT_CONFIG_INVALID")
		}
	}
	if d.Port > 0 || len(PortMappings(d, c)) > 0 {
		if !validListenAddress(c.BindAddress, d.Web != nil) || d.Port > 0 && (c.HostPort < 1 || c.HostPort > 65535) {
			return errors.New("SERVICE_PORT_CONFIG_INVALID")
		}
	}
	seenPorts := map[string]bool{}
	for _, p := range PortMappings(d, c) {
		key := p.Protocol + ":" + strconv.Itoa(p.HostPort)
		if seenPorts[key] {
			return errors.New("SERVICE_PORT_CONFIG_INVALID")
		}
		seenPorts[key] = true
	}
	for _, s := range d.Commands.Steps {
		if s.RunAs == "ACCOUNT" && (!commandAccount.MatchString(c.Account) || c.Account == "root") {
			return errors.New("COMMAND_ACCOUNT_REQUIRED")
		}
	}
	if c.Account != "" && (!commandAccount.MatchString(c.Account) || c.Account == "root") {
		return errors.New("COMMAND_ACCOUNT_INVALID")
	}
	return nil
}
