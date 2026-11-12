package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type softwareBinding struct {
	Software  SoftwareRequest                   `json:"software"`
	JobID     string                            `json:"jobId"`
	Resources []serviceinstall.ResolvedResource `json:"resources,omitempty"`
}

func (x *executor) softwareRunner(ctx context.Context, command string, args ...string) ([]byte, error) {
	if x.softwareRun != nil {
		limit := 8 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			limit = time.Until(deadline)
		}
		out, err := x.softwareRun(limit, command, args...)
		if live := installationOutput(ctx); live != nil {
			_, _ = live.Write(out)
		}
		return out, err
	}
	if command == "/usr/bin/docker" {
		limit := 8 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			limit = time.Until(deadline)
		}
		dockerCtx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		return x.dockerWithinContext(dockerCtx, args...)
	}
	// Canonical tool links (for example /usr/bin/java) must resolve to a
	// protected binary. The fixed command set is chosen only by this package.
	resolved, err := filepath.EvalSymlinks(command)
	if err != nil {
		return nil, err
	}
	if err = secureAncestors(resolved); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	commandPath := "/usr/bin:/bin"
	if command == "/usr/sbin/ufw" || command == "/usr/bin/apt-get" || command == "/usr/bin/dpkg" {
		commandPath = "/usr/sbin:/usr/bin:/sbin:/bin"
	}
	cmd.Env = []string{"PATH=" + commandPath, "HOME=/root", "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER="}
	if command == "/usr/sbin/runuser" || command == "/usr/bin/gpasswd" || command == "/usr/bin/env" {
		boundPackageCommand(cmd)
	}
	if command == "/usr/bin/apt-get" || command == "/usr/bin/dpkg" {
		cmd.Env = append(cmd.Env, "DEBIAN_FRONTEND=noninteractive")
		boundPackageCommand(cmd)
	}
	limit := 64 << 10
	if command == "/usr/bin/dpkg-query" {
		limit = packageInventoryLimit
	}
	buffer := &cappedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = commandOutputs(ctx, buffer)
	logExecutedCommand(ctx, command, args...)
	err = cmd.Run()
	return buffer.Bytes(), err
}

