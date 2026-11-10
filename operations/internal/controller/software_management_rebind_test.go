package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestStockAnnotationRebindRequestsFitBoundedExecutorInput(t *testing.T) {
	for _, d := range annotationSoftwareDefinitions() {
		i := serviceinstall.Request{SoftwareID: "11111111-1111-4111-8111-111111111111", BindingRevision: 1, Definition: d, Config: serviceinstall.Config{Name: d.Name, RuntimeMode: d.RuntimeModes[0], BindAddress: "0.0.0.0", HostPort: d.Port, MemoryMiB: d.DefaultMemoryMiB}, Source: serviceinstall.Source{Type: d.SourceModes[0]}}
		if i.Source.Type == "ARCHIVE" {
			i.Source.SHA256, i.Source.ByteSize = strings.Repeat("a", 64), 1
		}
		if err := i.Validate(); err != nil {
			t.Fatal(d.Key, err)
		}
		req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, Action: "SOFTWARE_OPERATION", SoftwareOperation: "SOFTWARE_OPERATION", Management: &serviceinstall.ManagementRequest{Definition: d, SoftwareID: i.SoftwareID, BindingRevision: 1, Root: "/opt/streamfusion-ops-target/software/" + i.SoftwareID, OperationKey: "configure", Installation: &i}, Software: &deploy.SoftwareRequest{ID: i.SoftwareID, Version: 1, Kind: d.Commands.Kind, IdentityHash: strings.Repeat("b", 64)}, InputHash: strings.Repeat("c", 64), CommandSignature: strings.Repeat("s", 88)}
		raw, err := json.Marshal(req)
		if err != nil || len(raw) > 128<<10 {
			t.Fatal("stock rebind exceeds the bounded target request", d.Key, len(raw), err)
		}
		if len(raw)+1 > deploy.ExecutorRequestLimit(req) {
			t.Fatal("controller sender would reject its stock configuration request", d.Key, len(raw))
		}
		if strings.Contains(d.Key, "py312") && len(raw) <= 64<<10 {
			t.Fatal("test no longer exercises a request larger than the old limit", len(raw))
		}
		parsed, _, err := deploy.ReadExecutorRequest(bytes.NewReader(append(raw, '\n')))
		if err != nil || serviceinstall.Digest(parsed.Management.Definition) != serviceinstall.Digest(d) || serviceinstall.Digest(parsed.Management.Installation) != serviceinstall.Digest(i) {
			t.Fatal("stock configuration request could not cross the actual executor reader", d.Key, err)
		}
		if strings.Contains(d.Key, "py312") && i.Source.Type == "ONLINE" {
			s, b := setup(t)
			b.login()
			seen := false
			hostID, _ := installSSHFixture(t, s, b, func(actual deploy.Request) ([]byte, bool) {
				if actual.Action != "SOFTWARE_OPERATION_CHECK" || actual.Management == nil {
					return nil, false
				}
				seen = serviceinstall.Digest(actual.Management.Definition) == serviceinstall.Digest(d) && serviceinstall.Digest(actual.Management.Installation) == serviceinstall.Digest(i)
				body, _ := json.Marshal(serviceinstall.ManagementPreflight{Ready: true})
				return body, true
			})
			host, err := s.readTarget(hostID)
			if err != nil {
				t.Fatal(err)
			}
			req.Action, req.InputHash = "SOFTWARE_OPERATION_CHECK", ""
			var checked serviceinstall.ManagementPreflight
			if err := s.callTarget(context.Background(), host, req, "", &checked); err != nil || !seen || !checked.Ready {
				t.Fatal("large stock request failed the real SSH sender", seen, err)
			}
		}
	}
}

