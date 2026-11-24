package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	got, err := s.reconcile(ctx, a)
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
