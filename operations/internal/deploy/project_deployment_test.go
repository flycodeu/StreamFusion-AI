package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureProjectID = "00000000-0000-4000-8000-000000000001"
	fixtureReleaseA  = "00000000-0000-4000-8000-000000000002"
	fixtureReleaseB  = "00000000-0000-4000-8000-000000000003"
)

func staticProjectFixture(id string) ProjectDeployment {
	return ProjectDeployment{ID: fixtureProjectID, ReleaseID: id, Kind: "STATIC", Port: 18081,
		ArtifactSHA256: strings.Repeat("a", 64), ArtifactSize: 30,
		Site: &ProjectSite{ServerName: "stream.example", SPA: true}}
}

func TestProjectNginxProxyFollowsPlatformAPIRewrite(t *testing.T) {
	p := staticProjectFixture(fixtureReleaseA)
	p.Site.Proxies = []ProjectProxy{{Location: "/api", URL: "http://127.0.0.1:18080", StripPrefix: true, WebSocket: true, SSE: true}}
	out, err := p.NginxConfig("/var/www/sfops/staged/www", "/var/log/nginx/sfops/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"location = /api {\n        proxy_pass http://127.0.0.1:18080/;",
		"location ^~ /api/ {\n        proxy_pass http://127.0.0.1:18080/;",
		"proxy_set_header X-Forwarded-For $remote_addr;",
		"proxy_set_header Upgrade $http_upgrade;", "proxy_buffering off;",
		`add_header X-StreamFusion-Release "` + fixtureReleaseA + `"; alias /var/www/sfops/staged/www/index.html;`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected Nginx directive %q; got:\n%s", want, out)
		}
	}
	p.Site.Proxies = append(p.Site.Proxies, ProjectProxy{Location: "/api/", URL: "http://other"})
	if _, err := p.NginxConfig("/var/www/sfops/staged/www", "/var/log/nginx/sfops/proj"); err == nil {
		t.Fatal("accepted a colliding Nginx location")
	}
}

func TestProjectRollbackRequiresDistinctActiveAndPrevious(t *testing.T) {
	p := staticProjectFixture(fixtureReleaseA)
	if validProjectDeployment(&p, ProjectRollback) == nil {
		t.Fatal("rollback lacks current release CAS")
	}
	p.ExpectedRelease = fixtureReleaseA
	if validProjectDeployment(&p, ProjectRollback) == nil {
		t.Fatal("rollback would target current release")
	}
	p.ExpectedRelease = fixtureReleaseB
	p.ArtifactSHA256, p.ArtifactSize = "", 0
	if err := validProjectDeployment(&p, ProjectRollback); err != nil {
		t.Fatal(err)
	}
	if err := validProjectSecrets(&p, ProjectRollback, map[string]string{"X": "fixture"}); err == nil {
		t.Fatal("rollback accepted an injected credential")
	}
}

func TestProjectReleaseLogsRedactExactAndEscapedSecrets(t *testing.T) {
	const secret = "literal$\"\\secret"
	log := fmt.Sprintf(`ready %s and %q password=field-secret`, secret, secret)
	got := redactProjectLogs(log, []string{"literal", secret})
	if got != "ready [REDACTED] and \"[REDACTED]\" password=[REDACTED]" {
		t.Errorf("unexpected redacted output %q", got)
	}
}

type websiteFixtureEntry struct {
	name, body string
	kind       byte
}

func makeWebsiteFixture(t *testing.T, entries ...websiteFixtureEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	g := gzip.NewWriter(&buffer)
	w := tar.NewWriter(g)
	for _, entry := range entries {
		if err := w.WriteHeader(&tar.Header{Name: entry.name, Typeflag: entry.kind, Size: int64(len(entry.body)), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestProjectWebsiteStagesIndexAndRejectsUnsafeArchives(t *testing.T) {
	for _, tc := range []struct {
		name string
		file websiteFixtureEntry
		ok   bool
	}{
		{"website", websiteFixtureEntry{"./assets/site.js", "site", tar.TypeReg}, true},
		{"traversal", websiteFixtureEntry{"../escape", "bad", tar.TypeReg}, false},
		{"symlink", websiteFixtureEntry{"./link", "", tar.TypeSymlink}, false},
		{"hardlink", websiteFixtureEntry{"./link", "", tar.TypeLink}, false},
		{"duplicate index", websiteFixtureEntry{"dist/index.html", "other", tar.TypeReg}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "site.tar.gz")
			dest := filepath.Join(dir, "site")
			if err := os.Mkdir(dest, 0755); err != nil {
				t.Fatal(err)
			}
			data := makeWebsiteFixture(t, websiteFixtureEntry{"./index.html", "hello", tar.TypeReg}, tc.file)
			if err := os.WriteFile(archive, data, 0600); err != nil {
				t.Fatal(err)
			}
			err := extractProjectWebsite(archive, dest)
			if (err == nil) != tc.ok {
				t.Fatalf("extract = %v", err)
			}
			if tc.ok {
				out, err := os.ReadFile(filepath.Join(dest, "assets", "site.js"))
				if err != nil || string(out) != "site" {
					t.Fatalf("wrong static asset: %q %v", out, err)
				}
			}
		})
	}
}

func TestProjectArtifactWireRejectsUnexpectedBytes(t *testing.T) {
	payload := []byte("expected")
	p := staticProjectFixture(fixtureReleaseA)
	p.ArtifactSHA256 = fmt.Sprintf("%x", sha256.Sum256(payload))
	p.ArtifactSize = int64(len(payload))
	for _, tc := range []struct {
		body string
		ok   bool
	}{{"expected", true}, {"expectedtrailer", false}, {"short", false}, {"unexpected"[:len(payload)], false}} {
		if err := projectReceiveArtifact(strings.NewReader(tc.body), filepath.Join(t.TempDir(), "staged"), p); (err == nil) != tc.ok {
			t.Errorf("receive(%q) = %v", tc.body, err)
		}
	}
}
