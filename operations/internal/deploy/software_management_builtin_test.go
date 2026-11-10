package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func builtinRebindFixture(t *testing.T, mode string) (*executor, Request, string) {
	t.Helper()
	x, original := serviceRequestFixture(t)
	if mode == "OFFICIAL" {
		original = officialTestRequest(t)
		original.ControllerID, original.TargetID = x.owner.ControllerID, x.owner.TargetID
	} else if mode == "NATIVE" {
		original.Installation.Config.RuntimeMode = "NATIVE"
		original.Installation.Source = serviceinstall.Source{Type: "ONLINE"}
	}
	i := original.Installation
	if mode != "OFFICIAL" {
		i.Config.StorageRoot = "/opt/demo"
	}
	var current serviceinstall.Definition
	for _, definition := range serviceinstall.BuiltinCommandCatalog() {
		if definition.Key == i.Definition.Key && definition.Version == i.Definition.Version {
			current = definition
			break
		}
	}
	current.Management = &serviceinstall.ManagementDefinition{Operations: []serviceinstall.ManagementOperation{{Key: "configure", Name: "配置", Rebind: true, ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{{Name: "应用", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10}}, VerifySteps: []serviceinstall.CommandStep{{Name: "验证", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10}}, RollbackSteps: []serviceinstall.CommandStep{{Name: "恢复", Script: "true", RunAs: "ROOT", TimeoutSeconds: 10}}}}}
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	if mode == "OFFICIAL" && runtime.GOOS == "windows" {
		root = "/opt/ops/software/" + i.SoftwareID
	}
	var s SoftwareRequest
	if mode == "OFFICIAL" {
		s = x.officialSoftware(original, officialInstallation{Plan: *i.Official, BinaryHash: strings.Repeat("a", 64)})
	} else if mode == "NATIVE" {
		s = x.nativeSoftware(original, nativeInstallation{BinaryPath: path.Join(root, serviceinstall.NativeBinaryRelative(i.Definition)), BinaryHash: strings.Repeat("a", 64)}, "")
	} else {
		s = SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: "DOCKER", Locator: strings.Repeat("a", 64), SoftwareKey: "NGINX", Ownership: x.installOwnership(original)}
	}
	s.IdentityHash = Digest([]string{s.Kind, s.Locator, s.Ownership.ImageID})
	req := original
	req.JobID, req.Action, req.SoftwareOperation = "55555555-5555-4555-8555-555555555555", "SOFTWARE_OPERATION", "SOFTWARE_OPERATION"
	req.Installation, req.Software = nil, &s
	req.Management = &serviceinstall.ManagementRequest{Installation: i, Definition: current, SoftwareID: s.ID, BindingRevision: s.Version, Root: root, OperationKey: "configure", ResourceHash: Digest([]serviceinstall.ResolvedResource{})}
	req.InputHash = CommandRequestDigest(req)
	for _, directory := range []string{filepath.Join(x.root, "software"), filepath.Join(x.root, "jobs", original.JobID), filepath.Join(x.root, "jobs", req.JobID)} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeManagementFixture(t, filepath.Join(x.root, "jobs", original.JobID, "intent.json"), original)
	writeManagementFixture(t, x.softwareFile(s.ID), softwareBinding{Software: s, JobID: original.JobID})
	dir := filepath.Join(x.root, "jobs", req.JobID)
	writeManagementFixture(t, filepath.Join(dir, "management-before.json"), managementBefore{InputHash: req.InputHash, State: "RUNNING"})
	if err := validateManagementInput(req); err != nil {
		t.Fatal("invalid fixed installation fixture", mode, req.Management.Validate(), err)
	}
	return x, req, dir
}

func builtinRebindDockerRunner(req Request, mutate func(map[string]any)) func(time.Duration, string, ...string) ([]byte, error) {
	return func(_ time.Duration, _ string, args ...string) ([]byte, error) {
		id := strings.Repeat("b", 64)
		if args[len(args)-1] == req.Software.Locator {
			id = req.Software.Locator
		}
		data := map[string]any{"id": id, "name": "/" + serviceName(req.Software.ID), "imageId": req.Software.Ownership.ImageID, "image": req.Software.Ownership.ImageID, "labels": serviceLabels(req.Software.Ownership), "state": "running"}
		if mutate != nil {
			mutate(data)
		}
		return json.Marshal(data)
	}
}

