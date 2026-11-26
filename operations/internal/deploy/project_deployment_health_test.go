package deploy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamFusionSiteHealthRequiresAPIThroughSite(t *testing.T) {
	var mode atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "platform.example.test" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/__sfops_health":
			w.Header().Set("X-StreamFusion-Release", "r1")
			w.Header().Set("Content-Length", "42")
		case r.Method == http.MethodGet && r.URL.Path == StreamFusionSiteHealthPath:
			switch mode.Load() {
			case 0:
				w.Header().Set("Content-Type", "application/vnd.spring-boot.actuator.v3+json")
				fmt.Fprint(w, `{"status":"UP"}`)
			case 1:
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><div id="app"></div>`)
			case 2:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"status":"DOWN"}`)
			default:
				http.Redirect(w, r, "/", http.StatusFound)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer site.Close()
	_, port, err := net.SplitHostPort(site.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	spec := ProjectDeployment{Kind: "STATIC", ReleaseID: "r1", Port: n,
		Site: &ProjectSite{ServerName: "platform.example.test", HealthPath: StreamFusionSiteHealthPath}}
	for i, want := range []bool{true, false, false, false} {
		mode.Store(int32(i))
		if got := projectHealth(t.Context(), spec, ""); got != want {
			t.Fatalf("mode %d: site health = %t, want %t", i, got, want)
		}
	}
	spec.Site.HealthPath = ""
	if !projectHealth(t.Context(), spec, "") {
		t.Fatal("generic static health should only require the release marker")
	}
}

func TestProjectJavaActuatorHealthRejectsFalseSuccess(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			fmt.Fprint(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"DOWN"}`)
	}))
	defer api.Close()
	if projectJavaEndpointHealth(t.Context(), api.URL, projectActuatorHealthPath) {
		t.Fatal("HTTP 200 with Actuator DOWN was accepted")
	}
	if !projectJavaEndpointHealth(t.Context(), api.URL, "/health") {
		t.Fatal("custom text health stopped working")
	}
}

func TestStreamFusionGeneratedSiteWithNginx(t *testing.T) {
	if os.Getenv("SFOPS_TEST_PROJECT_NGINX") != "1" {
		t.Skip("set SFOPS_TEST_PROJECT_NGINX=1 to test an isolated Nginx process")
	}
	nginx, err := exec.LookPath("nginx")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/actuator/health" {
			fmt.Fprint(w, `{"status":"UP"}`)
			return
		}
		if r.URL.Path == "/check-forwarding" {
			fmt.Fprint(w, `{"chain":`+strconv.Quote(r.Header.Get("X-Forwarded-For"))+`}`)
			return
		}
		if r.URL.Path == "/check-upload" {
			count, err := io.Copy(io.Discard, r.Body)
			if err != nil || count != 2<<20 {
				w.WriteHeader(http.StatusBadRequest)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"path":`+strconv.Quote(r.URL.Path)+`}`)
	}))
	defer api.Close()
	portListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	portListener.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><div id=app></div>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api-docs.html"), []byte("<!doctype html><div id=docs></div>"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := ProjectDeployment{Kind: "STATIC", ID: "sample", ReleaseID: "r1", Port: port, Site: &ProjectSite{
		ServerName: "platform.example.test", SPA: true, HealthPath: StreamFusionSiteHealthPath,
		Proxies: []ProjectProxy{{Location: "/api", URL: api.URL, StripPrefix: true}},
	}}
	fragment, err := spec.NginxConfig(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	fragment = strings.Replace(fragment, fmt.Sprintf("listen %d;", port), fmt.Sprintf("listen 127.0.0.1:%d;", port), 1)
	cfg := filepath.Join(dir, "nginx.conf")
	config := "daemon off;\nmaster_process off;\npid " + filepath.Join(dir, "nginx.pid") +
		";\nerror_log " + filepath.Join(dir, "process.log") + ";\nevents {}\nhttp {\n" + fragment + "}\n"
	if err := os.WriteFile(cfg, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nginx, "-p", dir, "-c", cfg)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for end := time.Now().Add(3 * time.Second); !projectHealth(t.Context(), spec, ""); {
		if time.Now().After(end) {
			t.Fatal("generated site did not serve the release marker and API health")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !projectSiteUpstreamHealth(t.Context(), spec.Site) {
		t.Fatal("could not verify API directly from the site")
	}
	for _, tt := range []struct {
		path    string
		code    int
		body    string
		browser bool
	}{
		{"/login", 200, "<div id=app>", true},
		{"/preview/center", 200, "<div id=app>", true},
		{"/storage/manage", 200, "<div id=app>", true},
		{"/api-docs.html", 200, "<div id=docs>", true},
		{"/__sfops_health", 200, "<div id=app>", true},
		{"/api/does-not-exist", 404, `{"path":"/does-not-exist"}`, false},
		{"/api/check-forwarding", 200, `{"chain":"192.0.2.42, 127.0.0.1"}`, false},
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, projectLocalSite(spec)+tt.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = spec.Site.ServerName
		if tt.path == "/api/check-forwarding" {
			req.Header.Set("X-Forwarded-For", "192.0.2.42")
		}
		resp, err := projectHealthClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != tt.code || !strings.Contains(string(body), tt.body) {
			t.Fatalf("%s: status=%d body=%q error=%v", tt.path, resp.StatusCode, body, err)
		}
		for name, value := range map[string]string{
			"Content-Security-Policy": "frame-ancestors 'self'",
			"X-Frame-Options":         "SAMEORIGIN",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "strict-origin-when-cross-origin",
		} {
			want := ""
			if tt.browser {
				want = value
			}
			if actual := resp.Header.Get(name); actual != want {
				t.Errorf("%s: %s = %q, want %q", tt.path, name, actual, want)
			}
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, projectLocalSite(spec)+"/api/check-upload", strings.NewReader(strings.Repeat("x", 2<<20)))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = spec.Site.ServerName
	resp, err := projectHealthClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("site rejected a supported upload: status=%d", resp.StatusCode)
	}
}
