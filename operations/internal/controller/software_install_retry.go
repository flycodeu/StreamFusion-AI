package controller

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func failedInstallServiceResource(resource serviceinstall.ResolvedResource) bool {
	return resource.Type == "PORT" || resource.Type == "UNIT" || resource.Type == "CONTAINER"
}

type installRetryConflictError struct{ message string }

func (e *installRetryConflictError) Error() string { return "MANAGEMENT_RESOURCE_CONFLICT" }

func failedCustomInstall(i serviceinstall.Request) bool {
	return i.Definition.Commands != nil && !serviceinstall.IsBuiltinCommand(i.Definition)
}

func failedDockerServiceInstall(i serviceinstall.Request) bool {
	return !failedCustomInstall(i) && i.Config.Mode() == "DOCKER" && i.Definition.Kind == "SERVICE" && !serviceinstall.IsOfficial(i.Definition)
}

func retryableUnboundFailedInstall(item softwareInstance) bool {
	i := item.Installation
	return i != nil && i.SoftwareID == item.ID && (failedCustomInstall(*i) || failedDockerServiceInstall(*i)) &&
		item.InstallationStatus == "FAILED" && item.ManagementMode == "OBSERVE" && item.InstallationEvidence == nil &&
		item.IdentityHash == "" && item.Locator == "pending:"+item.ID
}

func (s *Store) failedInstallations(hostID string) ([]softwareInstance, error) {
	rows, err := s.DB.Query(`SELECT `+softwareColumns+` FROM software_instances s JOIN targets t ON t.id=s.host_id
		WHERE s.host_id=? AND s.installation_status='FAILED' AND s.management_mode='OBSERVE' AND s.identity_hash=''
		AND EXISTS(SELECT 1 FROM jobs j WHERE j.id=s.last_job_id AND j.software_plan_id=s.install_plan_id
		AND j.kind='SOFTWARE_INSTALL' AND j.status='FAILED' AND j.target_ack_pending=0)`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []softwareInstance{}
	for rows.Next() {
		item, scanErr := scanSoftware(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if retryableUnboundFailedInstall(item) {
			items = append(items, item)
		}
	}
	return items, rows.Err()
}

// Recheck the previous object's identity and ports without running installation
// steps. The previous job and directory are never reused.
func (h *HTTP) confirmedFailedInstallResources(ctx context.Context, host target, wanted []serviceinstall.ResolvedResource) (map[string]string, error) {
	confirmed := map[string]string{}
	items, err := h.store.failedInstallations(host.ID)
	if err != nil {
		return confirmed, err
	}
	for _, item := range items {
		i := item.Installation
		claims := []serviceinstall.ResolvedResource{}
		for _, resource := range installationResources(host.InstallRoot, item.ID, i.DirectoryName, i.Definition, i.Config) {
			if failedInstallServiceResource(resource) {
				claims = append(claims, resource)
			}
		}
		if !resourcesConflict(wanted, claims) {
			continue
		}
		var checked serviceinstall.Preflight
		if err := h.store.callTarget(ctx, host, deploy.Request{Action: "SOFTWARE_INSTALL_CHECK", Installation: i}, "", &checked); err != nil {
			return confirmed, err
		}
		if failedInstallObjectsAbsent(host.InstallRoot, *i, checked) {
			confirmed[item.ID] = item.Version
		} else if failedCustomInstall(*i) && legacyFailedInstallCheck(host.InstallRoot, *i, checked) {
			return confirmed, &installCompatibilityError{message: "需要更新接入组件后核对上次安装，请更新后重试"}
		} else {
			remaining := checked
			remaining.Checks = nil
			for _, check := range checked.Checks {
				if !failedInstallPythonDependency(check) && (check.Key != "DIRECTORY" || check.Code != "SERVICE_DATA_DIRECTORY_EXISTS") {
					remaining.Checks = append(remaining.Checks, check)
				}
			}
			for _, check := range remaining.Checks {
				if check.Status != "PASS" {
					_, message := h.installPreflightRejection(remaining, i.Config)
					return confirmed, &installRetryConflictError{message: message}
				}
			}
			if failedDockerServiceInstall(*i) {
				return confirmed, &installRetryConflictError{message: "无法确认上次安装的容器和端口已空闲，请查看原安装日志并核对目标主机"}
			}
			return confirmed, errors.New("MANAGEMENT_RESOURCE_CONFLICT")
		}
	}
	return confirmed, nil
}

func legacyFailedInstallCheck(base string, i serviceinstall.Request, checked serviceinstall.Preflight) bool {
	if checked.Root != i.InstallRoot(base) {
		return false
	}
	directoryExists := false
	for _, check := range checked.Checks {
		if failedInstallPythonDependency(check) {
			continue
		}
		if check.Status == "PASS" {
			continue
		}
		if check.Status != "FAIL" || check.Key != "DIRECTORY" || check.Code != "SERVICE_DATA_DIRECTORY_EXISTS" {
			return false
		}
		directoryExists = true
	}
	return directoryExists
}

func failedInstallObjectsAbsent(base string, i serviceinstall.Request, checked serviceinstall.Preflight) bool {
	if checked.Root != i.InstallRoot(base) {
		return false
	}
	docker := failedDockerServiceInstall(i)
	mappings := serviceinstall.PortMappings(i.Definition, i.Config)
	if docker && !slices.Equal(checked.PortMappings, mappings) {
		return false
	}
	objectKey, objectCode, portCode := "EXISTING", "COMMAND_OBJECT_ABSENT", "SERVICE_PORT_AVAILABLE"
	if docker {
		objectKey, objectCode, portCode = "CONTAINER", "", ""
	}
	objectAbsent, dockerReady, ports := false, !docker, map[string]int{}
	for _, check := range checked.Checks {
		if !docker && failedInstallPythonDependency(check) {
			continue
		}
		if check.Status != "PASS" {
			if check.Key != "DIRECTORY" || check.Code != "SERVICE_DATA_DIRECTORY_EXISTS" || check.Status != "FAIL" {
				return false
			}
			continue
		}
		// Built-in preflight reports published-port failures separately and
		// emits no published-port entry when that check succeeded.
		if docker && check.Code != "" {
			return false
		}
		switch {
		case check.Key == objectKey && check.Code == objectCode:
			objectAbsent = true
		case check.Key == "DOCKER":
			dockerReady = true
		case strings.HasPrefix(check.Key, "PORT_") && check.Code == portCode:
			ports[check.Key]++
		}
	}
	for _, mapping := range mappings {
		key := "PORT_" + mapping.Key
		if ports[key] == 0 {
			return false
		}
		ports[key]--
	}
	return objectAbsent && dockerReady
}

// Recovery checks the previous attempt's objects, not whether its original
// interpreter can still install that older recipe. No commands are replayed.
func failedInstallPythonDependency(check serviceinstall.Check) bool {
	if check.Status != "FAIL" || check.Key != "PYTHON" && !strings.HasPrefix(check.Key, "PYTHON_") {
		return false
	}
	switch check.Code {
	case "COMMAND_PYTHON_PATH_INVALID", "COMMAND_PYTHON_NOT_FOUND", "COMMAND_PYTHON_NOT_EXECUTABLE", "COMMAND_PYTHON_PATH_UNSAFE", "COMMAND_PYTHON_CHECK_UNAVAILABLE", "COMMAND_PYTHON_VERSION_UNSUPPORTED", "COMMAND_PYTHON_MODULE_MISSING", "COMMAND_PYTHON_LIBRARY_MISSING", "COMMAND_PYTHON_SQLITE_UNSUPPORTED":
		return true
	}
	return false
}