func TestManagementBuiltinDockerRebindPreservesInstallLabelsAndEvidence(t *testing.T) {
	x, req, _ := builtinRebindFixture(t, "DOCKER")
	x.softwareRun = builtinRebindDockerRunner(req, nil)
	if err := x.validateManagementInstallation(req); err != nil {
		t.Fatal(err)
	}
	candidate, actual, err := x.managementCandidate(context.Background(), req)
	if err != nil || candidate.Locator == req.Software.Locator || !reflect.DeepEqual(candidate.Ownership, req.Software.Ownership) {
		t.Fatal("rebind replaced original install ownership", candidate, err)
	}
	var binding softwareBinding
	readJSON(x.softwareFile(req.Software.ID), &binding)
	receipt := newManagementRebindReceipt(req, binding, candidate, *actual, nil)
	if err := receipt.validate(req); err != nil {
		t.Fatal("builtin evidence rejected", err)
	}
	for _, field := range []string{"controller", "target", "software", "job", "config"} {
		t.Run(field, func(t *testing.T) {
			x.softwareRun = builtinRebindDockerRunner(req, func(data map[string]any) { data["labels"].(map[string]string)[serviceLabel+field] = req.JobID })
			if _, _, err := x.managementCandidate(context.Background(), req); err == nil {
				t.Fatal("replacement used changed installation ownership")
			}
		})
	}
	for _, field := range []string{"imageId", "name"} {
		t.Run(field, func(t *testing.T) {
			x.softwareRun = builtinRebindDockerRunner(req, func(data map[string]any) { data[field] = "unrelated" })
			if _, _, err := x.managementCandidate(context.Background(), req); err == nil {
				t.Fatal("unrelated container accepted")
			}
		})
	}
}

func TestManagementBuiltinNativeAndOfficialKeepFixedUnitAndBinary(t *testing.T) {
	for _, mode := range []string{"NATIVE", "OFFICIAL"} {
		t.Run(mode, func(t *testing.T) {
			x, req, _ := builtinRebindFixture(t, mode)
			if !managementInstallationOwnership(req) {
				t.Fatal("fixed native ownership rejected")
			}
			if runtime.GOOS == "linux" || mode != "OFFICIAL" {
				if err := x.validateManagementInstallation(req); err != nil {
					t.Fatal(err)
				}
			}
			candidate := *req.Software
			candidate.Version++
			candidate.IdentityHash = strings.Repeat("b", 64)
			actual := SoftwareObservation{Kind: candidate.Kind, Locator: candidate.Locator, IdentityHash: candidate.IdentityHash, State: "RUNNING"}
			var binding softwareBinding
			readJSON(x.softwareFile(req.Software.ID), &binding)
			receipt := newManagementRebindReceipt(req, binding, candidate, actual, nil)
			if err := receipt.validate(req); err != nil {
				t.Fatal("fixed native evidence rejected", err)
			}
			var changedReceipt managementRebindReceipt
			raw, _ := json.Marshal(receipt)
			json.Unmarshal(raw, &changedReceipt)
			changedReceipt.Installation.BinaryHash = strings.Repeat("c", 64)
			changedReceipt.Binding.Software.Ownership.BinaryHash = changedReceipt.Installation.BinaryHash
			if changedReceipt.validate(req) == nil {
				t.Fatal("configuration receipt replaced the installed program")
			}
			for _, field := range []string{"unit", "binary", "package-plan"} {
				var changed Request
				raw, _ := json.Marshal(req)
				json.Unmarshal(raw, &changed)
				if field == "unit" {
					changed.Software.Locator = "unrelated.service"
				} else if field == "binary" {
					changed.Software.Ownership.BinaryPath = "/opt/unrelated/binary"
				} else if mode == "OFFICIAL" {
					changed.Software.Ownership.Official.Paths.Binary = "/opt/unrelated/binary"
				} else {
					changed.Management.Installation.Config.HostPort++
				}
				if (field != "package-plan" || mode == "OFFICIAL") && managementInstallationOwnership(changed) || field == "package-plan" && mode != "OFFICIAL" && x.validateManagementInstallation(changed) == nil {
					t.Fatal("changed fixed installation accepted", field)
				}
			}
		})
	}
}

