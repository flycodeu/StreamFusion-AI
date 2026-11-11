package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func projectDraft28(name string) applicationProject {
	repositoryID, moduleID := uuid.NewString(), uuid.NewString()
	return applicationProject{
		Name: name, Description: "同仓前后端项目",
		Repositories: []applicationRepository{{ID: repositoryID, Name: "主仓库", URL: "https://github.com/example/project.git", Branch: "main"}},
		Modules:      []applicationModule{{ID: moduleID, Name: "后端", RepositoryID: repositoryID, Directory: "backend", Build: applicationBuild{Kind: "COMMAND", OutputPath: "target/app.jar", Steps: []applicationStep{{Name: "编译", Command: "mvn package"}}}, Runtime: applicationRuntime{Kind: "NATIVE"}}},
		Environments: []applicationEnvironment{{ID: uuid.NewString(), Name: "QA", Deployments: []applicationDeployment{{ID: uuid.NewString(), ModuleID: moduleID, Logs: applicationLogs{Kind: "FILE", Directory: "/srv/application/logs", Pattern: "*.log", RetentionDays: 30, MaxMiB: 1024}}}}},
	}
}

func TestApplicationProjectCRUDKeepsIdentityAndArchiveHistory(t *testing.T) {
	s, b := setup(t)
	b.login()
	p := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("智能化平台"), 200))
	if p.Version != "1" || p.CreatedAt == "" || len(p.Modules) != 1 || p.Modules[0].Build.Steps[0].TimeoutSeconds != 600 {
		t.Fatal("draft normalization failed", p)
	}
	original := p
	p.Description = "更新说明"
	p.Modules[0].Build.OutputPath = "target/new.jar"
	p = applicationResponse(t, b.call("PATCH", "/application-projects/"+p.ID, p, 200))
	if p.Version != "2" || p.Modules[0].ID != original.Modules[0].ID || p.Repositories[0].ID != original.Repositories[0].ID || p.CreatedAt != original.CreatedAt {
		t.Fatal("save changed stable identity", p)
	}
	b.call("PATCH", "/application-projects/"+p.ID, original, 409)
	loaded := applicationResponse(t, b.call("GET", "/application-projects/"+p.ID, nil, 200))
	if loaded.Description != p.Description || loaded.Modules[0].Build.OutputPath != "target/new.jar" {
		t.Fatal("stale write changed project")
	}
	p = applicationResponse(t, b.call("POST", "/application-projects/"+p.ID+"/archive", map[string]string{"version": p.Version}, 200))
	if !p.Archived {
		t.Fatal("archive did not persist")
	}
	if d := dataOf(t, b.call("GET", "/application-projects", nil, 200)); d["total"] != float64(0) {
		t.Fatal("archived project included by default", d)
	}
	if d := dataOf(t, b.call("GET", "/application-projects?archived=true&q=智能", nil, 200)); d["total"] != float64(1) {
		t.Fatal("archive search lost history", d)
	}
	p.Archived = false
	p = applicationResponse(t, b.call("PATCH", "/application-projects/"+p.ID, p, 200))
	if p.Archived || p.Version != "4" {
		t.Fatal("reactivation failed", p)
	}
	var jobs, locks int
	if err := s.DB.QueryRow("SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM operation_locks)").Scan(&jobs, &locks); err != nil || jobs != 0 || locks != 0 {
		t.Fatal("configuration unexpectedly executed remote changes", jobs, locks, err)
	}
}

func TestApplicationProjectBindingReplacementAndCrossProjectRejectionAreAtomic(t *testing.T) {
	s, b := setup(t)
	b.login()
	p := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("主项目"), 200))
	oldID := p.Environments[0].Deployments[0].ID
	p.Environments[0].Deployments[0].ID = uuid.NewString()
	p = applicationResponse(t, b.call("PATCH", "/application-projects/"+p.ID, p, 200))
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM application_deployments WHERE id=?", oldID).Scan(&count); err != nil || count != 0 {
		t.Fatal("discarded binding remained", count, err)
	}
	other := projectDraft28("另一个项目")
	other.Modules[0].ID = p.Modules[0].ID
	other.Environments[0].Deployments[0].ModuleID = p.Modules[0].ID
	b.call("POST", "/application-projects", other, 400)
	if err := s.DB.QueryRow("SELECT count(*) FROM application_projects").Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected aggregate left partial project", count, err)
	}
	loaded := applicationResponse(t, b.call("GET", "/application-projects/"+p.ID, nil, 200))
	if loaded.Modules[0].Name != "后端" || loaded.Environments[0].Deployments[0].ID != p.Environments[0].Deployments[0].ID {
		t.Fatal("rejected write changed original project")
	}
	// A missing target fails in the same transaction and restores the old version.
	p.Modules[0].Build.HostID = uuid.NewString()
	b.call("PATCH", "/application-projects/"+p.ID, p, 400)
	loaded = applicationResponse(t, b.call("GET", "/application-projects/"+p.ID, nil, 200))
	if loaded.Version != p.Version || loaded.Modules[0].Build.HostID != "" {
		t.Fatal("reference validation was not atomic")
	}
}

func TestApplicationProjectPaginationAndAuthentication(t *testing.T) {
	_, b := setup(t)
	b.call("GET", "/application-projects", nil, 401)
	b.call("POST", "/application-projects", map[string]string{"name": "未登录项目"}, 401)
	b.login()
	for i := range 13 {
		b.call("POST", "/application-projects", map[string]string{"name": fmt.Sprintf("分页项目%02d", i)}, 200)
	}
	page := dataOf(t, b.call("GET", "/application-projects?page=2&pageSize=10&q=分页", nil, 200))
	if page["total"] != float64(13) || len(page["items"].([]any)) != 3 || page["page"] != float64(2) {
		t.Fatal("server pagination failed", page)
	}
	page = dataOf(t, b.call("GET", "/application-projects?page=3&q=分页", nil, 200))
	if len(page["items"].([]any)) != 0 || page["total"] != float64(13) {
		t.Fatal("empty page lost total", page)
	}
	b.call("GET", "/application-projects?page=0", nil, 400)
	b.call("GET", "/application-projects?archived=unknown", nil, 400)
}

