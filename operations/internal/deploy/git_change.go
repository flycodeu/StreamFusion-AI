package deploy

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

var gitChangeScript = deployScript("git_change.sh")

type gitChangeMarker struct {
	JobID      string             `json:"jobId"`
	InputHash  string             `json:"inputHash"`
	BeforeHash string             `json:"beforeHash"`
	After      gitRepositoryState `json:"after"`
}

func (x *executor) gitBindingMatches(ctx context.Context, state gitRepositoryState, a *user.User) error {
	g := state.Request
	for key, expected := range map[string]string{"user.name": g.AuthorName, "user.email": g.AuthorEmail, "core.sshCommand": gitSSHCommand(g, gitPrivateDirectory(g, a))} {
		values, err := x.gitConfigValues(ctx, g, a, filepath.Join(g.RepoPath, ".git/config"), key)
		if err != nil || len(values) != 1 || values[0] != expected {
			return errors.New("GIT_BINDING_DRIFT")
		}
	}
	uid, _ := strconv.Atoi(a.Uid)
	private := gitPrivateDirectory(g, a)
	st, err := os.Stat(private)
	if gitOwnedPath(private, uid, true) != nil || err != nil || st.Mode().Perm() != 0700 {
		return errors.New("GIT_KEY_DRIFT")
	}
	for name, hash := range map[string]string{"key": state.KeyHash, "known_hosts": state.KnownHostsHash} {
		file := filepath.Join(gitPrivateDirectory(g, a), name)
		if name == "key" {
			file = gitPrivateKeyPath(g, a)
		}
		if gitOwnedPath(file, uid, false) != nil {
			return errors.New("GIT_KEY_DRIFT")
		}
		st, err := os.Stat(file)
		if err != nil || st.Mode().Perm() != 0600 || st.Size() > 32<<10 {
			return errors.New("GIT_KEY_DRIFT")
		}
		raw, err := os.ReadFile(file)
		if err != nil || nodeBytesSHA(raw) != hash {
			return errors.New("GIT_KEY_DRIFT")
		}
		if name == "key" {
			signer, e := ssh.ParsePrivateKey(raw)
			if e != nil || ssh.FingerprintSHA256(signer.PublicKey()) != g.KeyFingerprint {
				return errors.New("GIT_KEY_DRIFT")
			}
		}
	}
	return nil
}

func (x *executor) gitExistingBinding(ctx context.Context, g GitRepositoryRequest, a *user.User) (*gitRepositoryState, error) {
	files, err := filepath.Glob(filepath.Join(x.root, "git-repositories", "*.json"))
	if err != nil {
		return nil, errors.New("GIT_BINDING_DAMAGED")
	}
	var found *gitRepositoryState
	for _, file := range files {
		var old gitRepositoryState
		if readJSON(file, &old) != nil {
			return nil, errors.New("GIT_BINDING_DAMAGED")
		}
		if old.Request.BindingID == g.BindingID {
			if !gitSameRepository(old.Request, g) {
				return nil, errors.New("GIT_BINDING_IDENTITY_MISMATCH")
			}
			if old.Status == "BOUND" {
				if err := x.gitBindingMatches(ctx, old, a); err != nil {
					return nil, err
				}
			}
			copy := old
			found = &copy
		} else if old.Status == "BOUND" && old.Request.RepoPath == g.RepoPath {
			return nil, errors.New("GIT_REPOSITORY_ALREADY_BOUND")
		}
	}
	if g.Action == "UNBIND" && (found == nil || found.Status != "BOUND") {
		return nil, errors.New("GIT_BINDING_REQUIRED")
	}
	if found == nil {
		if _, err := os.Lstat(gitPrivateDirectory(g, a)); !os.IsNotExist(err) {
			return nil, errors.New("GIT_PRIVATE_PATH_CONFLICT")
		}
	}
	return found, nil
}

