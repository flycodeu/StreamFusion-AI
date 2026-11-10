//go:build linux

package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGitAccountDefaultACLPreservesGlobalConfiguration(t *testing.T) {
	acl, err := exec.LookPath("setfacl")
	if err != nil {
		t.Skip("default ACL tooling unavailable")
	}
	for _, mode := range []os.FileMode{0, 0600, 0644, 0664} {
		t.Run(fmt.Sprintf("original-mode-%04o", mode), func(t *testing.T) {
			home := t.TempDir()
			if out, err := exec.Command(acl, "-m", "d:u::rwx,d:g::rwx,d:o::rx", home).CombinedOutput(); err != nil {
				t.Fatalf("fixture default ACL: %v %s", err, out)
			}
			global, stage := filepath.Join(home, ".gitconfig"), filepath.Join(home, "stage")
			original := []byte("[custom]\n\tkeep = original\n")
			if mode != 0 {
				if err := os.WriteFile(global, original, mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(global, mode); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "credential"), []byte("protocol=https\nhost=git.example.test\nusername=first\npassword=fixture-token\n\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "identity.gitconfig"), []byte("[user]\nname = Fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(home, ".local/share/streamfusion-ops/git-accounts", "11111111-1111-4111-8111-111111111111")
			run := func(action string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return exec.CommandContext(ctx, "/usr/bin/env", "-i", "--chdir="+home, "HOME="+home, "PATH=/usr/bin:/bin", "LC_ALL=C", "/bin/bash", "--noprofile", "--norc", "-c", gitAccountScript, "sfops-account-acl-fixture", action, directory, stage, strconv.Itoa(os.Getuid()), "HTTPS", "credential.https://first@git.example.test.helper", "store --file "+gitQuote(filepath.Join(directory, "credentials")), "1", "includeIf.hasconfig:remote.*.url:https://first@git.example.test/**.path", "yes").CombinedOutput()
			}
			if mode == 0664 {
				if _, err := run("GIT_ACCOUNT_CONFIGURE"); err == nil {
					t.Fatal("existing unsafe global config accepted")
				}
				data, err := os.ReadFile(global)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatal("unsafe existing contents changed", err)
				}
				info, err := os.Stat(global)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatal("unsafe existing mode changed", err)
				}
				return
			}
			for _, action := range []string{"GIT_ACCOUNT_CONFIGURE", "GIT_ACCOUNT_CONFIGURE", "GIT_ACCOUNT_REMOVE"} {
				if out, err := run(action); err != nil {
					t.Fatalf("%s: %v %s", action, err, strings.ReplaceAll(string(out), "fixture-token", "[redacted]"))
				}
				info, err := os.Stat(global)
				expected := mode
				if expected == 0 {
					expected = 0600
				}
				if err != nil || info.Mode().Perm() != expected {
					t.Fatal("global config mode changed", err)
				}
				data, err := os.ReadFile(global)
				if err != nil || bytes.Contains(data, []byte("fixture-token")) || mode != 0 && !bytes.Contains(data, []byte("keep = original")) {
					t.Fatal("global config contents changed or token leaked", err)
				}
			}
			if _, err := os.Stat(filepath.Join(directory, "credentials")); !os.IsNotExist(err) {
				t.Fatal("credential survived removal", err)
			}
		})
	}
}

// Keep protected CI failures actionable without substituting a fake command
// runner or publishing credential material from even an isolated fixture.
func gitFixtureDiagnostics(t *testing.T, x *executor) func() {
	t.Helper()
	var last []byte
	var lastError error
	x.softwareRun = func(limit time.Duration, command string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		last, lastError = (&executor{}).softwareRunner(ctx, command, args...)
		return last, lastError
	}
	return func() {
		value := strings.ReplaceAll(string(last), "fixture-token", "[redacted]")
		if strings.Contains(value, "PRIVATE KEY") {
			value = "[private material redacted]"
		}
		t.Logf("isolated Git command: err=%v output=%q", lastError, value)
	}
}

