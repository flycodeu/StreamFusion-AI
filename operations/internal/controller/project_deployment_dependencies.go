package controller

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// A site on another host may still depend on this port. Use release
// snapshots so editing or deleting unrelated drafts cannot hide a dependency.
func checkProjectDecommissionDependencies(q applicationReader, p projectDeploymentContext, backend projectDeploymentSnapshot) error {
	if backend.Spec.Kind != "JAVA" {
		return nil
	}
	rows, err := q.Query(`SELECT j.target_id,j.snapshot_json
		FROM project_deployment_jobs j
		JOIN application_deployments d ON d.id=j.deployment_id
		LEFT JOIN project_deployment_running a ON a.deployment_id=d.id
		WHERE d.environment_id=? AND d.project_id=? AND d.id<>?
		  AND json_extract(j.snapshot_json,'$.spec.kind')='STATIC'
		  AND (j.id=a.current_id OR (j.action IN ('DEPLOY','ROLLBACK') AND j.status IN ('QUEUED','RUNNING','UNKNOWN')))`,
		p.env.ID, p.project.ID, p.draft.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hostID, raw string
		var site projectDeploymentSnapshot
		if err := rows.Scan(&hostID, &raw); err != nil {
			return err
		}
		if err := deploy.StrictJSON([]byte(raw), &site); err != nil {
			return err
		}
		if projectSiteUsesBackend(site.Spec.Site, hostID, p.host.Address, backend) {
			return errors.New("PROJECT_ACTIVE_SITE_USES_BACKEND")
		}
	}
	return rows.Err()
}

// Freeze the other direction too: a new or restored site cannot use an
// endpoint which an already accepted backend job is about to remove.
func checkProjectSiteUpstreams(q applicationReader, p projectDeploymentContext, site *deploy.ProjectSite) error {
	if site == nil || len(site.Proxies) == 0 {
		return nil
	}
	rows, err := q.Query(`SELECT t.address,live.snapshot_json,pending.action,pending.snapshot_json
		FROM application_deployments d
		JOIN project_deployment_running r ON r.deployment_id=d.id
		JOIN project_deployment_jobs live ON live.id=r.current_id
		JOIN targets t ON t.id=live.target_id
		JOIN project_deployment_jobs pending ON pending.deployment_id=d.id
		WHERE d.environment_id=? AND d.project_id=? AND d.id<>?
		  AND json_extract(live.snapshot_json,'$.spec.kind')='JAVA'
		  AND pending.action<>'VERIFY' AND pending.status IN ('QUEUED','RUNNING','UNKNOWN')`,
		p.env.ID, p.project.ID, p.draft.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var address, rawLive, action, rawPending string
		var live projectDeploymentSnapshot
		if err := rows.Scan(&address, &rawLive, &action, &rawPending); err != nil {
			return err
		}
		if err := deploy.StrictJSON([]byte(rawLive), &live); err != nil {
			return err
		}
		if !projectSiteUsesBackend(site, p.host.ID, address, live) {
			continue
		}
		stable, err := projectBackendJobKeepsEndpoint(live, action, rawPending)
		if err != nil {
			return err
		}
		if !stable {
			return errors.New("PROJECT_PROXY_TARGET_UNAVAILABLE")
		}
	}
	return rows.Err()
}

func projectSiteUsesBackend(site *deploy.ProjectSite, siteHostID, hostAddress string, backend projectDeploymentSnapshot) bool {
	if site == nil {
		return false
	}
	for _, proxy := range site.Proxies {
		u, err := url.Parse(proxy.URL)
		if err == nil && projectProxyUsesBackend(u, hostAddress, siteHostID == backend.HostID, backend.Spec) {
			return true
		}
	}
	return false
}

func projectProxyUsesBackend(u *url.URL, hostAddress string, sameHost bool, backend deploy.ProjectDeployment) bool {
	if u == nil || u.Port() != strconv.Itoa(backend.Port) {
		return false
	}
	host := u.Hostname()
	if address := net.ParseIP(host); address != nil && address.IsLoopback() || strings.EqualFold(host, "localhost") {
		bindIP := net.ParseIP(backend.Address)
		return sameHost && bindIP != nil && (bindIP.IsLoopback() || bindIP.IsUnspecified())
	}
	return strings.EqualFold(host, backend.Address) || strings.EqualFold(host, hostAddress)
}
