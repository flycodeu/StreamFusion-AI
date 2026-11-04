package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var gitManagedKeys = []string{"user.name", "user.email", "core.sshCommand"}

type gitRepositoryState struct {
	Request        GitRepositoryRequest `json:"request"`
	Original       map[string][]string  `json:"original"`
	ConfigHash     string               `json:"configHash"`
	KeyHash        string               `json:"keyHash"`
	KnownHostsHash string               `json:"knownHostsHash"`
	Status         string               `json:"status"`
	JobID          string               `json:"jobId"`
}

func (x *executor) gitBindingFile(id string) string {
	return filepath.Join(x.root, "git-repositories", id+".json")
}
func gitPrivateDirectory(g GitRepositoryRequest, account *user.User) string {
	return filepath.Join(account.HomeDir, ".local/share/streamfusion-ops/git", g.BindingID)
}
func gitQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func gitOwnedPath(name string, uid int, directory bool) error {
	for current := name; current != "/"; current = filepath.Dir(current) {
		st, err := os.Lstat(current)
		if err != nil || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0022 != 0 {
			return errors.New("GIT_PATH_UNSAFE")
		}
		owner, _ := nativeFileOwner(st)
		if current == name {
			if owner != uid || st.IsDir() != directory || !directory && !st.Mode().IsRegular() {
				return errors.New("GIT_PATH_UNSAFE")
			}
		} else if !st.IsDir() || owner != 0 && owner != uid {
			return errors.New("GIT_PATH_UNSAFE")
		}
	}
	return nil
}

func resolveGitRepository(g GitRepositoryRequest) (*user.User, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("GIT_LINUX_REQUIRED")
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	a, err := user.Lookup(g.Account)
	if err != nil || a.Uid == "0" || a.HomeDir == "/" || !filepath.IsAbs(a.HomeDir) || g.AccountUID != "" && g.AccountUID != a.Uid {
		return nil, errors.New("GIT_ACCOUNT_CHANGED")
	}
	uid, err := strconv.Atoi(a.Uid)
	if err != nil {
		return nil, errors.New("GIT_ACCOUNT_CHANGED")
	}
	for _, p := range []string{a.HomeDir, g.RepoPath, filepath.Join(g.RepoPath, ".git")} {
		if err := gitOwnedPath(p, uid, true); err != nil {
			return nil, err
		}
	}
	for _, p := range []string{"config", "HEAD"} {
		if err := gitOwnedPath(filepath.Join(g.RepoPath, ".git", p), uid, false); err != nil {
			return nil, err
		}
	}
	for directory := gitPrivateDirectory(g, a); directory != filepath.Clean(a.HomeDir); directory = filepath.Dir(directory) {
		if _, err := os.Lstat(directory); os.IsNotExist(err) {
			continue
		}
		if err := gitOwnedPath(directory, uid, true); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"commondir", "gitdir", "config.worktree"} {
		if _, err := os.Lstat(filepath.Join(g.RepoPath, ".git", name)); !os.IsNotExist(err) {
			return nil, errors.New("GIT_WORKTREE_UNSUPPORTED")
		}
	}
	binary, err := filepath.EvalSymlinks(g.GitBinary)
	if err != nil || secureAncestors(binary) != nil {
		return nil, errors.New("GIT_BINARY_UNTRUSTED")
	}
	return a, nil
}

func (x *executor) gitCommand(ctx context.Context, g GitRepositoryRequest, a *user.User, args ...string) ([]byte, error) {
	argv := []string{"-u", g.Account, "--", "/usr/bin/env", "-i", "HOME=" + a.HomeDir, "PATH=" + filepath.Dir(g.GitBinary) + ":/usr/bin:/bin", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", g.GitBinary, "-C", a.HomeDir}
	out, err := x.softwareRunner(ctx, "/usr/sbin/runuser", append(argv, args...)...)
	if len(out) >= 64<<10 {
		return nil, errors.New("GIT_OUTPUT_LIMIT")
	}
	if err != nil {
		return nil, errors.New("GIT_COMMAND_FAILED")
	}
	return out, nil
}

func (x *executor) gitConfigValues(ctx context.Context, g GitRepositoryRequest, a *user.User, file, key string) ([]string, error) {
	// --list avoids treating exit 1 (missing key) as indistinguishable from a
	// process/permission failure. Git's native parser handles escaping and repeats.
	out, err := x.gitCommand(ctx, g, a, "config", "--file", file, "--no-includes", "--null", "--list")
	if err != nil {
		return nil, err
	}
	var values []string
	for _, entry := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		name, value, _ := strings.Cut(entry, "\n")
		if lower := strings.ToLower(name); strings.HasPrefix(lower, "include.") || strings.HasPrefix(lower, "includeif.") {
			return nil, errors.New("GIT_CONFIG_INCLUDE_UNSUPPORTED")
		}
		if strings.EqualFold(name, key) {
			values = append(values, value)
		}
	}
	return values, nil
}

