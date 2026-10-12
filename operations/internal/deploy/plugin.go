package deploy

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/plugin"
)

type pluginMaterial struct {
	InstallationID string            `json:"installationId"`
	Definition     plugin.Definition `json:"definition"`
	Managed        bool              `json:"managed"`
	JobID          string            `json:"jobId"`
	ImageID        string            `json:"imageId"`
}

func pluginCatalogHash() string { return Digest(plugin.All()) }

func requestPluginDefinition(req Request) (plugin.Definition, error) {
	if req.Plugin == nil {
		return plugin.Definition{}, errors.New("PLUGIN_MANIFEST_MISSING")
	}
	p := req.Plugin
	d := plugin.Definition{Key: p.Key, Version: p.Version, Kind: p.Kind, Platform: p.Platform, Image: p.Image, Digest: p.Digest, RecipeRevision: p.RecipeRevision, Command: p.Command, Argument: p.Argument}
	return d, plugin.Validate(d)
}

func validatePluginInput(req Request) error {
	if !ID.MatchString(req.JobID) || !ID.MatchString(req.PluginInstallationID) || !Hex.MatchString(req.InputHash) || (req.PluginOperation != "PLUGIN_INSTALL" && req.PluginOperation != "PLUGIN_REMOVE") {
		return errors.New("JOB_IDENTITY_INVALID")
	}
	frozen := req
	frozen.Action = req.PluginOperation
	frozen.InputHash = ""
	frozen.ResultHash = ""
	frozen.Secrets = nil
	if Digest(frozen) != req.InputHash {
		return errors.New("INPUT_HASH_MISMATCH")
	}
	_, e := requestPluginDefinition(req)
	return e
}

