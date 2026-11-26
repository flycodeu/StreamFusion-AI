package controller

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProjectConnectionCreatesAndRunsTypedPipelines(t *testing.T) {
	store, b := setup(t)
	b.login()
	configs := map[string]string{}
	queuedJobs := map[string]string{}
	var created, queued int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.580.2")
		if r.URL.Path == "/login" {
			return
		}
		if user, token, ok := r.BasicAuth(); !ok || user != "qa-operator" || token != jenkinsFixtureToken {
			t.Error("missing project credential")
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/createItem" && r.Method == "POST" {
			name := r.URL.Query().Get("name")
			data, _ := io.ReadAll(r.Body)
			if !strings.HasPrefix(name, "sfops-qa-") || !strings.Contains(string(data), "<sandbox>true</sandbox>") || strings.Contains(string(data), jenkinsFixtureToken) {
				t.Error("unexpected generated workflow")
			}
			configs[name] = string(data)
			created++
			w.Header().Del("X-Jenkins")
			return
		}
		items := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(items) == 5 && items[0] == "queue" && items[1] == "item" && items[3] == "api" && items[4] == "json" {
			fmt.Fprintf(w, `{"id":%s,"task":{"url":%q}}`, items[2], queuedJobs[items[2]])
			return
		}
		if len(items) < 3 || items[0] != "job" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		payload, found := configs[items[1]]
		if !found {
			w.WriteHeader(404)
			return
		}
		switch items[2] {
		case "config.xml":
			w.Header().Del("X-Jenkins")
			fmt.Fprint(w, `<?xml version="1.1" encoding="UTF-8"?>`+payload)
		case "build":
			if r.Method != "POST" {
				t.Error("build must use POST")
			}
			queued++
			queuedJobs[fmt.Sprint(queued)] = "/job/" + items[1] + "/"
			w.Header().Set("Location", fmt.Sprintf("/queue/item/%d/", queued))
			w.Header().Del("X-Jenkins")
			w.WriteHeader(201)
		case "api":
			fmt.Fprint(w, `{"inQueue":false,"lastBuild":null}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	software := installedJenkinsFixture(t, store, b, upstream.URL)
	draft := projectDraft28("typed-qa")
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND"}
	draft.Modules[0].Directory = "stack/platform-api"
	for _, kind := range []string{"FRONTEND", "ALGORITHM", "OTHER"} {
		m := draft.Modules[0]
		m.ID = uuid.NewString()
		m.Name = kind
		m.Directory = "module-" + strings.ToLower(kind)
		m.Catalog = &applicationModuleCatalog{Kind: kind}
		if kind == "FRONTEND" {
			m.Directory = "stack/platform-web"
		}
		draft.Modules = append(draft.Modules, m)
	}
	otherRepo := applicationRepository{ID: uuid.NewString(), Name: "独立仓库", URL: "https://github.com/example/standalone.git", Branch: "main"}
	draft.Repositories = append(draft.Repositories, otherRepo)
	standaloneWeb := draft.Modules[1]
	standaloneWeb.ID, standaloneWeb.Name, standaloneWeb.RepositoryID = uuid.NewString(), "独立前端", otherRepo.ID
	draft.Modules = append(draft.Modules, standaloneWeb)
	pythonModule := draft.Modules[0]
	pythonModule.ID, pythonModule.Name, pythonModule.Directory = uuid.NewString(), "camera-inspector", "camera-inspector"
	pythonModule.Catalog = &applicationModuleCatalog{Kind: "OTHER", Environment: "Python >=3.12,<3.13、uv"}
	draft.Modules = append(draft.Modules, pythonModule)
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	root := "/application-projects/" + p.ID
	connection := root + "/jenkins-connection"
	settings := map[string]string{"expectedVersion": "0", "softwareId": software, "username": "qa-operator", "token": jenkinsFixtureToken}
	callSoftwareLogin(t, b, "POST", connection, softwareLoginTestPeer, b.encrypt(connection, settings), 200, "SUCCESS")
	if got := dataOf(t, b.call("GET", connection, nil, 200)); got["configured"] != true || got["buildAgentLabel"] != defaultJenkinsBuildAgentLabel || strings.Contains(fmt.Sprint(got), jenkinsFixtureToken) {
		t.Fatal("invalid connection metadata")
	}
	var firstReq map[string]string
	var firstScope, firstName string
	for i, m := range p.Modules {
		scope := root + "/modules/" + m.ID
		monitor := dataOf(t, b.call("GET", scope+"/jenkins-monitor", nil, 200))
		if m.Catalog.Kind == "OTHER" && m.ID != pythonModule.ID {
			if monitor["configured"] != false || monitor["reason"] == "" {
				t.Fatal(monitor)
			}
			continue
		}
		if monitor["configured"] != true {
			t.Fatal(monitor)
		}
		name := path.Base(strings.TrimRight(monitor["jobUrl"].(string), "/"))
		build := scope + "/jenkins-build"
		if got := dataOf(t, b.call("GET", build, nil, 200)); got["status"] != "NEVER_BUILT" {
			t.Fatal(got)
		}
		req := map[string]string{"requestId": uuid.NewString(), "expectedVersion": monitor["version"].(string)}
		for range 2 {
			b.call("POST", build, req, 200)
		}
		if i == 0 {
			firstReq = req
			firstScope = scope
			firstName = name
		}
		want := map[string]string{"BACKEND": "mvnw -B -ntp test", "FRONTEND": "pnpm install --frozen-lockfile", "ALGORITHM": "uv run --locked python -m pytest", "OTHER": "uv run --locked python -m pytest"}[m.Catalog.Kind]
		var workflow jenkinsWorkflow
		if err := xml.Unmarshal([]byte(configs[name]), &workflow); err != nil {
			t.Fatalf("decode %s workflow: %v", m.Catalog.Kind, err)
		}
		script := workflow.Definition.Script
		repository := draft.Repositories[0].URL
		if m.ID == standaloneWeb.ID {
			repository = otherRepo.URL
		}
		if !strings.Contains(script, want) || !strings.Contains(script, m.Directory) || !strings.Contains(script, repository) {
			t.Fatalf("incorrect %s pipeline", m.Catalog.Kind)
		}
		wantContract := m.Catalog.Kind == "FRONTEND" && m.ID != standaloneWeb.ID
		if strings.Contains(script, streamFusionWebAPIContract) != wantContract {
			t.Fatalf("incorrect StreamFusion site contract for %s in %s", m.Directory, repository)
		}
		if !strings.Contains(script, "agent { label 'sfops-build' }") || strings.Contains(script, "agent any") {
			t.Fatalf("%s pipeline did not target the default build label", m.Catalog.Kind)
		}
	}
	if created != 5 || queued != 5 {
		t.Fatalf("created=%d queued=%d", created, queued)
	}
	firstReq["requestId"] = uuid.NewString()
	b.call("POST", firstScope+"/jenkins-build", firstReq, 200)
	if created != 5 || queued != 6 {
		t.Fatal("existing managed workflow was not reused")
	}
	envScope := root + "/service-instances/" + p.Environments[0].Deployments[0].ID
	envMonitor := dataOf(t, b.call("GET", envScope+"/jenkins-monitor", nil, 200))
	if strings.Contains(envMonitor["jobUrl"].(string), firstName) {
		t.Fatal("module and environment share a job")
	}
	other := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("another-project"), 200))
	b.call("GET", "/application-projects/"+other.ID+"/modules/"+p.Modules[0].ID+"/jenkins-monitor", nil, 404)
	// A second fresh request must not replace a different workflow at the expected URL.
	configs[firstName] = "<flow-definition><description>different job</description></flow-definition>"
	firstReq["requestId"] = uuid.NewString()
	b.call("POST", firstScope+"/jenkins-build", firstReq, 409)
	if created != 5 || queued != 6 {
		t.Fatal("unowned workflow ran")
	}
	settings["expectedVersion"] = "1"
	settings["token"] = ""
	settings["buildAgentLabel"] = "trusted || built-in"
	callSoftwareLogin(t, b, "POST", connection, softwareLoginTestPeer, b.encrypt(connection, settings), 400, "INVALID_INPUT")
	settings["buildAgentLabel"] = "linux.build-21"
	callSoftwareLogin(t, b, "POST", connection, softwareLoginTestPeer, b.encrypt(connection, settings), 200, "SUCCESS")
	b.call("POST", firstScope+"/jenkins-build", firstReq, 409)
	for _, scope := range []jenkinsScope{{project: p.ID, module: p.Modules[0].ID},
		{project: p.ID, instance: p.Environments[0].Deployments[0].ID}} {
		_, monitor, _, err := effectiveJenkinsMonitor(store.DB, scope)
		if err != nil || monitor.automation == nil || monitor.automation.name == firstName ||
			!strings.Contains(monitor.automation.script, "agent { label 'linux.build-21' }") {
			t.Fatal("the new label did not change the generated workflow binding")
		}
	}
	settings["expectedVersion"] = "2"
	delete(settings, "buildAgentLabel")
	saved := callSoftwareLogin(t, b, "POST", connection, softwareLoginTestPeer, b.encrypt(connection, settings), 200, "SUCCESS")
	if got := dataOf(t, saved); got["buildAgentLabel"] != "linux.build-21" {
		t.Fatal("omitting the label changed the saved setting")
	}
	settings["expectedVersion"] = "3"
	settings["buildAgentLabel"] = ""
	saved = callSoftwareLogin(t, b, "POST", connection, softwareLoginTestPeer, b.encrypt(connection, settings), 200, "SUCCESS")
	if got := dataOf(t, saved); got["buildAgentLabel"] != defaultJenkinsBuildAgentLabel {
		t.Fatal("clearing the label did not restore the default")
	}
	var bindings int
	if err := store.DB.QueryRow("SELECT count(*) FROM application_deployments WHERE project_id=?", p.ID).Scan(&bindings); err != nil || bindings != 1 {
		t.Fatalf("QA created deployment bindings: %d", bindings)
	}
	dir := store.Dir
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
}

func TestSchema33PreservesLegacyQAReceiptAndKeepsItsTokenScoped(t *testing.T) {
	s, b := setup(t)
	b.login()
	software := installedJenkinsFixture(t, s, b, "http://127.0.0.1:19901")
	p := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("migrate-receipt"), 200))
	deployment := p.Environments[0].Deployments[0].ID
	scope := jenkinsScope{project: p.ID, instance: deployment}
	monitor := "/application-projects/" + p.ID + "/service-instances/" + deployment + "/jenkins-monitor"
	settings := map[string]string{"expectedVersion": "0", "softwareId": software, "username": "qa-operator", "token": jenkinsFixtureToken, "jobUrl": "http://127.0.0.1:19901/job/previous/"}
	callSoftwareLogin(t, b, "POST", monitor, softwareLoginTestPeer, b.encrypt(monitor, settings), 200, "SUCCESS")
	rewindSchema34Fixture(t, s.DB)
	_, err := s.DB.Exec(`DROP TABLE application_jenkins_connections;
	    DROP TRIGGER application_qa_clear_old_requests; DROP TABLE application_qa_requests;
	    CREATE TABLE application_qa_requests(
	        deployment_id TEXT NOT NULL REFERENCES application_qa_monitors(deployment_id) ON DELETE CASCADE,
	        request_id TEXT NOT NULL,monitor_version INTEGER NOT NULL,queue_id INTEGER NOT NULL DEFAULT 0,
	        PRIMARY KEY(deployment_id,request_id));
	    DELETE FROM schema_migrations WHERE version=33; PRAGMA user_version=32;`)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	if _, err = s.DB.Exec("INSERT INTO application_qa_requests VALUES(?,?,1,45)", deployment, requestID); err != nil {
		t.Fatal(err)
	}
	dir := s.Dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var queue, version, connections int
	if err = upgraded.DB.QueryRow("SELECT monitor_version,queue_id FROM application_qa_requests WHERE scope_id=? AND request_id=?", scope.requestScope(), requestID).Scan(&version, &queue); err != nil || version != 1 || queue != 45 {
		t.Fatalf("migration lost QA receipt: %d %d %v", version, queue, err)
	}
	if err = upgraded.DB.QueryRow("SELECT count(*) FROM application_jenkins_connections").Scan(&connections); err != nil || connections != 0 {
		t.Fatal("legacy token gained project scope")
	}
	_, old, _, err := effectiveJenkinsMonitor(upgraded.DB, scope)
	if err != nil || old.automation != nil || old.Version != 1 {
		t.Fatal("legacy QA monitor unavailable", err)
	}
}

func rewindSchema34Fixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
        DROP TRIGGER IF EXISTS project_deployment_guard_delete;
        DROP TRIGGER IF EXISTS project_deployment_guard_binding;
        DROP TRIGGER IF EXISTS project_deployment_guard_target;
        DROP TRIGGER IF EXISTS project_deployment_legacy_lock;
        DROP TABLE IF EXISTS project_deployment_running;
        DROP TABLE IF EXISTS project_deployment_plans;
        DROP TABLE IF EXISTS project_deployment_jobs;
        ALTER TABLE application_repositories DROP COLUMN jenkins_credential_id;
        ALTER TABLE application_jenkins_connections DROP COLUMN build_agent_label;
        DELETE FROM schema_migrations WHERE version>=34;
        PRAGMA user_version=33;`); err != nil {
		t.Fatal(err)
	}
}

func TestSchema34DefaultsExistingJenkinsConnectionsWithoutChangingTheirTokens(t *testing.T) {
	s, b := setup(t)
	b.login()
	software := installedJenkinsFixture(t, s, b, "http://127.0.0.1:19901")
	p := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("label-migration"), 200))
	connPath := "/application-projects/" + p.ID + "/jenkins-connection"
	callSoftwareLogin(t, b, "POST", connPath, softwareLoginTestPeer, b.encrypt(connPath, map[string]string{
		"expectedVersion": "0", "softwareId": software, "username": "qa-operator", "token": jenkinsFixtureToken,
	}), 200, "SUCCESS")
	rewindSchema34Fixture(t, s.DB)
	dir := s.Dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	scope := jenkinsScope{project: p.ID, connection: true}
	_, conn, err := readJenkinsMonitor(upgraded.DB, scope)
	if err != nil || conn.Version != 1 || conn.SoftwareID != software || conn.BuildAgentLabel != defaultJenkinsBuildAgentLabel {
		t.Fatal("existing connection was not preserved with the default build label")
	}
	token, err := conn.openToken(upgraded.Key, scope)
	defer clear(token)
	if err != nil || string(token) != jenkinsFixtureToken {
		t.Fatal("existing connection credential no longer opens")
	}
}

