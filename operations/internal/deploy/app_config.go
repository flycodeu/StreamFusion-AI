package deploy

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	AppConfigListAction        = "APP_CONFIG_LIST"
	AppConfigReadAction        = "APP_CONFIG_READ"
	AppConfigWriteAction       = "APP_CONFIG_WRITE"
	AppConfigDeleteAction      = "APP_CONFIG_DELETE"
	AppConfigBodySecretKey     = "appConfigBody"
	AppConfigCredSecretPrefix  = "appConfigCred:"
	AppConfigCapabilityVersion = 1
	AppConfigMaxBytes          = 128 << 10
)

type AppConfigRequest struct {
	AppSlug          string            `json:"appSlug"`
	Filename         string            `json:"filename,omitempty"`
	ExpectedRevision string            `json:"expectedRevision,omitempty"`
	ContentSHA256    string            `json:"contentSha256,omitempty"`
	ContentEnvelope  []byte            `json:"contentEnvelope,omitempty"`
	CredentialRefs   map[string]string `json:"credentialRefs,omitempty"`
}

type AppConfigFileSummary struct {
	Filename   string `json:"filename"`
	Path       string `json:"path"`
	Revision   string `json:"revision"`
	ByteSize   int64  `json:"byteSize"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
}

type AppConfigResult struct {
	ConfigDir string                 `json:"configDir"`
	Path      string                 `json:"path,omitempty"`
	Filename  string                 `json:"filename,omitempty"`
	Exists    bool                   `json:"exists"`
	Content   string                 `json:"content,omitempty"`
	Revision  string                 `json:"revision,omitempty"`
	Files     []AppConfigFileSummary `json:"files,omitempty"`
	Message   string                 `json:"message,omitempty"`
}

var (
	appConfigSlugPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	appConfigEnvFilePattern   = regexp.MustCompile(`^\.env(\.[A-Za-z0-9_-]{1,32})?$`)
	appConfigStdFilePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)
	appConfigCredPlaceholders = regexp.MustCompile(`\$\{SFOPS_CRED:([^}\r\n\x00]{1,120})\}`)
)

// ValidAppConfigSlug checks a normalized safe directory identifier.
func ValidAppConfigSlug(slug string) bool {
	return appConfigSlugPattern.MatchString(slug)
}

// ValidAppConfigFilename validates safe single-segment config file names.
func ValidAppConfigFilename(name string) bool {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "..") {
		return false
	}
	return appConfigEnvFilePattern.MatchString(name) || appConfigStdFilePattern.MatchString(name)
}

// ExtractAppConfigCredentialRefs returns unique placeholder names referenced by ${SFOPS_CRED:...}.
func ExtractAppConfigCredentialRefs(content string) []string {
	matches := appConfigCredPlaceholders.FindAllStringSubmatch(content, 64)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, name)
	}
	return refs
}

// ExpandAppConfigCredentials replaces ${SFOPS_CRED:<name>} placeholders using secrets map.
func ExpandAppConfigCredentials(template string, secrets map[string]string) (string, error) {
	if !strings.Contains(template, "${SFOPS_CRED:") {
		return template, nil
	}
	var missing bool
	out := appConfigCredPlaceholders.ReplaceAllStringFunc(template, func(raw string) string {
		m := appConfigCredPlaceholders.FindStringSubmatch(raw)
		if len(m) != 2 {
			missing = true
			return ""
		}
		name := strings.TrimSpace(m[1])
		val, ok := secrets[AppConfigCredSecretPrefix+name]
		if !ok {
			missing = true
			return ""
		}
		return val
	})
	if missing {
		return "", errors.New("APP_CONFIG_CREDENTIAL_MISSING")
	}
	return out, nil
}

// ValidateAppConfigContent runs lightweight zero-dependency syntax checks on YAML/JSON/XML/env configs.
func ValidateAppConfigContent(filename, content string) (int, error) {
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return 1, errors.New("APP_CONFIG_CONTENT_INVALID")
	}
	if strings.TrimSpace(content) == "" || len(content) > AppConfigMaxBytes {
		return 1, errors.New("APP_CONFIG_CONTENT_INVALID")
	}
	normalized := NormalizeConfigText(content)
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml"):
		return validateAppYAML(normalized)
	case strings.HasSuffix(lower, ".json"):
		if !json.Valid([]byte(normalized)) {
			return 1, errors.New("APP_CONFIG_JSON_INVALID")
		}
	case strings.HasSuffix(lower, ".xml"):
		dec := xml.NewDecoder(strings.NewReader(normalized))
		for {
			if _, err := dec.Token(); err != nil {
				if err == io.EOF {
					break
				}
				var synErr *xml.SyntaxError
				if errors.As(err, &synErr) && synErr.Line > 0 {
					return synErr.Line, errors.New("APP_CONFIG_XML_INVALID")
				}
				return 1, errors.New("APP_CONFIG_XML_INVALID")
			}
		}
	}
	return 0, nil
}

func validateAppYAML(content string) (int, error) {
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		lineNo := idx + 1
		for i := 0; i < len(line); i++ {
			if line[i] == ' ' {
				continue
			}
			if line[i] == '\t' {
				return lineNo, errors.New("APP_CONFIG_YAML_TAB_INDENT")
			}
			break
		}
		if strings.Contains(line, "${SFOPS_CRED:") && !appConfigCredPlaceholders.MatchString(line) {
			return lineNo, errors.New("APP_CONFIG_YAML_SYNTAX_INVALID")
		}
	}
	return 0, nil
}

func validateAppConfigRequest(req Request) error {
	a := req.AppConfig
	if a == nil || !ValidAppConfigSlug(a.AppSlug) {
		return errors.New("APP_CONFIG_INPUT_INVALID")
	}
	switch req.Action {
	case AppConfigListAction:
		if a.Filename != "" {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
		return nil
	case AppConfigReadAction:
		if !ValidAppConfigFilename(a.Filename) {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
		return nil
	case AppConfigWriteAction:
		if !ValidAppConfigFilename(a.Filename) || !Hex.MatchString(a.ContentSHA256) {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
		if a.ExpectedRevision != "" && a.ExpectedRevision != "ABSENT" && !Hex.MatchString(a.ExpectedRevision) {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
		return nil
	case AppConfigDeleteAction:
		if !ValidAppConfigFilename(a.Filename) {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
		if a.ExpectedRevision != "" && !Hex.MatchString(a.ExpectedRevision) {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
		return nil
	default:
		return errors.New("APP_CONFIG_INPUT_INVALID")
	}
}

func validateAppConfigSecrets(req Request, values map[string]string) error {
	a := req.AppConfig
	if a == nil {
		return nil
	}
	if req.Action != AppConfigWriteAction {
		if len(values) != 0 {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
		return nil
	}
	body, present := values[AppConfigBodySecretKey]
	if !present {
		return errors.New("APP_CONFIG_CONTENT_INVALID")
	}
	normalized := NormalizeConfigText(body)
	if ContentSHA256Hex(normalized) != a.ContentSHA256 {
		return errors.New("APP_CONFIG_CONTENT_INVALID")
	}
	if _, err := ValidateAppConfigContent(a.Filename, normalized); err != nil {
		return err
	}
	refs := ExtractAppConfigCredentialRefs(normalized)
	allowed := map[string]bool{AppConfigBodySecretKey: true}
	for _, name := range refs {
		key := AppConfigCredSecretPrefix + name
		if _, ok := values[key]; !ok {
			return errors.New("APP_CONFIG_CREDENTIAL_MISSING")
		}
		allowed[key] = true
	}
	for k := range values {
		if !allowed[k] {
			return errors.New("APP_CONFIG_INPUT_INVALID")
		}
	}
	return nil
}

func applyAppConfigOp(execRoot string, req Request, secrets map[string]string) (AppConfigResult, error) {
	if err := validateAppConfigRequest(req); err != nil {
		return AppConfigResult{}, err
	}
	a := req.AppConfig
	configDir := filepath.Join(execRoot, "apps", a.AppSlug, "config")
	templatesDir := filepath.Join(configDir, ".templates")
	backupDir := filepath.Join(configDir, ".backup")
	result := AppConfigResult{ConfigDir: filepath.ToSlash(configDir)}

	switch req.Action {
	case AppConfigListAction:
		entries, err := os.ReadDir(configDir)
		if err != nil {
			if os.IsNotExist(err) {
				result.Files = []AppConfigFileSummary{}
				return result, nil
			}
			return result, err
		}
		files := make([]AppConfigFileSummary, 0, len(entries))
		for _, entry := range entries {
			name := entry.Name()
			if !ValidAppConfigFilename(name) || entry.IsDir() {
				continue
			}
			runtimePath := filepath.Join(configDir, name)
			info, err := os.Lstat(runtimePath)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			tplPath := filepath.Join(templatesDir, name)
			sourcePath := runtimePath
			if tplInfo, tplErr := os.Lstat(tplPath); tplErr == nil && tplInfo.Mode().IsRegular() && tplInfo.Mode()&os.ModeSymlink == 0 {
				sourcePath = tplPath
			}
			_, rev, exists, readErr := readExistingRegularFile(sourcePath, AppConfigMaxBytes)
			if readErr != nil || !exists {
				continue
			}
			files = append(files, AppConfigFileSummary{
				Filename:   name,
				Path:       filepath.ToSlash(runtimePath),
				Revision:   rev,
				ByteSize:   info.Size(),
				ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Filename < files[j].Filename })
		result.Files = files
		return result, nil

	case AppConfigReadAction:
		runtimePath := filepath.Join(configDir, a.Filename)
		tplPath := filepath.Join(templatesDir, a.Filename)
		sourcePath := runtimePath
		if tplInfo, err := os.Lstat(tplPath); err == nil && tplInfo.Mode().IsRegular() && tplInfo.Mode()&os.ModeSymlink == 0 {
			sourcePath = tplPath
		}
		content, rev, exists, err := readExistingRegularFile(sourcePath, AppConfigMaxBytes)
		if err != nil {
			return result, err
		}
		result.Filename = a.Filename
		result.Path = filepath.ToSlash(runtimePath)
		result.Exists = exists
		result.Content = content
		result.Revision = rev
		return result, nil

	case AppConfigWriteAction:
		if err := validateAppConfigSecrets(req, secrets); err != nil {
			return result, err
		}
		runtimePath := filepath.Join(configDir, a.Filename)
		tplPath := filepath.Join(templatesDir, a.Filename)
		sourcePath := runtimePath
		if tplInfo, err := os.Lstat(tplPath); err == nil && tplInfo.Mode().IsRegular() && tplInfo.Mode()&os.ModeSymlink == 0 {
			sourcePath = tplPath
		}
		existingContent, currentRev, exists, err := readExistingRegularFile(sourcePath, AppConfigMaxBytes)
		if err != nil {
			return result, err
		}
		if a.ExpectedRevision != "" && a.ExpectedRevision != currentRev {
			return result, errors.New("APP_CONFIG_CONFLICT")
		}
		body := NormalizeConfigText(secrets[AppConfigBodySecretKey])
		expanded, err := ExpandAppConfigCredentials(body, secrets)
		if err != nil {
			return result, err
		}
		if exists {
			_ = atomicWriteFile(filepath.Join(backupDir, a.Filename+".bak"), []byte(existingContent), 0700, 0600)
		}
		if err := atomicWriteFile(runtimePath, []byte(expanded), 0700, 0600); err != nil {
			return result, err
		}
		if strings.Contains(body, "${SFOPS_CRED:") {
			if err := atomicWriteFile(tplPath, []byte(body), 0700, 0600); err != nil {
				return result, err
			}
		} else {
			_ = os.Remove(tplPath)
		}
		rev := ContentSHA256Hex(body)
		result.Filename = a.Filename
		result.Path = filepath.ToSlash(runtimePath)
		result.Exists = true
		result.Content = body
		result.Revision = rev
		result.Message = "配置文件已同步至目标服务器"
		return result, nil

	case AppConfigDeleteAction:
		runtimePath := filepath.Join(configDir, a.Filename)
		tplPath := filepath.Join(templatesDir, a.Filename)
		sourcePath := runtimePath
		if tplInfo, err := os.Lstat(tplPath); err == nil && tplInfo.Mode().IsRegular() && tplInfo.Mode()&os.ModeSymlink == 0 {
			sourcePath = tplPath
		}
		existingContent, currentRev, exists, err := readExistingRegularFile(sourcePath, AppConfigMaxBytes)
		if err != nil {
			return result, err
		}
		if !exists {
			result.Filename = a.Filename
			result.Path = filepath.ToSlash(runtimePath)
			result.Exists = false
			return result, nil
		}
		if a.ExpectedRevision != "" && a.ExpectedRevision != currentRev {
			return result, errors.New("APP_CONFIG_CONFLICT")
		}
		_ = atomicWriteFile(filepath.Join(backupDir, a.Filename+".bak"), []byte(existingContent), 0700, 0600)
		if err := os.Remove(runtimePath); err != nil && !os.IsNotExist(err) {
			return result, err
		}
		_ = os.Remove(tplPath)
		result.Filename = a.Filename
		result.Path = filepath.ToSlash(runtimePath)
		result.Exists = false
		result.Message = "配置文件已移除（已保留备份）"
		return result, nil
	}
	return result, errors.New("APP_CONFIG_INPUT_INVALID")
}

func (x *executor) executeAppConfig(req Request, output io.Writer) error {
	if err := x.verifyCommandRequest(req); err != nil {
		return err
	}
	lock, err := targetLock(x.root)
	if err != nil {
		return err
	}
	defer lock.Close()
	if info, statErr := os.Lstat(x.root); statErr != nil || secureAncestors(x.root) != nil || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("EXECUTOR_PATH_NOT_PROTECTED")
	}
	if req.AppConfig != nil && ValidAppConfigSlug(req.AppConfig.AppSlug) {
		if err := secureNearestAncestor(filepath.Join(x.root, "apps", req.AppConfig.AppSlug, "config")); err != nil {
			return err
		}
	}
	result, err := applyAppConfigOp(x.root, req, x.secrets)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
