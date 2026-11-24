package controller

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type projectDeploymentContext struct {
	project applicationProject
	module  applicationModule
	env     applicationEnvironment
	draft   applicationDeployment
	host    target
}

func readProjectDeployment(q applicationReader, projectID, deploymentID string) (projectDeploymentContext, error) {
	project, err := readApplicationProject(q, projectID)
	if err != nil {
		return projectDeploymentContext{}, err
	}
	for _, env := range project.Environments {
		for _, draft := range env.Deployments {
			if draft.ID != deploymentID {
				continue
			}
			for _, module := range project.Modules {
				if module.ID == draft.ModuleID {
					out := projectDeploymentContext{project: project, module: module, env: env, draft: draft}
					if draft.HostID != "" {
						out.host, err = scanHost(q.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", draft.HostID))
					}
					return out, err
				}
			}
		}
	}
	return projectDeploymentContext{}, sql.ErrNoRows
}

func (p projectDeploymentContext) kind() string {
	if p.module.Catalog == nil || p.draft.ManagedInstanceID != "" {
		return ""
	}
	switch {
	case p.draft.DeployMode == "NATIVE" && p.module.Catalog.Kind == "BACKEND":
		return "JAVA"
	case p.draft.DeployMode == "STATIC" && p.module.Catalog.Kind == "FRONTEND":
		return "STATIC"
	}
	return ""
}

func normalizeProjectConfigDrafts(drafts []deploy.ProjectConfigFile) error {
	seen := map[string]bool{}
	total := 0
	for _, file := range drafts {
		total += len(file.Content)
		if len(drafts) > 12 || total > 40<<10 {
			return applicationInvalid("Java 配置草稿最多 12 个文件，合计不超过 40 KiB")
		}
		if seen[file.Filename] || deploy.ValidateProjectConfigFile(file) != nil || applicationSecretValue(file.Content) {
			return applicationInvalid("草稿仅支持不重名的 YAML 或 properties，请使用变量或凭据引用，并检查文件格式")
		}
		seen[file.Filename] = true
	}
	return nil
}

var projectConfigPlaceholder = regexp.MustCompile(`\$\{SFOPS_CRED:([^}\r\n\x00]{1,120})\}`)

func projectFreezeConfig(q applicationReader, drafts []deploy.ProjectConfigFile) ([]deploy.ProjectConfigFile, error) {
	var out []deploy.ProjectConfigFile
	for _, file := range drafts {
		var lookupErr error
		file.Content = projectConfigPlaceholder.ReplaceAllStringFunc(file.Content, func(token string) string {
			if lookupErr != nil {
				return token
			}
			name := strings.TrimSpace(projectConfigPlaceholder.FindStringSubmatch(token)[1])
			var id, rev string
			if refID, refRev, ok := strings.Cut(name, ":"); ok && deploy.ID.MatchString(refID) {
				lookupErr = q.QueryRow(`SELECT id,CAST(revision AS TEXT) FROM credential_refs
					WHERE id=? AND CAST(revision AS TEXT)=? AND revoked_at IS NULL`, refID, refRev).Scan(&id, &rev)
			} else {
				lookupErr = q.QueryRow(`SELECT id,CAST(revision AS TEXT) FROM credential_refs
					WHERE (id=? OR name=?) AND kind IN ('PASSWORD','TOKEN') AND revoked_at IS NULL
					ORDER BY CASE kind WHEN 'PASSWORD' THEN 0 ELSE 1 END,revision DESC LIMIT 1`, name, name).Scan(&id, &rev)
			}
			return "${SFOPS_CRED:" + id + ":" + rev + "}"
		})
		if errors.Is(lookupErr, sql.ErrNoRows) || strings.Contains(projectConfigPlaceholder.ReplaceAllString(file.Content, ""), "${SFOPS_CRED:") {
			return nil, errors.New("PROJECT_CREDENTIAL_UNAVAILABLE")
		}
		if lookupErr != nil {
			return nil, lookupErr
		}
		out = append(out, file)
	}
	return out, nil
}

func projectDeploySpec(q applicationReader, p projectDeploymentContext, baseline string) (deploy.ProjectDeployment, error) {
	d := p.draft
	spec := deploy.ProjectDeployment{ID: d.ID, Kind: p.kind(), ExpectedRelease: baseline}
	if spec.Kind == "" {
		return spec, errors.New("PROJECT_MODE_UNSUPPORTED")
	}
	if p.host.ID == "" || p.host.Maintenance {
		return spec, errors.New("PROJECT_HOST_UNAVAILABLE")
	}
	if spec.Kind == "STATIC" {
		if d.Nginx == nil {
			return spec, errors.New("PROJECT_NGINX_CONFIG_INVALID")
		}
		if d.Nginx.NginxSoftwareID != "" {
			var matches bool
			err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM software_instances WHERE id=? AND host_id=?
				AND runtime_kind='SYSTEMD' AND locator='nginx.service' AND upper(software_type)='NGINX'
				AND management_mode<>'RELEASED' AND installation_status<>'UNINSTALLED')`, d.Nginx.NginxSoftwareID, d.HostID).Scan(&matches)
			if err != nil {
				return spec, err
			}
			if !matches {
				return spec, errors.New("PROJECT_NGINX_NATIVE_REQUIRED")
			}
		}
		spec.Port = projectListenPort(d)
		name := strings.ToLower(strings.TrimSuffix(d.Nginx.ServerName, "."))
		if name == "" {
			name = "_"
		}
		spec.Site = &deploy.ProjectSite{ServerName: name, SPA: d.Nginx.SPA}
		for _, rule := range d.Nginx.ProxyRules {
			upstream := rule.UpstreamURL
			if rule.TargetModuleID != "" {
				var err error
				upstream, err = projectModuleUpstream(q, p, rule.TargetModuleID)
				if err != nil {
					return spec, err
				}
			}
			spec.Site.Proxies = append(spec.Site.Proxies, deploy.ProjectProxy{
				Location: rule.Location, URL: upstream, StripPrefix: rule.StripPrefix, WebSocket: rule.WebSocket, SSE: rule.SSE,
			})
		}
		if err := checkProjectSiteConflict(q, p, spec); err != nil {
			return spec, err
		}
	} else {
		spec.Port = projectListenPort(d)
		spec.Address, spec.HealthPath, spec.Profile, spec.MemoryMiB = d.BindAddress, d.HealthPath, d.Profile, d.MemoryMiB
		if spec.Address == "" {
			spec.Address = "127.0.0.1"
		}
		if spec.HealthPath == "" {
			spec.HealthPath = p.module.Runtime.Health.Path
		}
		if spec.HealthPath == "" {
			spec.HealthPath = p.module.Catalog.HealthPath
		}
		if spec.MemoryMiB == 0 {
			spec.MemoryMiB = 512
		}
		var err error
		if spec.Files, err = projectFreezeConfig(q, d.ConfigDrafts); err != nil {
			return spec, err
		}
		for _, v := range d.Variables {
			item := deploy.ProjectVariable{Name: v.Name, Value: v.Value}
			if v.CredentialID != "" {
				var found bool
				err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=?
					AND kind IN ('PASSWORD','TOKEN') AND revoked_at IS NULL)`, v.CredentialID, v.CredentialRevision).Scan(&found)
				if err != nil {
					return spec, err
				}
				if !found {
					return spec, errors.New("PROJECT_CREDENTIAL_UNAVAILABLE")
				}
				item.CredentialRef = v.CredentialID + ":" + v.CredentialRevision
			}
			spec.Variables = append(spec.Variables, item)
		}
	}
	for _, id := range d.DependencyIDs {
		var hostID, address, raw string
		var port int
		err := q.QueryRow(`SELECT s.host_id,t.address,s.observation_json,
			coalesce(json_extract(s.installation_json,'$.config.hostPort'),0)
			FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?`, id).Scan(&hostID, &address, &raw, &port)
		if err != nil {
			return spec, err
		}
		if port == 0 {
			var obs deploy.SoftwareObservation
			if deploy.StrictJSON([]byte(raw), &obs) == nil && len(obs.ListeningPorts) == 1 {
				_, portText, splitErr := net.SplitHostPort(obs.ListeningPorts[0])
				if splitErr == nil {
					port, _ = strconv.Atoi(portText)
				}
			}
		}
		if hostID == d.HostID {
			address = "127.0.0.1"
		}
		if port == 0 || address == "" {
			return spec, errors.New("PROJECT_DEPENDENCY_ENDPOINT_MISSING")
		}
		spec.DependencyChecks = append(spec.DependencyChecks, deploy.ProjectDependency{Host: address, Port: port})
	}
	return spec, deploy.ValidateProjectDeployment(&spec, deploy.ProjectProbe)
}

