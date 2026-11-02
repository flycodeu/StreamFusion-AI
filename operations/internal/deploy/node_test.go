package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func testNodeContext() serviceinstall.NodeContext {
	return serviceinstall.NodeContext{EnvironmentID: "11111111-1111-4111-8111-111111111111", EnvironmentVersion: "1", Account: "ubuntu", AccountUID: "1000", RootPath: "/home/ubuntu/.local/share/streamfusion-ops/node/11111111-1111-4111-8111-111111111111", NVMVersion: serviceinstall.NodeNVMVersion, NVMHash: serviceinstall.NodeNVMHash}
}
func TestNodeObservationRejectsUnexpectedPathsAndLimits(t *testing.T) {
	n := testNodeContext()
	v := serviceinstall.NodeCatalog()[0].Version
	raw := "NVM\t" + n.NVMVersion + "\t" + n.NVMHash + "\t20786579783f26dffd0d38c0f794fb887e763df06a51ec842b2370a4fef0daca\nDEFAULT\t\nVERSION\t" + v + "\t" + serviceinstall.NodeVersionRoot(n, v) + "/bin/node\t" + strings.Repeat("a", 64) + "\t10.9.9\t\nPACKAGE\t" + v + "\tnpm\t10.9.9\n"
	obs, err := parseNodeObservation(n, []byte(raw))
	if err != nil || !obs.Exists || len(obs.Versions) != 1 || obs.Versions[0].NPMVersion != "10.9.9" {
		t.Fatalf("observation: %#v %v", obs, err)
	}
	for _, invalid := range []string{strings.ReplaceAll(raw, "/bin/node", "/bin/bash"), strings.ReplaceAll(raw, "DEFAULT\t\n", "DEFAULT\tlts/*\n"), raw + "unexpected\n", strings.ReplaceAll(raw, "\t10.9.9\t\n", "\t10.9.9\n")} {
		if _, err = parseNodeObservation(n, []byte(invalid)); err == nil {
			t.Fatal("accepted malformed observation")
		}
	}
	again, err := parseNodeObservation(n, []byte(raw))
	if err != nil || obs.Fingerprint != again.Fingerprint {
		t.Fatal("timestamp changes observation fingerprint")
	}
}
func TestNodeCommandDropsPrivilegeAndDoesNotLoadProfiles(t *testing.T) {
	n := testNodeContext()
	args := nodeCommandArguments(n, "/home/ubuntu", "set-default", "v22.23.4", "", "", "job", "", "/var/tmp/fixed-configs")
	joined := strings.Join(args, "\x00")
	for _, want := range []string{"-u\x00ubuntu\x00--\x00/usr/bin/env\x00-i", "HOME=/home/ubuntu", "--noprofile\x00--norc", "sfops-node\x00set-default"} {
		if !strings.Contains(joined, want) {
			t.Fatal(want)
		}
	}
	if strings.Contains(joined, "bash -l") || strings.Contains(nodeScript, "set -eu") {
		t.Fatal("profile or nounset breaks nvm isolation")
	}
	if !strings.Contains(nodeScript, "nvm version default") {
		t.Fatal("external aliases must be resolved read-only")
	}
}
func TestNodeOptionalFieldsPreserveLegacyInputs(t *testing.T) {
	for _, v := range []any{Request{}, Result{}, SoftwareOwnership{}, SoftwareUninstallRequest{}, serviceinstall.Request{}} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"\"node\"", "\"nodeEnvironment\"", "\"clearNodeDefault\""} {
			if strings.Contains(string(raw), key) {
				t.Fatalf("legacy input changed: %s", raw)
			}
		}
	}
}
func TestNodeManagedIdentityAndDeletionAreExact(t *testing.T) {
	n := testNodeContext()
	s := SoftwareRequest{Kind: "TOOL", SoftwareKey: "NODE", Locator: serviceinstall.NodeVersionRoot(n, "v22.23.4") + "/bin/node", Ownership: &SoftwareOwnership{Node: &n, RuntimeMode: "NATIVE"}}
	s.Ownership.BinaryPath = s.Locator
	if !validNodeOwnership(s) {
		t.Fatal("exact account Node rejected")
	}
	s.Ownership.BinaryPath = "/usr/bin/node"
	if validNodeOwnership(s) {
		t.Fatal("accepted system Node as account owned")
	}
	paths := nodeVersionDataPaths(n, "v22.23.4")
	if len(paths) != 3 || !strings.HasSuffix(paths[2], "nvm/.cache/bin/node-v22.23.4-linux-x64") {
		t.Fatal(paths)
	}
	commands := nodePlanCommands(NodeEnvironmentRequest{Context: n, Action: "REMOVE_ENVIRONMENT"})
	if len(commands) != 1 || strings.Contains(strings.Join(commands[0].Args, " "), "-rf") {
		t.Fatal("default removal would delete data")
	}
}

