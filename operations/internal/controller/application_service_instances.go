package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/projectconfig"
)

type applicationServiceInstanceInput struct {
	Version           string                       `json:"version"`
	ID                string                       `json:"id"`
	ModuleID          string                       `json:"moduleId"`
	Environment       string                       `json:"environment"`
	HostID            string                       `json:"hostId"`
	RecordedVersion   string                       `json:"recordedVersion"`
	RuntimeSoftwareID *string                      `json:"runtimeSoftwareId,omitempty"`
	ManagedInstanceID *string                      `json:"managedInstanceId,omitempty"`
	DeployMode        *string                      `json:"deployMode,omitempty"`
	Profile           *string                      `json:"profile,omitempty"`
	BindAddress       *string                      `json:"bindAddress,omitempty"`
	ServicePort       *int                         `json:"servicePort,omitempty"`
	MemoryMiB         *int                         `json:"memoryMiB,omitempty"`
	StartCommand      *string                      `json:"startCommand,omitempty"`
	HealthPath        *string                      `json:"healthPath,omitempty"`
	Ports             *[]applicationPort           `json:"ports,omitempty"`
	Variables         *[]applicationVariable       `json:"variables,omitempty"`
	Nginx             *applicationNginxDeploy      `json:"nginx,omitempty"`
	DependencyIDs     *[]string                    `json:"dependencyIds,omitempty"`
	ConfigDir         *string                      `json:"configDir,omitempty"`
	ConfigFiles       *[]applicationConfigFileMeta `json:"configFiles,omitempty"`
	ConfigDrafts      *[]deploy.ProjectConfigFile  `json:"configDrafts,omitempty"`
}

var (
	errApplicationServiceInstanceInUse           = errors.New("service instance has a runtime software reference")
	errApplicationServiceInstanceDuplicate       = errors.New("environment already has this module")
	errApplicationAlgorithmHostBindingDuplicated = errors.New("algorithm module already has a binding for this host")
)

func normalizeApplicationRecordedVersion(version *string) error {
	*version = strings.TrimSpace(*version)
	if utf8.RuneCountInString(*version) > 100 || strings.ContainsAny(*version, "\r\n\t\x00") || projectconfig.ContainsSecret(*version) {
		return applicationInvalid("登记版本须为单行文本，最多100个字符，请勿填写凭据")
	}
	return nil
}