func TestJenkinsCreateItemErrorClassificationWithoutHeader(t *testing.T) {
	store, b := setup(t)
	b.login()
	var mode string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("X-Jenkins", "2.580.2")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/config.xml") {
			w.Header().Set("X-Jenkins", "2.580.2")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/createItem" && r.Method == "POST" {
			switch mode {
			case "plugin":
				// Simulate Jenkins returning 500 without X-Jenkins header when Pipeline plugin is absent.
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, "com.thoughtworks.xstream.mapper.CannotResolveClassException: flow-definition")
			case "ordinary":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, "Unable to save flow-definition: disk full")
			case "auth":
				// Simulate 403 without X-Jenkins header.
				w.WriteHeader(http.StatusForbidden)
				_, _ = fmt.Fprint(w, "No valid crumb was included in the request")
			case "imposter":
				w.WriteHeader(http.StatusOK)
			}
			return
		}
		w.Header().Set("X-Jenkins", "2.580.2")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	software := installedJenkinsFixture(t, store, b, upstream.URL)
	draft := projectDraft28("create-item-errors")
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND"}
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	conn := "/application-projects/" + p.ID + "/jenkins-connection"
	callSoftwareLogin(t, b, "POST", conn, softwareLoginTestPeer, b.encrypt(conn, map[string]string{
		"expectedVersion": "0", "softwareId": software, "username": "qa-operator", "token": jenkinsFixtureToken,
	}), 200, "SUCCESS")
	scope := "/application-projects/" + p.ID + "/modules/" + p.Modules[0].ID
	monitor := dataOf(t, b.call("GET", scope+"/jenkins-monitor", nil, 200))
	version := monitor["version"].(string)

	for _, tc := range []struct {
		mode string
		want string
	}{
		{"plugin", "JENKINS_PLUGIN_REQUIRED"},
		{"ordinary", "JENKINS_RESPONSE_INVALID"},
		{"auth", "JENKINS_AUTH_REQUIRED"},
		{"imposter", "JENKINS_PEER_UNVERIFIED"},
	} {
		mode = tc.mode
		res := b.call("POST", scope+"/jenkins-build", map[string]string{
			"requestId": uuid.NewString(), "expectedVersion": version,
		}, 502)
		if !strings.Contains(res.Body.String(), `"`+tc.want+`"`) {
			t.Fatalf("mode=%s want %q body=%s", tc.mode, tc.want, res.Body.String())
		}
	}
}
