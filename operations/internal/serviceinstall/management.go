package serviceinstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type CredentialReference struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type ParameterDefinition struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Min      *int64   `json:"min,omitempty"`
	Max      *int64   `json:"max,omitempty"`
}

type ResourceDefinition struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Protocol    string `json:"protocol,omitempty"`
	BindAddress string `json:"bindAddress,omitempty"`
	Release     bool   `json:"release,omitempty"`
}

type ResolvedResource struct {
	Key          string `json:"key"`
	Type         string `json:"type"`
	Value        string `json:"value"`
	Protocol     string `json:"protocol,omitempty"`
	BindAddress  string `json:"bindAddress,omitempty"`
	IdentityHash string `json:"identityHash,omitempty"`
	Release      bool   `json:"release,omitempty"`
}

type ManagementOperation struct {
	Key           string                `json:"key"`
	Name          string                `json:"name"`
	Group         string                `json:"group,omitempty"`
	ReadOnly      bool                  `json:"readOnly,omitempty"`
	Rebind        bool                  `json:"rebind,omitempty"`
	Parameters    []ParameterDefinition `json:"parameters,omitempty"`
	Steps         []CommandStep         `json:"steps"`
	VerifySteps   []CommandStep         `json:"verifySteps,omitempty"`
	RollbackSteps []CommandStep         `json:"rollbackSteps,omitempty"`
	ResultFormat  string                `json:"resultFormat"`
	Resources     []ResourceDefinition  `json:"resources,omitempty"`
	RuntimeModes  []string              `json:"runtimeModes,omitempty"`
}

type ManagementDefinition struct {
	ScriptPrelude string                `json:"scriptPrelude,omitempty"`
	Parameters    []ParameterDefinition `json:"parameters,omitempty"`
	Operations    []ManagementOperation `json:"operations"`
	Resources     []ResourceDefinition  `json:"resources,omitempty"`
}

type ManagementRequest struct {
	Installation         *Request                       `json:"installation,omitempty"`
	Definition           Definition                     `json:"definition"`
	SoftwareID           string                         `json:"softwareId"`
	BindingRevision      int64                          `json:"bindingRevision"`
	Root                 string                         `json:"root"`
	Account              string                         `json:"account,omitempty"`
	OperationKey         string                         `json:"operationKey"`
	Parameters           map[string]string              `json:"parameters,omitempty"`
	ParameterCredentials map[string]CredentialReference `json:"parameterCredentials,omitempty"`
	OperationParameters  map[string]string              `json:"operationParameters,omitempty"`
	OperationCredentials map[string]CredentialReference `json:"operationCredentials,omitempty"`
	ResourceHash         string                         `json:"resourceHash,omitempty"`
}

type ManagementPreflight struct {
	Ready        bool               `json:"ready"`
	Checks       []Check            `json:"checks"`
	Resources    []ResolvedResource `json:"resources"`
	ResourceHash string             `json:"resourceHash"`
}

type ManagementTable struct {
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"rows"`
	Total   *int                `json:"total,omitempty"`
}

type ManagementEvidence struct {
	SoftwareID     string             `json:"softwareId"`
	OperationKey   string             `json:"operationKey"`
	DefinitionHash string             `json:"definitionHash"`
	ParametersHash string             `json:"parametersHash"`
	ResourceHash   string             `json:"resourceHash"`
	ReadOnly       bool               `json:"readOnly"`
	Output         string             `json:"output"`
	Table          *ManagementTable   `json:"table,omitempty"`
	Resources      []ResolvedResource `json:"resources,omitempty"`
	ObservedAt     string             `json:"observedAt"`
}

var parameterKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)
var operationKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var resourceKey = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,47}$`)
var resourceToken = regexp.MustCompile(`\$\{(OPS_[A-Z0-9_]+)\}`)