func TestGitAccountLinuxTerminalConfiguration(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("SFOPS_TEST_NODE_LINUX") != "1" {
		t.Skip("requires isolated Linux account fixture")
	}
	name := fmt.Sprintf("sfgita-%d-%d", os.Getpid(), time.Now().Unix()%100000)
	home := "/home/" + name
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("fixture home exists")
	}
	if out, err := exec.Command("/usr/sbin/useradd", "--create-home", "--skel", t.TempDir(), "--home-dir", home, "--shell", "/bin/bash", name).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	t.Cleanup(func() {
		_ = exec.Command("/usr/sbin/userdel", name).Run()
		if filepath.Clean(home) == "/home/"+name && strings.HasPrefix(name, "sfgita-") {
			_ = os.RemoveAll(home)
		}
	})
	a, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	// The executor's working directory may be private to root. Terminal
	// operations must start in the already validated target account HOME.
	t.Chdir(t.TempDir())
	x := &executor{secrets: map[string]string{"gitToken": "fixture-token"}}
	diagnostics := gitFixtureDiagnostics(t, x)
	g := GitAccountRequest{ID: "12345678-1234-4234-8234-123456789012", Version: "1", Account: name, AccountUID: a.Uid, AuthMethod: "HTTPS", Host: "git.example.test", Port: 443, Username: "first", CredentialID: "12345678-1234-4234-8234-123456789013", CredentialRevision: "1", AuthorName: "First Author", AuthorEmail: "first@example.test"}
	apply := func(action string) {
		t.Helper()
		var output bytes.Buffer
		if e := x.executeGitAccount(Request{Action: action, GitAccount: &g}, &output); e != nil {
			diagnostics()
			t.Fatal(e)
		}
		if strings.Contains(output.String(), "fixture-token") {
			t.Fatal("token leaked in evidence")
		}
	}
	apply("GIT_ACCOUNT_CONFIGURE")
	apply("GIT_ACCOUNT_CONFIGURE")
	path := filepath.Join(home, ".local/share/streamfusion-ops/git-accounts", g.ID, "credentials")
	if st, e := os.Stat(path); e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("credential permissions", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, e := x.softwareRunner(ctx, "/usr/sbin/runuser", "-u", name, "--", "/usr/bin/env", "-i", "--chdir="+home, "HOME="+home, "PATH=/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0", "/bin/bash", "-c", "printf 'protocol=https\\nhost=git.example.test\\nusername=first\\n\\n' | /usr/bin/git credential fill")
	if e != nil || !bytes.Contains(out, []byte("password=fixture-token")) {
		t.Fatal("terminal did not reuse configured token", e)
	}
	apply("GIT_ACCOUNT_REMOVE")
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("credential survived removal")
	}
	x.secrets = nil
	k := GitKeyRequest{ID: g.ID, Account: name}
	var pub bytes.Buffer
	if e = x.executeGitKey(Request{Action: "GIT_KEY_PREPARE", GitKey: &k}, &pub); e != nil {
		diagnostics()
		t.Fatal(e)
	}
	var key GitKeyEvidence
	if json.Unmarshal(pub.Bytes(), &key) != nil {
		t.Fatal("bad public key")
	}
	g.AuthMethod, g.Username, g.Port, g.KeyPath, g.KeyFingerprint = "SSH", "git", 2222, key.KeyPath, key.Fingerprint
	apply("GIT_ACCOUNT_CONFIGURE")
	apply("GIT_ACCOUNT_CONFIGURE")
	out, e = x.softwareRunner(ctx, "/usr/sbin/runuser", "-u", name, "--", "/usr/bin/env", "-i", "--chdir="+home, "HOME="+home, "PATH=/usr/bin:/bin", "/usr/bin/ssh", "-G", "ops-git-"+g.ID)
	if e != nil || !bytes.Contains(out, []byte("hostname git.example.test")) || !bytes.Contains(out, []byte("port 2222")) || !bytes.Contains(out, []byte(key.KeyPath)) {
		t.Fatal("SSH alias unavailable", e)
	}
	apply("GIT_ACCOUNT_REMOVE")
	if _, e = os.Stat(key.KeyPath); e != nil {
		t.Fatal("removing SSH config removed private key", e)
	}
}
