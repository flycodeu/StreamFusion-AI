package deploy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func managementRequestFixture() Request {
	req := commandTestRequest()
	d := req.Installation.Definition
	d.Management = &serviceinstall.ManagementDefinition{Parameters: []serviceinstall.ParameterDefinition{{Key: "MESSAGE", Label: "消息", Type: "TEXT", Default: "default"}}, Operations: []serviceinstall.ManagementOperation{{Key: "show-message", Name: "显示消息", ReadOnly: true, Steps: []serviceinstall.CommandStep{{Name: "输出", Script: `printf '%s' "$OPS_PARAM_MESSAGE"`, RunAs: "ROOT", TimeoutSeconds: 10}}, ResultFormat: "TEXT"}}}
	req.Software = &SoftwareRequest{ID: req.Installation.SoftwareID, Version: 1, Kind: "SYSTEMD", Locator: "redis.service", IdentityHash: Digest("identity")}
	req.Management = &serviceinstall.ManagementRequest{Definition: d, SoftwareID: req.Software.ID, BindingRevision: 1, Root: "/opt/demo", OperationKey: "show-message", ResourceHash: Digest([]serviceinstall.ResolvedResource{})}
	req.Installation = nil
	req.Action, req.SoftwareOperation = "SOFTWARE_OPERATION", "SOFTWARE_OPERATION"
	req.InputHash = Digest(req)
	return req
}

func writeManagementFixture(t *testing.T, file string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManagementSignatureFreezesParametersOperationResourcesAndPermissions(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	req := managementRequestFixture()
	if err := SignCommandRequest(&req, private); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(public)
	if err := verifyCommandSignature(req, key); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Request){
		func(r *Request) { r.Management.Parameters = map[string]string{"MESSAGE": "changed"} },
		func(r *Request) { r.Management.OperationKey = "another" },
		func(r *Request) { r.Management.ResourceHash = Digest("other resources") },
		func(r *Request) { r.Management.Definition.Management.Operations[0].Steps[0].RunAs = "ACCOUNT" },
	} {
		var changed Request
		raw, _ := json.Marshal(req)
		_ = json.Unmarshal(raw, &changed)
		change(&changed)
		if verifyCommandSignature(changed, key) == nil {
			t.Fatal("changed management plan retained valid signature")
		}
	}
}

func TestManagementCredentialTransportIsDeclaredAndAcknowledgementSurvivesRevocation(t *testing.T) {
	req := managementRequestFixture()
	ref := serviceinstall.CredentialReference{ID: "11111111-1111-4111-8111-111111111111", Revision: "1"}
	req.Management.Definition.Management.Parameters = append(req.Management.Definition.Management.Parameters, serviceinstall.ParameterDefinition{Key: "PASSWORD", Label: "密码", Type: "CREDENTIAL", Required: true})
	req.Management.ParameterCredentials = map[string]serviceinstall.CredentialReference{"PASSWORD": ref}
	if validateManagementSecrets(req, nil) == nil {
		t.Fatal("execution accepted missing credential")
	}
	values := map[string]string{serviceinstall.SecretKey(ref.ID, ref.Revision): "private-value"}
	if validateManagementSecrets(req, values) != nil {
		t.Fatal("declared credential rejected")
	}
	req.Action = "ACK"
	if err := validateManagementSecrets(req, nil); err != nil {
		t.Fatal("revoked credential blocked read-only acknowledgement", err)
	}
	if validateManagementSecrets(req, map[string]string{"another:1": "value"}) == nil {
		t.Fatal("status accepted unrelated credentials")
	}
}

func TestParameterEnvironmentKeepsShellSyntaxLiteral(t *testing.T) {
	value := "$(touch /tmp/not-executed); 'quote'\nsecond line"
	env := parameterEnvironment(map[string]string{"VALUE": value, "COUNT": "0"})
	if len(env) != 2 || env[0] != "OPS_PARAM_COUNT=0" || env[1] != "OPS_PARAM_VALUE="+value {
		t.Fatalf("parameters rewritten or unordered: %+v", env)
	}
}

