package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var projectJava21 = regexp.MustCompile(`(?m)(?:java|openjdk) version "21(?:[".+-])`)
var projectNginxInclude = regexp.MustCompile(`(?m)^\s*include\s+/etc/nginx/conf\.d/\*\.conf\s*;`)

func projectCommand(ctx context.Context, filename string, args ...string) (string, error) {
	// OS packages may expose alternatives via symlinks. Execute the resolved
	// root-owned binary after verifying access to it and its ancestors.
	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil || secureAncestors(resolved) != nil {
		return "", errors.New("PROJECT_RUNTIME_PATH_UNSAFE")
	}
	child, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, resolved, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=cat"}
	b := &cappedBuffer{limit: 24 << 10}
	cmd.Stdout = b
	cmd.Stderr = b
	err = cmd.Run()
	return b.String(), err
}

func projectCheckTools(ctx context.Context, p ProjectDeployment) error {
	if _, err := projectCommand(ctx, "/usr/bin/systemctl", "--version"); err != nil {
		return errors.New("PROJECT_SYSTEMD_REQUIRED")
	}
	if p.Kind == "JAVA" {
		out, err := projectCommand(ctx, "/usr/bin/java", "-version")
		if err != nil || !projectJava21.MatchString(out) {
			return errors.New("PROJECT_JAVA_21_REQUIRED")
		}
	} else {
		data, err := projectReadSmall("/etc/nginx/nginx.conf", 256<<10)
		if err != nil || !projectNginxInclude.Match(data) {
			return errors.New("PROJECT_NGINX_CONF_D_REQUIRED")
		}
		if _, err = projectCommand(ctx, "/usr/bin/systemctl", "is-active", "nginx.service"); err != nil {
			return errors.New("PROJECT_NGINX_SERVICE_REQUIRED")
		}
		if _, err = projectCommand(ctx, "/usr/sbin/nginx", "-t"); err != nil {
			return errors.New("PROJECT_NGINX_EXISTING_CONFIG_INVALID")
		}
		if p.Site != nil && p.Site.HealthPath != "" && !projectSiteUpstreamHealth(ctx, p.Site) {
			return errors.New("PROJECT_STREAMFUSION_API_UNHEALTHY")
		}
	}
	for _, d := range p.DependencyChecks {
		c, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(d.Host, strconv.Itoa(d.Port)))
		if err != nil {
			return errors.New("PROJECT_DEPENDENCY_UNREACHABLE")
		}
		c.Close()
	}
	return nil
}

func projectUnit(id string) string { return "sfops-project-" + id + ".service" }

const projectJavaLogField = "SFOPS_PROJECT_RELEASE="

func projectJavaUnit(p ProjectDeployment, dir string, legacy bool) string {
	name := "sfops-project-" + p.ID
	profile := ""
	if p.Profile != "" {
		profile = " --spring.profiles.active=" + p.Profile
	}
	serviceOptions := ""
	if !legacy {
		serviceOptions = "SuccessExitStatus=143\nLogExtraFields=" + projectJavaLogField + p.ReleaseID + "\n"
	}
	return fmt.Sprintf(`# StreamFusion Ops deployment %s
[Unit]
Description=StreamFusion Ops Java %s
After=network-online.target
[Service]
Type=simple
DynamicUser=yes
StateDirectory=%s
WorkingDirectory=/var/lib/%s
EnvironmentFile=%s/runtime.env
ExecStart=/usr/bin/java -Xmx%dm -jar %s/app.jar --server.address=%s --server.port=%d --spring.config.additional-location=optional:file:%s/config/%s
Restart=on-failure
RestartSec=3
%sNoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
UMask=0077
MemoryMax=%dM
[Install]
WantedBy=multi-user.target
`, p.ID, p.ID, name, name, dir, p.MemoryMiB*3/4, dir, p.Address, p.Port, dir, profile, serviceOptions, p.MemoryMiB)
}

func projectJavaStartupLogs(ctx context.Context, p ProjectDeployment, dir string, since time.Time) string {
	secrets, err := projectLogSecrets(dir)
	if err != nil {
		return ""
	}
	// A failed JVM can exit or restart before systemctl reports its invocation.
	// Use the service manager's release tag and this attempt's start time.
	from := fmt.Sprintf("@%d.%06d", since.Unix(), since.Nanosecond()/1000)
	output, err := projectCommand(ctx, "/usr/bin/journalctl", "--unit", projectUnit(p.ID), "--boot",
		"--since", from, "--no-pager", "--quiet", "--lines=35", "--output=short-iso", projectJavaLogField+p.ReleaseID)
	if err != nil {
		return ""
	}
	// The command output is capped. Drop an incomplete line before redacting
	// so a credential cut at the capture boundary cannot escape as a prefix.
	cut := strings.LastIndexByte(output, '\n')
	if cut < 0 {
		return ""
	}
	return strings.TrimSpace(redactProjectLogs(output[:cut], secrets))
}

var projectJavaStateLine = regexp.MustCompile(`^(?:ActiveState|SubState|Result|ExecMainCode|ExecMainStatus)=[a-z0-9-]{1,40}$`)

