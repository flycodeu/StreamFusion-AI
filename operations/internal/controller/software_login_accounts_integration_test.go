package controller

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func postLoginAccountFixture(t *testing.T, b *browser, path string, payload map[string]string, status int, code string) map[string]any {
	t.Helper()
	response := callSoftwareLogin(t, b, http.MethodPost, path, softwareLoginTestPeer, b.encrypt(path, payload), status, code)
	return dataOf(t, response)
}

func listLoginAccountFixtures(t *testing.T, b *browser, path string, secrets ...string) map[string]map[string]any {
	t.Helper()
	response := callSoftwareLogin(t, b, http.MethodGet, path, softwareLoginTestPeer, nil, http.StatusOK, "SUCCESS")
	for _, secret := range secrets {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("login account list exposed a password")
		}
	}
	rows, ok := dataOf(t, response)["accounts"].([]any)
	if !ok {
		t.Fatal("login account list is missing")
	}
	byID := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		account := row.(map[string]any)
		id, ok := account["id"].(string)
		if !ok || id == "" || byID[id] != nil || account["password"] != nil {
			t.Fatal("login account list contains invalid metadata")
		}
		byID[id] = account
	}
	return byID
}

func TestSoftwareLoginAccountsManualIsolationAndLegacyAccess(t *testing.T) {
	_, b, host := setupSoftwareLogin(t)
	item := registerTestSoftware(t, b, host, "DOCKER", "login-manual", "JENKINS")
	other := registerTestSoftware(t, b, host, "DOCKER", "login-other", "JENKINS")
	path := "/software-instances/" + item["id"].(string) + "/login-accounts"
	otherPath := "/software-instances/" + other["id"].(string) + "/login-accounts"
	legacyPath := strings.TrimSuffix(path, "s")
	const legacySecret = "fixture-old-account-password"
	const newSecret = "fixture-second-account-password"
	postLoginAccountFixture(t, b, legacyPath, map[string]string{"expectedVersion": "0", "username": "first-user", "password": legacySecret}, 200, "SUCCESS")
	initial := listLoginAccountFixtures(t, b, path, legacySecret)
	if len(initial) != 1 {
		t.Fatal("the existing single account is missing from the list")
	}
	var legacyID string
	for id, row := range initial {
		if row["username"] != "first-user" || row["source"] != "MANUAL" || row["version"] != "1" || row["hasPassword"] != true {
			t.Fatal("incorrect legacy metadata")
		}
		legacyID = id
	}

	newID := uuid.NewString()
	input := map[string]string{"id": newID, "expectedVersion": "0", "username": "second-user", "password": newSecret}
	created := postLoginAccountFixture(t, b, path, input, 200, "SUCCESS")
	if created["id"] != newID || created["version"] != "1" || created["password"] != nil {
		t.Fatal("new account returned incorrect metadata")
	}
	postLoginAccountFixture(t, b, path, input, 409, "VERSION_CONFLICT")
	rows := listLoginAccountFixtures(t, b, path, legacySecret, newSecret)
	if len(rows) != 2 || rows[newID]["username"] != "second-user" || rows[legacyID]["username"] != "first-user" {
		t.Fatal("creating a second account replaced the existing one")
	}
	if len(listLoginAccountFixtures(t, b, otherPath)) != 0 {
		t.Fatal("another instance can enumerate the saved accounts")
	}

	confirmation := map[string]string{"expectedVersion": "1", "password": testPassword}
	postLoginAccountFixture(t, b, path+"/"+newID+"/copy-password", map[string]string{"expectedVersion": "1", "password": "wrong-fixture-password"}, 403, "SOFTWARE_LOGIN_REAUTH_FAILED")
	postLoginAccountFixture(t, b, otherPath+"/"+newID+"/copy-password", confirmation, 404, "NOT_FOUND")
	update := map[string]string{"expectedVersion": "1", "username": "renamed-first-user"}
	postLoginAccountFixture(t, b, otherPath+"/"+newID, update, 404, "NOT_FOUND")
	for id, want := range map[string]string{legacyID: legacySecret, newID: newSecret} {
		result := postLoginAccountFixture(t, b, path+"/"+id+"/copy-password", confirmation, 200, "SUCCESS")
		if len(result) != 1 || result["password"] != want {
			t.Fatal("verified copy returned the wrong account password")
		}
	}
	postLoginAccountFixture(t, b, path+"/"+legacyID, update, 200, "SUCCESS")
	postLoginAccountFixture(t, b, path+"/"+legacyID, update, 409, "VERSION_CONFLICT")
	legacy := dataOf(t, callSoftwareLogin(t, b, http.MethodGet, legacyPath, softwareLoginTestPeer, nil, 200, "SUCCESS"))
	if legacy["username"] != "renamed-first-user" || legacy["version"] != "2" || legacy["hasPassword"] != true {
		t.Fatal("the original endpoint no longer addresses the same account")
	}
	if copied := postLoginAccountFixture(t, b, legacyPath+"/copy-password", map[string]string{"expectedVersion": "2", "password": testPassword}, 200, "SUCCESS"); copied["password"] != legacySecret {
		t.Fatal("editing metadata lost the legacy password")
	}
}

