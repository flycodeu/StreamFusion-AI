package controller

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestAdditionalSoftwareRejectsUnsafeParametersAndNestedMounts(t *testing.T) {
	bash := additionalTestBash(t)
	prefix := "set -euo pipefail\nfail() { printf '%s\\n' \"$*\" >&2; exit 17; }\nneed() { :; }\n"
	run := func(script string, env ...string) ([]byte, error) {
		command := exec.Command(bash, "-c", prefix+script)
		command.Env = append(os.Environ(), env...)
		return command.CombinedOutput()
	}
	for _, cidr := range []string{"999.0.0.1/24", "127.0.0.1/99"} {
		out, err := run(additionalScript("validate-cidr")+"\nvalidate_cidr \"$TEST_CIDR\"", "TEST_CIDR="+cidr)
		if err == nil {
			t.Fatalf("invalid CIDR accepted: %s %s", cidr, out)
		}
	}
	if out, err := run(additionalScript("validate-cidr") + "\nvalidate_cidr 127.0.0.1/32"); err != nil {
		t.Fatalf("valid CIDR rejected: %s %v", out, err)
	}
	dir := t.TempDir()
	password := filepath.Join(dir, "password")
	if err := os.WriteFile(password, []byte("test-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(additionalScript("secrets")+"\nread_secret \"$TEST_FILE\"", "TEST_FILE="+filepath.ToSlash(password)); err == nil || strings.Contains(string(out), "test-value") {
		t.Fatalf("newline secret accepted or exposed: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(dir, "work"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := run("findmnt() { printf '%s\\n' \"$root/data/external\"; }\n"+additionalScript("mount-check")+"\ncheck_no_mounts \"$root/data\"", "root="+filepath.ToSlash(dir)); err == nil || !strings.Contains(string(out), "挂载点") {
		t.Fatalf("nested mount allowed: %v %s", err, out)
	}

}

func TestAdditionalSoftwareSignedRequestsFitProtocolAndBindValidObjects(t *testing.T) {
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	id := "44444444-4444-4444-8444-444444444444"
	maximumDefinition, maximumRequest := 0, 0
	for _, definition := range additionalSoftwareDefinitions() {
		config := serviceinstall.Config{Name: definition.Name, RuntimeMode: definition.RuntimeModes[0], StorageRoot: "/opt/ops-apps", HostPort: definition.Port, BindAddress: "127.0.0.1", MemoryMiB: definition.DefaultMemoryMiB, Parameters: map[string]string{}, ParameterCredentials: map[string]serviceinstall.CredentialReference{}}
		for _, parameter := range definition.Parameters {
			if parameter.Type == "CREDENTIAL" {
				config.ParameterCredentials[parameter.Key] = serviceinstall.CredentialReference{ID: id, Revision: "1"}
			} else {
				config.Parameters[parameter.Key] = parameter.Default
			}
		}
		source := serviceinstall.Source{Type: definition.SourceModes[0]}
		if source.Type == "ARCHIVE" {
			source.SHA256, source.ByteSize = strings.Repeat("a", 64), 1024
		}
		installation := serviceinstall.Request{SoftwareID: id, BindingRevision: 1, Definition: definition, Config: config, Source: source}
		if err := installation.Validate(); err != nil {
			t.Fatalf("%s config: %v", definition.Key, err)
		}
		ownership := &deploy.SoftwareOwnership{RuntimeMode: config.RuntimeMode, CommandTemplateHash: deploy.Digest(definition), ControllerID: id, TargetID: id, SoftwareID: id, JobID: id, ConfigHash: deploy.Digest(config), ImageID: definition.ImageID}
		locator := strings.ReplaceAll(definition.Commands.Locator, "${OPS_SOFTWARE_ID}", id)
		if definition.Commands.Kind == "DOCKER" {
			locator = strings.Repeat("d", 64)
		} else if definition.Commands.BinaryPath != "" {
			ownership.BinaryPath, ownership.BinaryHash = "/opt/ops-apps/"+id+"/bin/run", strings.Repeat("b", 64)
		}
		software := &deploy.SoftwareRequest{ID: id, Version: 1, Kind: definition.Commands.Kind, Locator: locator, Ownership: ownership, IdentityHash: strings.Repeat("c", 64), SoftwareKey: strings.ToUpper(definition.Key), ExpectedVersion: definition.Version}
		if err := software.Validate(false); err != nil {
			t.Fatalf("%s bound object: %v", definition.Key, err)
		}
		base := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: id, TargetID: id, JobID: id, TraceID: strings.Repeat("a", 32), InputHash: strings.Repeat("b", 64), DeadlineAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}
		install := base
		install.Action, install.SoftwareOperation, install.Installation = "SOFTWARE_INSTALL", "SOFTWARE_INSTALL", &installation
		uninstall := base
		uninstall.Action, uninstall.SoftwareOperation, uninstall.Software = "SOFTWARE_UNINSTALL", "SOFTWARE_UNINSTALL", software
		uninstall.Uninstallation = &deploy.SoftwareUninstallRequest{Installation: installation, DeleteData: true}
		manage := base
		manage.Action, manage.SoftwareOperation, manage.Software = "SOFTWARE_OPERATION", "SOFTWARE_OPERATION", software
		manage.Management = &serviceinstall.ManagementRequest{Definition: definition, SoftwareID: id, BindingRevision: 1, Root: "/opt/ops-apps/" + id, OperationKey: "diagnostics"}
		for _, request := range []*deploy.Request{&install, &uninstall, &manage} {
			if err := deploy.SignCommandRequest(request, key); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(request)
			if err != nil || len(raw) >= 64<<10 {
				t.Fatalf("%s %s wire bytes=%d err=%v", definition.Key, request.Action, len(raw), err)
			}
			maximumRequest = max(maximumRequest, len(raw))
		}
		raw, _ := json.Marshal(definition)
		maximumDefinition = max(maximumDefinition, len(raw))
	}
	t.Logf("largest definition=%d bytes; signed install/uninstall/manage request=%d bytes", maximumDefinition, maximumRequest)
}

func TestAdditionalSoftwareDefinitionsCoverEditableRuntimeSourceMatrix(t *testing.T) {
	definitions := additionalSoftwareDefinitions()
	if len(definitions) != 20 {
		t.Fatalf("definitions=%d", len(definitions))
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		if err := definition.ValidateCommands(); err != nil {
			t.Fatalf("%s: %v", definition.Key, err)
		}
		key := definition.CatalogKey + "/" + definition.RuntimeModes[0] + "/" + definition.SourceModes[0]
		if seen[key] {
			t.Fatalf("duplicate variant: %s", key)
		}
		seen[key] = true
		if !strings.HasPrefix(definition.Key, "custom-") || definition.Commands.Builtin || definition.DefaultMemoryMiB < 512 || definition.Management == nil {
			t.Fatalf("not editable or incomplete: %s", definition.Key)
		}
		for _, step := range definition.Commands.Steps {
			if strings.Contains(step.Script, "@@") || strings.Contains(step.Script, "latest") {
				t.Fatalf("unresolved source in %s/%s", definition.Key, step.Name)
			}
		}
		if definition.RuntimeModes[0] == "DOCKER" && (len(definition.Digest) != 71 || len(definition.ImageID) != 71) {
			t.Fatalf("missing pinned image: %s", definition.Key)
		}
		if definition.Native != nil {
			t.Fatalf("custom variant must not imply cached native material: %s", definition.Key)
		}
		mappings := serviceinstall.PortMappings(definition, serviceinstall.Config{HostPort: definition.Port, BindAddress: "127.0.0.1"})
		main := false
		for _, mapping := range mappings {
			if mapping.Key == "main" && mapping.HostPort == definition.Port {
				main = true
			}
		}
		if !main {
			t.Fatalf("primary service port is not reserved: %s", definition.Key)
		}
	}
	for _, key := range []string{"jenkins", "postgresql", "gitlab", "prometheus", "nacos"} {
		for _, pair := range []string{"DOCKER/REGISTRY", "DOCKER/ARCHIVE", "NATIVE/ONLINE", "NATIVE/ARCHIVE"} {
			if !seen[key+"/"+pair] {
				t.Fatalf("missing %s/%s", key, pair)
			}
		}
	}
}

func TestAdditionalSoftwareSeedingPreservesEditedAndUnpublishedTemplates(t *testing.T) {
	s, _ := setup(t)
	seed := func() {
		t.Helper()
		tx, err := s.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = seedAdditionalSoftwareTemplates(tx); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	key := "custom-prometheus-native-online"
	if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=json_set(draft_json,'$.name','用户修改'),published_json='',version=9,revision=7 WHERE recipe_key=?", key); err != nil {
		t.Fatal(err)
	}
	seed()
	var draft, published string
	var version, revision, count int
	if err := s.DB.QueryRow("SELECT draft_json,published_json,version,revision FROM command_templates WHERE recipe_key=?", key).Scan(&draft, &published, &version, &revision); err != nil {
		t.Fatal(err)
	}
	var edited serviceinstall.Definition
	if err := json.Unmarshal([]byte(draft), &edited); err != nil {
		t.Fatal(err)
	}
	if edited.Name != "用户修改" || published != "" || version != 9 || revision != 7 {
		t.Fatal("user edits or publication state overwritten")
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM command_templates WHERE recipe_key IN ("+strings.TrimSuffix(strings.Repeat("?,", 20), ",")+")", additionalDefinitionKeys()...).Scan(&count); err != nil || count != 20 {
		t.Fatalf("seed count=%d err=%v", count, err)
	}
}

func additionalDefinitionKeys() []any {
	keys := []any{}
	for _, definition := range additionalSoftwareDefinitions() {
		keys = append(keys, definition.Key)
	}
	return keys
}

func additionalTestBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		for _, path := range []string{`D:\Developer Tools\Git\bin\bash.exe`, `C:\Program Files\Git\bin\bash.exe`} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	t.Skip("Bash is unavailable")
	return ""
}

func TestAdditionalSoftwarePublishedCommandsHaveValidBashSyntax(t *testing.T) {
	bash := additionalTestBash(t)
	for _, definition := range additionalSoftwareDefinitions() {
		steps := append([]serviceinstall.CommandStep{}, definition.Commands.Steps...)
		for _, operation := range definition.Management.Operations {
			steps = append(steps, operation.Steps...)
			steps = append(steps, operation.VerifySteps...)
			steps = append(steps, operation.RollbackSteps...)
		}
		for index, step := range steps {
			if strings.Contains(step.Script, "\r\n") {
				t.Fatalf("%s step%d %s contains Windows line endings", definition.Key, index+1, step.Name)
			}
			command := exec.Command(bash, "-n")
			command.Stdin = strings.NewReader(definition.Management.ScriptPrelude + "\n" + step.Script)
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s step%d %s: %v\n%s", definition.Key, index+1, step.Name, err, out)
			}
		}
	}
}