func TestApplicationProjectRejectsUnsafeDraftAndSecretValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*applicationProject)
	}{
		{"credential in repository", func(p *applicationProject) { p.Repositories[0].URL = "https://user:private-token@github.com/a/b.git" }},
		{"local repository", func(p *applicationProject) { p.Repositories[0].URL = "file:///etc/passwd" }},
		{"outside module", func(p *applicationProject) { p.Modules[0].Directory = "../backend" }},
		{"absolute dockerfile", func(p *applicationProject) { p.Modules[0].Build.Dockerfile = "/etc/Dockerfile" }},
		{"glob output", func(p *applicationProject) { p.Modules[0].Build.OutputPath = "target/*.jar" }},
		{"root build account", func(p *applicationProject) { p.Modules[0].Build.Account = "root" }},
		{"cross project repository", func(p *applicationProject) { p.Modules[0].RepositoryID = uuid.NewString() }},
		{"duplicate module", func(p *applicationProject) { p.Modules = append(p.Modules, p.Modules[0]) }},
		{"duplicate environment", func(p *applicationProject) { p.Environments = append(p.Environments, p.Environments[0]) }},
		{"secret variable", func(p *applicationProject) {
			p.Environments[0].Deployments[0].Variables = []applicationVariable{{Name: "API_TOKEN", Value: "do-not-store-this"}}
		}},
		{"secret URL", func(p *applicationProject) {
			p.Environments[0].Deployments[0].Variables = []applicationVariable{{Name: "DATABASE_URL", Value: "postgres://user:do-not-store-this@db/project"}}
		}},
		{"secret connection string", func(p *applicationProject) {
			p.Environments[0].Deployments[0].Variables = []applicationVariable{{Name: "CONNECTION_STRING", Value: "Server=db;Password=do-not-store-this;Database=project"}}
		}},
		{"mixed secret reference", func(p *applicationProject) {
			p.Environments[0].Deployments[0].Variables = []applicationVariable{{Name: "TOKEN", Value: "plaintext", CredentialID: uuid.NewString(), CredentialRevision: "1"}}
		}},
		{"root mount", func(p *applicationProject) {
			p.Environments[0].Deployments[0].Mounts = []applicationMount{{Source: "/", Target: "/host"}}
		}},
		{"unsafe log directory", func(p *applicationProject) { p.Environments[0].Deployments[0].Logs.Directory = "/srv/../etc" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := projectDraft28("测试项目")
			test.change(&p)
			if err := normalizeApplicationProject(&p); err == nil {
				t.Fatal("unsafe draft accepted")
			}
		})
	}
	_, b := setup(t)
	b.login()
	p := projectDraft28("HTTP校验")
	p.Environments[0].Deployments[0].Variables = []applicationVariable{{Name: "DB_PASSWORD", Value: "must-not-persist"}}
	b.call("POST", "/application-projects", p, 400)
	for _, value := range []string{"postgres://user:must-not-persist@host/db", "Server=db;Pwd=must-not-persist;Database=app"} {
		p.Environments[0].Deployments[0].Variables = []applicationVariable{{Name: "CONNECTION_STRING", Value: value}}
		b.call("POST", "/application-projects", p, 400)
	}
	if response := b.call("GET", "/application-projects?archived=all", nil, 200).Body.String(); strings.Contains(response, "must-not-persist") {
		t.Fatal("rejected secret persisted")
	}
}

func TestApplicationProjectGitEndpointMatchesAccountWithoutRemoteCalls(t *testing.T) {
	s, b, host, account, remote := gitControllerFixture(t)
	accountID := account["id"].(string)
	remote.mu.Lock()
	before := remote.calls
	remote.mu.Unlock()
	check := func(name, address string, want int) {
		p := projectDraft28(name)
		p.Repositories[0].URL = address
		p.Repositories[0].GitAccountID = accountID
		p.Modules[0].Build.HostID = host
		response := b.call("POST", "/application-projects", p, want)
		if want == 200 {
			var envelope struct{ Data applicationProject }
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Data.ID == "" {
				t.Fatal("save response invalid", err)
			}
		}
	}
	check("SSH用户不符", "ssh://other@git.example/team/repo.git", 400)
	check("SSH端口不符", "ssh://git@git.example:2222/team/repo.git", 400)
	check("SSH匹配", "ssh://git@git.example:22/team/repo.git", 200)
	if _, err := s.DB.Exec("UPDATE git_accounts SET ssh_port=2222 WHERE id=?", accountID); err != nil {
		t.Fatal(err)
	}
	check("SCP沿用非标准端口", "git@git.example:team/repo.git", 200)
	if _, err := s.DB.Exec("UPDATE git_accounts SET auth_method='HTTPS',ssh_port=8443 WHERE id=?", accountID); err != nil {
		t.Fatal(err)
	}
	check("HTTP拒绝Token", "http://git.example:8443/team/repo.git", 400)
	check("HTTPS端口不符", "https://git.example/team/repo.git", 400)
	check("HTTPS匹配", "https://git.example:8443/team/repo.git", 200)
	remote.mu.Lock()
	after := remote.calls
	remote.mu.Unlock()
	if after != before {
		t.Fatal("saving project unexpectedly connected to remote server")
	}
}