func (h *HTTP) changeApplicationServiceInstance(w http.ResponseWriter, r *http.Request, s *session, projectID, deleteID string) {
	var in applicationServiceInstanceInput
	if deleteID == "" {
		if decode(w, r, &in) != nil || in.Version == "" {
			h.fail(w, r, 400, "INVALID_INPUT", "请填写实例登记信息并携带当前项目版本")
			return
		}
		in.Environment = strings.TrimSpace(in.Environment)
		if !validName(in.Environment) || !deploy.ID.MatchString(in.ModuleID) || in.ID != "" && !deploy.ID.MatchString(in.ID) || in.HostID != "" && !deploy.ID.MatchString(in.HostID) || in.ID == "" && in.HostID == "" {
			h.fail(w, r, 400, "APPLICATION_CONFIG_INVALID", "请核对环境名称和所属模块，新登记须选择主机")
			return
		}
		if in.RuntimeSoftwareID != nil && *in.RuntimeSoftwareID != "" && !deploy.ID.MatchString(*in.RuntimeSoftwareID) {
			h.fail(w, r, 400, "APPLICATION_CONFIG_INVALID", "请选择可读取日志的运行软件")
			return
		}
		if err := normalizeApplicationRecordedVersion(&in.RecordedVersion); err != nil {
			h.applicationSaveError(w, r, err)
			return
		}
	} else {
		var request struct {
			Version string `json:"version"`
		}
		if decode(w, r, &request) != nil || request.Version == "" {
			h.fail(w, r, 400, "INVALID_INPUT", "请携带当前项目版本")
			return
		}
		in.ID, in.Version = deleteID, request.Version
	}

	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(s)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	project, err := readApplicationProject(tx, projectID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if project.Version != in.Version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "项目已被修改，请刷新后重试")
		return
	}
	if deleteID == "" && in.RuntimeSoftwareID != nil {
		for _, module := range project.Modules {
			if module.ID == in.ModuleID {
				if err = checkModuleRuntimeRole(tx, module.Name, in.ID, *in.RuntimeSoftwareID); err != nil {
					h.applicationSaveError(w, r, err)
					return
				}
				break
			}
		}
	}
	var result sql.Result
	result, err = tx.Exec("UPDATE application_projects SET version=version+1,updated_at=? WHERE id=? AND version=?", Now(), projectID, in.Version)
	if err == nil {
		var count int64
		count, err = result.RowsAffected()
		if err == nil && count == 0 {
			h.fail(w, r, 409, "VERSION_CONFLICT", "项目已被修改，请刷新后重试")
			return
		}
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}

	action := "APPLICATION_SERVICE_INSTANCE_SAVED"
	if in.ID == "" {
		in.ID = uuid.NewString()
		err = insertApplicationServiceInstance(tx, project, in)
	} else {
		if deleteID != "" {
			action = "APPLICATION_SERVICE_INSTANCE_DELETED"
		}
		err = updateApplicationServiceInstance(tx, project, in, deleteID != "")
	}
	if err == nil {
		project, err = readApplicationProject(tx, projectID)
	}
	if err == nil {
		err = auditTx(tx, s.Username, action, in.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	switch {
	case err == nil:
		h.success(w, r, project)
	case errors.Is(err, errApplicationServiceInstanceInUse):
		h.fail(w, r, 409, "APPLICATION_INSTANCE_IN_USE", "登记关联了运行软件，解除关联后才能删除或更换主机")
	case errors.Is(err, errApplicationServiceInstanceDuplicate):
		h.fail(w, r, 409, "APPLICATION_INSTANCE_DUPLICATED", "同一模块在这个环境已有登记，请编辑现有实例")
	case errors.Is(err, errApplicationAlgorithmHostBindingDuplicated):
		h.fail(w, r, 409, "APPLICATION_INSTANCE_DUPLICATED", "算法模块在同一环境只能在每台服务器登记一次，请核对或补齐已有登记的服务器")
	case errors.Is(err, sql.ErrNoRows):
		h.opsError(w, r, err)
	default:
		h.applicationSaveError(w, r, err)
	}
}

func checkApplicationServiceInstanceHost(tx *sql.Tx, hostID string) error {
	var exists bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM targets WHERE id=?)", hostID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return applicationInvalid("所选主机不存在，请刷新后重试")
	}
	return nil
}

func checkManagedInstance(tx *sql.Tx, id, hostID, environment string) error {
	if id == "" {
		return nil
	}
	var matches bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM instances WHERE id=? AND target_id=?
		AND json_extract(draft_json,'$.environment')=? COLLATE NOCASE)`, id, hostID, environment).Scan(&matches)
	if err == nil && !matches {
		return applicationInvalid("上线目标须属于模块所选服务器和环境")
	}
	return err
}

func checkModuleRuntimeRole(tx *sql.Tx, moduleName, deploymentID, softwareID string) error {
	if softwareID == "" || strings.EqualFold(strings.TrimSpace(moduleName), "jenkins") {
		return nil
	}
	var conflict bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM software_instances s WHERE s.id=? AND
        (s.software_type='JENKINS' OR lower(json_extract(s.installation_json,'$.definition.catalogKey'))='jenkins') AND
        NOT EXISTS(SELECT 1 FROM application_deployments d WHERE d.id=? AND d.runtime_software_id=s.id))`, softwareID, deploymentID).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return applicationInvalid("Jenkins 是构建服务，请为此模块选择实际运行的服务或容器")
	}
	return nil
}

