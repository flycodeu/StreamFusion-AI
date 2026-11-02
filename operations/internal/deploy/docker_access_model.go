package deploy

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DockerAccessCapabilityVersion = 1

// The observation is also frozen into the existing 64 KiB request envelope.
const DockerAccessObservationLimit = 32 << 10

var dockerAccountName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`)

type DockerAccessGroup struct {
	Name string `json:"name"`
	GID  string `json:"gid"`
}
type DockerAccessAccount struct {
	Name         string              `json:"name"`
	UID          string              `json:"uid"`
	GID          string              `json:"gid"`
	Shell        string              `json:"shell"`
	Eligible     bool                `json:"eligible"`
	Reason       string              `json:"reason,omitempty"`
	Member       bool                `json:"member"`
	PrimaryGroup bool                `json:"primaryGroup"`
	Groups       []DockerAccessGroup `json:"groups"`
}
type DockerAccessSocket struct {
	Path      string `json:"path"`
	UID       string `json:"uid"`
	GID       string `json:"gid"`
	Mode      string `json:"mode"`
	Ready     bool   `json:"ready"`
	ErrorCode string `json:"errorCode,omitempty"`
}
type DockerAccessObservation struct {
	SoftwareID         string                `json:"softwareId"`
	EngineIdentityHash string                `json:"engineIdentityHash"`
	DockerGID          string                `json:"dockerGid"`
	UIDMin             uint32                `json:"uidMin"`
	UIDMax             uint32                `json:"uidMax"`
	Socket             DockerAccessSocket    `json:"socket"`
	Accounts           []DockerAccessAccount `json:"accounts"`
	Fingerprint        string                `json:"fingerprint"`
	ObservedAt         string                `json:"observedAt"`
}
type DockerAccessChange struct {
	ActorGeneration         int                     `json:"actorGeneration,omitempty"`
	ExpectedHostVersion     string                  `json:"expectedHostVersion,omitempty"`
	ExpectedSoftwareVersion string                  `json:"expectedSoftwareVersion,omitempty"`
	Account                 string                  `json:"account"`
	Action                  string                  `json:"action"`
	Expected                DockerAccessObservation `json:"expected"`
}
type DockerAccessEvidence struct {
	Account     string                  `json:"account"`
	UID         string                  `json:"uid"`
	Action      string                  `json:"action"`
	Member      bool                    `json:"member"`
	Observation DockerAccessObservation `json:"observation"`
	ObservedAt  string                  `json:"observedAt"`
}

func dockerNumericID(value string) bool {
	n, err := strconv.ParseUint(value, 10, 32)
	return err == nil && strconv.FormatUint(n, 10) == value
}
func FingerprintDockerAccess(o DockerAccessObservation) string {
	o.Fingerprint = ""
	o.ObservedAt = ""
	return Digest(o)
}
func (o DockerAccessObservation) Validate() error {
	raw, err := json.Marshal(o)
	if err != nil || len(raw) > DockerAccessObservationLimit {
		return errors.New("DOCKER_ACCESS_OBSERVATION_LIMIT")
	}
	if !ID.MatchString(o.SoftwareID) || !Hex.MatchString(o.EngineIdentityHash) || !dockerNumericID(o.DockerGID) || o.DockerGID == "0" || o.UIDMin < 1 || o.UIDMax < o.UIDMin || o.Socket.Path != "/run/docker.sock" || len(o.Accounts) > 256 || o.Fingerprint != FingerprintDockerAccess(o) {
		return errors.New("DOCKER_ACCESS_OBSERVATION_INVALID")
	}
	if _, err := time.Parse(time.RFC3339Nano, o.ObservedAt); err != nil {
		return errors.New("DOCKER_ACCESS_OBSERVATION_INVALID")
	}
	if o.Socket.Ready && (o.Socket.UID != "0" || o.Socket.GID != o.DockerGID || o.Socket.Mode != "660" || o.Socket.ErrorCode != "") {
		return errors.New("DOCKER_ACCESS_SOCKET_INVALID")
	}
	seen := map[string]bool{}
	seenUID := map[string]bool{}
	for _, a := range o.Accounts {
		uid, err := strconv.ParseUint(a.UID, 10, 32)
		if !dockerAccountName.MatchString(a.Name) || a.Name == "root" || seen[a.Name] || seenUID[a.UID] || !dockerNumericID(a.UID) || !dockerNumericID(a.GID) || err != nil || uid < uint64(o.UIDMin) || uid > uint64(o.UIDMax) || len(a.Shell) > 256 || strings.ContainsAny(a.Shell, "\r\n\x00") || len(a.Groups) > 256 || a.PrimaryGroup != (a.GID == o.DockerGID) || a.PrimaryGroup && !a.Member {
			return errors.New("DOCKER_ACCESS_ACCOUNT_INVALID")
		}
		seen[a.Name] = true
		seenUID[a.UID] = true
		eligible := a.Shell != "" && strings.HasPrefix(a.Shell, "/") && !strings.HasSuffix(a.Shell, "/nologin") && !strings.HasSuffix(a.Shell, "/false")
		if a.Eligible != eligible || eligible && a.Reason != "" || !eligible && a.Reason != "DOCKER_ACCOUNT_LOGIN_DISABLED" {
			return errors.New("DOCKER_ACCESS_ACCOUNT_INVALID")
		}
		groups := map[string]bool{}
		gids := map[string]bool{}
		dockerMember := false
		for _, g := range a.Groups {
			if !dockerAccountName.MatchString(g.Name) || !dockerNumericID(g.GID) || groups[g.Name] || gids[g.GID] {
				return errors.New("DOCKER_ACCESS_GROUP_INVALID")
			}
			groups[g.Name] = true
			gids[g.GID] = true
			if g.Name == "docker" {
				if g.GID != o.DockerGID {
					return errors.New("DOCKER_ACCESS_GROUP_INVALID")
				}
				dockerMember = true
			}
		}
		if a.Member != (a.PrimaryGroup || dockerMember) {
			return errors.New("DOCKER_ACCESS_MEMBERSHIP_INVALID")
		}
	}
	return nil
}
func (c DockerAccessChange) Validate() error {
	if c.Expected.Validate() != nil || !dockerAccountName.MatchString(c.Account) || (c.Action != "GRANT" && c.Action != "REVOKE") {
		return errors.New("DOCKER_ACCESS_INPUT_INVALID")
	}
	a := dockerAccessAccount(c.Expected, c.Account)
	if a == nil || !a.Eligible {
		return errors.New("DOCKER_ACCESS_ACCOUNT_INELIGIBLE")
	}
	if c.Action == "REVOKE" && a.PrimaryGroup {
		return errors.New("DOCKER_ACCESS_PRIMARY_GROUP")
	}
	if c.Action == "GRANT" && !c.Expected.Socket.Ready {
		return errors.New("DOCKER_ACCESS_SOCKET_NOT_READY")
	}
	return nil
}
func dockerAccessAccount(o DockerAccessObservation, name string) *DockerAccessAccount {
	for i := range o.Accounts {
		if o.Accounts[i].Name == name {
			return &o.Accounts[i]
		}
	}
	return nil
}
