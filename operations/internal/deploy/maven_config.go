package deploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MavenConfigReadAction        = "SOFTWARE_MAVEN_CONFIG_READ"
	MavenConfigWriteAction       = "SOFTWARE_MAVEN_CONFIG_WRITE"
	MavenConfigSecretKey         = "mavenSettingsBody"
	MavenConfigCapabilityVersion = 1
	MavenConfigMaxBytes          = 64 << 10
)

type MavenConfigRequest struct {
	SoftwareID       string `json:"softwareId"`
	RuntimeMode      string `json:"runtimeMode"`
	InstallRoot      string `json:"installRoot"`
	ExpectedRevision string `json:"expectedRevision,omitempty"`
	ContentSHA256    string `json:"contentSha256,omitempty"`
	ContentEnvelope  []byte `json:"contentEnvelope,omitempty"`
}

type MavenConfigResult struct {
	Path      string `json:"path"`
	Scope     string `json:"scope"`
	Exists    bool   `json:"exists"`
	Content   string `json:"content"`
	Revision  string `json:"revision"`
	HasSecret bool   `json:"hasSecret"`
	Message   string `json:"message,omitempty"`
}

var mavenSecretElementPattern = regexp.MustCompile(`(?is)<(password|passphrase)\b[^>]*>[^<\s][^<]*</(password|passphrase)>`)

// ContentSHA256Hex returns the SHA-256 hex digest of UTF-8 file text.
func ContentSHA256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// NormalizeConfigText canonicalizes CRLF line endings before digesting or saving.
func NormalizeConfigText(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

// MavenSettingsHasSecret detects non-empty XML password or passphrase tags.
func MavenSettingsHasSecret(content string) bool {
	return mavenSecretElementPattern.MatchString(content)
}

// ValidateMavenSettingsXML parses settings.xml via stdlib encoding/xml and returns any syntax line number.
func ValidateMavenSettingsXML(content string) (int, error) {
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return 1, errors.New("MAVEN_SETTINGS_CONTENT_INVALID")
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || len(content) > MavenConfigMaxBytes {
		return 1, errors.New("MAVEN_SETTINGS_CONTENT_INVALID")
	}
	dec := xml.NewDecoder(strings.NewReader(content))
	rootChecked := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			var synErr *xml.SyntaxError
			if errors.As(err, &synErr) && synErr.Line > 0 {
				return synErr.Line, errors.New("MAVEN_SETTINGS_XML_INVALID")
			}
			return 1, errors.New("MAVEN_SETTINGS_XML_INVALID")
		}
		if start, ok := tok.(xml.StartElement); ok && !rootChecked {
			rootChecked = true
			if !strings.EqualFold(start.Name.Local, "settings") {
				return 1, errors.New("MAVEN_SETTINGS_ROOT_INVALID")
			}
		}
	}
	if !rootChecked {
		return 1, errors.New("MAVEN_SETTINGS_ROOT_INVALID")
	}
	return 0, nil
}

func validateMavenConfigRequest(req Request) error {
	m := req.MavenConfig
	if m == nil || !ID.MatchString(m.SoftwareID) {
		return errors.New("MAVEN_CONFIG_INPUT_INVALID")
	}
	switch m.RuntimeMode {
	case "DOCKER", "NATIVE", "OFFICIAL":
	default:
		return errors.New("MAVEN_CONFIG_INPUT_INVALID")
	}
	if m.RuntimeMode != "OFFICIAL" {
		if m.InstallRoot == "" || strings.Contains(m.InstallRoot, "..") || strings.ContainsAny(m.InstallRoot, "\r\n\x00") {
			return errors.New("MAVEN_CONFIG_PATH_INVALID")
		}
		posixAbs := strings.HasPrefix(m.InstallRoot, "/") && path.Clean(m.InstallRoot) != "/"
		localAbs := filepath.IsAbs(filepath.FromSlash(m.InstallRoot)) && filepath.Clean(filepath.FromSlash(m.InstallRoot)) != string(filepath.Separator)
		if !posixAbs && !localAbs {
			return errors.New("MAVEN_CONFIG_PATH_INVALID")
		}
	}
	if req.Action == MavenConfigWriteAction {
		if !Hex.MatchString(m.ContentSHA256) {
			return errors.New("MAVEN_SETTINGS_CONTENT_INVALID")
		}
		if m.ExpectedRevision != "" && m.ExpectedRevision != "ABSENT" && !Hex.MatchString(m.ExpectedRevision) {
			return errors.New("MAVEN_CONFIG_INPUT_INVALID")
		}
	} else if req.Action != MavenConfigReadAction {
		return errors.New("MAVEN_CONFIG_INPUT_INVALID")
	}
	return nil
}

func validateMavenConfigSecrets(req Request, values map[string]string) error {
	m := req.MavenConfig
	if m == nil {
		return nil
	}
	body, present := values[MavenConfigSecretKey]
	if req.Action == MavenConfigReadAction {
		if len(values) != 0 {
			return errors.New("MAVEN_CONFIG_INPUT_INVALID")
		}
		return nil
	}
	normalized := NormalizeConfigText(body)
	if !present || len(values) != 1 || ContentSHA256Hex(normalized) != m.ContentSHA256 {
		return errors.New("MAVEN_SETTINGS_CONTENT_INVALID")
	}
	if _, err := ValidateMavenSettingsXML(normalized); err != nil {
		return err
	}
	return nil
}