func (x *executor) executePlugin(req Request, output io.Writer) error {
	if e := validatePluginInput(req); e != nil {
		return e
	}
	lock, e := targetLock(x.root)
	if e != nil {
		return e
	}
	defer lock.Close()
	dir := filepath.Join(x.root, "jobs", req.JobID)
	resultFile := filepath.Join(dir, "result.json")
	intentFile := filepath.Join(dir, "intent.json")
	pendingFile := filepath.Join(x.root, "pending.json")
	if req.Action == "STATUS" || req.Action == "ACK" {
		var intent Request
		if e = readJSON(intentFile, &intent); e != nil {
			// The pending intent is durable before the per-job intent is written.
			if !os.IsNotExist(e) {
				return errors.New("TARGET_INTENT_DAMAGED")
			}
			if pe := readJSON(pendingFile, &intent); pe != nil {
				if !os.IsNotExist(pe) || req.Action != "STATUS" {
					return errors.New("TARGET_INTENT_MISMATCH")
				}
				return x.pluginNotStarted(req, dir, output)
			}
		}
		if intent.JobID != req.JobID || intent.InputHash != req.InputHash || validatePluginInput(intent) != nil {
			return errors.New("TARGET_INTENT_MISMATCH")
		}
		var result Result
		if e = readJSON(resultFile, &result); e != nil {
			if req.Action == "ACK" {
				return errors.New("RESULT_NOT_FINAL")
			}
			result = x.reconcilePlugin(intent, dir)
			if result.Status != "UNKNOWN" {
				if e = atomicJSON(resultFile, result); e != nil {
					return e
				}
			}
		}
		if result.JobID != req.JobID || result.InputHash != req.InputHash {
			return errors.New("RESULT_IDENTITY_MISMATCH")
		}
		if req.Action == "STATUS" {
			if result.Status == "SUCCEEDED" {
				if e = x.verifyPluginResult(intent, result); e != nil {
					result.Status, result.ErrorCode = "UNKNOWN", "PLUGIN_ACTUAL_STATE_DRIFT"
				}
			}
			return json.NewEncoder(output).Encode(result)
		}
		if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
			return errors.New("RESULT_NOT_FINAL")
		}
		if Digest(result) != req.ResultHash {
			return errors.New("ACK_RESULT_MISMATCH")
		}
		if result.Status == "SUCCEEDED" {
			if e = x.verifyPluginResult(intent, result); e != nil {
				return errors.New("PLUGIN_ACTUAL_STATE_DRIFT")
			}
		}
		var acknowledged map[string]string
		if readJSON(filepath.Join(dir, "ack.json"), &acknowledged) == nil {
			if acknowledged["ResultHash"] != req.ResultHash {
				return errors.New("ACK_RESULT_MISMATCH")
			}
			var pending Request
			if readJSON(pendingFile, &pending) == nil && pending.JobID == req.JobID && pending.InputHash == req.InputHash {
				if e = os.Remove(pendingFile); e != nil {
					return e
				}
			}
			result.Ack = true
			return json.NewEncoder(output).Encode(result)
		}
		var pending Request
		if readJSON(pendingFile, &pending) != nil || pending.JobID != req.JobID || pending.InputHash != req.InputHash {
			return errors.New("ACK_PENDING_MISMATCH")
		}
		if e = atomicJSON(filepath.Join(dir, "ack.json"), map[string]string{"ResultHash": req.ResultHash}); e != nil {
			return e
		}
		if e = os.Remove(pendingFile); e != nil {
			return e
		}
		result.Ack = true
		return json.NewEncoder(output).Encode(result)
	}
	if req.Action != req.PluginOperation {
		return errors.New("ACTION_NOT_ALLOWED")
	}
	var old Request
	if e = readJSON(intentFile, &old); e == nil {
		if old.InputHash != req.InputHash {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		var result Result
		if e = readJSON(resultFile, &result); e != nil {
			return errors.New("UNKNOWN_RECONCILE_REQUIRED")
		}
		return json.NewEncoder(output).Encode(result)
	} else if !os.IsNotExist(e) {
		return errors.New("TARGET_INTENT_DAMAGED")
	}
	if _, e = os.Lstat(pendingFile); !os.IsNotExist(e) {
		return errors.New("TARGET_PENDING")
	}
	deadline, e := time.Parse(time.RFC3339, req.DeadlineAt)
	if e != nil || time.Now().After(deadline) {
		return errors.New("DEADLINE_EXCEEDED")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e = atomicJSON(pendingFile, req); e != nil {
		return e
	}
	if e = atomicJSON(intentFile, req); e != nil {
		return e
	}
	d, _ := requestPluginDefinition(req)
	result := pluginResult(req, d)
	if e = x.checkpoint(req, "PREFLIGHT"); e != nil {
		return e
	}
	if req.Action == "PLUGIN_INSTALL" {
		result, e = x.installPlugin(req, d, dir)
	} else {
		result, e = x.removePlugin(req, d, dir)
	}
	if e != nil {
		result = pluginResult(req, d)
		result.Status, result.Phase, result.ErrorCode = "FAILED", "PLUGIN_ERROR", e.Error()
	}
	if e = atomicJSON(resultFile, result); e != nil {
		return e
	}
	if e = x.pluginLog(req, result.Phase, result.ErrorCode); e != nil {
		return e
	}
	return json.NewEncoder(output).Encode(result)
}

func pluginResult(req Request, d plugin.Definition) Result {
	return Result{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "UNKNOWN", Phase: "INTENT_RECORDED", PluginKey: d.Key, PluginVersion: d.Version, PluginKind: d.Kind, PluginImageDigest: d.Digest, Platform: d.Platform, ObservedAt: stamp()}
}

func (x *executor) inspectPlugin(d plugin.Definition) (string, error) {
	out, e := x.docker("image", "inspect", "--format", `{{.Id}}|{{.Os}}/{{.Architecture}}|{{join .RepoDigests ","}}`, d.Image+"@"+d.Digest)
	if e != nil {
		return "", errors.New("PLUGIN_IMAGE_MISSING")
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 3)
	if len(parts) != 3 || len(parts[0]) != 71 || !Hex.MatchString(strings.TrimPrefix(parts[0], "sha256:")) || parts[1] != d.Platform {
		return "", errors.New("PLUGIN_DIGEST_MISMATCH")
	}
	found := false
	for _, ref := range strings.Split(parts[2], ",") {
		if strings.HasSuffix(ref, "@"+d.Digest) {
			found = true
		}
	}
	if !found {
		return "", errors.New("PLUGIN_DIGEST_MISMATCH")
	}
	return parts[0], nil
}

func (x *executor) dockerStorage() (string, uint64, error) {
	out, e := x.docker("info", "--format", "{{.DockerRootDir}}")
	root := strings.TrimSpace(string(out))
	if e != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", 0, errors.New("DOCKER_STORAGE_UNAVAILABLE")
	}
	free, e := availableBytes(root)
	if e != nil {
		return "", 0, errors.New("DOCKER_STORAGE_UNAVAILABLE")
	}
	return root, free, nil
}

