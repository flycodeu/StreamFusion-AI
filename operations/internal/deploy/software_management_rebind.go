package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type managementBefore struct {
	InputHash string `json:"inputHash"`
	State     string `json:"state"`
}

type managementRebindReceipt struct {
	InputHash    string                  `json:"inputHash"`
	Binding      softwareBinding         `json:"binding"`
	Installation serviceinstall.Evidence `json:"installation"`
	Software     SoftwareObservation     `json:"software"`
}

func managementRebind(req Request) bool {
	if req.Management == nil {
		return false
	}
	op, err := req.Management.Definition.ManagementOperation(req.Management.OperationKey)
	return err == nil && op.Rebind
}

// The controller supplies the original signed installation, never a replacement
// definition or changed Config. Only its current binding revision may advance.
func (x *executor) validateManagementInstallation(req Request) error {
	if !managementRebind(req) {
		return nil
	}
	m, s := req.Management, req.Software
	i, o := m.Installation, s.Ownership
	if i == nil || o == nil || o.ControllerID != req.ControllerID || o.TargetID != req.TargetID || o.SoftwareID != s.ID || o.ConfigHash != Digest(i.Config) || !managementInstallationOwnership(req) {
		return errors.New("MANAGEMENT_REBIND_INSTALLATION_MISMATCH")
	}
	var binding softwareBinding
	var original Request
	if readJSON(x.softwareFile(s.ID), &binding) != nil || binding.JobID != o.JobID || readJSON(filepath.Join(x.root, "jobs", binding.JobID, "intent.json"), &original) != nil {
		return errors.New("MANAGEMENT_REBIND_INSTALLATION_MISMATCH")
	}
	installed := frozenManagementInstallation(original)
	if installed == nil {
		return errors.New("MANAGEMENT_REBIND_INSTALLATION_MISMATCH")
	}
	previous := *installed
	previous.BindingRevision = i.BindingRevision
	if !reflect.DeepEqual(previous, *i) || m.Root != serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config) {
		return errors.New("MANAGEMENT_REBIND_INSTALLATION_MISMATCH")
	}
	if s.Kind == "DOCKER" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := x.managementContainer(ctx, req, s.Locator)
		return err
	}
	return nil
}

func frozenManagementInstallation(intent Request) *serviceinstall.Request {
	if intent.Installation != nil {
		return intent.Installation
	}
	if intent.Reconfiguration != nil && intent.Reconfiguration.Validate() == nil {
		return configurationRequest(intent).Installation
	}
	return nil
}

func managementInstallationOwnership(req Request) bool {
	i, s := req.Management.Installation, req.Software
	o := s.Ownership
	if !serviceinstall.IsBuiltinCommand(req.Management.Definition) {
		return i.Definition.Commands != nil && o.CommandTemplateHash == Digest(i.Definition) && o.RuntimeMode == i.Config.Mode() && s.Kind == i.Definition.Commands.Kind && (s.Kind != "SYSTEMD" || s.Locator == commandPath(i.Definition.Commands.Locator, req.Management.Root, s.ID))
	}
	if o.CommandTemplateHash != "" || o.Node != nil || !reflect.DeepEqual(o.Official, i.Official) {
		return false
	}
	if i.Config.Mode() == "DOCKER" {
		return s.Kind == "DOCKER" && (o.RuntimeMode == "" || o.RuntimeMode == "DOCKER") && o.ImageID == i.Definition.ImageID && o.BinaryPath == "" && o.BinaryHash == ""
	}
	unit, binary := nativeUnit(s.ID), path.Join(req.Management.Root, serviceinstall.NativeBinaryRelative(i.Definition))
	if serviceinstall.IsOfficial(i.Definition) {
		if i.Official == nil {
			return false
		}
		unit, binary = i.Official.Paths.Unit, i.Official.Paths.Binary
	} else if i.Definition.Key == "docker" {
		unit, binary = "docker.service", "/usr/bin/dockerd"
	}
	return s.Kind == "SYSTEMD" && o.RuntimeMode == "NATIVE" && o.ImageID == "" && s.Locator == unit && o.BinaryPath == binary
}

func managementContainerName(req Request) string {
	if serviceinstall.IsBuiltinCommand(req.Management.Definition) {
		return serviceName(req.Software.ID)
	}
	return commandPath(req.Management.Installation.Definition.Commands.Locator, req.Management.Root, req.Software.ID)
}

func managementContainerOwnership(s SoftwareRequest, image string, labels map[string]string) bool {
	if s.Ownership.CommandTemplateHash == "" {
		return serviceOwnershipMatches(s, "/"+serviceName(s.ID), image, labels)
	}
	return image == s.Ownership.ImageID && labels["streamfusion.ops.software-id"] == s.ID
}

