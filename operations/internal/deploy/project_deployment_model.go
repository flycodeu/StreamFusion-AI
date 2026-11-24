package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const (
	ProjectDeployVersion = 2
	ProjectProbe         = "PROJECT_DEPLOY_PROBE"
	ProjectApply         = "PROJECT_DEPLOY_APPLY"
	ProjectRollback      = "PROJECT_DEPLOY_ROLLBACK"
	ProjectObserve       = "PROJECT_DEPLOY_OBSERVE"
	ProjectLogs          = "PROJECT_DEPLOY_LOGS"
)

const ProjectArtifactLimit = JarLimit
const ProjectStaticLimit = ArchiveLimit

type ProjectDeployment struct {
	ID               string              `json:"id"`
	ReleaseID        string              `json:"releaseId,omitempty"`
	ExpectedRelease  string              `json:"expectedRelease,omitempty"`
	Kind             string              `json:"kind"`
	Address          string              `json:"address,omitempty"`
	Port             int                 `json:"port"`
	Profile          string              `json:"profile,omitempty"`
	MemoryMiB        int                 `json:"memoryMiB,omitempty"`
	HealthPath       string              `json:"healthPath,omitempty"`
	ArtifactSHA256   string              `json:"artifactSha256,omitempty"`
	ArtifactSize     int64               `json:"artifactSize,omitempty"`
	Variables        []ProjectVariable   `json:"variables,omitempty"`
	Files            []ProjectConfigFile `json:"files,omitempty"`
	Site             *ProjectSite        `json:"site,omitempty"`
	DependencyChecks []ProjectDependency `json:"dependencyChecks,omitempty"`
}

type ProjectVariable struct {
	Name          string `json:"name"`
	Value         string `json:"value,omitempty"`
	CredentialRef string `json:"credentialRef,omitempty"`
}
type ProjectConfigFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}
type ProjectSite struct {
	ServerName string         `json:"serverName"`
	SPA        bool           `json:"spa"`
	Proxies    []ProjectProxy `json:"proxies,omitempty"`
}
type ProjectProxy struct {
	Location    string `json:"location"`
	URL         string `json:"url"`
	StripPrefix bool   `json:"stripPrefix,omitempty"`
	WebSocket   bool   `json:"webSocket,omitempty"`
	SSE         bool   `json:"sse,omitempty"`
}
type ProjectDependency struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}
type ProjectDeployResult struct {
	Status     string `json:"status"`
	Phase      string `json:"phase"`
	ErrorCode  string `json:"errorCode,omitempty"`
	ObservedAt string `json:"observedAt"`
	ReleaseID  string `json:"releaseId,omitempty"`
	Health     string `json:"health,omitempty"`
	Output     string `json:"output,omitempty"`
}

var (
	projectEnvKey        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)
	projectHost          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
	projectURLPath       = regexp.MustCompile(`^/[A-Za-z0-9/_.~%-]{0,240}$`)
	projectConfigName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,74}\.(yml|yaml|properties)$`)
	projectRef           = regexp.MustCompile(`^[0-9a-f-]{36}:[1-9][0-9]{0,3}$`)
	projectLiteralSecret = regexp.MustCompile(`(?im)^\s*["']?[\w.-]*(password|passwd|secret|token|api[_-]?key|private[_-]?key|credential)["']?\s*[:=]\s*(.*)$`)
)

func ValidateProjectConfigFile(file ProjectConfigFile) error {
	if !projectConfigName.MatchString(file.Filename) || strings.Contains(file.Filename, "..") || len(file.Content) > 32<<10 {
		return errors.New("PROJECT_CONFIG_FILE_INVALID")
	}
	if _, err := ValidateAppConfigContent(file.Filename, file.Content); err != nil {
		return err
	}
	if strings.Contains(file.Content, "PRIVATE KEY-----") {
		return errors.New("PROJECT_CONFIG_CREDENTIAL_REQUIRED")
	}
	for _, m := range projectLiteralSecret.FindAllStringSubmatch(file.Content, -1) {
		v := strings.Trim(strings.TrimSpace(m[2]), `"'`)
		if v != "" && v != "null" && !strings.HasPrefix(v, "#") && !(strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")) {
			return errors.New("PROJECT_CONFIG_CREDENTIAL_REQUIRED")
		}
	}
	return nil
}