func (x *executor) probePlugin(d plugin.Definition) (Result, error) {
	result := Result{PluginKey: d.Key, PluginVersion: d.Version, PluginKind: d.Kind, PluginImageDigest: d.Digest, Platform: d.Platform, ObservedAt: stamp()}
	imageID, e := x.inspectPlugin(d)
	if e != nil {
		return result, e
	}
	result.ImageID = imageID
	out, e := x.docker("run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--user", "65534:65534", "--workdir", "/tmp", "--tmpfs", "/tmp:rw,noexec,nosuid,size=32m", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--memory", "256m", "--cpus", "1", "--pids-limit", "64", "--env", "HOME=/tmp", "--entrypoint", d.Command, d.Image+"@"+d.Digest, d.Argument)
	text := strings.TrimSpace(string(out))
	if e != nil || len(text) > 8192 || !plugin.VersionMatches(d, text) {
		return result, errors.New("PLUGIN_VERSION_CHECK_FAILED")
	}
	result.Status, result.Health, result.ObservedVersion = "SUCCEEDED", "PLUGIN_READY", text
	return result, nil
}

func (x *executor) installPlugin(req Request, d plugin.Definition, dir string) (Result, error) {
	material := pluginMaterial{InstallationID: req.PluginInstallationID, Definition: d, JobID: req.JobID}
	file := filepath.Join(x.root, "plugins", req.PluginInstallationID+".json")
	var previous pluginMaterial
	if readJSON(file, &previous) == nil {
		if previous.Definition != d || previous.InstallationID != req.PluginInstallationID {
			return Result{}, errors.New("PLUGIN_RECORD_MISMATCH")
		}
		material.Managed = previous.Managed
	}
	out, e := x.docker("image", "inspect", d.Image+"@"+d.Digest)
	if e != nil && !strings.Contains(string(out), "No such image") && !strings.Contains(string(out), "No such object") {
		return Result{}, errors.New("PLUGIN_INSPECT_FAILED")
	}
	if e != nil {
		material.Managed = true
	}
	if e = atomicJSON(filepath.Join(dir, "plugin-material.json"), material); e != nil {
		return Result{}, e
	}
	// Existing immutable material is rechecked and never retagged.
	if material.Managed && strings.Contains(string(out), "No such") {
		_, free, err := x.dockerStorage()
		if err != nil {
			return Result{}, err
		}
		if free < 512<<20 {
			return Result{}, errors.New("PLUGIN_DISK_SPACE_LOW")
		}
		if e = x.checkpoint(req, "PULLING"); e != nil {
			return Result{}, e
		}
		if _, e = x.docker("pull", "--platform", d.Platform, d.Image+"@"+d.Digest); e != nil {
			return Result{}, errors.New("PLUGIN_PULL_FAILED")
		}
	}
	if e = x.checkpoint(req, "VERIFYING"); e != nil {
		return Result{}, e
	}
	if _, e = x.inspectPlugin(d); e != nil {
		return Result{}, e
	}
	if e = x.checkpoint(req, "VERSION_CHECK"); e != nil {
		return Result{}, e
	}
	actual, e := x.probePlugin(d)
	if e != nil {
		return Result{}, e
	}
	material.ImageID = actual.ImageID
	if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return Result{}, e
	}
	if e = atomicJSON(file, material); e != nil {
		return Result{}, e
	}
	result := pluginResult(req, d)
	result.Status, result.Phase, result.Health, result.ImageID, result.ObservedVersion = "SUCCEEDED", "RECORDED", "PLUGIN_READY", actual.ImageID, actual.ObservedVersion
	return result, nil
}

func (x *executor) removePlugin(req Request, d plugin.Definition, dir string) (Result, error) {
	if e := x.checkpoint(req, "REMOVING"); e != nil {
		return Result{}, e
	}
	file := filepath.Join(x.root, "plugins", req.PluginInstallationID+".json")
	var material pluginMaterial
	e := readJSON(file, &material)
	if e != nil && !os.IsNotExist(e) {
		return Result{}, errors.New("PLUGIN_RECORD_DAMAGED")
	}
	if e == nil && (material.Definition != d || material.InstallationID != req.PluginInstallationID) {
		return Result{}, errors.New("PLUGIN_RECORD_MISMATCH")
	}
	// Keep external cache material. Docker refuses removal when containers use it;
	// no --force, tag deletion, or global prune is allowed.
	if material.Managed {
		shared, e := x.pluginImageShared(material)
		if e != nil {
			return Result{}, e
		}
		if !shared {
			out, e := x.docker("image", "inspect", d.Image+"@"+d.Digest)
			if e != nil && !strings.Contains(string(out), "No such image") && !strings.Contains(string(out), "No such object") {
				return Result{}, errors.New("PLUGIN_INSPECT_FAILED")
			}
			if e == nil {
				if _, e = x.docker("image", "rm", d.Image+"@"+d.Digest); e != nil {
					return Result{}, errors.New("PLUGIN_REMOVE_BLOCKED")
				}
			}
		}
	}
	if e = os.Remove(file); e != nil && !os.IsNotExist(e) {
		return Result{}, e
	}
	result := pluginResult(req, d)
	result.Status, result.Phase, result.Health = "SUCCEEDED", "REMOVED", "PLUGIN_REMOVED"
	if e = atomicJSON(filepath.Join(dir, "removed.json"), result); e != nil {
		return Result{}, e
	}
	return result, nil
}

