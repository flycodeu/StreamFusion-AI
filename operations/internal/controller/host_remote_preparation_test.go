package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
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
	for _, scenario := range []string{"success", "sudo-required", "interrupted", "wrong-digest", "arm64", "host-changed", "root"} {
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
			var receivedPassword, correctDigest atomic.Bool
			correctDigest.Store(scenario != "wrong-digest")
			password := "test-only-sudo-$'secret"
			if scenario == "sudo-required" || scenario == "root" {
				password = ""
			}
			handler := func(command string) *hostSSHResponse {
				switch {
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
			code, uncertain := b.h.runHostPreparation(ctx, actor, job, target, archive, material.Binary.Hash, password)
			want := map[string]string{"success": "PREPARATION_READY", "root": "PREPARATION_READY", "sudo-required": "PREPARATION_SUDO_REQUIRED", "arm64": "PREPARATION_PLATFORM_UNSUPPORTED", "host-changed": "PREPARATION_HOST_CHANGED", "interrupted": "PREPARATION_RESULT_UNKNOWN", "wrong-digest": "PREPARATION_RESULT_UNKNOWN"}[scenario]
			if code != want || uncertain != (scenario == "interrupted" || scenario == "wrong-digest") {
				t.Fatalf("code=%s uncertain=%v, expected %s", code, uncertain, want)
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
			code, uncertain := b.h.runHostPreparation(ctx, actor, job, target, archive, material.Binary.Hash, "transient-cancel-test")
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