func gitRemoteMatches(g GitRepositoryRequest, remote string) bool {
	if len(remote) > 2048 || strings.ContainsAny(remote, "\x00\r\n\t ") {
		return false
	}
	if strings.HasPrefix(remote, "ssh://") {
		u, err := url.Parse(remote)
		if err != nil || u.User == nil || u.User.Username() != g.SSHUser || !strings.EqualFold(u.Hostname(), g.SSHHost) || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		if _, present := u.User.Password(); present {
			return false
		}
		port := strconv.Itoa(g.SSHPort)
		if u.Port() != "" {
			port = u.Port()
		}
		return port == strconv.Itoa(g.SSHPort) && len(u.Path) > 1 && !strings.ContainsAny(u.Path, "\x00\r\n")
	}
	prefix := g.SSHUser + "@" + g.SSHHost + ":"
	return strings.HasPrefix(remote, prefix) && len(remote) > len(prefix) && remote[len(prefix)] != '-'
}

func gitKnownHosts(g GitRepositoryRequest) []byte {
	key, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(g.HostPublicKey))
	host := g.SSHHost
	if g.SSHPort != 22 || strings.Contains(host, ":") {
		host = "[" + host + "]:" + strconv.Itoa(g.SSHPort)
	}
	return []byte(host + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + "\n")
}

func gitSSHCommand(g GitRepositoryRequest, directory string) string {
	args := []string{"/usr/bin/ssh", "-F", "/dev/null", "-o", "StrictHostKeyChecking=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "BatchMode=yes", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "UpdateHostKeys=no", "-o", "GlobalKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10", "-o", "UserKnownHostsFile=" + filepath.Join(directory, "known_hosts"), "-o", "HostName=" + g.SSHHost, "-p", strconv.Itoa(g.SSHPort), "-l", g.SSHUser, "-i", filepath.Join(directory, "key")}
	for i := range args {
		args[i] = gitQuote(args[i])
	}
	return strings.Join(args, " ")
}

func (x *executor) observeGitRepository(ctx context.Context, g GitRepositoryRequest) (*GitRepositoryObservation, error) {
	a, err := resolveGitRepository(g)
	if err != nil {
		return nil, err
	}
	g.AccountUID = a.Uid
	file := filepath.Join(g.RepoPath, ".git/config")
	raw, err := gitReadConfig(g, a)
	if err != nil {
		return nil, err
	}
	bare, err := x.gitConfigValues(ctx, g, a, file, "core.bare")
	if err != nil {
		return nil, err
	}
	if len(bare) > 0 && !strings.EqualFold(bare[len(bare)-1], "false") {
		return nil, errors.New("GIT_REPOSITORY_UNSUPPORTED")
	}
	remote, err := x.gitConfigValues(ctx, g, a, file, "remote.origin.url")
	if err != nil || len(remote) != 1 || !gitRemoteMatches(g, remote[0]) {
		return nil, errors.New("GIT_REMOTE_IDENTITY_MISMATCH")
	}
	version, err := x.gitCommand(ctx, g, a, "--version")
	if err != nil || !strings.HasPrefix(string(version), "git version ") || len(version) > 200 {
		return nil, errors.New("GIT_BINARY_UNTRUSTED")
	}
	head, err := os.ReadFile(filepath.Join(g.RepoPath, ".git/HEAD"))
	if err != nil || len(head) > 1024 {
		return nil, errors.New("GIT_REPOSITORY_UNSUPPORTED")
	}
	branch := ""
	if strings.HasPrefix(string(head), "ref: refs/heads/") {
		branch = strings.TrimSpace(strings.TrimPrefix(string(head), "ref: refs/heads/"))
	}
	hostKey, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(g.HostPublicKey))
	o := &GitRepositoryObservation{BindingID: g.BindingID, BindingVersion: g.BindingVersion, ProfileID: g.ProfileID, ProfileVersion: g.ProfileVersion, Account: g.Account, AccountUID: a.Uid, RepoPath: g.RepoPath, ConfigHash: nodeBytesSHA(raw), RemoteURL: remote[0], Branch: branch, GitVersion: strings.TrimSpace(strings.TrimPrefix(string(version), "git version ")), KeyFingerprint: g.KeyFingerprint, KeyPath: filepath.Join(gitPrivateDirectory(g, a), "key"), HostKeyFingerprint: ssh.FingerprintSHA256(hostKey), AuthorName: g.AuthorName, AuthorEmail: g.AuthorEmail, Status: "UNBOUND", ObservedAt: stamp()}
	if g.Action == "BIND" {
		plan, e := gitTerminalFiles(g, a)
		if e != nil {
			return nil, e
		}
		o.TerminalHash = Digest(plan)
	}
	var bound gitRepositoryState
	if err := readJSON(x.gitBindingFile(g.BindingID), &bound); err == nil && bound.Status == "BOUND" {
		if !gitSameRepository(bound.Request, g) || x.gitBindingMatches(ctx, bound, a) != nil {
			return nil, errors.New("GIT_BINDING_DRIFT")
		}
		o.Status = "BOUND"
	} else if err != nil && !os.IsNotExist(err) {
		return nil, errors.New("GIT_BINDING_DAMAGED")
	}
	return o, nil
}

