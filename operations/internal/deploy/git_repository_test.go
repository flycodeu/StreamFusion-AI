package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"os/user"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testGitRequest(t *testing.T) (GitRepositoryRequest, []byte, ssh.Signer) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	g := GitRepositoryRequest{Action: "BIND", BindingID: "11111111-1111-4111-8111-111111111111", BindingVersion: "1", ProfileID: "22222222-2222-4222-8222-222222222222", ProfileVersion: "1", CredentialID: "33333333-3333-4333-8333-333333333333", CredentialRevision: "1", Account: "fixture", AccountUID: "1001", RepoPath: "/home/fixture/repo", GitBinary: "/usr/bin/git", SSHHost: "git.example.com", SSHPort: 22, SSHUser: "git", HostPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), KeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey()), AuthorName: "Example User", AuthorEmail: "user@example.com"}
	return g, pem.EncodeToMemory(block), signer
}

func TestGitRepositoryValidationAndRemoteIdentity(t *testing.T) {
	g, _, _ := testGitRequest(t)
	if g.Validate() != nil {
		t.Fatal("valid input rejected")
	}
	for _, change := range []func(*GitRepositoryRequest){func(g *GitRepositoryRequest) { g.Account = "root" }, func(g *GitRepositoryRequest) { g.AccountUID = "0" }, func(g *GitRepositoryRequest) { g.RepoPath = "/home/fixture/../other" }, func(g *GitRepositoryRequest) { g.GitBinary = "/bin/bash" }, func(g *GitRepositoryRequest) { g.SSHHost = "-oProxyCommand=evil" }, func(g *GitRepositoryRequest) { g.AuthorName = "name\nextra" }, func(g *GitRepositoryRequest) { g.KeyFingerprint = "SHA256:bad\n" }, func(g *GitRepositoryRequest) { g.HostPublicKey = "command=evil " + g.HostPublicKey }} {
		bad := g
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid input accepted", bad.Account, bad.RepoPath)
		}
	}
	for _, remote := range []string{"git@git.example.com:team/repo.git", "ssh://git@git.example.com/team/repo.git"} {
		if !gitRemoteMatches(g, remote) {
			t.Fatal("valid remote rejected", remote)
		}
	}
	for _, remote := range []string{"https://git.example.com/team/repo.git", "git@other.example.com:repo.git", "ssh://git:token@git.example.com/repo.git", "ssh://git@git.example.com:2222/repo.git", "ext::danger", "file:///tmp/repo", "git@git.example.com:-option"} {
		if gitRemoteMatches(g, remote) {
			t.Fatal("foreign remote accepted", remote)
		}
	}
	command := gitSSHCommand(g, "/home/fixture/.local/share/streamfusion-ops/git/"+g.BindingID)
	for _, flag := range []string{"StrictHostKeyChecking=yes", "IdentitiesOnly=yes", "IdentityAgent=none", "GlobalKnownHostsFile=/dev/null", "BatchMode=yes"} {
		if !strings.Contains(command, flag) {
			t.Fatal("SSH protection missing", flag)
		}
	}
	if !strings.Contains(command, "'-F' '/dev/null'") {
		t.Fatal("SSH user configuration not isolated")
	}
	if !strings.HasPrefix(string(gitKnownHosts(g)), "git.example.com ssh-ed25519 ") {
		t.Fatal("host key identity not bound")
	}
}

func TestGitRepositoryJournalRejectsConflictingPayloadAndPreservesLegacy(t *testing.T) {
	g, _, _ := testGitRequest(t)
	g.ExpectedConfigHash = strings.Repeat("a", 64)
	r := Request{JobID: "44444444-4444-4444-8444-444444444444", Action: "GIT_REPOSITORY_CHANGE", SoftwareOperation: "GIT_REPOSITORY_CHANGE", GitRepository: &g}
	r.InputHash = Digest(r)
	if err := validateSoftwareInput(r); err != nil {
		t.Fatal(err)
	}
	r.NodeEnvironment = &NodeEnvironmentRequest{}
	if validateSoftwareInput(r) == nil {
		t.Fatal("conflicting Node payload accepted")
	}
	r.NodeEnvironment = nil
	r.GitRepository = nil
	if validateSoftwareInput(r) == nil {
		t.Fatal("missing Git payload accepted")
	}
	raw, _ := json.Marshal(Request{})
	if strings.Contains(string(raw), "gitRepository") {
		t.Fatal("legacy request JSON changed")
	}
}

func TestGitConfigUsesNativeParserAndRejectsIncludes(t *testing.T) {
	g, _, _ := testGitRequest(t)
	output := "user.name\nOriginal\x00user.name\nSecond\x00remote.origin.url\ngit@git.example.com:repo\x00"
	x := &executor{softwareRun: func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/sbin/runuser" || !strings.Contains(strings.Join(args, " "), "GIT_CONFIG_GLOBAL=/dev/null") || !strings.Contains(strings.Join(args, " "), "--no-includes") {
			t.Fatal("Git command not isolated")
		}
		return []byte(output), nil
	}}
	values, err := x.gitConfigValues(context.Background(), g, &user.User{HomeDir: "/home/fixture"}, "/home/fixture/repo/.git/config", "user.name")
	if err != nil || len(values) != 2 || values[1] != "Second" {
		t.Fatal(values, err)
	}
	output += "includeif.gitdir:~/other/.path\nother\x00"
	if _, err = x.gitConfigValues(context.Background(), g, &user.User{HomeDir: "/home/fixture"}, "config", "user.name"); err == nil || err.Error() != "GIT_CONFIG_INCLUDE_UNSUPPORTED" {
		t.Fatal("conditional include accepted", err)
	}
}
