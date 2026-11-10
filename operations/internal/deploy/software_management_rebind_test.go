package deploy

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func rebindRequestFixture(t *testing.T) (*executor, Request, string) {
	t.Helper()
	original := commandTestRequest()
	i := original.Installation
	i.Definition.Kind, i.Definition.RuntimeModes, i.Definition.SourceModes = "SERVICE", []string{"DOCKER"}, []string{"REGISTRY"}
	i.Definition.Commands.Kind, i.Definition.Commands.Locator = "DOCKER", "demo-${OPS_SOFTWARE_ID}"
	i.Config.RuntimeMode, i.Config.StorageRoot, i.Source.Type = "DOCKER", "/opt/demo", "REGISTRY"
	image := "sha256:" + strings.Repeat("c", 64)
	oldID := strings.Repeat("a", 64)
	x := &executor{root: t.TempDir()}
	owner := &SoftwareOwnership{ControllerID: original.ControllerID, TargetID: original.TargetID, SoftwareID: i.SoftwareID, JobID: original.JobID, ConfigHash: Digest(i.Config), RuntimeMode: "DOCKER", ImageID: image, CommandTemplateHash: Digest(i.Definition)}
	s := &SoftwareRequest{ID: i.SoftwareID, Version: 1, Kind: "DOCKER", Locator: oldID, IdentityHash: Digest([]string{"DOCKER", oldID, image}), SoftwareKey: "CUSTOM-DEMO", ExpectedVersion: "1", Ownership: owner}
	d := i.Definition
	d.Management = &serviceinstall.ManagementDefinition{ScriptPrelude: "shared() { :; }", Operations: []serviceinstall.ManagementOperation{{Key: "configure", Name: "应用配置", Rebind: true, ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{{Name: "应用", Script: "shared", RunAs: "ROOT", TimeoutSeconds: 5}}, VerifySteps: []serviceinstall.CommandStep{{Name: "验证", Script: "true", RunAs: "ROOT", TimeoutSeconds: 5}}, RollbackSteps: []serviceinstall.CommandStep{{Name: "恢复", Script: "true", RunAs: "ROOT", TimeoutSeconds: 5}}}}}
	req := original
	req.JobID = "55555555-5555-4555-8555-555555555555"
	req.Action, req.SoftwareOperation, req.Installation, req.Software = "SOFTWARE_OPERATION", "SOFTWARE_OPERATION", nil, s
	req.Management = &serviceinstall.ManagementRequest{Installation: i, Definition: d, SoftwareID: i.SoftwareID, BindingRevision: i.BindingRevision, Root: serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config), OperationKey: "configure", ResourceHash: Digest([]serviceinstall.ResolvedResource{})}
	req.InputHash = CommandRequestDigest(req)
	for _, dir := range []string{filepath.Join(x.root, "software"), filepath.Join(x.root, "jobs", original.JobID), filepath.Join(x.root, "jobs", req.JobID)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeManagementFixture(t, filepath.Join(x.root, "jobs", original.JobID, "intent.json"), original)
	writeManagementFixture(t, x.softwareFile(s.ID), softwareBinding{Software: *s, JobID: original.JobID})
	writeManagementFixture(t, filepath.Join(x.root, "jobs", req.JobID, "management-before.json"), managementBefore{InputHash: req.InputHash, State: "RUNNING"})
	if err := validateManagementInput(req); err != nil {
		t.Fatal(err)
	}
	return x, req, filepath.Join(x.root, "jobs", req.JobID)
}

func rebindDockerRunner(req Request, newID, state string, alter func(map[string]any)) func(time.Duration, string, ...string) ([]byte, error) {
	return func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/docker" || len(args) < 6 || args[0] != "container" || args[1] != "inspect" {
			return nil, errors.New("unexpected fixture command")
		}
		id := newID
		if args[len(args)-1] == req.Software.Locator {
			id = req.Software.Locator
		}
		data := map[string]any{"id": id, "name": "/demo-" + req.Software.ID, "imageId": req.Software.Ownership.ImageID, "image": "fixture:1", "state": state, "labels": map[string]string{"streamfusion.ops.software-id": req.Software.ID}}
		if alter != nil {
			alter(data)
		}
		return json.Marshal(data)
	}
}

