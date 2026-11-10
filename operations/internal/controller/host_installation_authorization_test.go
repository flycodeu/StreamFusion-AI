package controller

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestInstallationAuthorizationPrivilegesUpdatesAndRecovery(t *testing.T) {
	for _, scenario := range []string{"passwordless", "password-required", "sudo-denied", "interrupted", "identity-mismatch", "changed-binary", "host-changed", "upgrade-password", "upgrade-interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			upgrade := strings.HasPrefix(scenario, "upgrade-")
			passwordRequired := scenario == "password-required" || scenario == "upgrade-password"
			interrupted := scenario == "interrupted" || scenario == "upgrade-interrupted"
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, false)
			host := f.create(t, b, "authorization-"+scenario)
			target, _ := s.readTarget(host["id"].(string))
			b.h.targetPackageDir = targetPreparationFixture(t)
			material, _ := readTargetPackage(b.h.targetPackageDir)
			var current atomic.Value
			current.Store(material.Binary.Hash)
			if upgrade {
				current.Store(strings.Repeat("a", 64))
				preparationHistory(t, s, b, target, current.Load().(string), "")
			}
			var uploads, dispatched atomic.Int32
			var authorized atomic.Bool
			handler := func(command string) *hostSSHResponse {
				switch {
				case command == executorSSHCommand(target):
					owner, fingerprint := target.ID, ""
					if scenario == "identity-mismatch" {
						owner = uuid.NewString()
					}
					if authorized.Load() {
						fingerprint = deploy.CommandPublicKeyFingerprint(s.commandPublicKey())
					}
					return &hostSSHResponse{Stdout: fmt.Sprintf(`{"owner":{"protocolVersion":1,"executorVersion":%q,"controllerId":%q,"targetId":%q},"platform":"linux/amd64","commandTrustFingerprint":%q}`, deploy.Version, s.controllerID(), owner, fingerprint)}
				case strings.HasPrefix(command, "sha256sum -- "):
					sha := current.Load().(string)
					if scenario == "changed-binary" {
						sha = strings.Repeat("e", 64)
					}
					return &hostSSHResponse{Stdout: sha + "  " + target.InstallRoot + "/executor\n"}
				case strings.Contains(command, "SFOPS_PRIVILEGE_READY"):
					if scenario == "sudo-denied" {
						return &hostSSHResponse{Exit: 1, Stderr: "sudo: user is not allowed to execute this command\n"}
					}
					if passwordRequired && !strings.HasPrefix(command, "/usr/bin/sudo -S") {
						return &hostSSHResponse{Exit: 1, Stderr: "sudo: a password is required\n"}
					}
					return &hostSSHResponse{Stdout: "SFOPS_PRIVILEGE_READY\n", Input: func(data []byte) {
						if scenario == "host-changed" {
							_, _ = s.DB.Exec("UPDATE targets SET version=version+1 WHERE id=?", target.ID)
						}
						if passwordRequired && string(data) != "request-only-password\n" {
							t.Error("missing password on privilege probe stdin")
						}
					}}
				case strings.Contains(command, "&& cat >"):
					uploads.Add(1)
					return &hostSSHResponse{}
				case strings.Contains(command, "SFOPS_SETUP_ENTERED") && upgrade:
					current.Store(material.Binary.Hash)
					return &hostSSHResponse{Stdout: "SFOPS_SETUP_READY\n", Input: func(data []byte) {
						if passwordRequired && string(data) != "request-only-password\n" {
							t.Error("upgrade did not reuse the same transient password")
						}
					}}
				case strings.Contains(command, "rmdir --"):
					return &hostSSHResponse{}
				case strings.Contains(command, "SFOPS_AUTHORIZATION_ENTERED"):
					dispatched.Add(1)
					if !strings.Contains(command, "target-trust") || strings.Contains(command, "target-upgrade") || strings.Contains(command, "target-init") || strings.Contains(command, "request-only-password") {
						t.Error("authorization used an unexpected operation", command)
					}
					if interrupted {
						return &hostSSHResponse{Exit: 1, Stderr: "SFOPS_AUTHORIZATION_ENTERED\n"}
					}
					authorized.Store(true)
					return &hostSSHResponse{Stdout: "SFOPS_AUTHORIZATION_READY\n", Input: func(data []byte) {
						if passwordRequired && string(data) != "request-only-password\n" {
							t.Error("password was not stdin-only")
						}
					}}
				}
				return nil
			}
			f.Responses.Store(&handler)
			input := preparationInput{ExpectedVersion: target.Version, Key: uuid.NewString(), Confirmed: true, AuthorizeOnly: !upgrade, UpdateOnly: upgrade, AuthorizeCommands: upgrade, ExpectedCurrentSHA256: current.Load().(string), ExpectedPackageSHA256: material.Binary.Hash}
			path := "/hosts/" + target.ID + "/preparation"
			result := dataOf(t, b.call("POST", path+"/install", input, 200))
			want := map[string]string{"passwordless": "SUCCEEDED", "interrupted": "UNKNOWN", "upgrade-interrupted": "UNKNOWN"}[scenario]
			if want == "" {
				want = "FAILED"
			}
			wantUploads := int32(0)
			if upgrade && !passwordRequired {
				wantUploads = 1
			}
			if result["status"] != want || uploads.Load() != wantUploads {
				t.Fatalf("result=%v uploads=%d", result, uploads.Load())
			}
			if passwordRequired || scenario == "sudo-denied" {
				code := "PREPARATION_SUDO_REQUIRED"
				if scenario == "sudo-denied" {
					code = "PREPARATION_SUDO_DENIED"
				}
				if result["errorCode"] != code || dispatched.Load() != 0 {
					t.Fatal("privilege failure reached authorization", result)
				}
			}
			if scenario != "host-changed" {
				before := dispatched.Load()
				again := dataOf(t, b.call("POST", path+"/install", input, 200))
				if again["id"] != result["id"] || dispatched.Load() != before {
					t.Fatal("duplicate request replayed authorization")
				}
			}
			if passwordRequired {
				input.Key, input.SudoPassword = uuid.NewString(), "request-only-password"
				result = dataOf(t, b.call("POST", path+"/install", input, 200))
				if upgrade {
					wantUploads = 1
				}
				if result["status"] != "SUCCEEDED" || dispatched.Load() != 1 || uploads.Load() != wantUploads {
					t.Fatal("password did not resume the small authorization", result)
				}
				var raw string
				s.DB.QueryRow("SELECT input_json||result_json FROM jobs WHERE id=?", result["id"]).Scan(&raw)
				if strings.Contains(raw, input.SudoPassword) || strings.Contains(jsonText(result), input.SudoPassword) {
					t.Fatal("password persisted or returned")
				}
			}
			if interrupted {
				reconcile := map[string]any{"jobId": result["id"], "expectedVersion": target.Version}
				b.call("POST", path+"/reconcile", reconcile, 409)
				b.call("POST", path+"/repair", preparationRepairInput{JobID: result["id"].(string), ExpectedVersion: target.Version, Confirmed: true}, 409)
				input.Key = uuid.NewString()
				b.call("POST", path+"/install", input, 409)
				authorized.Store(true)
				verified := dataOf(t, b.call("POST", path+"/reconcile", reconcile, 200))
				if verified["status"] != "SUCCEEDED" || dispatched.Load() != 1 {
					t.Fatal("reconciliation replayed authorization", verified)
				}
			}
			var softwareJobs, locks int
			s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='SOFTWARE_INSTALL'").Scan(&softwareJobs)
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+target.ID).Scan(&locks)
			if softwareJobs != 0 || locks != 0 {
				t.Fatal("authorization submitted software or left a finished lock", softwareJobs, locks)
			}
		})
	}
}
