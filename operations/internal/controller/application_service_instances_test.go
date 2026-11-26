package controller

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestModuleReleaseUsesTheSavedTargetAndRechecksItBeforeSubmit(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "发布服务器", "kind": "LOCAL"}, 201))["id"].(string)
	targetID := uuid.NewString()
	if _, err := s.DB.Exec(`INSERT INTO instances(id,name,target_id,draft_json,created_at)
		VALUES(?,?,?,'{"environment":"qa"}',?)`, targetID, "业务后台", host, Now()); err != nil {
		t.Fatal(err)
	}
	project := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("发布测试"), 200))
	path := "/application-projects/" + project.ID + "/service-instances"
	input := applicationServiceInstanceInput{Version: project.Version, ModuleID: project.Modules[0].ID, Environment: "other", HostID: host, ManagedInstanceID: &targetID}
	b.call("POST", path, input, 400)
	input.Environment = project.Environments[0].Name
	input.ID = project.Environments[0].Deployments[0].ID
	project = applicationResponse(t, b.call("POST", path, input, 200))
	if project.Environments[0].Deployments[0].ManagedInstanceID != targetID {
		t.Fatal("module has no saved release target")
	}
	input.Version = project.Version
	input.ManagedInstanceID = nil
	project = applicationResponse(t, b.call("POST", path, input, 200))
	if project.Environments[0].Deployments[0].ManagedInstanceID != targetID {
		t.Fatal("an older caller discarded the release target")
	}
	// A plan created before the operator cleared the target must not publish.
	p := plan{ID: uuid.NewString(), Kind: "PUBLISH", InstanceID: targetID, TargetID: host, ProjectDeploymentID: input.ID}
	if _, err := s.DB.Exec("INSERT INTO operation_plans VALUES(?,?,?,?,?)", p.ID, targetID, jsonText(p), Now(), Now()); err != nil {
		t.Fatal(err)
	}
	clearTarget := ""
	input.Version = project.Version
	input.ManagedInstanceID = &clearTarget
	b.call("POST", path, input, 200)
	stale := b.call("POST", "/plans/"+p.ID+"/submit", map[string]string{"idempotencyKey": uuid.NewString()}, 409)
	if !strings.Contains(stale.Body.String(), "PROJECT_RELEASE_TARGET_CHANGED") {
		t.Fatal("expected module target conflict", stale.Body.String())
	}
}

