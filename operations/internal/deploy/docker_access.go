package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func validateDockerAccessInput(req Request) error {
	if req.DockerAccess == nil || req.DockerAccess.Validate() != nil || req.SoftwareOperation != "SOFTWARE_DOCKER_ACCESS" || req.Software == nil || req.Software.Validate(false) != nil || !ownedDockerEngine(*req.Software) || req.Installation != nil || req.Uninstallation != nil || req.Reconfiguration != nil || req.NodeEnvironment != nil {
		return errors.New("DOCKER_ACCESS_INPUT_INVALID")
	}
	if req.DockerAccess.Expected.SoftwareID != req.Software.ID || req.DockerAccess.Expected.EngineIdentityHash != req.Software.IdentityHash {
		return errors.New("DOCKER_ACCESS_ENGINE_IDENTITY_MISMATCH")
	}
	return nil
}
func (x *executor) readDockerAccess(req Request, output io.Writer) error {
	if req.Software == nil || req.Software.Validate(false) != nil || !ownedDockerEngine(*req.Software) || req.DockerAccess != nil || req.Installation != nil || req.Uninstallation != nil || req.Reconfiguration != nil || req.NodeEnvironment != nil || req.Plugin != nil || req.Candidate != nil || req.Baseline != nil || req.Binding != nil {
		return errors.New("DOCKER_ACCESS_ENGINE_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	obs, err := x.observeDockerAccess(ctx, *req.Software)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(obs)
}
func parseDockerAccountRange(data []byte) (uint32, uint32, error) {
	values := map[string]uint32{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if fields[0] != "UID_MIN" && fields[0] != "UID_MAX" {
			continue
		}
		if len(fields) < 2 {
			return 0, 0, errors.New("DOCKER_ACCOUNT_RANGE_UNAVAILABLE")
		}
		number, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil || number == 0 || values[fields[0]] != 0 {
			return 0, 0, errors.New("DOCKER_ACCOUNT_RANGE_UNAVAILABLE")
		}
		values[fields[0]] = uint32(number)
	}
	min, max := values["UID_MIN"], values["UID_MAX"]
	if min == 0 || max < min {
		return 0, 0, errors.New("DOCKER_ACCOUNT_RANGE_UNAVAILABLE")
	}
	return min, max, nil
}

type dockerLocalGroup struct {
	DockerAccessGroup
	Members map[string]bool
}

func parseDockerLocalGroups(data []byte) ([]dockerLocalGroup, string, error) {
	groups := []dockerLocalGroup{}
	gid := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 4 || !dockerAccountName.MatchString(fields[0]) || !dockerNumericID(fields[2]) || seen[fields[0]] {
			return nil, "", errors.New("DOCKER_LOCAL_GROUPS_INVALID")
		}
		seen[fields[0]] = true
		members := map[string]bool{}
		if fields[3] != "" {
			for _, name := range strings.Split(fields[3], ",") {
				if !dockerAccountName.MatchString(name) {
					return nil, "", errors.New("DOCKER_LOCAL_GROUPS_INVALID")
				}
				members[name] = true
			}
		}
		groups = append(groups, dockerLocalGroup{DockerAccessGroup{fields[0], fields[2]}, members})
		if fields[0] == "docker" {
			gid = fields[2]
		}
	}
	if gid == "" || gid == "0" {
		return nil, "", errors.New("DOCKER_LOCAL_GROUP_MISSING")
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, gid, nil
}
func parseDockerLocalAccounts(data []byte, groups []dockerLocalGroup, dockerGID string, min, max uint32) ([]DockerAccessAccount, error) {
	accounts := []DockerAccessAccount{}
	seenNames, seenUIDs := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || !dockerNumericID(fields[2]) || !dockerNumericID(fields[3]) {
			return nil, errors.New("DOCKER_LOCAL_ACCOUNTS_INVALID")
		}
		uid, _ := strconv.ParseUint(fields[2], 10, 32)
		if uid < uint64(min) || uid > uint64(max) || uid == 0 || fields[0] == "root" {
			continue
		}
		if !dockerAccountName.MatchString(fields[0]) || seenNames[fields[0]] || seenUIDs[fields[2]] || len(accounts) >= 256 {
			return nil, errors.New("DOCKER_LOCAL_ACCOUNTS_INVALID")
		}
		seenNames[fields[0]], seenUIDs[fields[2]] = true, true
		a := DockerAccessAccount{Name: fields[0], UID: fields[2], GID: fields[3], Shell: fields[6], Eligible: true, PrimaryGroup: fields[3] == dockerGID, Groups: []DockerAccessGroup{}}
		if a.Shell == "" || !strings.HasPrefix(a.Shell, "/") || strings.HasSuffix(a.Shell, "/nologin") || strings.HasSuffix(a.Shell, "/false") {
			a.Eligible = false
			a.Reason = "DOCKER_ACCOUNT_LOGIN_DISABLED"
		}
		a.Member = a.PrimaryGroup
		for _, g := range groups {
			if g.Members[a.Name] {
				a.Groups = append(a.Groups, g.DockerAccessGroup)
				if g.Name == "docker" {
					a.Member = true
				}
			}
		}
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
	return accounts, nil
}
func (x *executor) observeDockerAccess(ctx context.Context, s SoftwareRequest) (DockerAccessObservation, error) {
	o := DockerAccessObservation{}
	if !ownedDockerEngine(s) || s.Validate(false) != nil {
		return o, errors.New("DOCKER_ACCESS_ENGINE_REQUIRED")
	}
	if x.owner.DockerSocket != "/run/docker.sock" && x.owner.DockerSocket != "/var/run/docker.sock" {
		return o, errors.New("DOCKER_ACCESS_CUSTOM_SOCKET_UNSUPPORTED")
	}
	if err := x.boundSoftware(s); err != nil {
		return o, err
	}
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || actual == nil || actual.IdentityHash != s.IdentityHash {
		return o, errors.New("DOCKER_ACCESS_ENGINE_IDENTITY_MISMATCH")
	}
	definitions, err := x.softwareRunner(ctx, "/usr/bin/cat", "--", "/etc/login.defs")
	if err != nil || len(definitions) >= 64<<10 {
		return o, errors.New("DOCKER_ACCOUNT_RANGE_UNAVAILABLE")
	}
	min, max, err := parseDockerAccountRange(definitions)
	if err != nil {
		return o, err
	}
	groupData, err := x.softwareRunner(ctx, "/usr/bin/getent", "-s", "files", "group")
	if err != nil || len(groupData) >= 64<<10 {
		return o, errors.New("DOCKER_LOCAL_GROUPS_UNAVAILABLE")
	}
	groups, gid, err := parseDockerLocalGroups(groupData)
	if err != nil {
		return o, err
	}
	accountData, err := x.softwareRunner(ctx, "/usr/bin/getent", "-s", "files", "passwd")
	if err != nil || len(accountData) >= 64<<10 {
		return o, errors.New("DOCKER_LOCAL_ACCOUNTS_UNAVAILABLE")
	}
	accounts, err := parseDockerLocalAccounts(accountData, groups, gid, min, max)
	if err != nil {
		return o, err
	}
	socket := DockerAccessSocket{Path: "/run/docker.sock", ErrorCode: "DOCKER_SOCKET_UNAVAILABLE"}
	socketData, socketErr := x.softwareRunner(ctx, "/usr/bin/stat", "--format=%F|%u|%g|%a", "--", socket.Path)
	if socketErr == nil {
		fields := strings.Split(strings.TrimSpace(string(socketData)), "|")
		if len(fields) != 4 || !dockerNumericID(fields[1]) || !dockerNumericID(fields[2]) {
			return o, errors.New("DOCKER_SOCKET_OBSERVATION_INVALID")
		}
		socket.UID, socket.GID, socket.Mode = fields[1], fields[2], fields[3]
		socket.ErrorCode = "DOCKER_SOCKET_PERMISSION_UNSUPPORTED"
		if fields[0] == "socket" && socket.UID == "0" && socket.GID == gid && socket.Mode == "660" {
			socket.Ready = true
			socket.ErrorCode = ""
		}
	}
	o = DockerAccessObservation{SoftwareID: s.ID, EngineIdentityHash: actual.IdentityHash, DockerGID: gid, UIDMin: min, UIDMax: max, Socket: socket, Accounts: accounts, ObservedAt: stamp()}
	o.Fingerprint = FingerprintDockerAccess(o)
	if err = o.Validate(); err != nil {
		return o, err
	}
	return o, nil
}

func dockerAccessIdentityMatches(before, after DockerAccessObservation, name string, requireSocket bool) bool {
	if before.SoftwareID != after.SoftwareID || before.EngineIdentityHash != after.EngineIdentityHash || before.DockerGID != after.DockerGID || before.UIDMin != after.UIDMin || before.UIDMax != after.UIDMax {
		return false
	}
	if requireSocket && (!after.Socket.Ready || before.Socket != after.Socket) {
		return false
	}
	a, b := dockerAccessAccount(before, name), dockerAccessAccount(after, name)
	if a == nil || b == nil {
		return false
	}
	normalized := func(a DockerAccessAccount) DockerAccessAccount {
		a.Member = false
		groups := []DockerAccessGroup{}
		for _, g := range a.Groups {
			if g.Name != "docker" {
				groups = append(groups, g)
			}
		}
		a.Groups = groups
		return a
	}
	return reflect.DeepEqual(normalized(*a), normalized(*b))
}

type dockerAccessMarker struct {
	JobID     string             `json:"jobId"`
	InputHash string             `json:"inputHash"`
	Change    DockerAccessChange `json:"change"`
}

func (x *executor) changeDockerAccess(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	if err := validateDockerAccessInput(req); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	current, err := x.observeDockerAccess(ctx, *req.Software)
	if err != nil {
		return result, err
	}
	change := req.DockerAccess
	if !dockerAccessIdentityMatches(change.Expected, current, change.Account, change.Action == "GRANT") {
		return result, errors.New("DOCKER_ACCESS_IDENTITY_CHANGED")
	}
	before, actual := dockerAccessAccount(change.Expected, change.Account), dockerAccessAccount(current, change.Account)
	if before.Member != actual.Member {
		return result, errors.New("DOCKER_ACCESS_MEMBERSHIP_CHANGED")
	}
	marker := dockerAccessMarker{req.JobID, req.InputHash, *change}
	if err = atomicJSON(filepath.Join(dir, "docker-access-command.json"), marker); err != nil {
		return result, err
	}
	desired := change.Action == "GRANT"
	if actual.Member != desired {
		option := "-d"
		if desired {
			option = "-a"
		}
		_, _ = x.softwareRunner(ctx, "/usr/bin/gpasswd", option, change.Account, "docker")
	}
	// Even a failed/timeout command may have changed the group file. Observe
	// positively on a fresh deadline; never infer failure means no modification.
	return x.reconcileDockerAccess(req, dir), nil
}
func (x *executor) reconcileDockerAccess(req Request, dir string) Result {
	result := softwareResult(req)
	result.ErrorCode = "DOCKER_ACCESS_UNCONFIRMED"
	change := req.DockerAccess
	var marker dockerAccessMarker
	if readJSON(filepath.Join(dir, "docker-access-command.json"), &marker) != nil || marker.JobID != req.JobID || marker.InputHash != req.InputHash || !reflect.DeepEqual(marker.Change, *change) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	observed, err := x.observeDockerAccess(ctx, *req.Software)
	if err != nil || !dockerAccessIdentityMatches(change.Expected, observed, change.Account, change.Action == "GRANT") {
		return result
	}
	account := dockerAccessAccount(observed, change.Account)
	desired := change.Action == "GRANT"
	if account == nil || account.Member != desired {
		return result
	}
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil || actual == nil || actual.IdentityHash != req.Software.IdentityHash || (actual.State != "RUNNING" && actual.State != "STOPPED" && actual.State != "FAILED") {
		return result
	}
	result.Software = actual
	result.Status, result.Phase, result.ErrorCode = "SUCCEEDED", "DOCKER_ACCESS_UPDATED", ""
	result.DockerAccess = &DockerAccessEvidence{Account: change.Account, UID: account.UID, Action: change.Action, Member: account.Member, Observation: observed, ObservedAt: stamp()}
	return result
}
func (x *executor) verifyDockerAccessResult(req Request, result Result) error {
	e := result.DockerAccess
	c := req.DockerAccess
	if e == nil || e.Observation.Validate() != nil || e.Account != c.Account || e.Action != c.Action || e.Member != (c.Action == "GRANT") || !dockerAccessIdentityMatches(c.Expected, e.Observation, c.Account, c.Action == "GRANT") {
		return errors.New("DOCKER_ACCESS_RECEIPT_MISMATCH")
	}
	account := dockerAccessAccount(e.Observation, c.Account)
	if account == nil || e.UID != account.UID || e.Member != account.Member {
		return errors.New("DOCKER_ACCESS_RECEIPT_MISMATCH")
	}
	return x.boundSoftware(*req.Software)
}
