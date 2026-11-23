package controller

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"golang.org/x/crypto/ssh"
)

const jenkinsFixtureToken = "fixture-jenkins-token-20"
const jenkinsFixtureJob = "/job/Folder%20Name/job/platform-api/"

func installedJenkinsFixture(t *testing.T, s *Store, b *browser, origin string) string {
	t.Helper()
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "Jenkins fixture", "kind": "LOCAL"}, 201))["id"].(string)
	locator, identity := strings.Repeat("a", 64), strings.Repeat("b", 64)
	id := registerTestSoftware(t, b, host, "DOCKER", locator, "JENKINS")["id"].(string)
	definition, err := s.findInstallRecipe("custom-jenkins-docker-online", "2.580.2")
	if err != nil {
		t.Fatal(err)
	}
	config := serviceinstall.Config{Name: "Jenkins", RuntimeMode: "DOCKER", BindAddress: "0.0.0.0", HostPort: port}
	i := serviceinstall.Request{SoftwareID: id, BindingRevision: 1, Definition: definition, Config: config}
	e := serviceinstall.Evidence{Kind: "DOCKER", Locator: locator, SoftwareID: id, BindingRevision: 1, RecipeKey: definition.Key,
		RecipeRevision: definition.Revision, ConfigHash: serviceinstall.Digest(config), ImageID: "sha256:" + strings.Repeat("c", 64), ContainerID: locator,
		IdentityHash: identity, ObservedVersion: definition.Version, Health: "READY", ObservedAt: Now()}
	if _, err = s.DB.Exec(`UPDATE software_instances SET management_mode='MANAGED',binding_revision=1,identity_hash=?,
		installation_status='READY',actual_state='RUNNING',installation_json=?,installation_evidence_json=? WHERE id=?`, identity, jsonText(i), jsonText(e), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func testJenkinsProject(t *testing.T, b *browser, job string) string {
	t.Helper()
	data := dataOf(t, b.call("POST", "/application-projects/catalog", map[string]string{"name": "Jenkins CI fixture", "jenkinsUrl": job}, 200))
	return data["id"].(string)
}

func TestJenkinsBuildMonitorReadsBoundJobWithoutMutation(t *testing.T) {
	s, b := setup(t)
	b.login()
	var authRequests, redirectRequests atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectRequests.Add(1)
		w.WriteHeader(204)
	}))
	t.Cleanup(redirect.Close)
	var upstreamMode atomic.Int32
	jenkins := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("Jenkins request must be read-only")
		}
		w.Header().Set("X-Jenkins", "2.580.2")
		p := r.URL.EscapedPath()
		if p == "/login" {
			if r.Header.Get("Authorization") != "" {
				t.Error("Jenkins peer probe leaked authorization")
			}
			if upstreamMode.Load() == 3 {
				w.Header().Set("X-Jenkins", "other-version")
			}
			return
		}
		if user, token, ok := r.BasicAuth(); !ok || user != "ops-fixture" || token != jenkinsFixtureToken {
			t.Error("Jenkins request has wrong scoped token")
		}
		authRequests.Add(1)
		switch strings.TrimPrefix(p, jenkinsFixtureJob) {
		case "api/json":
			switch upstreamMode.Load() {
			case 1:
				http.Redirect(w, r, redirect.URL+"/credentials", http.StatusFound)
			case 2:
				w.WriteHeader(http.StatusForbidden)
			case 4:
				fmt.Fprint(w, `{"inQueue":true,"queueItem":{"why":"Waiting for a build node"},"lastBuild":null}`)
			default:
				fmt.Fprint(w, `{"inQueue":false,"lastBuild":{"number":17,"url":"https://untrusted.example/build/"}}`)
			}
		case "17/api/json":
			fmt.Fprint(w, `{"number":17,"building":true,"result":null,"timestamp":1763900000000,"duration":58,"artifacts":[{"fileName":"platform-api.jar","relativePath":"platform-api/target/platform-api.jar"},{"fileName":"invalid","relativePath":"../../other"}]}`)
		case "17/wfapi/describe":
			if upstreamMode.Load() == 5 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprint(w, `{"stages":[{"name":"环境预检","status":"SUCCESS","durationMillis":3},{"name":"Maven 打包","status":"IN_PROGRESS","durationMillis":7}]}`)
		case "17/logText/progressiveText":
			w.Header().Set("X-Text-Size", "9824")
			w.Header().Set("X-More-Data", "true")
			if upstreamMode.Load() == 6 {
				fmt.Fprint(w, strings.Repeat("x", jenkinsLogLimit+900))
				return
			}
			fmt.Fprintf(w, "SFOPS_STAGE: 环境预检\nSFOPS_STAGE: Maven 打包\n<pre>secret=%s\n", jenkinsFixtureToken)
		default:
			t.Errorf("unexpected Jenkins endpoint %s", p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(jenkins.Close)
	software := installedJenkinsFixture(t, s, b, jenkins.URL)
	project := testJenkinsProject(t, b, jenkins.URL+jenkinsFixtureJob)
	root := "/application-projects/" + project
	monitor, build := root+"/jenkins-monitor", root+"/jenkins-build"
	get := func(path string) map[string]any {
		t.Helper()
		return dataOf(t, b.call("GET", path, nil, 200))
	}
	post := func(params map[string]string, status int, code string) *httptest.ResponseRecorder {
		t.Helper()
		return callSoftwareLogin(t, b, "POST", monitor, softwareLoginTestPeer, b.encrypt(monitor, params), status, code)
	}
	if initial := get(monitor); initial["configured"] != false || initial["version"] != "0" || len(initial["instances"].([]any)) != 1 {
		t.Fatal("unexpected initial monitor metadata")
	}
	if state := get(build); state["status"] != "NOT_CONFIGURED" || authRequests.Load() != 0 {
		t.Fatal("unbound project queried upstream")
	}
	params := map[string]string{"expectedVersion": "0", "softwareId": software, "username": "ops-fixture", "token": jenkinsFixtureToken}
	callSoftwareLogin(t, b, "POST", monitor, softwareLoginTestPeer, params, 400, "SECURE_REQUEST_INVALID")
	if saved := dataOf(t, post(params, 200, "SUCCESS")); saved["version"] != "1" || saved["hasToken"] != true || saved["configured"] != true {
		t.Fatal("monitor not durably bound")
	}
	post(params, 409, "VERSION_CONFLICT")
	params["expectedVersion"], params["token"] = "1", ""
	post(params, 200, "SUCCESS")
	var encrypted []byte
	if err := s.DB.QueryRow("SELECT token_ciphertext FROM application_jenkins_monitors WHERE project_id=?", project).Scan(&encrypted); err != nil || len(encrypted) == 0 || bytes.Contains(encrypted, []byte(jenkinsFixtureToken)) {
		t.Fatalf("Jenkins token not stored as ciphertext (%v)", err)
	}
	active := get(build)
	if active["number"] != float64(17) || active["status"] != "RUNNING" || active["currentStage"] != "Maven 打包" || active["stagesAvailable"] != true || active["buildUrl"] != jenkins.URL+jenkinsFixtureJob+"17/" {
		t.Fatalf("wrong Jenkins state: %+v", active)
	}
	artifacts := active["artifacts"].([]any)
	if len(artifacts) != 1 || artifacts[0].(map[string]any)["url"] != jenkins.URL+jenkinsFixtureJob+"17/artifact/platform-api/target/platform-api.jar" {
		t.Fatalf("artifact escaped the bound Jenkins job: %+v", artifacts)
	}
	logPath := build + "/log?number=17&offset=23"
	if logs := get(logPath); logs["more"] != true || logs["offset"] != float64(23) || logs["nextOffset"] != float64(9824) || !strings.Contains(logs["text"].(string), "secret=[已屏蔽]") || strings.Contains(logs["text"].(string), jenkinsFixtureToken) {
		t.Fatal("invalid progressive offset or secret redaction")
	}
	b.call("GET", build+"/log?number=../../other&offset=-1", nil, 400)
	upstreamMode.Store(1)
	callSoftwareLogin(t, b, "GET", build, softwareLoginTestPeer, nil, 502, "JENKINS_RESPONSE_INVALID")
	if redirectRequests.Load() != 0 {
		t.Fatal("Jenkins redirect forwarded authorization or probed a different origin")
	}
	upstreamMode.Store(2)
	callSoftwareLogin(t, b, "GET", build, softwareLoginTestPeer, nil, 502, "JENKINS_AUTH_REQUIRED")
	upstreamMode.Store(3)
	before := authRequests.Load()
	callSoftwareLogin(t, b, "GET", build, softwareLoginTestPeer, nil, 502, "JENKINS_PEER_UNVERIFIED")
	if authRequests.Load() != before {
		t.Fatal("authorization sent before Jenkins identity verified")
	}
	upstreamMode.Store(4)
	if queued := get(build); queued["status"] != "QUEUED" || queued["number"] != nil || queued["queueReason"] != "Waiting for a build node" {
		t.Fatal("queue presented as a numbered build")
	}
	upstreamMode.Store(5)
	if fallback := get(build); fallback["currentStage"] != "Maven 打包" || fallback["stagesAvailable"] != true {
		t.Fatal("first-party Jenkins stages unavailable without optional plugin")
	}
	upstreamMode.Store(6)
	if bounded := get(logPath); bounded["truncated"] != true || bounded["more"] != false || len(bounded["text"].(string)) != jenkinsLogLimit {
		t.Fatal("Jenkins log limit not enforced")
	}
	before = authRequests.Load()
	b.call("PATCH", root+"/catalog", map[string]string{"version": "1", "jenkinsUrl": redirect.URL + "/job/other/"}, 200)
	callSoftwareLogin(t, b, "GET", build, softwareLoginTestPeer, nil, 409, "JENKINS_BINDING_CHANGED")
	params["expectedVersion"], params["token"] = "2", jenkinsFixtureToken
	post(params, 400, "JENKINS_JOB_URL_INVALID")
	if redirectRequests.Load() != 0 || authRequests.Load() != before {
		t.Fatal("changed project URL forwarded the bound token")
	}
	for _, p := range []string{monitor, "/credentials", "/audit"} {
		if strings.Contains(b.call("GET", p, nil, 200).Body.String(), jenkinsFixtureToken) {
			t.Fatal("ordinary endpoint exposed Jenkins token")
		}
	}
	var jobs int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("Jenkins monitor created an Ops job: %d/%v", jobs, err)
	}
	b.call("DELETE", root, map[string]string{"version": "2"}, 200)
	var retained int
	if err := s.DB.QueryRow("SELECT count(*) FROM application_jenkins_monitors").Scan(&retained); err != nil || retained != 0 {
		t.Fatal("deleting project left a Jenkins token binding")
	}
}