func checkApplicationServiceRuntime(tx *sql.Tx, hostID, softwareID string) error {
	if softwareID == "" {
		return nil
	}
	var eligible bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1
		FROM software_instances s LEFT JOIN jobs j ON j.id=s.last_job_id
		WHERE s.id=? AND s.host_id=? AND s.runtime_kind IN ('SYSTEMD','DOCKER')
		AND s.management_mode<>'RELEASED' AND s.installation_status<>'UNINSTALLED'
		AND s.actual_state<>'REMOVED' AND s.identity_hash<>''
		AND NOT COALESCE(j.kind IN ('SOFTWARE_UNINSTALL','SOFTWARE_UNMANAGE','PLUGIN_REMOVE')
			AND j.status IN ('QUEUED','RUNNING','UNKNOWN'),0))`, softwareID, hostID).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return applicationInvalid("所选运行软件须位于当前主机且已通过身份检查，未卸载或解除管理")
	}
	return nil
}

func applyDeploymentInputFields(in applicationServiceInstanceInput, dst *applicationDeployment) {
	if in.DeployMode != nil {
		dst.DeployMode = *in.DeployMode
	}
	if in.Profile != nil {
		dst.Profile = *in.Profile
	}
	if in.BindAddress != nil {
		dst.BindAddress = *in.BindAddress
	}
	if in.ServicePort != nil {
		dst.ServicePort = *in.ServicePort
	}
	if in.MemoryMiB != nil {
		dst.MemoryMiB = *in.MemoryMiB
	}
	if in.StartCommand != nil {
		dst.StartCommand = *in.StartCommand
	}
	if in.HealthPath != nil {
		dst.HealthPath = *in.HealthPath
	}
	if in.Ports != nil {
		dst.Ports = *in.Ports
	}
	if in.Variables != nil {
		dst.Variables = *in.Variables
	}
	if in.Nginx != nil {
		copied := *in.Nginx
		if copied.ProxyRules != nil {
			copied.ProxyRules = append([]applicationNginxProxyRule(nil), copied.ProxyRules...)
		}
		dst.Nginx = &copied
	}
	if in.DependencyIDs != nil {
		dst.DependencyIDs = append([]string(nil), (*in.DependencyIDs)...)
	}
	if in.ConfigDir != nil {
		dst.ConfigDir = *in.ConfigDir
	}
	if in.ConfigFiles != nil {
		dst.ConfigFiles = append([]applicationConfigFileMeta(nil), (*in.ConfigFiles)...)
	}
	if in.ConfigDrafts != nil {
		dst.ConfigDrafts = append([]deploy.ProjectConfigFile(nil), (*in.ConfigDrafts)...)
	}
}

func checkEnvironmentHostPortConflict(project applicationProject, envName, currentID string, candidate applicationDeployment) error {
	if candidate.HostID == "" || candidate.DeployMode == "STATIC" {
		return nil
	}
	used := map[string]bool{}
	collectPorts := func(d applicationDeployment) []string {
		if d.DeployMode == "STATIC" {
			return nil
		}
		var keys []string
		if d.ServicePort > 0 {
			keys = append(keys, fmt.Sprintf("TCP:%d", d.ServicePort))
		}
		for _, p := range d.Ports {
			proto := strings.ToUpper(p.Protocol)
			if proto == "" {
				proto = "TCP"
			}
			if p.HostPort > 0 {
				keys = append(keys, fmt.Sprintf("%s:%d", proto, p.HostPort))
			}
		}
		return keys
	}
	for _, env := range project.Environments {
		if !strings.EqualFold(env.Name, envName) {
			continue
		}
		for _, other := range env.Deployments {
			if other.ID == currentID || other.HostID != candidate.HostID {
				continue
			}
			for _, key := range collectPorts(other) {
				used[key] = true
			}
		}
	}
	for _, key := range collectPorts(candidate) {
		if used[key] {
			return applicationInvalid("所选主机已有相同的端口配置，请更换主机或调整端口")
		}
	}
	return nil
}

func projectModuleIDSet(project applicationProject) map[string]bool {
	set := make(map[string]bool, len(project.Modules))
	for _, m := range project.Modules {
		set[m.ID] = true
	}
	return set
}

func applicationModuleByID(project applicationProject, id string) (applicationModule, bool) {
	for _, module := range project.Modules {
		if module.ID == id {
			return module, true
		}
	}
	return applicationModule{}, false
}

func checkApplicationServiceBinding(project applicationProject, environmentName string, candidate applicationDeployment, multiHost bool) error {
	for _, environment := range project.Environments {
		if strings.EqualFold(environment.Name, environmentName) &&
			applicationDeploymentBindingConflict(environment.Deployments, candidate, multiHost) {
			if multiHost {
				return errApplicationAlgorithmHostBindingDuplicated
			}
			return errApplicationServiceInstanceDuplicate
		}
	}
	return nil
}

func insertApplicationServiceInstance(tx *sql.Tx, project applicationProject, in applicationServiceInstanceInput) error {
	module, moduleExists := applicationModuleByID(project, in.ModuleID)
	if !moduleExists {
		return applicationInvalid("所选模块必须属于当前项目")
	}
	if err := checkApplicationServiceInstanceHost(tx, in.HostID); err != nil {
		return err
	}
	var softwareID string
	if in.RuntimeSoftwareID != nil {
		softwareID = *in.RuntimeSoftwareID
		if err := checkApplicationServiceRuntime(tx, in.HostID, softwareID); err != nil {
			return err
		}
	}
	var environmentID string
	for _, environment := range project.Environments {
		if strings.ToLower(environment.Name) != strings.ToLower(in.Environment) {
			continue
		}
		if len(environment.Deployments) >= 40 {
			return applicationInvalid("每环境最多40个模块配置")
		}
		environmentID = environment.ID
		break
	}
	newDeployment := applicationDeployment{
		ID: in.ID, ModuleID: in.ModuleID, HostID: in.HostID, RecordedVersion: in.RecordedVersion, RuntimeSoftwareID: softwareID,
		Ports: []applicationPort{}, Variables: []applicationVariable{}, Mounts: []applicationMount{},
	}
	if err := checkApplicationServiceBinding(project, in.Environment, newDeployment, module.allowsMultipleHostBindings()); err != nil {
		return err
	}
	if in.ManagedInstanceID != nil {
		newDeployment.ManagedInstanceID = *in.ManagedInstanceID
		if err := checkManagedInstance(tx, newDeployment.ManagedInstanceID, in.HostID, in.Environment); err != nil {
			return err
		}
	}
	applyDeploymentInputFields(in, &newDeployment)
	if err := normalizeApplicationDeployment(&newDeployment, projectModuleIDSet(project), in.Environment, nil); err != nil {
		return err
	}
	if err := module.validateDeploymentMode(newDeployment.DeployMode, ""); err != nil {
		return err
	}
	if err := validateApplicationDeploymentRefs(tx, newDeployment); err != nil {
		return err
	}
	if err := checkEnvironmentHostPortConflict(project, in.Environment, newDeployment.ID, newDeployment); err != nil {
		return err
	}
	if environmentID == "" {
		if len(project.Environments) >= 20 {
			return applicationInvalid("每项目最多20个环境")
		}
		environmentID = uuid.NewString()
		if _, err := tx.Exec(`INSERT INTO application_environments(id,project_id,name,position)
			VALUES(?,?,?,(SELECT COALESCE(MAX(position),-1)+1 FROM application_environments WHERE project_id=?))`, environmentID, project.ID, in.Environment, project.ID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO application_deployments(id,project_id,environment_id,module_id,host_id,runtime_software_id,config_json,position)
		VALUES(?,?,?,?,?,NULLIF(?,''),?,(SELECT COALESCE(MAX(position),-1)+1 FROM application_deployments WHERE environment_id=?))`, in.ID, project.ID, environmentID, in.ModuleID, in.HostID, softwareID, jsonText(newDeployment), environmentID)
	return err
}

