package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestJenkinsConnectionTestEndpoint(t *testing.T) {
	store, b := setup(t)
	b.login()

	if !sensitiveRequestPath("/application-projects/" + uuid.NewString() + "/jenkins-connection/test") {
		t.Fatal("expected jenkins-connection/test path to require sensitive envelope encryption")
	}

	allowPlugins := true
	allowAgents := true
	dropHeader := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !dropHeader {
			w.Header().Set("X-Jenkins", "2.580.2")
		}
		if r.URL.Path == "/login" {
			return
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "qa-operator" || token != jenkinsFixtureToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/me/api/json":
			fmt.Fprint(w, `{"id":"qa-operator","fullName":"QA Operator"}`)
		case "/pluginManager/api/json":
			if !allowPlugins {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `{"plugins":[{"shortName":"git","active":true,"version":"5.7.0"},{"shortName":"workflow-aggregator","active":true,"version":"600.vb"}]}`)
		case "/computer/api/json":
			if !allowAgents {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if !strings.Contains(r.URL.Query().Get("tree"), "assignedLabels[name]") {
				t.Error("agent query did not select the assigned labels")
			}
			fmt.Fprint(w, `{"computer":[
				{"offline":false,"numExecutors":2,"assignedLabels":[{"name":"built-in"}]},
				{"offline":false,"numExecutors":2,"assignedLabels":[{"name":"sfops-build"}]},
				{"offline":true,"numExecutors":2,"assignedLabels":[{"name":"sfops-build"}]},
				{"offline":false,"numExecutors":0,"assignedLabels":[{"name":"sfops-build"}]},
				{"offline":true,"numExecutors":2,"assignedLabels":[{"name":"build.linux-21"}]}
			]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	software := installedJenkinsFixture(t, store, b, upstream.URL)
	p := applicationResponse(t, b.call("POST", "/application-projects", projectDraft28("conn-test"), 200))
	testPath := "/application-projects/" + p.ID + "/jenkins-connection/test"

	// 1. Valid credentials + plugin list
	res := callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, map[string]string{
		"softwareId": software,
		"username":   "qa-operator",
		"token":      jenkinsFixtureToken,
	}), 200, "SUCCESS")
	got := dataOf(t, res)
	if got["ok"] != true || got["jenkinsVersion"] != "2.580.2" || got["pluginsChecked"] != true ||
		got["buildAgentLabel"] != defaultJenkinsBuildAgentLabel {
		t.Fatalf("unexpected test connection response: %#v", got)
	}
	if strings.Contains(fmt.Sprint(got), jenkinsFixtureToken) {
		t.Fatal("connection test echoed token")
	}
	userObj := got["authenticatedUser"].(map[string]any)
	if userObj["id"] != "qa-operator" || userObj["fullName"] != "QA Operator" {
		t.Fatalf("unexpected authenticated user: %#v", userObj)
	}
	plugins := got["recommendedPlugins"].([]any)
	if len(plugins) != 5 {
		t.Fatalf("expected 5 recommended plugins checked, got %d", len(plugins))
	}
	assertAgentCheck := func(result map[string]any, checked bool, matching, online float64) {
		t.Helper()
		agent, ok := result["buildAgents"].(map[string]any)
		if !ok || agent["checked"] != checked || agent["matching"] != matching || agent["online"] != online {
			t.Fatalf("unexpected build agent check: %#v", result["buildAgents"])
		}
	}
	assertAgentCheck(got, true, 3, 1)

	// 2. Failure to read plugins or agents does not invalidate the credentials.
	allowPlugins = false
	allowAgents = false
	res = callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, map[string]string{
		"softwareId": software,
		"username":   "qa-operator",
		"token":      jenkinsFixtureToken,
	}), 200, "SUCCESS")
	if got = dataOf(t, res); got["ok"] != true || got["pluginsChecked"] != false {
		t.Fatalf("expected graceful fallback when pluginManager returns 403: %#v", got)
	}
	assertAgentCheck(got, false, 0, 0)
	allowAgents = true

	// 3. Invalid credentials fail with JENKINS_AUTH_REQUIRED
	callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, map[string]string{
		"softwareId": software,
		"username":   "qa-operator",
		"token":      "wrong-token-1234567890",
	}), 502, "JENKINS_AUTH_REQUIRED")

	// 4. Empty token without saved connection fails with JENKINS_TOKEN_REQUIRED
	callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, map[string]string{
		"softwareId": software,
		"username":   "qa-operator",
		"token":      "",
	}), 400, "JENKINS_TOKEN_REQUIRED")

	// 5. Saving connection then testing with empty token reuses saved secret
	savePath := "/application-projects/" + p.ID + "/jenkins-connection"
	saved := callSoftwareLogin(t, b, "POST", savePath, softwareLoginTestPeer, b.encrypt(savePath, map[string]string{
		"expectedVersion": "0",
		"softwareId":      software,
		"username":        "qa-operator",
		"token":           jenkinsFixtureToken,
		"buildAgentLabel": "build.linux-21",
	}), 200, "SUCCESS")
	if got = dataOf(t, saved); got["buildAgentLabel"] != "build.linux-21" {
		t.Fatal("save did not return the configured build label")
	}
	testInput := map[string]string{
		"softwareId": software,
		"username":   "qa-operator",
		"token":      "",
	}
	res = callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, testInput), 200, "SUCCESS")
	if got = dataOf(t, res); got["buildAgentLabel"] != "build.linux-21" {
		t.Fatal("test did not reuse the configured build label")
	}
	assertAgentCheck(got, true, 1, 0)
	testInput["buildAgentLabel"] = "  ci_gpu-2  "
	res = callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, testInput), 200, "SUCCESS")
	if got = dataOf(t, res); got["buildAgentLabel"] != "ci_gpu-2" {
		t.Fatal("test did not return the proposed build label")
	}
	assertAgentCheck(got, true, 0, 0)
	if got = dataOf(t, b.call("GET", savePath, nil, 200)); got["buildAgentLabel"] != "build.linux-21" {
		t.Fatal("testing the draft changed the saved build label")
	}
	testInput["buildAgentLabel"] = "linux || built-in"
	callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, testInput), 400, "INVALID_INPUT")

	// 6. Missing X-Jenkins header fails peer verification
	dropHeader = true
	callSoftwareLogin(t, b, "POST", testPath, softwareLoginTestPeer, b.encrypt(testPath, map[string]string{
		"softwareId": software,
		"username":   "qa-operator",
		"token":      jenkinsFixtureToken,
	}), 502, "JENKINS_PEER_UNVERIFIED")
}
