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
	"strings"
	"testing"
	"time"
)

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
	x := &executor{secrets: map[string]string{"gitToken": "fixture-token"}}
	g := GitAccountRequest{ID: "12345678-1234-4234-8234-123456789012", Version: "1", Account: name, AccountUID: a.Uid, AuthMethod: "HTTPS", Host: "git.example.test", Port: 443, Username: "first", CredentialID: "12345678-1234-4234-8234-123456789013", CredentialRevision: "1", AuthorName: "First Author", AuthorEmail: "first@example.test"}
	apply := func(action string) {
		t.Helper()
		var output bytes.Buffer
		if e := x.executeGitAccount(Request{Action: action, GitAccount: &g}, &output); e != nil {
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
	out, e := x.softwareRunner(ctx, "/usr/sbin/runuser", "-u", name, "--", "/usr/bin/env", "-i", "HOME="+home, "PATH=/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0", "/bin/bash", "-c", "printf 'protocol=https\\nhost=git.example.test\\nusername=first\\n\\n' | /usr/bin/git credential fill")
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
		t.Fatal(e)
	}
	var key GitKeyEvidence
	if json.Unmarshal(pub.Bytes(), &key) != nil {
		t.Fatal("bad public key")
	}
	g.AuthMethod, g.Username, g.Port, g.KeyPath, g.KeyFingerprint = "SSH", "git", 2222, key.KeyPath, key.Fingerprint
	apply("GIT_ACCOUNT_CONFIGURE")
	apply("GIT_ACCOUNT_CONFIGURE")
	out, e = x.softwareRunner(ctx, "/usr/sbin/runuser", "-u", name, "--", "/usr/bin/env", "-i", "HOME="+home, "PATH=/usr/bin:/bin", "/usr/bin/ssh", "-G", "ops-git-"+g.ID)
	if e != nil || !bytes.Contains(out, []byte("hostname git.example.test")) || !bytes.Contains(out, []byte("port 2222")) || !bytes.Contains(out, []byte(key.KeyPath)) {
		t.Fatal("SSH alias unavailable", e)
	}
	apply("GIT_ACCOUNT_REMOVE")
	if _, e = os.Stat(key.KeyPath); e != nil {
		t.Fatal("removing SSH config removed private key", e)
	}
}
