package deploy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func commandTestRequest() Request {
	d := serviceinstall.Definition{Key: "custom-demo", Name: "Demo", Version: "1", Revision: "1", Platform: "linux/amd64", Kind: "TOOL", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ONLINE"}, Commands: &serviceinstall.CommandRecipe{Kind: "TOOL", Locator: "${OPS_INSTALL_ROOT}/bin/demo", Steps: []serviceinstall.CommandStep{
		{Phase: "INSTALL", Name: "安装", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10},
		{Phase: "VERIFY", Name: "验证", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10},
		{Phase: "UNINSTALL", Name: "卸载", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10},
	}}}
	return Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: "11111111-1111-4111-8111-111111111111", TargetID: "22222222-2222-4222-8222-222222222222", JobID: "33333333-3333-4333-8333-333333333333", Action: "SOFTWARE_INSTALL", SoftwareOperation: "SOFTWARE_INSTALL", DeadlineAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339), Installation: &serviceinstall.Request{SoftwareID: "44444444-4444-4444-8444-444444444444", BindingRevision: 1, Definition: d, Config: serviceinstall.Config{Name: "Demo", RuntimeMode: "NATIVE", MemoryMiB: 512}, Source: serviceinstall.Source{Type: "ONLINE"}}}
}

func TestCommandSignatureBindsEntireExecutionPlan(t *testing.T) {
	pub, private, _ := ed25519.GenerateKey(nil)
	key := base64.StdEncoding.EncodeToString(pub)
	req := commandTestRequest()
	req.InputHash = Digest(req)
	if err := SignCommandRequest(&req, private); err != nil {
		t.Fatal(err)
	}
	if err := verifyCommandSignature(req, key); err != nil {
		t.Fatal(err)
	}
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(req)
	changes := map[string]func(*Request){
		"catalog grouping": func(r *Request) { r.Installation.Definition.CatalogKey = "nginx" },
		"target":           func(r *Request) { r.TargetID = "55555555-5555-4555-8555-555555555555" },
		"job":              func(r *Request) { r.JobID = "55555555-5555-4555-8555-555555555555" },
		"deadline":         func(r *Request) { r.DeadlineAt = "2099-01-01T00:00:00Z" },
		"account":          func(r *Request) { r.Installation.Config.Account = "another" },
		"script":           func(r *Request) { r.Installation.Definition.Commands.Steps[0].Script = "id" },
		"line ending":      func(r *Request) { r.Installation.Definition.Commands.Steps[0].Script += "\r\n" },
		"permission":       func(r *Request) { r.Installation.Definition.Commands.Steps[0].RunAs = "ACCOUNT" },
		"material":         func(r *Request) { r.Installation.Source.SHA256 = Digest("different") },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var changed Request
			_ = json.Unmarshal(data, &changed)
			change(&changed)
			changed.InputHash = CommandRequestDigest(changed)
			if verifyCommandSignature(changed, key) == nil {
				t.Fatal("forged plan accepted")
			}
		})
	}
	for _, action := range []string{"STATUS", "ACK", "PROGRESS", "SOFTWARE_INSTALL_LOGS"} {
		copy := req
		copy.Action = action
		copy.ResultHash = Digest("result")
		if err := verifyCommandSignature(copy, key); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
}

