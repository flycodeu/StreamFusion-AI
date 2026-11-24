package controller

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestProjectStaticPreflightOnlyReusesTheRunningArtifact(t *testing.T) {
	s, b := setup(t)
	b.login()

	var targetCalls, applies atomic.Int32
	var targetMu sync.Mutex
	var active string
	target := pluginSSHFixture(t, s, b, false, func(req deploy.Request) ([]byte, bool) {
		targetCalls.Add(1)
		if req.Action == "CAPABILITIES" {
			out, _ := json.Marshal(executorCapabilities{
				Owner:                   deploy.Owner{ControllerID: req.ControllerID, TargetID: req.TargetID},
				ProjectDeployVersion:    deploy.ProjectDeployVersion,
				CommandTrustFingerprint: deploy.CommandPublicKeyFingerprint(s.commandPublicKey()),
			})
			return out, true
		}
		if req.ProjectDeployment == nil {
			return nil, false
		}
		targetMu.Lock()
		defer targetMu.Unlock()
		switch req.Action {
		case deploy.ProjectApply:
			applies.Add(1)
			active = req.ProjectDeployment.ReleaseID
		case deploy.ProjectProbe:
			if req.ProjectDeployment.ExpectedRelease != active {
				t.Error("target probe did not use the running version")
			}
		default:
			t.Errorf("unexpected target action %s", req.Action)
		}
		out, _ := json.Marshal(deploy.ProjectDeployResult{
			Status: "SUCCEEDED", Health: "READY", ReleaseID: active, ObservedAt: Now(),
		})
		return out, true
	})

	const build = 31
	const artifact = "fixture web archive A"
	const replacement = "fixture web archive B" // Same number, commit, name and length; only the digest changes.
	const filename = "web-dist.tar.gz"
	commit := strings.Repeat("7", 40)
	type workflow struct{ path, config string }
	var expected atomic.Pointer[workflow]
	var replaced atomic.Bool
	var manifests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.580.2")
		if r.URL.Path == "/login" {
			return
		}
		want := expected.Load()
		user, token, _ := r.BasicAuth()
		if want == nil || !strings.HasPrefix(r.URL.Path, want.path) ||
			r.Method != "GET" || user != "qa-operator" || token != jenkinsFixtureToken {
			t.Errorf("unexpected Jenkins request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body := artifact
		if replaced.Load() {
			body = replacement
		}
		switch strings.TrimPrefix(r.URL.Path, want.path) {
		case "config.xml":
			fmt.Fprint(w, want.config)
		case "31/api/json":
			fmt.Fprintf(w, `{"number":%d,"building":false,"result":"SUCCESS","artifacts":[{"fileName":%q,"relativePath":%q},{"fileName":"sfops-release.txt","relativePath":"sfops-release.txt"}]}`, build, filename, filename)
		case "31/artifact/sfops-release.txt":
			manifests.Add(1)
			fmt.Fprintf(w, "%s\n%s\n%x\n%d\n", commit, filename, sha256.Sum256([]byte(body)), len(body))
		case "31/artifact/" + filename:
			fmt.Fprint(w, body)
		default:
			t.Errorf("unexpected Jenkins request path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	jenkins := installedJenkinsFixture(t, s, b, upstream.URL)
	draft := projectDraft28("static-reuse-fixture")
	draft.Modules[0].Name = "前端"
	draft.Modules[0].Directory = "frontend"
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "FRONTEND"}
	d := &draft.Environments[0].Deployments[0]
	d.HostID, d.DeployMode = target, "STATIC"
	d.Nginx = &applicationNginxDeploy{SiteName: "reuse", ServerName: "reuse.example.test", ListenPort: 18080, SPA: true}
	project := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	projectPath := "/application-projects/" + project.ID
	base := projectPath + "/deployments/" + d.ID
	conn := projectPath + "/jenkins-connection"
	callSoftwareLogin(t, b, "POST", conn, softwareLoginTestPeer, b.encrypt(conn, map[string]string{
		"expectedVersion": "0", "softwareId": jenkins, "username": "qa-operator", "token": jenkinsFixtureToken,
	}), 200, "SUCCESS")
	monitor, _, jobURL, err := s.jenkinsBuildBinding(t.Context(), jenkinsScope{project: project.ID, module: project.Modules[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	config, err := xml.Marshal(expectedJenkinsWorkflow(monitor.automation))
	if err != nil {
		t.Fatal(err)
	}
	expected.Store(&workflow{path: jobURL.Path, config: string(config)})

	preflight := func(releaseID string, status int) *httptest.ResponseRecorder {
		t.Helper()
		body := map[string]any{"buildNumber": build}
		if releaseID != "" {
			body["currentReleaseId"] = releaseID
		}
		return b.call("POST", base+"/preflight", body, status)
	}
	token := dataOf(t, preflight("", 200))["token"].(string)
	releaseID := dataOf(t, b.call("POST", base+"/submit", map[string]string{"token": token}, 202))["jobId"].(string)
	job, err := scanProjectJob(s.DB.QueryRow("SELECT "+projectJobSummaryFields+" FROM project_deployment_jobs WHERE id=?", releaseID))
	if err != nil {
		t.Fatal(err)
	}
	b.h.processProjectDeployment(t.Context(), job)
	status := dataOf(t, b.call("GET", base, nil, 200))
	current, ok := status["current"].(map[string]any)
	if !ok || current["releaseId"] != releaseID || applies.Load() != 1 {
		t.Fatal("fixture release did not become current", status)
	}
	planCount := func() int {
		t.Helper()
		var count int
		if err := s.DB.QueryRow("SELECT count(*) FROM project_deployment_plans WHERE deployment_id=?", d.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	before := planCount()
	if plan := dataOf(t, preflight(releaseID, 200)); plan["commit"] != commit || plan["token"] == "" || planCount() != before+1 {
		t.Fatal("matching artifact did not yield a reuse plan", plan)
	}
	before = planCount()
	targetBefore, manifestsBefore := targetCalls.Load(), manifests.Load()
	if reply := preflight(uuid.NewString(), 409); !strings.Contains(reply.Body.String(), "PROJECT_PLAN_CHANGED") {
		t.Fatal("stale release was not rejected", reply.Body.String())
	}
	if manifests.Load() != manifestsBefore {
		t.Fatal("stale release reached Jenkins")
	}
	replaced.Store(true)
	if reply := preflight(releaseID, 409); !strings.Contains(reply.Body.String(), "PROJECT_CURRENT_ARTIFACT_CHANGED") {
		t.Fatal("replacement artifact was not rejected", reply.Body.String())
	}
	if manifests.Load() != manifestsBefore+1 || targetCalls.Load() != targetBefore || applies.Load() != 1 || planCount() != before {
		t.Fatal("rejected reuse did not stop before target probes and plan creation")
	}
}