func TestJenkinsMonitorV29MigrationPreservesProjectsAndProtectsKey(t *testing.T) {
	s, b := setup(t)
	b.login()
	const origin = "http://127.0.0.1:18080"
	pid := testJenkinsProject(t, b, origin+jenkinsFixtureJob)
	software := installedJenkinsFixture(t, s, b, origin)
	dir := s.Dir
	if _, err := s.DB.Exec("DROP TRIGGER application_qa_scope_changed; DROP TABLE application_qa_requests; DROP TABLE application_qa_monitors; DROP TABLE application_jenkins_monitors; DROP TABLE software_login_accounts; DELETE FROM schema_migrations WHERE version>=30;" + schemaV29); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })
	b.h.store = upgraded
	var version int
	if err = upgraded.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("database did not reach schema %d: %d/%v", SchemaVersion, version, err)
	}
	if p, err := readApplicationProject(upgraded.DB, pid); err != nil || p.JenkinsURL != origin+jenkinsFixtureJob || p.Version != "1" {
		t.Fatalf("v30 migration changed project: %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "before-schema-30-*.tar.gz")); len(files) != 1 {
		t.Fatal("v30 migration backup missing")
	}
	monitor := "/application-projects/" + pid + "/jenkins-monitor"
	params := map[string]string{"expectedVersion": "0", "softwareId": software, "username": "ops-fixture", "token": jenkinsFixtureToken}
	callSoftwareLogin(t, b, "POST", monitor, softwareLoginTestPeer, b.encrypt(monitor, params), 200, "SUCCESS")
	if err = upgraded.Close(); err != nil {
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
	if reopened, err = Open(dir); err == nil {
		reopened.Close()
		t.Fatal("v30 opened encrypted Jenkins bindings without original key")
	} else if !strings.HasPrefix(err.Error(), "KEY_MISSING:") {
		t.Fatal(err)
	}
}

func TestJenkinsBuildUsesPinnedSSHTunnel(t *testing.T) {
	s, b := setup(t)
	b.login()
	jenkins := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.580.2")
		if r.URL.Path == "/login" {
			if r.Header.Get("Authorization") != "" {
				t.Error("unauthenticated probe sent authorization")
			}
			return
		}
		if user, secret, ok := r.BasicAuth(); !ok || user != "ops-fixture" || secret != jenkinsFixtureToken {
			t.Error("tunneled query is missing scoped credentials")
		}
		fmt.Fprint(w, `{"inQueue":false,"lastBuild":null}`)
	}))
	t.Cleanup(jenkins.Close)
	u, err := url.Parse(jenkins.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "Jenkins SSH fixture", "kind": "PASSWORD", "secret": "fixture-ssh-pass"}, 201))
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, secret []byte) (*ssh.Permissions, error) {
		if meta.User() != "ci-fixture" || string(secret) != "fixture-ssh-pass" {
			return nil, fmt.Errorf("unexpected SSH credentials")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var destinations atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		connection, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			return
		}
		defer connection.Close()
		go ssh.DiscardRequests(requests)
		for pending := range channels {
			var payload struct {
				Host       string
				Port       uint32
				Origin     string
				OriginPort uint32
			}
			if pending.ChannelType() != "direct-tcpip" || ssh.Unmarshal(pending.ExtraData(), &payload) != nil || payload.Host != "127.0.0.1" || payload.Port != uint32(port) {
				pending.Reject(ssh.Prohibited, "unexpected forwarded address")
				continue
			}
			socket, err := net.Dial("tcp", u.Host)
			if err != nil {
				pending.Reject(ssh.ConnectionFailed, "fixture unavailable")
				continue
			}
			forward, requests, err := pending.Accept()
			if err != nil {
				socket.Close()
				continue
			}
			destinations.Add(1)
			go ssh.DiscardRequests(requests)
			go func() { defer socket.Close(); _, _ = io.Copy(socket, forward) }()
			go func() { defer forward.Close(); _, _ = io.Copy(forward, socket) }()
		}
	}()
	software := installedJenkinsFixture(t, s, b, jenkins.URL)
	instance, err := s.readSoftware(software)
	if err != nil {
		t.Fatal(err)
	}
	sshHost := target{sshTarget: sshTarget{Kind: "SSH", Address: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		User: "ci-fixture", Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), CredentialID: credential["id"].(string), Revision: credential["revision"].(string)}}
	endpoint, err := buildJenkinsEndpoint(instance, sshHost)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jenkinsJobURL(endpoint, jenkins.URL+jenkinsFixtureJob)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, stop, err := s.connectJenkinsBuild(ctx, endpoint, *job, "ops-fixture", []byte(jenkinsFixtureToken))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if result, err := client.status(ctx); err != nil || result.Status != "NEVER_BUILT" || destinations.Load() == 0 {
		t.Fatalf("Jenkins job did not use pinned SSH forwarding: %s, %v", result.Status, err)
	}
	stop()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("SSH tunnel was not released")
	}
}