func ValidateParameterDefinitions(defs []ParameterDefinition) error {
	if len(defs) > 48 {
		return errors.New("PARAMETER_LIMIT")
	}
	seen := map[string]bool{}
	for _, p := range defs {
		if !parameterKey.MatchString(p.Key) || seen[p.Key] || strings.TrimSpace(p.Label) == "" || len(p.Label) > 120 || !slices.Contains([]string{"TEXT", "INTEGER", "BOOLEAN", "SELECT", "PATH", "PORT", "CREDENTIAL"}, p.Type) || len(p.Options) > 64 || len(p.Default) > 8192 || p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			return errors.New("PARAMETER_DEFINITION_INVALID")
		}
		seen[p.Key] = true
		if p.Type != "INTEGER" && p.Type != "PORT" && (p.Min != nil || p.Max != nil) || p.Type != "SELECT" && len(p.Options) != 0 || p.Type == "SELECT" && len(p.Options) == 0 || p.Type == "CREDENTIAL" && p.Default != "" {
			return errors.New("PARAMETER_DEFINITION_INVALID")
		}
		options := map[string]bool{}
		for _, value := range p.Options {
			if value == "" || len(value) > 1024 || strings.ContainsRune(value, 0) || options[value] {
				return errors.New("PARAMETER_OPTIONS_INVALID")
			}
			options[value] = true
		}
		if p.Default != "" && validateParameterValue(p, p.Default) != nil {
			return errors.New("PARAMETER_DEFAULT_INVALID")
		}
	}
	return nil
}

func validateParameterValue(p ParameterDefinition, value string) error {
	if len(value) > 8192 || strings.ContainsRune(value, 0) || p.Required && strings.TrimSpace(value) == "" {
		return errors.New("PARAMETER_VALUE_INVALID")
	}
	if value == "" && !p.Required {
		return nil
	}
	switch p.Type {
	case "INTEGER", "PORT":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || p.Min != nil && n < *p.Min || p.Max != nil && n > *p.Max || p.Type == "PORT" && (n < 1 || n > 65535) {
			return errors.New("PARAMETER_VALUE_INVALID")
		}
	case "BOOLEAN":
		if value != "true" && value != "false" {
			return errors.New("PARAMETER_VALUE_INVALID")
		}
	case "SELECT":
		if !slices.Contains(p.Options, value) {
			return errors.New("PARAMETER_VALUE_INVALID")
		}
	case "PATH":
		if !path.IsAbs(value) || path.Clean(value) != value || len(value) > 512 || strings.ContainsAny(value, "\r\n") {
			return errors.New("PARAMETER_VALUE_INVALID")
		}
	case "CREDENTIAL":
		return errors.New("PARAMETER_CREDENTIAL_REFERENCE_REQUIRED")
	}
	return nil
}

// ResolveParameters returns a new public-value map. Scripts receive these as
// environment values; they are never substituted into script source text.
func ResolveParameters(defs []ParameterDefinition, values map[string]string, refs map[string]CredentialReference) (map[string]string, error) {
	if err := ValidateParameterDefinitions(defs); err != nil {
		return nil, err
	}
	known := map[string]ParameterDefinition{}
	for _, p := range defs {
		known[p.Key] = p
	}
	for key := range values {
		if p, ok := known[key]; !ok || p.Type == "CREDENTIAL" {
			return nil, errors.New("PARAMETER_UNDECLARED")
		}
	}
	for key, ref := range refs {
		if p, ok := known[key]; !ok || p.Type != "CREDENTIAL" || !credential(ref.ID, ref.Revision) {
			return nil, errors.New("PARAMETER_CREDENTIAL_INVALID")
		}
	}
	out := map[string]string{}
	for _, p := range defs {
		if p.Type == "CREDENTIAL" {
			if _, ok := refs[p.Key]; p.Required && !ok {
				return nil, errors.New("PARAMETER_CREDENTIAL_REQUIRED")
			}
			continue
		}
		value, exists := values[p.Key]
		if !exists {
			value = p.Default
		}
		if err := validateParameterValue(p, value); err != nil {
			return nil, fmt.Errorf("%s: %w", p.Key, err)
		}
		out[p.Key] = value
	}
	return out, nil
}