func TestApplicationServiceInstanceRegistrationAndRemoval(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "应用主机", "kind": "LOCAL"}, 201))["id"].(string)
	draft := projectDraft28("安防平台")
	draft.Modules = append(draft.Modules, applicationModule{ID: uuid.NewString(), Name: "platform-api"})
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	basePath := "/application-projects/" + p.ID
	path := basePath + "/service-instances"
	legacyEnvironment := p.Environments[0]
	input := applicationServiceInstanceInput{Version: p.Version, ModuleID: p.Modules[1].ID, Environment: "qa", HostID: host, RecordedVersion: "qa-1.2.3"}
	b.call("POST", path, applicationServiceInstanceInput{Version: p.Version, ModuleID: p.Modules[1].ID, Environment: "unused"}, 400)
	invalid := input
	invalid.HostID = uuid.NewString()
	b.call("POST", path, invalid, 400)
	invalid = input
	invalid.ModuleID = uuid.NewString()
	b.call("POST", path, invalid, 400)
	invalid = input
	invalid.RecordedVersion = "password=testing-only"
	b.call("POST", path, invalid, 400)
	invalid.RecordedVersion = strings.Repeat("版", 101)
	b.call("POST", path, invalid, 400)
	p = applicationResponse(t, b.call("POST", path, input, 200))
	if p.Version != "2" || len(p.Environments) != 1 || p.Environments[0].Name != "QA" || p.Environments[0].ID != legacyEnvironment.ID || len(p.Environments[0].Deployments) != 2 {
		t.Fatal("registration did not reuse the existing environment", p)
	}
	qa := p.Environments[0].Deployments[1]
	if qa.HostID != host || qa.RecordedVersion != "qa-1.2.3" || qa.RuntimeSoftwareID != "" || len(qa.Ports) != 0 || !reflect.DeepEqual(legacyEnvironment.Deployments[0], p.Environments[0].Deployments[0]) {
		t.Fatal("new registration changed runtime facts or the older record", p.Environments[0])
	}
	b.call("POST", path, input, 409)
	input.Version = p.Version
	if response := b.call("POST", path, input, 409); !strings.Contains(response.Body.String(), "APPLICATION_INSTANCE_DUPLICATED") {
		t.Fatal(response.Body.String())
	}
	input.Environment, input.RecordedVersion = "生产", "1.1.0"
	p = applicationResponse(t, b.call("POST", path, input, 200))
	if p.Version != "3" || len(p.Environments) != 2 || p.Environments[1].Name != "生产" || p.Environments[1].Deployments[0].RecordedVersion != "1.1.0" {
		t.Fatal("production registration was not independent", p)
	}
	// A full-project client unaware of recordedVersion should preserve the existing values.
	p.Environments[0].Deployments[1].RecordedVersion = ""
	p = applicationResponse(t, b.call("PATCH", basePath, p, 200))
	if p.Environments[0].Deployments[1].RecordedVersion != "qa-1.2.3" {
		t.Fatal("legacy full-project update lost the QA version", p)
	}
	p = applicationResponse(t, b.call("PATCH", basePath+"/catalog", map[string]any{"version": p.Version, "archived": true}, 200))
	archivedProjects := dataOf(t, b.call("GET", "/application-projects?archived=true", nil, 200))
	if !p.Archived || archivedProjects["total"] != float64(1) {
		t.Fatal("archived registration is missing from the archived project list", p, archivedProjects)
	}
	version := map[string]string{"version": p.Version}
	legacyID := legacyEnvironment.Deployments[0].ID
	b.call("DELETE", basePath+"/modules/"+p.Modules[0].ID, version, 409)
	b.call("DELETE", path+"/"+legacyID, map[string]string{"version": "1"}, 409)
	p = applicationResponse(t, b.call("DELETE", path+"/"+legacyID, version, 200))
	if p.Version != "6" || !p.Archived || len(p.Environments) != 2 || p.Environments[0].ID != legacyEnvironment.ID || len(p.Environments[0].Deployments) != 1 || p.Environments[0].Deployments[0].ID != qa.ID {
		t.Fatal("deletion must leave other registrations and the environment intact", p)
	}
	p = applicationResponse(t, b.call("DELETE", basePath+"/modules/"+p.Modules[0].ID, map[string]string{"version": p.Version}, 200))
	if !p.Archived || len(p.Modules) != 1 || p.Modules[0].Name != "platform-api" {
		t.Fatal("dependency cleanup should preserve the archived project and its other modules", p)
	}
	var jobs, audited int
	if err := s.DB.QueryRow(`SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM audit WHERE action IN ('APPLICATION_SERVICE_INSTANCE_SAVED','APPLICATION_SERVICE_INSTANCE_DELETED'))`).Scan(&jobs, &audited); err != nil || jobs != 0 || audited != 3 {
		t.Fatal("registration must have audit records without creating any jobs", jobs, audited, err)
	}
}