func projectListenPort(d applicationDeployment) int {
	if d.DeployMode == "STATIC" && d.Nginx != nil && d.Nginx.ListenPort > 0 {
		return d.Nginx.ListenPort
	}
	if d.ServicePort > 0 {
		return d.ServicePort
	}
	if len(d.Ports) > 0 {
		return d.Ports[0].HostPort
	}
	if d.DeployMode == "STATIC" {
		return 80
	}
	return 0
}

func projectModuleUpstream(q applicationReader, p projectDeploymentContext, moduleID string) (string, error) {
	for _, d := range p.env.Deployments {
		if d.ModuleID != moduleID || d.ID == p.draft.ID || d.HostID == "" || projectListenPort(d) == 0 || d.DeployMode == "STATIC" {
			continue
		}
		host := d.BindAddress
		if d.HostID == p.host.ID && (host == "" || host == "0.0.0.0" || host == "::") {
			host = "127.0.0.1"
		} else if d.HostID != p.host.ID && (host == "" || host == "0.0.0.0" || host == "::") {
			if err := q.QueryRow("SELECT address FROM targets WHERE id=?", d.HostID).Scan(&host); err != nil {
				return "", err
			}
		}
		if ip := net.ParseIP(host); host == "" || d.HostID != p.host.ID && ip != nil && ip.IsLoopback() {
			break
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(projectListenPort(d))), nil
	}
	return "", errors.New("PROJECT_PROXY_TARGET_UNAVAILABLE")
}

