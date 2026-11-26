package controller

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func seedProjectRunningRelease(t *testing.T, s *Store, p applicationProject, d applicationDeployment, port int) string {
	t.Helper()
	release := seedProjectReleaseJob(t, s, p, d, deploy.ProjectDeployment{Kind: "JAVA", Address: "0.0.0.0", Port: port})
	if _, err := s.DB.Exec(`INSERT INTO project_deployment_running(deployment_id,current_id) VALUES(?,?)`, d.ID, release); err != nil {
		t.Fatal(err)
	}
	return release
}

func seedProjectReleaseJob(t *testing.T, s *Store, p applicationProject, d applicationDeployment, spec deploy.ProjectDeployment) string {
	t.Helper()
	var actor string
	if err := s.DB.QueryRow("SELECT id FROM admins LIMIT 1").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	release := uuid.NewString()
	now := Now()
	spec.ID, spec.ReleaseID = d.ID, release
	snap := projectDeploymentSnapshot{ProjectID: p.ID, ModuleID: d.ModuleID, HostID: d.HostID, BuildNumber: 1,
		Spec: spec}
	if _, err := s.DB.Exec(`INSERT INTO project_deployment_jobs(id,deployment_id,target_id,actor_id,action,status,phase,
		host_name,subject_name,build_number,commit_id,release_id,baseline_id,snapshot_json,trace_id,created_at,updated_at,execution_log,health)
		VALUES(?,?,?,?,'DEPLOY','SUCCEEDED','VERIFIED','backend','project',1,'commit',?,'',?,'trace',?,?,'release fixture','READY')`,
		release, d.ID, d.HostID, actor, release, jsonText(snap), now, now); err != nil {
		t.Fatal(err)
	}
	return release
}

func TestProjectDeploymentUpstreamUsesActiveReleaseRatherThanDraft(t *testing.T) {
	s, b := setup(t)
	b.login()
	hostID := pluginSSHFixture(t, s, b, false, func(deploy.Request) ([]byte, bool) { return nil, false })
	draft := projectDraft28("active-upstream")
	d := &draft.Environments[0].Deployments[0]
	d.HostID, d.DeployMode, d.ServicePort = hostID, "NATIVE", 18081
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))

	site := projectDeploymentContext{project: p, env: p.Environments[0], host: target{ID: hostID}}
	if _, err := projectModuleUpstream(s.DB, site, d.ModuleID); err == nil || err.Error() != "PROJECT_PROXY_TARGET_UNAVAILABLE" {
		t.Fatal("proxy accepted an unreleased endpoint", err)
	}
	if url, err := projectConfiguredModuleUpstream(s.DB, site, d.ModuleID); err != nil || url != "http://127.0.0.1:18081" {
		t.Fatal("generic proxy could not use a separately managed service address", url, err)
	}
	release := seedProjectRunningRelease(t, s, p, *d, 18080)
	if url, err := projectModuleUpstream(s.DB, site, d.ModuleID); err != nil || url != "http://127.0.0.1:18080" {
		t.Fatal("proxy did not use running snapshot", url, err)
	}
	pending := uuid.NewString()
	if _, err := s.DB.Exec(`INSERT INTO project_deployment_jobs(id,deployment_id,target_id,actor_id,action,status,phase,
		host_name,subject_name,build_number,commit_id,release_id,baseline_id,snapshot_json,trace_id,created_at,updated_at)
		SELECT ?,deployment_id,target_id,actor_id,'DEPLOY','QUEUED','QUEUED',host_name,subject_name,2,commit_id,?,id,
		json_set(snapshot_json,'$.spec.port',18081),'trace',created_at,updated_at FROM project_deployment_jobs WHERE id=?`, pending, pending, release); err != nil {
		t.Fatal(err)
	}
	if _, err := projectModuleUpstream(s.DB, site, d.ModuleID); err == nil || err.Error() != "PROJECT_PROXY_TARGET_UNAVAILABLE" {
		t.Fatal("proxy accepted an upstream being replaced mid-publication", err)
	}
	if _, err := s.DB.Exec("UPDATE project_deployment_jobs SET status='FAILED' WHERE id=?", pending); err != nil {
		t.Fatal(err)
	}
	base := "/application-projects/" + p.ID + "/deployments/" + d.ID
	b.call("POST", base+"/decommission", map[string]string{"expectedReleaseId": release}, 202)
	if _, err := projectModuleUpstream(s.DB, site, d.ModuleID); err == nil || err.Error() != "PROJECT_PROXY_TARGET_UNAVAILABLE" {
		t.Fatal("proxy accepted an endpoint pending decommission", err)
	}
}