func TestAlgorithmModuleRegistersDistinctHostsInOneEnvironment(t *testing.T) {
	s, b := setup(t)
	b.login()
	hostA := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "算法服务器 A", "kind": "LOCAL"}, 201))["id"].(string)
	hostB := uuid.NewString()
	if _, err := s.DB.Exec("INSERT INTO targets(id,name,install_root,kind,address,created_at) VALUES(?,?,?,'SSH',?,?)",
		hostB, "算法服务器 B", "/opt/algorithm", "192.0.2.10", Now()); err != nil {
		t.Fatal(err)
	}
	draft := projectDraft28("算法两台服务器")
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "ALGORITHM"}
	draft.Environments[0].Deployments[0].HostID = hostA
	draft.Environments[0].Deployments[0].ServicePort = 8101
	draft.Modules = append(draft.Modules, applicationModule{ID: uuid.NewString(), Name: "platform-api",
		Catalog: &applicationModuleCatalog{Kind: "BACKEND"}})
	project := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	base := "/application-projects/" + project.ID
	path := base + "/service-instances"
	port := 8101
	input := applicationServiceInstanceInput{Version: project.Version, ModuleID: project.Modules[0].ID,
		Environment: "qa", HostID: hostB, ServicePort: &port}
	project = applicationResponse(t, b.call("POST", path, input, 200))
	env := project.Environments[0]
	if len(project.Environments) != 1 || len(env.Deployments) != 2 ||
		env.Deployments[0].HostID != hostA || env.Deployments[1].HostID != hostB ||
		env.Deployments[0].ID == env.Deployments[1].ID {
		t.Fatal("algorithm registrations were not distinct within one environment", env)
	}
	input.Version = project.Version
	if res := b.call("POST", path, input, 409); !strings.Contains(res.Body.String(), "APPLICATION_INSTANCE_DUPLICATED") {
		t.Fatal(res.Body.String())
	}
	input.ID, input.HostID = env.Deployments[1].ID, hostA
	b.call("POST", path, input, 409)
	if res := b.call("PATCH", base+"/catalog", applicationCatalogInput{Version: project.Version,
		Modules: []applicationCatalogModule{{ID: project.Modules[0].ID, Name: project.Modules[0].Name,
			RepositoryID: project.Modules[0].RepositoryID, Directory: project.Modules[0].Directory,
			Catalog: &applicationModuleCatalog{Kind: "BACKEND"}}}}, 400); !strings.Contains(res.Body.String(), "不同服务器") {
		t.Fatal(res.Body.String())
	}
	// The comprehensive draft endpoint follows the same rules and preserves both identities.
	project = applicationResponse(t, b.call("PATCH", base, project, 200))
	if !reflect.DeepEqual(project.Environments[0].Deployments, env.Deployments) {
		t.Fatal("full project edit changed registered hosts")
	}
	input = applicationServiceInstanceInput{Version: project.Version, ModuleID: project.Modules[1].ID,
		Environment: env.Name, HostID: hostA}
	project = applicationResponse(t, b.call("POST", path, input, 200))
	input.Version, input.HostID = project.Version, hostB
	b.call("POST", path, input, 409)
	project = applicationResponse(t, b.call("DELETE", path+"/"+env.Deployments[1].ID,
		map[string]string{"version": project.Version}, 200))
	if len(project.Environments[0].Deployments) != 2 || project.Environments[0].Deployments[0].ID != env.Deployments[0].ID {
		t.Fatal("removing one host changed the other registration")
	}
}

