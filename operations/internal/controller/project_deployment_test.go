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
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestProjectDeploymentReleaseAndReconcile(t *testing.T) {
	s, b := setup(t)
	b.login()
	var mu sync.Mutex
	var active, behavior string
	var applies int
	targetID := pluginSSHFixture(t, s, b, false, func(req deploy.Request) ([]byte, bool) {
		if req.Action == "CAPABILITIES" {
			out, _ := json.Marshal(executorCapabilities{
				Owner:                   deploy.Owner{ControllerID: req.ControllerID, TargetID: req.TargetID},
				ProjectDeployVersion:    deploy.ProjectDeployVersion,
				CommandTrustFingerprint: deploy.CommandPublicKeyFingerprint(s.commandPublicKey()),
			})
			return out, true
		}
		spec := req.ProjectDeployment
		if spec == nil {
			return nil, false
		}
		mu.Lock()
		defer mu.Unlock()
		var out = deploy.ProjectDeployResult{Status: "SUCCEEDED", Phase: "VERIFIED", Health: "READY", ObservedAt: Now()}
		if req.CommandSignature == "" || req.TargetID == "" || req.JobID == "" || req.TraceID == "" {
			t.Error("project deployment request lacks signed trace identity")
		}
		switch req.Action {
		case deploy.ProjectProbe:
			if len(req.Secrets) != 0 || len(spec.Files) != 0 {
				t.Error("preflight sent files or runtime secrets")
			}
		case deploy.ProjectApply:
			applies++
			if spec.ExpectedRelease != active || spec.Port != 18080 ||
				len(spec.Files) != 1 || !strings.Contains(spec.Files[0].Content, "${SFOPS_CRED:") ||
				strings.Contains(spec.Files[0].Content, "project-fixture-password") ||
				req.Secrets["DB_PASSWORD"] != "project-fixture-password" || len(req.Secrets) != 2 {
				t.Errorf("incorrect apply contract: port=%d files=%d credentialCount=%d", spec.Port, len(spec.Files), len(req.Secrets))
			}
			if behavior == "failure" {
				out.Status, out.Phase, out.ErrorCode, out.Health = "FAILED", "TARGET_EXECUTION", "PROJECT_HEALTH_CHECK_FAILED", ""
				out.Output = strings.Repeat("旧日志", 4000) + "\nJava fixture could not start\nAuthorization: Bearer fake-runtime-token"
			} else {
				active = spec.ReleaseID
				if behavior == "lost" {
					out.Status, out.ErrorCode = "UNKNOWN", "PROJECT_RECOVERY_REQUIRED"
				}
			}
		case deploy.ProjectRollback:
			if spec.ExpectedRelease != active || len(req.Secrets) != 0 {
				t.Error("rollback did not use the expected baseline or sent credentials")
			}
			active = spec.ReleaseID
		case deploy.ProjectLogs:
			out.Output = "bounded runtime service log"
		case deploy.ProjectObserve:
			if active == "" {
				out.Health = "NOT_DEPLOYED"
			}
		default:
			t.Errorf("unexpected target action %s", req.Action)
		}
		out.ReleaseID = active
		raw, _ := json.Marshal(out)
		return raw, true
	})
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{
		"name": "Project DB", "kind": "PASSWORD", "secret": "project-fixture-password",
	}, 201))
	var jenkinsPath, config string
	const artifact = "fixture-runnable-jar"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(artifact)))
	commit := strings.Repeat("7", 40)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.580.2")
		target := strings.TrimPrefix(r.URL.Path, jenkinsPath)
		if target == "/login" {
			return
		}
		if user, token, _ := r.BasicAuth(); user != "qa-operator" || token != jenkinsFixtureToken || r.Method != "GET" {
			t.Error("artifact download did not use read-only authenticated Jenkins request")
		}
		switch {
		case target == "config.xml":
			fmt.Fprint(w, config)
		case strings.HasSuffix(target, "/api/json"):
			fmt.Fprintf(w, `{"number":%s,"building":false,"result":"SUCCESS","artifacts":[{"fileName":"app.jar","relativePath":"app.jar"},{"fileName":"sfops-release.txt","relativePath":"sfops-release.txt"}]}`, strings.Split(target, "/")[0])
		case strings.HasSuffix(target, "/artifact/sfops-release.txt"):
			fmt.Fprintf(w, "%s\napp.jar\n%s\n%d\n", commit, digest, len(artifact))
		case strings.HasSuffix(target, "/artifact/app.jar"):
			fmt.Fprint(w, artifact)
		default:
			t.Errorf("unexpected Jenkins route %q", target)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	software := installedJenkinsFixture(t, s, b, upstream.URL)
	draft := projectDraft28("deployment-fixture")
	draft.Repositories[0].JenkinsCredentialID = "repo-read"
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND", HealthPath: "/health"}
	d := &draft.Environments[0].Deployments[0]
	d.HostID, d.DeployMode, d.ServicePort = targetID, "NATIVE", 18080
	d.ConfigDrafts = []deploy.ProjectConfigFile{{Filename: "application.yml", Content: "db:\n  password: ${SFOPS_CRED:Project DB}\n"}}
	d.Variables = []applicationVariable{{Name: "DB_PASSWORD", CredentialID: credential["id"].(string), CredentialRevision: credential["revision"].(string)}}
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	projectPath := "/application-projects/" + p.ID
	base := projectPath + "/deployments/" + d.ID
	conn := projectPath + "/jenkins-connection"
	callSoftwareLogin(t, b, "POST", conn, softwareLoginTestPeer, b.encrypt(conn, map[string]string{
		"expectedVersion": "0", "softwareId": software, "username": "qa-operator", "token": jenkinsFixtureToken,
	}), 200, "SUCCESS")
	monitor, _, jobURL, err := s.jenkinsBuildBinding(t.Context(), jenkinsScope{project: p.ID, module: p.Modules[0].ID})
	if err != nil || !strings.Contains(monitor.automation.script, "credentialsId: 'repo-read'") {
		t.Fatal("Jenkins credential ID not passed to generated workflow", err)
	}
	raw, err := xml.Marshal(expectedJenkinsWorkflow(monitor.automation))
	if err != nil {
		t.Fatal(err)
	}
	jenkinsPath, config = jobURL.Path, string(raw)
	plan := func(number int) string {
		return dataOf(t, b.call("POST", base+"/preflight", map[string]int{"buildNumber": number}, 200))["token"].(string)
	}
	submit := func(token string) string {
		return dataOf(t, b.call("POST", base+"/submit", map[string]string{"token": token}, 202))["jobId"].(string)
	}
	execute := func(id string) map[string]any {
		t.Helper()
		job, err := scanProjectJob(s.DB.QueryRow("SELECT "+projectJobSummaryFields+" FROM project_deployment_jobs WHERE id=?", id))
		if err != nil {
			t.Fatal(err)
		}
		b.h.processProjectDeployment(t.Context(), job)
		return dataOf(t, b.call("GET", base, nil, 200))
	}
	stale := plan(10)
	if _, err := s.DB.Exec("UPDATE targets SET version=version+1 WHERE id=?", targetID); err != nil {
		t.Fatal(err)
	}
	if res := b.call("POST", base+"/submit", map[string]string{"token": stale}, 409); !strings.Contains(res.Body.String(), "PROJECT_PLAN_CHANGED") {
		t.Fatal(res.Body.String())
	}
	mu.Lock()
	behavior = "failure"
	mu.Unlock()
	firstFailure := submit(plan(9))
	status := execute(firstFailure)
	if status["current"] != nil || status["latest"].(map[string]any)["status"] != "FAILED" {
		t.Fatal("an unsuccessful first deployment was reported as active", status)
	}
	b.call("GET", base+"/logs", nil, 409)
	result := dataOf(t, b.call("GET", "/jobs/"+firstFailure+"/logs", nil, 200))
	log := result["text"].(string)
	if !strings.Contains(log, "PROJECT_HEALTH_CHECK_FAILED") || !strings.Contains(log, "Java fixture could not start") ||
		!strings.Contains(log, "较早的日志已截断") || strings.Contains(log, "fake-runtime-token") || len(log) > 8192 {
		t.Fatal("first deployment did not preserve a bounded, redacted startup log", log)
	}
	mu.Lock()
	behavior = ""
	mu.Unlock()
	firstToken := plan(10)
	first := submit(firstToken)
	if again := submit(firstToken); again != first {
		t.Fatal("same confirmation queued a second deployment")
	}
	b.call("DELETE", projectPath+"/service-instances/"+d.ID, map[string]string{"version": p.Version}, 409)
	status = execute(first)
	if status["current"].(map[string]any)["releaseId"] != first || status["health"] != "READY" {
		t.Fatal("first release not visible", status)
	}
	mu.Lock()
	behavior = "failure"
	mu.Unlock()
	failed := submit(plan(11))
	status = execute(failed)
	if status["current"].(map[string]any)["releaseId"] != first || status["latest"].(map[string]any)["status"] != "FAILED" {
		t.Fatal("a failed candidate replaced the running release", status)
	}
	mu.Lock()
	behavior = "lost"
	mu.Unlock()
	second := submit(plan(12))
	status = execute(second)
	if status["latest"].(map[string]any)["status"] != "UNKNOWN" || status["current"].(map[string]any)["releaseId"] != first {
		t.Fatal("unconfirmed change promoted", status)
	}
	v := dataOf(t, b.call("POST", base+"/verify", map[string]any{}, 202))
	if v["jobId"] != second {
		t.Fatal("verify replaced instead of reconciling the existing job", v)
	}
	status = execute(second)
	if status["current"].(map[string]any)["releaseId"] != second || status["previous"].(map[string]any)["releaseId"] != first {
		t.Fatal("recovery not recorded", status)
	}
	b.call("POST", base+"/rollback", map[string]string{"previousReleaseId": uuid.NewString()}, 409)
	r := dataOf(t, b.call("POST", base+"/rollback", map[string]string{"previousReleaseId": first}, 202))
	status = execute(r["jobId"].(string))
	if status["current"].(map[string]any)["releaseId"] != first || status["previous"].(map[string]any)["releaseId"] != second {
		t.Fatal("rollback did not restore the confirmed version", status)
	}
	if logs := b.call("GET", "/jobs/"+first+"/logs", nil, 200).Body.String(); !strings.Contains(logs, "ARTIFACT_DOWNLOAD") || !strings.Contains(logs, "VERIFIED") || strings.Contains(logs, "project-fixture-password") {
		t.Fatal("execution log missing safe stages")
	}
	if logs := b.call("GET", base+"/logs", nil, 200).Body.String(); !strings.Contains(logs, "bounded runtime service log") {
		t.Fatal("runtime logs not available")
	}
	history := dataOf(t, b.call("GET", "/jobs?kind=PROJECT_DEPLOY", nil, 200))
	if len(history["items"].([]any)) != 4 || applies != 4 {
		t.Fatal("deployment history or apply count mismatch", history, applies)
	}
}
