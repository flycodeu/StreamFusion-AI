package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

func preparationActor(t *testing.T, b *browser) *session {
	t.Helper()
	r := httptest.NewRequest("GET", "http://127.0.0.1:19090/", nil)
	r.AddCookie(b.cookie)
	actor, err := b.h.getSession(r)
	if err != nil || actor == nil || actor.AdminID == "" {
		t.Fatal("missing authenticated session", err)
	}
	return actor
}

func TestHostPreparationEncryptedRequestAndVersionGuards(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	host := f.create(t, b, "prepare-security")
	path := "/hosts/" + host["id"].(string) + "/preparation/install"
	input := preparationInput{ExpectedVersion: "1", Key: uuid.NewString(), Confirmed: true, SudoPassword: "transient-sudo-test"}
	w := b.rawCall("POST", path, input, 400)
	if !strings.Contains(w.Body.String(), "SECURE_REQUEST_INVALID") || strings.Contains(w.Body.String(), input.SudoPassword) {
		t.Fatal("plaintext preparation was accepted or disclosed")
	}
	input.ExpectedVersion = "0"
	if !strings.Contains(b.call("POST", path, input, 409).Body.String(), "VERSION_CONFLICT") {
		t.Fatal("stale host version accepted")
	}
	input.ExpectedVersion, input.Confirmed = "1", false
	b.call("POST", path, input, 400)
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='HOST_PREPARE'").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid request created a job", err)
	}
	b.call("POST", "/auth/logout", nil, 200)
	b.rawCall("POST", path, input, 401)
}