func TestAdditionalSoftwareCommonExecutesInsteadOfFailingOnPipefail(t *testing.T) {
	bash := additionalTestBash(t)
	common := additionalScript("common")
	broken := strings.ReplaceAll(common, "\n", "\r\n")
	if runtime.GOOS == "windows" {
		// Git Bash accepts CRLF scripts. Preserve the exact Linux option token
		// explicitly so this test still executes and reproduces the failure.
		broken = "set -euo $'pipefail\\r'\n"
	}
	for _, test := range []struct {
		name, script string
		fails        bool
	}{
		{"carriage return reproduces install failure", broken, true},
		{"published shared script executes", common, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(bash, "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", test.script+"\nprintf '%s\\n' 'header ready'")
			command.Env = append(os.Environ(), "OPS_INSTALL_ROOT=/tmp/streamfusion-ops-script-test", "OPS_SOFTWARE_ID=11111111-1111-4111-8111-111111111111")
			out, err := command.CombinedOutput()
			if test.fails {
				if err == nil || !strings.Contains(string(out), "invalid option name") {
					t.Fatalf("expected Bash pipefail CRLF failure, got %v: %s", err, out)
				}
			} else if err != nil || string(out) != "header ready\n" {
				t.Fatalf("shared script did not execute: %v: %s", err, out)
			}
		})
	}
}