func validateOperationStep(step CommandStep) error {
	if step.Kind != "" && step.Kind != "COMMAND" || step.Action != "" || step.ID != "" && !commandStepID.MatchString(step.ID) || strings.TrimSpace(step.Name) == "" || len(step.Name) > 120 || strings.TrimSpace(step.Script) == "" || len(step.Script) > 16384 || strings.ContainsRune(step.Script, 0) || !slices.Contains([]string{"ROOT", "ACCOUNT"}, step.RunAs) || step.TimeoutSeconds < 1 || step.TimeoutSeconds > 3600 || len(step.WorkingDirectory) > 512 || strings.ContainsAny(step.WorkingDirectory, "\x00\r\n") {
		return errors.New("MANAGEMENT_STEP_INVALID")
	}
	return nil
}

func (d Definition) ValidateManagement() error {
	if err := d.validateWebAccess(); err != nil {
		return err
	}
	if d.DefaultMemoryMiB != 0 && (d.DefaultMemoryMiB < 128 || d.DefaultMemoryMiB > 16384) {
		return errors.New("SERVICE_MEMORY_DEFAULT_INVALID")
	}
	if err := ValidateParameterDefinitions(d.Parameters); err != nil {
		return err
	}
	m := d.Management
	if m == nil {
		return nil
	}
	if len(m.ScriptPrelude) > 32<<10 || strings.ContainsRune(m.ScriptPrelude, 0) {
		return errors.New("MANAGEMENT_PRELUDE_INVALID")
	}
	if err := ValidateParameterDefinitions(m.Parameters); err != nil {
		return err
	}
	if len(m.Operations) == 0 || len(m.Operations) > 32 || len(m.Resources) > 64 {
		return errors.New("MANAGEMENT_DEFINITION_INVALID")
	}
	seen := map[string]bool{}
	for _, op := range m.Operations {
		if !operationKey.MatchString(op.Key) || seen[op.Key] || strings.TrimSpace(op.Name) == "" || len(op.Name) > 120 || len(op.Group) > 80 || len(op.Steps) < 1 || len(op.Steps) > 40 || len(op.VerifySteps) > 20 || !op.ReadOnly && len(op.VerifySteps) == 0 || !slices.Contains([]string{"TEXT", "TABLE"}, op.ResultFormat) {
			return errors.New("MANAGEMENT_OPERATION_INVALID")
		}
		seen[op.Key] = true
		if len(op.RollbackSteps) > 0 && !op.Rebind {
			return errors.New("MANAGEMENT_REBIND_INVALID")
		}
		if (op.Rebind || len(op.RollbackSteps) != 0) && (op.ReadOnly || op.Key != "configure" && op.Key != "migrate-data" || len(op.RollbackSteps) == 0 || len(op.VerifySteps) == 0 || len(op.RollbackSteps) > 20 || d.Commands == nil || !IsBuiltinCommand(d) && d.Commands.Kind != "SYSTEMD" && d.Commands.Kind != "DOCKER" || d.Kind == "TOOL") {
			return errors.New("MANAGEMENT_REBIND_INVALID")
		}
		for _, mode := range op.RuntimeModes {
			if !slices.Contains(d.RuntimeModes, mode) {
				return errors.New("MANAGEMENT_RUNTIME_INVALID")
			}
		}
		for _, resource := range op.Resources {
			if resource.Release && op.ReadOnly {
				return errors.New("MANAGEMENT_RESOURCE_RELEASE_INVALID")
			}
		}
		if err := validateResourceDefinitions(append(slices.Clone(m.Resources), op.Resources...)); err != nil {
			return err
		}
		if err := ValidateParameterDefinitions(op.Parameters); err != nil {
			return err
		}
		for _, p := range op.Parameters {
			for _, instance := range m.Parameters {
				if p.Key == instance.Key {
					return errors.New("MANAGEMENT_PARAMETER_DUPLICATE")
				}
			}
		}
		for _, step := range append(append(slices.Clone(op.Steps), op.VerifySteps...), op.RollbackSteps...) {
			if err := validateOperationStep(step); err != nil {
				return err
			}
		}
	}
	for _, resource := range m.Resources {
		if resource.Release {
			return errors.New("MANAGEMENT_RESOURCE_RELEASE_INVALID")
		}
	}
	return validateResourceDefinitions(m.Resources)
}