func validateSoftwareInput(req Request) error {
	if !ID.MatchString(req.JobID) || !Hex.MatchString(req.InputHash) || !softwareMutation(req.SoftwareOperation) {
		return errors.New("SOFTWARE_JOB_IDENTITY_INVALID")
	}
	if req.RuntimePolicy != nil {
		if req.RuntimePolicy.Version != 1 || req.Software == nil || !runtimePolicyAction(req) || req.Software.Kind == "TOOL" || req.RuntimePolicy.Policy != nil && req.RuntimePolicy.Policy.Validate(req.Software.Kind) != nil || req.SoftwareOperation == "SOFTWARE_RUNTIME_POLICY" && req.RuntimePolicy.Policy == nil || req.Management != nil || req.Installation != nil || req.Reconfiguration != nil || req.Uninstallation != nil {
			return errors.New("RUNTIME_POLICY_INPUT_INVALID")
		}
	} else if req.SoftwareOperation == "SOFTWARE_RUNTIME_POLICY" {
		return errors.New("RUNTIME_POLICY_INPUT_REQUIRED")
	}
	if req.Management != nil {
		if err := validateManagementInput(req); err != nil {
			return err
		}
	} else if req.SoftwareOperation == "SOFTWARE_OPERATION" {
		return errors.New("MANAGEMENT_INPUT_REQUIRED")
	} else if req.GitRepository != nil {
		if err := validateGitRepositoryInput(req); err != nil {
			return err
		}
	} else if req.SoftwareOperation == "GIT_REPOSITORY_CHANGE" {
		return errors.New("GIT_REPOSITORY_INPUT_INVALID")
	} else if req.DockerAccess != nil {
		if err := validateDockerAccessInput(req); err != nil {
			return err
		}
	} else if req.SoftwareOperation == "SOFTWARE_DOCKER_ACCESS" {
		return errors.New("DOCKER_ACCESS_INPUT_REQUIRED")
	} else if req.NodeEnvironment != nil {
		if req.SoftwareOperation != "NODE_ENVIRONMENT_CHANGE" || req.NodeEnvironment.Validate() != nil || req.Software != nil || req.Installation != nil || req.Uninstallation != nil || req.Reconfiguration != nil {
			return errors.New("NODE_ENVIRONMENT_INPUT_INVALID")
		}
	} else if req.Uninstallation != nil {
		if err := validateSoftwareUninstall(req); err != nil {
			return err
		}
	} else if req.Reconfiguration != nil {
		if req.Installation != nil || req.Software == nil || req.Software.Validate(false) != nil || req.SoftwareOperation != "SOFTWARE_CONFIGURE" || req.Reconfiguration.Validate() != nil || req.Software.ID != req.Reconfiguration.Previous.SoftwareID || req.Software.Version != req.Reconfiguration.ExpectedBindingRevision || req.Software.Ownership == nil || req.Software.Ownership.ConfigHash != Digest(req.Reconfiguration.Previous.Config) {
			return errors.New("SERVICE_CONFIGURATION_INPUT_INVALID")
		}
	} else if req.Installation != nil {
		if req.Software != nil || req.SoftwareOperation != "SOFTWARE_INSTALL" || req.Installation.Validate() != nil {
			return errors.New("SERVICE_INSTALL_INPUT_INVALID")
		}
		if serviceinstall.IsOfficial(req.Installation.Definition) && req.Installation.Official.Validate(req.Installation.Definition) != nil {
			return errors.New("OFFICIAL_PLAN_REQUIRED")
		}
	} else if req.Software == nil || req.Software.Validate(false) != nil || req.SoftwareOperation == "SOFTWARE_INSTALL" || req.SoftwareOperation == "SOFTWARE_UNINSTALL" {
		return errors.New("SOFTWARE_JOB_IDENTITY_INVALID")
	}
	if req.Plugin != nil || req.Candidate != nil || req.Baseline != nil || req.Binding != nil {
		return errors.New("SOFTWARE_REQUEST_CONFLICT")
	}
	frozen := req
	frozen.Action, frozen.InputHash, frozen.ResultHash, frozen.Secrets = req.SoftwareOperation, "", "", nil
	frozen.CommandSignature = ""
	if req.ResumeOffset != 0 && (req.Action != "RECOVER" || req.Installation == nil || req.ResumeOffset < 0) {
		return errors.New("INSTALL_RESUME_OFFSET_INVALID")
	}
	frozen.ResumeOffset = 0
	if Digest(frozen) != req.InputHash {
		return errors.New("INPUT_HASH_MISMATCH")
	}
	return nil
}

func softwareResult(req Request) Result {
	return Result{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "UNKNOWN", Phase: "SOFTWARE_PENDING", ObservedAt: stamp()}
}

