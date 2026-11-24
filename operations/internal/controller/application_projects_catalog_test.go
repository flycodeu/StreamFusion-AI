package controller

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestApplicationProjectCatalogRegistersWithNameAndKeepsHistory(t *testing.T) {
	s, b := setup(t)
	b.call("POST", "/application-projects/catalog", map[string]string{"name": "待登记"}, 401)
	b.login()
	registered := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]string{"name": " 仅名称 "}, 200))
	if registered.Name != "仅名称" || registered.Code != "project-"+registered.ID || registered.Version != "1" || len(registered.Modules)+len(registered.Repositories)+len(registered.Environments) != 0 {
		t.Fatalf("minimal registration failed: %+v", registered)
	}

	old := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("原有项目"), 200))
	oldModule := old.Modules[0]
	var deploymentJSON string
	if err := s.DB.QueryRow("SELECT config_json FROM application_deployments WHERE id=?", old.Environments[0].Deployments[0].ID).Scan(&deploymentJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE application_modules SET config_json=json_set(config_json,'$.legacyExtension','kept') WHERE id=?", oldModule.ID); err != nil {
		t.Fatal(err)
	}
	jenkins := "https://jenkins.example:8443/jenkins/job/Folder%20Name/job/StreamFusion/"
	catalog := &applicationModuleCatalog{Kind: "BACKEND", Environment: "JDK 21 + Maven Wrapper", StartCommand: "./mvnw spring-boot:run", RuntimeMode: "Spring Boot"}
	path := "/application-projects/" + old.ID
	updated := applicationResponse(t, b.call("PATCH", path+"/catalog", map[string]any{
		"version": old.Version, "description": "档案说明", "jenkinsUrl": jenkins,
		"modules": []applicationCatalogModule{{ID: oldModule.ID, Name: "平台 API", RepositoryID: oldModule.RepositoryID, Directory: "platform-api", Catalog: catalog}},
	}, 200))
	if updated.Code != old.Code || updated.Version != "2" || updated.Description != "档案说明" || updated.JenkinsURL != jenkins || !reflect.DeepEqual(updated.Repositories, old.Repositories) || !reflect.DeepEqual(updated.Environments, old.Environments) {
		t.Fatalf("catalog edit changed legacy data: %+v", updated)
	}
	module := updated.Modules[0]
	if module.ID != oldModule.ID || module.Name != "平台 API" || module.Directory != "platform-api" || !reflect.DeepEqual(module.Catalog, catalog) || !reflect.DeepEqual(module.Build, oldModule.Build) || module.Runtime != oldModule.Runtime {
		t.Fatalf("unexpected module after catalog edit: %+v", module)
	}
	var extension, actualDeploymentJSON string
	if err := s.DB.QueryRow("SELECT json_extract(config_json,'$.legacyExtension') FROM application_modules WHERE id=?", oldModule.ID).Scan(&extension); err != nil || extension != "kept" {
		t.Fatal("legacy module extension lost", extension, err)
	}
	if err := s.DB.QueryRow("SELECT config_json FROM application_deployments WHERE id=?", old.Environments[0].Deployments[0].ID).Scan(&actualDeploymentJSON); err != nil || actualDeploymentJSON != deploymentJSON {
		t.Fatal("legacy deployment JSON was rewritten", err)
	}
	b.call("PATCH", path+"/catalog", map[string]string{"version": old.Version, "description": "stale edit"}, 409)
	// A client of the existing whole-tree API drops fields it does not know.
	updated.JenkinsURL = ""
	updated.Modules[0].Catalog = nil
	updated = applicationResponse(t, b.call("PATCH", path, updated, 200))
	if updated.JenkinsURL != jenkins || !reflect.DeepEqual(updated.Modules[0].Catalog, catalog) {
		t.Fatal("legacy update lost catalog data")
	}
	archived := applicationResponse(t, b.call("POST", path+"/archive", map[string]string{"version": updated.Version}, 200))
	restored := applicationResponse(t, b.call("PATCH", path+"/catalog", map[string]any{"version": archived.Version, "archived": false, "jenkinsUrl": ""}, 200))
	if restored.Archived || restored.JenkinsURL != "" || !reflect.DeepEqual(restored.Modules[0].Catalog, catalog) || !reflect.DeepEqual(restored.Environments, old.Environments) {
		t.Fatal("restore or link removal changed other fields", restored)
	}
	var jobs int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("catalog writes created jobs", jobs, err)
	}
}

