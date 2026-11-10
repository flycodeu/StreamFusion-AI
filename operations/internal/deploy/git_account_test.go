package deploy

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitCredentialStoreReusesAndSeparatesAccountsWithoutNetwork(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git executable unavailable")
	}
	dir := t.TempDir()
	config := filepath.ToSlash(filepath.Join(dir, "gitconfig"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	run := func(input string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+config, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "HOME="+dir, "XDG_CONFIG_HOME="+dir)
		cmd.Stdin = strings.NewReader(input)
		return cmd.CombinedOutput()
	}
	for _, username := range []string{"first", "second"} {
		file := filepath.ToSlash(filepath.Join(dir, username+"-credentials"))
		if _, e := run("protocol=https\nhost=git.example.test\nusername="+username+"\npassword=fixture-"+username+"\n\n", "credential-store", "--file", file, "store"); e != nil {
			t.Fatal("credential store failed", e)
		}
		if _, e := run("", "config", "--global", "credential.https://"+username+"@git.example.test.helper", "store --file "+gitQuote(file)); e != nil {
			t.Fatal(e)
		}
		identity := filepath.ToSlash(filepath.Join(dir, username+".gitconfig"))
		if _, e := run("", "config", "--file", identity, "user.name", username); e != nil {
			t.Fatal(e)
		}
		if _, e := run("", "config", "--global", "includeIf.hasconfig:remote.*.url:https://"+username+"@git.example.test/**.path", identity); e != nil {
			t.Fatal(e)
		}
	}
	for _, username := range []string{"first", "second"} {
		out, e := run("protocol=https\nhost=git.example.test\nusername="+username+"\n\n", "credential", "fill")
		if e != nil || !bytes.Contains(out, []byte("password=fixture-"+username)) {
			t.Fatal("stored credential not reused for requested account", e)
		}
		repo := filepath.Join(dir, username+"-repo")
		if _, e = run("", "init", repo); e != nil {
			t.Fatal(e)
		}
		if _, e = run("", "-C", repo, "remote", "add", "origin", "https://"+username+"@git.example.test/team/project.git"); e != nil {
			t.Fatal(e)
		}
		identity, e := run("", "-C", repo, "config", "user.name")
		if e != nil || strings.TrimSpace(string(identity)) != username {
			t.Fatal("commit identity was not selected by remote URL", e)
		}
	}
	out, _ := run("protocol=https\nhost=other.example.test\nusername=first\n\n", "credential", "fill")
	if bytes.Contains(out, []byte("fixture-")) {
		t.Fatal("token escaped configured host")
	}
}