func validateResourceDefinitions(resources []ResourceDefinition) error {
	if len(resources) > 64 {
		return errors.New("MANAGEMENT_RESOURCE_INVALID")
	}
	seen := map[string]bool{}
	for _, resource := range resources {
		if !resourceKey.MatchString(resource.Key) || seen[resource.Key] || !slices.Contains([]string{"PATH_READ", "PATH_WRITE", "PORT", "UNIT", "CONTAINER"}, resource.Type) || resource.Value == "" || len(resource.Value) > 512 || strings.ContainsAny(resource.Value+resource.BindAddress, "\x00\r\n") {
			return errors.New("MANAGEMENT_RESOURCE_INVALID")
		}
		seen[resource.Key] = true
		if resource.Type == "PORT" {
			if !slices.Contains([]string{"TCP", "UDP"}, resource.Protocol) {
				return errors.New("MANAGEMENT_RESOURCE_INVALID")
			}
		} else if resource.Protocol != "" || resource.BindAddress != "" {
			return errors.New("MANAGEMENT_RESOURCE_INVALID")
		}
	}
	return nil
}

func (op ManagementOperation) SupportsRuntime(mode string) bool {
	return len(op.RuntimeModes) == 0 || slices.Contains(op.RuntimeModes, mode)
}

func (d Definition) ManagementOperation(key string) (ManagementOperation, error) {
	if d.Management != nil {
		for _, op := range d.Management.Operations {
			if op.Key == key {
				return op, nil
			}
		}
	}
	return ManagementOperation{}, errors.New("MANAGEMENT_OPERATION_NOT_FOUND")
}

func (r ManagementRequest) Validate() error {
	if !uuid.MatchString(r.SoftwareID) || r.BindingRevision < 1 || !path.IsAbs(r.Root) || path.Clean(r.Root) != r.Root || r.Root == "/" || len(r.Root) > 512 || strings.ContainsAny(r.Root, "\x00\r\n") || r.ResourceHash != "" && !sha.MatchString(r.ResourceHash) {
		return errors.New("MANAGEMENT_INPUT_INVALID")
	}
	if err := ValidateDefinition(r.Definition); err != nil {
		return err
	}
	if err := r.Definition.ValidateManagement(); err != nil {
		return err
	}
	op, err := r.Definition.ManagementOperation(r.OperationKey)
	if err != nil {
		return err
	}
	if op.Rebind {
		i := r.Installation
		if i == nil || i.Validate() != nil || i.SoftwareID != r.SoftwareID || i.BindingRevision != r.BindingRevision || !managementInstallationDefinitionMatches(i.Definition, r.Definition) || i.Config.Mode() != "NATIVE" && i.Config.Mode() != "DOCKER" {
			return errors.New("MANAGEMENT_REBIND_INSTALLATION_INVALID")
		}
	} else if r.Installation != nil {
		return errors.New("MANAGEMENT_REBIND_INSTALLATION_UNEXPECTED")
	}
	for _, step := range append(append(slices.Clone(op.Steps), op.VerifySteps...), op.RollbackSteps...) {
		if step.RunAs == "ACCOUNT" && (!commandAccount.MatchString(r.Account) || r.Account == "root") {
			return errors.New("COMMAND_ACCOUNT_REQUIRED")
		}
	}
	if _, err = ResolveParameters(r.Definition.Management.Parameters, r.Parameters, r.ParameterCredentials); err != nil {
		return err
	}
	_, err = ResolveParameters(op.Parameters, r.OperationParameters, r.OperationCredentials)
	return err
}

