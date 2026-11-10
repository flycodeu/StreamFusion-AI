package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Key IDs are stable retry identities. Only public material crosses the wire.
type GitKeyRequest struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	SSHHost string `json:"sshHost,omitempty"`
	SSHPort int    `json:"sshPort,omitempty"`
}
type GitKeyEvidence struct {
	ID              string `json:"id"`
	Account         string `json:"account"`
	AccountUID      string `json:"accountUid"`
	KeyPath         string `json:"keyPath"`
	PublicKey       string `json:"publicKey"`
	Fingerprint     string `json:"fingerprint"`
	HostPublicKey   string `json:"hostPublicKey,omitempty"`
	HostFingerprint string `json:"hostFingerprint,omitempty"`
}

func gitKeyPath(home, id string) string {
	return filepath.Join(home, ".local/share/streamfusion-ops/git-keys", id, "key")
}

var gitKeyScript = deployScript("git_key.sh")

func (x *executor) executeGitKey(req Request, output io.Writer) error {
	k := req.GitKey
	if k == nil || !ID.MatchString(k.ID) || !gitAccountPattern.MatchString(k.Account) || k.Account == "root" || runtime.GOOS != "linux" || req.GitRepository != nil || gitRequestConflict(req) || len(req.Secrets) != 0 || len(x.secrets) != 0 || req.Action != "GIT_KEY_PREPARE" && req.Action != "GIT_KEY_READ" && req.Action != "GIT_HOST_KEY_CHECK" {
		return errors.New("GIT_KEY_INPUT_INVALID")
	}
	a, err := user.Lookup(k.Account)
	if err != nil || a.Uid == "0" || !filepath.IsAbs(a.HomeDir) || a.HomeDir == "/" || filepath.Clean(a.HomeDir) != a.HomeDir {
		return errors.New("GIT_ACCOUNT_CHANGED")
	}
	uid, _ := strconv.Atoi(a.Uid)
	if gitOwnedPath(a.HomeDir, uid, true) != nil {
		return errors.New("GIT_PATH_UNSAFE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	generate := "no"
	if req.Action == "GIT_KEY_PREPARE" {
		generate = "yes"
	}
	evidence := GitKeyEvidence{ID: k.ID, Account: k.Account, AccountUID: a.Uid}
	if req.Action != "GIT_HOST_KEY_CHECK" {
		out, err := x.softwareRunner(ctx, "/usr/sbin/runuser", "-u", k.Account, "--", "/usr/bin/env", "-i", "--chdir="+a.HomeDir, "PATH=/usr/bin:/bin", "HOME="+a.HomeDir, "LC_ALL=C", "/bin/bash", "--noprofile", "--norc", "-c", gitKeyScript, "sfops-git-key", k.ID, a.Uid, generate)
		if err != nil || len(out) > 16<<10 {
			return errors.New("GIT_KEY_UNAVAILABLE")
		}
		key, _, options, rest, err := ssh.ParseAuthorizedKey(out)
		if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
			return errors.New("GIT_KEY_UNAVAILABLE")
		}
		evidence.KeyPath = gitKeyPath(a.HomeDir, k.ID)
		evidence.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		evidence.Fingerprint = ssh.FingerprintSHA256(key)
	}
	if req.Action == "GIT_HOST_KEY_CHECK" {
		if (!gitHostPattern.MatchString(k.SSHHost) && net.ParseIP(k.SSHHost) == nil) || k.SSHPort < 1 || k.SSHPort > 65535 {
			return errors.New("GIT_HOST_KEY_INVALID")
		}
		conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(k.SSHHost, strconv.Itoa(k.SSHPort)))
		if e != nil {
			return errors.New("GIT_HOST_KEY_UNAVAILABLE")
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		var hostKey ssh.PublicKey
		_, _, _, _ = ssh.NewClientConn(conn, k.SSHHost, &ssh.ClientConfig{User: "git", HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error { hostKey = key; return errors.New("KEY_CAPTURED") }})
		if hostKey == nil {
			return errors.New("GIT_HOST_KEY_UNAVAILABLE")
		}
		evidence.HostPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostKey)))
		evidence.HostFingerprint = ssh.FingerprintSHA256(hostKey)
	}
	return json.NewEncoder(output).Encode(evidence)
}

// Source paths must be an Ops-generated identity inside the selected HOME.
func gitTargetKeyValid(g GitRepositoryRequest, a *user.User) bool {
	if g.KeySource != "TARGET" {
		return true
	}
	id := filepath.Base(filepath.Dir(g.KeyPath))
	return ID.MatchString(id) && g.KeyPath == gitKeyPath(a.HomeDir, id)
}