func TestSoftwareLoginAccountsReadInstalledCredentialRevision(t *testing.T) {
	s, b, host := setupSoftwareLogin(t)
	addCredential := func(name, password string) string {
		t.Helper()
		data := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": name, "kind": "PASSWORD", "secret": password}, 201))
		return data["id"].(string)
	}
	const rootSecret = "mysql-fixture-root-1"
	const userSecret = "mysql-fixture-user-1"
	const rotatedSecret = "mysql-fixture-root-2"
	rootRef := addCredential("fixture root", rootSecret)
	userRef := addCredential("fixture user", userSecret)
	dataOf(t, b.call("POST", "/credentials/"+rootRef+"/revisions", map[string]string{"name": "new root", "kind": "PASSWORD", "expectedRevision": "1", "secret": rotatedSecret}, 201))

	instance := registerTestSoftware(t, b, host, "DOCKER", "login-mysql", "MYSQL")
	id := instance["id"].(string)
	path := "/software-instances/" + id + "/login-accounts"
	definition, err := serviceinstall.Find("mysql", "8.4.11")
	if err != nil {
		t.Fatal(err)
	}
	installation := serviceinstall.Request{SoftwareID: id, BindingRevision: 1, Definition: definition, Config: serviceinstall.Config{
		Name: "fixture mysql", Username: "worker", Database: "app", CredentialID: rootRef, CredentialRevision: "1", UserCredentialID: userRef, UserCredentialRevision: "1",
	}}
	if _, err := s.DB.Exec("UPDATE software_instances SET management_mode='MANAGED',installation_status='READY',actual_state='RUNNING',binding_revision=1,installation_json=? WHERE id=?", jsonText(installation), id); err != nil {
		t.Fatal(err)
	}
	rows := listLoginAccountFixtures(t, b, path, rootSecret, userSecret, rotatedSecret)
	if len(rows) != 2 || rows["mysql-root"]["username"] != "root" || rows["mysql-user"]["username"] != "worker" {
		t.Fatal("both bound MySQL accounts should be available")
	}
	for accountID, password := range map[string]string{"mysql-root": rootSecret, "mysql-user": userSecret} {
		row := rows[accountID]
		if row["source"] != "INSTALL" || row["hasPassword"] != true {
			t.Fatal("bound credential metadata is incorrect")
		}
		confirm := map[string]string{"expectedVersion": row["version"].(string), "password": testPassword}
		if copy := postLoginAccountFixture(t, b, path+"/"+accountID+"/copy-password", confirm, 200, "SUCCESS"); copy["password"] != password {
			t.Fatal("copy must read the exact revision referenced by this installation")
		}
	}
	// Model a stored snapshot whose referenced credential was revoked after it was listed.
	if _, err := s.DB.Exec("UPDATE credential_refs SET revoked_at=? WHERE id=? AND revision=1", Now(), rootRef); err != nil {
		t.Fatal(err)
	}
	postLoginAccountFixture(t, b, path+"/mysql-root/copy-password", map[string]string{"expectedVersion": rows["mysql-root"]["version"].(string), "password": testPassword}, 404, "NOT_FOUND")
	remaining := listLoginAccountFixtures(t, b, path, rootSecret, userSecret, rotatedSecret)
	if len(remaining) != 2 || remaining["mysql-root"]["hasPassword"] != false || remaining["mysql-user"]["hasPassword"] != true {
		t.Fatal("the revoked root binding must stay visible and only its copy action should be unavailable")
	}
}