func TestManagementRebindCompletionAndReconcilePreserveOriginalInstallOwnership(t *testing.T) {
	x, req, dir := rebindRequestFixture(t)
	x.softwareRun = rebindDockerRunner(req, strings.Repeat("b", 64), "running", nil)
	if err := x.validateManagementInstallation(req); err != nil {
		t.Fatal(err)
	}
	candidate, actual, err := x.managementCandidate(context.Background(), req)
	if err != nil || candidate.Version != 2 || candidate.Locator == req.Software.Locator || candidate.Ownership.JobID != req.Software.Ownership.JobID || candidate.Ownership.ConfigHash != req.Software.Ownership.ConfigHash {
		t.Fatal("candidate changed installation ownership", candidate, err)
	}
	var old softwareBinding
	readJSON(x.softwareFile(req.Software.ID), &old)
	resources := []serviceinstall.ResolvedResource{}
	receipt := newManagementRebindReceipt(req, old, candidate, *actual, resources)
	if err := receipt.validate(req); err != nil {
		t.Fatal("initial receipt invalid", err)
	}
	writeManagementFixture(t, filepath.Join(dir, "management-rebind.json"), receipt)
	evidence := serviceinstall.ManagementEvidence{SoftwareID: req.Software.ID, OperationKey: "configure", DefinitionHash: Digest(req.Management.Definition), ParametersHash: req.Management.ParameterHash(), ResourceHash: req.Management.ResourceHash, Resources: resources, ObservedAt: stamp()}
	writeManagementFixture(t, filepath.Join(dir, "management-verified.json"), evidence)
	var storedEvidence serviceinstall.ManagementEvidence
	if err := readJSON(filepath.Join(dir, "management-verified.json"), &storedEvidence); err != nil {
		t.Fatal(err)
	}
	if _, err := x.completeManagement(req, dir, storedEvidence); err != nil {
		// A directory-sync failure can follow a durable rename. Reconciliation
		// must finish from the verified receipt without replaying the scripts.
		var binding softwareBinding
		if readJSON(x.softwareFile(req.Software.ID), &binding) != nil || Digest(binding) != Digest(receipt.Binding) {
			t.Fatal("initial completion failed before publishing binding", err)
		}
	}
	for pass := 0; pass < 2; pass++ {
		result := x.reconcileManagement(req, dir)
		if result.Status != "SUCCEEDED" || req.Management.ValidateRebindEvidence(*result.Installation) != nil || x.verifySoftwareResult(req, result) != nil {
			t.Fatalf("verified completion failed pass=%d result=%+v", pass, result)
		}
		var binding softwareBinding
		if readJSON(x.softwareFile(req.Software.ID), &binding) != nil || binding.JobID != old.JobID || !reflect.DeepEqual(binding.Software, candidate) {
			t.Fatal("binding was not idempotent or replaced the original job")
		}
	}
	if req.Software.Version != 1 || req.Software.Locator != strings.Repeat("a", 64) || req.Management.Installation.BindingRevision != 1 {
		t.Fatal("completion mutated the frozen request")
	}
}

func TestManagementRebindRejectsChangedContainerOwnershipAndFrozenInstallation(t *testing.T) {
	x, req, _ := rebindRequestFixture(t)
	x.softwareRun = rebindDockerRunner(req, strings.Repeat("b", 64), "running", nil)
	for name, alter := range map[string]func(map[string]any){
		"image": func(v map[string]any) { v["imageId"] = "sha256:" + strings.Repeat("d", 64) },
		"name":  func(v map[string]any) { v["name"] = "/another" },
		"owner": func(v map[string]any) { v["labels"] = map[string]string{"streamfusion.ops.software-id": "another"} },
	} {
		t.Run(name, func(t *testing.T) {
			x.softwareRun = rebindDockerRunner(req, strings.Repeat("b", 64), "running", alter)
			if _, _, err := x.managementCandidate(context.Background(), req); err == nil {
				t.Fatal("foreign replacement container acquired binding")
			}
		})
	}
	x.softwareRun = rebindDockerRunner(req, strings.Repeat("b", 64), "running", nil)
	req.Management.Installation.Config.MemoryMiB++
	if x.validateManagementInstallation(req) == nil {
		t.Fatal("replacement installation configuration was accepted")
	}
}

func TestManagementRebindCannotStartAStoppedService(t *testing.T) {
	x, req, dir := rebindRequestFixture(t)
	writeManagementFixture(t, filepath.Join(dir, "management-before.json"), managementBefore{InputHash: req.InputHash, State: "STOPPED"})
	x.softwareRun = rebindDockerRunner(req, strings.Repeat("b", 64), "running", nil)
	if _, _, err := x.managementCandidate(context.Background(), req); err == nil {
		t.Fatal("configuration started a previously stopped service")
	}
	x.softwareRun = rebindDockerRunner(req, strings.Repeat("b", 64), "exited", nil)
	if _, _, err := x.managementCandidate(context.Background(), req); err != nil {
		t.Fatal("stopped service could not retain its state", err)
	}
}