func managementInstallationDefinitionMatches(original, current Definition) bool {
	if IsBuiltinCommand(current) {
		// The published management script may evolve while the fixed package,
		// image and installation contract remain the original signed snapshot.
		return (original.Commands == nil || IsBuiltinCommand(original)) && Digest(BareDefinition(original)) == Digest(BareDefinition(current))
	}
	return original.Commands != nil && !IsBuiltinCommand(original) && original.Key == current.Key && original.Version == current.Version && original.Commands.Kind == current.Commands.Kind && original.Commands.Locator == current.Commands.Locator && original.Commands.BinaryPath == current.Commands.BinaryPath
}

func (r ManagementRequest) ValidateRebindEvidence(e Evidence) error {
	op, err := r.Definition.ManagementOperation(r.OperationKey)
	if err != nil || !op.Rebind || r.Installation == nil || r.Validate() != nil {
		return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
	}
	installation := *r.Installation
	installation.BindingRevision++
	if e.ValidateFor(installation) != nil || e.Health != "READY" {
		return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
	}
	if IsBuiltinCommand(r.Definition) {
		if installation.Config.Mode() == "DOCKER" {
			if e.Kind != "DOCKER" {
				return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
			}
		} else {
			binary := path.Join(r.Root, NativeBinaryRelative(installation.Definition))
			if IsOfficial(installation.Definition) {
				spec, _ := OfficialRecipeSpec(installation.Definition)
				binary = spec.Paths.Binary
			} else if installation.Definition.Key == "docker" {
				binary = "/usr/bin/dockerd"
			}
			if e.Kind != "SYSTEMD" || e.BinaryPath != binary {
				return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
			}
		}
	} else if e.Kind != installation.Definition.Commands.Kind || e.Kind == "SYSTEMD" && e.Locator != strings.NewReplacer("${OPS_INSTALL_ROOT}", r.Root, "${OPS_SOFTWARE_ID}", r.SoftwareID).Replace(installation.Definition.Commands.Locator) {
		return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
	}
	return nil
}

func (r ManagementRequest) ParameterHash() string {
	return Digest(struct {
		Parameters           map[string]string
		Credentials          map[string]CredentialReference
		OperationParameters  map[string]string
		OperationCredentials map[string]CredentialReference
	}{r.Parameters, r.ParameterCredentials, r.OperationParameters, r.OperationCredentials})
}

func (r ManagementRequest) CredentialReferences() []CredentialReference {
	return uniqueCredentialReferences(r.ParameterCredentials, r.OperationCredentials)
}

func (c Config) CredentialReferences() []CredentialReference {
	legacy := map[string]CredentialReference{}
	for _, ref := range []CredentialReference{{c.CredentialID, c.CredentialRevision}, {c.UserCredentialID, c.UserCredentialRevision}, {c.AccessKeyCredentialID, c.AccessKeyCredentialRevision}} {
		if ref.ID != "" {
			legacy[SecretKey(ref.ID, ref.Revision)] = ref
		}
	}
	return uniqueCredentialReferences(c.ParameterCredentials, legacy)
}

func uniqueCredentialReferences(groups ...map[string]CredentialReference) []CredentialReference {
	refs := map[string]CredentialReference{}
	for _, group := range groups {
		for _, ref := range group {
			refs[SecretKey(ref.ID, ref.Revision)] = ref
		}
	}
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]CredentialReference, 0, len(keys))
	for _, key := range keys {
		out = append(out, refs[key])
	}
	return out
}

