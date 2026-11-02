package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func preparationHistory(t *testing.T, s *Store, b *browser, host target, sha, recovery string) string {
	t.Helper()
	snapshot := preparationSnapshot{Host: host, BinarySHA: sha, ScriptSHA: strings.Repeat("b", 64), RecoveryBinarySHA: recovery}
	job, _, err := b.h.acceptPreparation(preparationActor(t, b), uuid.NewString(), deploy.Digest(snapshot), snapshot, "history-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.h.finishPreparation(job, "SUCCEEDED", "PREPARATION_READY"); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestPreparationUpdateInputBindsMaterialsAndKeepsLegacySnapshots(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, true, false)
	host := f.create(t, b, "update-input")
	target, _ := s.readTarget(host["id"].(string))
	b.h.targetPackageDir = targetPreparationFixture(t)
	material, _ := readTargetPackage(b.h.targetPackageDir)
	input := preparationInput{ExpectedVersion: target.Version, Key: uuid.NewString(), Confirmed: true, UpdateOnly: true, ExpectedPackageSHA256: material.Binary.Hash}
	path := "/hosts/" + target.ID + "/preparation/install"
	before := f.Commands.Load()
	b.call("POST", path, input, 400)
	input.ExpectedCurrentSHA256 = strings.Repeat("a", 64)
	input.ExpectedPackageSHA256 = strings.Repeat("b", 64)
	if !strings.Contains(b.call("POST", path, input, 409).Body.String(), "PREPARATION_MATERIAL_CHANGED") {
		t.Fatal("changed material accepted")
	}
	input.UpdateOnly = false
	b.call("POST", path, input, 400)
	if f.Commands.Load() != before {
		t.Fatal("invalid request reached target")
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE target_id=?", target.ID).Scan(&count)
	if count != 0 {
		t.Fatal("invalid request created job")
	}
	legacy := preparationSnapshot{Host: target, BinarySHA: material.Binary.Hash, ScriptSHA: material.Script.Hash}
	if strings.Contains(jsonText(legacy), "updateFromSHA256") {
		t.Fatal("legacy snapshot JSON changed")
	}
}

func TestPreparationUpdateObservationRequiresActualKnownBinary(t *testing.T) {
	for _, scenario := range []string{"current-without-history", "known-old", "recovery-old", "unknown-old", "changed-identity-history", "mismatched-owner", "unreadable", "maintenance", "unknown-job", "ack-pending"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, false)
			host := f.create(t, b, "update-"+scenario)
			target, _ := s.readTarget(host["id"].(string))
			b.h.targetPackageDir = targetPreparationFixture(t)
			material, _ := readTargetPackage(b.h.targetPackageDir)
			sha := strings.Repeat("a", 64)
			if scenario == "current-without-history" {
				sha = material.Binary.Hash
			}
			var history string
			if scenario != "unknown-old" && scenario != "current-without-history" {
				old := target
				if scenario == "changed-identity-history" {
					old.User = "different"
				}
				if scenario == "recovery-old" {
					history = preparationHistory(t, s, b, old, strings.Repeat("c", 64), sha)
				} else {
					history = preparationHistory(t, s, b, old, sha, "")
				}
			}
			if scenario == "maintenance" {
				s.DB.Exec("UPDATE targets SET maintenance=1 WHERE id=?", target.ID)
			}
			if scenario == "unknown-job" {
				s.DB.Exec("UPDATE jobs SET status='UNKNOWN' WHERE id=?", history)
			}
			if scenario == "ack-pending" {
				s.DB.Exec("UPDATE jobs SET target_ack_pending=1 WHERE id=?", history)
			}
			handler := func(command string) *hostSSHResponse {
				if scenario == "mismatched-owner" && command == executorSSHCommand(target) {
					return &hostSSHResponse{Stdout: fmt.Sprintf(`{"owner":{"protocolVersion":1,"executorVersion":"0.2.0","controllerId":%q,"targetId":%q},"platform":"linux/amd64"}`, s.controllerID(), uuid.NewString())}
				}
				if strings.HasPrefix(command, "sha256sum -- ") {
					if scenario == "unreadable" {
						return &hostSSHResponse{Exit: 1}
					}
					return &hostSSHResponse{Stdout: sha + "  " + target.InstallRoot + "/executor\n"}
				}
				return nil
			}
			f.Responses.Store(&handler)
			base := "/hosts/" + target.ID + "/preparation"
			before := f.Commands.Load()
			plain := dataOf(t, b.call("GET", base, nil, 200))
			if plain["update"] != nil || f.Commands.Load() != before {
				t.Fatal("ordinary GET probed remote or added update", plain)
			}
			data := dataOf(t, b.call("GET", base+"?checkUpdate=1", nil, 200))
			got := data["update"].(map[string]any)
			want := "MANUAL_REQUIRED"
			switch scenario {
			case "current-without-history":
				want = "CURRENT"
			case "known-old", "recovery-old":
				want = "AVAILABLE"
			case "maintenance", "unknown-job", "ack-pending":
				want = "BLOCKED"
			}
			if got["status"] != want {
				t.Fatalf("got %v want %s", got, want)
			}
			var count int
			s.DB.QueryRow("SELECT count(*) FROM jobs WHERE target_id=?", target.ID).Scan(&count)
			if count > 1 {
				t.Fatal("observation created a job")
			}
		})
	}
}