func TestManagementReadOnlyReceivesObservedStateBeforeExecuting(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux bash for local temporary-file execution")
	}
	for _, scenario := range []struct{ state, expected string }{{"running", "RUNNING"}, {"exited", "STOPPED"}, {"restarting", "UNKNOWN"}} {
		t.Run(scenario.expected, func(t *testing.T) {
			x, req, dir := rebindRequestFixture(t)
			i := req.Management.Installation
			i.Config.StorageRoot = ""
			req.Management.Root = serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
			req.Software.Ownership.ConfigHash = Digest(i.Config)
			original := req
			original.JobID, original.Management, original.Software, original.Installation = req.Software.Ownership.JobID, nil, nil, i
			writeManagementFixture(t, filepath.Join(x.root, "jobs", original.JobID, "intent.json"), original)
			writeManagementFixture(t, x.softwareFile(req.Software.ID), softwareBinding{Software: *req.Software, JobID: original.JobID})
			req.Management.OperationKey, req.Management.Installation = "read-config", nil
			req.Management.Definition.Management.Operations = []serviceinstall.ManagementOperation{{Key: "read-config", Name: "读取实际状态", ReadOnly: true, ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{{Name: "读取状态", RunAs: "ROOT", TimeoutSeconds: 5, Script: `printf '%s' "$OPS_PREVIOUS_STATE"`}}}}
			if err := os.Remove(filepath.Join(dir, "management-before.json")); err != nil {
				t.Fatal(err)
			}
			x.softwareRun = rebindDockerRunner(req, req.Software.Locator, scenario.state, nil)
			req.InputHash = CommandRequestDigest(req)
			checked := x.managementPreflight(req)
			if !checked.Ready {
				t.Fatal("readonly preflight blocked actual state", checked.Checks)
			}
			req.Management.ResourceHash = checked.ResourceHash
			req.InputHash = CommandRequestDigest(req)
			result, err := x.applyManagement(req, dir)
			if err != nil || result.Status != "SUCCEEDED" || result.Management == nil || result.Management.Output != scenario.expected {
				t.Fatalf("readonly step did not receive observed state: result=%+v error=%v", result, err)
			}
			if actual := x.managementPreviousState(req); actual != scenario.expected {
				t.Fatal("observed state was not frozen before execution", actual)
			}
		})
	}
}

func TestManagementRebindSignedFieldsAndCapabilityGate(t *testing.T) {
	_, req, _ := rebindRequestFixture(t)
	if RequiredCommandRecipeVersion(req.Management.Definition) != 8 {
		t.Fatal("rebind reached an older executor")
	}
	public, private, _ := ed25519.GenerateKey(nil)
	if err := SignCommandRequest(&req, private); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(public)
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Management.Definition.Management.Operations[0].Rebind = false },
		func(r *Request) { r.Management.Definition.Management.Operations[0].RollbackSteps[0].Script = "changed" },
		func(r *Request) { r.Management.Definition.Management.ScriptPrelude += "\nchanged" },
		func(r *Request) { r.Management.Installation.Config.MemoryMiB++ },
	} {
		var changed Request
		data, _ := json.Marshal(req)
		json.Unmarshal(data, &changed)
		mutate(&changed)
		if verifyCommandSignature(changed, key) == nil {
			t.Fatal("signed rebind boundary changed")
		}
	}
	if got := managementStepScript(*req.Management, req.Management.Definition.Management.Operations[0].Steps[0]); strings.Count(got, req.Management.Definition.Management.ScriptPrelude) != 1 {
		t.Fatal("shared source was omitted or appended twice")
	}
}

