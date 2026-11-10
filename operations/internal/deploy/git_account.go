package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type GitAccountRequest struct {
	CredentialID       string `json:"credentialId,omitempty"`
	CredentialRevision string `json:"credentialRevision,omitempty"`
	ID                 string `json:"id"`
	Version            string `json:"version"`
	Account            string `json:"account"`
	AccountUID         string `json:"accountUid,omitempty"`
	AuthMethod         string `json:"authMethod"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	KeyPath            string `json:"keyPath,omitempty"`
	KeyFingerprint     string `json:"keyFingerprint,omitempty"`
	HostPublicKey      string `json:"hostPublicKey,omitempty"`
	AuthorName         string `json:"authorName,omitempty"`
	AuthorEmail        string `json:"authorEmail,omitempty"`
}

var gitLoginPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)

type GitAccountEvidence struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Account      string `json:"account"`
	AccountUID   string `json:"accountUid"`
	Status       string `json:"status"`
	SSHHostAlias string `json:"sshHostAlias,omitempty"`
	ObservedAt   string `json:"observedAt"`
}

func (g GitAccountRequest) Validate() error {
	if len(g.AuthorName) > 200 || len(g.AuthorEmail) > 254 || strings.ContainsAny(g.AuthorName+g.AuthorEmail, "\x00\r\n") {
		return errors.New("GIT_ACCOUNT_INPUT_INVALID")
	}
	if g.HostPublicKey != "" {
		_, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(g.HostPublicKey))
		if len(g.HostPublicKey) > 16<<10 || e != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
			return errors.New("GIT_HOST_KEY_INVALID")
		}
	}
	if !ID.MatchString(g.ID) || !gitRevisionPattern.MatchString(g.Version) || !gitAccountPattern.MatchString(g.Account) || g.Account == "root" || !gitHostPattern.MatchString(g.Host) || g.Port < 1 || g.Port > 65535 || g.AuthMethod != "HTTPS" && g.AuthMethod != "SSH" || g.AuthMethod == "HTTPS" && (!gitLoginPattern.MatchString(g.Username) || g.KeyPath != "" || !ID.MatchString(g.CredentialID) || !gitRevisionPattern.MatchString(g.CredentialRevision)) || g.AuthMethod == "SSH" && (!gitAccountPattern.MatchString(g.Username) || !gitFingerprintPattern.MatchString(g.KeyFingerprint)) {
		return errors.New("GIT_ACCOUNT_INPUT_INVALID")
	}
	return nil
}

func (x *executor) executeGitAccount(req Request, output io.Writer) error {
	g := req.GitAccount
	if runtime.GOOS != "linux" || g == nil || g.Validate() != nil || req.GitKey != nil || req.GitRepository != nil || gitRequestConflict(req) || req.Action != "GIT_ACCOUNT_CONFIGURE" && req.Action != "GIT_ACCOUNT_REMOVE" {
		return errors.New("GIT_ACCOUNT_INPUT_INVALID")
	}
	a, err := user.Lookup(g.Account)
	if err != nil || a.Uid == "0" || !filepath.IsAbs(a.HomeDir) || a.HomeDir == "/" || filepath.Clean(a.HomeDir) != a.HomeDir || strings.ContainsAny(a.HomeDir, "\x00\r\n") || g.AccountUID != "" && a.Uid != g.AccountUID {
		return errors.New("GIT_ACCOUNT_CHANGED")
	}
	uid, uidError := strconv.Atoi(a.Uid)
	if uidError != nil || uid <= 0 {
		return errors.New("GIT_ACCOUNT_CHANGED")
	}
	if gitOwnedPath(a.HomeDir, uid, true) != nil {
		return errors.New("GIT_PATH_UNSAFE")
	}
	directory := filepath.Join(a.HomeDir, ".local/share/streamfusion-ops/git-accounts", g.ID)
	for p := directory; p != a.HomeDir; p = filepath.Dir(p) {
		if _, e := os.Lstat(p); os.IsNotExist(e) {
			continue
		}
		if gitOwnedPath(p, uid, true) != nil {
			return errors.New("GIT_PATH_UNSAFE")
		}
	}
	for _, file := range []string{filepath.Join(a.HomeDir, ".gitconfig"), filepath.Join(a.HomeDir, ".ssh/config")} {
		if _, e := os.Lstat(file); os.IsNotExist(e) {
			continue
		}
		if gitOwnedPath(file, uid, false) != nil {
			return errors.New("GIT_PATH_UNSAFE")
		}
	}
	if _, e := os.Lstat(filepath.Join(a.HomeDir, ".ssh")); e == nil && gitOwnedPath(filepath.Join(a.HomeDir, ".ssh"), uid, true) != nil {
		return errors.New("GIT_PATH_UNSAFE")
	}
	stage, err := gitPrivateStage(a)
	if err != nil {
		return errors.New("GIT_STAGE_FAILED")
	}
	defer os.RemoveAll(stage)
	authority := g.Host
	if g.Port != 443 {
		authority += ":" + strconv.Itoa(g.Port)
	}
	key := "credential.https://" + g.Username + "@" + authority + ".helper"
	helper := deployTemplate("git_account_credential_helper.command.tmpl", gitQuote(filepath.Join(directory, "credentials")))
	alias := "ops-git-" + g.ID
	remotePattern := "https://" + g.Username + "@" + authority + "/**"
	if g.AuthMethod == "SSH" {
		remotePattern = g.Username + "@" + alias + ":**"
	}
	identityKey := "includeIf.hasconfig:remote.*.url:" + remotePattern + ".path"
	withIdentity := "no"
	if req.Action == "GIT_ACCOUNT_CONFIGURE" {
		if g.AuthorName != "" || g.AuthorEmail != "" {
			withIdentity = "yes"
			identity := deployScript("git_account_user.conf")
			if g.AuthorName != "" {
				identity += deployTemplate("git_account_identity_value.conf.tmpl", "name", strconv.Quote(g.AuthorName))
			}
			if g.AuthorEmail != "" {
				identity += deployTemplate("git_account_identity_value.conf.tmpl", "email", strconv.Quote(g.AuthorEmail))
			}
			if gitStageFile(stage, "identity.gitconfig", []byte(identity), a) != nil {
				return errors.New("GIT_STAGE_FAILED")
			}
		}
		if g.AuthMethod == "HTTPS" {
			token := x.secrets["gitToken"]
			if token == "" || len(token) > 32<<10 || strings.ContainsAny(token, "\x00\r\n") {
				return errors.New("GIT_CREDENTIAL_UNAVAILABLE")
			}
			body := deployTemplate("git_account_credential.tmpl", authority, g.Username, token)
			if gitStageFile(stage, "credential", []byte(body), a) != nil {
				return errors.New("GIT_STAGE_FAILED")
			}
		} else {
			if !gitTargetKeyValid(GitRepositoryRequest{KeySource: "TARGET", KeyPath: g.KeyPath}, a) || gitOwnedPath(g.KeyPath, uid, false) != nil {
				return errors.New("GIT_KEY_UNAVAILABLE")
			}
			st, e := os.Stat(g.KeyPath)
			if e != nil || st.Size() > 32<<10 || st.Mode().Perm() != 0600 {
				return errors.New("GIT_KEY_UNAVAILABLE")
			}
			raw, e := os.ReadFile(g.KeyPath)
			if e != nil {
				return errors.New("GIT_KEY_UNAVAILABLE")
			}
			signer, e := ssh.ParsePrivateKey(raw)
			if e != nil || ssh.FingerprintSHA256(signer.PublicKey()) != g.KeyFingerprint {
				return errors.New("GIT_KEY_IDENTITY_MISMATCH")
			}
			body := deployTemplate("git_account_ssh.conf.tmpl", alias, g.Host, g.Port, g.Username, strconv.Quote(g.KeyPath))
			if g.HostPublicKey != "" {
				body = strings.Replace(body, "\nHost *\n", deployTemplate("git_account_known_hosts.conf.tmpl", strconv.Quote(filepath.Join(directory, "known_hosts"))), 1)
				if gitStageFile(stage, "known_hosts", gitKnownHosts(GitRepositoryRequest{SSHHost: g.Host, SSHPort: g.Port, HostPublicKey: g.HostPublicKey}), a) != nil {
					return errors.New("GIT_STAGE_FAILED")
				}
			}
			if gitStageFile(stage, "ssh.conf", []byte(body), a) != nil {
				return errors.New("GIT_STAGE_FAILED")
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := []string{"-u", g.Account, "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=" + a.HomeDir, "LC_ALL=C", "/bin/bash", "--noprofile", "--norc", "-c", gitAccountScript, "sfops-git-account", req.Action, directory, stage, a.Uid, g.AuthMethod, key, helper, g.Version}
	args = append(args, identityKey, withIdentity)
	if _, err = x.softwareRunner(ctx, "/usr/sbin/runuser", args...); err != nil {
		return errors.New("GIT_ACCOUNT_CONFIGURATION_FAILED")
	}
	status := "CONFIGURED"
	if req.Action == "GIT_ACCOUNT_REMOVE" {
		status = "REMOVED"
	}
	evidence := GitAccountEvidence{ID: g.ID, Version: g.Version, Account: g.Account, AccountUID: a.Uid, Status: status, ObservedAt: stamp()}
	if g.AuthMethod == "SSH" {
		evidence.SSHHostAlias = alias
	}
	return json.NewEncoder(output).Encode(evidence)
}

var gitAccountScript = deployScript("git_account.sh")