func TestApplicationProjectCatalogDeletesOnlyUnboundModule(t *testing.T) {
	s, b := setup(t)
	b.call("DELETE", "/application-projects/"+uuid.NewString()+"/modules/"+uuid.NewString(), map[string]string{"version": "1"}, 401)
	b.login()
	original := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("模块目录"), 200))
	endpoint := "/application-projects/" + original.ID
	project := applicationResponse(t, b.call("PATCH", endpoint+"/catalog", map[string]any{
		"version": original.Version, "modules": []applicationCatalogModule{{Name: "待分类占位", Directory: "."}},
	}, 200))
	placeholder := project.Modules[1]
	removePlaceholder := endpoint + "/modules/" + placeholder.ID
	confirmation := map[string]string{"version": project.Version}
	b.call("DELETE", removePlaceholder, map[string]string{}, 400)
	if result := b.call("DELETE", removePlaceholder, map[string]string{"version": original.Version}, 409); !strings.Contains(result.Body.String(), "VERSION_CONFLICT") {
		t.Fatal("stale delete was not rejected as a version conflict")
	}
	if result := b.call("DELETE", endpoint+"/modules/"+original.Modules[0].ID, confirmation, 409); !strings.Contains(result.Body.String(), "APPLICATION_MODULE_IN_USE") {
		t.Fatal("existing environment did not protect the module")
	}
	other := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]string{"name": "另一项目"}, 200))
	b.call("DELETE", "/application-projects/"+other.ID+"/modules/"+placeholder.ID, map[string]string{"version": other.Version}, 404)
	unchanged := applicationResponse(t, b.call("GET", endpoint, nil, 200))
	if !reflect.DeepEqual(unchanged, project) {
		t.Fatal("a rejected delete changed the project")
	}

	updated := applicationResponse(t, b.call("DELETE", removePlaceholder, confirmation, 200))
	if updated.Version != "3" || !reflect.DeepEqual(updated.Modules, original.Modules) || !reflect.DeepEqual(updated.Repositories, original.Repositories) || !reflect.DeepEqual(updated.Environments, original.Environments) {
		t.Fatal("delete rewrote unrelated project records", updated)
	}
	b.call("DELETE", removePlaceholder, map[string]string{"version": updated.Version}, 404)
	var audit, jobs int
	if err := s.DB.QueryRow(`SELECT (SELECT count(*) FROM audit WHERE action='APPLICATION_MODULE_DELETED' AND object_id=?), (SELECT count(*) FROM jobs)`, placeholder.ID).Scan(&audit, &jobs); err != nil || audit != 1 || jobs != 0 {
		t.Fatalf("delete should only record an audit entry: audit=%d jobs=%d err=%v", audit, jobs, err)
	}
}

func TestApplicationProjectCatalogValidatesLinksAndWritesAtomically(t *testing.T) {
	_, b := setup(t)
	b.login()
	for _, value := range []string{"//jenkins.test/job/x/", "javascript:alert(1)", "https://user:pass@jenkins.test/job/x/", "https://jenkins.test/job/x/?token=example", "https://jenkins.test:65536/job/x/", "http://a..b/job/x/", "http://[name]/job/x/", "https://jenkins.test/job/x%0a/"} {
		b.call("POST", "/application-projects/catalog", map[string]string{"name": "Bad link", "jenkinsUrl": value}, 400)
	}
	repositoryID := uuid.NewString()
	module := applicationCatalogModule{Name: "前端", RepositoryID: repositoryID, Directory: ".", Catalog: &applicationModuleCatalog{StartCommand: "npm run dev"}}
	project := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]any{
		"name": "项目", "repositories": []applicationCatalogRepository{{ID: repositoryID, Name: "主仓库", URL: "https://git.example/project.git"}}, "modules": []applicationCatalogModule{module},
	}, 200))
	if len(project.Modules) != 1 || project.Modules[0].RepositoryID != repositoryID || project.Modules[0].Catalog.StartCommand != "npm run dev" {
		t.Fatal("new module did not attach to repository", project)
	}
	other := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]string{"name": "另一个项目"}, 200))
	path := "/application-projects/" + other.ID
	b.call("PATCH", path+"/catalog", map[string]any{"version": other.Version, "description": "invalid", "modules": []applicationCatalogModule{module}}, 400)
	b.call("PATCH", path+"/catalog", map[string]any{"version": other.Version, "description": "invalid", "repositories": []applicationCatalogRepository{{ID: repositoryID, Name: "冲突"}}}, 409)
	loaded := applicationResponse(t, b.call("GET", path, nil, 200))
	if loaded.Version != other.Version || loaded.Description != "" || len(loaded.Repositories)+len(loaded.Modules) != 0 {
		t.Fatal("invalid request left a partial edit", loaded)
	}
}