func TestProjectDeploymentRejectsChangingEndpointOfActiveSite(t *testing.T) {
	s, b := setup(t)
	b.login()
	hostID := pluginSSHFixture(t, s, b, false, func(deploy.Request) ([]byte, bool) { return nil, false })
	draft := projectDraft28("active-site-dependency")
	env := &draft.Environments[0]
	backend := &env.Deployments[0]
	backend.HostID, backend.DeployMode, backend.ServicePort, backend.BindAddress = hostID, "NATIVE", 18080, "0.0.0.0"
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND", HealthPath: "/health"}
	front := applicationModule{ID: uuid.NewString(), Name: "前端", RepositoryID: draft.Repositories[0].ID, Directory: "web",
		Build:   applicationBuild{Kind: "COMMAND", OutputPath: "dist", Steps: []applicationStep{{Name: "打包", Command: "npm run build"}}},
		Runtime: applicationRuntime{Kind: "STATIC"}, Catalog: &applicationModuleCatalog{Kind: "FRONTEND"}}
	draft.Modules = append(draft.Modules, front)
	site := applicationDeployment{ID: uuid.NewString(), ModuleID: front.ID, HostID: hostID, DeployMode: "STATIC",
		Nginx: &applicationNginxDeploy{SiteName: "front", ServerName: "active.example.test", ListenPort: 18082, SPA: true}}
	env.Deployments = append(env.Deployments, site)
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	current := seedProjectRunningRelease(t, s, p, *backend, 18080)
	siteRelease := seedProjectRunningRelease(t, s, p, site, 18082)
	snap, err := readProjectJobSnapshot(s.DB, siteRelease)
	if err != nil {
		t.Fatal(err)
	}
	snap.Spec.Kind = "STATIC"
	snap.Spec.Site = &deploy.ProjectSite{ServerName: "active.example.test", SPA: true,
		Proxies: []deploy.ProjectProxy{{Location: "/api/", URL: "http://127.0.0.1:18080", StripPrefix: true}}}
	if _, err := s.DB.Exec("UPDATE project_deployment_jobs SET snapshot_json=? WHERE id=?", jsonText(snap), siteRelease); err != nil {
		t.Fatal(err)
	}
	c, err := readProjectDeployment(s.DB, p.ID, backend.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectDeploySpec(s.DB, c, current); err != nil {
		t.Fatal("same endpoint should be deployable", err)
	}
	c.draft.ServicePort = 18081
	if _, err := projectDeploySpec(s.DB, c, current); err == nil || err.Error() != "PROJECT_ACTIVE_SITE_USES_OLD_ENDPOINT" {
		t.Fatal("changed port would leave active site on old endpoint", err)
	}
	url := "/application-projects/" + p.ID + "/deployments/" + backend.ID + "/decommission"
	if reply := b.call("POST", url, map[string]string{"expectedReleaseId": current}, 409); !strings.Contains(reply.Body.String(), "PROJECT_ACTIVE_SITE_USES_BACKEND") {
		t.Fatal("backend may not be decommissioned under active site", reply.Body.String())
	}
}

func TestProjectDeploymentSiteRejectsUpstreamPendingRemoval(t *testing.T) {
	s, b := setup(t)
	b.login()
	backendHost, siteHost := uuid.NewString(), uuid.NewString()
	if _, err := s.DB.Exec(`INSERT INTO targets(id,name,install_root,kind,address,created_at) VALUES
		(?,'API host','/opt/backend-test','SSH','192.0.2.10',?),
		(?,'site host','/opt/site-test','SSH','192.0.2.11',?)`, backendHost, Now(), siteHost, Now()); err != nil {
		t.Fatal(err)
	}
	draft := projectDraft28("pending-removal-site-dependency")
	env := &draft.Environments[0]
	backend := &env.Deployments[0]
	backend.HostID, backend.DeployMode, backend.ServicePort, backend.BindAddress = backendHost, "NATIVE", 18080, "0.0.0.0"
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND", HealthPath: "/health"}
	front := applicationModule{ID: uuid.NewString(), Name: "Frontend", RepositoryID: draft.Repositories[0].ID, Directory: "web",
		Build:   applicationBuild{Kind: "COMMAND", OutputPath: "dist", Steps: []applicationStep{{Name: "打包", Command: "npm run build"}}},
		Runtime: applicationRuntime{Kind: "STATIC"}, Catalog: &applicationModuleCatalog{Kind: "FRONTEND"}}
	draft.Modules = append(draft.Modules, front)
	const upstream = "http://192.0.2.10:18080"
	site := applicationDeployment{ID: uuid.NewString(), ModuleID: front.ID, HostID: siteHost, DeployMode: "STATIC",
		Nginx: &applicationNginxDeploy{SiteName: "site", ServerName: "site.example.test", ListenPort: 18082, SPA: true,
			ProxyRules: []applicationNginxProxyRule{{Location: "/service/", UpstreamURL: upstream}}}}
	env.Deployments = append(env.Deployments, site)
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	backendRelease := seedProjectRunningRelease(t, s, p, *backend, 18080)
	frozenSite := deploy.ProjectDeployment{Kind: "STATIC", Port: 18082,
		Site: &deploy.ProjectSite{ServerName: "site.example.test", SPA: true}}
	current := seedProjectReleaseJob(t, s, p, site, frozenSite)
	frozenSite.Site.Proxies = []deploy.ProjectProxy{{Location: "/service/", URL: upstream}}
	previous := seedProjectReleaseJob(t, s, p, site, frozenSite)
	if _, err := s.DB.Exec(`INSERT INTO project_deployment_running(deployment_id,current_id,previous_id) VALUES(?,?,?)`,
		site.ID, current, previous); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.h.loadProjectDeployPlan(t.Context(), p.ID, site.ID); err != nil {
		t.Fatal("stable reverse proxy should be deployable", err)
	}
	base := "/application-projects/" + p.ID + "/deployments/"
	retirement := dataOf(t, b.call("POST", base+backend.ID+"/decommission", map[string]string{"expectedReleaseId": backendRelease}, 202))["jobId"].(string)
	for _, status := range []string{"QUEUED", "UNKNOWN"} {
		if _, err := s.DB.Exec(`UPDATE project_deployment_jobs SET status=? WHERE id=?`, status, retirement); err != nil {
			t.Fatal(err)
		}
		if _, _, err := b.h.loadProjectDeployPlan(t.Context(), p.ID, site.ID); err == nil || err.Error() != "PROJECT_PROXY_TARGET_UNAVAILABLE" {
			t.Error("site publish should reject backend pending removal", status, err)
		}
		got := b.call("POST", base+site.ID+"/rollback", map[string]string{"previousReleaseId": previous}, 409).Body.String()
		if !strings.Contains(got, "PROJECT_PROXY_TARGET_UNAVAILABLE") {
			t.Fatal("site rollback should reject backend pending removal", status, got)
		}
	}
}

func TestStreamFusionSiteRollbackRequiresCurrentUpstream(t *testing.T) {
	s, b := setup(t)
	b.login()
	backendHost := uuid.NewString()
	if _, err := s.DB.Exec("INSERT INTO targets(id,name,install_root,kind,address,created_at) VALUES(?,'platform-api host','/opt/backend-test','SSH','192.0.2.10',?)", backendHost, Now()); err != nil {
		t.Fatal(err)
	}
	siteHost := pluginSSHFixture(t, s, b, false, func(req deploy.Request) ([]byte, bool) {
		switch req.Action {
		case "CAPABILITIES":
			data, _ := json.Marshal(executorCapabilities{
				Owner:                   deploy.Owner{ControllerID: req.ControllerID, TargetID: req.TargetID},
				ProjectDeployVersion:    deploy.ProjectDeployVersion,
				CommandTrustFingerprint: deploy.CommandPublicKeyFingerprint(s.commandPublicKey()),
			})
			return data, true
		case deploy.ProjectRollback:
			data, _ := json.Marshal(deploy.ProjectDeployResult{
				ReleaseID: req.ProjectDeployment.ReleaseID, Status: "SUCCEEDED", Phase: "TARGET_EXECUTION", Health: "READY",
			})
			return data, true
		}
		return nil, false
	})
	draft := projectDraft28("streamfusion-site-rollback")
	draft.Modules[0].Directory = "platform-api"
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND", HealthPath: "/actuator/health"}
	web := applicationModule{ID: uuid.NewString(), Name: "Platform Web", RepositoryID: draft.Repositories[0].ID, Directory: "platform-web",
		Build:   applicationBuild{Kind: "COMMAND", OutputPath: "dist", Steps: []applicationStep{{Name: "打包", Command: "npm run build"}}},
		Runtime: applicationRuntime{Kind: "STATIC"}, Catalog: &applicationModuleCatalog{Kind: "FRONTEND"}}
	draft.Modules = append(draft.Modules, web)
	env := &draft.Environments[0]
	backend := &env.Deployments[0]
	backend.HostID, backend.DeployMode, backend.ServicePort, backend.BindAddress = backendHost, "NATIVE", 18080, "0.0.0.0"
	site := applicationDeployment{ID: uuid.NewString(), ModuleID: web.ID, HostID: siteHost, DeployMode: "STATIC",
		Nginx: &applicationNginxDeploy{SiteName: "platform-web", ServerName: "platform.example.test", ListenPort: 18082, SPA: true}}
	env.Deployments = append(env.Deployments, site)
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	apiRelease := seedProjectRunningRelease(t, s, p, *backend, 18080)
	frozenSite := func(upstream, healthPath string) deploy.ProjectDeployment {
		return deploy.ProjectDeployment{Kind: "STATIC", Port: 18082, Site: &deploy.ProjectSite{
			ServerName: "platform.example.test", SPA: true, HealthPath: healthPath,
			Proxies: []deploy.ProjectProxy{{Location: "/api/", URL: upstream, StripPrefix: true}},
		}}
	}
	const activeUpstream = "http://192.0.2.10:18080"
	const staleUpstream = "http://192.0.2.20:18080"
	current := seedProjectReleaseJob(t, s, p, site, frozenSite(staleUpstream, deploy.StreamFusionSiteHealthPath))
	legacy := seedProjectReleaseJob(t, s, p, site, frozenSite(activeUpstream, ""))
	mismatch := seedProjectReleaseJob(t, s, p, site, frozenSite(staleUpstream, deploy.StreamFusionSiteHealthPath))
	restore := seedProjectReleaseJob(t, s, p, site, frozenSite(activeUpstream, deploy.StreamFusionSiteHealthPath))
	if _, err := s.DB.Exec("INSERT INTO project_deployment_running(deployment_id,current_id,previous_id) VALUES(?,?,?)", site.ID, current, legacy); err != nil {
		t.Fatal(err)
	}
	base := "/application-projects/" + p.ID + "/deployments/"
	rollback := func(previous string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return b.call("POST", base+site.ID+"/rollback", map[string]string{"previousReleaseId": previous}, status)
	}
	if got := rollback(legacy, 409).Body.String(); !strings.Contains(got, "PROJECT_STREAMFUSION_ROLLBACK_CONTRACT_CHANGED") {
		t.Fatal("legacy snapshot accepted or not explained", got)
	}
	setPrevious := func(previous string) {
		t.Helper()
		if _, err := s.DB.Exec("UPDATE project_deployment_running SET previous_id=? WHERE deployment_id=?", previous, site.ID); err != nil {
			t.Fatal(err)
		}
	}
	setPrevious(mismatch)
	if got := rollback(mismatch, 409).Body.String(); !strings.Contains(got, "PROJECT_STREAMFUSION_ROLLBACK_UPSTREAM_CHANGED") {
		t.Fatal("previous release pointing to a retired backend was not rejected", got)
	}
	setPrevious(restore)
	stale := dataOf(t, rollback(restore, 202))["jobId"].(string)
	if got := b.call("POST", base+backend.ID+"/decommission", map[string]string{"expectedReleaseId": apiRelease}, 409).Body.String(); !strings.Contains(got, "PROJECT_ACTIVE_SITE_USES_BACKEND") {
		t.Fatal("could decommission backend reserved by a queued site rollback", got)
	}
	// Simulate drift after admission and verify that the worker rejects the
	// frozen route without sending a project rollback to the target server.
	if _, err := s.DB.Exec(`UPDATE project_deployment_jobs SET snapshot_json=json_set(snapshot_json,'$.spec.port',18081) WHERE id=?`, apiRelease); err != nil {
		t.Fatal(err)
	}
	load := func(id string) projectDeploymentJob {
		t.Helper()
		job, err := scanProjectJob(s.DB.QueryRow("SELECT "+projectJobSummaryFields+" FROM project_deployment_jobs WHERE id=?", id))
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	b.h.processProjectDeployment(t.Context(), load(stale))
	if job := load(stale); job.Status != "FAILED" || job.ErrorCode != "PROJECT_STREAMFUSION_ROLLBACK_UPSTREAM_CHANGED" {
		t.Fatal("worker did not reject upstream drift", job)
	}
	if active, _, err := readProjectVersions(s.DB, site.ID); err != nil || active != current {
		t.Fatal("failed rollback changed the running site", active, err)
	}
	if _, err := s.DB.Exec(`UPDATE project_deployment_jobs SET snapshot_json=json_set(snapshot_json,'$.spec.port',18080) WHERE id=?`, apiRelease); err != nil {
		t.Fatal(err)
	}
	valid := dataOf(t, rollback(restore, 202))["jobId"].(string)
	b.h.processProjectDeployment(t.Context(), load(valid))
	if active, previous, err := readProjectVersions(s.DB, site.ID); err != nil || active != restore || previous != current {
		t.Fatal("compatible rollback failed to switch releases", active, previous, err, load(valid))
	}
}

func TestProjectDeploymentSchemaUpgradePreservesRunningPointersAndHistory(t *testing.T) {
	s, b := setup(t)
	b.login()
	hostID := pluginSSHFixture(t, s, b, false, func(deploy.Request) ([]byte, bool) { return nil, false })
	draft := projectDraft28("upgrade-running")
	d := &draft.Environments[0].Deployments[0]
	d.HostID, d.DeployMode, d.ServicePort = hostID, "NATIVE", 18080
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	release := seedProjectRunningRelease(t, s, p, *d, 18080)

	queued := uuid.NewString()
	if _, err := s.DB.Exec(`INSERT INTO project_deployment_jobs(id,deployment_id,target_id,actor_id,action,status,phase,
		host_name,subject_name,build_number,commit_id,release_id,baseline_id,snapshot_json,trace_id,created_at,updated_at)
		SELECT ?,deployment_id,target_id,actor_id,'DEPLOY','QUEUED','RECONCILING',host_name,subject_name,2,commit_id,?,id,
		snapshot_json,'trace',created_at,updated_at FROM project_deployment_jobs WHERE id=?`, queued, queued, release); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO project_deployment_plans(token_hash,actor_id,deployment_id,snapshot_json,expires_at,job_id)
		SELECT 'fixture-token',actor_id,deployment_id,'{}',updated_at,id FROM project_deployment_jobs WHERE id=?`, release); err != nil {
		t.Fatal(err)
	}
	// This fixture contains only rows and actions understood by v34. Reopen to
	// exercise the rebuilds with a running pointer and a consumed approval.
	if _, err := s.DB.Exec(`DELETE FROM schema_migrations WHERE version>=35; PRAGMA user_version=34;`); err != nil {
		t.Fatal(err)
	}
	dir := s.Dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	current, _, err := readProjectVersions(upgraded.DB, d.ID)
	if err != nil || current != release {
		t.Fatal("upgrade lost running pointer", current, err)
	}
	var consumed, log string
	if err = upgraded.DB.QueryRow("SELECT p.job_id,j.execution_log FROM project_deployment_plans p JOIN project_deployment_jobs j ON j.id=p.job_id WHERE p.token_hash='fixture-token'").Scan(&consumed, &log); err != nil || consumed != release || log != "release fixture" {
		t.Fatal("upgrade lost approval or execution history", consumed, log, err)
	}
	var status string
	if err = upgraded.DB.QueryRow("SELECT status FROM project_deployment_jobs WHERE id=?", queued).Scan(&status); err != nil || status != "UNKNOWN" {
		t.Fatal("old Verify-triggered recovery must wait for explicit recovery after upgrade", status, err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "before-schema-35-*.tar.gz"))
	if err != nil || len(backups) != 1 {
		t.Fatal("upgrade did not preserve an original backup", backups, err)
	}
	if _, err = upgraded.DB.Exec("DELETE FROM application_deployments WHERE id=?", d.ID); err == nil {
		t.Fatal("upgrade lost active release protection")
	}
}