func updateApplicationServiceInstance(tx *sql.Tx, project applicationProject, in applicationServiceInstanceInput, remove bool) error {
	var environmentID, environmentName, moduleID, hostID, existingRaw string
	var bound bool
	err := tx.QueryRow(`SELECT d.environment_id,e.name,d.module_id,COALESCE(d.host_id,''),d.config_json,
		(COALESCE(d.runtime_software_id,'')<>'' OR COALESCE(json_extract(d.config_json,'$.runtimeSoftwareId'),'')<>'')
		FROM application_deployments d JOIN application_environments e ON e.id=d.environment_id AND e.project_id=d.project_id
		WHERE d.project_id=? AND d.id=?`, project.ID, in.ID).Scan(&environmentID, &environmentName, &moduleID, &hostID, &existingRaw, &bound)
	if err != nil {
		return err
	}
	if remove {
		if bound {
			return errApplicationServiceInstanceInUse
		}
		_, err := tx.Exec("DELETE FROM application_deployments WHERE project_id=? AND id=?", project.ID, in.ID)
		return err
	}
	if moduleID != in.ModuleID || strings.ToLower(environmentName) != strings.ToLower(in.Environment) {
		return applicationInvalid("实例的所属模块和环境不能修改，请重新登记")
	}
	var merged applicationDeployment
	if err = json.Unmarshal([]byte(existingRaw), &merged); err != nil {
		return err
	}
	merged.ID, merged.ModuleID, merged.HostID = in.ID, moduleID, hostID
	if in.HostID != "" && in.HostID != hostID {
		if bound {
			return errApplicationServiceInstanceInUse
		}
		if err := checkApplicationServiceInstanceHost(tx, in.HostID); err != nil {
			return err
		}
		hostID = in.HostID
		merged.HostID = hostID
	}
	module, _ := applicationModuleByID(project, moduleID)
	if err = checkApplicationServiceBinding(project, environmentName, merged, module.allowsMultipleHostBindings()); err != nil {
		return err
	}
	if in.ManagedInstanceID != nil {
		merged.ManagedInstanceID = *in.ManagedInstanceID
	}
	if err = checkManagedInstance(tx, merged.ManagedInstanceID, hostID, environmentName); err != nil {
		return err
	}
	if in.RuntimeSoftwareID != nil {
		if err = checkApplicationServiceRuntime(tx, hostID, *in.RuntimeSoftwareID); err != nil {
			return err
		}
		merged.RuntimeSoftwareID = *in.RuntimeSoftwareID
	}
	merged.RecordedVersion = in.RecordedVersion
	previousMode := merged.DeployMode
	applyDeploymentInputFields(in, &merged)
	if err = normalizeApplicationDeployment(&merged, projectModuleIDSet(project), environmentName, nil); err != nil {
		return err
	}
	if err = module.validateDeploymentMode(merged.DeployMode, previousMode); err != nil {
		return err
	}
	if err = validateApplicationDeploymentRefs(tx, merged); err != nil {
		return err
	}
	if err = checkEnvironmentHostPortConflict(project, environmentName, in.ID, merged); err != nil {
		return err
	}

	// Update via SQLite json_set/json_remove so unknown future keys in config_json remain intact.
	jsonExpr := "json_set(config_json,'$.recordedVersion',?"
	jsonArgs := []any{merged.RecordedVersion}
	if merged.HostID != "" && in.HostID != "" {
		jsonExpr += ",'$.hostId',?"
		jsonArgs = append(jsonArgs, hostID)
	}
	if in.ManagedInstanceID != nil {
		jsonExpr += ",'$.managedInstanceId',?"
		jsonArgs = append(jsonArgs, merged.ManagedInstanceID)
	}
	if in.RuntimeSoftwareID != nil {
		jsonExpr += ",'$.runtimeSoftwareId',?"
		jsonArgs = append(jsonArgs, merged.RuntimeSoftwareID)
	}
	if in.DeployMode != nil {
		jsonExpr += ",'$.deployMode',?"
		jsonArgs = append(jsonArgs, merged.DeployMode)
	}
	if in.Profile != nil {
		jsonExpr += ",'$.profile',?"
		jsonArgs = append(jsonArgs, merged.Profile)
	}
	if in.BindAddress != nil {
		jsonExpr += ",'$.bindAddress',?"
		jsonArgs = append(jsonArgs, merged.BindAddress)
	}
	if in.ServicePort != nil {
		jsonExpr += ",'$.servicePort',?"
		jsonArgs = append(jsonArgs, merged.ServicePort)
	}
	if in.MemoryMiB != nil {
		jsonExpr += ",'$.memoryMiB',?"
		jsonArgs = append(jsonArgs, merged.MemoryMiB)
	}
	if in.StartCommand != nil {
		jsonExpr += ",'$.startCommand',?"
		jsonArgs = append(jsonArgs, merged.StartCommand)
	}
	if in.HealthPath != nil {
		jsonExpr += ",'$.healthPath',?"
		jsonArgs = append(jsonArgs, merged.HealthPath)
	}
	if in.Ports != nil {
		jsonExpr += ",'$.ports',json(?)"
		jsonArgs = append(jsonArgs, jsonText(merged.Ports))
	}
	if in.Variables != nil {
		jsonExpr += ",'$.variables',json(?)"
		jsonArgs = append(jsonArgs, jsonText(merged.Variables))
	}
	if in.DependencyIDs != nil {
		ids := merged.DependencyIDs
		if ids == nil {
			ids = []string{}
		}
		jsonExpr += ",'$.dependencyIds',json(?)"
		jsonArgs = append(jsonArgs, jsonText(ids))
	}
	if in.ConfigDir != nil {
		jsonExpr += ",'$.configDir',?"
		jsonArgs = append(jsonArgs, merged.ConfigDir)
	}
	if in.ConfigFiles != nil {
		files := merged.ConfigFiles
		if files == nil {
			files = []applicationConfigFileMeta{}
		}
		jsonExpr += ",'$.configFiles',json(?)"
		jsonArgs = append(jsonArgs, jsonText(files))
	}
	if in.ConfigDrafts != nil {
		jsonExpr += ",'$.configDrafts',json(?)"
		jsonArgs = append(jsonArgs, jsonText(*in.ConfigDrafts))
	}
	if in.Nginx != nil && merged.Nginx != nil {
		jsonExpr += ",'$.nginx',json(?)"
		jsonArgs = append(jsonArgs, jsonText(merged.Nginx))
	}
	jsonExpr += ")"
	if in.Nginx != nil && merged.Nginx == nil {
		jsonExpr = "json_remove(" + jsonExpr + ",'$.nginx')"
	}

	var updateCols []string
	var args []any
	if in.HostID != "" {
		updateCols = append(updateCols, "host_id=?")
		args = append(args, hostID)
	}
	if in.RuntimeSoftwareID != nil {
		updateCols = append(updateCols, "runtime_software_id=NULLIF(?,'')")
		args = append(args, merged.RuntimeSoftwareID)
	}
	updateCols = append(updateCols, "config_json="+jsonExpr)
	args = append(args, jsonArgs...)
	args = append(args, project.ID, in.ID)

	_, err = tx.Exec("UPDATE application_deployments SET "+strings.Join(updateCols, ",")+" WHERE project_id=? AND id=?", args...)
	return err
}