func TestSoftwareLoginAccountsRedisInstallAndRebinding(t *testing.T) {
	s, b, host := setupSoftwareLogin(t)
	instance := registerTestSoftware(t, b, host, "DOCKER", "login-redis", "REDIS")
	id := instance["id"].(string)
	path := "/software-instances/" + id + "/login-accounts"
	definition, err := serviceinstall.Find("redis", "7.4.11")
	if err != nil {
		t.Fatal(err)
	}
	installation := serviceinstall.Request{SoftwareID: id, BindingRevision: 1, Definition: definition, Config: serviceinstall.Config{Name: "cache"}}
	saveBinding := func() {
		t.Helper()
		if _, err := s.DB.Exec("UPDATE software_instances SET management_mode='MANAGED',installation_status='READY',actual_state='RUNNING',binding_revision=?,installation_json=? WHERE id=?", installation.BindingRevision, jsonText(installation), id); err != nil {
			t.Fatal(err)
		}
	}
	saveBinding()
	if len(listLoginAccountFixtures(t, b, path)) != 0 {
		t.Fatal("an instance with no bound Redis credential has no installation login")
	}
	const initialSecret = "redis-fixture-initial-value"
	const rotatedSecret = "redis-fixture-rotated-value"
	payload := map[string]string{"name": "fixture cache", "kind": "PASSWORD", "secret": initialSecret}
	credential := dataOf(t, b.call("POST", "/credentials", payload, 201))
	installation.Config.CredentialID = credential["id"].(string)
	installation.Config.CredentialRevision = "1"
	saveBinding()
	rows := listLoginAccountFixtures(t, b, path, initialSecret)
	row := rows["redis-default"]
	if len(rows) != 1 || row["source"] != "INSTALL" || row["username"] != "default" || row["hasPassword"] != true {
		t.Fatal("the bound default Redis account was not offered")
	}

	payload["secret"], payload["expectedRevision"] = rotatedSecret, "1"
	dataOf(t, b.call("POST", "/credentials/"+installation.Config.CredentialID+"/revisions", payload, 201))
	copyPath := path + "/redis-default/copy-password"
	confirmation := map[string]string{"expectedVersion": row["version"].(string), "password": testPassword}
	if copied := postLoginAccountFixture(t, b, copyPath, confirmation, 200, "SUCCESS"); copied["password"] != initialSecret {
		t.Fatal("creating a new credential revision changed the installed password")
	}
	installation.BindingRevision++
	installation.Config.CredentialRevision = "2"
	saveBinding()
	postLoginAccountFixture(t, b, copyPath, confirmation, 409, "VERSION_CONFLICT")
	newRow := listLoginAccountFixtures(t, b, path, initialSecret, rotatedSecret)["redis-default"]
	confirmation["expectedVersion"] = newRow["version"].(string)
	if copied := postLoginAccountFixture(t, b, copyPath, confirmation, 200, "SUCCESS"); copied["password"] != rotatedSecret {
		t.Fatal("refreshed account did not copy the rebound credential")
	}
}
