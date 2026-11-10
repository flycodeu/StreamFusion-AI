package controller

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func managementTestBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		if git, err := exec.LookPath("git"); err == nil {
			bash := filepath.Join(filepath.Dir(git), "..", "bin", "bash.exe")
			if _, err := os.Stat(bash); err == nil {
				return bash
			}
		}
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		return bash
	}
	t.Skip("bash is unavailable")
	return ""
}

func TestMySQLManagementPresetHasBoundedEditableCommands(t *testing.T) {
	for _, definition := range serviceinstall.BuiltinCommandCatalog() {
		if definition.Key != "mysql" {
			continue
		}
		definition.Management = mysqlManagementDefinition(definition)
		if err := definition.ValidateCommands(); err != nil {
			data, _ := json.Marshal(definition)
			t.Fatalf("mysql template (%d bytes): %v", len(data), err)
		}
		if len(definition.Management.Operations) != 8 {
			t.Fatal("management operations are missing")
		}
		id := uuid.NewString()
		for _, operation := range definition.Management.Operations {
			parameters := map[string]string{"ADMIN_USER": "root"}
			refs := map[string]serviceinstall.CredentialReference{"ADMIN_PASSWORD": {ID: uuid.NewString(), Revision: "1"}}
			inputs := map[string]string{"DATABASE": "example_app", "USERNAME": "example_user", "HOST": "localhost", "PERMISSION": "只读"}
			request := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString(), JobID: uuid.NewString(), InputHash: strings.Repeat("a", 64), CommandSignature: strings.Repeat("a", 88), Action: "SOFTWARE_OPERATION", SoftwareOperation: "SOFTWARE_OPERATION", Software: &deploy.SoftwareRequest{ID: id, Version: 1, Kind: "DOCKER", Locator: strings.Repeat("b", 64), IdentityHash: strings.Repeat("c", 64)}, Management: &serviceinstall.ManagementRequest{Definition: definition, SoftwareID: id, BindingRevision: 1, Root: "/opt/streamfusion-ops-target/software/" + id, OperationKey: operation.Key, Parameters: parameters, ParameterCredentials: refs, OperationParameters: inputs}}
			data, err := json.Marshal(request)
			if err != nil || len(data) > 64<<10 {
				t.Fatalf("%s request is too large (%d bytes): %v", operation.Key, len(data), err)
			}
		}
		bash := managementTestBash(t)
		for _, op := range definition.Management.Operations {
			for _, step := range append(op.Steps, op.VerifySteps...) {
				cmd := exec.Command(bash, "-n")
				cmd.Stdin = strings.NewReader(step.Script)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v %s", op.Key, err, output)
				}
			}
		}
		definition.Key, definition.CatalogKey = "custom-mysql-copy", "mysql"
		if mysqlManagementDefinition(definition) != nil {
			t.Fatal("custom catalog labels were treated as builtin authorization")
		}
	}
}

func TestMySQLManagementSQLStdinPasswordProofAndExactDatabaseGrant(t *testing.T) {
	// A fake mysql executable records stdin and argv. No database is opened.
	bash := managementTestBash(t)
	root := t.TempDir()
	for _, directory := range []string{"work", "config", "runtime/mysql/bin"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("config/health.cnf", "[client]\nuser=root\npassword=initial\n")
	write("password", "Some'Complex\\Password9")
	write("runtime/mysql/bin/mysql", `#!/bin/bash
set -e
printf '%s\n' "$*" >> "$OPS_INSTALL_ROOT/argv"
sql=$(cat)
printf '%s\n' "$sql" >> "$OPS_INSTALL_ROOT/sql"
case "$sql" in
 *'SELECT @@global.partial_revokes'*) printf '0\n';;
 *'SELECT SHA2'*) printf '%064d\n' 1;;
 *'COUNT(DISTINCT PRIVILEGE_TYPE)'*) printf '2\n';;
esac
`)
	base := []string{"PATH=/usr/bin:/bin", "OPS_INSTALL_ROOT=" + filepath.ToSlash(root), "OPS_JOB_ID=" + uuid.NewString(), "OPS_RUNTIME_KIND=SYSTEMD", "OPS_PARAM_ADMIN_USER=root", "OPS_PARAM_USERNAME=app_user", "OPS_PARAM_HOST=localhost", "OPS_SECRET_PASSWORD_FILE=" + filepath.ToSlash(filepath.Join(root, "password")), "OPS_PARAM_DATABASE=app_data", "OPS_PARAM_PERMISSION=只读"}
	run := func(script string, env ...string) (string, error) {
		cmd := exec.Command(bash, "--noprofile", "--norc")
		cmd.Env = append(append(os.Environ(), base...), env...)
		cmd.Stdin = strings.NewReader(mysqlManagementCommand(script, false))
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	if output, err := run("mysql-verify-password", "OPS_OPERATION_KEY=create-user"); err == nil {
		t.Fatal("account existence was accepted without a mutation proof", output)
	}
	for _, script := range []string{"mysql-password", "mysql-verify-password"} {
		if output, err := run(script, "OPS_OPERATION_KEY=create-user"); err != nil || strings.Contains(output, "Complex") {
			t.Fatal(script, err, output)
		}
	}
	for _, script := range []string{"mysql-grant", "mysql-verify-grant"} {
		if output, err := run(script, "OPS_OPERATION_KEY=grant-database"); err != nil {
			t.Fatal(script, err, output)
		}
	}
	sql, _ := os.ReadFile(filepath.Join(root, "sql"))
	argv, _ := os.ReadFile(filepath.Join(root, "argv"))
	if !strings.Contains(string(sql), "IDENTIFIED BY 'Some''Complex\\Password9'") || !strings.Contains(string(sql), "ON `app\\_data`.*") || strings.Contains(string(argv), "Complex") || strings.Contains(string(sql), "ON *.*") {
		t.Fatal("password transport or exact database grant changed", string(argv))
	}
	for _, value := range []string{"root", "bad';DROP USER x;--"} {
		if output, err := run("mysql-password", "OPS_OPERATION_KEY=change-password", "OPS_PARAM_USERNAME="+value); err == nil {
			t.Fatal("unsafe business account accepted", output)
		}
	}
	remaining, _ := filepath.Glob(filepath.Join(root, "work/.mysql-options.*"))
	if len(remaining) != 0 {
		t.Fatal("temporary credential file retained", remaining)
	}
}
