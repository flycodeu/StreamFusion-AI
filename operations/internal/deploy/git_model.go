package deploy

import (
	"errors"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

const GitRepositoryCapabilityVersion = 1

type GitRepositoryRequest struct {
	Action               string `json:"action"`
	BindingID            string `json:"bindingId"`
	BindingVersion       string `json:"bindingVersion"`
	ProfileID            string `json:"profileId"`
	ProfileVersion       string `json:"profileVersion"`
	CredentialID         string `json:"credentialId"`
	CredentialRevision   string `json:"credentialRevision"`
	Account              string `json:"account"`
	AccountUID           string `json:"accountUid"`
	RepoPath             string `json:"repoPath"`
	GitBinary            string `json:"gitBinary"`
	SSHHost              string `json:"sshHost"`
	SSHPort              int    `json:"sshPort"`
	SSHUser              string `json:"sshUser"`
	HostPublicKey        string `json:"hostPublicKey"`
	KeyFingerprint       string `json:"keyFingerprint"`
	AuthorName           string `json:"authorName"`
	AuthorEmail          string `json:"authorEmail"`
	ExpectedConfigHash   string `json:"expectedConfigHash,omitempty"`
	ExpectedTerminalHash string `json:"expectedTerminalHash,omitempty"`
}

type GitRepositoryObservation struct {
	BindingID          string `json:"bindingId"`
	BindingVersion     string `json:"bindingVersion"`
	ProfileID          string `json:"profileId"`
	ProfileVersion     string `json:"profileVersion"`
	Account            string `json:"account"`
	AccountUID         string `json:"accountUid"`
	RepoPath           string `json:"repoPath"`
	ConfigHash         string `json:"configHash"`
	TerminalHash       string `json:"terminalHash"`
	RemoteURL          string `json:"remoteUrl"`
	Branch             string `json:"branch"`
	GitVersion         string `json:"gitVersion"`
	KeyFingerprint     string `json:"keyFingerprint"`
	KeyPath            string `json:"keyPath"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
	AuthorName         string `json:"authorName"`
	AuthorEmail        string `json:"authorEmail"`
	Status             string `json:"status"`
	ObservedAt         string `json:"observedAt"`
}

type GitRepositoryPreflight struct {
	Ready       bool                      `json:"ready"`
	Code        string                    `json:"code"`
	Message     string                    `json:"message"`
	Observation *GitRepositoryObservation `json:"observation,omitempty"`
}

var gitAccountPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)
var gitHostPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)
var gitRevisionPattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
var gitFingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

func (g GitRepositoryRequest) Validate() error {
	validText := func(s string, max int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
	}
	if (g.Action != "BIND" && g.Action != "UNBIND") || !ID.MatchString(g.BindingID) || !ID.MatchString(g.ProfileID) || !ID.MatchString(g.CredentialID) || !gitRevisionPattern.MatchString(g.BindingVersion) || !gitRevisionPattern.MatchString(g.ProfileVersion) || !gitRevisionPattern.MatchString(g.CredentialRevision) || !gitAccountPattern.MatchString(g.Account) || g.Account == "root" || !gitAccountPattern.MatchString(g.SSHUser) || !validText(g.AuthorName, 200) || !validText(g.AuthorEmail, 254) || !strings.Contains(g.AuthorEmail, "@") {
		return errors.New("GIT_REPOSITORY_INPUT_INVALID")
	}
	if g.AccountUID != "" {
		if uid, e := strconv.ParseUint(g.AccountUID, 10, 32); e != nil || uid == 0 {
			return errors.New("GIT_ACCOUNT_INVALID")
		}
	}
	if !validText(g.RepoPath, 4096) || !path.IsAbs(g.RepoPath) || path.Clean(g.RepoPath) != g.RepoPath || g.RepoPath == "/" || !path.IsAbs(g.GitBinary) || path.Clean(g.GitBinary) != g.GitBinary || path.Base(g.GitBinary) != "git" || strings.ContainsAny(g.GitBinary, "\x00\r\n") {
		return errors.New("GIT_PATH_INVALID")
	}
	if (!gitHostPattern.MatchString(g.SSHHost) && net.ParseIP(g.SSHHost) == nil) || g.SSHPort < 1 || g.SSHPort > 65535 || !gitFingerprintPattern.MatchString(g.KeyFingerprint) {
		return errors.New("GIT_SSH_IDENTITY_INVALID")
	}
	if len(g.HostPublicKey) > 16<<10 {
		return errors.New("GIT_HOST_KEY_INVALID")
	}
	_, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(g.HostPublicKey))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("GIT_HOST_KEY_INVALID")
	}
	if g.ExpectedConfigHash != "" && !Hex.MatchString(g.ExpectedConfigHash) {
		return errors.New("GIT_CONFIG_HASH_INVALID")
	}
	if g.ExpectedTerminalHash != "" && !Hex.MatchString(g.ExpectedTerminalHash) {
		return errors.New("GIT_TERMINAL_HASH_INVALID")
	}
	return nil
}
