package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func applicationReferenceDraft(hostID string) applicationProject {
	moduleID := uuid.NewString()
	return applicationProject{
		Code: "reference-project", Name: "引用保护项目",
		Modules:      []applicationModule{{ID: moduleID, Name: "后端", Directory: ".", Build: applicationBuild{Kind: "COMMAND", HostID: hostID, Account: "ubuntu"}, Runtime: applicationRuntime{Kind: "NATIVE"}}},
		Environments: []applicationEnvironment{{ID: uuid.NewString(), Name: "test", Deployments: []applicationDeployment{{ID: uuid.NewString(), ModuleID: moduleID, HostID: hostID}}}},
	}
}

func applicationResponse(t *testing.T, response *httptest.ResponseRecorder) applicationProject {
	t.Helper()
	var result struct{ Data applicationProject }
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Data
}

func requireApplicationReference(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if body := response.Body.String(); !strings.Contains(body, "APPLICATION_REFERENCE_IN_USE") || !strings.Contains(body, "引用保护项目") {
		t.Fatal("missing actionable project reference", body)
	}
}

func TestApplicationProjectProtectsHostIdentityAndArchivedReferences(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "应用主机", "kind": "LOCAL"}, 201))
	id := host["id"].(string)
	project := applicationResponse(t, b.call("POST", "/application-projects", applicationReferenceDraft(id), 200))
	requireApplicationReference(t, b.call("PATCH", "/hosts/"+id, map[string]any{"name": "应用主机", "kind": "LOCAL", "installRoot": "/opt/changed-application-target", "expectedVersion": host["version"]}, 409))
	current, err := s.readTarget(id)
	if err != nil || current.InstallRoot != host["installRoot"] || current.Version != host["version"] {
		t.Fatal("rejected change altered the target", current, err)
	}
	// Display-only renaming remains available while connection identity is held.
	renamed := dataOf(t, b.call("PATCH", "/hosts/"+id, map[string]any{"name": "重命名应用主机", "kind": "LOCAL", "expectedVersion": current.Version}, 200))
	requireApplicationReference(t, b.call("DELETE", "/hosts/"+id+"?expectedVersion="+renamed["version"].(string), nil, 409))
	project = applicationResponse(t, b.call("POST", "/application-projects/"+project.ID+"/archive", map[string]string{"version": project.Version}, 200))
	if !project.Archived {
		t.Fatal("project was not archived")
	}
	requireApplicationReference(t, b.call("DELETE", "/hosts/"+id+"?expectedVersion="+renamed["version"].(string), nil, 409))
	var jobs int
	if err = s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("saving drafts or checking references created a remote job", jobs, err)
	}
}

func TestApplicationProjectProtectsSoftwareUntilReferencesAreRemoved(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "工具主机", "kind": "LOCAL"}, 201))["id"].(string)
	software := registerTestSoftware(t, b, host, "TOOL", "python", "PYTHON")
	id := software["id"].(string)
	draft := applicationReferenceDraft(host)
	draft.Modules[0].Build.SoftwareID = id
	draft.Environments[0].Deployments[0].RuntimeSoftwareID = id
	project := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	input := map[string]any{"expectedVersion": software["version"], "idempotencyKey": "application-tool-unmanage"}
	requireApplicationReference(t, b.call("POST", "/software-instances/"+id+"/unmanage", input, 409))
	current, err := s.readSoftware(id)
	if err != nil || current.ManagementMode != "OBSERVE" {
		t.Fatal("reference rejection changed software ownership", current, err)
	}
	// Keeping only the runtime reference must still protect the object.
	project.Modules[0].Build.SoftwareID = ""
	project = applicationResponse(t, b.call("PATCH", "/application-projects/"+project.ID, project, 200))
	requireApplicationReference(t, b.call("POST", "/software-instances/"+id+"/unmanage", input, 409))
	project.Environments[0].Deployments[0].RuntimeSoftwareID = ""
	applicationResponse(t, b.call("PATCH", "/application-projects/"+project.ID, project, 200))
	b.call("POST", "/software-instances/"+id+"/unmanage", input, 200)
}