func TestPreparationAutomaticUpdateLocksFreezesAndNeverInitializes(t *testing.T) {
	for _, scenario := range []string{"success", "sudo-required", "sudo-denied", "interrupted", "changed-before-start", "changed-after-upload", "material-after-upload", "unknown-history"} {
		t.Run(scenario, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			f := hostSSHServer(t, s, b, true, false)
			host := f.create(t, b, "auto-"+scenario)
			target, _ := s.readTarget(host["id"].(string))
			b.h.targetPackageDir = targetPreparationFixture(t)
			material, _ := readTargetPackage(b.h.targetPackageDir)
			oldSHA := strings.Repeat("a", 64)
			if scenario != "unknown-history" {
				preparationHistory(t, s, b, target, oldSHA, "")
			}
			var current atomic.Value
			current.Store(oldSHA)
			if scenario == "changed-before-start" {
				current.Store(strings.Repeat("d", 64))
			}
			var uploads, privileged atomic.Int32
			handler := func(command string) *hostSSHResponse {
				switch {
				case strings.HasPrefix(command, "sha256sum -- "):
					return &hostSSHResponse{Stdout: current.Load().(string) + "  " + target.InstallRoot + "/executor\n"}
				case strings.Contains(command, "&& cat >"):
					uploads.Add(1)
					return &hostSSHResponse{Input: func(data []byte) {
						gz, err := gzip.NewReader(bytes.NewReader(data))
						if err != nil {
							t.Error(err)
							return
						}
						defer gz.Close()
						reader := tar.NewReader(gz)
						found := false
						for {
							header, err := reader.Next()
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Error(err)
								return
							}
							if header.Name == "install.sh" {
								body, _ := io.ReadAll(reader)
								script := string(body)
								found = true
								if !strings.Contains(script, "--expected-current-sha256 '"+oldSHA+"'") || !strings.Contains(script, "target-upgrade") || strings.Contains(script, "--authorize-only") || strings.Contains(script, "target-repair") || strings.Contains(script, "--apply") {
									t.Error("automatic bundle contains unsafe bootstrap", script)
								}
							}
						}
						if !found {
							t.Error("no installer")
						}
						if scenario == "changed-after-upload" {
							current.Store(strings.Repeat("e", 64))
						}
						if scenario == "material-after-upload" {
							b.h.targetPackageDir = "missing-fixture-release"
						}
					}}
				case strings.Contains(command, "SFOPS_SETUP_ENTERED"):
					privileged.Add(1)
					if scenario == "sudo-denied" {
						return &hostSSHResponse{Exit: 1, Stderr: "sudo: user is not allowed to execute this command\n"}
					}
					if scenario == "sudo-required" && !strings.HasPrefix(command, "/usr/bin/sudo -S") {
						return &hostSSHResponse{Exit: 1, Stderr: "sudo: a password is required\n"}
					}
					if scenario == "interrupted" {
						return &hostSSHResponse{Exit: 1, Stderr: "SFOPS_SETUP_ENTERED\n"}
					}
					current.Store(material.Binary.Hash)
					return &hostSSHResponse{Stdout: "SFOPS_SETUP_READY\n", Input: func(data []byte) {
						if scenario == "sudo-required" && (string(data) != "only-this-request-password\n" || strings.Contains(command, "only-this-request-password")) {
							t.Error("sudo password was not stdin-only")
						}
					}}
				case strings.Contains(command, "rmdir --"):
					return &hostSSHResponse{}
				}
				return nil
			}
			f.Responses.Store(&handler)
			input := preparationInput{ExpectedVersion: target.Version, Key: uuid.NewString(), Confirmed: true, UpdateOnly: true, ExpectedCurrentSHA256: oldSHA, ExpectedPackageSHA256: material.Binary.Hash}
			path := "/hosts/" + target.ID + "/preparation/install"
			result := dataOf(t, b.call("POST", path, input, 200))
			want := "FAILED"
			if scenario == "success" {
				want = "SUCCEEDED"
			}
			if scenario == "interrupted" {
				want = "UNKNOWN"
			}
			if result["status"] != want {
				t.Fatalf("got %v want %s", result, want)
			}
			if scenario == "sudo-denied" && result["errorCode"] != "PREPARATION_SUDO_DENIED" {
				t.Fatal("denied permission mislabeled as password challenge", result)
			}
			if scenario == "success" || scenario == "sudo-required" || scenario == "sudo-denied" || scenario == "interrupted" {
				if privileged.Load() != 1 {
					t.Fatal("missing dispatch")
				}
			} else if privileged.Load() != 0 {
				t.Fatal("changed state reached privileged installation")
			}
			if (scenario == "changed-before-start" || scenario == "unknown-history") && uploads.Load() != 0 {
				t.Fatal("untrusted update uploaded")
			}
			var locked int
			s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+target.ID).Scan(&locked)
			if (locked == 1) != (scenario == "interrupted") {
				t.Fatal("incorrect lock retention", locked)
			}
			if scenario != "material-after-upload" {
				again := dataOf(t, b.call("POST", path, input, 200))
				if again["id"] != result["id"] {
					t.Fatal("repeat did not return original job")
				}
				if privileged.Load() > 1 {
					t.Fatal("replayed update")
				}
			}
			if scenario == "sudo-required" {
				input.Key = uuid.NewString()
				input.SudoPassword = "only-this-request-password"
				retried := dataOf(t, b.call("POST", path, input, 200))
				if retried["status"] != "SUCCEEDED" || retried["id"] == result["id"] || privileged.Load() != 2 {
					t.Fatal("password challenge did not resume through new ordinary job", retried)
				}
				var raw string
				s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", retried["id"]).Scan(&raw)
				if strings.Contains(raw, input.SudoPassword) || strings.Contains(jsonText(retried), input.SudoPassword) {
					t.Fatal("transient password persisted or returned")
				}
			}
			var softwareJobs int
			s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='SOFTWARE_INSTALL'").Scan(&softwareJobs)
			if softwareJobs != 0 {
				t.Fatal("update submitted software")
			}
		})
	}
}