func TestManagementReadsExistingConfigurationSnapshotWithoutChangingIt(t *testing.T) {
	x, req, _ := builtinRebindFixture(t, "DOCKER")
	i := *req.Management.Installation
	config := i.Config
	config.HostPort++
	original := Request{Reconfiguration: &serviceinstall.ChangeRequest{Previous: i, Config: config, ExpectedBindingRevision: i.BindingRevision}}
	current := configurationRequest(original).Installation
	req.Management.Installation = current
	req.Management.BindingRevision, req.Software.Version = current.BindingRevision, current.BindingRevision
	req.Software.Ownership.ConfigHash = Digest(config)
	req.InputHash = CommandRequestDigest(req)
	writeManagementFixture(t, filepath.Join(x.root, "jobs", req.Software.Ownership.JobID, "intent.json"), original)
	writeManagementFixture(t, x.softwareFile(req.Software.ID), softwareBinding{Software: *req.Software, JobID: req.Software.Ownership.JobID})
	x.softwareRun = builtinRebindDockerRunner(req, nil)
	if err := x.validateManagementInstallation(req); err != nil {
		t.Fatal("current configured installation was not reconstructed", err)
	}
	if x.managementRoot(*req.Software) != req.Management.Root {
		t.Fatal("configured installation lost its storage root")
	}
	claims := x.bindingResources(softwareBinding{Software: *req.Software, JobID: req.Software.Ownership.JobID})
	if !reflect.DeepEqual(original.Reconfiguration.Previous, i) {
		t.Fatal("original snapshot changed")
	}
	for _, claim := range claims {
		if claim.Type == "PORT" && claim.Value == "8089" {
			return
		}
	}
	t.Fatal("configured port was not retained", claims)
}

func TestManagementNewPortSemanticsAreCapabilityScoped(t *testing.T) {
	_, builtin, _ := builtinRebindFixture(t, "DOCKER")
	builtin.Management.Definition.Management.Operations[0].Resources = []serviceinstall.ResourceDefinition{{Key: "main", Type: "PORT", Value: "8089", BindAddress: "127.0.0.1", Protocol: "TCP"}}
	if !managementOriginPortsMatch(*builtin.Management, builtin.Management.Installation) {
		t.Fatal("original builtin declared port was lost when Web metadata was published")
	}
	if RequiredCommandRecipeVersion(builtin.Management.Definition) != 9 {
		t.Fatal("builtin rebind reached an executor that cannot validate its ownership")
	}
	_, annotation, _ := rebindRequestFixture(t)
	d := annotation.Management.Definition
	d.Web, d.Port = &serviceinstall.WebAccessDefinition{Scheme: "http", PortKey: "main"}, 9924
	d.Management.Operations[0].Resources = []serviceinstall.ResourceDefinition{{Key: "main", Type: "PORT", Value: "9924", BindAddress: "0.0.0.0", Protocol: "TCP"}}
	if RequiredCommandRecipeVersion(d) != 8 {
		t.Fatal("single existing annotation Web port needlessly required another update")
	}
	d.Web = nil
	if RequiredCommandRecipeVersion(d) != 9 {
		t.Fatal("non-Web port replacement reached an older executor")
	}
	d.Management.Operations = nil
	d.Management.ScriptPrelude = strings.Repeat("x", 16385)
	if RequiredCommandRecipeVersion(d) != 9 {
		t.Fatal("larger signed common source reached an executor with the old bound")
	}
}

func TestMaintainedAdditionalConfigurationRequiresObservedReadOnlyState(t *testing.T) {
	_, req, _ := rebindRequestFixture(t)
	d := req.Management.Definition
	for _, key := range []string{"jenkins", "postgresql", "gitlab", "prometheus", "nacos"} {
		d.CatalogKey = key
		if RequiredCommandRecipeVersion(d) != 8 {
			t.Fatal("unchanged template needlessly required an update", key)
		}
	}
	d.Management.Operations = append(d.Management.Operations, serviceinstall.ManagementOperation{Key: "read-config", ReadOnly: true})
	for _, key := range []string{"jenkins", "postgresql", "gitlab", "prometheus", "nacos"} {
		d.CatalogKey = key
		if RequiredCommandRecipeVersion(d) != 9 {
			t.Fatal("actual configuration reader reached an older executor", key)
		}
	}
	for _, key := range []string{"ytlabel", "labelstudio", "custom-example"} {
		d.CatalogKey = key
		if RequiredCommandRecipeVersion(d) != 8 {
			t.Fatal("unrelated annotation or custom template needlessly required an update", key)
		}
	}
	d.CatalogKey = "jenkins"
	d.Management.Operations[0].Rebind = false
	if RequiredCommandRecipeVersion(d) != 8 {
		t.Fatal("ordinary management unnecessarily acquired the new rebind requirement")
	}
}
