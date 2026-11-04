//go:build linux

package deploy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestGitRepositoryLinuxIsolatedLifecycle(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("SFOPS_TEST_NODE_LINUX") != "1" {
		t.Skip("requires isolated Linux account fixture with SFOPS_TEST_NODE_LINUX=1")
	}
	name := fmt.Sprintf("sfgit-%d-%d", os.Getpid(), time.Now().Unix()%100000)
	home := "/home/" + name
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("fixture home exists")
	}
	if out, err := exec.Command("/usr/sbin/useradd", "--create-home", "--skel", t.TempDir(), "--home-dir", home, "--shell", "/bin/bash", name).CombinedOutput(); err != nil {
		t.Fatalf("fixture account: %v %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("/usr/sbin/userdel", name).Run()
		if filepath.Clean(home) == "/home/"+name && strings.HasPrefix(name, "sfgit-") {
			_ = os.RemoveAll(home)
		}
	})
	a, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		out, e := exec.Command("/usr/sbin/runuser", append([]string{"-u", name, "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=" + home, "/usr/bin/git"}, args...)...).CombinedOutput()
		if e != nil {
			t.Fatalf("fixture git: %v %s", e, out)
		}
		return strings.TrimSpace(string(out))
	}
	repo, remote := filepath.Join(home, "repo"), filepath.Join(home, "remote.git")
	run("init", repo)
	run("init", "--bare", remote)
	g, key, signer := testGitRequest(t)
	g.Account = name
	g.AccountUID = a.Uid
	g.RepoPath = repo
	port, hostKey := gitFixtureSSH(t, name, remote, signer)
	g.SSHHost = "127.0.0.1"
	g.SSHPort = port
	run("-C", repo, "remote", "add", "origin", fmt.Sprintf("ssh://git@127.0.0.1:%d/project.git", port))
	run("-C", repo, "config", "user.name", "Original Name")
	run("-C", repo, "config", "user.email", "original@example.com")
	run("-C", repo, "config", "core.sshCommand", "ssh -F original-config")
	run("-C", repo, "config", "custom.keep", "unchanged")
	x := &executor{root: t.TempDir(), owner: Owner{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: "55555555-5555-4555-8555-555555555555", TargetID: "66666666-6666-4666-8666-666666666666"}, secrets: map[string]string{"gitKey": string(key)}}
	read := func(action string) GitRepositoryPreflight {
		t.Helper()
		var out bytes.Buffer
		if e := x.executeGitRead(Request{Action: action, GitRepository: &g}, &out); e != nil {
			t.Fatal(e)
		}
		var p GitRepositoryPreflight
		if json.Unmarshal(out.Bytes(), &p) != nil {
			t.Fatal("bad preflight")
		}
		return p
	}
	pre := read("GIT_REPOSITORY_CHECK")
	if !pre.Ready {
		t.Fatalf("check: %+v", pre)
	}
	g.ExpectedConfigHash = pre.Observation.ConfigHash
	g.ExpectedTerminalHash = pre.Observation.TerminalHash
	sequence := 0
	change := func() (Request, Result) {
		t.Helper()
		sequence++
		req := Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, JobID: fmt.Sprintf("77777777-7777-4777-8777-%012d", sequence), Action: "GIT_REPOSITORY_CHANGE", SoftwareOperation: "GIT_REPOSITORY_CHANGE", GitRepository: &g, DeadlineAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
		req.InputHash = Digest(req)
		var out bytes.Buffer
		if e := x.executeSoftware(req, &out); e != nil {
			t.Fatal(e)
		}
		var result Result
		if json.Unmarshal(out.Bytes(), &result) != nil || result.Status != "SUCCEEDED" {
			t.Fatalf("change: %s", out.String())
		}
		return req, result
	}
	ack := func(req Request, result Result) {
		t.Helper()
		req.Action = "ACK"
		req.ResultHash = Digest(result)
		var out bytes.Buffer
		if e := x.executeSoftware(req, &out); e != nil {
			t.Fatal(e)
		}
	}
	req, result := change()
	var again bytes.Buffer
	if err = x.executeSoftware(req, &again); err != nil {
		t.Fatal("idempotent repeat", err)
	}
	var replay Result
	_ = json.Unmarshal(again.Bytes(), &replay)
	if Digest(replay) != Digest(result) {
		t.Fatal("duplicate operation changed receipt")
	}
	ack(req, result)
	if run("-C", repo, "config", "user.name") != g.AuthorName || run("-C", repo, "config", "custom.keep") != "unchanged" {
		t.Fatal("binding changed unrelated configuration")
	}
	for _, file := range []string{"key", "known_hosts", "original.config"} {
		st, e := os.Stat(filepath.Join(gitPrivateDirectory(g, a), file))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("binding material not private", file, e)
		}
	}
	if p := read("GIT_REPOSITORY_TEST"); !p.Ready || p.Code != "GIT_REMOTE_READABLE" {
		t.Fatalf("remote read: %+v", p)
	}
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherKey)
	hostKey.Store(&otherSigner)
	if p := read("GIT_REPOSITORY_TEST"); p.Ready || p.Code != "GIT_REMOTE_READ_FAILED" {
		t.Fatalf("changed host key accepted: %+v", p)
	}
	// Changes outside the managed keys survive both observation and unbinding.
	run("-C", repo, "config", "custom.later", "preserve-me")
	g.Action = "UNBIND"
	pre = read("GIT_REPOSITORY_CHECK")
	if !pre.Ready {
		t.Fatalf("unbinding check: %+v", pre)
	}
	stale := g
	stale.ExpectedConfigHash = strings.Repeat("a", 64)
	if _, e := x.applyGitRepository(Request{GitRepository: &stale}, t.TempDir()); e == nil || e.Error() != "GIT_CONFIG_CHANGED" {
		t.Fatal("stale config accepted", e)
	}
	g.ExpectedConfigHash = pre.Observation.ConfigHash
	_, result = change()
	if result.GitRepository.Status != "UNBOUND" {
		t.Fatal("unbind status")
	}
	if run("-C", repo, "config", "user.name") != "Original Name" || run("-C", repo, "config", "core.sshCommand") != "ssh -F original-config" || run("-C", repo, "config", "custom.later") != "preserve-me" {
		t.Fatal("unbinding failed to preserve original keys and later unrelated edits")
	}
	if _, e := os.Lstat(filepath.Join(gitPrivateDirectory(g, a), "key")); !os.IsNotExist(e) {
		t.Fatal("private key survived unbinding")
	}
	// All durable controller-side files must remain free of the private key.
	_ = filepath.WalkDir(x.root, func(path string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			raw, _ := os.ReadFile(path)
			if bytes.Contains(raw, []byte("BEGIN OPENSSH PRIVATE KEY")) {
				t.Error("private key entered journal")
			}
		}
		return nil
	})
	// A symlink .git/config must never be followed even when its target is owned.
	config := filepath.Join(repo, ".git/config")
	real := filepath.Join(repo, ".git/config.saved")
	if err = os.Rename(config, real); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(real, config); err != nil {
		t.Fatal(err)
	}
	if _, e := resolveGitRepository(g); e == nil {
		t.Fatal("symlink config accepted")
	}
}