func TestCommandPreflightSignatureIsBoundedAndSecretFree(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	req := commandTestRequest()
	req.Action = "SOFTWARE_INSTALL_CHECK"
	req.SoftwareOperation = ""
	req.JobID = ""
	req.DeadlineAt = ""
	if err := SignCommandRequest(&req, key); err != nil {
		t.Fatal(err)
	}
	deadline, err := time.Parse(time.RFC3339, req.DeadlineAt)
	if err != nil || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Minute {
		t.Fatalf("deadline: %s", req.DeadlineAt)
	}
	if err := verifyCommandSignature(req, base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	req.Secrets = map[string]string{"password": "test-only"}
	if SignCommandRequest(&req, key) == nil {
		t.Fatal("secret-bearing custom command signed")
	}
}

func TestCustomTemplateCannotBypassTrustOrReplayScripts(t *testing.T) {
	x := executor{root: t.TempDir()}
	req := commandTestRequest()
	req.Action = "SOFTWARE_INSTALL_CHECK"
	var output bytes.Buffer
	if err := x.executeSoftware(req, &output); err == nil || err.Error() != "COMMAND_TRUST_REQUIRED" {
		t.Fatalf("unsigned request: %v", err)
	}
	if _, err := x.installResumeOffset(req, filepath.Join(x.root, "jobs", req.JobID)); err == nil || err.Error() != "INSTALL_RESUME_UNSAFE" {
		t.Fatalf("custom resume: %v", err)
	}
	forged := Request{Action: "SOFTWARE_ADOPT", Software: &SoftwareRequest{ID: req.Installation.SoftwareID, Ownership: &SoftwareOwnership{CommandTemplateHash: Digest(req.Installation.Definition)}}}
	if err := x.executeSoftware(forged, &output); err == nil || err.Error() != "COMMAND_ADOPTION_FORBIDDEN" {
		t.Fatalf("forged custom ownership: %v", err)
	}
}

func TestCommandReconcileRequiresTheInstalledBinaryIdentity(t *testing.T) {
	req := commandTestRequest()
	req.InputHash = Digest(req)
	x := executor{root: t.TempDir()}
	binaryHash := Digest("binary")
	s := SoftwareRequest{ID: req.Installation.SoftwareID, Version: 1, Kind: "TOOL", Locator: "/opt/demo/bin/demo", SoftwareKey: "CUSTOM-DEMO", ExpectedVersion: "1", Ownership: &SoftwareOwnership{ControllerID: req.ControllerID, TargetID: req.TargetID, SoftwareID: req.Installation.SoftwareID, JobID: req.JobID, ConfigHash: Digest(req.Installation.Config), RuntimeMode: "NATIVE", BinaryPath: "/opt/demo/bin/demo", BinaryHash: binaryHash, CommandTemplateHash: Digest(req.Installation.Definition)}}
	s.IdentityHash = Digest([]string{"TOOL", s.Locator, binaryHash})
	if err := os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(softwareBinding{Software: s, JobID: req.JobID})
	if err := os.WriteFile(x.softwareFile(s.ID), data, 0600); err != nil {
		t.Fatal(err)
	}
	x.softwareRun = func(_ time.Duration, _ string, _ ...string) ([]byte, error) {
		return []byte(binaryHash + "  " + s.Locator + "\n"), nil
	}
	if result := x.reconcileCommands(req, ""); result.Status != "SUCCEEDED" {
		t.Fatalf("unchanged tool: %+v", result)
	}
	x.softwareRun = func(_ time.Duration, _ string, _ ...string) ([]byte, error) {
		return []byte(Digest("changed") + "  " + s.Locator + "\n"), nil
	}
	if result := x.reconcileCommands(req, ""); result.Status != "UNKNOWN" {
		t.Fatalf("changed binary accepted: %+v", result)
	}
}

func TestCommandExistingObjectChecksAreConclusive(t *testing.T) {
	existingID := Digest("existing container")
	for _, test := range []struct {
		name, kind, locator, output, want string
		failed                            bool
	}{
		{"missing service", "SYSTEMD", "demo.service", "not-found\n", "", false},
		{"existing service", "SYSTEMD", "demo.service", "loaded\n", "COMMAND_OBJECT_ALREADY_EXISTS", false},
		{"masked service", "SYSTEMD", "demo.service", "masked\n", "COMMAND_OBJECT_ALREADY_EXISTS", false},
		{"unknown service response", "SYSTEMD", "demo.service", "", "COMMAND_OBJECT_CHECK_UNAVAILABLE", false},
		{"service query failed", "SYSTEMD", "demo.service", "not-found\n", "COMMAND_OBJECT_CHECK_UNAVAILABLE", true},
		{"missing container", "DOCKER", "demo", existingID + " another\n", "", false},
		{"existing container name", "DOCKER", "demo", existingID + " demo\n", "COMMAND_OBJECT_ALREADY_EXISTS", false},
		{"existing short container id", "DOCKER", existingID[:12], existingID + " another\n", "COMMAND_OBJECT_ALREADY_EXISTS", false},
		{"empty docker list", "DOCKER", "demo", "", "", false},
		{"docker query failed", "DOCKER", "demo", "", "COMMAND_OBJECT_CHECK_UNAVAILABLE", true},
		{"truncated docker list", "DOCKER", "demo", "partial", "COMMAND_OBJECT_CHECK_UNAVAILABLE", false},
		{"ambiguous home placeholder", "TOOL", "${HOME}/bin/demo", "", "COMMAND_LOCATOR_INVALID", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := commandTestRequest()
			req.Installation.Definition.Commands.Kind = test.kind
			req.Installation.Definition.Commands.Locator = test.locator
			x := executor{root: t.TempDir(), softwareRun: func(_ time.Duration, _ string, _ ...string) ([]byte, error) {
				if test.failed {
					return []byte(test.output), errors.New("query failed")
				}
				return []byte(test.output), nil
			}}
			err := x.commandObjectAbsent(*req.Installation, "/opt/demo")
			if test.want == "" && err != nil || test.want != "" && (err == nil || err.Error() != test.want) {
				t.Fatalf("got %v, want %s", err, test.want)
			}
			if test.want != "" {
				p := x.commandPreflight(*req.Installation)
				found := false
				for _, check := range p.Checks {
					if check.Key == "EXISTING" && check.Code == test.want {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing preflight rejection: %+v", p.Checks)
				}
			}
		})
	}
	file := filepath.Join(t.TempDir(), "tool")
	if err := commandFileAbsent(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := commandFileAbsent(file); err == nil || err.Error() != "COMMAND_OBJECT_ALREADY_EXISTS" {
		t.Fatalf("existing tool: %v", err)
	}
	t.Run("dangling link", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "tool")
		if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), link); err != nil {
			t.Skip("symlink creation unavailable")
		}
		if err := commandFileAbsent(link); err == nil || err.Error() != "COMMAND_OBJECT_ALREADY_EXISTS" {
			t.Fatalf("dangling tool link: %v", err)
		}
	})
}

