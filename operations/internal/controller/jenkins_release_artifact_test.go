package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetchReleaseArtifactUsesOnlyTheVerifiedModuleBuild(t *testing.T) {
	store, browser := setup(t)
	browser.login()
	handler := browser.h
	const artifact = "application-data"
	commit := strings.Repeat("a", 40)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(artifact)))
	var workflow, path string
	var mode, downloads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.580.2")
		if r.Method != "GET" {
			t.Errorf("unexpected Jenkins mutation %s", r.Method)
		}
		p := r.URL.Path
		if p == "/login" {
			if r.Header.Get("Authorization") != "" {
				t.Error("public peer probe used credentials")
			}
			return
		}
		user, token, _ := r.BasicAuth()
		if user != "qa-operator" || token != jenkinsFixtureToken {
			t.Error("missing scoped Jenkins credential")
		}
		switch p {
		case path + "config.xml":
			w.Header().Del("X-Jenkins")
			if mode.Load() == 1 {
				fmt.Fprint(w, "<flow-definition/>")
			} else {
				fmt.Fprint(w, `<?xml version="1.1" encoding="UTF-8"?>`+workflow)
			}
		case path + "17/api/json":
			number, outcome := 17, "SUCCESS"
			output := "app.jar"
			switch mode.Load() {
			case 2:
				number = 42
			case 3:
				outcome = "FAILURE"
			case 4:
				output = "../app.jar"
			}
			fmt.Fprintf(w, `{"number":%d,"building":false,"result":"%s","artifacts":[{"fileName":"app.jar","relativePath":"%s"},{"fileName":"sfops-release.txt","relativePath":"sfops-release.txt"}]}`, number, outcome, output)
		case path + "17/artifact/sfops-release.txt":
			w.Header().Del("X-Jenkins")
			size := len(artifact)
			if mode.Load() == 5 {
				size = 513 << 20
			}
			fmt.Fprintf(w, "%s\napp.jar\n%s\n%d\n", commit, digest, size)
		case path + "17/artifact/app.jar":
			downloads.Add(1)
			switch mode.Load() {
			case 6:
				w.Header().Set("Location", "https://example.invalid/artifact")
				w.WriteHeader(http.StatusFound)
			case 7:
				fmt.Fprint(w, strings.Repeat("x", len(artifact)))
			case 8:
				w.(http.Flusher).Flush()
				fmt.Fprint(w, artifact+"extra")
			case 9:
				w.Header().Set("X-Jenkins", "another-version")
				fmt.Fprint(w, artifact)
			default:
				w.Header().Del("X-Jenkins")
				fmt.Fprint(w, artifact)
			}
		default:
			t.Errorf("unexpected artifact route %s", p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	software := installedJenkinsFixture(t, store, browser, upstream.URL)
	draft := projectDraft28("jenkins-release")
	draft.Modules[0].Catalog = &applicationModuleCatalog{Kind: "BACKEND"}
	project := applicationResponse(t, browser.call("POST", "/application-projects", draft, 200))
	module := project.Modules[0]
	connection := "/application-projects/" + project.ID + "/jenkins-connection"
	settings := map[string]string{"expectedVersion": "0", "softwareId": software, "username": "qa-operator", "token": jenkinsFixtureToken}
	callSoftwareLogin(t, browser, "POST", connection, softwareLoginTestPeer, browser.encrypt(connection, settings), 200, "SUCCESS")
	monitor, _, job, err := store.jenkinsBuildBinding(t.Context(), jenkinsScope{project: project.ID, module: module.ID})
	if err != nil || monitor.automation == nil {
		t.Fatal("could not resolve managed Jenkins job:", err)
	}
	config, err := xml.Marshal(expectedJenkinsWorkflow(monitor.automation))
	if err != nil {
		t.Fatal(err)
	}
	path, workflow = job.Path, string(config)
	expected := projectBuildArtifact{Commit: commit, Filename: "app.jar", SHA256: digest, Size: int64(len(artifact))}
	got, err := handler.fetchProjectReleaseArtifact(t.Context(), project, module, 17, nil)
	if err != nil || got != expected || downloads.Load() != 0 {
		t.Fatalf("metadata preview %v, %v, downloads %d", got, err, downloads.Load())
	}
	var body bytes.Buffer
	got, err = handler.fetchProjectReleaseArtifact(t.Context(), project, module, 17, &body)
	if err != nil || got != expected || body.String() != artifact {
		t.Fatalf("download %v, %v, %q", got, err, body.String())
	}
	for _, tc := range []struct {
		mode int32
		err  error
	}{
		{1, errJenkinsJobConflict},
		{2, errJenkinsResponse},
		{3, errJenkinsReleaseNotReady},
		{4, errJenkinsReleaseArtifact},
		{5, errJenkinsReleaseTooLarge},
		{6, errJenkinsResponse},
		{7, errJenkinsReleaseMismatch},
		{8, errJenkinsReleaseMismatch},
		{9, errJenkinsPeer},
	} {
		mode.Store(tc.mode)
		body.Reset()
		if _, err := handler.fetchProjectReleaseArtifact(t.Context(), project, module, 17, &body); !errors.Is(err, tc.err) {
			t.Errorf("mode %d: error %v, want %v", tc.mode, err, tc.err)
		}
		if body.Len() > len(artifact) {
			t.Errorf("mode %d: wrote beyond declared artifact length", tc.mode)
		}
	}
}
