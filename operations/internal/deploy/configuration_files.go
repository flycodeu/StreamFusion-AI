package deploy

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

var nginxConfigurationFileScript = deployScript("config-files/nginx.sh")

func restorableManagementState(req Request, state string) bool {
	return state == "RUNNING" || state == "STOPPED" ||
		state == "FAILED" && req.Management != nil && req.Management.ConfigFile != nil && req.Software != nil && req.Software.Kind == "SYSTEMD"
}

func (x *executor) runConfigurationFile(ctx context.Context, req Request, stage string) ([]byte, error) {
	f := req.Management.ConfigFile
	if f == nil {
		return nil, errors.New("CONFIG_FILE_INPUT_REQUIRED")
	}
	if _, err := exec.LookPath("/usr/bin/python3"); err != nil {
		return nil, errors.New("CONFIG_FILE_PYTHON_REQUIRED")
	}
	work := filepath.Join(x.root, "jobs", req.JobID)
	if stage == "CHECK" {
		req.JobID = uuid.NewString()
		var err error
		work, err = os.MkdirTemp(filepath.Join(x.root, "jobs"), ".config-check-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(work)
	}
	if secureAncestors(work) != nil {
		return nil, errors.New("CONFIG_FILE_PATH_UNPROTECTED")
	}
	private, err := os.MkdirTemp(work, ".config-files-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(private)
	body := filepath.Join(private, "body.conf")
	result := filepath.Join(private, "result.json")
	if f.Action == "save" && (stage == "CHECK" || stage == "APPLY") {
		content, ok := x.secrets[serviceinstall.ConfigurationFileSecret]
		if !ok || !serviceinstall.ValidConfigurationFileBody(content) || serviceinstall.Digest(content) != f.SHA256 {
			return nil, errors.New("CONFIG_FILE_CONTENT_INVALID")
		}
		if err := os.WriteFile(body, []byte(content), 0600); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", nginxConfigurationFileScript)
	cmd.Dir = work
	listen, listenErr := x.configurationFileListen(req)
	if listenErr != nil {
		return nil, listenErr
	}
	cmd.Env = []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C",
		"OPS_INSTALL_ROOT=" + req.Management.Root,
		"OPS_DATA_ROOT=" + filepath.Join(req.Management.Root, "data"),
		"OPS_CONFIG_ROOT=" + filepath.Join(req.Management.Root, "config"),
		"OPS_SOFTWARE_ID=" + req.Software.ID,
		"OPS_SOFTWARE_LOCATOR=" + req.Software.Locator,
		"OPS_JOB_ID=" + req.JobID,
		"OPS_RUNTIME_KIND=" + req.Software.Kind,
		"OPS_OPERATION_PHASE=" + stage,
		"OPS_PREVIOUS_STATE=" + x.managementPreviousState(req),
		"OPS_NGINX_OFFICIAL=" + strconv.FormatBool(serviceinstall.IsOfficial(req.Management.Definition)),
		"OPS_PARAM_FILE_ACTION=" + f.Action,
		"OPS_PARAM_SITE=" + f.Name,
		"OPS_PARAM_BASE_SHA256=" + f.Revision,
		"OPS_PARAM_LISTEN=" + listen,
		"OPS_CONFIG_CONTENT_FILE=" + body,
		"OPS_CONFIG_RESULT_FILE=" + result,
		"OPS_CONFIG_WORK_DIR=" + work,
	}
	buffer := &cappedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = commandOutputs(ctx, buffer)
	err = cmd.Run()
	data, readErr := os.ReadFile(result)
	if readErr != nil || len(data) == 0 || len(data) > 128<<10 {
		return nil, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
	}
	var report serviceinstall.ConfigurationFileResult
	if StrictJSON(data, &report) != nil || report.Status == "FAILED" || err != nil {
		if stage == "CHECK" {
			return data, err
		}
		if report.Code != "" {
			return nil, errors.New(report.Code)
		}
		return nil, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
	}
	if stage == "CHECK" {
		if !validConfigurationFileReport(*f, report) {
			return nil, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
		}
		return data, nil
	}
	if report.Status != "PASSED" || report.Message == "" {
		return nil, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
	}
	return []byte(report.Message), nil
}

func validConfigurationFileReport(file serviceinstall.ConfigurationFileRequest, report serviceinstall.ConfigurationFileResult) bool {
	if report.Status == "FAILED" {
		return report.Code != "" && report.Message != ""
	}
	switch file.Action {
	case "list":
		return report.Files != nil && len(report.Files) <= 200 && report.Content == "" && (!report.CanCreate || serviceinstall.ConfigurationFileCreationRevision(report.Revision))
	case "read":
		return len(report.Content) <= serviceinstall.ConfigurationFileLimit && (!report.Editable || Hex.MatchString(report.Revision) && serviceinstall.ValidConfigurationFileBody(report.Content))
	default:
		return report.Status == "PASSED" && report.Message != "" && report.Content == "" && len(report.Files) == 0
	}
}

func (x *executor) checkConfigurationFile(req Request) (serviceinstall.ConfigurationFileResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	data, err := x.runConfigurationFile(ctx, req, "CHECK")
	var out serviceinstall.ConfigurationFileResult
	if err != nil && err.Error() == "CONFIG_FILE_PYTHON_REQUIRED" {
		return serviceinstall.ConfigurationFileResult{Status: "FAILED", Code: "CONFIG_FILE_PYTHON_REQUIRED", Message: "配置文件编辑需要服务器已有 Python 3.9 或更新版本"}, err
	}
	if StrictJSON(data, &out) != nil {
		return out, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
	}
	if !validConfigurationFileReport(*req.Management.ConfigFile, out) {
		return out, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
	}
	if req.Management.ConfigFile.Action == "list" && err == nil {
		out.Listen, _ = x.configurationFileListen(req)
	}
	return out, err
}

// Recovery only observes the durable file transaction; it never repeats a
// write or reload after a lost response.
func (x *executor) reconcileConfigurationFile(req Request, dir string) (serviceinstall.ManagementEvidence, error) {
	var evidence serviceinstall.ManagementEvidence
	if req.Management.ConfigFile == nil || x.boundSoftware(*req.Software) != nil {
		return evidence, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	message, err := x.runConfigurationFile(ctx, req, "VERIFY")
	if err != nil {
		return evidence, err
	}
	declared, err := req.Management.ResolveResources(req.Software.Locator)
	if err != nil {
		return evidence, err
	}
	resources, err := x.managementResultResources(ctx, req, declared)
	if err != nil {
		return evidence, err
	}
	evidence = serviceinstall.ManagementEvidence{SoftwareID: req.Software.ID, OperationKey: req.Management.OperationKey, DefinitionHash: Digest(req.Management.Definition), ParametersHash: req.Management.ParameterHash(), ResourceHash: req.Management.ResourceHash, Output: string(message), Resources: resources, ObservedAt: stamp()}
	if evidence.ValidateFor(*req.Management) != nil {
		return evidence, errors.New("CONFIG_FILE_RESULT_UNCONFIRMED")
	}
	if err = atomicJSON(filepath.Join(dir, "management-verified.json"), evidence); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func (x *executor) reconcileRestoredConfigurationFile(req Request, dir string) (Result, error) {
	r := softwareResult(req)
	var before managementBefore
	if readJSON(filepath.Join(dir, "management-before.json"), &before) != nil || before.InputHash != req.InputHash || !restorableManagementState(req, before.State) || x.boundSoftware(*req.Software) != nil {
		return r, errors.New("CONFIG_FILE_ROLLBACK_UNCONFIRMED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := x.runConfigurationFile(ctx, req, "VERIFY_RESTORED"); err != nil {
		return r, err
	}
	actual, err := ObserveSoftware(ctx, *req.Software, x.softwareRunner)
	if err != nil || actual.State != before.State {
		return r, errors.New("CONFIG_FILE_ROLLBACK_UNCONFIRMED")
	}
	r.Status, r.Phase, r.ErrorCode, r.Software = "FAILED", "OPERATION_RESTORED", "CONFIG_FILE_APPLY_RESTORED", actual
	if err := atomicJSON(filepath.Join(dir, "management-restored.json"), r); err != nil {
		return r, err
	}
	return r, nil
}

func (x *executor) configurationFileListen(req Request) (string, error) {
	var binding softwareBinding
	var original Request
	if readJSON(x.softwareFile(req.Software.ID), &binding) != nil || readJSON(filepath.Join(x.root, "jobs", binding.JobID, "intent.json"), &original) != nil {
		return "", errors.New("CONFIG_FILE_INSTALLATION_UNCONFIRMED")
	}
	installed := frozenManagementInstallation(original)
	if installed == nil {
		return "", errors.New("CONFIG_FILE_INSTALLATION_UNCONFIRMED")
	}
	if req.Software.Kind == "DOCKER" {
		for _, mapping := range serviceinstall.PortMappings(installed.Definition, installed.Config) {
			if mapping.Protocol == "TCP" {
				return strconv.Itoa(mapping.ContainerPort), nil
			}
		}
	} else {
		for _, resource := range binding.Resources {
			if resource.Key == serviceinstall.WebPortKey(req.Management.Definition) && resource.Type == "PORT" && resource.Protocol == "TCP" && !resource.Release {
				return net.JoinHostPort(resource.BindAddress, resource.Value), nil
			}
		}
		return net.JoinHostPort(installed.Config.BindAddress, strconv.Itoa(installed.Config.HostPort)), nil
	}
	return "", errors.New("CONFIG_FILE_LISTENER_UNCONFIRMED")
}