func (x *executor) managementContainer(ctx context.Context, req Request, locator string) (string, error) {
	data, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/docker", "container", "inspect", "--format", `{"id":{{json .Id}},"name":{{json .Name}},"imageId":{{json .Image}},"labels":{{json .Config.Labels}}}`, "--", locator)
	var actual struct {
		ID      string            `json:"id"`
		Name    string            `json:"name"`
		ImageID string            `json:"imageId"`
		Labels  map[string]string `json:"labels"`
	}
	s := req.Software
	if err != nil || json.Unmarshal(data, &actual) != nil || !Hex.MatchString(actual.ID) || actual.Name != "/"+managementContainerName(req) || !managementContainerOwnership(*s, actual.ImageID, actual.Labels) {
		return "", errors.New("MANAGEMENT_REBIND_CONTAINER_MISMATCH")
	}
	return actual.ID, nil
}

func (x *executor) managementCandidate(ctx context.Context, req Request) (SoftwareRequest, *SoftwareObservation, error) {
	s := *req.Software
	o := *s.Ownership
	s.Ownership, s.Version, s.IdentityHash = &o, s.Version+1, ""
	if s.Kind == "DOCKER" {
		name := managementContainerName(req)
		id, err := x.managementContainer(ctx, req, name)
		if err != nil {
			return s, nil, err
		}
		s.Locator = id
	} else if o.BinaryPath != "" {
		info, err := os.Lstat(o.BinaryPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || secureAncestors(o.BinaryPath) != nil {
			return s, nil, errors.New("MANAGEMENT_REBIND_BINARY_UNPROTECTED")
		}
		o.BinaryHash, err = fileSHA(o.BinaryPath)
		if err != nil {
			return s, nil, err
		}
		if serviceinstall.IsBuiltinCommand(req.Management.Definition) && o.BinaryHash != req.Software.Ownership.BinaryHash {
			return s, nil, errors.New("MANAGEMENT_REBIND_BINARY_CHANGED")
		}
	}
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	previousState := x.managementPreviousState(req)
	if err != nil || previousState != "RUNNING" && previousState != "STOPPED" || actual.State != previousState {
		if err == nil {
			err = errors.New("MANAGEMENT_REBIND_STATE_UNCONFIRMED")
		}
		return s, actual, err
	}
	s.IdentityHash = actual.IdentityHash
	return s, actual, nil
}

func newManagementRebindReceipt(req Request, binding softwareBinding, candidate SoftwareRequest, actual SoftwareObservation, resources []serviceinstall.ResolvedResource) managementRebindReceipt {
	i := req.Management.Installation
	e := serviceinstall.Evidence{SoftwareID: i.SoftwareID, BindingRevision: candidate.Version, RecipeKey: i.Definition.Key, RecipeRevision: i.Definition.Revision, ConfigHash: Digest(i.Config), Kind: candidate.Kind, Locator: candidate.Locator, IdentityHash: candidate.IdentityHash, BinaryPath: candidate.Ownership.BinaryPath, BinaryHash: candidate.Ownership.BinaryHash, ImageID: candidate.Ownership.ImageID, ObservedVersion: actual.Version, Health: "READY", ObservedAt: stamp()}
	if candidate.Kind == "DOCKER" {
		e.ContainerID = candidate.Locator
	}
	binding.Software, binding.Resources = candidate, resources
	return managementRebindReceipt{InputHash: req.InputHash, Binding: binding, Installation: e, Software: actual}
}

func (r managementRebindReceipt) validate(req Request) error {
	if req.Management.ValidateRebindEvidence(r.Installation) != nil || r.InputHash != req.InputHash || r.Binding.JobID != req.Software.Ownership.JobID || r.Software.Kind != r.Installation.Kind || r.Software.Locator != r.Installation.Locator || r.Software.IdentityHash != r.Installation.IdentityHash || r.Software.State != "RUNNING" && r.Software.State != "STOPPED" {
		return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
	}
	wanted, actual := *req.Software, r.Binding.Software
	wanted.Version++
	wanted.IdentityHash = r.Installation.IdentityHash
	owner := *wanted.Ownership
	wanted.Ownership = &owner
	if wanted.Kind == "DOCKER" {
		wanted.Locator = r.Installation.Locator
	} else if !serviceinstall.IsBuiltinCommand(req.Management.Definition) {
		owner.BinaryHash = r.Installation.BinaryHash
	}
	if !reflect.DeepEqual(wanted, actual) || actual.Ownership.BinaryPath != r.Installation.BinaryPath || actual.Ownership.BinaryHash != r.Installation.BinaryHash || actual.Ownership.ImageID != r.Installation.ImageID {
		return errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
	}
	return nil
}

func (x *executor) completeManagementRebind(req Request, dir string, evidence serviceinstall.ManagementEvidence) (Result, error) {
	r := softwareResult(req)
	var receipt managementRebindReceipt
	var binding softwareBinding
	if readJSON(filepath.Join(dir, "management-rebind.json"), &receipt) != nil || receipt.validate(req) != nil || !reflect.DeepEqual(evidence.Resources, receipt.Binding.Resources) || readJSON(x.softwareFile(req.Software.ID), &binding) != nil || binding.JobID != receipt.Binding.JobID {
		return r, errors.New("MANAGEMENT_REBIND_RECEIPT_MISMATCH")
	}
	if !reflect.DeepEqual(binding, receipt.Binding) && !reflect.DeepEqual(binding.Software, *req.Software) {
		return r, errors.New("SOFTWARE_BINDING_REVISION_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if actual, err := ObserveSoftware(ctx, receipt.Binding.Software, x.softwareRunner); err != nil || actual.State != x.managementPreviousState(req) || receipt.Software.State != x.managementPreviousState(req) {
		return r, errors.New("MANAGEMENT_REBIND_STATE_UNCONFIRMED")
	}
	if !reflect.DeepEqual(binding, receipt.Binding) {
		if err := atomicJSON(x.softwareFile(req.Software.ID), receipt.Binding); err != nil {
			return r, err
		}
	}
	if err := x.cleanupReboundContainer(ctx, req, receipt.Binding.Software); err != nil {
		x.appendServiceLog(req, "MANAGEMENT_CLEANUP", "原受管容器备份暂未清理，可在运行日志中核对；当前配置已确认")
	}
	r.Status, r.Phase, r.Management, r.Software, r.Installation = "SUCCEEDED", "SOFTWARE_OPERATION_COMPLETED", &evidence, &receipt.Software, &receipt.Installation
	r.ConfigHash, r.Health, r.ObservedVersion, r.ImageID = receipt.Installation.ConfigHash, receipt.Installation.Health, receipt.Installation.ObservedVersion, receipt.Installation.ImageID
	return r, nil
}

func (x *executor) cleanupReboundContainer(ctx context.Context, req Request, next SoftwareRequest) error {
	old := req.Software
	if old.Kind != "DOCKER" || next.Kind != "DOCKER" || old.Locator == next.Locator || !Hex.MatchString(old.Locator) || x.boundSoftware(next) != nil {
		return nil
	}
	data, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/docker", "container", "inspect", "--format", `{"id":{{json .Id}},"image":{{json .Image}},"labels":{{json .Config.Labels}},"state":{{json .State.Status}}}`, "--", old.Locator)
	var backup struct {
		ID     string            `json:"id"`
		Image  string            `json:"image"`
		Labels map[string]string `json:"labels"`
		State  string            `json:"state"`
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(data, &backup) != nil || backup.ID != old.Locator || !managementContainerOwnership(*old, backup.Image, backup.Labels) || backup.State != "exited" && backup.State != "created" {
		return errors.New("MANAGEMENT_BACKUP_CONTAINER_UNCONFIRMED")
	}
	_, err = x.sourceSoftwareCommand(ctx, "docker-remove", old.Locator)
	return err
}

func validManagementRestoredResult(req Request, r Result) bool {
	return r.JobID == req.JobID && r.InputHash == req.InputHash && r.Status == "FAILED" && r.Phase == "OPERATION_RESTORED" && r.Management == nil && r.Installation == nil && r.Software != nil && r.Software.Kind == req.Software.Kind && r.Software.Locator == req.Software.Locator && r.Software.IdentityHash == req.Software.IdentityHash && (r.Software.State == "RUNNING" || r.Software.State == "STOPPED")
}

func (x *executor) restoreManagement(req Request, dir string, op serviceinstall.ManagementOperation, cause error) Result {
	r := softwareResult(req)
	r.Phase, r.ErrorCode = "MANAGEMENT_RESULT_UNCONFIRMED", cause.Error()
	var before managementBefore
	if readJSON(filepath.Join(dir, "management-before.json"), &before) != nil || before.InputHash != req.InputHash || before.State != "RUNNING" && before.State != "STOPPED" || x.boundSoftware(*req.Software) != nil {
		return r
	}
	account, err := managementAccount(*req.Management)
	values, refs, valuesErr := managementValues(*req.Management)
	if err != nil || valuesErr != nil {
		return r
	}
	if _, err = x.runManagementSteps(req, dir, op.RollbackSteps, "ROLLBACK", account, values, refs); err != nil {
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil || actual.State != before.State {
		return r
	}
	r.Status, r.Phase, r.Software = "FAILED", "OPERATION_RESTORED", actual
	if err = atomicJSON(filepath.Join(dir, "management-restored.json"), r); err != nil {
		return softwareResult(req)
	}
	return r
}

func (x *executor) managementPreviousState(req Request) string {
	var before managementBefore
	if readJSON(filepath.Join(x.root, "jobs", req.JobID, "management-before.json"), &before) == nil && before.InputHash == req.InputHash {
		return before.State
	}
	return ""
}

func managementOperationPhase(stage string) string {
	if stage == "ROLLBACK" {
		return stage
	}
	return "APPLY"
}

func managementStepScript(m serviceinstall.ManagementRequest, step serviceinstall.CommandStep) string {
	if m.Definition.Management.ScriptPrelude == "" {
		return step.Script
	}
	return m.Definition.Management.ScriptPrelude + "\n" + step.Script
}
