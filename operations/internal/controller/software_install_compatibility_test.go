package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func installCompatibilityCapabilities(req deploy.Request, s *Store) executorCapabilities {
	return executorCapabilities{Owner: deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID}, Platform: "linux/amd64", DockerEngine: true, InstallVersion: serviceinstall.CapabilityVersion, NativeInstallVersion: 1, CatalogHash: "a-historical-catalog-hash", CommandRecipeVersion: deploy.CommandRecipeVersion, CommandTrustFingerprint: deploy.CommandPublicKeyFingerprint(s.commandPublicKey())}
}

func TestNativeOnlinePlanDownloadsOnTargetWithoutControllerMaterial(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-executor", true: "target-download"}[supported], func(t *testing.T) {
			s, b := setup(t)
			b.login()
			host, fixture := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
				if req.Action != "CAPABILITIES" {
					return nil, false
				}
				capability := installCompatibilityCapabilities(req, s)
				if supported {
					capability.NativeDownloadVersion = 1
				}
				raw, _ := json.Marshal(capability)
				return raw, true
			})
			d, _ := serviceinstall.Find("java", "8")
			input := map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{RuntimeMode: "NATIVE", Name: "java8", MemoryMiB: 512}, "source": map[string]string{"type": "ONLINE"}}
			if !supported {
				response := b.call("POST", "/hosts/"+host+"/software-install-plans", input, 409)
				if !strings.Contains(response.Body.String(), "EXECUTOR_UPGRADE_REQUIRED") {
					t.Fatal(response.Body.String())
				}
				return
			}
			plan := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", input, 200))
			accepted := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "native-target-download"}, 202))
			var raw string
			if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", accepted["jobId"]).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var intent deploy.Request
			if err := deploy.StrictJSON([]byte(raw), &intent); err != nil || intent.Installation == nil {
				t.Fatal(err)
			}
			if !intent.Installation.Source.TargetDownload(intent.Installation.Config) || fixture.InstallCount.Load() != 0 {
				t.Fatal("planning uploaded bytes or performed installation")
			}
			if !sameMaterialDefinition(intent.Installation.Definition, d) {
				t.Fatal("fixed vendor URLs or digests changed")
			}
			var materials int
			if err := s.DB.QueryRow("SELECT count(*) FROM installation_materials").Scan(&materials); err != nil || materials != 0 {
				t.Fatal("target online download depends on a controller material", err, materials)
			}
		})
	}
}

func TestNativeDownloadCapabilityDoesNotBlockHistoricalMaterialRequests(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, _ := installSSHFixture(t, s, b)
	target, err := s.readTarget(host)
	if err != nil {
		t.Fatal(err)
	}
	for _, sourceType := range []string{"ONLINE", "ARCHIVE"} {
		source := serviceinstall.Source{Type: sourceType, SHA256: strings.Repeat("a", 64), ByteSize: 100}
		if err := b.h.installTargetReady(context.Background(), target, serviceinstall.Config{RuntimeMode: "NATIVE"}, source); err != nil {
			t.Fatal("existing archive installation requires unrelated download capability", err)
		}
	}
}

