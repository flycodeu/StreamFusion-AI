package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestJenkinsUnlockChecksAdministratorAndCurrentBindingWithoutJobs(t *testing.T) {
	s, b := setup(t)
	b.login()
	const bootstrap = "f1a0e9b8c7d6e5f40123456789abcdef"
	var calls atomic.Int32
	var changeVersion atomic.Bool
	var wrongInstance atomic.Bool
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		if req.Action != deploy.JenkinsUnlockReadAction {
			return nil, false
		}
		calls.Add(1)
		if req.Software == nil || req.Software.Ownership == nil || req.CommandSignature == "" || req.DeadlineAt == "" || req.JobID != "" {
			t.Error("unlock request must use a signed specific software binding with no job")
			return nil, true
		}
		softwareID := req.Software.ID
		if wrongInstance.Load() {
			softwareID = "04444444-4444-4444-8444-444444444444"
		}
		if changeVersion.Load() {
			if _, err := s.DB.Exec("UPDATE software_instances SET version=version+1 WHERE id=?", req.Software.ID); err != nil {
				t.Error(err)
			}
		}
		result, err := json.Marshal(deploy.JenkinsUnlockReadResult{ControllerID: req.ControllerID, TargetID: req.TargetID,
			SoftwareID: softwareID, BindingRevision: req.Software.Version, IdentityHash: req.Software.IdentityHash, Password: bootstrap})
		if err != nil {
			t.Error(err)
		}
		return result, true
	})
	definition, err := s.findInstallRecipe("custom-jenkins-docker-online", "2.580.2")
	if err != nil {
		t.Fatal(err)
	}
	plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{
		"expectedHostVersion": "1", "recipeKey": definition.Key, "recipeVersion": definition.Version, "recipeRevision": definition.Revision,
		"config": serviceinstall.Config{Name: "Jenkins fixture", RuntimeMode: "DOCKER", StorageRoot: "/opt/ci-jenkins", BindAddress: "127.0.0.1", HostPort: 18080, MemoryMiB: definition.DefaultMemoryMiB},
		"source": map[string]string{"type": "REGISTRY"},
	}, 200))
	receipt := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "install-for-jenkins-read"}, 202))
	job, id := receipt["jobId"].(string), receipt["softwareInstanceId"].(string)
	var frozen string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	b.h.processSoftware(context.Background(), job, frozen)
	matrixCheckJobFinished(t, s, job)
	item, err := s.readSoftware(id)
	if err != nil || item.InstallationStatus != "READY" || item.ActualState != "RUNNING" {
		t.Fatalf("fixture installation failed: %v", err)
	}
	path := "/software-instances/" + id + "/jenkins-unlock"
	call := func(method, peer string, encrypted any, status int, code string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(encrypted)
		r := httptest.NewRequest(method, "http://127.0.0.1:19090"+api+path, bytes.NewReader(body))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:19090")
		r.Header.Set("X-CSRF-Token", b.csrf)
		r.AddCookie(b.cookie)
		w := httptest.NewRecorder()
		b.h.ServeHTTP(w, r)
		var envelope Envelope
		if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil || w.Code != status || envelope.Code != code {
			t.Fatalf("unlock result: status=%d code=%s err=%v", w.Code, envelope.Code, err)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unlock result is cacheable")
		}
		if status == 200 {
			data, ok := envelope.Data.(map[string]any)
			if !ok || len(data) != 1 || data["password"] != bootstrap {
				t.Fatal("password-only response missing")
			}
		} else if envelope.Data != nil || strings.Contains(envelope.Msg, bootstrap) {
			t.Fatal("failed retrieval disclosed result data")
		}
		return w
	}
	const local = "127.0.0.1:49100"
	params := map[string]string{"expectedVersion": item.Version, "password": testPassword}
	call("GET", local, nil, 405, "METHOD_NOT_ALLOWED")
	call("POST", "198.51.100.2:49100", params, 403, "JENKINS_UNLOCK_TLS_REQUIRED")
	call("POST", local, params, 400, "SECURE_REQUEST_INVALID")
	call("POST", local, b.encrypt(path, map[string]string{"expectedVersion": item.Version, "password": strings.Repeat("验", 22)}), 400, "JENKINS_UNLOCK_PASSWORD_FORMAT")
	call("POST", local, b.encrypt(path, map[string]string{"expectedVersion": "01", "password": testPassword}), 409, "VERSION_CONFLICT")
	call("POST", local, b.encrypt(path, map[string]string{"expectedVersion": item.Version, "password": "WrongPassword1"}), 403, "JENKINS_UNLOCK_REAUTH_FAILED")
	call("POST", local, b.encrypt(path, map[string]string{"expectedVersion": "999", "password": testPassword}), 409, "VERSION_CONFLICT")
	if calls.Load() != 0 {
		t.Fatal("auth, plaintext or stale records accessed target")
	}
	var failedAttempts int
	if err = s.DB.QueryRow("SELECT failed_attempts FROM admins WHERE username='admin'").Scan(&failedAttempts); err != nil || failedAttempts != 0 {
		t.Fatal("correct reauthentication did not clear the failure streak", err)
	}
	encrypted := b.encrypt(path, params)
	call("POST", local, encrypted, 200, "SUCCESS")
	call("POST", local, encrypted, 400, "SECURE_REQUEST_INVALID")
	wrongInstance.Store(true)
	call("POST", local, b.encrypt(path, params), 409, "JENKINS_UNLOCK_BINDING_UNVERIFIED")
	wrongInstance.Store(false)
	changeVersion.Store(true)
	call("POST", local, b.encrypt(path, params), 409, "VERSION_CONFLICT")
	var jobs, audits, leaked int
	if err = s.DB.QueryRow(`SELECT (SELECT count(*) FROM jobs), (SELECT count(*) FROM audit WHERE action=?),
		(SELECT count(*) FROM audit WHERE instr(result,?)>0) + (SELECT count(*) FROM jobs WHERE instr(result_json,?)>0 OR instr(input_json,?)>0)`,
		jenkinsUnlockAudit, bootstrap, bootstrap, bootstrap).Scan(&jobs, &audits, &leaked); err != nil || jobs != 1 || audits != 4 || leaked != 0 || calls.Load() != 3 {
		t.Fatalf("unexpected records: jobs=%d audits=%d leaks=%d reads=%d err=%v", jobs, audits, leaked, calls.Load(), err)
	}
}

func TestJenkinsUnlockExecutorDiagnosticsOnlyTrustFixedCodes(t *testing.T) {
	for _, code := range []string{"SOFTWARE_INPUT_INVALID", "SOFTWARE_JOB_IDENTITY_INVALID", "JENKINS_UNLOCK_SETUP_INACTIVE", "JENKINS_UNLOCK_BINDING_UNVERIFIED", "JENKINS_UNLOCK_SECRET_UNAVAILABLE"} {
		err := errors.New("ssh exit 1")
		if got := executorFailureCode(classifyExecutorInvocation(err, code+"\n")); got != code {
			t.Fatalf("expected %s, got %s", code, got)
		}
		if got := executorFailureCode(classifyExecutorInvocation(err, code+"\nprivate payload")); got != "EXECUTOR_CALL_FAILED" {
			t.Fatalf("unexpected classification for multi-line output: %s", got)
		}
	}
}
