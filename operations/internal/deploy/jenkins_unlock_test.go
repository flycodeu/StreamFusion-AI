package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fakeJenkinsUnlockHTML = `<html><input id="security-token" class="jenkins-input " type="password" name="j_password"/></html>`
const installedJenkinsUnlockInput = `<input name="j_password" id="security-token" type="password" class="jenkins-input ">`

func TestJenkinsUnlockPageRequiresExactVersionAndBootstrapInput(t *testing.T) {
	cases := []struct {
		name, version, body string
		status              int
		want                error
	}{
		{"active", "2.580.2", fakeJenkinsUnlockHTML, 200, nil},
		{"installed Jenkins attribute order", "2.580.2", installedJenkinsUnlockInput, 200, nil},
		{"normal login", "2.580.2", `<input id="j_password" type="password" name="j_password"/>`, 200, jenkinsUnlockInactive},
		{"non-browser marker", "2.580.2", `id="security-token"`, 200, jenkinsUnlockInactive},
		{"commented input", "2.580.2", "<!--" + installedJenkinsUnlockInput + "-->", 200, jenkinsUnlockInactive},
		{"script text", "2.580.2", "<script>const sample = '" + installedJenkinsUnlockInput + "';</script>", 200, jenkinsUnlockInactive},
		{"nested template", "2.580.2", "<template><template></template>" + installedJenkinsUnlockInput + "</template>", 200, jenkinsUnlockInactive},
		{"escaped input in unrelated attribute", "2.580.2", "<div data-input='&lt;" + installedJenkinsUnlockInput[1:] + "'></div>", 200, jenkinsUnlockInactive},
		{"raw input in unrelated attribute", "2.580.2", "<div data-input='" + installedJenkinsUnlockInput + "'></div>", 200, jenkinsUnlockUnavailable},
		{"lookalike attributes", "2.580.2", `<input data-id="security-token" type="password" data-name="j_password">`, 200, jenkinsUnlockInactive},
		{"conflicting duplicate", "2.580.2", `<input id="normal" id="security-token" type="password" name="j_password">`, 200, jenkinsUnlockInactive},
		{"unparseable response", "2.580.2", `<input id="security-token`, 200, jenkinsUnlockUnavailable},
		{"foreign service", "", fakeJenkinsUnlockHTML, 200, jenkinsUnlockUnavailable},
		{"wrong version", "2.580.3", fakeJenkinsUnlockHTML, 200, jenkinsUnlockUnavailable},
		{"oversized response", "2.580.2", fakeJenkinsUnlockHTML + strings.Repeat("a", 256<<10), 200, jenkinsUnlockUnavailable},
		{"redirect", "2.580.2", "", 302, jenkinsUnlockUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/login" || r.Method != http.MethodGet {
					t.Error("unexpected Jenkins route")
				}
				w.Header().Set("X-Jenkins", tc.version)
				w.Header().Set("Content-Type", "text/html; charset=UTF-8")
				w.Header().Set("Location", "/unsafe")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			if err := verifyJenkinsUnlockPage(context.Background(), server.Listener.Addr().String(), "2.580.2"); err != tc.want || requests.Load() != 1 {
				t.Fatalf("unexpected result: %v, calls: %d", err, requests.Load())
			}
		})
	}
}

func TestJenkinsUnlockDockerBindingRequiresOwnedMountAndPublishedPort(t *testing.T) {
	s := SoftwareRequest{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Kind: "DOCKER", Locator: strings.Repeat("b", 64),
		Ownership: &SoftwareOwnership{ImageID: "sha256:" + strings.Repeat("c", 64)}}
	s.IdentityHash = Digest([]string{s.Kind, s.Locator, s.Ownership.ImageID})
	root := filepath.Join(t.TempDir(), s.ID)
	for _, tc := range []string{"valid", "invalid id", "wrong name", "wrong owner", "wrong image", "paused", "different data", "nested mount", "wrong port"} {
		t.Run(tc, func(t *testing.T) {
			metadata := map[string]any{"id": s.Locator, "name": "/sfops-" + s.ID, "label": s.ID, "image": s.Ownership.ImageID,
				"running": true, "paused": false}
			mount := map[string]string{"Type": "bind", "Source": filepath.Join(root, "data"), "Destination": "/var/jenkins_home"}
			mounts := []map[string]string{mount}
			port := map[string]string{"HostIp": "0.0.0.0", "HostPort": "18080"}
			switch tc {
			case "invalid id":
				metadata["id"] = strings.Repeat("d", 64)
			case "wrong name":
				metadata["name"] = "/another-jenkins"
			case "wrong owner":
				metadata["label"] = "foreign"
			case "wrong image":
				metadata["image"] = "sha256:" + strings.Repeat("e", 64)
			case "paused":
				metadata["paused"] = true
			case "different data":
				mount["Source"] = filepath.Join(root, "data-other")
			case "nested mount":
				mounts = append(mounts, map[string]string{"Type": "bind", "Source": "/other", "Destination": "/var/jenkins_home/secrets"})
			case "wrong port":
				port["HostPort"] = "18081"
			}
			metadata["mounts"] = mounts
			metadata["ports"] = map[string][]map[string]string{"8080/tcp": {port}}
			x := executor{softwareRun: func(_ time.Duration, command string, args ...string) ([]byte, error) {
				if command != "/usr/bin/docker" || len(args) != 6 || args[5] != s.Locator || !strings.Contains(args[3], ".NetworkSettings.Ports") {
					t.Fatal("did not query only the expected container")
				}
				return json.Marshal(metadata)
			}}
			if err := x.jenkinsDockerBinding(context.Background(), s, root, "0.0.0.0", 18080); (err == nil) != (tc == "valid") {
				t.Fatalf("unexpected binding result: %v", err)
			}
		})
	}
}
