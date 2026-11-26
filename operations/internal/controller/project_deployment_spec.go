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
	"github.com/streamfusion-ai/operations/internal/projectconfig"
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
	if p.module.Catalog == nil {
		return ""
	}
	return projectDeploymentKind(p.module.Catalog.Kind, p.draft.DeployMode, p.draft.ManagedInstanceID)
}

func projectDeploymentKind(moduleKind, deployMode, managedInstanceID string) string {
	if managedInstanceID != "" {
		return ""
	}
	switch {
	case deployMode == "NATIVE" && moduleKind == "BACKEND":
		return "JAVA"
	case deployMode == "STATIC" && moduleKind == "FRONTEND":
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
		if seen[file.Filename] || deploy.ValidateProjectConfigFile(file) != nil || projectconfig.ContainsSecret(file.Content) {
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
		if err := configureStreamFusionProjectSite(p, spec.Site); err != nil {
			return spec, err
		}
		streamFusionAPI := streamFusionPlatformAPIModule(p.module, p.project.Modules)
		for _, rule := range d.Nginx.ProxyRules {
			upstream := rule.UpstreamURL
			if rule.TargetModuleID != "" {
				var err error
				if rule.TargetModuleID == streamFusionAPI && strings.TrimSuffix(rule.Location, "/") == "/api" {
					upstream, err = projectModuleUpstream(q, p, rule.TargetModuleID)
				} else {
					upstream, err = projectConfiguredModuleUpstream(q, p, rule.TargetModuleID)
				}
				if err != nil {
					return spec, err
				}
			}
			spec.Site.Proxies = append(spec.Site.Proxies, deploy.ProjectProxy{
				Location: rule.Location, URL: upstream, StripPrefix: rule.StripPrefix, WebSocket: rule.WebSocket, SSE: rule.SSE,
			})
		}
		if err := checkProjectSiteUpstreams(q, p, spec.Site); err != nil {
			return spec, err
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
		if err := checkProjectBackendEndpointChange(q, p, baseline, spec); err != nil {
			return spec, err
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
	var upstream string
	for _, d := range p.env.Deployments {
		if d.ModuleID != moduleID || d.ID == p.draft.ID {
			continue
		}
		var release string
		err := q.QueryRow(`SELECT current_id FROM project_deployment_running WHERE deployment_id=?`, d.ID).Scan(&release)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		snap, err := readProjectJobSnapshot(q, release)
		if err != nil {
			return "", err
		}
		if snap.Spec.Kind != "JAVA" || snap.HostID == "" || snap.Spec.Port == 0 {
			continue
		}
		if safe, err := projectBackendEndpointStable(q, snap); err != nil {
			return "", err
		} else if !safe {
			continue
		}
		candidate, err := projectBackendURL(q, p.host.ID, snap)
		if err != nil {
			return "", err
		}
		if candidate == "" {
			continue
		}
		if upstream != "" {
			return "", errors.New("PROJECT_PROXY_TARGET_AMBIGUOUS")
		}
		upstream = candidate
	}
	if upstream == "" {
		return "", errors.New("PROJECT_PROXY_TARGET_UNAVAILABLE")
	}
	return upstream, nil
}

// Other project templates can reference a separately managed application.
// StreamFusion's /api instead resolves only from the active release.
func projectConfiguredModuleUpstream(q applicationReader, p projectDeploymentContext, moduleID string) (string, error) {
	for _, d := range p.env.Deployments {
		port := projectListenPort(d)
		if d.ModuleID != moduleID || d.ID == p.draft.ID || d.HostID == "" || port == 0 || d.DeployMode == "STATIC" {
			continue
		}
		candidate := projectDeploymentSnapshot{HostID: d.HostID, Spec: deploy.ProjectDeployment{Address: d.BindAddress, Port: port}}
		endpoint, err := projectBackendURL(q, p.host.ID, candidate)
		if err != nil {
			return "", err
		}
		if endpoint != "" {
			return endpoint, nil
		}
	}
	return "", errors.New("PROJECT_PROXY_TARGET_UNAVAILABLE")
}

func projectBackendEndpointStable(q applicationReader, active projectDeploymentSnapshot) (bool, error) {
	var action, raw string
	err := q.QueryRow(`SELECT action,snapshot_json FROM project_deployment_jobs WHERE deployment_id=?
		AND action<>'VERIFY' AND status IN ('QUEUED','RUNNING','UNKNOWN')`, active.Spec.ID).Scan(&action, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return projectBackendJobKeepsEndpoint(active, action, raw)
}

func projectBackendJobKeepsEndpoint(active projectDeploymentSnapshot, action, raw string) (bool, error) {
	if action == "DECOMMISSION" {
		return false, nil
	}
	var pending projectDeploymentSnapshot
	if err := deploy.StrictJSON([]byte(raw), &pending); err != nil {
		return false, err
	}
	return sameProjectBackendEndpoint(active, pending.HostID, pending.Spec), nil
}

func sameProjectBackendEndpoint(active projectDeploymentSnapshot, hostID string, candidate deploy.ProjectDeployment) bool {
	return active.HostID == hostID && active.Spec.Port == candidate.Port && active.Spec.Address == candidate.Address
}

func projectBackendURL(q applicationReader, siteHostID string, snap projectDeploymentSnapshot) (string, error) {
	address := snap.Spec.Address
	if ip := net.ParseIP(address); address == "" || ip != nil && ip.IsUnspecified() {
		if snap.HostID == siteHostID {
			address = "127.0.0.1"
		} else if err := q.QueryRow("SELECT address FROM targets WHERE id=?", snap.HostID).Scan(&address); err != nil {
			return "", err
		}
	}
	if ip := net.ParseIP(address); address == "" || snap.HostID != siteHostID && (strings.EqualFold(address, "localhost") || ip != nil && ip.IsLoopback()) {
		return "", nil
	}
	return "http://" + net.JoinHostPort(address, strconv.Itoa(snap.Spec.Port)), nil
}

func checkProjectBackendEndpointChange(q applicationReader, p projectDeploymentContext, current string, candidate deploy.ProjectDeployment) error {
	if candidate.Kind != "JAVA" || current == "" {
		return nil
	}
	snap, err := readProjectJobSnapshot(q, current)
	if err != nil {
		return err
	}
	if sameProjectBackendEndpoint(snap, p.host.ID, candidate) {
		return nil
	}
	err = checkProjectDecommissionDependencies(q, p, snap)
	if err != nil && err.Error() == "PROJECT_ACTIVE_SITE_USES_BACKEND" {
		return errors.New("PROJECT_ACTIVE_SITE_USES_OLD_ENDPOINT")
	}
	return err
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
	lines := []string{
		fmt.Sprintf("Nginx · %s:%d · %d 条反向代理", spec.Site.ServerName, spec.Port, len(spec.Site.Proxies)),
		fmt.Sprintf("/etc/nginx/conf.d/sfops-project-%s.conf", spec.ID),
	}
	if spec.Site.HealthPath != "" {
		for _, proxy := range spec.Site.Proxies {
			if strings.TrimSuffix(proxy.Location, "/") == "/api" {
				lines = append(lines, fmt.Sprintf("实际 API 上游 · %s", proxy.URL))
				break
			}
		}
		lines = append(lines, fmt.Sprintf("目标机反代检查 · %s（JSON status=UP）", spec.Site.HealthPath))
	}
	return lines
}