func projectJavaStartupState(ctx context.Context, id string) string {
	output, err := projectCommand(ctx, "/usr/bin/systemctl", "show",
		"--property=ActiveState,SubState,Result,ExecMainCode,ExecMainStatus", projectUnit(id))
	if err != nil {
		return ""
	}
	var lines []string
	for line := range strings.SplitSeq(output, "\n") {
		if projectJavaStateLine.MatchString(line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " ")
}

func projectHealth(ctx context.Context, p ProjectDeployment, dir string) bool {
	if p.Kind == "STATIC" {
		if !projectStaticReleaseServed(ctx, p) {
			return false
		}
		return p.Site.HealthPath == "" || projectJSONHealth(ctx, projectHealthClient(), projectLocalSite(p)+p.Site.HealthPath, p.Site.ServerName)
	}
	address := p.Address
	if address == "0.0.0.0" {
		address = "127.0.0.1"
	}
	if !projectJavaUnitRuns(ctx, p.ID, dir) {
		return false
	}
	base := "http://" + net.JoinHostPort(address, strconv.Itoa(p.Port))
	return projectJavaEndpointHealth(ctx, base, p.HealthPath)
}

func projectJavaUnitRuns(ctx context.Context, id, dir string) bool {
	pid, err := projectCommand(ctx, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", projectUnit(id))
	mainPID, parseErr := strconv.Atoi(strings.TrimSpace(pid))
	return err == nil && parseErr == nil && mainPID > 1 && projectJavaProcess(mainPID, dir)
}

func projectJavaProcess(pid int, dir string) bool {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	defer f.Close()
	args, err := io.ReadAll(io.LimitReader(f, 16<<10))
	if err != nil || len(args) == 16<<10 {
		return false
	}
	fields := strings.Split(string(args), "\x00")
	for n := 0; n+1 < len(fields); n++ {
		if fields[n] == "-jar" && fields[n+1] == filepath.Join(dir, "app.jar") {
			return true
		}
	}
	return false
}

func projectControlFile(p ProjectDeployment) string {
	if p.Kind == "STATIC" {
		return "/etc/nginx/conf.d/sfops-project-" + p.ID + ".conf"
	}
	return filepath.Join("/etc/systemd/system", projectUnit(p.ID))
}

func projectReload(ctx context.Context, p ProjectDeployment, remove bool) error {
	if p.Kind == "STATIC" {
		if _, err := projectCommand(ctx, "/usr/sbin/nginx", "-t"); err != nil {
			return errors.New("PROJECT_NGINX_CHECK_FAILED")
		}
		if _, err := projectCommand(ctx, "/usr/bin/systemctl", "reload", "nginx.service"); err != nil {
			return errors.New("PROJECT_NGINX_RELOAD_FAILED")
		}
		return nil
	}
	if _, err := projectCommand(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return errors.New("PROJECT_SYSTEMD_RELOAD_FAILED")
	}
	if !remove {
		if _, err := projectCommand(ctx, "/usr/bin/systemctl", "enable", projectUnit(p.ID)); err != nil {
			return errors.New("PROJECT_JAVA_ENABLE_FAILED")
		}
		if _, err := projectCommand(ctx, "/usr/bin/systemctl", "restart", projectUnit(p.ID)); err != nil {
			return errors.New("PROJECT_JAVA_START_FAILED")
		}
	}
	return nil
}

func projectNginxLogs(id string) (string, error) {
	dir := filepath.Join("/var/log/nginx/sfops", id)
	if err := secureAncestors(dir); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, name := range []string{"error.log", "access.log"} {
		file := filepath.Join(dir, name)
		info, err := os.Lstat(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("PROJECT_LOGS_UNAVAILABLE")
		}
		f, err := os.Open(file)
		if err != nil {
			return "", err
		}
		_, _ = f.Seek(max(0, info.Size()-(12<<10)), io.SeekStart)
		fmt.Fprintf(&out, "[%s]\n", name)
		_, _ = io.Copy(&out, io.LimitReader(f, 12<<10))
		f.Close()
	}
	return out.String(), nil
}

func validateProjectLogFile(file string) error {
	info, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("PROJECT_LOG_PATH_UNSAFE")
	}
	return nil
}

// These files were generated by projectEnvironment and remain root-only.
func projectLogSecrets(dir string) ([]string, error) {
	var keys []string
	if err := projectReadRecord(filepath.Join(dir, ".log-redaction.json"), &keys); err != nil {
		return nil, err
	}
	raw, err := projectReadSmall(filepath.Join(dir, "runtime.env"), 960<<10)
	if err != nil {
		return nil, err
	}
	required := make(map[string]bool, len(keys))
	for _, key := range keys {
		required[key] = true
	}
	secrets := make([]string, 0, len(keys))
	unescape := strings.NewReplacer("\\\\", "\\", "\\\"", "\"", "\\$", "$", "\\`", "`")
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, quoted, ok := strings.Cut(line, "=")
		if !ok || !required[key] {
			continue
		}
		if len(quoted) <= 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
			return nil, errors.New("PROJECT_LOGS_UNAVAILABLE")
		}
		secrets = append(secrets, unescape.Replace(quoted[1:len(quoted)-1]))
		delete(required, key)
	}
	if len(required) != 0 {
		return nil, errors.New("PROJECT_LOGS_UNAVAILABLE")
	}
	return secrets, nil
}

func redactProjectLogs(value string, secrets []string) string {
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	pairs := make([]string, 0, 2*len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		pairs = append(pairs, secret, "[REDACTED]")
		if b, err := json.Marshal(secret); err == nil && string(b[1:len(b)-1]) != secret {
			pairs = append(pairs, string(b[1:len(b)-1]), "[REDACTED]")
		}
	}
	return RedactSoftwareLogs(strings.NewReplacer(pairs...).Replace(value))
}