func (x *executor) pluginImageShared(material pluginMaterial) (bool, error) {
	files, e := filepath.Glob(filepath.Join(x.root, "plugins", "*.json"))
	if e != nil {
		return false, e
	}
	for _, f := range files {
		var other pluginMaterial
		if readJSON(f, &other) != nil {
			return false, errors.New("PLUGIN_RECORD_DAMAGED")
		}
		if other.InstallationID != material.InstallationID && (other.Definition.Digest == material.Definition.Digest || other.ImageID == material.ImageID) {
			return true, nil
		}
	}
	return false, nil
}

func (x *executor) verifyPluginResult(req Request, result Result) error {
	if req.Action == "PLUGIN_REMOVE" {
		if _, e := os.Lstat(filepath.Join(x.root, "plugins", req.PluginInstallationID+".json")); !os.IsNotExist(e) {
			return errors.New("PLUGIN_RECORD_STILL_PRESENT")
		}
		return nil
	}
	d, e := requestPluginDefinition(req)
	if e != nil {
		return e
	}
	imageID, e := x.inspectPlugin(d)
	if e != nil || imageID != result.ImageID {
		return errors.New("PLUGIN_IMAGE_DRIFT")
	}
	return nil
}

// Reconciliation never pulls or deletes an image. It only verifies immutable
// material or a durable removal receipt from the original operation.
func (x *executor) reconcilePlugin(req Request, dir string) Result {
	d, _ := requestPluginDefinition(req)
	result := pluginResult(req, d)
	result.ErrorCode = "PLUGIN_JOB_UNKNOWN"
	if req.Action == "PLUGIN_REMOVE" {
		var removed Result
		if readJSON(filepath.Join(dir, "removed.json"), &removed) == nil && removed.JobID == req.JobID && removed.InputHash == req.InputHash && x.verifyPluginResult(req, removed) == nil {
			return removed
		}
		return result
	}
	var material pluginMaterial
	if readJSON(filepath.Join(dir, "plugin-material.json"), &material) != nil || material.Definition != d || material.JobID != req.JobID {
		return result
	}
	actual, e := x.probePlugin(d)
	if e != nil {
		// A missing image is a confirmed failure, other probe errors remain unknown.
		out, inspectErr := x.docker("image", "inspect", d.Image+"@"+d.Digest)
		if inspectErr != nil && (strings.Contains(string(out), "No such image") || strings.Contains(string(out), "No such object")) {
			result.Status, result.Phase, result.ErrorCode = "FAILED", "PLUGIN_ERROR", "PLUGIN_IMAGE_MISSING"
		}
		return result
	}
	material.ImageID = actual.ImageID
	file := filepath.Join(x.root, "plugins", req.PluginInstallationID+".json")
	if os.MkdirAll(filepath.Dir(file), 0700) != nil || atomicJSON(file, material) != nil {
		return result
	}
	result.Status, result.Phase, result.Health, result.ImageID, result.ObservedVersion, result.ErrorCode = "SUCCEEDED", "RECORDED", "PLUGIN_READY", actual.ImageID, actual.ObservedVersion, ""
	return result
}

func (x *executor) pluginNotStarted(req Request, dir string, output io.Writer) error {
	for _, name := range []string{"intent.json", "progress.json", "result.json", "ack.json", "plugin-material.json", "removed.json"} {
		if _, e := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(e) {
			return errors.New("TARGET_EVIDENCE_PRESENT_OR_DAMAGED")
		}
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	req.Action = req.PluginOperation
	d, _ := requestPluginDefinition(req)
	result := pluginResult(req, d)
	result.Status, result.Phase, result.ErrorCode = "FAILED", "NOT_STARTED", "TARGET_NOT_STARTED"
	if e := atomicJSON(filepath.Join(x.root, "pending.json"), req); e != nil {
		return e
	}
	if e := atomicJSON(filepath.Join(dir, "intent.json"), req); e != nil {
		return e
	}
	if e := atomicJSON(filepath.Join(dir, "result.json"), result); e != nil {
		return e
	}
	return json.NewEncoder(output).Encode(result)
}
