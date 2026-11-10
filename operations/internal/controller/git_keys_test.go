package controller

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitKeyReadFailureKeepsFixedDiagnosticWithoutTargetOutput(t *testing.T) {
	if got := classifyExecutorInvocation(errors.New("ssh exit 1"), "GIT_HOST_KEY_UNAVAILABLE\n"); got.Error() != "GIT_HOST_KEY_UNAVAILABLE" {
		t.Fatal(got)
	}
	if got := classifyExecutorInvocation(errors.New("ssh exit 1"), "GIT_HOST_KEY_UNAVAILABLE\nprivate-output"); got.Error() != "EXECUTOR_CALL_FAILED" {
		t.Fatal(got)
	}
}

func TestTargetGitKeyProfileBindingAndScope(t *testing.T) {
	s, b, host, legacy, _ := gitControllerFixture(t)
	target, _ := s.readTarget(host)
	keyID := "12345678-1234-4234-8234-123456789012"
	evidence := dataOf(t, b.call("POST", "/hosts/"+host+"/git-keys", map[string]any{"id": keyID, "account": "ubuntu", "expectedHostVersion": target.Version, "operation": "GENERATE"}, 200))
	if strings.Contains(jsonText(evidence), "PRIVATE KEY") {
		t.Fatal("private key leaked")
	}
	input := map[string]any{"name": "目标账号", "sshHost": "git.example", "sshPort": 22, "sshUser": "git", "authorName": "Developer", "authorEmail": "dev@example.com", "keySource": "TARGET", "hostId": host, "systemAccount": "ubuntu", "keyId": keyID, "keyPath": "/spoofed/key", "publicKey": "spoofed", "expectedHostVersion": target.Version, "hostPublicKey": legacy["hostPublicKey"], "hostKeyConfirmed": true}
	input["authMethod"] = "SSH"
	a := dataOf(t, b.call("POST", "/git-accounts", input, 200))
	if a["keyPath"] != evidence["keyPath"] || a["publicKey"] != evidence["publicKey"] || a["credentialId"] != "" {
		t.Fatal("unverified key data saved", a)
	}
	wrong := map[string]any{"expectedHostVersion": target.Version, "action": "BIND", "account": "another", "repoPath": "/home/another/project", "profileId": a["id"], "expectedProfileVersion": a["version"]}
	r := b.call("POST", "/hosts/"+host+"/git-repository-plans", wrong, 409)
	if !strings.Contains(r.Body.String(), "GIT_KEY_SCOPE_MISMATCH") {
		t.Fatal(r.Body.String())
	}
	p := gitPlanFixture(t, b, host, a, nil, "BIND")
	accepted := dataOf(t, b.call("POST", "/git-repository-plans/"+p["id"].(string)+"/submit", map[string]any{"confirmed": true, "idempotencyKey": "target-key-binding"}, 202))
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", accepted["jobId"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processGitRepository(context.Background(), accepted["jobId"].(string), raw)
	row, err := readGitRepository(s.DB, accepted["bindingId"].(string))
	if err != nil || row.Status != "BOUND" || row.LastJobStatus != "SUCCEEDED" {
		t.Fatal(row, err)
	}
}

func TestGitAccountOptionalConfigurationDoesNotRequireRepositoryOrCommitIdentity(t *testing.T) {
	s, b, host, legacy, _ := gitControllerFixture(t)
	target, _ := s.readTarget(host)
	in := map[string]any{"name": "内网 GitLab", "sshHost": "gitlab.internal.test", "sshPort": 2222, "sshUser": "git", "credentialId": legacy["credentialId"], "credentialRevision": legacy["credentialRevision"], "authorName": "", "authorEmail": "", "hostPublicKey": "", "hostKeyConfirmed": false}
	in["authMethod"], in["keySource"], in["hostId"], in["systemAccount"], in["keyId"], in["expectedHostVersion"] = "SSH", "TARGET", host, "ubuntu", "12345678-1234-4234-8234-123456789012", target.Version
	saved := dataOf(t, b.call("POST", "/git-accounts", in, 200))
	if saved["hostPublicKey"] != "" || saved["sshHost"] != "gitlab.internal.test" {
		t.Fatal(saved)
	}
	a, err := scanGitAccount(s.DB.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=?", saved["id"]))
	if err != nil || a.request().Validate() == nil {
		t.Fatal("incomplete account must never bypass repository connection checks", err)
	}
	in["hostPublicKey"] = legacy["hostPublicKey"]
	response := b.call("POST", "/git-accounts", in, 409)
	if !strings.Contains(response.Body.String(), "GIT_HOST_KEY_INVALID") {
		t.Fatal(response.Body.String())
	}
}

func TestGitSchema19PreservesLegacyAccountsAndBindings(t *testing.T) {
	f := schemaSixteenFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for i, source := range []string{schemaV17, schemaV18} {
		version := 17 + i
		if _, err = db.Exec(source); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", version, hashToken(source), "historical", Now()); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec("INSERT INTO git_accounts(id,name,ssh_host,ssh_port,ssh_user,author_name,author_email,credential_id,credential_revision,host_public_key,key_fingerprint,created_at,updated_at) VALUES('old-profile','Old','git.example',22,'git','Old','old@example.com',?,1,'host-key','fingerprint','created','updated')", f.credential)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO git_repositories(id,host_id,account,account_uid,repo_path,profile_id,change_json,created_at,updated_at) VALUES('old-binding',?,'ubuntu','1000','/home/ubuntu/repo','old-profile','historical-bytes','created','updated')", f.host)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := scanGitAccount(s.DB.QueryRow("SELECT " + gitAccountColumns + " FROM git_accounts WHERE id='old-profile'"))
	if err != nil || a.KeySource != "OPS" || a.CredentialID != f.credential || a.UpdatedAt != "updated" {
		t.Fatal(a, err)
	}
	var raw string
	if err = s.DB.QueryRow("SELECT change_json FROM git_repositories WHERE id='old-binding'").Scan(&raw); err != nil || raw != "historical-bytes" {
		t.Fatal(raw, err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