func TestAdditionalSoftwarePostgresConfigRestoredOnPackageFailure(t *testing.T) {
	bash := additionalTestBash(t)
	dir := t.TempDir()
	for _, existing := range []bool{true, false} {
		for _, terminate := range []bool{false, true} {
			name := map[bool]string{true: "existing", false: "absent"}[existing] + map[bool]string{true: "-terminated", false: "-failed"}[terminate]
			t.Run(name, func(t *testing.T) {
				base := filepath.Join(dir, t.Name()[strings.LastIndex(t.Name(), "/")+1:])
				if err := os.MkdirAll(filepath.Join(base, "work"), 0700); err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(base, "createcluster.conf")
				if existing {
					if err := os.WriteFile(config, []byte("# administrator configuration\ncreate_main_cluster = true\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				script := additionalScript("native-postgresql")
				start, end := strings.Index(script, "cluster_config=/etc/"), strings.Index(script, "[[ \"$(/usr/lib/")
				if start < 0 || end < start {
					t.Fatal("missing protected package installation")
				}
				script = script[start:end]
				script = strings.ReplaceAll(script, "/etc/postgresql-common/createcluster.conf", "$root/createcluster.conf")
				script = strings.ReplaceAll(script, "/etc/postgresql-common", "$root")
				failure, expectedExit := "return 71", 71
				if terminate {
					failure, expectedExit = "kill -TERM $$", 143
				}
				script = "set -euo pipefail\nfail() { echo \"$*\" >&2; exit 1; }\ninstall_packages() { " + failure + "; }\n" + script
				command := exec.Command(bash, "-c", script)
				command.Env = append(os.Environ(), "root="+filepath.ToSlash(base))
				out, runErr := command.CombinedOutput()
				if exit, ok := runErr.(*exec.ExitError); !ok || exit.ExitCode() != expectedExit {
					t.Fatalf("expected exit%d, got %v: %s", expectedExit, runErr, out)
				}
				data, err := os.ReadFile(config)
				if existing {
					if err != nil || string(data) != "# administrator configuration\ncreate_main_cluster = true\n" {
						t.Fatalf("original config was not restored: %q %v", data, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("new shared config left after failure: %v", err)
				}
			})
		}
	}
}