func TestManagementResourcesRejectSymlinkUnownedFilesAndOtherBindings(t *testing.T) {
	x := &executor{root: t.TempDir()}
	root := t.TempDir()
	file := filepath.Join(root, "existing.conf")
	if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	resource := serviceinstall.ResolvedResource{Key: "config", Type: "PATH_WRITE", Value: file}
	if _, err := x.inspectManagementResource(context.Background(), resource, nil, SoftwareRequest{}, filepath.Join(root, "different-instance")); err == nil || err.Error() != "MANAGEMENT_RESOURCE_NOT_OWNED" {
		t.Fatalf("unowned configuration accepted: %v", err)
	}
	owned := []serviceinstall.ResolvedResource{{Type: "PATH_WRITE", Value: root}}
	if _, err := x.inspectManagementResource(context.Background(), resource, owned, SoftwareRequest{}, root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(root, "link")); err == nil {
		if _, _, err := managementPathIdentity(filepath.Join(root, "link")); err == nil {
			t.Fatal("symlink treated as owned plain configuration")
		}
	}
	s := SoftwareRequest{ID: "11111111-1111-4111-8111-111111111111", Kind: "SYSTEMD", Locator: "test.service"}
	if err := os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	writeManagementFixture(t, x.softwareFile(s.ID), softwareBinding{Software: s, JobID: "original-install", Resources: []serviceinstall.ResolvedResource{{Type: "PATH_WRITE", Value: "/srv/managed"}}})
	others, err := x.otherManagedResources("22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	conflict := false
	for _, other := range others {
		if serviceinstall.ResourceConflict(serviceinstall.ResolvedResource{Type: "PATH_WRITE", Value: "/srv/managed/site.conf"}, other) {
			conflict = true
		}
	}
	if !conflict {
		t.Fatal("another instance's write claim was ignored")
	}
}

func TestManagementUncertainExecutionNeverReplaysUserScripts(t *testing.T) {
	req := managementRequestFixture()
	x := &executor{root: t.TempDir()}
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeManagementFixture(t, filepath.Join(dir, "management-started.json"), req.Management)
	calls := 0
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("must not run")
	}
	if result := x.reconcileManagement(req, dir); result.Status != "UNKNOWN" || calls != 0 {
		t.Fatalf("unverified operation replayed: %+v calls=%d", result, calls)
	}
}

func TestManagementOfficialPathsRequireTheFrozenPackageOwnership(t *testing.T) {
	x := &executor{root: t.TempDir()}
	req := officialTestRequest(t)
	s := x.officialSoftware(req, officialInstallation{Plan: *req.Installation.Official, BinaryHash: strings.Repeat("a", 64)})
	contains := func(resources []serviceinstall.ResolvedResource, wanted string) bool {
		for _, resource := range resources {
			if resource.Type == "PATH_WRITE" && resource.Value == wanted {
				return true
			}
		}
		return false
	}
	if !contains(x.bindingResources(softwareBinding{Software: s}), "/etc/redis") {
		t.Fatal("verified package config directory treated as external")
	}
	forged := *s.Ownership.Official
	forged.Paths.OwnedPaths = append([]string{"/"}, forged.Paths.OwnedPaths...)
	s.Ownership.Official = &forged
	resources := x.bindingResources(softwareBinding{Software: s})
	if contains(resources, "/") || contains(resources, "/etc/redis") {
		t.Fatal("forged package path claims acquired ownership")
	}
}