func TestAlgorithmRegistrationDoesNotIntroduceStaticMode(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "算法主机", "kind": "LOCAL"}, 201))["id"].(string)
	draft := projectDraft28("算法部署形态")
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "ALGORITHM"}
	draft.Environments[0].Deployments[0].HostID = host
	draft.Environments[0].Deployments[0].DeployMode = "STATIC"
	checkInvalid := func(method, path string, input any) {
		t.Helper()
		if response := b.call(method, path, input, 400); !strings.Contains(response.Body.String(), "只能登记原生进程或 Docker 容器") {
			t.Fatal(response.Body.String())
		}
	}
	checkInvalid("POST", "/application-projects", draft)
	draft.Environments[0].Deployments[0].DeployMode = "NATIVE"
	project := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	base := "/application-projects/" + project.ID
	registrations := base + "/service-instances"

	static, docker := " STATIC ", "DOCKER"
	input := applicationServiceInstanceInput{Version: project.Version, ModuleID: project.Modules[0].ID,
		Environment: "staging", HostID: host, DeployMode: &static}
	checkInvalid("POST", registrations, input)
	input.ID, input.Environment = project.Environments[0].Deployments[0].ID, "QA"
	checkInvalid("POST", registrations, input)
	project.Environments[0].Deployments[0].DeployMode = "STATIC"
	checkInvalid("PATCH", base, project)

	// Simulate a static registration saved before the new mode restriction.
	if _, err := s.DB.Exec(`UPDATE application_deployments SET config_json=json_set(config_json,'$.deployMode','STATIC') WHERE id=?`, input.ID); err != nil {
		t.Fatal(err)
	}
	input.RecordedVersion = "historical-static"
	project = applicationResponse(t, b.call("POST", registrations, input, 200))
	if row := project.Environments[0].Deployments[0]; row.DeployMode != "STATIC" || row.RecordedVersion != input.RecordedVersion {
		t.Fatalf("legacy static registration was not preserved: %+v", row)
	}
	project = applicationResponse(t, b.call("PATCH", base, project, 200))
	renamed := "算法部署形态更新"
	project = applicationResponse(t, b.call("PATCH", base+"/catalog",
		applicationCatalogInput{Version: project.Version, Name: &renamed}, 200))

	input.Version, input.DeployMode = project.Version, &docker
	project = applicationResponse(t, b.call("POST", registrations, input, 200))
	if mode := project.Environments[0].Deployments[0].DeployMode; mode != "DOCKER" {
		t.Fatal("legacy registration could not convert to a container:", mode)
	}
	input.Version, input.DeployMode = project.Version, &static
	checkInvalid("POST", registrations, input)

	frontend := projectDraft28("前端静态形态")
	frontend.Modules[0].Catalog = &applicationModuleCatalog{Kind: "FRONTEND"}
	frontend.Environments[0].Deployments[0].DeployMode = "STATIC"
	project = applicationResponse(t, b.call("POST", "/application-projects", frontend, 200))
	module := project.Modules[0]
	checkInvalid("PATCH", "/application-projects/"+project.ID+"/catalog", applicationCatalogInput{
		Version: project.Version, Modules: []applicationCatalogModule{{ID: module.ID, Name: module.Name, RepositoryID: module.RepositoryID,
			Directory: module.Directory, Catalog: &applicationModuleCatalog{Kind: "ALGORITHM"}}},
	})
}