func TestCommandInstallRechecksBeforeRunningScripts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux filesystem protection checks")
	}
	root := t.TempDir()
	if os.Geteuid() == 0 {
		var err error
		root, err = os.MkdirTemp("/var/lib", "sfops-command-existing-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(root)
		if err = secureAncestors(root); err != nil {
			t.Fatalf("root fixture parent is not protected: %v", err)
		}
		if err = os.Chmod(root, 0777); err != nil {
			t.Fatal(err)
		}
	}
	req := commandTestRequest()
	req.Installation.Definition.Kind = "SERVICE"
	req.Installation.Definition.Commands.Kind = "SYSTEMD"
	req.Installation.Definition.Commands.Locator = "sfops-custom-existing-test.service"
	req.Installation.Definition.Commands.Steps[0].Script = `touch "$OPS_INSTALL_ROOT/must-not-run"`
	queries := 0
	x := executor{root: root, softwareRun: func(_ time.Duration, _ string, _ ...string) ([]byte, error) {
		queries++
		if queries == 1 {
			return []byte("not-found\n"), nil
		}
		return []byte("loaded\n"), nil
	}}
	dir := filepath.Join(root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := x.installCommands(req, dir); err == nil || err.Error() != "NATIVE_DIRECTORY_NOT_PROTECTED" {
		t.Fatalf("unprotected installation directory accepted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "software", req.Installation.SoftwareID)); !os.IsNotExist(err) {
		t.Fatalf("rejected preparation created the installation directory: %v", err)
	}
	if os.Geteuid() != 0 {
		return
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	queries = 0
	if _, err := x.installCommands(req, dir); err == nil || err.Error() != "COMMAND_OBJECT_ALREADY_EXISTS" {
		t.Fatalf("object appeared during preparation: %v", err)
	}
	if queries != 2 {
		t.Fatalf("object checks: %d", queries)
	}
	if _, err := os.Lstat(filepath.Join(root, "software", req.Installation.SoftwareID, "must-not-run")); !os.IsNotExist(err) {
		t.Fatalf("install script ran: %v", err)
	}
}

func TestCommandStepsStopAtFailureAndKeepReceipts(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires Linux root to execute the target shell")
	}
	req := commandTestRequest()
	req.Installation.Definition.Commands.Steps = []serviceinstall.CommandStep{
		{Phase: "INSTALL", Name: "first", Script: `printf first > "$OPS_DATA_ROOT/steps"`, RunAs: "ROOT", TimeoutSeconds: 5},
		{Phase: "INSTALL", Name: "failed", Script: "exit 3", RunAs: "ROOT", TimeoutSeconds: 5},
		{Phase: "VERIFY", Name: "not executed", Script: `printf unexpected >> "$OPS_DATA_ROOT/steps"`, RunAs: "ROOT", TimeoutSeconds: 5},
	}
	x := executor{root: t.TempDir()}
	root := filepath.Join(x.root, "software", req.Installation.SoftwareID)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	for _, path := range []string{filepath.Join(root, "data"), filepath.Join(root, "work"), dir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.runCommandSteps(req, dir, root, "INSTALL", "VERIFY"); err == nil || err.Error() != "COMMAND_STEP_FAILED" {
		t.Fatalf("step failure: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "data", "steps"))
	if err != nil || string(data) != "first" {
		t.Fatalf("side effects: %s %v", data, err)
	}
	var receipt commandStepReceipt
	if err := readJSON(filepath.Join(dir, "command-step-02.json"), &receipt); err != nil || receipt.State != "FAILED" || receipt.FinishedAt == "" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	if err := x.runCommandSteps(req, dir, root, "INSTALL", "VERIFY"); err == nil || err.Error() != "COMMAND_STEP_RECONCILE_REQUIRED" {
		t.Fatalf("replay allowed: %v", err)
	}
}

func TestCommandAccountAndTimeoutBoundaries(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires Linux root")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("nobody account unavailable")
	}
	root, err := os.MkdirTemp("", "sfops-command-account-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"work", "data"} {
		if err = os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	if err = os.Chown(filepath.Join(root, "data"), uid, gid); err != nil {
		t.Fatal(err)
	}
	x := executor{root: root}
	req := commandTestRequest()
	step := serviceinstall.CommandStep{RunAs: "ACCOUNT", Script: `id -u > "$OPS_DATA_ROOT/uid"`}
	if err = x.runCommandStep(context.Background(), req, root, step, account); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "data", "uid"))
	if err != nil || strings.TrimSpace(string(data)) != account.Uid {
		t.Fatalf("account uid: %s %v", data, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	err = x.runCommandStep(ctx, req, root, serviceinstall.CommandStep{RunAs: "ROOT", Script: "sleep 30 & wait"}, nil)
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("child process deadline: %v %s", err, time.Since(started))
	}
}

func TestCommandPhaseOrderPrecedesEditorRowOrder(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires Linux root")
	}
	req := commandTestRequest()
	req.Installation.Definition.Commands.Steps = []serviceinstall.CommandStep{
		{Phase: "VERIFY", Name: "verify last", Script: `test "$(cat "$OPS_DATA_ROOT/order")" = firstsecond`, RunAs: "ROOT", TimeoutSeconds: 5},
		{Phase: "INSTALL", Name: "first", Script: `printf first > "$OPS_DATA_ROOT/order"`, RunAs: "ROOT", TimeoutSeconds: 5},
		{Phase: "INSTALL", Name: "second", Script: `printf second >> "$OPS_DATA_ROOT/order"`, RunAs: "ROOT", TimeoutSeconds: 5},
	}
	x := executor{root: t.TempDir()}
	root := filepath.Join(x.root, "software", req.Installation.SoftwareID)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	for _, path := range []string{filepath.Join(root, "data"), filepath.Join(root, "work"), dir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.runCommandSteps(req, dir, root, "INSTALL", "VERIFY"); err != nil {
		t.Fatal(err)
	}
}

func TestCommandTrustRequiresRootEnrollment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux root enrollment checks")
	}
	root := t.TempDir()
	if os.Geteuid() == 0 {
		var err error
		root, err = os.MkdirTemp("/var/lib", "sfops-command-trust-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(root)
		if err = secureAncestors(root); err != nil {
			t.Fatalf("root fixture parent is not protected: %v", err)
		}
	}
	req := commandTestRequest()
	if err := os.WriteFile(filepath.Join(root, "executor"), []byte("test fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	owner := Owner{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: req.ControllerID, TargetID: req.TargetID}
	if err := atomicJSON(filepath.Join(root, "owner.json"), owner); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(nil)
	encoded := base64.StdEncoding.EncodeToString(pub)
	if os.Geteuid() != 0 {
		if err := TrustCommandController(root, req.ControllerID, req.TargetID, encoded); err == nil || err.Error() != "LINUX_ROOT_REQUIRED" {
			t.Fatalf("ordinary account enrolled a controller: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "command-trust.json")); !os.IsNotExist(err) {
			t.Fatalf("rejected enrollment created trust: %v", err)
		}
		return
	}
	if err := os.Chmod(root, 0777); err != nil {
		t.Fatal(err)
	}
	if err := TrustCommandController(root, req.ControllerID, req.TargetID, encoded); err == nil || err.Error() != "EXECUTOR_PATH_NOT_PROTECTED" {
		t.Fatalf("writable enrollment ancestor accepted: %v", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if err := TrustCommandController(root, req.ControllerID, req.TargetID, encoded); err != nil {
			t.Fatal(err)
		}
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if err := TrustCommandController(root, req.ControllerID, req.TargetID, base64.StdEncoding.EncodeToString(other)); err == nil || err.Error() != "COMMAND_TRUST_CONFLICT" {
		t.Fatalf("trust overwrite: %v", err)
	}
	x := executor{root: root, owner: owner}
	req.InputHash = Digest(req)
	if err := SignCommandRequest(&req, key); err != nil {
		t.Fatal(err)
	}
	if err := x.verifyCommandRequest(req); err != nil {
		t.Fatal(err)
	}
	req.Installation.Definition.Commands.Steps[0].Script = "id"
	if err := x.verifyCommandRequest(req); err == nil {
		t.Fatal("forged command accepted")
	}
	if err := os.Chmod(filepath.Join(root, "command-trust.json"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := x.verifyCommandRequest(req); err == nil || err.Error() != "COMMAND_TRUST_REQUIRED" {
		t.Fatalf("unprotected trust accepted: %v", err)
	}
}