func gitReadConfig(g GitRepositoryRequest, a *user.User) ([]byte, error) {
	uid, _ := strconv.Atoi(a.Uid)
	name := filepath.Join(g.RepoPath, ".git/config")
	if err := gitOwnedPath(name, uid, false); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(filepath.Join(g.RepoPath, ".git"))
	if err != nil {
		return nil, errors.New("GIT_CONFIG_UNREADABLE")
	}
	defer r.Close()
	f, err := r.Open("config")
	if err != nil {
		return nil, errors.New("GIT_CONFIG_UNREADABLE")
	}
	defer f.Close()
	st, e := f.Stat()
	original, le := os.Lstat(name)
	if e != nil || le != nil || !os.SameFile(st, original) || st.Size() > 128<<10 {
		return nil, errors.New("GIT_CONFIG_UNREADABLE")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 128<<10+1))
	if err != nil || len(raw) > 128<<10 {
		return nil, errors.New("GIT_CONFIG_UNREADABLE")
	}
	return raw, nil
}

func gitSameRepository(a, b GitRepositoryRequest) bool {
	return a.BindingID == b.BindingID && a.Account == b.Account && a.AccountUID == b.AccountUID && a.RepoPath == b.RepoPath
}

func (x *executor) executeGitRead(req Request, output io.Writer) error {
	if req.GitRepository == nil || req.GitRepository.Validate() != nil || gitRequestConflict(req) {
		return errors.New("GIT_REPOSITORY_INPUT_INVALID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	g := *req.GitRepository
	o, err := x.observeGitRepository(ctx, g)
	if err == nil && req.Action == "GIT_REPOSITORY_CHECK" {
		g.AccountUID = o.AccountUID
		a, e := resolveGitRepository(g)
		if e != nil {
			err = e
		} else {
			_, err = x.gitExistingBinding(ctx, g, a)
		}
	}
	pre := GitRepositoryPreflight{Ready: err == nil, Code: "GIT_REPOSITORY_READY", Message: "仓库已确认", Observation: o}
	if err == nil && req.Action == "GIT_REPOSITORY_TEST" {
		var bound gitRepositoryState
		if readJSON(x.gitBindingFile(g.BindingID), &bound) != nil || bound.Status != "BOUND" || !gitSameRepository(bound.Request, g) || bound.Request.KeyFingerprint != g.KeyFingerprint || bound.Request.HostPublicKey != g.HostPublicKey {
			err = errors.New("GIT_BINDING_REQUIRED")
		} else {
			a, _ := resolveGitRepository(g)
			neutral, e := os.MkdirTemp("/var/tmp", "sfops-git-test-")
			if e != nil {
				err = errors.New("GIT_TEST_UNAVAILABLE")
			} else {
				defer os.RemoveAll(neutral)
				_ = os.Chmod(neutral, 0755)
				_, err = x.gitCommand(ctx, g, a, "-C", neutral, "-c", "core.sshCommand="+gitSSHCommand(bound.Request, gitPrivateDirectory(g, a)), "-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never", "ls-remote", "--refs", "--", o.RemoteURL)
				if err != nil {
					err = errors.New("GIT_REMOTE_READ_FAILED")
				} else {
					pre.Code = "GIT_REMOTE_READABLE"
					pre.Message = "远端仓库可读取"
				}
			}
		}
	}
	if err != nil {
		pre.Ready = false
		pre.Code = err.Error()
		pre.Message = "仓库检查未通过，请检查账号、目录与SSH身份"
	}
	return json.NewEncoder(output).Encode(pre)
}

func gitRequestConflict(r Request) bool {
	return r.Software != nil || r.Installation != nil || r.NodeEnvironment != nil || r.DockerAccess != nil || r.Uninstallation != nil || r.Reconfiguration != nil || r.Firewall != nil || r.Plugin != nil || r.Candidate != nil || r.Baseline != nil || r.Binding != nil
}