func TestApplicationProjectCredentialsProtectExactRevisionAndKeepSecretsOutOfDraft(t *testing.T) {
	_, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "凭据主机", "kind": "LOCAL"}, 201))["id"].(string)
	secret := "project-reference-test-sensitive-token"
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "应用Token", "kind": "TOKEN", "secret": secret}, 201))
	id := credential["id"].(string)
	b.call("POST", "/credentials/"+id+"/revisions", map[string]string{"name": "应用Token", "kind": "TOKEN", "secret": secret + "-next", "expectedRevision": "1"}, 201)
	draft := applicationReferenceDraft(host)
	draft.Environments[0].Deployments[0].Variables = []applicationVariable{{Name: "API_TOKEN", CredentialID: id, CredentialRevision: "1"}}
	project := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	response := b.call("POST", "/credentials/"+id+"/revisions/1/revoke", nil, 409)
	if !strings.Contains(response.Body.String(), "CREDENTIAL_IN_USE") {
		t.Fatal(response.Body.String())
	}
	// A different, unreferenced revision remains revocable.
	b.call("POST", "/credentials/"+id+"/revisions/2/revoke", nil, 200)
	for _, path := range []string{"/application-projects/" + project.ID, "/audit?limit=100"} {
		if strings.Contains(b.call("GET", path, nil, 200).Body.String(), secret) {
			t.Fatal("project metadata or audit exposed secret material")
		}
	}
	project.Environments[0].Deployments[0].Variables = nil
	applicationResponse(t, b.call("PATCH", "/application-projects/"+project.ID, project, 200))
	b.call("POST", "/credentials/"+id+"/revisions/1/revoke", nil, 200)
}

func TestApplicationProjectProtectsGitAccountWithoutRepositoryBinding(t *testing.T) {
	s, b, host, account, remote := gitControllerFixture(t)
	draft := applicationReferenceDraft(host)
	repositoryID := uuid.NewString()
	draft.Repositories = []applicationRepository{{ID: repositoryID, Name: "后端仓库", URL: "git@git.example:team/repo.git", GitAccountID: account["id"].(string), Branch: "main"}}
	draft.Modules[0].RepositoryID = repositoryID
	project := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	remote.mu.Lock()
	before, accountCalls := remote.calls, remote.accountCalls
	remote.mu.Unlock()
	hostRecord, err := s.readTarget(host)
	if err != nil {
		t.Fatal(err)
	}
	// The account UI removes terminal configuration before deleting its record.
	// Both entry points must reject the reference before touching the target.
	requireApplicationReference(t, b.call("POST", "/git-accounts/"+account["id"].(string)+"/remove-configuration", map[string]any{"expectedVersion": account["version"], "expectedHostVersion": hostRecord.Version}, 409))
	requireApplicationReference(t, b.call("DELETE", "/git-accounts/"+account["id"].(string), map[string]any{"expectedVersion": account["version"]}, 409))
	remote.mu.Lock()
	after, afterAccounts := remote.calls, remote.accountCalls
	remote.mu.Unlock()
	if before != after || accountCalls != afterAccounts {
		t.Fatal("project account reference check performed remote account or repository operations")
	}
	var bindings int
	if err := s.DB.QueryRow("SELECT count(*) FROM git_repositories").Scan(&bindings); err != nil || bindings != 0 {
		t.Fatal("project configuration unexpectedly bound a repository", bindings, err)
	}
	project.Repositories[0].GitAccountID = ""
	applicationResponse(t, b.call("PATCH", "/application-projects/"+project.ID, project, 200))
	b.call("DELETE", "/git-accounts/"+account["id"].(string), map[string]any{"expectedVersion": account["version"]}, 200)
}

func TestApplicationProjectProtectsNodeEnvironmentReleaseThroughItsVersion(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, target := nodeFixture(t, s, b)
	discovered := dataOf(t, b.call("POST", "/hosts/"+host+"/node-environments/discover", map[string]string{"expectedHostVersion": "1", "account": "ubuntu"}, 200))
	registered := dataOf(t, b.call("POST", "/hosts/"+host+"/node-environments/register", map[string]any{"expectedHostVersion": "1", "name": "项目Node", "candidate": discovered["candidates"].([]any)[0]}, 200))
	environmentID := registered["environmentId"].(string)
	environment, err := s.readNodeEnvironment(environmentID)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := s.nodeEnvironmentVersions(environmentID)
	if err != nil || len(versions) != 1 {
		t.Fatal("expected registered Node version", versions, err)
	}
	draft := applicationReferenceDraft(host)
	draft.Modules[0].Build.SoftwareID = versions[0].ID
	project := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	input := map[string]any{"expectedVersion": environment.Version, "confirmed": true, "confirmationName": environment.Name}
	requireApplicationReference(t, b.call("POST", "/node-environments/"+environmentID+"/release", input, 409))
	after, err := s.readNodeEnvironment(environmentID)
	if err != nil || after.Version != environment.Version || after.ManagementMode != "OBSERVE" {
		t.Fatal("blocked release changed the environment", after, err)
	}
	project.Modules[0].Build.SoftwareID = ""
	applicationResponse(t, b.call("PATCH", "/application-projects/"+project.ID, project, 200))
	b.call("POST", "/node-environments/"+environmentID+"/release", input, 200)
	if target.mutations.Load() != 0 {
		t.Fatal("draft or release metadata unexpectedly mutated the Node target")
	}
}
