package deploy

import (
	"context"
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

type configurationMarker struct {
	JobID        string          `json:"jobId"`
	InputHash    string          `json:"inputHash"`
	Previous     SoftwareRequest `json:"previous"`
	Root         string          `json:"root"`
	WasRunning   bool            `json:"wasRunning"`
	NewContainer string          `json:"newContainer"`
	NewIdentity  string          `json:"newIdentity"`
	Healthy      bool            `json:"healthy"`
	Restored     bool            `json:"restored"`
	Version      string          `json:"version"`
}

func configurationRequest(req Request) Request {
	target := req
	installation := req.Reconfiguration.Previous
	installation.Config = req.Reconfiguration.Config
	installation.BindingRevision++
	target.Installation = &installation
	target.Reconfiguration = nil
	target.Software = nil
	return target
}
func (x *executor) configurationPreflight(req Request) serviceinstall.Preflight {
	target := configurationRequest(req)
	i := target.Installation
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	p := serviceinstall.Preflight{Ready: true, Root: root, DataDirectory: filepath.Join(root, "data"), ConfigDirectory: filepath.Join(root, "config"), Checks: []serviceinstall.Check{}, PortMappings: serviceinstall.PortMappings(i.Definition, i.Config)}
	fail := func(key, code, message string) {
		p.Ready = false
		p.Checks = append(p.Checks, serviceinstall.Check{Key: key, Status: "FAIL", Code: code, Message: message})
	}
	if req.Reconfiguration.Validate() != nil || req.Software == nil || x.boundSoftware(*req.Software) != nil {
		fail("BINDING", "SOFTWARE_BINDING_REVISION_MISMATCH", "原安装绑定已变化，请重新读取实例")
		return p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil || actual.State != "RUNNING" && actual.State != "STOPPED" {
		fail("STATE", "SOFTWARE_STATE_UNCONFIRMED", "无法确认原服务状态，不能更改配置")
		return p
	}
	if _, _, _, err = x.serviceSecrets(*i); err != nil {
		fail("CREDENTIAL", err.Error(), "新凭据不满足固定配方")
	}
	old := map[string]bool{}
	for _, mapping := range serviceinstall.PortMappings(i.Definition, req.Reconfiguration.Previous.Config) {
		old[Digest(mapping)] = true
	}
	for _, mapping := range p.PortMappings {
		if old[Digest(mapping)] {
			continue
		}
		if availableServicePort(mapping) != nil {
			fail("PORT_"+mapping.Key, "SERVICE_PORT_UNAVAILABLE", "新端口已占用或地址不可用")
		}
		if i.Config.Mode() == "DOCKER" {
			owner, err := x.servicePublishedPortFor(ctx, mapping.BindAddress, mapping.HostPort, mapping.Protocol)
			if err != nil || owner != "" {
				fail("PUBLISHED_"+mapping.Key, "SERVICE_PORT_PUBLISHED", "新发布端口不可确认空闲")
			}
		}
	}
	if i.Config.Mode() == "NATIVE" {
		p.BinaryDirectory = filepath.Dir(req.Software.Ownership.BinaryPath)
		p.LogsDirectory = filepath.Join(root, "logs")
	}
	if p.Ready {
		p.Checks = append(p.Checks, serviceinstall.Check{Key: "CONFIGURATION", Status: "PASS", Message: "仅修改端口和允许的配置；数据与程序位置保持不变"})
	}
	return p
}

func (x *executor) configureSoftware(req Request, dir string) (Result, error) {
	result := softwareResult(req)
	preflight := x.configurationPreflight(req)
	for _, check := range preflight.Checks {
		if check.Status != "PASS" {
			return result, errors.New(check.Code)
		}
	}
	target := configurationRequest(req)
	i := target.Installation
	root := preflight.Root
	ctx, cancel := context.WithTimeout(context.Background(), min(nativeDeadline(req), 5*time.Minute))
	defer cancel()
	before, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil {
		return result, err
	}
	marker := configurationMarker{JobID: req.JobID, InputHash: req.InputHash, Previous: *req.Software, Root: root, WasRunning: before.State == "RUNNING"}
	stage := filepath.Join(root, "config.next-"+req.JobID)
	previous := filepath.Join(root, "config.previous-"+req.JobID)
	config := filepath.Join(root, "config")
	info, err := os.Lstat(config)
	if err != nil || !info.IsDir() || secureAncestors(config) != nil {
		return result, errors.New("SERVICE_CONFIG_DIRECTORY_INVALID")
	}
	_, gid := nativeFileOwner(info)
	if err = os.Mkdir(stage, info.Mode().Perm()); err != nil {
		return result, err
	}
	// Before the stop boundary the new configuration has no live consumer.
	// Remove it on validation failure; after the marker it must be preserved
	// for reconciliation until either commit or confirmed rollback finishes.
	staged := true
	defer func() {
		if staged {
			_ = os.RemoveAll(stage)
		}
	}()
	if err = os.Chown(stage, 0, gid); err != nil {
		return result, err
	}
	var createArgs []string
	if i.Config.Mode() == "NATIVE" {
		if err = x.nativeConfigurationAt(target, root, stage, gid, false); err != nil {
			return result, err
		}
		unit, readErr := os.ReadFile(nativeUnitPath(i.SoftwareID))
		if readErr != nil {
			return result, readErr
		}
		if err = serviceFile(filepath.Join(dir, "unit.previous"), unit, 0, true); err != nil {
			return result, err
		}
	} else if createArgs, err = x.prepareServiceAt(target, root, stage, false); err != nil {
		return result, err
	}
	if err = atomicJSON(filepath.Join(dir, "configuration.json"), marker); err != nil {
		return result, err
	}
	staged = false
	if err = x.checkpoint(req, "CONFIGURATION_STOP"); err != nil {
		return result, nil
	}
	if err = x.changeOwnedState(ctx, *req.Software, "STOPPED"); err != nil {
		return result, nil
	}
	if err = os.Rename(config, previous); err != nil {
		return x.rollbackConfiguration(req, dir, marker), nil
	}
	if err = os.Rename(stage, config); err != nil {
		return x.rollbackConfiguration(req, dir, marker), nil
	}
	var current SoftwareRequest
	if i.Config.Mode() == "NATIVE" {
		if err = replaceProtectedServiceFile(nativeUnitPath(i.SoftwareID), []byte(renderNativeUnit(target, root, nativeUserName(i.SoftwareID)))); err != nil {
			return x.rollbackConfiguration(req, dir, marker), nil
		}
		if _, err = x.softwareRunner(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			return x.rollbackConfiguration(req, dir, marker), nil
		}
		n := nativeInstallation{SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), BinaryPath: req.Software.Ownership.BinaryPath, BinaryHash: req.Software.Ownership.BinaryHash}
		current = x.nativeSoftware(target, n, "")
		actual, observeErr := ObserveSoftware(ctx, current, x.softwareRunner)
		if observeErr != nil {
			return x.rollbackConfiguration(req, dir, marker), nil
		}
		current.IdentityHash = actual.IdentityHash
	} else {
		backupName := serviceName(i.SoftwareID) + "-previous-" + req.JobID
		if _, err = x.softwareRunner(ctx, "/usr/bin/docker", "container", "rename", req.Software.Locator, backupName); err != nil {
			return x.rollbackConfiguration(req, dir, marker), nil
		}
		output, createErr := x.serviceDocker(target, "CONFIGURATION_CREATE", 45*time.Second, createArgs...)
		id := strings.TrimSpace(string(output))
		if createErr != nil || !Hex.MatchString(id) {
			recovered, _, observeErr := x.createdService(target, serviceName(i.SoftwareID))
			if observeErr != nil {
				return x.rollbackConfiguration(req, dir, marker), nil
			}
			current = recovered
		} else {
			current, _, err = x.createdService(target, id)
			if err != nil {
				return result, nil
			}
		}
		marker.NewContainer = current.Locator
	}
	marker.NewIdentity = current.IdentityHash
	if err = atomicJSON(filepath.Join(dir, "configuration.json"), marker); err != nil {
		return result, nil
	}
	if err = x.changeOwnedState(ctx, current, "RUNNING"); err != nil {
		return x.rollbackConfiguration(req, dir, marker), nil
	}
	version, healthErr := x.waitServiceHealth(target, current, 90*time.Second)
	if healthErr != nil {
		return x.rollbackConfiguration(req, dir, marker), nil
	}
	marker.Healthy, marker.Version = true, version
	if err = atomicJSON(filepath.Join(dir, "configuration.json"), marker); err != nil {
		return result, nil
	}
	if !marker.WasRunning {
		if err = x.changeOwnedState(ctx, current, "STOPPED"); err != nil {
			return result, nil
		}
	}
	if err = atomicJSON(x.softwareFile(i.SoftwareID), softwareBinding{Software: current, JobID: req.JobID}); err != nil {
		return result, nil
	}
	result = x.configurationResult(target, current, version, "SUCCEEDED")
	if result.Status != "SUCCEEDED" {
		return result, nil
	}
	if err = atomicJSON(filepath.Join(dir, "configuration-result.json"), result); err != nil {
		return softwareResult(req), nil
	}
	// The original container is removed only after the new identity has a durable
	// confirmed receipt; its bind-mounted data is never removed.
	if i.Config.Mode() == "DOCKER" {
		_, _ = x.softwareRunner(ctx, "/usr/bin/docker", "container", "rm", "--", req.Software.Locator)
	}
	_ = os.RemoveAll(previous)
	return result, nil
}
func (x *executor) configurationResult(req Request, s SoftwareRequest, version, status string) Result {
	var result Result
	if req.Installation.Config.Mode() == "NATIVE" {
		result = x.ownedInstallResult(req, s, version, "READY", status)
	} else {
		result = x.serviceInstallResult(req, s, version, "READY", status)
	}
	if result.Status == status {
		if status == "SUCCEEDED" {
			result.Phase = "SOFTWARE_CONFIGURED"
		} else {
			result.Phase = "CONFIGURATION_RESTORED"
			result.ErrorCode = "SERVICE_CONFIGURATION_REJECTED_RESTORED"
		}
	}
	return result
}
func (x *executor) changeOwnedState(ctx context.Context, s SoftwareRequest, state string) error {
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return err
	}
	if actual.State == state {
		return nil
	}
	action := "start"
	if state == "STOPPED" {
		action = "stop"
	}
	if s.Kind == "SYSTEMD" {
		_, _ = x.softwareRunner(ctx, "/usr/bin/systemctl", action, "--", s.Locator)
	} else {
		args := []string{"container", action, "--", s.Locator}
		if action == "stop" {
			args = []string{"container", "stop", "--time", "20", "--", s.Locator}
		}
		_, _ = x.softwareRunner(ctx, "/usr/bin/docker", args...)
	}
	observeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actual, err = ObserveSoftware(observeCtx, s, x.softwareRunner)
	if err != nil || actual.State != state {
		return errors.New("SOFTWARE_COMMAND_UNCONFIRMED")
	}
	return nil
}
func (x *executor) rollbackConfiguration(req Request, dir string, marker configurationMarker) Result {
	result := softwareResult(req)
	result.ErrorCode = "CONFIGURATION_ROLLBACK_UNCONFIRMED"
	target := configurationRequest(req)
	i := target.Installation
	root := marker.Root
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if i.Config.Mode() == "DOCKER" {
		current, _, err := x.createdService(target, serviceName(i.SoftwareID))
		if err == nil && current.Locator != req.Software.Locator {
			if x.changeOwnedState(ctx, current, "STOPPED") != nil {
				return result
			}
			if _, err = x.softwareRunner(ctx, "/usr/bin/docker", "container", "rm", "--", current.Locator); err != nil {
				return result
			}
		}
		// Rename the original full ID back. No replacement of its identity occurs.
		if _, err = x.softwareRunner(ctx, "/usr/bin/docker", "container", "rename", req.Software.Locator, serviceName(i.SoftwareID)); err != nil {
			if _, observeErr := ObserveSoftware(ctx, *req.Software, x.softwareRunner); observeErr != nil {
				return result
			}
		}
	} else {
		if _, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "stop", "--", req.Software.Locator); err != nil {
			return result
		}
		unit, err := os.ReadFile(filepath.Join(dir, "unit.previous"))
		if err != nil || replaceProtectedServiceFile(nativeUnitPath(i.SoftwareID), unit) != nil {
			return result
		}
	}
	previous := filepath.Join(root, "config.previous-"+req.JobID)
	config := filepath.Join(root, "config")
	failed := filepath.Join(root, "config.failed-"+req.JobID)
	if _, err := os.Lstat(previous); err == nil {
		if _, err = os.Lstat(config); err == nil {
			if err = os.Rename(config, failed); err != nil {
				return result
			}
		} else if !os.IsNotExist(err) {
			return result
		}
		if err = os.Rename(previous, config); err != nil {
			return result
		}
	}
	if i.Config.Mode() == "NATIVE" {
		if _, err := x.softwareRunner(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			return result
		}
	}
	if x.changeOwnedState(ctx, *req.Software, "RUNNING") != nil {
		return result
	}
	baseline := req
	baseline.Installation = &req.Reconfiguration.Previous
	baseline.Reconfiguration = nil
	baseline.Software = nil
	version, err := x.waitServiceHealth(baseline, *req.Software, 30*time.Second)
	if err != nil {
		return result
	}
	if !marker.WasRunning {
		if x.changeOwnedState(ctx, *req.Software, "STOPPED") != nil {
			return result
		}
	}
	// The original binding was never overwritten before successful validation.
	if x.boundSoftware(*req.Software) != nil {
		return result
	}
	marker.Restored, marker.Version = true, version
	if atomicJSON(filepath.Join(dir, "configuration.json"), marker) != nil {
		return result
	}
	result = x.configurationResult(baseline, *req.Software, version, "FAILED")
	if result.Status == "FAILED" {
		if atomicJSON(filepath.Join(dir, "configuration-result.json"), result) != nil {
			return softwareResult(req)
		}
		_ = os.RemoveAll(failed)
		_ = os.RemoveAll(filepath.Join(root, "config.next-"+req.JobID))
	}
	return result
}
func (x *executor) reconcileConfiguration(req Request, dir string) Result {
	unknown := softwareResult(req)
	unknown.ErrorCode = "CONFIGURATION_RECONCILE_REQUIRED"
	var marker configurationMarker
	if readJSON(filepath.Join(dir, "configuration.json"), &marker) != nil || marker.JobID != req.JobID || marker.InputHash != req.InputHash || !reflect.DeepEqual(marker.Previous, *req.Software) {
		return unknown
	}
	var receipt Result
	if readJSON(filepath.Join(dir, "configuration-result.json"), &receipt) == nil && receipt.JobID == req.JobID && receipt.InputHash == req.InputHash {
		return receipt
	}
	target := configurationRequest(req)
	if marker.Restored {
		baseline := req
		baseline.Installation = &req.Reconfiguration.Previous
		baseline.Reconfiguration = nil
		baseline.Software = nil
		if x.boundSoftware(*req.Software) != nil {
			return unknown
		}
		return x.configurationResult(baseline, *req.Software, marker.Version, "FAILED")
	}
	if !marker.Healthy {
		return unknown
	}
	var current SoftwareRequest
	if target.Installation.Config.Mode() == "NATIVE" {
		n := nativeInstallation{SoftwareID: target.Installation.SoftwareID, JobID: req.JobID, ConfigHash: Digest(target.Installation.Config), BinaryPath: req.Software.Ownership.BinaryPath, BinaryHash: req.Software.Ownership.BinaryHash}
		current = x.nativeSoftware(target, n, marker.NewIdentity)
	} else {
		var err error
		current, _, err = x.createdService(target, marker.NewContainer)
		if err != nil || current.IdentityHash != marker.NewIdentity {
			return unknown
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, current, x.softwareRunner)
	if err != nil || !marker.WasRunning && actual.State != "STOPPED" || marker.WasRunning && actual.State != "RUNNING" {
		return unknown
	}
	if err = atomicJSON(x.softwareFile(current.ID), softwareBinding{Software: current, JobID: req.JobID}); err != nil {
		return unknown
	}
	return x.configurationResult(target, current, marker.Version, "SUCCEEDED")
}
func (x *executor) verifyConfiguration(req Request, result Result) error {
	expected := configurationRequest(req)
	if result.Status == "FAILED" {
		expected.Installation = &req.Reconfiguration.Previous
	}
	return x.verifyServiceInstallation(expected, result)
}