func TestApplicationServiceInstanceEditKeepsRuntimeAndLegacyConfig(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "应用主机", "kind": "LOCAL"}, 201))["id"].(string)
	otherHost := uuid.NewString()
	if _, err := s.DB.Exec("INSERT INTO targets(id,name,install_root,kind,address,created_at) VALUES(?,?,?,'SSH',?,?)", otherHost, "备用主机", "/opt/instance-host", "192.0.2.10", Now()); err != nil {
		t.Fatal(err)
	}
	software := registerTestSoftware(t, b, host, "TOOL", "python", "PYTHON")["id"].(string)
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "构建凭据", "kind": "TOKEN", "secret": "service-instance-fixture-token"}, 201))["id"].(string)
	draft := projectDraft28("保留历史配置")
	deployment := &draft.Environments[0].Deployments[0]
	deployment.HostID = host
	deployment.RuntimeSoftwareID = software
	deployment.Variables = []applicationVariable{{Name: "API_TOKEN", CredentialID: credential, CredentialRevision: "1"}}
	deployment.Ports = []applicationPort{{Protocol: "TCP", HostPort: 8080, ContainerPort: 8080}}
	draft.Modules = append(draft.Modules, applicationModule{ID: uuid.NewString(), Name: "未绑定软件的模块"})
	draft.Environments[0].Deployments = append(draft.Environments[0].Deployments,
		applicationDeployment{ID: uuid.NewString(), ModuleID: draft.Modules[1].ID, Ports: deployment.Ports})
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	legacyID := p.Environments[0].Deployments[0].ID
	if _, err := s.DB.Exec(`UPDATE application_deployments SET config_json=json_set(config_json,'$.futureField',json('{"keep":true}')) WHERE id=?`, legacyID); err != nil {
		t.Fatal(err)
	}
	otherProject := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("另一个项目"), 200))
	path := "/application-projects/" + p.ID + "/service-instances"
	input := applicationServiceInstanceInput{Version: p.Version, ID: legacyID, ModuleID: p.Modules[0].ID, Environment: "QA", HostID: otherHost, RecordedVersion: "custom-43"}
	if response := b.call("POST", path, input, 409); !strings.Contains(response.Body.String(), "APPLICATION_INSTANCE_IN_USE") {
		t.Fatal(response.Body.String())
	}
	b.call("DELETE", path+"/"+legacyID, map[string]string{"version": p.Version}, 409)
	for _, invalid := range []applicationServiceInstanceInput{
		{Version: p.Version, ID: legacyID, ModuleID: otherProject.Modules[0].ID, Environment: "QA", HostID: host},
		{Version: p.Version, ID: legacyID, ModuleID: input.ModuleID, Environment: "PROD", HostID: host},
	} {
		b.call("POST", path, invalid, 400)
	}
	invalid := input
	invalid.ID = otherProject.Environments[0].Deployments[0].ID
	b.call("POST", path, invalid, 404)
	b.call("DELETE", path+"/"+invalid.ID, map[string]string{"version": p.Version}, 404)
	before := p.Environments[0].Deployments[0]
	input.HostID = ""
	p = applicationResponse(t, b.call("POST", path, input, 200))
	before.RecordedVersion = input.RecordedVersion
	if p.Version != "2" || !reflect.DeepEqual(before, p.Environments[0].Deployments[0]) {
		t.Fatal("editing recorded version changed runtime configuration", p.Environments[0].Deployments[0])
	}
	var future bool
	if err := s.DB.QueryRow(`SELECT json_extract(config_json,'$.futureField.keep') FROM application_deployments WHERE id=?`, legacyID).Scan(&future); err != nil || !future {
		t.Fatal("editing the registration discarded fields unknown to the controller", future, err)
	}
	withoutRuntime := p.Environments[0].Deployments[1]
	input = applicationServiceInstanceInput{Version: p.Version, ID: withoutRuntime.ID, ModuleID: withoutRuntime.ModuleID, Environment: "qa", HostID: host}
	b.call("POST", path, input, 400)
	input.HostID = otherHost
	p = applicationResponse(t, b.call("POST", path, input, 200))
	if p.Version != "3" || p.Environments[0].Deployments[1].HostID != otherHost || p.Environments[0].Deployments[0].HostID != host {
		t.Fatal("host change did not enforce the existing port constraint", p.Environments[0].Deployments)
	}
}