func TestServiceInstallHistoricalCatalogStillChecksSelectedRecipeAndPreservesIntent(t *testing.T) {
	s, b := setup(t)
	b.login()
	var checks atomic.Int32
	selected, _ := s.findInstallRecipe("nginx", "1.30.6")
	host, fixture := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		if req.Action == "CAPABILITIES" {
			raw, _ := json.Marshal(installCompatibilityCapabilities(req, s))
			return raw, true
		}
		if req.Action == "SOFTWARE_INSTALL_CHECK" {
			checks.Add(1)
			if req.Installation == nil || serviceinstall.ValidateDefinition(req.Installation.Definition) != nil || serviceinstall.Digest(req.Installation.Definition) != serviceinstall.Digest(selected) {
				t.Error("selected definition changed before target validation")
			}
		}
		return nil, false
	})
	plan := nginxPlan(t, b, host, 18210)
	accepted := dataOf(t, b.call("POST", "/software-install-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "historical-catalog-compatible"}, 202))
	if checks.Load() != 2 || fixture.InstallCount.Load() != 0 {
		t.Fatal("create and submit must both validate without executing installation", checks.Load(), fixture.InstallCount.Load())
	}
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", accepted["jobId"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var intent deploy.Request
	if err := deploy.StrictJSON([]byte(raw), &intent); err != nil || intent.Installation == nil {
		t.Fatal(err)
	}
	if serviceinstall.Digest(intent.Installation.Definition) != serviceinstall.Digest(selected) {
		t.Fatal("compatibility check normalized the frozen recipe")
	}
	hash := intent.InputHash
	intent.InputHash = ""
	if deploy.Digest(intent) != hash {
		t.Fatal("compatibility check changed the existing intent digest contract")
	}
}

func TestServiceInstallUnsupportedSelectedRecipeFailsWithActionableCode(t *testing.T) {
	s, b := setup(t)
	b.login()
	var checks atomic.Int32
	var mutations atomic.Int32
	f := hostSSHServer(t, s, b, true, true)
	respond := func(command string) *hostSSHResponse {
		if command != executorSSHCommand(target{sshTarget: sshTarget{User: "ops"}, InstallRoot: "/opt/streamfusion-ops-target"}) {
			return nil
		}
		response := &hostSSHResponse{}
		response.Input = func(input []byte) {
			var req deploy.Request
			if deploy.StrictJSON(input, &req) != nil {
				t.Error("invalid target request")
				response.Exit = 1
				return
			}
			switch req.Action {
			case "CAPABILITIES":
				raw, _ := json.Marshal(installCompatibilityCapabilities(req, s))
				response.Stdout = string(raw)
			case "SOFTWARE_INSTALL_CHECK":
				checks.Add(1)
				if req.Installation == nil || req.Installation.Definition.Key != "java" {
					t.Error("target did not receive selected recipe")
				}
				// Old executors reject untrusted selected definitions with this
				// fixed stderr code and a nonzero exit, before writing an intent.
				response.Exit, response.Stderr = 1, "SERVICE_INSTALL_INPUT_INVALID\n"
			default:
				mutations.Add(1)
				response.Exit = 1
			}
		}
		return response
	}
	f.Responses.Store(&respond)
	host := f.create(t, b, "old-catalog-selected-recipe")["id"].(string)
	d, _ := serviceinstall.Find("java", "21")
	in := map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": serviceinstall.Config{Name: "unsupported java", MemoryMiB: 512}, "source": map[string]string{"type": "REGISTRY"}}
	response := b.call("POST", "/hosts/"+host+"/software-install-plans", in, 409)
	if !strings.Contains(response.Body.String(), "SERVICE_INSTALL_INPUT_INVALID") || !strings.Contains(response.Body.String(), "升级执行器") || checks.Load() != 1 || mutations.Load() != 0 {
		t.Fatal(response.Body.String(), checks.Load(), mutations.Load())
	}
	var plans, jobs int
	s.DB.QueryRow("SELECT count(*) FROM software_install_plans").Scan(&plans)
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='SOFTWARE_INSTALL'").Scan(&jobs)
	if plans != 0 || jobs != 0 {
		t.Fatal("unsupported recipe created installation intent", plans, jobs)
	}
}

func TestServiceInstallReadinessRejectsIncompatibleVersionsAndRuntime(t *testing.T) {
	for _, test := range []struct {
		name, mode, code string
		edit             func(*executorCapabilities)
	}{
		{"docker-old-catalog-no-compose", "DOCKER", "", func(*executorCapabilities) {}},
		{"native-no-docker", "NATIVE", "", func(c *executorCapabilities) { c.DockerEngine = false }},
		{"old-install-version", "DOCKER", "EXECUTOR_UPGRADE_REQUIRED", func(c *executorCapabilities) { c.InstallVersion = 1 }},
		{"wrong-protocol", "DOCKER", "EXECUTOR_UPGRADE_REQUIRED", func(c *executorCapabilities) { c.Owner.ProtocolVersion = 2 }},
		{"missing-protocol", "DOCKER", "EXECUTOR_UPGRADE_REQUIRED", func(c *executorCapabilities) { c.Owner.ProtocolVersion = 0 }},
		{"wrong-executor-version", "DOCKER", "EXECUTOR_UPGRADE_REQUIRED", func(c *executorCapabilities) { c.Owner.ExecutorVersion = "0.1.0" }},
		{"wrong-owner", "DOCKER", "EXECUTOR_IDENTITY_MISMATCH", func(c *executorCapabilities) { c.Owner.ControllerID = uuid.NewString() }},
		{"wrong-platform", "DOCKER", "INSTALL_PLATFORM_UNSUPPORTED", func(c *executorCapabilities) { c.Platform = "linux/arm64" }},
		{"missing-engine", "DOCKER", "DOCKER_ENGINE_UNAVAILABLE", func(c *executorCapabilities) { c.DockerEngine = false }},
		{"missing-native-version", "NATIVE", "NATIVE_INSTALL_UPGRADE_REQUIRED", func(c *executorCapabilities) { c.NativeInstallVersion = 0 }},
		{"unsupported-runtime", "OTHER", "INSTALL_RUNTIME_UNSUPPORTED", func(*executorCapabilities) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
				if req.Action != "CAPABILITIES" {
					return nil, false
				}
				capability := installCompatibilityCapabilities(req, s)
				test.edit(&capability)
				raw, _ := json.Marshal(capability)
				return raw, true
			})
			target, err := s.readTarget(host)
			if err != nil {
				t.Fatal(err)
			}
			err = b.h.installTargetReady(context.Background(), target, serviceinstall.Config{RuntimeMode: test.mode}, serviceinstall.Source{})
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Error() != test.code {
				t.Fatal(err, "expected", test.code)
			}
			code, message := installTargetError(err)
			if code != test.code || message == "" || strings.Contains(message, "核对执行器、授权和依赖") {
				t.Fatal("lost specific compatibility diagnostic", code, message)
			}
		})
	}
}

