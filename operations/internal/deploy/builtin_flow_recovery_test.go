package deploy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestBuiltinObservationCompletesOnlyFinalSystemReceipt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	for _, operation := range []string{"install", "uninstall"} {
		for _, state := range []string{"missing", "RUNNING", "FAILED", "command-failed"} {
			t.Run(operation+"/"+state, func(t *testing.T) {
				x, req := builtinFixture(t, "java", "21-ubuntu24.04-repository")
				if operation == "uninstall" {
					req.Uninstallation = &SoftwareUninstallRequest{Installation: *req.Installation}
					req.Installation = nil
				}
				finalFile, changedCommand := "", false
				for _, step := range commandDefinition(req).Commands.Steps {
					if !slices.Contains(builtinFlowPhases(req), step.Phase) || builtinStep(req, step.ID) == nil {
						continue
					}
					file := filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+step.ID+".json")
					receipt := commandStepReceipt{Phase: step.Phase, Name: step.Name, State: "SUCCEEDED", StartedAt: "before-recovery"}
					if step.Kind == "SYSTEM" && (step.Action == "INSTALL_CONFIRM" || step.Action == "UNINSTALL_CONFIRM") {
						finalFile = file
						if state == "missing" {
							continue
						}
						receipt.State = "RUNNING"
						if state == "FAILED" {
							receipt.State = "FAILED"
						}
					} else if step.Kind == "COMMAND" && state == "command-failed" && !changedCommand {
						receipt.State, changedCommand = "FAILED", true
					}
					raw, _ := json.Marshal(receipt)
					if err := os.WriteFile(file, raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
					t.Fatal("recovery executed a command")
					return nil, nil
				}
				confirmed := x.confirmBuiltinObservation(req, Result{Status: "SUCCEEDED"})
				if confirmed != (state != "command-failed") {
					t.Fatalf("confirmed=%v", confirmed)
				}
				var final commandStepReceipt
				if readJSON(finalFile, &final) != nil {
					t.Fatal("final receipt missing")
				}
				if (final.State == "SUCCEEDED") != confirmed || confirmed && !x.builtinResultConfirmed(req, Result{Status: "SUCCEEDED"}) {
					t.Fatal("incorrect final receipt", final)
				}
				if state != "missing" && final.StartedAt != "before-recovery" {
					t.Fatal("history timestamp changed")
				}
			})
		}
	}
}

func TestNodeInstallPreparationRejectsSymlinkedCacheAndExistingVersion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target account shell requires Linux")
	}
	for _, state := range []string{"empty", "cache-link", "version-exists"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			root, version := filepath.Join(home, "environment"), "v22.23.4"
			if err := os.MkdirAll(filepath.Join(root, "nvm/.cache"), 0700); err != nil {
				t.Fatal(err)
			}
			if state == "cache-link" {
				if err := os.Symlink(home, filepath.Join(root, "nvm/.cache/bin")); err != nil {
					t.Fatal(err)
				}
			}
			if state == "version-exists" {
				if err := os.MkdirAll(filepath.Join(root, "nvm/versions/node", version), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", serviceinstall.NodeCommandScript, "fixture", "prepare-install", root, version, "", "", "", "", "")
			command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			out, err := command.CombinedOutput()
			if (err == nil) != (state == "empty") {
				t.Fatalf("err=%v output=%s", err, out)
			}
			if state == "empty" {
				if _, err := os.Lstat(filepath.Join(root, "nvm/.cache/bin")); !os.IsNotExist(err) {
					t.Fatal("read-only preparation created cache")
				}
			}
		})
	}
}
