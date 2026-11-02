//go:build linux

package deploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestNodeTerminalLinuxBashLifecycle(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("SFOPS_TEST_NODE_LINUX") != "1" {
		t.Skip("requires isolated Linux account fixture with SFOPS_TEST_NODE_LINUX=1")
	}
	name := fmt.Sprintf("sfnode-%d-%d", os.Getpid(), time.Now().Unix()%100000)
	home := "/home/" + name
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("fixture HOME exists")
	}
	// Runner images may preload nvm through /etc/skel/.bash_profile. This
	// lifecycle starts with an unmanaged Bash account; foreign managers are
	// exercised explicitly below instead of inherited from the host image.
	skeleton := t.TempDir()
	profiles := map[string]string{
		".bashrc":  "# existing interactive configuration\nexport KEEP_ME='unchanged'\ncase $- in *i*) ;; *) return ;; esac\n",
		".profile": "# existing login configuration\nif [ -n \"${BASH_VERSION:-}\" ] && [ -f \"$HOME/.bashrc\" ]; then . \"$HOME/.bashrc\"; fi\n",
	}
	for file, content := range profiles {
		if err := os.WriteFile(filepath.Join(skeleton, file), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("/usr/sbin/useradd", "--create-home", "--skel", skeleton, "--home-dir", home, "--shell", "/bin/bash", name).CombinedOutput(); err != nil {
		t.Fatalf("account fixture: %v %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("/usr/sbin/userdel", name).Run()
		if filepath.Clean(home) == "/home/"+name && strings.HasPrefix(name, "sfnode-") {
			_ = os.RemoveAll(home)
		}
	})
	account, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	n := testNodeContext()
	n.Account = name
	n.AccountUID = account.Uid
	n.RootPath = filepath.Join(home, ".local/share/streamfusion-ops/node", n.EnvironmentID)
	x := &executor{root: t.TempDir(), owner: Owner{ControllerID: "22222222-2222-4222-8222-222222222222", TargetID: "33333333-3333-4333-8333-333333333333"}}
	seq := 0
	request := func() (Request, string) {
		seq++
		id := fmt.Sprintf("44444444-4444-4444-8444-%012d", seq)
		dir := filepath.Join(x.root, "jobs", id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		return Request{ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, JobID: id, InputHash: strings.Repeat("a", 64)}, dir
	}
	original := map[string][]byte{}
	for _, file := range []string{".bashrc", ".profile"} {
		raw, e := os.ReadFile(filepath.Join(home, file))
		if e != nil {
			t.Fatal(e)
		}
		original[file] = raw
	}
	material := func(name string) *serviceinstall.Source {
		base := os.Getenv("SFOPS_TEST_NODE_MATERIAL_DIR")
		if base == "" {
			return &serviceinstall.Source{Type: "OFFICIAL"}
		}
		file := filepath.Join(base, name)
		info, e := os.Stat(file)
		if e != nil {
			t.Fatal(e)
		}
		hash, e := fileSHA(file)
		if e != nil {
			t.Fatal(e)
		}
		input, e := os.Open(file)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { input.Close() })
		x.installationInput = input
		return &serviceinstall.Source{Type: "ARCHIVE", SHA256: hash, ByteSize: info.Size()}
	}
	req, dir := request()
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "INITIALIZE", Source: *material("nvm-0.40.8-native.tar")}
	r, err := x.applyNodeEnvironment(req, dir)
	if err != nil || r.Status != "SUCCEEDED" {
		log, _ := os.ReadFile(filepath.Join(dir, "service-install.log"))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, inspectErr := runNode(ctx, n, "inspect", "", "", "", "", "", x.softwareRunner)
		t.Fatalf("init: %+v %v log=%s inspect=%s err=%v", r, err, log, out, inspectErr)
	}
	d := serviceinstall.NodeCatalog()[0]
	n.ConnectTerminal = true
	req, dir = request()
	req.Installation = &serviceinstall.Request{SoftwareID: "55555555-5555-4555-8555-555555555555", BindingRevision: 1, Definition: d, Config: serviceinstall.Config{Name: "node-terminal-test", RuntimeMode: "NATIVE", MemoryMiB: 512}, Source: *material("node-" + d.Version + "-native-node-" + d.Version + "-linux-x64.tar.xz"), Node: &n}
	// A high-priority profile from another nvm installation must still block
	// terminal integration, including the form used by GitHub runner images.
	foreignProfile := filepath.Join(home, ".bash_profile")
	foreignContent := []byte("[ -s \"$NVM_DIR/nvm.sh\" ] && \\. \"$NVM_DIR/nvm.sh\"  # This loads nvm\n")
	if err := os.WriteFile(foreignProfile, foreignContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(foreignProfile, uid, gid); err != nil {
		t.Fatal(err)
	}
	conflict := x.nodeInstallPreflight(*req.Installation)
	if conflict.Ready || len(conflict.Checks) != 1 || conflict.Checks[0].Code != "NODE_TERMINAL_CONFLICT" {
		t.Fatalf("foreign nvm preflight: %+v", conflict)
	}
	if raw, err := os.ReadFile(foreignProfile); err != nil || !bytes.Equal(raw, foreignContent) {
		t.Fatal("preflight changed foreign profile")
	}
	for file, before := range original {
		if raw, err := os.ReadFile(filepath.Join(home, file)); err != nil || !bytes.Equal(raw, before) {
			t.Fatal("preflight changed existing profile")
		}
	}
	if err := os.Remove(foreignProfile); err != nil {
		t.Fatal(err)
	}
	pre := x.nodeInstallPreflight(*req.Installation)
	if !pre.Ready || pre.NodeTerminalHash == "" {
		t.Fatalf("preflight: %+v", pre)
	}
	n.TerminalHash = pre.NodeTerminalHash
	r, err = x.installNodeVersion(req, dir)
	if err != nil || r.Status != "SUCCEEDED" || r.NodeEnvironment.Terminal == nil || r.NodeEnvironment.Terminal.Status != "CONNECTED" {
		t.Fatalf("install: %+v %v", r, err)
	}
	// Neither the explicit no-default choice nor existing shell bytes/modes change.
	if r.NodeEnvironment.DefaultVersion != "" {
		t.Fatal("installation selected unwanted default")
	}
	for file, before := range original {
		raw, _ := os.ReadFile(filepath.Join(home, file))
		if !bytes.HasPrefix(raw, before) {
			t.Fatal("profile changed outside block")
		}
		st, _ := os.Stat(filepath.Join(home, file))
		if st.Mode().Perm() != 0644 {
			t.Fatal("profile mode changed")
		}
		backup, _ := os.ReadFile(filepath.Join(n.RootPath, "backups/terminal", req.JobID, file))
		if !bytes.Equal(backup, before) {
			t.Fatal("backup differs")
		}
	}
	shell := func(flag, command string) string {
		out, e := exec.Command("/usr/sbin/runuser", "-u", name, "--", "/usr/bin/env", "-i", "HOME="+home, "PATH=/usr/bin:/bin", "TERM=dumb", "/bin/bash", flag, command).CombinedOutput()
		if e != nil {
			t.Fatalf("bash %s: %s %v", flag, out, e)
		}
		return string(out)
	}
	if !strings.Contains(shell("-ic", "type nvm >/dev/null; printf 'NVM_OK\\n'"), "NVM_OK") {
		t.Fatal("interactive nvm absent")
	}
	n.TerminalHash = ""
	n.ExpectedDefault = ""
	req, dir = request()
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "SET_DEFAULT", NodeVersion: d.Version}
	r, err = x.applyNodeEnvironment(req, dir)
	if err != nil || r.Status != "SUCCEEDED" {
		t.Fatalf("default: %+v %v", r, err)
	}
	n.ExpectedDefault = d.Version
	for _, flag := range []string{"-lc", "-ic"} {
		out := shell(flag, "node --version; npm --version")
		if !strings.Contains(out, d.Version) {
			t.Fatalf("new bash missing Node: %s", out)
		}
	}
	// Repeated explicit connection is a no-op, yet has its own verifiable intent.
	beforeBash, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	req, dir = request()
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "CONNECT_TERMINAL"}
	r, err = x.applyNodeEnvironment(req, dir)
	if err != nil || r.Status != "SUCCEEDED" {
		t.Fatalf("reconnect: %+v %v", r, err)
	}
	afterBash, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if !bytes.Equal(beforeBash, afterBash) {
		t.Fatal("duplicate block")
	}
	// Higher-priority existing login file is selected, never create .bash_profile.
	if _, e := os.Lstat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(e) {
		t.Fatal("shadowed profile")
	}
	login := filepath.Join(home, ".bash_login")
	if e := os.WriteFile(login, []byte("# keep login\n"), 0640); e != nil {
		t.Fatal(e)
	}
	_ = os.Chown(login, uid, gid)
	observation, _, err := nodeTerminalFiles(n)
	if err != nil || observation.Profiles[1].Path != login {
		t.Fatalf("login selection: %+v %v", observation, err)
	}
	// No symlinks, wrong owners, foreign nvm or large files are accepted.
	_ = os.Remove(login)
	_ = os.Symlink(filepath.Join(home, ".profile"), login)
	if _, _, err = nodeTerminalFiles(n); err == nil {
		t.Fatal("symlink accepted")
	}
	_ = os.Remove(login)
	_ = os.WriteFile(login, []byte("export NVM_DIR=~/.nvm\n"), 0644)
	_ = os.Chown(login, uid, gid)
	observation, _, err = nodeTerminalFiles(n)
	if err != nil || observation.Status != "CONFLICT" {
		t.Fatal("foreign nvm accepted")
	}
	_ = os.Chown(login, 0, 0)
	if _, _, err = nodeTerminalFiles(n); err == nil {
		t.Fatal("wrong owner accepted")
	}
	_ = os.Remove(login)
	// Recovery cannot replay a partial operation or report success after one file.
	for file, raw := range original {
		if e := os.WriteFile(filepath.Join(home, file), raw, 0644); e != nil {
			t.Fatal(e)
		}
		_ = os.Chown(filepath.Join(home, file), uid, gid)
	}
	observation, plan, err := nodeTerminalFiles(n)
	if err != nil {
		t.Fatal(err)
	}
	req, dir = request()
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "CONNECT_TERMINAL"}
	if e := atomicJSON(filepath.Join(dir, "node-before.json"), r.NodeEnvironment); e != nil {
		t.Fatal(e)
	}
	if e := atomicJSON(filepath.Join(dir, "node-change.json"), req.NodeEnvironment); e != nil {
		t.Fatal(e)
	}
	if e := atomicJSON(filepath.Join(dir, "node-terminal.json"), plan); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(plan.Files[0].Path, plan.Files[0].Content, 0644); e != nil {
		t.Fatal(e)
	}
	_ = os.Chown(plan.Files[0].Path, uid, gid)
	if got := x.reconcileNodeEnvironment(req, dir); got.Status == "SUCCEEDED" {
		t.Fatal("partial terminal update falsely succeeded")
	}
	raw, _ := os.ReadFile(plan.Files[1].Path)
	if !bytes.Equal(raw, original[".profile"]) {
		t.Fatal("recovery replayed second file")
	}
	if e := os.WriteFile(plan.Files[1].Path, plan.Files[1].Content, 0644); e != nil {
		t.Fatal(e)
	}
	_ = os.Chown(plan.Files[1].Path, uid, gid)
	if got := x.reconcileNodeEnvironment(req, dir); got.Status != "SUCCEEDED" {
		t.Fatalf("completed observation did not recover: %+v", got)
	}
	// Once frozen, changed user configuration is rejected before any write.
	n.TerminalHash = Digest(observation)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, dir = request()
	if e := x.connectNodeTerminal(ctx, n, "66666666-6666-4666-8666-666666666666", dir); e == nil || e.Error() != "NODE_TERMINAL_CHANGED" {
		t.Fatalf("drift accepted: %v", e)
	}
	current, _, err := nodeTerminalFiles(n)
	if err != nil {
		t.Fatal(err)
	}
	n.TerminalHash = Digest(current)
	if err = os.Chmod(filepath.Join(home, ".bashrc"), 0600); err != nil {
		t.Fatal(err)
	}
	_, dir = request()
	if e := x.connectNodeTerminal(ctx, n, "66666666-6666-4666-8666-666666666666", dir); e == nil || e.Error() != "NODE_TERMINAL_CHANGED" {
		t.Fatalf("mode drift accepted: %v", e)
	}
	_ = os.Chmod(filepath.Join(home, ".bashrc"), 0644)
	n.TerminalHash = ""
	// Removing just our exact marked block retains all user bytes and modes.
	_, dir = request()
	if e := x.changeNodeTerminal(ctx, n, "77777777-7777-4777-8777-777777777777", dir, true); e != nil {
		t.Fatal(e)
	}
	for file, raw := range original {
		actual, _ := os.ReadFile(filepath.Join(home, file))
		if !bytes.Equal(raw, actual) {
			t.Fatal("disconnect damaged existing content")
		}
	}
	if !nodeTerminalPlanMatches(n, dir, false) {
		t.Fatal("disconnect result unconfirmed")
	}
	t.Run("npmRegistry", func(t *testing.T) { testNodeRegistryLinux(t, x, n, d.Version, request) })
}