func TestApplicationServiceInstanceBindsObservedRuntimeAndCanDetach(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "运行主机", "kind": "LOCAL"}, 201))["id"].(string)
	otherHost := uuid.NewString()
	if _, err := s.DB.Exec("INSERT INTO targets(id,name,install_root,kind,address,created_at) VALUES(?,?,?,'SSH',?,?)", otherHost, "其他主机", "/opt/other-runtime", "192.0.2.11", Now()); err != nil {
		t.Fatal(err)
	}
	server := registerTestSoftware(t, b, host, "SYSTEMD", "platform-api.service", "OTHER")["id"].(string)
	replacement := registerTestSoftware(t, b, host, "DOCKER", "platform-api", "OTHER")["id"].(string)
	remote := registerTestSoftware(t, b, otherHost, "SYSTEMD", "platform-api.service", "OTHER")["id"].(string)
	tool := registerTestSoftware(t, b, host, "TOOL", "python", "PYTHON")["id"].(string)
	released := registerTestSoftware(t, b, host, "SYSTEMD", "retired.service", "OTHER")["id"].(string)
	jenkins := registerTestSoftware(t, b, host, "SYSTEMD", "jenkins.service", "JENKINS")["id"].(string)
	draft := projectDraft28("关联运行实例")
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	base := "/application-projects/" + p.ID + "/service-instances"
	input := applicationServiceInstanceInput{
		Version: p.Version, ModuleID: p.Modules[0].ID, Environment: "test", HostID: host, RuntimeSoftwareID: &server,
	}
	b.call("POST", base, input, 400) // The registered object has no observed identity yet.
	if _, err := s.DB.Exec("UPDATE software_instances SET identity_hash=? WHERE id IN (?,?,?,?,?,?)", strings.Repeat("1", 64), server, replacement, remote, tool, released, jenkins); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE software_instances SET management_mode='RELEASED' WHERE id=?", released); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{remote, tool, released, jenkins, "invalid"} {
		input.RuntimeSoftwareID = &invalid
		b.call("POST", base, input, 400)
	}
	input.RuntimeSoftwareID = &server
	p = applicationResponse(t, b.call("POST", base, input, 200))
	input.ID = p.Environments[1].Deployments[0].ID
	assertBinding := func(expected string) {
		t.Helper()
		var column, stored string
		err := s.DB.QueryRow(`SELECT coalesce(runtime_software_id,''),json_extract(config_json,'$.runtimeSoftwareId') FROM application_deployments WHERE id=?`, input.ID).Scan(&column, &stored)
		if err != nil || column != expected || stored != expected || p.Environments[1].Deployments[0].RuntimeSoftwareID != expected {
			t.Fatal("different runtime associations in database and response", column, stored, p.Environments[1].Deployments[0].RuntimeSoftwareID, err)
		}
	}
	assertBinding(server)
	input.Version, input.RuntimeSoftwareID, input.RecordedVersion = p.Version, nil, "1.0"
	p = applicationResponse(t, b.call("POST", base, input, 200))
	assertBinding(server)
	b.call("DELETE", base+"/"+input.ID, map[string]string{"version": p.Version}, 409)
	input.Version, input.RuntimeSoftwareID = p.Version, &replacement
	p = applicationResponse(t, b.call("POST", base, input, 200))
	assertBinding(replacement)
	detach := ""
	input.Version, input.RuntimeSoftwareID, input.HostID = p.Version, &detach, otherHost
	b.call("POST", base, input, 409)
	input.HostID = ""
	p = applicationResponse(t, b.call("POST", base, input, 200))
	assertBinding("")
	input.Version, input.HostID, input.RuntimeSoftwareID = p.Version, otherHost, &remote
	p = applicationResponse(t, b.call("POST", base, input, 200))
	assertBinding(remote)
	if p.Environments[1].Deployments[0].HostID != otherHost {
		t.Fatal("binding changed software without changing the previously unbound host")
	}
}