// Render a restricted server fragment; repository content cannot become directives.
func (p ProjectDeployment) NginxConfig(docroot, logroot string) (string, error) {
	if p.Site == nil || !(p.Site.ServerName == "_" || projectHost.MatchString(p.Site.ServerName)) || p.Port < 1 || p.Port > 65535 || len(p.Site.Proxies) > 30 {
		return "", errors.New("PROJECT_NGINX_CONFIG_INVALID")
	}
	for _, name := range []string{docroot, logroot} {
		if !strings.HasPrefix(name, "/") || !projectSafePath(name) {
			return "", errors.New("PROJECT_NGINX_PATH_INVALID")
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# StreamFusion Ops deployment %s\nserver {\n    listen %d;\n    server_name %s;\n    root %s;\n    index index.html;\n    access_log %s/access.log;\n    error_log %s/error.log;\n", p.ID, p.Port, p.Site.ServerName, docroot, logroot, logroot)
	fmt.Fprintf(&b, "    location = /__sfops_health { access_log off; add_header X-StreamFusion-Release \"%s\"; alias %s/index.html; }\n", p.ReleaseID, docroot)
	if p.Site.SPA {
		b.WriteString("    location / { try_files $uri $uri/ /index.html; }\n")
	} else {
		b.WriteString("    location / { try_files $uri $uri/ =404; }\n")
	}
	seen := map[string]bool{"/": true, "/__sfops_health": true}
	for _, r := range p.Site.Proxies {
		u, err := url.Parse(r.URL)
		prefix := strings.TrimSuffix(r.Location, "/")
		if err != nil || !projectURLPath.MatchString(r.Location) || strings.Contains(r.Location, "%") || path.Clean(r.Location) != prefix || seen[prefix] || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (!projectHost.MatchString(u.Hostname()) && net.ParseIP(u.Hostname()) == nil) || (u.Path != "" && (!projectURLPath.MatchString(u.Path) || u.RawPath != "" || strings.Contains(u.Path, "%"))) {
			return "", errors.New("PROJECT_PROXY_INVALID")
		}
		seen[prefix] = true
		if !r.StripPrefix && u.Path != "" && u.Path != "/" {
			return "", errors.New("PROJECT_PROXY_INVALID")
		}
		if r.StripPrefix {
			u.Path = strings.TrimRight(u.Path, "/") + "/"
		} else {
			u.Path = ""
		}
		if !strings.HasSuffix(r.Location, "/") {
			projectNginxProxy(&b, "= "+prefix, u.String(), r)
		}
		projectNginxProxy(&b, "^~ "+prefix+"/", u.String(), r)
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func projectNginxProxy(b *strings.Builder, location, upstream string, r ProjectProxy) {
	fmt.Fprintf(b, "    location %s {\n        proxy_pass %s;\n        proxy_http_version 1.1;\n        proxy_set_header Host $host;\n        proxy_set_header X-Forwarded-Proto $scheme;\n        proxy_set_header X-Forwarded-For $remote_addr;\n", location, upstream)
	if r.WebSocket {
		b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n        proxy_set_header Connection \"upgrade\";\n")
	}
	if r.SSE {
		b.WriteString("        proxy_buffering off;\n        proxy_read_timeout 3600s;\n")
	}
	b.WriteString("    }\n")
}

var projectPathPattern = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

func projectSafePath(value string) bool {
	return projectPathPattern.MatchString(value) && path.Clean(value) == value && !strings.Contains(value, "..")
}

func validProjectDeployment(p *ProjectDeployment, action string) error {
	if p == nil || !ID.MatchString(p.ID) || (p.ReleaseID != "" && !ID.MatchString(p.ReleaseID)) || (p.ExpectedRelease != "" && !ID.MatchString(p.ExpectedRelease)) || (p.Kind != "JAVA" && p.Kind != "STATIC") || p.Port < 1 || p.Port > 65535 {
		return errors.New("PROJECT_DEPLOY_CONFIG_INVALID")
	}
	if action == ProjectApply {
		maxSize := int64(ProjectArtifactLimit)
		if p.Kind == "STATIC" {
			maxSize = ProjectStaticLimit
		}
		if p.ReleaseID == "" || !Hex.MatchString(p.ArtifactSHA256) || p.ArtifactSize < 1 || p.ArtifactSize > maxSize {
			return errors.New("PROJECT_ARTIFACT_INVALID")
		}
	}
	if action == ProjectRollback && (p.ReleaseID == "" || p.ExpectedRelease == "" || p.ReleaseID == p.ExpectedRelease) {
		return errors.New("PROJECT_ROLLBACK_RELEASE_INVALID")
	}
	for _, d := range p.DependencyChecks {
		if len(p.DependencyChecks) > 20 || d.Port < 1 || d.Port > 65535 || (!projectHost.MatchString(d.Host) && net.ParseIP(d.Host) == nil) {
			return errors.New("PROJECT_DEPENDENCY_INVALID")
		}
	}
	if p.Kind == "STATIC" {
		if len(p.Files) != 0 || len(p.Variables) != 0 {
			return errors.New("PROJECT_STATIC_CONFIG_UNSUPPORTED")
		}
		_, err := p.NginxConfig("/var/www/sfops/site", "/var/log/nginx/sfops/site")
		return err
	}
	if p.Port < 1024 || net.ParseIP(p.Address).To4() == nil || !profilePattern.MatchString(p.Profile) || p.MemoryMiB < 256 || p.MemoryMiB > 16384 || !projectURLPath.MatchString(p.HealthPath) || p.Site != nil || len(p.Files) > 12 || len(p.Variables) > 48 {
		return errors.New("PROJECT_JAVA_CONFIG_INVALID")
	}
	vars, used := map[string]bool{}, map[string]bool{}
	for _, v := range p.Variables {
		if !projectEnvKey.MatchString(v.Name) || vars[v.Name] || strings.HasPrefix(v.Name, "SFOPS_INTERNAL_") || len(v.Value) > 2048 || strings.ContainsAny(v.Value, "\r\n\x00") || (v.CredentialRef != "" && (!projectRef.MatchString(v.CredentialRef) || v.Value != "")) {
			return errors.New("PROJECT_JAVA_VARIABLE_INVALID")
		}
		vars[v.Name] = true
	}
	total := 0
	for _, f := range p.Files {
		total += len(f.Content)
		if ValidateProjectConfigFile(f) != nil || used[f.Filename] || total > 48<<10 {
			return errors.New("PROJECT_CONFIG_FILE_INVALID")
		}
		used[f.Filename] = true
	}
	return nil
}

func ProjectSecretVar(ref string) string {
	hash := sha256.Sum256([]byte(ref))
	return "SFOPS_INTERNAL_" + strings.ToUpper(hex.EncodeToString(hash[:8]))
}

func validProjectSecrets(p *ProjectDeployment, action string, values map[string]string) error {
	allowed := map[string]bool{}
	if action == ProjectApply {
		for _, v := range p.Variables {
			if v.CredentialRef != "" {
				allowed[v.Name] = true
			}
		}
		for _, f := range p.Files {
			for _, ref := range ExtractAppConfigCredentialRefs(f.Content) {
				allowed[ProjectSecretVar(ref)] = true
			}
		}
	}
	if len(values) != len(allowed) {
		return errors.New("PROJECT_CREDENTIAL_UNAVAILABLE")
	}
	for key, value := range values {
		if !allowed[key] || value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("PROJECT_CREDENTIAL_INVALID")
		}
	}
	return nil
}

func ValidateProjectDeployment(p *ProjectDeployment, action string) error {
	return validProjectDeployment(p, action)
}