func TestHostPreparationSSHResultsIdempotencyAndRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "sudo-required", "interrupted", "wrong-digest", "arm64", "host-changed", "root", "invalid-reply-once", "invalid-reply-always"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, false)
			var host map[string]any
			if scenario == "root" {
				input := f.connection()
				input["sshUser"] = "root"
				input["fingerprintToken"] = dataOf(t, b.call("POST", "/hosts/fingerprint", input, 200))["fingerprintToken"]
				input["kind"], input["name"] = "SSH", "root-prepare"
				host = dataOf(t, b.call("POST", "/hosts", input, 201))
			} else {
				host = f.create(t, b, "prepare-"+scenario)
			}
			target, err := s.readTarget(host["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			actor := preparationActor(t, b)
			p, material := preparationForHost(target, s.controllerID(), targetPreparationFixture(t))
			archive, err := createTargetPreparationArchive(p, material)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(archive.Name())
			defer archive.Close()
			key := uuid.NewString()
			snapshot := preparationSnapshot{Host: target, BinarySHA: material.Binary.Hash, ScriptSHA: material.Script.Hash}
			hash := deploy.Digest([]string{target.ID, target.Version, material.Binary.Hash, material.Script.Hash})
			job, reused, err := b.h.acceptPreparation(actor, key, hash, snapshot, "preparation-test")
			if err != nil || reused {
				t.Fatal(job, reused, err)
			}
			if again, reused, err := b.h.acceptPreparation(actor, key, hash, snapshot, "repeated"); err != nil || !reused || again != job {
				t.Fatal("repeat submit did not reuse job", err)
			}
			if _, _, err := b.h.acceptPreparation(actor, key, "different", snapshot, "conflict"); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
				t.Fatal("changed duplicate accepted", err)
			}
			if _, _, err := b.h.acceptPreparation(actor, uuid.NewString(), hash, snapshot, "busy"); err == nil || err.Error() != "HOST_BUSY" {
				t.Fatal("parallel operation accepted", err)
			}

			var privileged, uploads atomic.Int32
			var capabilityCalls atomic.Int32
			var receivedPassword, correctDigest atomic.Bool
			correctDigest.Store(scenario != "wrong-digest")
			password := "test-only-sudo-$'secret"
			if scenario == "sudo-required" || scenario == "root" {
				password = ""
			}
			handler := func(command string) *hostSSHResponse {
				switch {
				case command == executorSSHCommand(target) && strings.HasPrefix(scenario, "invalid-reply-"):
					if capabilityCalls.Add(1) == 1 || scenario == "invalid-reply-always" {
						return &hostSSHResponse{Stdout: `{"owner":`}
					}
					return nil
				case command == hostReadOnlyCommand && scenario == "arm64":
					return &hostSSHResponse{Stdout: "SFOPS_HOST_V1\nLinux\naarch64\nDOCKER_MISSING\n"}
				case strings.Contains(command, "&& cat >"):
					uploads.Add(1)
					return &hostSSHResponse{Input: func(input []byte) {
						if len(input) == 0 {
							t.Error("empty upload")
						}
						if scenario == "host-changed" {
							_, _ = s.DB.Exec("UPDATE targets SET maintenance=1,version=version+1 WHERE id=?", target.ID)
						}
					}}
				case strings.Contains(command, "SFOPS_SETUP_ENTERED"):
					privileged.Add(1)
					if strings.Contains(command, password) && password != "" {
						t.Error("password included in command")
					}
					if scenario == "sudo-required" {
						return &hostSSHResponse{Stderr: "sudo: a password is required\n", Exit: 1}
					}
					if scenario == "interrupted" {
						return &hostSSHResponse{Stderr: "SFOPS_SETUP_ENTERED\nsudo unrelated diagnostic\n", Exit: 1}
					}
					return &hostSSHResponse{Stdout: "installation complete\nSFOPS_SETUP_READY\n", Input: func(input []byte) {
						receivedPassword.Store(string(input) == password+"\n" || scenario == "root" && len(input) == 0)
					}}
				case strings.HasPrefix(command, "sha256sum -- "):
					sha := material.Binary.Hash
					if !correctDigest.Load() {
						sha = strings.Repeat("0", 64)
					}
					return &hostSSHResponse{Stdout: sha + "  " + target.InstallRoot + "/executor\n"}
				case scenario == "root" && command == executorSSHCommand(target):
					return &hostSSHResponse{Stdout: fmt.Sprintf(`{"owner":{"protocolVersion":1,"executorVersion":"0.2.0","controllerId":%q,"targetId":%q,"dockerSocket":"","dockerBinary":""},"platform":"linux/amd64","dockerAvailable":false,"softwareLifecycleVersion":1}`, s.controllerID(), target.ID)}
				case strings.Contains(command, "rmdir --"):
					return &hostSSHResponse{}
				}
				return nil
			}
			f.Responses.Store(&handler)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			code, uncertain := b.h.runHostPreparation(ctx, actor, job, target, archive, material.Binary.Hash, password, false)
			want := map[string]string{"success": "PREPARATION_READY", "root": "PREPARATION_READY", "sudo-required": "PREPARATION_SUDO_REQUIRED", "arm64": "PREPARATION_PLATFORM_UNSUPPORTED", "host-changed": "PREPARATION_HOST_CHANGED", "interrupted": "PREPARATION_RESULT_UNKNOWN", "wrong-digest": "EXECUTOR_HASH_MISMATCH", "invalid-reply-once": "PREPARATION_READY", "invalid-reply-always": "EXECUTOR_RESPONSE_INVALID"}[scenario]
			if code != want || uncertain != (scenario == "interrupted" || scenario == "wrong-digest" || scenario == "invalid-reply-always") {
				t.Fatalf("code=%s uncertain=%v, expected %s", code, uncertain, want)
			}
			if strings.HasPrefix(scenario, "invalid-reply-") && (capabilityCalls.Load() != 2 || privileged.Load() != 1) {
				t.Fatal("invalid reply did not get exactly one read-only retry", capabilityCalls.Load(), privileged.Load())
			}
			if (scenario == "success" || scenario == "root") && !receivedPassword.Load() {
				t.Fatal("password was not passed only through stdin")
			}
			if scenario == "arm64" && uploads.Load() != 0 || (scenario == "arm64" || scenario == "host-changed") && privileged.Load() != 0 {
				t.Fatal("rejected host reached installation")
			}
			status := "FAILED"
			if uncertain {
				status = "UNKNOWN"
			} else if code == "PREPARATION_READY" {
				status = "SUCCEEDED"
			}
			if err := b.h.finishPreparation(job, status, code); err != nil {
				t.Fatal(err)
			}
			var finalPhase string
			if err := s.DB.QueryRow("SELECT phase FROM jobs WHERE id=?", job).Scan(&finalPhase); err != nil {
				t.Fatal(err)
			}
			wantPhase := map[string]string{"success": "VERIFYING_EXECUTOR", "root": "VERIFYING_EXECUTOR", "sudo-required": "INSTALLING_EXECUTOR", "arm64": "CONNECTING", "host-changed": "UPLOADING_EXECUTOR", "interrupted": "INSTALLING_EXECUTOR", "wrong-digest": "VERIFYING_EXECUTOR", "invalid-reply-once": "VERIFYING_EXECUTOR", "invalid-reply-always": "VERIFYING_EXECUTOR"}[scenario]
			if finalPhase != wantPhase {
				t.Fatalf("finalization erased progress phase: got %s want %s", finalPhase, wantPhase)
			}
			if scenario == "interrupted" {
				_, _ = s.DB.Exec("UPDATE jobs SET status='RUNNING' WHERE id=?", job)
				restart, stop := context.WithCancel(context.Background())
				stop()
				b.h.RunOperations(restart)
				var recovered, recoveredCode string
				if err := s.DB.QueryRow("SELECT status,error_code FROM jobs WHERE id=?", job).Scan(&recovered, &recoveredCode); err != nil || recovered != "UNKNOWN" || recoveredCode != "CONTROLLER_RESTARTED" || privileged.Load() != 1 {
					t.Fatal("restart replayed or lost interrupted preparation", err)
				}
			}
			var locks int
			if err := s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks); err != nil || locks != boolInt(uncertain) {
				t.Fatal("incorrect lock disposition", locks, err)
			}
			if scenario == "wrong-digest" {
				b.call("POST", "/jobs/"+job+"/reconcile", nil, 409)
				b.call("PATCH", "/hosts/"+target.ID+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": target.Version}, 200)
				b.call("POST", "/hosts/"+target.ID+"/preparation/reconcile", map[string]string{"jobId": job, "expectedVersion": target.Version}, 409)
				correctDigest.Store(true)
				result := dataOf(t, b.call("POST", "/jobs/"+job+"/reconcile", nil, 200))
				if result["status"] != "SUCCEEDED" || privileged.Load() != 1 {
					t.Fatal("reconciliation replayed installation or failed to commit")
				}
			}
			var persisted string
			if err := s.DB.QueryRow("SELECT input_json||result_json FROM jobs WHERE id=?", job).Scan(&persisted); err != nil || strings.Contains(persisted, "test-only-sudo") {
				t.Fatal("secret persisted", err)
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestPreparationRecoveryKeepsOriginalIdentityAndReconcilesReplacement(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, true, false)
	host := f.create(t, b, "recover-replacement")
	target, err := s.readTarget(host["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	actor := preparationActor(t, b)
	_, material := preparationForHost(target, s.controllerID(), targetPreparationFixture(t))
	original := preparationSnapshot{Host: target, BinarySHA: strings.Repeat("a", 64), ScriptSHA: strings.Repeat("b", 64)}
	job, _, err := b.h.acceptPreparation(actor, uuid.NewString(), "recovery-frozen", original, "recovery-frozen")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.h.finishPreparation(job, "UNKNOWN", "PREPARATION_RESULT_UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = b.h.acceptPreparationRepair(actor, target, job, raw, material, "recovery-accepted"); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved preparationSnapshot
	if err = deploy.StrictJSON([]byte(raw), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.BinarySHA != original.BinarySHA || saved.ScriptSHA != original.ScriptSHA || saved.RecoveryBinarySHA != material.Binary.Hash || saved.RecoveryScriptSHA != material.Script.Hash {
		t.Fatal("accepted recovery lost frozen identities")
	}
	handler := func(command string) *hostSSHResponse {
		if strings.HasPrefix(command, "sha256sum -- ") {
			return &hostSSHResponse{Stdout: material.Binary.Hash + "  " + target.InstallRoot + "/executor\n"}
		}
		if strings.Contains(command, "SFOPS_SETUP_ENTERED") {
			t.Error("reconciliation replayed installation")
		}
		return nil
	}
	f.Responses.Store(&handler)
	response := b.call("POST", "/hosts/"+target.ID+"/preparation/reconcile", map[string]string{"jobId": job, "expectedVersion": target.Version}, 200)
	if dataOf(t, response)["status"] != "SUCCEEDED" {
		t.Fatal("accepted replacement could not be reconciled")
	}
	var locks int
	if err = s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks); err != nil || locks != 0 {
		t.Fatal("confirmed recovery retained lock", locks, err)
	}
}

func TestPreparationRecoveryRejectsChangedMaterialsBeforeSSH(t *testing.T) {
	s, b := setup(t)
	b.login()
	b.h.targetPackageDir = targetPreparationFixture(t)
	f := hostSSHServer(t, s, b, true, false)
	host := f.create(t, b, "recover-material-change")
	target, err := s.readTarget(host["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := preparationSnapshot{Host: target, BinarySHA: strings.Repeat("a", 64), ScriptSHA: strings.Repeat("b", 64), RecoveryBinarySHA: strings.Repeat("c", 64), RecoveryScriptSHA: strings.Repeat("d", 64)}
	job, _, err := b.h.acceptPreparation(preparationActor(t, b), uuid.NewString(), "material-change", snapshot, "material-change")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.h.finishPreparation(job, "UNKNOWN", "PREPARATION_RESULT_UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	response := b.call("POST", "/hosts/"+target.ID+"/preparation/repair", preparationRepairInput{JobID: job, ExpectedVersion: target.Version, Confirmed: true}, 409)
	if !strings.Contains(response.Body.String(), "PREPARATION_RECOVERY_MATERIAL_CHANGED") || f.Commands.Load() != 0 {
		t.Fatal("changed recovery materials reached SSH", response.Body.String())
	}
}

func TestPreparationFailureKeepsOnlyStableDiagnosticCodes(t *testing.T) {
	if got := preparationScriptFailure("SFOPS_SETUP_ENTERED\nSFOPS_ERROR=PREPARATION_AUTHORIZATION_CONFLICT\nprivate output"); got != "PREPARATION_AUTHORIZATION_CONFLICT" {
		t.Fatal(got)
	}
	if got := preparationScriptFailure("SFOPS_ERROR=arbitrary-private-output\n"); got != "PREPARATION_RESULT_UNKNOWN" {
		t.Fatal(got)
	}
	if preparationVerificationMessage("EXECUTOR_MISSING") == preparationVerificationMessage("EXECUTOR_HASH_UNAVAILABLE") {
		t.Fatal("missing and unreadable still share a diagnosis")
	}
}

func targetArchiveEntry(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if header.Name == name {
			content, readErr := io.ReadAll(reader)
			if readErr != nil {
				t.Fatal(readErr)
			}
			return content
		}
	}
	t.Fatalf("archive entry %s not found", name)
	return nil
}

func TestHostPreparationRepairRequiresOriginalUnknownLock(t *testing.T) {
	for _, scenario := range []string{"terminal", "lock-missing"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, false)
			host := f.create(t, b, "repair-guard-"+scenario)
			target, err := s.readTarget(host["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			actor := preparationActor(t, b)
			snapshot := preparationSnapshot{Host: target, BinarySHA: strings.Repeat("a", 64), ScriptSHA: strings.Repeat("b", 64)}
			job, _, err := b.h.acceptPreparation(actor, uuid.NewString(), "repair-guard-"+scenario, snapshot, "repair-guard")
			if err != nil {
				t.Fatal(err)
			}
			if err = b.h.finishPreparation(job, "UNKNOWN", "PREPARATION_RESULT_UNKNOWN"); err != nil {
				t.Fatal(err)
			}
			if got := b.h.preparationRepairSHA(target); got != snapshot.BinarySHA {
				t.Fatal("protected unknown job did not expose its bound repair digest", got)
			}
			if scenario == "terminal" {
				if err = b.h.finishPreparation(job, "FAILED", "PREPARATION_REPAIR_UNCONFIRMED"); err != nil {
					t.Fatal(err)
				}
			} else if _, err = s.DB.Exec("DELETE FROM operation_locks WHERE job_id=?", job); err != nil {
				t.Fatal(err)
			}
			if got := b.h.preparationRepairSHA(target); got != "" {
				t.Fatal("unprotected job still exposed a repair digest", got)
			}
			response := b.call("POST", "/hosts/"+target.ID+"/preparation/repair", preparationRepairInput{JobID: job, ExpectedVersion: target.Version, Confirmed: true}, 409)
			if !strings.Contains(response.Body.String(), "PREPARATION_REPAIR_NOT_ALLOWED") || f.Commands.Load() != 0 {
				t.Fatal("repair guard allowed remote execution", response.Body.String(), f.Commands.Load())
			}
		})
	}
}

func TestHostPreparationRepairEncryptedSuccessAndInterruptedRetention(t *testing.T) {
	for _, scenario := range []string{"success", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			b.h.targetPackageDir = targetPreparationFixture(t)
			f := hostSSHServer(t, s, b, true, false)
			host := f.create(t, b, "repair-"+scenario)
			target, err := s.readTarget(host["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			actor := preparationActor(t, b)
			_, material := preparationForHost(target, s.controllerID(), b.h.targetPackageDir)
			originalSHA := strings.Repeat("a", 64)
			if originalSHA == material.Binary.Hash {
				t.Fatal("test requires distinct original and current executors")
			}
			snapshot := preparationSnapshot{Host: target, BinarySHA: originalSHA, ScriptSHA: strings.Repeat("b", 64)}
			job, _, err := b.h.acceptPreparation(actor, uuid.NewString(), "repair-"+scenario, snapshot, "repair-test")
			if err != nil {
				t.Fatal(err)
			}
			if err = b.h.finishPreparation(job, "UNKNOWN", "PREPARATION_RESULT_UNKNOWN"); err != nil {
				t.Fatal(err)
			}

			password := "repair-only-sudo-secret"
			path := "/hosts/" + target.ID + "/preparation/repair"
			plaintext := b.rawCall("POST", path, preparationRepairInput{JobID: job, ExpectedVersion: target.Version, Confirmed: true, SudoPassword: password}, 400)
			if !strings.Contains(plaintext.Body.String(), "SECURE_REQUEST_INVALID") || strings.Contains(plaintext.Body.String(), password) {
				t.Fatal("plaintext repair was accepted or disclosed")
			}

			var uploaded atomic.Pointer[[]byte]
			var privileged, passwordReceived atomic.Bool
			handler := func(command string) *hostSSHResponse {
				switch {
				case strings.Contains(command, "&& cat >"):
					return &hostSSHResponse{Input: func(input []byte) {
						copyOfInput := append([]byte(nil), input...)
						uploaded.Store(&copyOfInput)
					}}
				case strings.Contains(command, "SFOPS_SETUP_ENTERED"):
					privileged.Store(true)
					if strings.Contains(command, password) || !strings.Contains(command, "--repair-only --non-interactive") {
						t.Error("repair command disclosed the password or omitted repair mode")
					}
					if scenario == "interrupted" {
						return &hostSSHResponse{Stderr: "SFOPS_SETUP_ENTERED\nconnection closed\n", Exit: 1, Input: func(input []byte) { passwordReceived.Store(string(input) == password+"\n") }}
					}
					return &hostSSHResponse{Stdout: "SFOPS_SETUP_READY\n", Input: func(input []byte) { passwordReceived.Store(string(input) == password+"\n") }}
				case strings.HasPrefix(command, "sha256sum -- "):
					return &hostSSHResponse{Stdout: material.Binary.Hash + "  " + target.InstallRoot + "/executor\n"}
				case strings.Contains(command, "rmdir --"):
					return &hostSSHResponse{}
				}
				return nil
			}
			f.Responses.Store(&handler)
			want := 200
			if scenario == "interrupted" {
				want = 409
			}
			response := b.call("POST", path, preparationRepairInput{JobID: job, ExpectedVersion: target.Version, Confirmed: true, SudoPassword: password}, want)
			if !privileged.Load() || !passwordReceived.Load() {
				t.Fatal("repair did not use the transient sudo input")
			}
			archive := uploaded.Load()
			if archive == nil {
				t.Fatal("repair package was not uploaded")
			}
			installer := targetArchiveEntry(t, *archive, "install.sh")
			if !bytes.Contains(installer, []byte(originalSHA)) || !bytes.Contains(installer, []byte("target-repair")) {
				t.Fatal("repair package was not bound to the original executor digest")
			}
			var status, persisted string
			var locks int
			if err = s.DB.QueryRow("SELECT status,input_json||result_json FROM jobs WHERE id=?", job).Scan(&status, &persisted); err != nil {
				t.Fatal(err)
			}
			if err = s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(persisted, password) {
				t.Fatal("repair password was persisted")
			}
			if scenario == "success" {
				if status != "SUCCEEDED" || locks != 0 {
					t.Fatal("confirmed repair did not release the original lock", status, locks)
				}
			} else if status != "UNKNOWN" || locks != 1 || !strings.Contains(response.Body.String(), "PREPARATION_REPAIR_UNCONFIRMED") {
				t.Fatal("interrupted repair lost the original protected job", status, locks, response.Body.String())
			}
		})
	}
}

func TestHostPreparationReconcileFinalization(t *testing.T) {
	for _, scenario := range []string{"success", "maintenance", "host-changed", "session-revoked", "commit-failed"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, false)
			host := f.create(t, b, "reconcile-"+scenario)
			target, err := s.readTarget(host["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			actor := preparationActor(t, b)
			digest := strings.Repeat("a", 64)
			snapshot := preparationSnapshot{Host: target, BinarySHA: digest, ScriptSHA: strings.Repeat("b", 64)}
			job, _, err := b.h.acceptPreparation(actor, uuid.NewString(), "reconcile-finalization", snapshot, "reconcile-finalization")
			if err != nil {
				t.Fatal(err)
			}
			if err = b.h.finishPreparation(job, "UNKNOWN", "PREPARATION_RESULT_UNKNOWN"); err != nil {
				t.Fatal(err)
			}
			if scenario == "maintenance" {
				b.call("PATCH", "/hosts/"+target.ID+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": target.Version}, 200)
				target, err = s.readTarget(target.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "commit-failed" {
				_, err = s.DB.Exec(`CREATE TRIGGER reject_preparation_result BEFORE INSERT ON audit WHEN NEW.action='HOST_PREPARATION_RESULT' BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			var verifications atomic.Int32
			handler := func(command string) *hostSSHResponse {
				if !strings.HasPrefix(command, "sha256sum -- ") {
					return nil
				}
				verifications.Add(1)
				switch scenario {
				case "host-changed":
					_, err := s.DB.Exec("UPDATE targets SET maintenance=1,version=version+1 WHERE id=?", target.ID)
					if err != nil {
						t.Error(err)
					}
				case "session-revoked":
					_, err := s.DB.Exec("DELETE FROM sessions WHERE token_hash=?", actor.Hash)
					if err != nil {
						t.Error(err)
					}
				}
				return &hostSSHResponse{Stdout: digest + "  " + target.InstallRoot + "/executor\n"}
			}
			f.Responses.Store(&handler)
			wantHTTP := map[string]int{"success": 200, "maintenance": 200, "host-changed": 409, "session-revoked": 401, "commit-failed": 500}[scenario]
			response := b.call("POST", "/hosts/"+target.ID+"/preparation/reconcile", map[string]string{"jobId": job, "expectedVersion": target.Version}, wantHTTP)
			if scenario == "host-changed" && !strings.Contains(response.Body.String(), "VERSION_CONFLICT") {
				t.Fatal("host change was not identified", response.Body.String())
			}
			if verifications.Load() != 1 {
				t.Fatal("reconciliation did not verify the installed digest")
			}
			var status string
			var locks, results int
			if err = s.DB.QueryRow("SELECT status FROM jobs WHERE id=?", job).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if err = s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", job).Scan(&locks); err != nil {
				t.Fatal(err)
			}
			if err = s.DB.QueryRow("SELECT count(*) FROM audit WHERE action='HOST_PREPARATION_RESULT' AND object_id=?", job).Scan(&results); err != nil {
				t.Fatal(err)
			}
			if wantHTTP == 200 {
				if status != "SUCCEEDED" || locks != 0 || results != 2 {
					t.Fatal("confirmed result did not release its lock atomically", status, locks, results)
				}
			} else if status != "UNKNOWN" || locks != 1 || results != 1 {
				t.Fatal("failed finalization lost the original unknown job or lock", status, locks, results)
			}
		})
	}
}

func TestHostPreparationCanceledRequestCleansOnlyUpload(t *testing.T) {
	for _, scenario := range []string{"upload", "installation", "fingerprint-changed"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, false, false)
			host := f.create(t, b, "cancel-"+scenario)
			target, err := s.readTarget(host["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			p, material := preparationForHost(target, s.controllerID(), targetPreparationFixture(t))
			archive, err := createTargetPreparationArchive(p, material)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(archive.Name())
			defer archive.Close()
			actor := preparationActor(t, b)
			job, _, err := b.h.acceptPreparation(actor, uuid.NewString(), "cancel-test", preparationSnapshot{Host: target, BinarySHA: material.Binary.Hash, ScriptSHA: material.Script.Hash}, "cancel-test")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var uploads, privileged, cleanups atomic.Int32
			handler := func(command string) *hostSSHResponse {
				switch {
				case strings.Contains(command, "&& cat >"):
					uploads.Add(1)
					return &hostSSHResponse{Input: func(_ []byte) {
						if scenario == "installation" {
							return
						}
						if scenario == "fingerprint-changed" {
							_, private, err := ed25519.GenerateKey(rand.Reader)
							if err != nil {
								t.Error(err)
								return
							}
							signer, err := ssh.NewSignerFromKey(private)
							if err != nil {
								t.Error(err)
								return
							}
							f.Key.Store(&signer)
						}
						cancel()
					}}
				case strings.Contains(command, "SFOPS_SETUP_ENTERED"):
					privileged.Add(1)
					return &hostSSHResponse{Input: func(_ []byte) { cancel() }}
				case strings.Contains(command, "rmdir --"):
					cleanups.Add(1)
					stage := "/tmp/sfops-upload-" + job
					if !strings.Contains(command, stage+"/bundle.tar.gz") || !strings.Contains(command, stage) || strings.Contains(command, "rm -rf") || strings.Contains(command, "SFOPS_SETUP_ENTERED") {
						t.Error("cleanup exceeded the original job upload", command)
					}
					return &hostSSHResponse{}
				}
				return nil
			}
			f.Responses.Store(&handler)
			code, uncertain := b.h.runHostPreparation(ctx, actor, job, target, archive, material.Binary.Hash, "transient-cancel-test", false)
			if uploads.Load() != 1 || privileged.Load() != int32(boolInt(scenario == "installation")) || uncertain != (scenario == "installation") {
				t.Fatal("canceled request replayed or misclassified the installation", code, uncertain, uploads.Load(), privileged.Load())
			}
			if scenario == "installation" && code != "PREPARATION_RESULT_UNKNOWN" || scenario != "installation" && code != "PREPARATION_UPLOAD_FAILED" {
				t.Fatal("canceled phase was not preserved", code)
			}
			if want := int32(boolInt(scenario != "fingerprint-changed")); cleanups.Load() != want {
				t.Fatal("cleanup did not reconnect safely with the original fingerprint", cleanups.Load(), want)
			}
		})
	}
}