func TestApplicationProjectCatalogCodeAndModuleFieldsRoundTrip(t *testing.T) {
	_, b := setup(t)
	b.login()
	b.call("POST", "/application-projects/catalog", map[string]string{"name": "Bad code", "code": "Invalid Code!"}, 400)
	b.call("POST", "/application-projects/catalog", map[string]any{
		"name": "Bad access url",
		"modules": []applicationCatalogModule{{
			Name: "api", Directory: "api",
			Catalog: &applicationModuleCatalog{AccessURL: "javascript:alert(1)"},
		}},
	}, 400)

	created := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]any{
		"name": "智慧安防平台",
		"code": "nt-camera",
		"modules": []applicationCatalogModule{{
			Name:      "platform-api",
			Directory: "platform-api",
			Catalog: &applicationModuleCatalog{
				Kind:         "BACKEND",
				Description:  "视频平台核心后端",
				Environment:  "JDK 21 + Maven Wrapper",
				Dependencies: []string{"MySQL", "Redis", "MySQL"},
				RuntimeMode:  "DOCKER",
				StartCommand: "java -jar app.jar",
				AccessURL:    "https://ops.example:8443/api/",
			},
		}},
	}, 200))
	if created.Code != "nt-camera" || len(created.Modules) != 1 {
		t.Fatalf("unexpected project: %+v", created)
	}
	cat := created.Modules[0].Catalog
	if cat == nil || cat.Description != "视频平台核心后端" || cat.AccessURL != "https://ops.example:8443/api/" || !reflect.DeepEqual(cat.Dependencies, []string{"MySQL", "Redis"}) {
		t.Fatalf("unexpected module catalog metadata: %+v", cat)
	}
	updated := applicationResponse(t, b.call("PATCH", "/application-projects/"+created.ID+"/catalog", map[string]any{
		"version": created.Version,
		"code":    "nt-camera-v2",
	}, 200))
	if updated.Code != "nt-camera-v2" {
		t.Fatalf("expected updated code nt-camera-v2, got %q", updated.Code)
	}
}

func TestApplicationProjectCatalogExtendedBuildFieldsRoundTrip(t *testing.T) {
	_, b := setup(t)
	b.login()
	created := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]any{
		"name": "全自动识别项目",
		"modules": []applicationCatalogModule{{
			Name:      "platform-api",
			Directory: "platform-api",
			Catalog: &applicationModuleCatalog{
				Kind:              "BACKEND",
				Environment:       "JDK 21、Spring Boot",
				Dependencies:      []string{"Java 21", "Maven", "MySQL", "Redis"},
				RuntimeMode:       "NATIVE",
				CheckCommand:      "java -version",
				TestCommand:       "sh mvnw test",
				SkipTests:         true,
				BuildCommand:      "sh mvnw -B -ntp -DskipTests package",
				ArtifactPath:      "target/*.jar",
				StartCommand:      "java -jar app.jar",
				DefaultPort:       18080,
				HealthPath:        "/actuator/health",
				Profiles:          []string{"test", "prod"},
				DefaultProfile:    "test",
				SupportsContainer: true,
				EnvHints: []applicationEnvHint{
					{Name: "DB_URL", DefaultValue: "jdbc:mysql://127.0.0.1:3306/app", Profile: "test"},
					{Name: "DB_PASSWORD", DefaultValue: "strip-secret-default", Sensitive: true, Profile: "test"},
				},
			},
		}},
	}, 200))
	cat := created.Modules[0].Catalog
	if cat == nil || !cat.SkipTests || cat.ArtifactPath != "target/*.jar" || cat.DefaultPort != 18080 || cat.DefaultProfile != "test" || !cat.SupportsContainer {
		t.Fatalf("unexpected extended catalog fields: %+v", cat)
	}
	if len(cat.EnvHints) != 2 || cat.EnvHints[1].DefaultValue != "" || !cat.EnvHints[1].Sensitive {
		t.Fatalf("sensitive hint default should be cleared: %+v", cat.EnvHints)
	}
	// Reject non-sensitive hint with embedded credential in default value.
	b.call("PATCH", "/application-projects/"+created.ID+"/catalog", map[string]any{
		"version": created.Version,
		"modules": []applicationCatalogModule{{
			ID: created.Modules[0].ID, Name: "platform-api", Directory: "platform-api",
			Catalog: &applicationModuleCatalog{
				Kind: "BACKEND", Environment: "JDK 21", RuntimeMode: "NATIVE",
				EnvHints: []applicationEnvHint{{Name: "SERVICE_DSN", DefaultValue: "https://user:pass@db.example/app"}},
			},
		}},
	}, 400)
}
