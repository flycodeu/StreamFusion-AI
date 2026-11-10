package deploy

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func builtinFixture(t *testing.T, key, version string) (*executor, Request) {
	t.Helper()
	req := commandTestRequest()
	found := false
	for _, d := range serviceinstall.BuiltinCommandCatalog() {
		if d.Key == key && d.Version == version {
			req.Installation.Definition = d
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing builtin")
	}
	x := &executor{root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(x.root, "jobs", req.JobID), 0700); err != nil {
		t.Fatal(err)
	}
	return x, req
}

func TestBuiltinEditedAPTCommandUsesFrozenArgumentsAndCannotReplay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	x, req := builtinFixture(t, "java", "21-ubuntu24.04-repository")
	step := builtinStep(req, "OFFICIAL_PACKAGES_APPLY")
	step.Script += "\nprintf 'edited-install\\n'"
	arg := "openjdk-21-jdk=21;printf injected"
	calls := 0
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		calls++
		if command != "/usr/bin/env" {
			t.Fatal(command)
		}
		script := args[len(args)-1]
		if !strings.Contains(script, "edited-install") || !strings.Contains(script, posixSingleQuote(arg)) || strings.Contains(script, "${OPS_PACKAGE_ARGUMENTS}") {
			t.Fatalf("wrong published script: %s", script)
		}
		return []byte("installed\n"), nil
	}
	args := []string{"--assume-yes", "install", "--", arg}
	if _, err := x.loggedSoftwareCommand(context.Background(), req, "OFFICIAL_PACKAGES_APPLY", "/usr/bin/apt-get", args...); err != nil {
		t.Fatal(err)
	}
	if _, err := x.loggedSoftwareCommand(context.Background(), req, "OFFICIAL_PACKAGES_APPLY", "/usr/bin/apt-get", args...); err == nil || err.Error() != "COMMAND_STEP_RECONCILE_REQUIRED" {
		t.Fatalf("replayed step: %v", err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestBuiltinDockerCreateExpandsOnlyValidatedNonSecretArguments(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	x, req := builtinFixture(t, "mysql", "8.4.11")
	req.Installation.Config.RuntimeMode = "DOCKER"
	req.Installation.Source.Type = "REGISTRY"
	d := req.Installation.Definition
	secret := "TEST-password-not-command"
	x.secrets = map[string]string{"test": secret}
	args := []string{"container", "create", "--pull", "never", "--name", "name", "--log-opt", "max-file=3", "--publish", "127.0.0.1:3307:3306/tcp", "--label", "owner=id", "--volume", "/tmp/config:/etc/config:ro", "--env", "MYSQL_ROOT_PASSWORD_FILE=/run/secrets/root", d.ImageID, "--max-connections=200"}
	x.softwareRun = func(_ time.Duration, command string, actual ...string) ([]byte, error) {
		script := actual[len(actual)-1]
		for _, arg := range args[10:] {
			if arg == d.ImageID {
				continue
			}
			if !strings.Contains(script, posixSingleQuote(arg)) {
				t.Fatalf("lost %q from %s", arg, script)
			}
		}
		if strings.Contains(strings.Join(actual, " "), secret) {
			t.Fatal("secret in command/environment")
		}
		return []byte("output " + secret + "\n"), nil
	}
	if _, err := x.loggedSoftwareCommand(context.Background(), req, "SERVICE_CREATE", "/usr/bin/docker", args...); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(x.root, "jobs", req.JobID, "service-install.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "[REDACTED]") {
		t.Fatal("command output was not redacted")
	}
}

func TestBuiltinRecoveryRequiresSuccessfulPublishedVerification(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	x, req := builtinFixture(t, "java", "21-ubuntu24.04-repository")
	result := Result{Status: "SUCCEEDED"}
	if x.builtinResultConfirmed(req, result) {
		t.Fatal("observed binary replaced missing script verification")
	}
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) { return []byte("21\n"), nil }
	if _, _, err := x.runBuiltinStep(context.Background(), req, "VERIFY", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if x.builtinResultConfirmed(req, result) {
		t.Fatal("one verification cannot confirm the entire flow")
	}
	for _, step := range req.Installation.Definition.Commands.Steps {
		if !slices.Contains([]string{"INSTALL", "VERIFY"}, step.Phase) || builtinStep(req, step.ID) == nil {
			continue
		}
		if err := atomicJSON(filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+step.ID+".json"), commandStepReceipt{State: "SUCCEEDED"}); err != nil {
			t.Fatal(err)
		}
	}
	if !x.builtinResultConfirmed(req, result) {
		t.Fatal("completed flow not accepted")
	}
	if err := atomicJSON(filepath.Join(x.root, "jobs", req.JobID, "builtin-step-VERIFY.json"), commandStepReceipt{State: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	if x.builtinResultConfirmed(req, result) {
		t.Fatal("interrupted verification accepted")
	}
}

func TestBuiltinNodeDefinitionIsSignedAndSelectedByAction(t *testing.T) {
	_, req := builtinFixture(t, "nvm", serviceinstall.NodeNVMVersion)
	d := req.Installation.Definition
	req.Installation = nil
	req.Action = "NODE_ENVIRONMENT_CHANGE"
	req.SoftwareOperation = req.Action
	req.NodeEnvironment = &NodeEnvironmentRequest{Definition: &d, Action: "REMOVE_ENVIRONMENT"}
	if selected, err := nodeEnvironmentDefinition(*req.NodeEnvironment); err != nil || selected.Key != "nvm" {
		t.Fatalf("remove did not use frozen manager: %v", err)
	}
	pub, key, _ := ed25519.GenerateKey(nil)
	req.InputHash = CommandRequestDigest(req)
	if err := SignCommandRequest(&req, key); err != nil {
		t.Fatal(err)
	}
	if err := verifyCommandSignature(req, base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(d.Commands.Steps, func(s serviceinstall.CommandStep) bool { return s.ID == "NODE_UNINSTALL" })
	d.Commands.Steps[index].Script += "\nprintf altered"
	req.InputHash = CommandRequestDigest(req)
	if verifyCommandSignature(req, base64.StdEncoding.EncodeToString(pub)) == nil {
		t.Fatal("changed manager command kept signature")
	}
}

func TestBuiltinSecretsAreExcludedFromSignatureButReferencesStayFrozen(t *testing.T) {
	_, req := builtinFixture(t, "mysql", "8.4.11")
	req.Installation.Config.CredentialID = "55555555-5555-4555-8555-555555555555"
	req.Installation.Config.CredentialRevision = "1"
	req.Action = "SOFTWARE_INSTALL_CHECK"
	req.SoftwareOperation = ""
	req.DeadlineAt = ""
	req.InputHash = ""
	req.Secrets = map[string]string{req.Installation.Config.CredentialID + ":1": "TestOnly!1234"}
	pub, key, _ := ed25519.GenerateKey(nil)
	if err := SignCommandRequest(&req, key); err != nil {
		t.Fatal(err)
	}
	req.Secrets = nil
	if err := verifyCommandSignature(req, base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	req.Installation.Config.CredentialRevision = "2"
	if verifyCommandSignature(req, base64.StdEncoding.EncodeToString(pub)) == nil {
		t.Fatal("credential reference changed without a new signature")
	}
}

func TestBuiltinAccountsAreResolvedBeforeInstallation(t *testing.T) {
	x, req := builtinFixture(t, "nginx", "1.30.6")
	if err := builtinAccountCheck(*req.Installation); err != nil {
		t.Fatal(err)
	}
	builtinStep(req, "NATIVE_START").RunAs = "ACCOUNT"
	if err := builtinAccountCheck(*req.Installation); err != nil {
		t.Fatalf("managed service account rejected: %v", err)
	}
	if got := x.builtinVariables(req)["OPS_ACCOUNT"]; got != nativeUserName(req.Installation.SoftwareID) {
		t.Fatalf("account=%q", got)
	}
	_, official := builtinFixture(t, "java", "21-ubuntu24.04-repository")
	builtinStep(official, "VERIFY").RunAs = "ACCOUNT"
	if err := builtinAccountCheck(*official.Installation); err == nil || err.Error() != "COMMAND_ACCOUNT_REQUIRED" {
		t.Fatalf("accountless verification not rejected: %v", err)
	}
}
