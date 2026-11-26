package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newProjectLinuxFixture(t *testing.T) (projectStore, projectActive, projectActive) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("protected project state requires root")
	}
	var base string
	for _, parent := range []string{"/var/tmp", "/var/lib", "/root"} {
		if secureAncestors(parent) != nil {
			continue
		}
		base, _ = os.MkdirTemp(parent, "sfops-project-test-")
		if base != "" {
			break
		}
	}
	if base == "" {
		t.Fatal("could not create protected test directory")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(base); err != nil {
			t.Error(err)
		}
	})
	p := staticProjectFixture(fixtureReleaseA)
	s := newProjectStore(p, filepath.Join(base, "state"))
	s.releases, s.logs, s.control = filepath.Join(base, "releases"), filepath.Join(base, "logs"), filepath.Join(base, "site.conf")
	body := makeWebsiteFixture(t, websiteFixtureEntry{"dist/index.html", "fixture home", '0'}, websiteFixtureEntry{"dist/assets/app.js", "fixture asset", '0'})
	stage := func(id string) projectActive {
		t.Helper()
		p.ReleaseID, p.ArtifactSHA256, p.ArtifactSize = id, fmt.Sprintf("%x", sha256.Sum256(body)), int64(len(body))
		out, err := s.stage(bytes.NewReader(body), p, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	umask := syscall.Umask(0077)
	defer syscall.Umask(umask)
	a, b := stage(fixtureReleaseA), stage(fixtureReleaseB)
	for relative, want := range map[string]os.FileMode{"www": 0755, "www/assets": 0755, "www/assets/app.js": 0644, ".release.json": 0600} {
		st, err := os.Stat(filepath.Join(s.dir(a.ReleaseID), relative))
		if err != nil || st.Mode().Perm() != want {
			t.Fatalf("staged %s has unexpected permissions: %v %v", relative, st, err)
		}
	}
	return s, a, b
}

func TestProjectDeploymentLinuxReconcilesAndRollsBackFrozenRelease(t *testing.T) {
	s, a, b := newProjectLinuxFixture(t)
	running := ""
	reloadCount := 0
	s.reload = func(_ context.Context, p ProjectDeployment, remove bool) error {
		reloadCount++
		running = p.ReleaseID
		if remove {
			running = ""
		}
		return nil
	}
	s.healthy = func(_ context.Context, p ProjectDeployment, _ string) bool { return running == p.ReleaseID }
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := s.activate(ctx, a, projectActive{}, nil); err != nil {
		t.Fatal(err)
	}

	// Interrupted after the new config reloaded, before active.json committed.
	if err := atomicJSON(s.pending(), projectPending{Candidate: b.ReleaseID, Baseline: a.ReleaseID}); err != nil {
		t.Fatal(err)
	}
	bBody, _ := s.body(b)
	if err := projectAtomicFile(s.control, bBody, 0644); err != nil {
		t.Fatal(err)
	}
	running = b.ReleaseID
	if observed, health := s.observePending(ctx, a); observed != b.ReleaseID || health != "READY" {
		t.Fatalf("pending read did not identify candidate: %s %s", observed, health)
	}
	before := reloadCount
	if _, err := s.resolveActive(ctx, a, ProjectObserve, a.Spec); !errors.Is(err, projectRecoveryRequired) {
		t.Fatalf("observation failed to report pending mutation: %v", err)
	}
	if _, err := s.resolveActive(ctx, a, ProjectRecover, a.Spec); !errors.Is(err, projectRecoveryRequired) {
		t.Fatalf("unrelated recovery got through: %v", err)
	}
	if running != b.ReleaseID || reloadCount != before {
		t.Fatalf("read-only check or unrelated recovery changed release: running=%s reloads=%d", running, reloadCount)
	}
	if pending, err := s.readPending(); err != nil || pending.Candidate != b.ReleaseID {
		t.Fatalf("read-only observation removed journal: %v %#v", err, pending)
	}
	recoverSpec := b.Spec
	recoverSpec.ExpectedRelease = a.ReleaseID
	got, err := s.resolveActive(ctx, a, ProjectRecover, recoverSpec)
	if err != nil || got.ReleaseID != a.ReleaseID || running != a.ReleaseID {
		t.Fatalf("reconciliation did not restore baseline: %v %s %s", err, got.ReleaseID, running)
	}
	if _, err = s.staged(b.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if err = s.activate(ctx, b, a, nil); err != nil {
		t.Fatal(err)
	}

	// Interrupted after durable commit, before deleting the pending marker.
	if err = atomicJSON(s.pending(), projectPending{Candidate: b.ReleaseID, Baseline: a.ReleaseID}); err != nil {
		t.Fatal(err)
	}
	numBefore := reloadCount
	got, err = s.reconcile(ctx, b)
	if err != nil || got.ReleaseID != b.ReleaseID || running != b.ReleaseID || numBefore != reloadCount {
		t.Fatalf("committed candidate was altered: %v %s %s", err, got.ReleaseID, running)
	}

	// A rollback uses the previously staged version and reloads once.
	old, err := s.staged(a.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.activate(ctx, old, b, nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.readActive()
	if err != nil || got.ReleaseID != a.ReleaseID || running != a.ReleaseID || reloadCount != numBefore+1 {
		t.Fatalf("rollback did not activate staged baseline: %v %s %s", err, got.ReleaseID, running)
	}
}

func TestProjectDeploymentLinuxRecognizesOnlyCurrentAndLegacyNginxControl(t *testing.T) {
	p := staticProjectFixture(fixtureReleaseA)
	p.Site.HealthPath = StreamFusionSiteHealthPath
	p.Site.Proxies = []ProjectProxy{{Location: "/api", URL: "http://127.0.0.1:18080", StripPrefix: true}}
	s := newProjectStore(p, "/var/lib/sfops/state")
	release := projectActive{ReleaseID: p.ReleaseID, Spec: p}
	wanted, err := s.body(release)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := p.nginxConfig(filepath.Join(s.dir(p.ReleaseID), "www"), s.logs, false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(wanted, []byte(legacy)) || !s.matchesControl([]byte(legacy), wanted, release) {
		t.Fatal("previous generated site must remain recognizable after executor upgrade")
	}
	drift := strings.Replace(legacy, "127.0.0.1:18080", "127.0.0.1:18081", 1)
	if s.matchesControl([]byte(drift), wanted, release) {
		t.Fatal("changing the upstream is still configuration drift")
	}
}

func TestProjectDeploymentLinuxDecommissionResumesAndPreservesHistory(t *testing.T) {
	s, a, b := newProjectLinuxFixture(t)
	ctx := t.Context()
	reloadCount := 0
	running := ""
	failNextReload := false
	s.reload = func(_ context.Context, p ProjectDeployment, remove bool) error {
		reloadCount++
		if failNextReload {
			failNextReload = false
			return errors.New("failed to reload Nginx")
		}
		running = p.ReleaseID
		if remove {
			running = ""
		}
		return nil
	}
	s.healthy = func(_ context.Context, p ProjectDeployment, _ string) bool { return running == p.ReleaseID }
	s.stillServing = func(_ context.Context, p ProjectDeployment) bool { return running == p.ReleaseID }
	if err := s.activate(ctx, a, projectActive{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.decommission(ctx, a, b.ReleaseID); err == nil {
		t.Fatal("decommissioned unexpected active release")
	}
	original, err := s.currentControl()
	if err != nil {
		t.Fatal(err)
	}
	if err = projectAtomicFile(s.control, []byte("# custom\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.decommission(ctx, a, a.ReleaseID); !errors.Is(err, projectControlDrift) || reloadCount != 1 {
		t.Fatalf("custom configuration was not protected: %d %v", reloadCount, err)
	}
	if err = projectAtomicFile(s.control, original, 0644); err != nil {
		t.Fatal(err)
	}
	failNextReload = true
	if err = s.decommission(ctx, a, a.ReleaseID); !errors.Is(err, projectRecoveryRequired) {
		t.Fatalf("interrupted decommission unexpectedly finalized: %v", err)
	}
	if _, err = s.resolveActive(ctx, a, ProjectObserve, a.Spec); !errors.Is(err, projectRecoveryRequired) {
		t.Fatalf("observation ignored unfinished decommission: %v", err)
	}
	if running != a.ReleaseID || reloadCount != 2 {
		t.Fatalf("read-only observation altered service: running=%s reloads=%d", running, reloadCount)
	}
	if err = s.decommission(ctx, a, a.ReleaseID); err != nil {
		t.Fatalf("decommission resume failed: %v", err)
	}
	active, err := s.readActive()
	if err != nil || active.ReleaseID != "" || running != "" {
		t.Fatalf("decommission left an active release: %v %v %s", active, err, running)
	}
	if err = s.decommission(ctx, active, a.ReleaseID); err != nil || reloadCount != 3 {
		t.Fatalf("response-loss retry was not idempotent: %d %v", reloadCount, err)
	}
	if _, err = s.staged(a.ReleaseID); err != nil {
		t.Fatalf("decommission removed release history: %v", err)
	}
	if err = s.activate(ctx, b, active, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.decommission(ctx, b, a.ReleaseID); err == nil || running != b.ReleaseID {
		t.Fatal("stale retry altered later release")
	}
}

func TestProjectDeploymentLinuxDecommissionStopsOwnedJavaBeforeRemovingUnit(t *testing.T) {
	fixture, release, _ := newProjectLinuxFixture(t)
	file, hash, size := jarFixture(t, "Manifest-Version: 1.0\r\nMain-Class: demo.Main\r\n\r\n", 65)
	jar, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer jar.Close()
	p := ProjectDeployment{
		ID: release.Spec.ID, ReleaseID: release.ReleaseID, Kind: "JAVA", Port: 18080, Address: "127.0.0.1",
		Profile: "prod", MemoryMiB: 256, HealthPath: "/health", ArtifactSHA256: hash, ArtifactSize: size,
	}
	s := newProjectStore(p, filepath.Join(filepath.Dir(fixture.base), "java-state"))
	s.releases = filepath.Join(filepath.Dir(fixture.base), "java-releases")
	s.control = filepath.Join(filepath.Dir(fixture.base), "app.service")
	staged, err := s.stage(jar, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	s.reload = func(_ context.Context, _ ProjectDeployment, remove bool) error {
		if remove {
			if _, err := os.Stat(s.control); !os.IsNotExist(err) {
				t.Fatalf("unit file remained when reloading systemd: %v", err)
			}
			calls = append(calls, "reload")
		}
		return nil
	}
	s.healthy = func(context.Context, ProjectDeployment, string) bool { return true }
	if err := s.activate(t.Context(), staged, projectActive{}, nil); err != nil {
		t.Fatal(err)
	}
	s.verifyOwnedUnit = func(context.Context, projectActive, string, string) error { return projectControlDrift }
	s.removeUnit = func(context.Context, string) error {
		if _, err := os.Stat(s.control); err != nil {
			t.Fatalf("owned unit disappeared before stop: %v", err)
		}
		calls = append(calls, "stop")
		return nil
	}
	s.verifyStoppedUnit = func(context.Context, string) error {
		calls = append(calls, "verify")
		return nil
	}
	if err := s.decommission(t.Context(), staged, p.ReleaseID); !errors.Is(err, projectControlDrift) || s.decommissionPending(staged) {
		t.Fatalf("foreign runtime fragment should fail before journal or stop: %v", err)
	}
	s.verifyOwnedUnit = func(context.Context, projectActive, string, string) error {
		calls = append(calls, "check ownership")
		return nil
	}
	if err := s.decommission(t.Context(), staged, p.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(calls) != "[check ownership stop reload verify]" {
		t.Fatalf("unexpected Java decommission effects: %v", calls)
	}
	if _, err := s.staged(p.ReleaseID); err != nil {
		t.Fatalf("decommission should retain deployed JAR: %v", err)
	}
}

func TestProjectDeploymentLinuxRetentionPreservesKnownRollbackAndRefusesUnknownFiles(t *testing.T) {
	s, a, b := newProjectLinuxFixture(t)
	ctx := t.Context()
	s.reload = func(context.Context, ProjectDeployment, bool) error { return nil }
	s.healthy = func(context.Context, ProjectDeployment, string) bool { return true }
	if err := s.activate(ctx, a, projectActive{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.activate(ctx, b, a, nil); err != nil {
		t.Fatal(err)
	}
	c := staticProjectFixture("00000000-0000-4000-8000-000000000004")
	content := makeWebsiteFixture(t, websiteFixtureEntry{"dist/index.html", "fixture home", '0'})
	c.ArtifactSHA256, c.ArtifactSize = fmt.Sprintf("%x", sha256.Sum256(content)), int64(len(content))
	staged, err := s.stage(bytes.NewReader(content), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	unowned := s.dir("00000000-0000-4000-8000-000000000005")
	if err := os.Symlink(s.dir(a.ReleaseID), unowned); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(s.pending(), projectPending{Candidate: c.ReleaseID, Baseline: b.ReleaseID}); err != nil {
		t.Fatal(err)
	}
	if s.retainReleases(ctx, b, a.ReleaseID) != projectRetentionWarning {
		t.Fatal("retention ignored an unresolved release mutation")
	}
	if _, err := s.staged(c.ReleaseID); err != nil {
		t.Fatalf("retention removed pending release: %v", err)
	}
	if err := s.clearPending(); err != nil {
		t.Fatal(err)
	}
	if err := s.activate(ctx, staged, b, nil); err != nil {
		t.Fatal(err)
	}
	if s.retainReleases(ctx, staged, b.ReleaseID) != projectRetentionWarning {
		t.Fatal("retention should report unmatched version directory")
	}
	if _, err := s.staged(a.ReleaseID); !os.IsNotExist(err) {
		t.Fatalf("retention did not prune oldest managed release: %v", err)
	}
	for _, item := range []string{b.ReleaseID, c.ReleaseID} {
		if _, err := s.staged(item); err != nil {
			t.Fatalf("retention pruned required release: %v", err)
		}
	}
	if _, err := os.Lstat(unowned); err != nil {
		t.Fatalf("retention altered unowned entry: %v", err)
	}
}

func TestProjectDeploymentLinuxRefusesSymlinksAndControlDrift(t *testing.T) {
	s, a, b := newProjectLinuxFixture(t)
	ctx := context.Background()
	s.healthy = func(context.Context, ProjectDeployment, string) bool { return false }
	s.reload = func(context.Context, ProjectDeployment, bool) error {
		t.Fatal("attempted to reload after control drift")
		return nil
	}
	if err := atomicJSON(s.state(), a); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(s.pending(), projectPending{Candidate: b.ReleaseID, Baseline: a.ReleaseID}); err != nil {
		t.Fatal(err)
	}
	custom := []byte("# changed by an administrator\n")
	if err := os.WriteFile(s.control, custom, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reconcile(ctx, a); !errors.Is(err, projectRecoveryRequired) {
		t.Fatalf("reconcile changed bespoke control file: %v", err)
	}
	if got, _ := os.ReadFile(s.control); !bytes.Equal(got, custom) {
		t.Fatal("bespoke control file overwritten")
	}

	target := filepath.Join(filepath.Dir(s.base), "unmanaged")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.base, "external")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := projectSecureDir(filepath.Join(link, "should-not-create"), 0755); err == nil {
		t.Fatal("followed deployment directory symlink")
	}
	if _, err := projectReadSmall(filepath.Join(link, "missing"), 4096); err == nil || os.IsNotExist(err) {
		t.Fatalf("unsafe parent misreported as missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "should-not-create")); !os.IsNotExist(err) {
		t.Fatal("created a directory outside managed root")
	}
	if err := os.Symlink(s.control, filepath.Join(s.logs, "access.log")); err != nil {
		t.Fatal(err)
	}
	if err := s.safeLogs(); err == nil {
		t.Fatal("accepted log symlink for nginx reload")
	}
}