func ValidateCredentialValues(refs []CredentialReference, values map[string]string) error {
	if len(values) != len(refs) {
		return errors.New("PARAMETER_SECRET_MISMATCH")
	}
	return ValidateCredentialSubset(refs, values)
}

// Completed-job status and acknowledgement may outlive a credential revision.
// Missing values are harmless there; an unrelated value is never accepted.
func ValidateCredentialSubset(refs []CredentialReference, values map[string]string) error {
	known := map[string]bool{}
	for _, ref := range refs {
		known[SecretKey(ref.ID, ref.Revision)] = true
	}
	for key, value := range values {
		if !known[key] || len(value) < 1 || len(value) > 32768 || strings.ContainsRune(value, 0) {
			return errors.New("PARAMETER_SECRET_INVALID")
		}
	}
	return nil
}

func (r ManagementRequest) ResolveResources(locator string) ([]ResolvedResource, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	op, _ := r.Definition.ManagementOperation(r.OperationKey)
	values, _ := ResolveParameters(r.Definition.Management.Parameters, r.Parameters, r.ParameterCredentials)
	inputs, _ := ResolveParameters(op.Parameters, r.OperationParameters, r.OperationCredentials)
	for key, value := range inputs {
		values[key] = value
	}
	variables := map[string]string{"OPS_INSTALL_ROOT": r.Root, "OPS_DATA_ROOT": path.Join(r.Root, "data"), "OPS_CONFIG_ROOT": path.Join(r.Root, "config"), "OPS_SOFTWARE_ID": r.SoftwareID, "OPS_SOFTWARE_LOCATOR": locator, "OPS_VERSION": r.Definition.Version}
	for key, value := range values {
		variables["OPS_PARAM_"+key] = value
	}
	resolve := func(value string) (string, error) {
		invalid := false
		result := resourceToken.ReplaceAllStringFunc(value, func(token string) string {
			key := token[2 : len(token)-1]
			v, ok := variables[key]
			if !ok {
				invalid = true
			}
			return v
		})
		if invalid || strings.ContainsAny(result, "$\x00\r\n") {
			return "", errors.New("MANAGEMENT_RESOURCE_VARIABLE_INVALID")
		}
		return result, nil
	}
	out := []ResolvedResource{}
	for _, definition := range append(slices.Clone(r.Definition.Management.Resources), op.Resources...) {
		value, err := resolve(definition.Value)
		if err != nil {
			return nil, err
		}
		address, err := resolve(definition.BindAddress)
		if err != nil {
			return nil, err
		}
		switch definition.Type {
		case "PATH_READ", "PATH_WRITE":
			if !path.IsAbs(value) || path.Clean(value) != value || value == "/" || len(value) > 512 {
				return nil, errors.New("MANAGEMENT_RESOURCE_PATH_INVALID")
			}
		case "PORT":
			p, err := strconv.Atoi(value)
			ip := net.ParseIP(address)
			if err != nil || p < 1 || p > 65535 || ip == nil || ip.IsMulticast() {
				return nil, errors.New("MANAGEMENT_RESOURCE_PORT_INVALID")
			}
		case "UNIT":
			if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,95}\.service$`).MatchString(value) {
				return nil, errors.New("MANAGEMENT_RESOURCE_UNIT_INVALID")
			}
		case "CONTAINER":
			if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`).MatchString(value) {
				return nil, errors.New("MANAGEMENT_RESOURCE_CONTAINER_INVALID")
			}
		}
		resource := ResolvedResource{Key: definition.Key, Type: definition.Type, Value: value, Protocol: definition.Protocol, BindAddress: address, Release: definition.Release}
		duplicate := false
		for _, prior := range out {
			if prior.Type == resource.Type && prior.Value == resource.Value && prior.Protocol == resource.Protocol && prior.BindAddress == resource.BindAddress {
				if prior.Release != resource.Release {
					return nil, errors.New("MANAGEMENT_RESOURCE_RELEASE_INVALID")
				}
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, resource)
		}
	}
	return out, nil
}