func gitPrivateStage(a *user.User) (string, error) {
	dir, err := os.MkdirTemp("/var/tmp", "sfops-git-")
	if err != nil {
		return "", err
	}
	uid, _ := strconv.Atoi(a.Uid)
	gid, _ := strconv.Atoi(a.Gid)
	if err = os.Chmod(dir, 0700); err == nil {
		err = os.Chown(dir, uid, gid)
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}
func gitStageFile(stage, name string, raw []byte, a *user.User) error {
	file := filepath.Join(stage, name)
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	// Explicit mode defeats inherited default ACL permissions before secret bytes.
	if err = f.Chmod(0600); err != nil {
		return err
	}
	uid, _ := strconv.Atoi(a.Uid)
	gid, _ := strconv.Atoi(a.Gid)
	if err = f.Chown(uid, gid); err != nil {
		return err
	}
	_, err = f.Write(raw)
	return err
}

func (x *executor) applyGitRepository(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	result.ErrorCode = "GIT_RESULT_UNCONFIRMED"
	g := *req.GitRepository
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	observation, err := x.observeGitRepository(ctx, g)
	if err != nil {
		return result, err
	}
	if g.AccountUID == "" || g.AccountUID != observation.AccountUID || g.ExpectedConfigHash == "" || g.ExpectedConfigHash != observation.ConfigHash {
		return result, errors.New("GIT_CONFIG_CHANGED")
	}
	a, err := resolveGitRepository(g)
	if err != nil {
		return result, err
	}
	old, err := x.gitExistingBinding(ctx, g, a)
	if err != nil {
		return result, err
	}
	if g.Action == "BIND" {
		plan, err := gitTerminalFiles(g, a)
		if err != nil {
			return result, err
		}
		if g.ExpectedTerminalHash == "" || Digest(plan) != g.ExpectedTerminalHash {
			return result, errors.New("GIT_TERMINAL_CHANGED")
		}
	}
	raw, err := gitReadConfig(g, a)
	if err != nil {
		return result, err
	}
	stage, err := gitPrivateStage(a)
	if err != nil {
		return result, errors.New("GIT_STAGE_FAILED")
	}
	defer os.RemoveAll(stage)
	if err = gitStageFile(stage, "config", raw, a); err != nil {
		return result, errors.New("GIT_STAGE_FAILED")
	}
	after := gitRepositoryState{Request: g, Original: map[string][]string{}, Status: "BOUND", JobID: req.JobID}
	if old != nil && old.Status == "BOUND" {
		after.Original = old.Original
	} else {
		for _, key := range gitManagedKeys {
			values, e := x.gitConfigValues(ctx, g, a, filepath.Join(stage, "config"), key)
			if e != nil {
				return result, e
			}
			after.Original[key] = values
		}
	}
	values := map[string][]string{"user.name": {g.AuthorName}, "user.email": {g.AuthorEmail}, "core.sshCommand": {gitSSHCommand(g, gitPrivateDirectory(g, a))}}
	if g.Action == "UNBIND" {
		values = after.Original
		after.Status = "UNBOUND"
	}
	for _, key := range gitManagedKeys {
		current, e := x.gitConfigValues(ctx, g, a, filepath.Join(stage, "config"), key)
		if e != nil {
			return result, e
		}
		if len(current) > 0 {
			if _, e = x.gitCommand(ctx, g, a, deployCommand("git-config-unset", filepath.Join(stage, "config"), key)...); e != nil {
				return result, e
			}
		}
		for _, value := range values[key] {
			if _, e = x.gitCommand(ctx, g, a, deployCommand("git-config-add", filepath.Join(stage, "config"), key, value)...); e != nil {
				return result, e
			}
		}
	}
	after.ConfigHash, err = fileSHA(filepath.Join(stage, "config"))
	if err != nil {
		return result, errors.New("GIT_STAGE_FAILED")
	}
	if g.Action == "BIND" {
		key := []byte(x.secrets["gitKey"])
		if g.KeySource == "TARGET" {
			if len(key) != 0 {
				return result, errors.New("GIT_KEY_IDENTITY_MISMATCH")
			}
			key, err = os.ReadFile(g.KeyPath)
			if err != nil {
				return result, errors.New("GIT_KEY_UNAVAILABLE")
			}
		}
		if len(key) > 32<<10 {
			return result, errors.New("GIT_KEY_IDENTITY_MISMATCH")
		}
		signer, e := ssh.ParsePrivateKey(key)
		if e != nil || ssh.FingerprintSHA256(signer.PublicKey()) != g.KeyFingerprint {
			return result, errors.New("GIT_KEY_IDENTITY_MISMATCH")
		}
		known := gitKnownHosts(g)
		after.KeyHash = nodeBytesSHA(key)
		after.KnownHostsHash = nodeBytesSHA(known)
		if g.KeySource != "TARGET" && gitStageFile(stage, "key", key, a) != nil {
			return result, errors.New("GIT_STAGE_FAILED")
		}
		if gitStageFile(stage, "known_hosts", known, a) != nil || gitStageFile(stage, "original.config", raw, a) != nil {
			return result, errors.New("GIT_STAGE_FAILED")
		}
	}
	marker := gitChangeMarker{req.JobID, req.InputHash, observation.ConfigHash, after}
	if err = atomicJSON(filepath.Join(dir, "git-change.json"), marker); err != nil {
		return result, err
	}
	// Every later failure is UNKNOWN. Recovery observes this intent and never
	// repeats config changes, key deployment or terminal edits.
	if g.Action == "BIND" {
		if err = x.connectGitTerminal(ctx, g, a, req.JobID, dir); err != nil {
			return result, nil
		}
	}
	st, err := os.Stat(filepath.Join(g.RepoPath, ".git/config"))
	if err != nil {
		return result, nil
	}
	previousKey, previousKnown, previousPath := "", "", ""
	if old != nil && old.Status == "BOUND" {
		previousKey = old.KeyHash
		previousKnown = old.KnownHostsHash
		previousPath = gitPrivateKeyPath(old.Request, a)
	}
	args := []string{"-u", g.Account, "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=" + a.HomeDir, "LC_ALL=C", "/bin/bash", "--noprofile", "--norc", "-c", gitChangeScript, "sfops-git", g.Action, g.RepoPath, g.AccountUID, observation.ConfigHash, after.ConfigHash, strconv.FormatUint(uint64(st.Mode().Perm()), 8), stage, gitPrivateDirectory(g, a), req.JobID, previousKey, previousKnown}
	args = append(args, g.KeySource, previousPath)
	if _, err = x.softwareRunner(ctx, "/usr/sbin/runuser", args...); err != nil {
		return result, nil
	}
	return x.reconcileGitRepository(req, dir), nil
}

func (x *executor) reconcileGitRepository(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "GIT_RESULT_UNCONFIRMED"
	var marker gitChangeMarker
	if readJSON(filepath.Join(dir, "git-change.json"), &marker) != nil || marker.JobID != req.JobID || marker.InputHash != req.InputHash || !reflect.DeepEqual(marker.After.Request, *req.GitRepository) {
		return result
	}
	g := marker.After.Request
	a, err := resolveGitRepository(g)
	if err != nil {
		return result
	}
	raw, err := gitReadConfig(g, a)
	if err != nil || nodeBytesSHA(raw) != marker.After.ConfigHash {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if g.Action == "BIND" {
		if x.gitBindingMatches(ctx, marker.After, a) != nil || !gitTerminalPlanApplied(g, a, dir) {
			return result
		}
	} else {
		for _, name := range []string{"key", "known_hosts"} {
			if _, e := os.Lstat(filepath.Join(gitPrivateDirectory(g, a), name)); !os.IsNotExist(e) {
				return result
			}
		}
		for _, key := range gitManagedKeys {
			got, e := x.gitConfigValues(ctx, g, a, filepath.Join(g.RepoPath, ".git/config"), key)
			if e != nil || !reflect.DeepEqual(got, marker.After.Original[key]) {
				return result
			}
		}
	}
	if err = os.MkdirAll(filepath.Dir(x.gitBindingFile(g.BindingID)), 0700); err != nil {
		return result
	}
	if err = atomicJSON(x.gitBindingFile(g.BindingID), marker.After); err != nil {
		return result
	}
	o, err := x.observeGitRepository(ctx, g)
	if err != nil {
		return result
	}
	result.Status = "SUCCEEDED"
	result.ErrorCode = ""
	result.Phase = "GIT_REPOSITORY_BOUND"
	result.GitRepository = o
	if g.Action == "UNBIND" {
		result.Phase = "GIT_REPOSITORY_UNBOUND"
	}
	return result
}

func (x *executor) verifyGitRepositoryResult(req Request, result Result) error {
	o := result.GitRepository
	if o == nil || o.BindingID != req.GitRepository.BindingID || o.AccountUID != req.GitRepository.AccountUID || o.RepoPath != req.GitRepository.RepoPath || !Hex.MatchString(o.ConfigHash) {
		return errors.New("GIT_RECEIPT_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	actual, err := x.observeGitRepository(ctx, *req.GitRepository)
	if err != nil || actual.ConfigHash != o.ConfigHash || actual.Status != o.Status {
		return errors.New("GIT_BINDING_DRIFT")
	}
	return nil
}

// A caller cannot smuggle a Git payload into a different software operation.
func validateGitRepositoryInput(req Request) error {
	if req.GitRepository == nil || req.GitRepository.Validate() != nil || req.SoftwareOperation != "GIT_REPOSITORY_CHANGE" || req.GitKey != nil || gitRequestConflict(req) || req.GitRepository.AccountUID == "" || !Hex.MatchString(req.GitRepository.ExpectedConfigHash) {
		return errors.New("GIT_REPOSITORY_INPUT_INVALID")
	}
	return nil
}
