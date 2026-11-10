package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

func TestGitMigrationPreservesHistoricalJobsAndCredentials(t *testing.T) {
	f := schemaSixteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schemaV17); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schema_migrations VALUES(17,?,?,?)", hashToken(schemaV17), "historical", Now()); err != nil {
		t.Fatal(err)
	}
	before := schemaSeventeenHistoricalRows(t, db)
	db.Close()
	s, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !reflect.DeepEqual(before, schemaSeventeenHistoricalRows(t, s.DB)) {
		t.Fatal("historical rows or locks changed")
	}
	var bad int
	if err = s.DB.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&bad); err != nil || bad != 0 {
		t.Fatal(err, bad)
	}
	backups, _ := filepath.Glob(filepath.Join(f.dir, "before-schema-18-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatal("migration backup missing")
	}
	if err = s.verifySecrets(); err != nil {
		t.Fatal(err)
	}
}

type gitFixture struct {
	mu                sync.Mutex
	changed           bool
	unknown           bool
	calls             int
	accountVersion    int
	repositoryVersion int
	accountCalls      int
	keyCalls          int
	results           map[string]deploy.Result
}

func gitControllerFixture(t *testing.T) (*Store, *browser, string, map[string]any, *gitFixture) {
	s, b := setup(t)
	b.login()
	f := &gitFixture{results: map[string]deploy.Result{}, accountVersion: deploy.GitAccountCapabilityVersion, repositoryVersion: deploy.GitRepositoryCapabilityVersion}
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	keySigner, _ := ssh.NewSignerFromKey(privateKey)
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		encode := func(v any) ([]byte, bool) { raw, _ := json.Marshal(v); return raw, true }
		if req.Action == "CAPABILITIES" {
			f.mu.Lock()
			defer f.mu.Unlock()
			cap := installCompatibilityCapabilities(req, s)
			cap.GitRepositoryVersion = f.repositoryVersion
			cap.GitAccountVersion = f.accountVersion
			return encode(cap)
		}
		if g := req.GitAccount; g != nil {
			f.mu.Lock()
			f.accountCalls++
			f.mu.Unlock()
			if g.AuthMethod == "HTTPS" && req.Action == "GIT_ACCOUNT_CONFIGURE" && req.Secrets["gitToken"] == "" {
				t.Error("HTTPS configuration missing token")
			}
			if req.Action == "GIT_ACCOUNT_REMOVE" && len(req.Secrets) != 0 {
				t.Error("remove received token")
			}
			status := "CONFIGURED"
			if req.Action == "GIT_ACCOUNT_REMOVE" {
				status = "REMOVED"
			}
			return encode(deploy.GitAccountEvidence{ID: g.ID, Version: g.Version, Account: g.Account, AccountUID: "1000", Status: status, ObservedAt: Now()})
		}
		if k := req.GitKey; k != nil {
			f.mu.Lock()
			f.keyCalls++
			f.mu.Unlock()
			if len(req.Secrets) != 0 {
				t.Error("key operation received secrets")
			}
			e := deploy.GitKeyEvidence{ID: k.ID, Account: k.Account, AccountUID: "1000", KeyPath: "/home/" + k.Account + "/.local/share/streamfusion-ops/git-keys/" + k.ID + "/key", PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(keySigner.PublicKey()))), Fingerprint: ssh.FingerprintSHA256(keySigner.PublicKey())}
			e.HostPublicKey, e.HostFingerprint = e.PublicKey, e.Fingerprint
			return encode(e)
		}
		g := req.GitRepository
		if g == nil {
			return nil, false
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		o := &deploy.GitRepositoryObservation{BindingID: g.BindingID, BindingVersion: g.BindingVersion, ProfileID: g.ProfileID, ProfileVersion: g.ProfileVersion, Account: g.Account, AccountUID: "1000", RepoPath: g.RepoPath, ConfigHash: strings.Repeat("a", 64), TerminalHash: strings.Repeat("c", 64), RemoteURL: "git@git.example:team/repo.git", Branch: "main", GitVersion: "2.43.0", KeyFingerprint: g.KeyFingerprint, Status: "BOUND", ObservedAt: Now()}
		if f.changed {
			o.ConfigHash = strings.Repeat("b", 64)
		}
		switch req.Action {
		case "GIT_REPOSITORY_CHECK", "GIT_REPOSITORY_OBSERVE", "GIT_REPOSITORY_TEST":
			if len(req.Secrets) != 0 {
				t.Error("read-only request received private key")
			}
			return encode(deploy.GitRepositoryPreflight{Ready: true, Code: "GIT_REMOTE_READABLE", Observation: o})
		case "GIT_REPOSITORY_CHANGE":
			f.calls++
			if g.Action == "BIND" && g.KeySource != "TARGET" && req.Secrets["gitKey"] == "" {
				t.Error("binding missing key")
			}
			if g.KeySource == "TARGET" && len(req.Secrets) != 0 {
				t.Error("target binding received private key")
			}
			if g.Action == "UNBIND" {
				o.Status = "UNBOUND"
				if len(req.Secrets) != 0 {
					t.Error("unbind received key")
				}
			}
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "GIT_REPOSITORY_UPDATED", ObservedAt: Now(), GitRepository: o}
			f.results[req.JobID] = result
			if f.unknown {
				result.Status = "UNKNOWN"
			}
			return encode(result)
		case "STATUS", "ACK":
			result := f.results[req.JobID]
			if req.Action == "ACK" && req.ResultHash == deploy.Digest(result) {
				result.Ack = true
			}
			return encode(result)
		}
		return nil, false
	})
	hostRecord, _ := s.readTarget(host)
	private, err := s.secret(hostRecord.CredentialID, hostRecord.Revision, "SSH_KEY")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"name": "公司Git", "sshHost": "git.example", "sshPort": 22, "sshUser": "git", "authorName": "Work User", "authorEmail": "work@example.com", "credentialId": hostRecord.CredentialID, "credentialRevision": hostRecord.Revision, "hostPublicKey": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), "hostKeyConfirmed": true}
	input["authMethod"], input["keySource"], input["hostId"], input["systemAccount"], input["keyId"], input["expectedHostVersion"] = "SSH", "TARGET", host, "ubuntu", "12345678-1234-4234-8234-123456789012", hostRecord.Version
	a := dataOf(t, b.call("POST", "/git-accounts", input, 200))
	if strings.Contains(jsonText(a), "PRIVATE KEY") {
		t.Fatal("private key leaked")
	}
	return s, b, host, a, f
}
func gitPlanFixture(t *testing.T, b *browser, host string, a map[string]any, binding *gitRepository, action string) map[string]any {
	t.Helper()
	target, _ := b.h.store.readTarget(host)
	in := map[string]any{"expectedHostVersion": target.Version, "action": action, "account": "ubuntu", "repoPath": "/home/ubuntu/project", "profileId": a["id"], "expectedProfileVersion": a["version"]}
	if binding != nil {
		in["bindingId"] = binding.ID
		in["expectedBindingVersion"] = binding.Version
	}
	return dataOf(t, b.call("POST", "/hosts/"+host+"/git-repository-plans", in, 200))
}