func TestBaseConfigurationCannotOverwriteConfirmedManagedPaths(t *testing.T) {
	for _, resourceType := range []string{"PATH_WRITE", "PATH_READ", "UNIT", ""} {
		name := resourceType
		if name == "" {
			name = "none"
		}
		t.Run(name, func(t *testing.T) {
			x, req := serviceRequestFixture(t)
			previous := *req.Installation
			req.Installation = nil
			req.Reconfiguration = &serviceinstall.ChangeRequest{Previous: previous, Config: previous.Config, ExpectedBindingRevision: 1}
			req.Software = &SoftwareRequest{ID: previous.SoftwareID, Version: 1, Kind: "SYSTEMD", Locator: "nginx.service", IdentityHash: Digest("identity")}
			req.Action, req.SoftwareOperation = "SOFTWARE_CONFIGURE", "SOFTWARE_CONFIGURE"
			binding := softwareBinding{Software: *req.Software}
			if resourceType != "" {
				binding.Resources = []serviceinstall.ResolvedResource{{Type: resourceType, Value: "/srv/managed/config", IdentityHash: Digest("confirmed")}}
			}
			_ = os.MkdirAll(filepath.Dir(x.softwareFile(previous.SoftwareID)), 0700)
			writeManagementFixture(t, x.softwareFile(previous.SoftwareID), binding)
			calls := 0
			x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
				calls++
				return nil, errors.New("observation deliberately unavailable")
			}
			checked := x.configurationPreflight(req)
			blocked := false
			for _, check := range checked.Checks {
				blocked = blocked || check.Code == "MANAGEMENT_CONFIGURATION_REQUIRED"
			}
			if blocked != (resourceType == "PATH_WRITE") || blocked && calls != 0 || !blocked && calls == 0 {
				t.Fatalf("management guard mismatch type=%s checks=%+v calls=%d", resourceType, checked.Checks, calls)
			}
			if blocked {
				dir := t.TempDir()
				if _, err := x.configureSoftware(req, dir); err == nil || err.Error() != "MANAGEMENT_CONFIGURATION_REQUIRED" || calls != 0 {
					t.Fatalf("old base task crossed mutation boundary: %v calls=%d", err, calls)
				}
				if _, err := os.Stat(filepath.Join(dir, "configuration.json")); !os.IsNotExist(err) {
					t.Fatal("rejected base configuration wrote a stop marker")
				}
			}
		})
	}
}

func TestManagementLocalExecutionPassesParametersAndPrivateCredentialFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux bash; no remote target execution")
	}
	req := managementRequestFixture()
	root := t.TempDir()
	req.Management.Root = root
	ref := serviceinstall.CredentialReference{ID: "11111111-1111-4111-8111-111111111111", Revision: "1"}
	secret := "not-a-command-$()"
	x := &executor{root: root, secrets: map[string]string{serviceinstall.SecretKey(ref.ID, ref.Revision): secret}}
	dir := filepath.Join(root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "work"), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "injection-marker")
	value := "$(touch " + marker + "); 'literal'"
	step := serviceinstall.CommandStep{Name: "参数与凭据", RunAs: "ROOT", TimeoutSeconds: 10, Script: `printf '%s\n' "$OPS_PARAM_MESSAGE"; cat "$OPS_SECRET_PASSWORD_FILE"`}
	output, err := x.runManagementSteps(req, dir, []serviceinstall.CommandStep{step}, "APPLY", nil, map[string]string{"MESSAGE": value}, map[string]serviceinstall.CredentialReference{"PASSWORD": ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("parameter was executed as shell source")
	}
	if !strings.Contains(output, value) || strings.Contains(output, secret) {
		t.Fatalf("literal or redaction failed: %q", output)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "service-install.log"))
	if bytes.Contains(log, []byte(secret)) {
		t.Fatal("secret entered execution log")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".ops-secret-*"))
	if len(files) != 0 {
		t.Fatal("transient secret files remained")
	}
}