// Runs real Bash only against t.TempDir. Docker observation is a bounded
// in-memory fixture; no system service or installed container is changed.
func TestManagementRebindApplyVerificationAndRollback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux bash for local temporary-file execution")
	}
	for _, mode := range []string{"success", "verify-failed", "rollback-failed", "rollback-not-restored", "invalid-table"} {
		t.Run(mode, func(t *testing.T) {
			x, req, dir := rebindRequestFixture(t)
			i := req.Management.Installation
			i.Config.StorageRoot = ""
			req.Management.Root = serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
			req.Software.Ownership.ConfigHash = Digest(i.Config)
			if err := os.MkdirAll(filepath.Join(req.Management.Root, "work"), 0700); err != nil {
				t.Fatal(err)
			}
			stateFile := filepath.Join(req.Management.Root, "current-id")
			if err := os.WriteFile(stateFile, []byte(req.Software.Locator), 0600); err != nil {
				t.Fatal(err)
			}
			op := &req.Management.Definition.Management.Operations[0]
			op.Steps[0].Script = `shared; printf '%s' '` + strings.Repeat("b", 64) + `' > "$OPS_INSTALL_ROOT/current-id"; printf 'not a table'`
			op.RollbackSteps[0].Script = `shared; printf '%s' "$OPS_PREVIOUS_LOCATOR" > "$OPS_INSTALL_ROOT/current-id"`
			if mode == "verify-failed" || mode == "rollback-failed" || mode == "rollback-not-restored" {
				op.VerifySteps[0].Script = "false"
			}
			if mode == "rollback-failed" {
				op.RollbackSteps[0].Script = "false"
			} else if mode == "rollback-not-restored" {
				op.RollbackSteps[0].Script = "true"
			}
			if mode == "invalid-table" {
				op.ResultFormat = "TABLE"
			}
			req.InputHash = CommandRequestDigest(req)
			original := req
			original.JobID, original.Management, original.Software, original.Installation = req.Software.Ownership.JobID, nil, nil, i
			writeManagementFixture(t, filepath.Join(x.root, "jobs", original.JobID, "intent.json"), original)
			writeManagementFixture(t, x.softwareFile(req.Software.ID), softwareBinding{Software: *req.Software, JobID: original.JobID})
			x.softwareRun = func(limit time.Duration, command string, args ...string) ([]byte, error) {
				current, err := os.ReadFile(stateFile)
				if err != nil {
					return nil, err
				}
				return rebindDockerRunner(req, string(current), "running", func(v map[string]any) { v["id"] = string(current) })(limit, command, args...)
			}
			result, err := x.applyManagement(req, dir)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "success":
				if result.Status != "SUCCEEDED" || result.Installation == nil || x.verifySoftwareResult(req, result) != nil {
					t.Fatalf("rebind failed: %+v", result)
				}
			case "rollback-failed", "rollback-not-restored":
				if result.Status != "UNKNOWN" || result.Management != nil || result.Installation != nil || x.boundSoftware(*req.Software) != nil {
					t.Fatalf("uncertain rollback replaced binding or became final: %+v", result)
				}
			default:
				if !validManagementRestoredResult(req, result) || x.boundSoftware(*req.Software) != nil {
					t.Fatalf("failed operation was not safely restored: %+v", result)
				}
				before, _ := os.ReadFile(stateFile)
				reconciled := x.reconcileManagement(req, dir)
				after, _ := os.ReadFile(stateFile)
				if !validManagementRestoredResult(req, reconciled) || string(before) != string(after) {
					t.Fatal("restored result was not reused")
				}
			}
		})
	}
}

func TestManagementRollbackHasIndependentBudgetAfterApplyDeadline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux bash for local temporary-file execution")
	}
	x, req, dir := rebindRequestFixture(t)
	req.Management.Root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(req.Management.Root, "work"), 0700); err != nil {
		t.Fatal(err)
	}
	req.DeadlineAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
	op := req.Management.Definition.Management.Operations[0]
	op.RollbackSteps[0].Script = `shared; printf '%s|%s|%s' "$OPS_OPERATION_PHASE" "$OPS_PREVIOUS_LOCATOR" "$OPS_PREVIOUS_STATE" > "$OPS_INSTALL_ROOT/recovered"`
	req.InputHash = CommandRequestDigest(req)
	writeManagementFixture(t, filepath.Join(dir, "management-before.json"), managementBefore{InputHash: req.InputHash, State: "RUNNING"})
	x.softwareRun = rebindDockerRunner(req, strings.Repeat("b", 64), "running", nil)
	if _, err := x.runManagementSteps(req, dir, op.Steps, "APPLY", nil, nil, nil); err == nil || err.Error() != "DEADLINE_EXCEEDED" {
		t.Fatal("expired apply deadline was ignored", err)
	}
	result := x.restoreManagement(req, dir, op, errors.New("COMMAND_STEP_INTERRUPTED"))
	contents, err := os.ReadFile(filepath.Join(req.Management.Root, "recovered"))
	if !validManagementRestoredResult(req, result) || err != nil || string(contents) != "ROLLBACK|"+req.Software.Locator+"|RUNNING" {
		t.Fatal("deadline blocked bounded rollback or wrong environment", result.Status, string(contents), err)
	}
}

func TestCommandStepSharesSignedPreludeForInstallAndUninstall(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux Bash for temporary-file command execution")
	}
	req := commandTestRequest()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "work"), 0700); err != nil {
		t.Fatal(err)
	}
	req.Installation.Definition.Management = &serviceinstall.ManagementDefinition{ScriptPrelude: `shared() { printf 'shared\n' >> "$OPS_INSTALL_ROOT/prelude-used"; }`, Operations: []serviceinstall.ManagementOperation{{Key: "inspect", Name: "读取", ReadOnly: true, ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{{Name: "读取", Script: "true", RunAs: "ROOT", TimeoutSeconds: 5}}}}}
	x := &executor{root: root}
	step := serviceinstall.CommandStep{Name: "共享函数", Script: "shared", RunAs: "ROOT", TimeoutSeconds: 5}
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			req.Uninstallation = &SoftwareUninstallRequest{Installation: *req.Installation}
			req.Installation = nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := x.runCommandStep(ctx, req, root, step, nil)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "prelude-used"))
	if err != nil || string(data) != "shared\nshared\n" {
		t.Fatal("signed shared source was omitted or duplicated", string(data), err)
	}
}