func checkProjectSiteConflict(q applicationReader, p projectDeploymentContext, spec deploy.ProjectDeployment) error {
	var found bool
	err := q.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM project_deployment_running a JOIN project_deployment_jobs j ON j.id=a.current_id
		WHERE j.target_id=? AND j.deployment_id<>? AND json_extract(j.snapshot_json,'$.spec.kind')='STATIC'
		AND json_extract(j.snapshot_json,'$.spec.port')=? AND json_extract(j.snapshot_json,'$.spec.site.serverName')=?
	)`, p.host.ID, p.draft.ID, spec.Port, spec.Site.ServerName).Scan(&found)
	if found {
		return errors.New("PROJECT_NGINX_SITE_CONFLICT")
	}
	return err
}

func projectSpecSummary(spec deploy.ProjectDeployment) []string {
	if spec.Kind == "JAVA" {
		return []string{
			fmt.Sprintf("Java 21 · systemd · %s:%d · %d MiB", spec.Address, spec.Port, spec.MemoryMiB),
			fmt.Sprintf("健康检查 %s · %d 个配置草稿 · %d 个依赖探测", spec.HealthPath, len(spec.Files), len(spec.DependencyChecks)),
		}
	}
	return []string{
		fmt.Sprintf("Nginx · %s:%d · %d 条反向代理", spec.Site.ServerName, spec.Port, len(spec.Site.Proxies)),
		fmt.Sprintf("/etc/nginx/conf.d/sfops-project-%s.conf", spec.ID),
	}
}