func TestServiceInstallRecipeInvocationErrorsRemainSpecificAndSanitized(t *testing.T) {
	for _, expected := range []string{"SERVICE_RECIPE_UNTRUSTED", "SERVICE_INSTALL_INPUT_INVALID"} {
		err := classifyExecutorInvocation(errors.New("ssh exit 1"), expected+"\n")
		code, message := installPreflightError(err)
		if code != expected || !strings.Contains(message, "所选配方") || !strings.Contains(message, "升级执行器") {
			t.Fatal(code, message)
		}
	}
	err := classifyExecutorInvocation(errors.New("ssh exit 1"), "SERVICE_RECIPE_UNTRUSTED password=opaque-secret")
	code, message := installPreflightError(err)
	if code != "INSTALL_PREFLIGHT_FAILED" || strings.Contains(message, "opaque-secret") {
		t.Fatal("untrusted stderr escaped fixed-code classification", code, message)
	}
}

func TestServiceInstallChecksLiveCapabilitiesWithoutCachedReadiness(t *testing.T) {
	for _, status := range []string{"AVAILABLE", "UNAVAILABLE", "UNKNOWN"} {
		t.Run(status, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			var capabilities, checks atomic.Int32
			host, f := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
				if req.Action == "CAPABILITIES" {
					capabilities.Add(1)
				}
				if req.Action == "SOFTWARE_INSTALL_CHECK" {
					checks.Add(1)
				}
				return nil, false
			})
			cached := unknownHostCapabilities()
			cached.Mutation.Status = status
			cached.Mutation.Code = "HISTORICAL_CHECK"
			cached.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			if _, err := s.DB.Exec("UPDATE targets SET capabilities_json=?,observed_at=? WHERE id=?", jsonText(cached), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), host); err != nil {
				t.Fatal(err)
			}
			p := nginxPlan(t, b, host, 18211)
			b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "live-capability-" + status}, 202)
			if capabilities.Load() != 2 || checks.Load() != 2 || f.InstallCount.Load() != 0 {
				t.Fatal("cached status replaced live check or executed installation", capabilities.Load(), checks.Load(), f.InstallCount.Load())
			}
			current, err := s.readTarget(host)
			if err != nil || current.Version != "1" || current.Capabilities.Mutation.Status != status || current.Capabilities.ExpiresAt != cached.ExpiresAt {
				t.Fatal("preflight altered host configuration or historical capability facts", err)
			}
		})
	}
}

func TestServiceInstallFreshCacheCannotAuthorizeChangedExecutor(t *testing.T) {
	s, b := setup(t)
	b.login()
	host, f := installSSHFixture(t, s, b)
	cached := unknownHostCapabilities()
	cached.Mutation = checkedCapability(true, "EXECUTOR_READY", "historical success")
	cached.ExpiresAt = time.Now().Add(hostCapabilityFreshnessTTL).UTC().Format(time.RFC3339Nano)
	if _, err := s.DB.Exec("UPDATE targets SET capabilities_json=? WHERE id=?", jsonText(cached), host); err != nil {
		t.Fatal(err)
	}
	p := nginxPlan(t, b, host, 18212)
	f.OldExecutor.Store(true)
	response := b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "fresh-cache-changed-executor"}, 409)
	if !strings.Contains(response.Body.String(), "EXECUTOR_UPGRADE_REQUIRED") {
		t.Fatal(response.Body.String())
	}
	var jobs int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='SOFTWARE_INSTALL'").Scan(&jobs); err != nil || jobs != 0 || f.InstallCount.Load() != 0 {
		t.Fatal("cached success authorized a changed executor", err, jobs, f.InstallCount.Load())
	}
}