func resolveMavenSettingsPath(req *MavenConfigRequest) (string, string, error) {
	if req.RuntimeMode == "OFFICIAL" {
		return filepath.FromSlash("/etc/maven/settings.xml"), "OFFICIAL", nil
	}
	cleanRoot := filepath.Clean(filepath.FromSlash(req.InstallRoot))
	if !filepath.IsAbs(cleanRoot) || cleanRoot == string(filepath.Separator) || strings.Contains(req.InstallRoot, "..") {
		return "", "", errors.New("MAVEN_CONFIG_PATH_INVALID")
	}
	if req.RuntimeMode == "NATIVE" {
		return filepath.Join(cleanRoot, "runtime", "maven", "conf", "settings.xml"), "NATIVE", nil
	}
	return filepath.Join(cleanRoot, "config", "settings.xml"), "DOCKER", nil
}

func atomicWriteFile(target string, data []byte, dirPerm, filePerm os.FileMode) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-ops-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(filePerm)
	}
	if err == nil {
		err = f.Sync()
	}
	if ce := f.Close(); err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, target); err != nil {
		return err
	}
	cleanup = false
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func readExistingRegularFile(target string, maxBytes int64) (string, string, bool, error) {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "ABSENT", false, nil
		}
		return "", "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", "", false, errors.New("CONFIG_FILE_UNSAFE")
	}
	if info.Size() > maxBytes {
		return "", "", false, errors.New("CONFIG_FILE_UNSUPPORTED")
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return "", "", false, err
	}
	if bytes.Contains(raw, []byte{0}) || !utf8.Valid(raw) {
		return "", "", false, errors.New("CONFIG_FILE_UNSUPPORTED")
	}
	text := NormalizeConfigText(string(raw))
	return text, ContentSHA256Hex(text), true, nil
}

func applyMavenConfigOp(execRoot string, req Request, body string) (MavenConfigResult, error) {
	if err := validateMavenConfigRequest(req); err != nil {
		return MavenConfigResult{}, err
	}
	targetPath, scope, err := resolveMavenSettingsPath(req.MavenConfig)
	if err != nil {
		return MavenConfigResult{}, err
	}
	currentContent, currentRev, exists, err := readExistingRegularFile(targetPath, MavenConfigMaxBytes)
	if err != nil {
		return MavenConfigResult{}, err
	}
	if req.Action == MavenConfigReadAction {
		return MavenConfigResult{
			Path:      filepath.ToSlash(targetPath),
			Scope:     scope,
			Exists:    exists,
			Content:   currentContent,
			Revision:  currentRev,
			HasSecret: MavenSettingsHasSecret(currentContent),
		}, nil
	}
	if expected := req.MavenConfig.ExpectedRevision; expected != "" && expected != currentRev {
		return MavenConfigResult{}, errors.New("MAVEN_CONFIG_CONFLICT")
	}
	normalized := NormalizeConfigText(body)
	if ContentSHA256Hex(normalized) != req.MavenConfig.ContentSHA256 {
		return MavenConfigResult{}, errors.New("MAVEN_SETTINGS_CONTENT_INVALID")
	}
	if _, err := ValidateMavenSettingsXML(normalized); err != nil {
		return MavenConfigResult{}, err
	}
	if exists {
		_ = atomicWriteFile(targetPath+".bak", []byte(currentContent), 0755, 0600)
	}
	hasSecret := MavenSettingsHasSecret(normalized)
	perm := os.FileMode(0644)
	if hasSecret {
		perm = 0600
	}
	if err := atomicWriteFile(targetPath, []byte(normalized), 0755, perm); err != nil {
		return MavenConfigResult{}, err
	}
	if scope == "DOCKER" && execRoot != "" {
		mirrorPath := filepath.Join(execRoot, "config", "maven-settings.xml")
		if info, statErr := os.Lstat(mirrorPath); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return MavenConfigResult{}, errors.New("CONFIG_FILE_UNSAFE")
		}
		if err := atomicWriteFile(mirrorPath, []byte(normalized), 0755, perm); err != nil {
			return MavenConfigResult{}, fmt.Errorf("MAVEN_CONFIG_MIRROR_FAILED: %w", err)
		}
	}
	rev := ContentSHA256Hex(normalized)
	return MavenConfigResult{
		Path:      filepath.ToSlash(targetPath),
		Scope:     scope,
		Exists:    true,
		Content:   normalized,
		Revision:  rev,
		HasSecret: hasSecret,
		Message:   "Maven settings.xml 已保存",
	}, nil
}

func secureNearestAncestor(targetPath string) error {
	p := filepath.Clean(targetPath)
	for {
		info, err := os.Lstat(p)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("EXECUTOR_PATH_NOT_PROTECTED")
			}
			return secureAncestors(p)
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return errors.New("EXECUTOR_PATH_NOT_PROTECTED")
		}
		p = parent
	}
}

func (x *executor) executeMavenConfig(req Request, output io.Writer) error {
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
	if req.MavenConfig != nil {
		if targetPath, _, pathErr := resolveMavenSettingsPath(req.MavenConfig); pathErr == nil {
			if err := secureNearestAncestor(targetPath); err != nil {
				return err
			}
		}
	}
	body := x.secrets[MavenConfigSecretKey]
	result, err := applyMavenConfigOp(x.root, req, body)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