func gitFixtureSSH(t *testing.T, account, repo string, signer ssh.Signer) (int, *atomic.Pointer[ssh.Signer]) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	current := &atomic.Pointer[ssh.Signer]{}
	current.Store(&signer)
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				config := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					if metadata.User() != "git" || !bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
						return nil, fmt.Errorf("wrong identity")
					}
					return nil, nil
				}}
				config.AddHostKey(*current.Load())
				_, channels, requests, e := ssh.NewServerConn(conn, config)
				if e != nil {
					return
				}
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					ch, requests, e := channel.Accept()
					if e != nil {
						return
					}
					for request := range requests {
						var payload struct{ Command string }
						if request.Type != "exec" || ssh.Unmarshal(request.Payload, &payload) != nil || !strings.HasPrefix(payload.Command, "git-upload-pack ") {
							request.Reply(false, nil)
							continue
						}
						request.Reply(true, nil)
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						cmd := exec.CommandContext(ctx, "/usr/sbin/runuser", "-u", account, "--", "/usr/bin/git", "upload-pack", repo)
						cmd.Stdin = ch
						cmd.Stdout = ch
						cmd.Stderr = ch.Stderr()
						e = cmd.Run()
						cancel()
						status := uint32(0)
						if e != nil {
							status = 1
						}
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						ch.Close()
					}
				}
			}()
		}
	}()
	port, _ := strconv.Atoi(strings.Split(listener.Addr().String(), ":")[1])
	return port, current
}
