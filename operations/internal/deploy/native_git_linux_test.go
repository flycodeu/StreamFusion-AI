//go:build linux

package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// The real Ubuntu payload is exercised in a private directory. No package
// transaction, global Git configuration or existing repository is changed.
func TestGitLinuxOfflinePayload(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("SFOPS_TEST_NODE_LINUX") != "1" {
		t.Skip("requires isolated Linux root runner")
	}
	d, err := serviceinstall.Find("git", "2.43.0-1ubuntu7.4")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	archive := filepath.Join(root, "git.deb")
	if err := serviceinstall.PrepareNativeMaterial(ctx, d, archive); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"bin", "runtime"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, name, args...)
		command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		return command.CombinedOutput()
	}
	x := &executor{}
	req := Request{Installation: &serviceinstall.Request{Definition: d}}
	if err = x.prepareGitProgram(ctx, req, root, map[string]string{d.Native.Parts[0].Name: archive}); err != nil {
		t.Fatal(err)
	}
	if err = rootOwnNativeTree(root, 0, true); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bin/git")
	git := func(args ...string) string {
		t.Helper()
		out, err := run(ctx, binary, args...)
		if err != nil {
			t.Fatalf("Git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	if git("--version") != "git version 2.43.0" || git("--exec-path") != filepath.Join(root, "runtime/git/usr/lib/git-core") {
		t.Fatal("Git wrapper did not select the bundled program and helpers")
	}
	repo := filepath.Join(root, "repository")
	git("init", "--initial-branch=main", repo)
	git("-C", repo, "config", "user.name", "Offline Fixture")
	git("-C", repo, "config", "user.email", "offline@example.test")
	git("-C", repo, "commit", "--allow-empty", "-m", "isolated fixture")
	if git("-C", repo, "config", "user.email") != "offline@example.test" || len(git("-C", repo, "rev-parse", "HEAD")) != 40 {
		t.Fatal("Git config/commit unavailable")
	}
	bare := filepath.Join(root, "remote.git")
	git("clone", "--bare", repo, bare)
	if !strings.Contains(git("ls-remote", "--refs", bare), "refs/heads/main") {
		t.Fatal("Git upload-pack helper unavailable")
	}
}
