package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type dockerAccessFixture struct {
	x                                                                         *executor
	software                                                                  SoftwareRequest
	member, primary, noSocket, badSocket, failAfterCommand, observationFailed bool
	uid, dockerGID, digest, state                                             string
	changes                                                                   int
	mutations                                                                 []string
}

func newDockerAccessFixture(t *testing.T) *dockerAccessFixture {
	t.Helper()
	f := &dockerAccessFixture{uid: "1000", dockerGID: "998", digest: strings.Repeat("a", 64), state: "inactive"}
	f.x = &executor{root: t.TempDir(), owner: Owner{ControllerID: uuid.NewString(), TargetID: uuid.NewString(), DockerSocket: "/var/run/docker.sock"}}
	calls := []string{}
	f.x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		switch command {
		case "/usr/bin/cat":
			if strings.Join(args, " ") != "-- /etc/login.defs" {
				t.Fatal(args)
			}
			return []byte("UID_MIN 1000\nUID_MAX 60000\n"), nil
		case "/usr/bin/getent":
			if f.observationFailed {
				return nil, errors.New("fixture unavailable")
			}
			if len(args) != 3 || args[0] != "-s" || args[1] != "files" {
				t.Fatal("not local-only", args)
			}
			if args[2] == "group" {
				member := ""
				if f.member {
					member = "ubuntu"
				}
				return []byte("root:x:0:\nsudo:x:27:ubuntu\ndocker:x:" + f.dockerGID + ":" + member + "\nubuntu:x:1000:\n"), nil
			}
			if args[2] == "passwd" {
				gid := "1000"
				if f.primary {
					gid = f.dockerGID
				}
				return []byte("root:x:0:0:root:/root:/bin/bash\nservice:x:997:997:service:/nonexistent:/usr/sbin/nologin\nubuntu:x:" + f.uid + ":" + gid + ":private GECOS:/home/ubuntu:/bin/bash\nlocked:x:1002:1002:locked:/home/locked:/usr/sbin/nologin\n"), nil
			}
			t.Fatal(args)
		case "/usr/bin/stat":
			if args[0] == "--format=%F|%u|%g|%a" {
				if f.noSocket {
					return nil, errors.New("not found")
				}
				mode := "660"
				if f.badSocket {
					mode = "666"
				}
				return []byte("socket|0|" + f.dockerGID + "|" + mode + "\n"), nil
			}
		case "/usr/bin/gpasswd":
			if len(args) != 3 || (args[0] != "-a" && args[0] != "-d") || args[1] != "ubuntu" || args[2] != "docker" {
				t.Fatal("unexpected mutation", args)
			}
			f.changes++
			f.mutations = append(f.mutations, command+" "+strings.Join(args, " "))
			f.member = args[0] == "-a"
			if f.failAfterCommand {
				f.observationFailed = true
				return nil, context.DeadlineExceeded
			}
			return []byte("fixture group updated"), nil
		}
		out, err := unitSoftwareRunner(f.state, f.digest, &calls)(context.Background(), command, args...)
		return []byte(strings.ReplaceAll(string(out), "redis.service", "docker.service")), err
	}
	id := uuid.NewString()
	f.software = SoftwareRequest{ID: id, Version: 1, Kind: "SYSTEMD", Locator: "docker.service", SoftwareKey: "DOCKER", Ownership: &SoftwareOwnership{ControllerID: f.x.owner.ControllerID, TargetID: f.x.owner.TargetID, SoftwareID: id, JobID: uuid.NewString(), ConfigHash: strings.Repeat("b", 64), RuntimeMode: "NATIVE", BinaryPath: "/usr/bin/dockerd", BinaryHash: f.digest}}
	actual, err := ObserveSoftware(context.Background(), f.software, f.x.softwareRunner)
	if err != nil {
		t.Fatal(err)
	}
	f.software.IdentityHash = actual.IdentityHash
	if err = os.MkdirAll(filepath.Dir(f.x.softwareFile(id)), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(softwareBinding{Software: f.software, JobID: f.software.Ownership.JobID})
	if err = os.WriteFile(f.x.softwareFile(id), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *dockerAccessFixture) request(t *testing.T, action string) Request {
	t.Helper()
	obs, err := f.x.observeDockerAccess(context.Background(), f.software)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: f.x.owner.ControllerID, TargetID: f.x.owner.TargetID, JobID: uuid.NewString(), TraceID: uuid.NewString(), Action: "SOFTWARE_DOCKER_ACCESS", SoftwareOperation: "SOFTWARE_DOCKER_ACCESS", Software: &f.software, DockerAccess: &DockerAccessChange{Account: "ubuntu", Action: action, Expected: obs}, DeadlineAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
	r.InputHash = Digest(r)
	return r
}
func (f *dockerAccessFixture) run(t *testing.T, r Request) Result {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux durable journal fsync fixture; commands remain supplied runner fakes")
	}
	if err := validateSoftwareInput(r); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := f.x.softwareLocked(r, &out); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestDockerAccessLocalAccountAndSocketObservation(t *testing.T) {
	f := newDockerAccessFixture(t)
	o, err := f.x.observeDockerAccess(context.Background(), f.software)
	if err != nil || o.Validate() != nil {
		t.Fatal(o, err)
	}
	if len(o.Accounts) != 2 || !o.Socket.Ready || o.DockerGID != "998" || o.Socket.Path != "/run/docker.sock" {
		t.Fatal(o)
	}
	if dockerAccessAccount(o, "root") != nil || dockerAccessAccount(o, "service") != nil || dockerAccessAccount(o, "locked").Eligible {
		t.Fatal("root/system/disabled accounts allowed")
	}
	if bytes.Contains(mustDockerAccessJSON(t, o), []byte("private GECOS")) {
		t.Fatal("account private description exposed")
	}
	for _, member := range []bool{false, true} {
		f.member = member
		f.noSocket = true
		o, err = f.x.observeDockerAccess(context.Background(), f.software)
		if err != nil || o.Socket.Ready {
			t.Fatal(o, err)
		}
		grant := DockerAccessChange{Account: "ubuntu", Action: "GRANT", Expected: o}
		if grant.Validate() == nil {
			t.Fatal("grant accepted missing socket")
		}
		revoke := grant
		revoke.Action = "REVOKE"
		if revoke.Validate() != nil {
			t.Fatal("revoke unnecessarily depends on running Docker")
		}
	}
}
func mustDockerAccessJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestDockerAccessInputIsFrozenAndOldHashesAreUnchanged(t *testing.T) {
	f := newDockerAccessFixture(t)
	r := f.request(t, "GRANT")
	if err := validateSoftwareInput(r); err != nil {
		t.Fatal(err)
	}
	r.DockerAccess.Account = "root"
	if validateSoftwareInput(r) == nil {
		t.Fatal("root allowed")
	}
	r = f.request(t, "GRANT")
	r.DockerAccess.Action = "REVOKE"
	if validateSoftwareInput(r) == nil {
		t.Fatal("changed action accepted old input hash")
	}
	for _, v := range []any{Request{}, Result{}} {
		if bytes.Contains(mustDockerAccessJSON(t, v), []byte("dockerAccess")) {
			t.Fatal("legacy digest changed")
		}
	}
	f.primary = true
	r = f.request(t, "REVOKE")
	if r.DockerAccess.Validate() == nil {
		t.Fatal("primary docker group can be revoked")
	}
}
func TestDockerAccessGrantRevokePreserveOtherGroupsAndHistory(t *testing.T) {
	f := newDockerAccessFixture(t)
	before, _ := os.ReadFile(f.x.softwareFile(f.software.ID))
	r := f.request(t, "GRANT")
	result := f.run(t, r)
	if result.Status != "SUCCEEDED" || result.DockerAccess == nil || !result.DockerAccess.Member || result.Software.State != "STOPPED" || f.changes != 1 {
		t.Fatal(result, f.changes)
	}
	a := dockerAccessAccount(result.DockerAccess.Observation, "ubuntu")
	if len(a.Groups) != 2 || a.Groups[1].Name != "sudo" {
		t.Fatal("other groups changed", a)
	}
	repeated := f.run(t, r)
	if Digest(repeated) != Digest(result) || f.changes != 1 {
		t.Fatal("replayed mutation")
	}
	ack := r
	ack.Action = "ACK"
	ack.ResultHash = Digest(result)
	if !f.run(t, ack).Ack {
		t.Fatal("ack missing")
	}
	after, _ := os.ReadFile(f.x.softwareFile(f.software.ID))
	if !bytes.Equal(before, after) {
		t.Fatal("engine installation/binding changed")
	}
	f.noSocket = true
	revoke := f.request(t, "REVOKE")
	removed := f.run(t, revoke)
	if removed.Status != "SUCCEEDED" || removed.DockerAccess.Member || f.changes != 2 {
		t.Fatal(removed)
	}
	if strings.Join(f.mutations, "|") != "/usr/bin/gpasswd -a ubuntu docker|/usr/bin/gpasswd -d ubuntu docker" {
		t.Fatal(f.mutations)
	}
	if _, err := os.Stat(filepath.Join(f.x.root, "jobs", r.JobID, "result.json")); err != nil {
		t.Fatal("prior job removed")
	}
}
func TestDockerAccessDriftRejectsBeforeAnyMutation(t *testing.T) {
	for _, kind := range []string{"uid", "dockerGid", "engine", "membership", "socket"} {
		t.Run(kind, func(t *testing.T) {
			f := newDockerAccessFixture(t)
			r := f.request(t, "GRANT")
			switch kind {
			case "uid":
				f.uid = "1003"
			case "dockerGid":
				f.dockerGID = "999"
			case "engine":
				f.digest = strings.Repeat("c", 64)
			case "membership":
				f.member = true
			case "socket":
				f.badSocket = true
			}
			result := f.run(t, r)
			if result.Status != "FAILED" || f.changes != 0 {
				t.Fatal(result, f.changes)
			}
		})
	}
}
func TestDockerAccessUnknownObservesOriginalOperationWithoutReplay(t *testing.T) {
	f := newDockerAccessFixture(t)
	r := f.request(t, "GRANT")
	f.failAfterCommand = true
	unknown := f.run(t, r)
	if unknown.Status != "UNKNOWN" || f.changes != 1 {
		t.Fatal(unknown, f.changes)
	}
	if f.run(t, r).Status != "UNKNOWN" || f.changes != 1 {
		t.Fatal("retry replayed unknown command")
	}
	f.observationFailed = false
	status := r
	status.Action = "STATUS"
	done := f.run(t, status)
	if done.Status != "SUCCEEDED" || f.changes != 1 {
		t.Fatal(done, f.changes)
	}
	if f.x.verifyDockerAccessResult(r, done) != nil {
		t.Fatal("verified recovery receipt rejected")
	}
}

func TestDockerAccessObservationRejectsLargeOrForgedMembership(t *testing.T) {
	f := newDockerAccessFixture(t)
	f.member = true
	base, err := f.x.observeDockerAccess(context.Background(), f.software)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"member", "dockerGid", "duplicateGroupGid", "shell"} {
		t.Run(kind, func(t *testing.T) {
			var o DockerAccessObservation
			_ = json.Unmarshal(mustDockerAccessJSON(t, base), &o)
			a := dockerAccessAccount(o, "ubuntu")
			for i := range o.Accounts {
				if o.Accounts[i].Name == "ubuntu" {
					a = &o.Accounts[i]
				}
			}
			switch kind {
			case "member":
				a.Member = false
			case "dockerGid":
				a.Groups[0].GID = "999"
			case "duplicateGroupGid":
				a.Groups = append(a.Groups, DockerAccessGroup{Name: "other", GID: a.Groups[0].GID})
			case "shell":
				a.Shell = "/" + strings.Repeat("x", 300)
			}
			o.Fingerprint = FingerprintDockerAccess(o)
			if o.Validate() == nil {
				t.Fatal("forged observation accepted")
			}
		})
	}
	large := base
	large.Accounts = []DockerAccessAccount{}
	for i := 0; i < 64; i++ {
		a := DockerAccessAccount{Name: fmt.Sprintf("person%d", i), UID: fmt.Sprint(2000 + i), GID: "1000", Shell: "/bin/bash", Eligible: true, Groups: []DockerAccessGroup{}}
		for j := 0; j < 64; j++ {
			a.Groups = append(a.Groups, DockerAccessGroup{Name: fmt.Sprintf("group%d", j), GID: fmt.Sprint(3000 + j)})
		}
		large.Accounts = append(large.Accounts, a)
	}
	large.Fingerprint = FingerprintDockerAccess(large)
	if err = large.Validate(); err == nil || err.Error() != "DOCKER_ACCESS_OBSERVATION_LIMIT" {
		t.Fatal("oversized response accepted", err)
	}
}

func TestDockerAccessTransientEngineKeepsUnknownUntilStatusWithoutReplay(t *testing.T) {
	f := newDockerAccessFixture(t)
	r := f.request(t, "GRANT")
	f.state = "activating"
	pending := f.run(t, r)
	if pending.Status != "UNKNOWN" || pending.DockerAccess != nil || f.changes != 1 {
		t.Fatal("transient engine created unusable final receipt", pending, f.changes)
	}
	f.state = "active"
	status := r
	status.Action = "STATUS"
	result := f.run(t, status)
	if result.Status != "SUCCEEDED" || result.Software == nil || result.Software.State != "RUNNING" || result.DockerAccess == nil || !result.DockerAccess.Member || f.changes != 1 {
		t.Fatal("status did not recover without replay", result, f.changes)
	}
	ack := r
	ack.Action = "ACK"
	ack.ResultHash = Digest(result)
	if !f.run(t, ack).Ack {
		t.Fatal("recovered result could not be acknowledged")
	}
}
