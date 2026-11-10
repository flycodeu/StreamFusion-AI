package controller

import (
	"strings"
	"testing"
)

func TestGitOldExecutorStopsBeforeSendingAccountOrKey(t *testing.T) {
	s, b, host, account, fixture := gitControllerFixture(t)
	target, _ := s.readTarget(host)
	fixture.mu.Lock()
	fixture.accountVersion = 2
	fixture.repositoryVersion = 2
	accountCalls, keyCalls := fixture.accountCalls, fixture.keyCalls
	fixture.mu.Unlock()
	responses := []string{
		b.call("POST", "/git-accounts/"+account["id"].(string)+"/configure", map[string]string{"expectedVersion": "1", "expectedHostVersion": target.Version}, 409).Body.String(),
		b.call("POST", "/hosts/"+host+"/git-keys", map[string]string{"id": "23456789-1234-4234-8234-123456789012", "account": "ubuntu", "operation": "GENERATE", "expectedHostVersion": target.Version}, 409).Body.String(),
	}
	for _, response := range responses {
		if !strings.Contains(response, "EXECUTOR_GIT_UNSUPPORTED") {
			t.Fatal(response)
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.accountCalls != accountCalls || fixture.keyCalls != keyCalls {
		t.Fatal("unsupported executor received account or key mutation")
	}
}

func TestGitHTTPSAccountConfigurationRetainsOnlyCredentialReference(t *testing.T) {
	s, b, host, _, _ := gitControllerFixture(t)
	target, _ := s.readTarget(host)
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "Git token", "kind": "TOKEN", "secret": "fixture-access-token"}, 201))
	input := map[string]any{"name": "GitHub", "authMethod": "HTTPS", "username": "developer", "sshHost": "github.com", "sshPort": 443, "sshUser": "git", "systemAccount": "ubuntu", "hostId": host, "expectedHostVersion": target.Version, "credentialId": credential["id"], "credentialRevision": credential["revision"]}
	a := dataOf(t, b.call("POST", "/git-accounts", input, 200))
	if strings.Contains(jsonText(a), "fixture-access-token") || a["authMethod"] != "HTTPS" || a["configuredVersion"] != "0" {
		t.Fatal("bad account response")
	}
	id := a["id"].(string)
	body := map[string]string{"expectedVersion": "1", "expectedHostVersion": target.Version}
	for i := 0; i < 2; i++ {
		result := dataOf(t, b.call("POST", "/git-accounts/"+id+"/configure", body, 200))
		if result["status"] != "CONFIGURED" || strings.Contains(jsonText(result), "fixture-access-token") {
			t.Fatal("bad configure evidence")
		}
	}
	stored, e := scanGitAccount(s.DB.QueryRow("SELECT "+gitAccountColumns+" FROM git_accounts WHERE id=?", id))
	if e != nil || stored.ConfiguredVersion != "1" || stored.AccountUID != "1000" {
		t.Fatal(stored, e)
	}
	b.call("DELETE", "/git-accounts/"+id, map[string]string{"expectedVersion": "1"}, 409)
	b.call("POST", "/git-accounts/"+id+"/remove-configuration", body, 200)
	b.call("DELETE", "/git-accounts/"+id, map[string]string{"expectedVersion": "1"}, 200)
}
