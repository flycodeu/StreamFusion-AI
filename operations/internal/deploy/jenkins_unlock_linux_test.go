package deploy

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"golang.org/x/sys/unix"
)

const jenkinsFixturePassword = "decade0123456789abcdef0123456789"

func jenkinsProtectedTestRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("test needs root privileges for the protected target fixture")
	}
	root, err := os.MkdirTemp("/var/lib", "ops-unlock-test-")
	if err != nil {
		t.Skip("target fixture directory is unavailable")
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func jenkinsBootstrapFixture(t *testing.T, root, id string) string {
	t.Helper()
	for _, dir := range []string{"config", "data/secrets"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(filepath.Join(root, "data"), 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "ops-owner"), []byte("jenkins:"+id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "data", "secrets", "initialAdminPassword")
	if err := os.WriteFile(file, []byte(jenkinsFixturePassword+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(file, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestJenkinsUnlockReadingRejectsUnsafeSecretFiles(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, scenario := range []string{"regular", "symlink", "symlink parent", "hardlink", "fifo", "other owner", "oversize", "wrong instance"} {
		t.Run(scenario, func(t *testing.T) {
			root := jenkinsProtectedTestRoot(t)
			file := jenkinsBootstrapFixture(t, root, id)
			must := func(err error) {
				if err != nil {
					t.Fatal(err)
				}
			}
			wantedID := id
			switch scenario {
			case "symlink":
				must(os.Rename(file, file+".outside"))
				must(os.Symlink(file+".outside", file))
			case "symlink parent":
				dir := filepath.Dir(file)
				must(os.Rename(dir, dir+".outside"))
				must(os.Symlink(dir+".outside", dir))
			case "hardlink":
				must(os.Link(file, file+".outside"))
			case "fifo":
				must(os.Remove(file))
				must(unix.Mkfifo(file, 0640))
			case "other owner":
				must(os.Chown(file, 1001, 1001))
			case "oversize":
				must(os.WriteFile(file, []byte(strings.Repeat("a", 1024)), 0640))
			case "wrong instance":
				wantedID = "22222222-2222-4222-8222-222222222222"
			}
			value, err := readJenkinsBootstrapFile(root, wantedID)
			if (scenario == "regular" && (err != nil || value != jenkinsFixturePassword)) || (scenario != "regular" && (err == nil || value != "")) {
				t.Fatalf("unexpected case outcome: %v", err)
			}
		})
	}
}

func TestJenkinsUnlockTargetChecksSetupBeforeAndAfterAndDoesNotPersistReply(t *testing.T) {
	root := jenkinsProtectedTestRoot(t)
	var requests atomic.Int32
	var finishDuringCopy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.580.2")
		w.Header().Set("Content-Type", "text/html")
		if requests.Add(1)%2 == 0 && finishDuringCopy.Load() {
			_, _ = w.Write([]byte(`normal login`))
			return
		}
		_, _ = w.Write([]byte(fakeJenkinsUnlockHTML))
	}))
	defer server.Close()
	address, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	intent := commandTestRequest()
	install := intent.Installation
	d := &install.Definition
	d.Key, d.CatalogKey, d.Kind, d.Version, d.Port = "custom-jenkins-docker-online", "jenkins", "SERVICE", "2.580.2", 8080
	d.Image, d.Digest, d.ImageID = "docker.io/jenkins/jenkins:2.580.2-lts-jdk21", "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	d.RuntimeModes, d.SourceModes = []string{"DOCKER"}, []string{"REGISTRY"}
	d.Commands.Kind, d.Commands.Locator = "DOCKER", "sfops-${OPS_SOFTWARE_ID}"
	install.Source.Type = "REGISTRY"
	install.Config.RuntimeMode, install.Config.BindAddress, install.Config.HostPort = "DOCKER", address, port
	if err := install.Validate(); err != nil {
		t.Fatal("invalid target fixture", err)
	}
	s := SoftwareRequest{ID: install.SoftwareID, Version: 1, Kind: "DOCKER", Locator: strings.Repeat("c", 64),
		SoftwareKey: strings.ToUpper(d.Key), ExpectedVersion: d.Version,
		Ownership: &SoftwareOwnership{CommandTemplateHash: Digest(*d), RuntimeMode: "DOCKER",
			ControllerID: intent.ControllerID, TargetID: intent.TargetID, SoftwareID: install.SoftwareID, JobID: intent.JobID, ConfigHash: Digest(install.Config), ImageID: d.ImageID}}
	s.IdentityHash = Digest([]string{s.Kind, s.Locator, s.Ownership.ImageID})
	installRoot := serviceinstall.InstallRoot(root, s.ID, install.Config)
	secret := jenkinsBootstrapFixture(t, installRoot, s.ID)
	x := executor{root: root, softwareRun: func(_ time.Duration, _ string, _ ...string) ([]byte, error) {
		return json.Marshal(map[string]any{"id": s.Locator, "name": "/sfops-" + s.ID, "label": s.ID, "image": d.ImageID, "running": true,
			"mounts": []map[string]string{{"Type": "bind", "Source": filepath.Join(installRoot, "data"), "Destination": "/var/jenkins_home"}},
			"ports":  map[string][]map[string]string{"8080/tcp": {{"HostIP": address, "HostPort": portText}}}})
	}}
	jobDir := filepath.Join(root, "jobs", intent.JobID)
	if err := os.MkdirAll(jobDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(jobDir, "intent.json"), intent); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: intent.JobID}); err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(root, "command-trust.json"), commandTrust{intent.ControllerID, intent.TargetID, base64.StdEncoding.EncodeToString(pub)}); err != nil {
		t.Fatal(err)
	}
	req := Request{Action: JenkinsUnlockReadAction, ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: intent.ControllerID, TargetID: intent.TargetID, Software: &s}
	if err = SignCommandRequest(&req, key); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = x.executeSoftware(req, &out); err != jenkinsUnlockBinding || out.Len() != 0 {
		t.Fatal("missing lock should fail without returning material", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "executor.lock")); !os.IsNotExist(err) {
		t.Fatal("read created the target lock")
	}
	lock, err := targetLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = x.executeSoftware(req, &out); err != nil {
		t.Fatal(err)
	}
	var result JenkinsUnlockReadResult
	if err = StrictJSON(out.Bytes(), &result); err != nil || result.Password != jenkinsFixturePassword || result.BindingRevision != s.Version || result.IdentityHash != s.IdentityHash || result.SoftwareID != s.ID || result.ControllerID != req.ControllerID || result.TargetID != req.TargetID {
		t.Fatal("reply did not match bound fixture")
	}
	if requests.Load() != 2 {
		t.Fatal("read used no current post-read setup check")
	}
	finishDuringCopy.Store(true)
	out.Reset()
	if err = x.executeSoftware(req, &out); err != jenkinsUnlockInactive || out.Len() != 0 {
		t.Fatal("inactive setup returned material", err)
	}
	for _, tamper := range []func(*Request){
		func(r *Request) { r.SoftwareOperation = "SOFTWARE_INSTALL" },
		func(r *Request) { r.CommandSignature = "" },
		func(r *Request) { r.DeadlineAt = time.Now().Add(-time.Second).Format(time.RFC3339) },
		func(r *Request) { copy := *r.Software; copy.Version++; r.Software = &copy },
	} {
		invalid := req
		tamper(&invalid)
		if err = x.executeSoftware(invalid, &out); err == nil || out.Len() != 0 {
			t.Fatal("invalid read returned material")
		}
	}
	if err = filepath.WalkDir(root, func(path string, dir fs.DirEntry, err error) error {
		if err != nil || dir.IsDir() || path == secret {
			return err
		}
		contents, err := os.ReadFile(path)
		if err == nil && bytes.Contains(contents, []byte(jenkinsFixturePassword)) {
			t.Fatal("read created a persistent copy outside the original secret file")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
