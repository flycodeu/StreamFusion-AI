package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestInstanceQAIsolatesJobsAndTracksItsOwnRun(t *testing.T) {
	s, b := setup(t)
	b.login()
	const path = "/job/api-test/"
	var posts atomic.Int32
	var expired, unknown atomic.Bool
	var versionlessResponse atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.580.2")
		if r.URL.Path == "/login" {
			if r.Header.Get("Authorization") != "" {
				t.Error("initial peer validation used credentials")
			}
			return
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "ops-fixture" || p != jenkinsFixtureToken {
			t.Error("missing dedicated credential")
		}
		switch r.URL.Path {
		case path + "build":
			if r.Method != "POST" {
				t.Error("wrong enqueue verb")
			}
			posts.Add(1)
			if unknown.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			location := "/queue/item/51/"
			if versionlessResponse.Load() > 0 {
				w.Header().Del("X-Jenkins")
			}
			if versionlessResponse.Load() == 1 {
				location = "https://other.invalid" + location
			}
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusCreated)
		case "/queue/item/51/api/json":
			if expired.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if versionlessResponse.Load() == 2 {
				fmt.Fprint(w, `{"id":51,"task":{"url":"/job/another-job/"}}`)
				return
			}
			fmt.Fprint(w, `{"id":51,"task":{"url":"/job/api-test/"},"executable":{"number":12}}`)
		case path + "api/json":
			fmt.Fprint(w, `{"lastBuild":{"number":22},"builds":[{"number":22,"queueId":73},{"number":12,"queueId":51}]}`)
		case path + "12/api/json":
			fmt.Fprint(w, `{"number":12,"result":"SUCCESS","artifacts":[{"fileName":"platform-api.jar","relativePath":"target/platform-api.jar"}]}`)
		case path + "12/wfapi/describe":
			fmt.Fprint(w, `{"stages":[{"name":"Compile","status":"SUCCESS"}]}`)
		case path + "12/logText/progressiveText":
			fmt.Fprint(w, "module-build-12")
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	software := installedJenkinsFixture(t, s, b, upstream.URL)
	p := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("qa-scoped"), 200))
	other := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("qa-other"), 200))
	scope := "/application-projects/" + p.ID + "/service-instances/" + p.Environments[0].Deployments[0].ID
	otherScope := "/application-projects/" + other.ID + "/service-instances/" + other.Environments[0].Deployments[0].ID
	b.call("GET", "/application-projects/"+other.ID+"/service-instances/"+p.Environments[0].Deployments[0].ID+"/jenkins-monitor", nil, 404)
	config := map[string]string{"expectedVersion": "0", "softwareId": software, "username": "ops-fixture", "token": jenkinsFixtureToken, "jobUrl": upstream.URL + path}
	save := func(root string, status int, code string) {
		endpoint := root + "/jenkins-monitor"
		callSoftwareLogin(t, b, "POST", endpoint, softwareLoginTestPeer, b.encrypt(endpoint, config), status, code)
	}
	save(scope, 200, "SUCCESS")
	save(otherScope, 409, "JENKINS_JOB_IN_USE")
	req := map[string]string{"requestId": uuid.NewString(), "expectedVersion": "1"}
	run := scope + "/jenkins-build"
	b.call("POST", otherScope+"/jenkins-build", req, 409)
	for i := 0; i < 2; i++ {
		queued := dataOf(t, b.call("POST", run, req, 200))
		if queued["queueId"] != float64(51) || queued["status"] != "QUEUED" {
			t.Fatal(queued)
		}
	}
	b.call("GET", run+"?queueId=73", nil, 404)
	for _, expiredValue := range []bool{false, true} {
		expired.Store(expiredValue)
		for _, query := range []string{"?queueId=51", "?number=12"} {
			got := dataOf(t, b.call("GET", run+query, nil, 200))
			if got["number"] != float64(12) || got["status"] != "SUCCESS" || len(got["artifacts"].([]any)) != 1 {
				t.Fatal(got)
			}
		}
	}
	if got := dataOf(t, b.call("GET", run+"/log?number=12", nil, 200)); got["text"] != "module-build-12" {
		t.Fatal(got)
	}
	req["requestId"] = uuid.NewString()
	unknown.Store(true)
	b.call("POST", run, req, 409)
	b.call("POST", run, req, 409)
	if posts.Load() != 2 {
		t.Fatalf("idempotent request or uncertain delivery was replayed: %d", posts.Load())
	}
	unknown.Store(false)
	expired.Store(false)
	for _, mode := range []int32{1, 2} {
		versionlessResponse.Store(mode)
		req["requestId"] = uuid.NewString()
		callSoftwareLogin(t, b, "POST", run, softwareLoginTestPeer, req, 409, "JENKINS_TRIGGER_UNCONFIRMED")
		callSoftwareLogin(t, b, "POST", run, softwareLoginTestPeer, req, 409, "JENKINS_TRIGGER_UNCONFIRMED")
	}
	if posts.Load() != 4 {
		t.Fatalf("non-matching queue receipt was retriggered: %d", posts.Load())
	}
	// Changing the deployment host invalidates the former execution binding.
	var host string
	if err := s.DB.QueryRow("SELECT host_id FROM software_instances WHERE id=?", software).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE application_deployments SET host_id=? WHERE id=?", host, p.Environments[0].Deployments[0].ID); err != nil {
		t.Fatal(err)
	}
	got := dataOf(t, b.call("GET", scope+"/jenkins-monitor", nil, 200))
	if got["configured"] != false || strings.Contains(fmt.Sprint(got), jenkinsFixtureToken) {
		t.Fatal("stale execution credential")
	}
	// Project-level credentials can be empty while a scoped credential remains.
	// Losing the original controller key must not silently replace it.
	save(scope, 200, "SUCCESS")
	dir := s.Dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(dir, "master.key"), filepath.Join(dir, "original.key")); err != nil {
		t.Fatal(err)
	}
	if unkeyed, err := Open(dir); err == nil {
		unkeyed.Close()
		t.Fatal("scoped QA bindings accepted a replacement key")
	} else if !strings.HasPrefix(err.Error(), "KEY_MISSING:") {
		t.Fatal(err)
	}
}
