package controller

import (
	"testing"

	"github.com/google/uuid"
)

func TestProjectDeploymentStatusIsScopedToRegistration(t *testing.T) {
	s, b := setup(t)
	b.login()
	draft := projectDraft28("deployment-status-read")
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND"}
	draft.Environments[0].Deployments[0].DeployMode = "NATIVE"
	for _, entry := range []struct{ name, kind, mode string }{
		{"页面", "FRONTEND", "STATIC"},
		{"算法", "ALGORITHM", "NATIVE"},
		{"非标准部署", "BACKEND", "DOCKER"},
	} {
		module := draft.Modules[0]
		module.ID, module.Name, module.Catalog = uuid.NewString(), entry.name, &applicationModuleCatalog{Kind: entry.kind}
		draft.Modules = append(draft.Modules, module)
		draft.Environments[0].Deployments = append(draft.Environments[0].Deployments, applicationDeployment{
			ID: uuid.NewString(), ModuleID: module.ID, DeployMode: entry.mode,
			Nginx: &applicationNginxDeploy{ServerName: "app.example.test"},
		})
	}
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	other := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("another-deployment-status-read"), 200))
	path := "/application-projects/" + p.ID + "/deployments/"
	for i, want := range []any{"JAVA", "STATIC", nil, nil} {
		status := dataOf(t, b.call("GET", path+p.Environments[0].Deployments[i].ID, nil, 200))
		if status["kind"] != want || status["health"] != "NOT_DEPLOYED" {
			t.Fatalf("registration %d = %v, want kind %v and NOT_DEPLOYED", i, status, want)
		}
	}
	b.call("GET", path+other.Environments[0].Deployments[0].ID, nil, 404)
	b.call("GET", path+uuid.NewString(), nil, 404)

	// A bad sibling cannot break this registration's polling. The selected
	// configuration is still read and subjected to the normal error mapping.
	_, err := s.DB.Exec("UPDATE application_modules SET config_json='invalid' WHERE id=?", p.Modules[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	status := dataOf(t, b.call("GET", path+p.Environments[0].Deployments[0].ID, nil, 200))
	if status["kind"] != "JAVA" {
		t.Fatalf("unrelated module interfered with status: %v", status)
	}
	b.call("GET", path+p.Environments[0].Deployments[2].ID, nil, 500)
}