func (x *executor) executeSoftware(req Request, output io.Writer) error {
	if commandDefinition(req) != nil {
		if err := x.verifyCommandRequest(req); err != nil {
			return err
		}
	} else if req.Software != nil && req.Software.Ownership != nil && req.Software.Ownership.CommandTemplateHash != "" {
		// Custom ownership is established only by a signed installation. An
		// ordinary executor caller cannot forge it to adopt protected objects.
		if req.Action == "SOFTWARE_ADOPT" || req.SoftwareOperation == "SOFTWARE_ADOPT" {
			return errors.New("COMMAND_ADOPTION_FORBIDDEN")
		}
		if req.Action == "SOFTWARE_OBSERVE" || req.Action == "SOFTWARE_LOGS" {
			if err := x.boundSoftware(*req.Software); err != nil {
				return err
			}
		}
	}
	if req.Action == "SOFTWARE_OPERATION_CHECK" {
		if err := validateManagementInput(req); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(x.managementPreflight(req))
	}
	if req.Action == "GIT_REPOSITORY_CHECK" || req.Action == "GIT_REPOSITORY_OBSERVE" || req.Action == "GIT_REPOSITORY_TEST" {
		return x.executeGitRead(req, output)
	}
	if req.GitRepository != nil && req.Action != "GIT_REPOSITORY_CHANGE" && req.Action != "STATUS" && req.Action != "ACK" {
		return errors.New("GIT_REPOSITORY_INPUT_INVALID")
	}
	if req.Action == "SOFTWARE_DOCKER_ACCESS_CHECK" {
		return x.readDockerAccess(req, output)
	}
	if req.Action == "NODE_ENVIRONMENT_CHECK" || req.Action == "NODE_ENVIRONMENT_OBSERVE" || req.Action == "NODE_ENVIRONMENT_DISCOVER" {
		return x.executeNodeRead(req, output)
	}
	if (req.Installation != nil || req.Reconfiguration != nil || req.NodeEnvironment != nil || req.Uninstallation != nil || req.Management != nil || req.Software != nil) && (req.Action == "PROGRESS" || req.Action == "SOFTWARE_INSTALL_LOGS") {
		return x.readServiceInstallation(req, output)
	}
	if req.Action == "SOFTWARE_CONFIGURE_CHECK" {
		if req.Reconfiguration == nil || req.Reconfiguration.Validate() != nil || req.Software == nil {
			return errors.New("SERVICE_CONFIGURATION_INPUT_INVALID")
		}
		return json.NewEncoder(output).Encode(x.configurationPreflight(req))
	}
	if req.Action == "SOFTWARE_INSTALL_CHECK" {
		if req.Installation == nil || req.Installation.Validate() != nil || req.Software != nil || req.Plugin != nil || req.Candidate != nil || req.Baseline != nil || req.Binding != nil {
			return errors.New("SERVICE_INSTALL_INPUT_INVALID")
		}
		return json.NewEncoder(output).Encode(x.serviceInstallPreflight(*req.Installation))
	}
	if req.Software == nil && req.Installation == nil && req.NodeEnvironment == nil && req.GitRepository == nil {
		return errors.New("SOFTWARE_REQUEST_REQUIRED")
	}
	if req.Action == "SOFTWARE_OBSERVE" || req.Action == "SOFTWARE_LOGS" {
		if req.Software == nil || req.Installation != nil {
			return errors.New("SOFTWARE_REQUEST_CONFLICT")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var actual *SoftwareObservation
		var err error
		if req.Action == "SOFTWARE_LOGS" {
			actual, err = ReadSoftwareLogs(ctx, *req.Software, x.softwareRunner)
		} else {
			actual, err = ObserveSoftware(ctx, *req.Software, x.softwareRunner)
		}
		if err != nil {
			return err
		}
		if req.Action == "SOFTWARE_OBSERVE" && actual.Kind != "TOOL" {
			if policy, policyErr := x.softwareRuntimeState(ctx, *req.Software); policyErr == nil {
				actual.RuntimePolicy = policy
			} else {
				actual.RuntimePolicy = &runtimepolicy.State{ObservedAt: stamp(), Reason: "无法读取运行策略，请检查服务配置与读取权限"}
			}
		}
		actual.Log = x.redactSoftwareSecrets(actual.Log)
		result := softwareResult(req)
		result.Status, result.Phase, result.Software = "SUCCEEDED", "SOFTWARE_OBSERVED", actual
		return json.NewEncoder(output).Encode(result)
	}
	if err := validateSoftwareInput(req); err != nil {
		return err
	}
	if req.Action != req.SoftwareOperation && req.Action != "STATUS" && req.Action != "ACK" && !(req.Installation != nil && (req.Action == "RECOVER" || req.Action == "RECOVER_CHECK")) {
		return errors.New("ACTION_NOT_ALLOWED")
	}
	lock, err := targetLock(x.root)
	if err != nil {
		return err
	}
	defer lock.Close()
	return x.softwareLocked(req, output)
}

func (x *executor) softwareLocked(req Request, output io.Writer) error {
	dir := filepath.Join(x.root, "jobs", req.JobID)
	intentFile, resultFile := filepath.Join(dir, "intent.json"), filepath.Join(dir, "result.json")
	pendingFile := filepath.Join(x.root, "pending.json")
	var intent Request
	err := readJSON(intentFile, &intent)
	resuming := req.Action == "RECOVER"
	if resuming || req.Action == "RECOVER_CHECK" {
		offset, resumeErr := x.installResumeOffset(req, dir)
		if resumeErr != nil {
			return resumeErr
		}
		if !resuming {
			return json.NewEncoder(output).Encode(map[string]int64{"offset": offset})
		}
		if offset != req.ResumeOffset {
			return errors.New("INSTALL_RESUME_OFFSET_CHANGED")
		}
	}
	if req.Action == "STATUS" || req.Action == "ACK" {
		if err != nil {
			if !os.IsNotExist(err) {
				return errors.New("TARGET_INTENT_DAMAGED")
			}
			if pe := readJSON(pendingFile, &intent); pe != nil {
				if os.IsNotExist(pe) && req.Action == "STATUS" {
					return x.softwareNotStarted(req, dir, output)
				}
				return errors.New("TARGET_INTENT_MISMATCH")
			}
		}
		if intent.JobID != req.JobID || intent.InputHash != req.InputHash || validateSoftwareInput(intent) != nil {
			return errors.New("TARGET_INTENT_MISMATCH")
		}
		var result Result
		if readJSON(resultFile, &result) != nil {
			if req.Action == "ACK" {
				return errors.New("RESULT_NOT_FINAL")
			}
			result = x.reconcileSoftware(intent, dir)
			if result.Status != "UNKNOWN" {
				if err = atomicJSON(resultFile, result); err != nil {
					return err
				}
			}
		}
		if result.JobID != req.JobID || result.InputHash != req.InputHash {
			return errors.New("RESULT_IDENTITY_MISMATCH")
		}
		if req.Action == "STATUS" {
			if result.Status == "UNKNOWN" {
				result = x.reconcileSoftware(intent, dir)
				if result.Status != "UNKNOWN" {
					if err = atomicJSON(resultFile, result); err != nil {
						return err
					}
				}
			}
			return json.NewEncoder(output).Encode(result)
		}
		return x.ackSoftware(req, intent, result, dir, output)
	}
	if err == nil && !resuming {
		if intent.InputHash != req.InputHash {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		var result Result
		if readJSON(resultFile, &result) != nil {
			return errors.New("UNKNOWN_RECONCILE_REQUIRED")
		}
		return json.NewEncoder(output).Encode(result)
	}
	if err != nil && !os.IsNotExist(err) {
		return errors.New("TARGET_INTENT_DAMAGED")
	}
	deadline, err := time.Parse(time.RFC3339, req.DeadlineAt)
	if !resuming && (err != nil || time.Now().After(deadline)) {
		return errors.New("DEADLINE_EXCEEDED")
	}
	if intent.JobID == "" {
		if _, err = os.Lstat(pendingFile); !os.IsNotExist(err) {
			return errors.New("TARGET_PENDING")
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		frozen := req
		frozen.Action, frozen.ResumeOffset = req.SoftwareOperation, 0
		if err = atomicJSON(pendingFile, frozen); err != nil {
			return err
		}
		if err = atomicJSON(intentFile, frozen); err != nil {
			return err
		}
		if err = x.checkpoint(req, "SOFTWARE_PREFLIGHT"); err != nil {
			return err
		}
	}
	result := softwareResult(req)
	if resuming {
		if err := x.prepareBuiltinResume(req, dir); err != nil {
			return err
		}
		req.Action = req.SoftwareOperation
		req.DeadlineAt = time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
	}
	if err = x.preserveRuntimePolicy(req); err == nil {
		result, err = x.applySoftware(req, dir)
		if err == nil {
			result = x.applyInstalledRuntimePolicy(req, dir, result)
			result = x.cleanupRuntimePolicyAfterUninstall(req, dir, result)
		}
	}
	if err != nil {
		result = softwareResult(req)
		result.Status, result.Phase, result.ErrorCode = "FAILED", "SOFTWARE_REJECTED", err.Error()
		if req.Installation != nil && req.Installation.Definition.Key != "docker" && !serviceinstall.IsNode(req.Installation.Definition) &&
			(err.Error() == "NATIVE_MATERIAL_TRANSFER_FAILED" || err.Error() == "SERVICE_ARCHIVE_TRANSFER_FAILED" || err.Error() == "SERVICE_IMAGE_PULL_INTERRUPTED") {
			result.Status, result.Phase = "UNKNOWN", "INSTALL_TRANSFER_INTERRUPTED"
		}
		if req.Installation != nil && err.Error() == "NATIVE_DOWNLOAD_INTERRUPTED" {
			result.Status, result.Phase = "UNKNOWN", "INSTALL_TRANSFER_INTERRUPTED"
		}
		if commandInstallation(req) != nil && (err.Error() == "COMMAND_STEP_INTERRUPTED" || err.Error() == "COMMAND_STEP_RECONCILE_REQUIRED") {
			result.Status, result.Phase = "UNKNOWN", "COMMAND_STEP_UNCONFIRMED"
		}
		if req.Reconfiguration != nil {
			// configureSoftware returns an error only before its durable stop
			// boundary. Later uncertainty is UNKNOWN and never reaches here.
			// This receipt proves no live configuration was applied; it does not
			// claim that the previous service was observed healthy.
			result.Phase = "CONFIGURATION_NOT_APPLIED"
		}
		if req.Management != nil {
			result.Phase = "OPERATION_NOT_APPLIED"
		}
	}
	if err = atomicJSON(resultFile, result); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func (x *executor) ackSoftware(req, intent Request, result Result, dir string, output io.Writer) error {
	if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
		return errors.New("RESULT_NOT_FINAL")
	}
	if Digest(result) != req.ResultHash {
		return errors.New("ACK_RESULT_MISMATCH")
	}
	if result.Status == "SUCCEEDED" && x.verifySoftwareResult(intent, result) != nil {
		return errors.New("SOFTWARE_ACTUAL_STATE_DRIFT")
	}
	ackFile, pendingFile := filepath.Join(dir, "ack.json"), filepath.Join(x.root, "pending.json")
	var acknowledged map[string]string
	if readJSON(ackFile, &acknowledged) == nil {
		if acknowledged["ResultHash"] != req.ResultHash {
			return errors.New("ACK_RESULT_MISMATCH")
		}
	} else {
		var pending Request
		if readJSON(pendingFile, &pending) != nil || pending.JobID != req.JobID || pending.InputHash != req.InputHash {
			return errors.New("ACK_PENDING_MISMATCH")
		}
		if err := atomicJSON(ackFile, map[string]string{"ResultHash": req.ResultHash}); err != nil {
			return err
		}
	}
	var pending Request
	if readJSON(pendingFile, &pending) == nil && pending.JobID == req.JobID && pending.InputHash == req.InputHash {
		if err := os.Remove(pendingFile); err != nil {
			return err
		}
	}
	result.Ack = true
	return json.NewEncoder(output).Encode(result)
}

func (x *executor) softwareNotStarted(req Request, dir string, output io.Writer) error {
	// Missing intent is not proof of no execution. Keep any remaining evidence,
	// including markers introduced by newer installers, for investigation.
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("TARGET_EVIDENCE_PRESENT_OR_DAMAGED")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			return errors.New("TARGET_EVIDENCE_PRESENT_OR_DAMAGED")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("TARGET_EVIDENCE_PRESENT_OR_DAMAGED")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	req.Action = req.SoftwareOperation
	result := softwareResult(req)
	result.Status, result.Phase, result.ErrorCode = "FAILED", "NOT_STARTED", "TARGET_NOT_STARTED"
	if err := atomicJSON(filepath.Join(x.root, "pending.json"), req); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "intent.json"), req); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "result.json"), result); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

// Keep known per-request credentials out of even the already-redacted log tail.
func (x *executor) redactSoftwareSecrets(value string) string {
	for _, secret := range x.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return RedactSoftwareLogs(value)
}