func ResourceConflict(a, b ResolvedResource) bool {
	if strings.HasPrefix(a.Type, "PATH_") && strings.HasPrefix(b.Type, "PATH_") {
		return (a.Type == "PATH_WRITE" || b.Type == "PATH_WRITE") && (a.Value == b.Value || strings.HasPrefix(a.Value, b.Value+"/") || strings.HasPrefix(b.Value, a.Value+"/"))
	}
	if a.Type != b.Type {
		return false
	}
	if a.Type == "PORT" {
		ipA, ipB := net.ParseIP(a.BindAddress), net.ParseIP(b.BindAddress)
		return a.Protocol == b.Protocol && a.Value == b.Value && ipA != nil && ipB != nil && (ipA.Equal(ipB) || ipA.IsUnspecified() || ipB.IsUnspecified())
	}
	return a.Type != "PATH_READ" && a.Value == b.Value
}

// Resource keys are labels. A software instance can create several sites with
// the same label, so claims are identified by their resolved resource address.
func SameResource(a, b ResolvedResource) bool {
	return a.Type == b.Type && a.Value == b.Value && a.Protocol == b.Protocol && a.BindAddress == b.BindAddress
}

func MergeResourceClaims(prior, changes []ResolvedResource) ([]ResolvedResource, error) {
	out := slices.Clone(prior)
	for _, change := range changes {
		out = slices.DeleteFunc(out, func(resource ResolvedResource) bool { return SameResource(resource, change) })
		if !change.Release {
			out = append(out, change)
		}
	}
	if len(out) > 256 {
		return nil, errors.New("MANAGEMENT_RESOURCE_LIMIT")
	}
	return out, nil
}

func (e ManagementEvidence) ValidateFor(r ManagementRequest) error {
	op, err := r.Definition.ManagementOperation(r.OperationKey)
	if err != nil || e.SoftwareID != r.SoftwareID || e.OperationKey != r.OperationKey || e.DefinitionHash != Digest(r.Definition) || e.ParametersHash != r.ParameterHash() || e.ResourceHash != r.ResourceHash || e.ReadOnly != op.ReadOnly || e.ObservedAt == "" || len(e.Output) > 64<<10 {
		return errors.New("MANAGEMENT_RECEIPT_MISMATCH")
	}
	if len(e.Resources) > 256 {
		return errors.New("MANAGEMENT_RESOURCE_LIMIT")
	}
	for _, resource := range e.Resources {
		if resource.Release || !sha.MatchString(resource.IdentityHash) {
			return errors.New("MANAGEMENT_RESOURCES_UNCONFIRMED")
		}
	}
	if op.ResultFormat == "TABLE" {
		if e.Table == nil || len(e.Table.Columns) > 32 || len(e.Table.Rows) > 200 {
			return errors.New("MANAGEMENT_TABLE_INVALID")
		}
		if e.Table.Total != nil && (*e.Table.Total < len(e.Table.Rows) || *e.Table.Total > 1_000_000_000) {
			return errors.New("MANAGEMENT_TABLE_INVALID")
		}
		columns := map[string]bool{}
		for _, column := range e.Table.Columns {
			if column == "" || len(column) > 120 || columns[column] || strings.ContainsRune(column, 0) {
				return errors.New("MANAGEMENT_TABLE_INVALID")
			}
			columns[column] = true
		}
		for _, row := range e.Table.Rows {
			for key, value := range row {
				if !columns[key] || len(value) > 8192 || strings.ContainsRune(value, 0) {
					return errors.New("MANAGEMENT_TABLE_INVALID")
				}
			}
		}
		encoded, err := json.Marshal(e.Table)
		if err != nil || len(encoded) > 64<<10 {
			return errors.New("MANAGEMENT_TABLE_INVALID")
		}
	} else if e.Table != nil {
		return errors.New("MANAGEMENT_TABLE_INVALID")
	}
	return nil
}
