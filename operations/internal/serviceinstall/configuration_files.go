package serviceinstall

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"
)

const ConfigurationFileLimit = 32 << 10
const ConfigurationFileScript = "# Ops 受管配置文件\ntrue"
const ConfigurationFileSecret = "configurationFileBody"

// Configuration bodies are file payloads, never shell arguments or parameter
// substitutions. Their digest is covered by the normal signed operation.
type ConfigurationFileRequest struct {
	Name            string `json:"name"`
	Action          string `json:"action"`
	Revision        string `json:"revision"`
	SHA256          string `json:"sha256,omitempty"`
	ContentEnvelope []byte `json:"contentEnvelope,omitempty"`
}

type ConfigurationFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Enabled  bool   `json:"enabled"`
	Managed  bool   `json:"managed"`
	Editable bool   `json:"editable"`
}

type ConfigurationFileResult struct {
	Files     []ConfigurationFile `json:"files"`
	CanCreate bool                `json:"canCreate"`
	Content   string              `json:"content,omitempty"`
	Revision  string              `json:"revision,omitempty"`
	Editable  bool                `json:"editable"`
	Status    string              `json:"status,omitempty"`
	Message   string              `json:"message,omitempty"`
	Code      string              `json:"code,omitempty"`
	Line      int                 `json:"line,omitempty"`
	Listen    string              `json:"listen,omitempty"`
}

var configurationFileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,47}$`)

// File operations are executor capabilities added to a request, not scripts
// published into the installation template. Keep the original template's size
// limit and adopted revision independent of these transport-only operations.
func WithoutConfigurationFileOperations(d Definition) Definition {
	if d.Management == nil {
		return d
	}
	m := *d.Management
	m.Operations = make([]ManagementOperation, 0, len(m.Operations))
	for _, op := range d.Management.Operations {
		if op.Key != "config-file-read" && op.Key != "config-file-save" {
			m.Operations = append(m.Operations, op)
		}
	}
	d.Management = &m
	if len(m.Operations) == 0 {
		d.Management = nil
	}
	return d
}

func ConfigurationFileCreationRevision(revision string) bool {
	return strings.HasPrefix(revision, "ABSENT:") && sha.MatchString(strings.TrimPrefix(revision, "ABSENT:"))
}

func (f ConfigurationFileRequest) Validate() error {
	if f.Action != "save" && len(f.ContentEnvelope) != 0 {
		return errors.New("CONFIG_FILE_INPUT_INVALID")
	}
	if f.Action == "list" {
		if f.Name != "" || f.SHA256 != "" || f.Revision != "" {
			return errors.New("CONFIG_FILE_INPUT_INVALID")
		}
		return nil
	}
	if f.Action == "read" && strings.HasPrefix(f.Name, "@") && len(f.Name) <= 80 && !strings.ContainsAny(f.Name, "/\\\r\n\x00") && f.SHA256 == "" && f.Revision == "" {
		return nil
	}
	if !configurationFileName.MatchString(f.Name) {
		return errors.New("CONFIG_FILE_NAME_INVALID")
	}
	if f.Action == "read" && f.SHA256 == "" && f.Revision == "" {
		return nil
	}
	if !sha.MatchString(f.Revision) && (f.Action != "save" || !ConfigurationFileCreationRevision(f.Revision)) {
		return errors.New("CONFIG_FILE_REVISION_INVALID")
	}
	switch f.Action {
	case "save":
		if !sha.MatchString(f.SHA256) || len(f.ContentEnvelope) < 29 || len(f.ContentEnvelope) > ConfigurationFileLimit+28 {
			return errors.New("CONFIG_FILE_CONTENT_INVALID")
		}
	case "enable", "disable", "delete":
		if f.SHA256 != "" || len(f.ContentEnvelope) != 0 {
			return errors.New("CONFIG_FILE_INPUT_INVALID")
		}
	default:
		return errors.New("CONFIG_FILE_INPUT_INVALID")
	}
	return nil
}

func ValidConfigurationFileBody(content string) bool {
	return len(content) <= ConfigurationFileLimit && utf8.ValidString(content) && strings.TrimSpace(content) != "" && !strings.ContainsAny(content, "\r\x00")
}

func (r ManagementRequest) ValidateConfigurationFile() error {
	if r.ConfigFile == nil {
		if r.OperationKey == "config-file-read" || r.OperationKey == "config-file-save" {
			return errors.New("CONFIG_FILE_INPUT_REQUIRED")
		}
		return nil
	}
	if r.Definition.Key != "nginx" || !IsBuiltinCommand(r.Definition) || r.ConfigFile.Validate() != nil {
		return errors.New("CONFIG_FILE_INPUT_INVALID")
	}
	op, err := r.Definition.ManagementOperation(r.OperationKey)
	if err != nil || len(op.Steps) != 1 || op.Steps[0].Script != ConfigurationFileScript || op.Steps[0].RunAs != "ROOT" || len(op.Parameters) != 0 || r.OperationKey != "config-file-read" && r.OperationKey != "config-file-save" || op.ReadOnly != (r.OperationKey == "config-file-read") || op.Rebind {
		return errors.New("CONFIG_FILE_OPERATION_INVALID")
	}
	if op.ReadOnly != (r.ConfigFile.Action == "list" || r.ConfigFile.Action == "read") {
		return errors.New("CONFIG_FILE_OPERATION_INVALID")
	}
	if !op.ReadOnly && (!reflect.DeepEqual(op.VerifySteps, op.Steps) || !reflect.DeepEqual(op.RollbackSteps, op.Steps)) {
		return errors.New("CONFIG_FILE_OPERATION_INVALID")
	}
	return nil
}