func TestManagementReleaseRequiresActualAbsenceAndBusyPortsRequireIdentityProof(t *testing.T) {
	x := &executor{root: t.TempDir(), softwareRun: func(time.Duration, string, ...string) ([]byte, error) { return nil, errors.New("unavailable") }}
	file := filepath.Join(t.TempDir(), "site.conf")
	if err := os.WriteFile(file, []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	resource := serviceinstall.ResolvedResource{Type: "PATH_WRITE", Value: file, Release: true}
	if x.managementResourceReleased(context.Background(), resource) == nil {
		t.Fatal("existing file claim released")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("release inspection modified the file", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := x.managementResourceReleased(context.Background(), resource); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	resource = serviceinstall.ResolvedResource{Type: "PORT", Value: port, Protocol: "TCP", BindAddress: "127.0.0.1"}
	if x.managementResourceReleased(context.Background(), resource) == nil {
		t.Fatal("listening port claim released")
	}
	if _, err := x.inspectManagementResource(context.Background(), resource, []serviceinstall.ResolvedResource{resource}, SoftwareRequest{Kind: "SYSTEMD", Locator: "test.service"}, x.root); err == nil {
		t.Fatal("old claim was treated as current socket ownership proof")
	}
}

func TestManagementVerifiedExecutionReturnsObservationAndNeverReplays(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires local Linux bash and directory fsync; services are mocked")
	}
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "identity-drift"}[drift], func(t *testing.T) {
			req := managementRequestFixture()
			x := &executor{root: t.TempDir()}
			req.Management.Root = x.managementRoot(*req.Software)
			config := filepath.Join(req.Management.Root, "config", "site.conf")
			calls := []string{}
			x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
				digest := strings.Repeat("a", 64)
				if _, err := os.Stat(config); err == nil && drift {
					digest = strings.Repeat("b", 64)
				}
				return unitSoftwareRunner("active", digest, &calls)(context.Background(), command, args...)
			}
			actual, err := ObserveSoftware(context.Background(), *req.Software, x.softwareRunner)
			// The fixture starts with an artificial identity; obtain the actual one.
			if err != nil {
				probe := *req.Software
				probe.IdentityHash = ""
				actual, err = ObserveSoftware(context.Background(), probe, x.softwareRunner)
			}
			if err != nil {
				t.Fatal(err)
			}
			req.Software.IdentityHash = actual.IdentityHash
			_ = os.MkdirAll(filepath.Dir(x.softwareFile(req.Software.ID)), 0700)
			writeManagementFixture(t, x.softwareFile(req.Software.ID), softwareBinding{Software: *req.Software})
			op := &req.Management.Definition.Management.Operations[0]
			op.ReadOnly = false
			op.Steps[0].Script = `mkdir -p "$OPS_CONFIG_ROOT"; printf '%s\n' applied >> "$OPS_CONFIG_ROOT/site.conf"`
			op.VerifySteps = []serviceinstall.CommandStep{{Name: "核验", Script: `test -s "$OPS_CONFIG_ROOT/site.conf"`, RunAs: "ROOT", TimeoutSeconds: 10}}
			op.Resources = []serviceinstall.ResourceDefinition{{Key: "site", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/site.conf"}}
			preflight := x.managementPreflight(req)
			if !preflight.Ready {
				t.Fatalf("preflight %+v", preflight)
			}
			req.Management.ResourceHash = preflight.ResourceHash
			dir := filepath.Join(x.root, "jobs", req.JobID)
			_ = os.MkdirAll(dir, 0700)
			result, err := x.applyManagement(req, dir)
			if err != nil {
				t.Fatal(err)
			}
			if drift {
				if result.Status != "UNKNOWN" || result.ErrorCode != "SOFTWARE_IDENTITY_DRIFT" {
					t.Fatalf("drift reported success %+v", result)
				}
			} else if result.Status != "SUCCEEDED" || result.Software == nil || len(result.Management.Resources) != 1 || result.Management.Resources[0].IdentityHash == "" {
				t.Fatalf("verified result incomplete %+v", result)
			}
			recovered := x.reconcileManagement(req, dir)
			if (recovered.Status == "SUCCEEDED") == drift {
				t.Fatalf("recovery status %+v", recovered)
			}
			data, _ := os.ReadFile(config)
			if string(data) != "applied\n" {
				t.Fatalf("recovery replayed operation: %q", data)
			}
		})
	}
}