func TestApplicationServiceInstanceOneStopConfigAndComputedURL(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := uuid.NewString()
	if _, err := s.DB.Exec("INSERT INTO targets(id,name,install_root,kind,address,created_at) VALUES(?,?,?,'SSH',?,?)", host, "业务服务器", "/opt/app-host", "10.20.30.40", Now()); err != nil {
		t.Fatal(err)
	}
	nginxID := registerTestSoftware(t, b, host, "DOCKER", "nginx-web", "NGINX")["id"].(string)
	mysqlID := registerTestSoftware(t, b, host, "DOCKER", "mysql-db", "MYSQL")["id"].(string)
	dbPassCred := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "MySQL 密码", "kind": "PASSWORD", "secret": "db-pass-fixture-secret"}, 201))["id"].(string)

	created := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]any{
		"name": "企业安防平台",
		"modules": []applicationCatalogModule{
			{Name: "platform-api", Directory: "platform-api", Catalog: &applicationModuleCatalog{Kind: "BACKEND", Environment: "JDK 21", RuntimeMode: "NATIVE"}},
			{Name: "platform-web", Directory: "platform-web", Catalog: &applicationModuleCatalog{Kind: "FRONTEND", Environment: "Node 24", RuntimeMode: "STATIC"}},
		},
	}, 200))
	apiModule := created.Modules[0]
	webModule := created.Modules[1]
	endpoint := "/application-projects/" + created.ID + "/service-instances"

	// Reject plaintext password variable when not referencing a credential.
	deployNative := "NATIVE"
	profileTest := "test"
	port18080 := 18080
	startCmd := "java -Xms256m -jar target/platform-api.jar --spring.profiles.active=test"
	healthPath := "/actuator/health"
	badVars := []applicationVariable{{Name: "DB_PASSWORD", Value: "plain-secret"}}
	b.call("POST", endpoint, applicationServiceInstanceInput{
		Version: created.Version, ModuleID: apiModule.ID, Environment: "test", HostID: host,
		DeployMode: &deployNative, ServicePort: &port18080, Variables: &badVars,
	}, 400)

	goodVars := []applicationVariable{
		{Name: "DB_URL", Value: "jdbc:mysql://10.20.30.40:3306/streamfusion"},
		{Name: "DB_PASSWORD", CredentialID: dbPassCred, CredentialRevision: "1"},
	}
	deps := []string{mysqlID}
	p := applicationResponse(t, b.call("POST", endpoint, applicationServiceInstanceInput{
		Version: created.Version, ModuleID: apiModule.ID, Environment: "test", HostID: host,
		DeployMode: &deployNative, Profile: &profileTest, ServicePort: &port18080,
		StartCommand: &startCmd, HealthPath: &healthPath, Variables: &goodVars, DependencyIDs: &deps,
	}, 200))

	apiDep := p.Environments[0].Deployments[0]
	if apiDep.DeployMode != "NATIVE" || apiDep.Profile != "test" || apiDep.ServicePort != 18080 || apiDep.StartCommand != startCmd || apiDep.HealthPath != healthPath {
		t.Fatalf("unexpected backend deployment config: %+v", apiDep)
	}
	if apiDep.ComputedAccessURL != "http://10.20.30.40:18080" || len(apiDep.Variables) != 2 || len(apiDep.DependencyIDs) != 1 {
		t.Fatalf("unexpected computed URL / vars / deps: %+v", apiDep)
	}

	// Frontend Nginx static deployment with reverse proxy pointing to platform-api module.
	deployStatic := "STATIC"
	nginxCfg := applicationNginxDeploy{
		NginxSoftwareID: nginxID,
		SiteName:        "platform-web-test",
		ServerName:      "_",
		ListenPort:      80,
		SPA:             true,
		ProxyRules:      []applicationNginxProxyRule{{Location: "/api", TargetModuleID: apiModule.ID, StripPrefix: true}},
	}
	p = applicationResponse(t, b.call("POST", endpoint, applicationServiceInstanceInput{
		Version: p.Version, ModuleID: webModule.ID, Environment: "test", HostID: host,
		DeployMode: &deployStatic, Nginx: &nginxCfg,
	}, 200))
	webDep := p.Environments[0].Deployments[1]
	if webDep.DeployMode != "STATIC" || webDep.Nginx == nil || webDep.Nginx.NginxSoftwareID != nginxID || len(webDep.Nginx.ProxyRules) != 1 || webDep.ComputedAccessURL != "http://10.20.30.40" {
		t.Fatalf("unexpected frontend nginx deployment config: %+v", webDep)
	}

	// Verify custom domain overrides host IP in computed access URL cleanly.
	nginxCfg.ServerName = "ops.example.local"
	nginxCfg.ListenPort = 8088
	p = applicationResponse(t, b.call("POST", endpoint, applicationServiceInstanceInput{
		Version: p.Version, ID: webDep.ID, ModuleID: webModule.ID, Environment: "test", HostID: host,
		DeployMode: &deployStatic, Nginx: &nginxCfg,
	}, 200))
	if got := p.Environments[0].Deployments[1].ComputedAccessURL; got != "http://ops.example.local:8088" {
		t.Fatalf("expected http://ops.example.local:8088, got %q", got)
	}
}