func TestNodeManagerSelectionIsFrozenInHistoricalContext(t *testing.T) {
	n := NodeEnvironmentRequest{Context: testNodeContext(), Action: "INITIALIZE"}
	d, err := nodeEnvironmentDefinition(n)
	if err != nil || d.Version != n.Context.NVMVersion || d.Native.Parts[0].SHA256 != n.Context.NVMHash {
		t.Fatal("initialization does not use frozen manager")
	}
	n.Action = "UPDATE_NVM"
	d, err = nodeEnvironmentDefinition(n)
	if err != nil || d.Version != n.Context.NVMVersion {
		t.Fatal("historical empty update follows default")
	}
	n.PackageVersion = "999.0.0"
	if _, err = nodeEnvironmentDefinition(n); err == nil {
		t.Fatal("untrusted manager accepted")
	}
}

func TestNodeObservationPreflightHasNoMutationCommands(t *testing.T) {
	for _, action := range []string{"OBSERVE", "DISCOVER", "INVALID"} {
		if commands := nodePlanCommands(NodeEnvironmentRequest{Context: testNodeContext(), Action: action}); len(commands) != 0 {
			t.Fatal(action, commands)
		}
	}
}

func TestNodePackageActionsRequirePinnedVersions(t *testing.T) {
	for _, action := range []string{"UPDATE_NPM", "INSTALL_PNPM", "UPDATE_PNPM"} {
		n := NodeEnvironmentRequest{Context: testNodeContext(), Action: action, NodeVersion: "v22.23.4"}
		if n.Validate() == nil {
			t.Fatal("empty material version accepted", action)
		}
		n.PackageVersion = "999.0.0"
		if n.Validate() == nil {
			t.Fatal("unknown material version accepted", action)
		}
	}
}

func TestNodeRemovalDoesNotConfirmPartialCleanup(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nvm"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nvm/nvm-exec"), []byte("still installed"), 0600); err != nil {
		t.Fatal(err)
	}
	if nodeEnvironmentRemoved(root, false) || nodeEnvironmentRemoved(root, true) {
		t.Fatal("partial deletion confirmed")
	}
	if err := os.Remove(filepath.Join(root, "nvm/nvm-exec")); err != nil {
		t.Fatal(err)
	}
	if !nodeEnvironmentRemoved(root, false) || nodeEnvironmentRemoved(root, true) {
		t.Fatal("retained data treated as deleted")
	}
	if err := os.Remove(filepath.Join(root, "nvm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if !nodeEnvironmentRemoved(root, true) {
		t.Fatal("confirmed absent root rejected")
	}
}

func TestNodeManagerRequiresBothPinnedFiles(t *testing.T) {
	n := testNodeContext()
	obs := &NodeEnvironmentObservation{NVMVersion: n.NVMVersion, NVMHash: n.NVMHash}
	if trustedObservedNodeManager(obs) {
		t.Fatal("manager with missing nvm-exec accepted")
	}
	obs.NVMExecHash = strings.Repeat("a", 64)
	if trustedObservedNodeManager(obs) {
		t.Fatal("changed nvm-exec accepted")
	}
	d, _ := serviceinstall.NodeEnvironmentRecipe("UPDATE_NVM", n.NVMVersion)
	obs.NVMExecHash = d.Native.Parts[1].SHA256
	if !trustedObservedNodeManager(obs) {
		t.Fatal("intact manager rejected")
	}
}