func configurationBindingFixture(t *testing.T) (deploy.Request, deploy.Result) {
	t.Helper()
	id := "11111111-1111-4111-8111-111111111111"
	step := serviceinstall.CommandStep{Name: "配置", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10}
	d := serviceinstall.Definition{Key: "custom-config-test", Name: "测试服务", Version: "1", Revision: "1", Kind: "SERVICE", Platform: "linux/amd64", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"ONLINE"}, Commands: &serviceinstall.CommandRecipe{Kind: "SYSTEMD", Locator: "ops-app-${OPS_SOFTWARE_ID}.service", BinaryPath: "${OPS_INSTALL_ROOT}/bin/run"}}
	for _, phase := range []string{"INSTALL", "VERIFY", "UNINSTALL"} {
		s := step
		s.Phase = phase
		d.Commands.Steps = append(d.Commands.Steps, s)
	}
	i := &serviceinstall.Request{SoftwareID: id, BindingRevision: 1, Definition: d, Config: serviceinstall.Config{Name: "测试服务", RuntimeMode: "NATIVE", MemoryMiB: 512}, Source: serviceinstall.Source{Type: "ONLINE"}}
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	d.Management = &serviceinstall.ManagementDefinition{Operations: []serviceinstall.ManagementOperation{{Key: "configure", Name: "编辑配置", Rebind: true, Steps: []serviceinstall.CommandStep{step}, VerifySteps: []serviceinstall.CommandStep{step}, RollbackSteps: []serviceinstall.CommandStep{step}, ResultFormat: "TEXT"}}}
	root := "/opt/ops/software/" + id
	m := &serviceinstall.ManagementRequest{Definition: d, SoftwareID: id, BindingRevision: 1, Root: root, OperationKey: "configure", Installation: i}
	old := &deploy.SoftwareRequest{ID: id, Version: 1, Kind: "SYSTEMD", Locator: "ops-app-" + id + ".service", IdentityHash: strings.Repeat("a", 64), Ownership: &deploy.SoftwareOwnership{BinaryPath: root + "/bin/run", BinaryHash: strings.Repeat("b", 64)}}
	fact := &deploy.SoftwareObservation{Kind: old.Kind, Locator: old.Locator, IdentityHash: strings.Repeat("c", 64), State: "RUNNING", ObservedAt: Now()}
	e := &serviceinstall.Evidence{SoftwareID: id, BindingRevision: 2, RecipeKey: d.Key, RecipeRevision: "1", ConfigHash: serviceinstall.Digest(i.Config), Kind: old.Kind, Locator: old.Locator, IdentityHash: fact.IdentityHash, Health: "READY", ObservedVersion: "1", ObservedAt: Now(), BinaryPath: old.Ownership.BinaryPath, BinaryHash: strings.Repeat("d", 64)}
	return deploy.Request{JobID: "22222222-2222-4222-8222-222222222222", Management: m, Software: old}, deploy.Result{Status: "SUCCEEDED", Software: fact, Installation: e}
}

func TestConfigurationBindingOnlyAcceptsExplicitVerifiedReplacement(t *testing.T) {
	req, result := configurationBindingFixture(t)
	if err := validateManagementBindingResult(req, result); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*deploy.Request, *deploy.Result){
		func(r *deploy.Request, _ *deploy.Result) {
			r.Management.Definition.Management.Operations[0].Rebind = false
		},
		func(_ *deploy.Request, r *deploy.Result) { r.Installation = nil },
		func(_ *deploy.Request, r *deploy.Result) { r.Installation.BindingRevision = 1 },
		func(_ *deploy.Request, r *deploy.Result) { r.Installation.BinaryPath = "/usr/bin/bash" },
		func(_ *deploy.Request, r *deploy.Result) { r.Software.State = "FAILED" },
		func(_ *deploy.Request, r *deploy.Result) { r.Software.IdentityHash = strings.Repeat("e", 64) },
	} {
		req, result := configurationBindingFixture(t)
		change(&req, &result)
		if validateManagementBindingResult(req, result) == nil {
			t.Fatal("unverified replacement accepted")
		}
	}
}

func TestConfigurationBindingCommitKeepsInstallationOwnershipAndHistory(t *testing.T) {
	s, _, host, id, _ := readyNginxFixture(t)
	req, result := configurationBindingFixture(t)
	req.TargetID = host
	req.Management.SoftwareID, req.Management.Installation.SoftwareID, req.Software.ID = id, id, id
	result.Installation.SoftwareID = id
	var oldJob, historicBefore string
	if err := s.DB.QueryRow("SELECT configuration_job_id FROM software_instances WHERE id=?", id).Scan(&oldJob); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT input_json||result_json FROM jobs WHERE software_plan_id IN (SELECT install_plan_id FROM software_instances WHERE id=?)", id).Scan(&historicBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE software_instances SET last_job_id=?,binding_revision=?,identity_hash=? WHERE id=?", req.JobID, req.Software.Version, req.Software.IdentityHash, id); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	changed, err := commitManagementBinding(tx, req, result)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		t.Fatal("binding was not committed")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var identity, currentJob, raw string
	var revision int64
	if err = s.DB.QueryRow("SELECT identity_hash,binding_revision,configuration_job_id,installation_json FROM software_instances WHERE id=?", id).Scan(&identity, &revision, &currentJob, &raw); err != nil {
		t.Fatal(err)
	}
	if identity != result.Software.IdentityHash || revision != 2 || currentJob != oldJob {
		t.Fatal("configuration changed installation job ownership")
	}
	var installed serviceinstall.Request
	if deploy.StrictJSON([]byte(raw), &installed) != nil || installed.BindingRevision != 2 || serviceinstall.Digest(installed.Config) != serviceinstall.Digest(req.Management.Installation.Config) || serviceinstall.Digest(installed.Definition) != serviceinstall.Digest(req.Management.Installation.Definition) {
		t.Fatal("original installation configuration was rewritten")
	}
	var historic string
	if err = s.DB.QueryRow("SELECT input_json||result_json FROM jobs WHERE software_plan_id IN (SELECT install_plan_id FROM software_instances WHERE id=?)", id).Scan(&historic); err != nil || historic != historicBefore {
		t.Fatal("installation history changed", err)
	}
}