func TestGitBindingFreezesIdentityRecoversWithoutReplayAndPreservesHistory(t *testing.T) {
	s, b, host, a, f := gitControllerFixture(t)
	plan := gitPlanFixture(t, b, host, a, nil, "BIND")
	path := "/git-repository-plans/" + plan["id"].(string) + "/submit"
	input := map[string]any{"confirmed": true, "idempotencyKey": "git-bind-test-1"}
	accepted := dataOf(t, b.call("POST", path, input, 202))
	job, binding := accepted["jobId"].(string), accepted["bindingId"].(string)
	repeated := dataOf(t, b.call("POST", path, input, 200))
	if repeated["jobId"] != job {
		t.Fatal("duplicate job")
	}
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	if strings.Contains(raw, "PRIVATE KEY") || strings.Contains(raw, `"secrets"`) {
		t.Fatal("durable job contains key")
	}
	f.unknown = true
	b.h.processGitRepository(context.Background(), job, raw)
	row, _ := readGitRepository(s.DB, binding)
	if row.LastJobStatus != "UNKNOWN" {
		t.Fatal(row.LastJobStatus)
	}
	b.call("POST", "/jobs/"+job+"/reconcile", map[string]any{}, 200)
	row, _ = readGitRepository(s.DB, binding)
	if row.Status != "BOUND" || row.LastJobStatus != "SUCCEEDED" || row.LastJobAckPending || f.calls != 1 {
		t.Fatal(row, f.calls)
	}
	b.call("DELETE", "/git-accounts/"+a["id"].(string), map[string]string{"expectedVersion": "1"}, 409)
	observed := dataOf(t, b.call("POST", "/git-repositories/"+binding+"/test", map[string]string{"expectedVersion": row.Version}, 200))
	if observed["code"] != "GIT_REMOTE_READABLE" {
		t.Fatal(observed)
	}
	plan = gitPlanFixture(t, b, host, a, &row, "UNBIND")
	input["idempotencyKey"] = "git-unbind-test-2"
	accepted = dataOf(t, b.call("POST", "/git-repository-plans/"+plan["id"].(string)+"/submit", input, 202))
	job = accepted["jobId"].(string)
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	f.unknown = false
	b.h.processGitRepository(context.Background(), job, raw)
	row, _ = readGitRepository(s.DB, binding)
	if row.Status != "RELEASED" || row.LastJobAckPending {
		t.Fatal(row)
	}
	b.call("DELETE", "/git-accounts/"+a["id"].(string), map[string]string{"expectedVersion": "1"}, 200)
	if body := b.call("GET", "/hosts/"+host+"/git-repositories", nil, 200).Body.String(); !strings.Contains(body, binding) || !strings.Contains(body, "公司Git") {
		t.Fatal("deleted profile hid repository history", body)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM git_repositories WHERE id=?", binding).Scan(&count)
	if count != 1 {
		t.Fatal("binding history deleted")
	}
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='GIT_REPOSITORY_CHANGE'").Scan(&count)
	if count != 2 {
		t.Fatal("job history missing")
	}
}

func TestGitPlanRejectsDriftAndChangedActorBeforeWriting(t *testing.T) {
	s, b, host, a, f := gitControllerFixture(t)
	plan := gitPlanFixture(t, b, host, a, nil, "BIND")
	path := "/git-repository-plans/" + plan["id"].(string) + "/submit"
	f.changed = true
	response := b.call("POST", path, map[string]any{"confirmed": true, "idempotencyKey": "git-drift-rejected"}, 409)
	if !strings.Contains(response.Body.String(), "GIT_PLAN_STALE") {
		t.Fatal(response)
	}
	f.changed = false
	accepted := dataOf(t, b.call("POST", path, map[string]any{"confirmed": true, "idempotencyKey": "git-actor-check"}, 202))
	job := accepted["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw)
	s.DB.Exec("UPDATE admins SET session_generation=session_generation+1")
	b.h.processGitRepository(context.Background(), job, raw)
	var status, code string
	s.DB.QueryRow("SELECT status,error_code FROM jobs WHERE id=?", job).Scan(&status, &code)
	if status != "FAILED" || code != "GIT_ACTOR_CHANGED" || f.calls != 0 {
		t.Fatal(status, code, f.calls)
	}
}
