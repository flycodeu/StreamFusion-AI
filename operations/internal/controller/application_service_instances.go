package controller

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type applicationServiceInstanceInput struct {
	Version           string  `json:"version"`
	ID                string  `json:"id"`
	ModuleID          string  `json:"moduleId"`
	Environment       string  `json:"environment"`
	HostID            string  `json:"hostId"`
	RecordedVersion   string  `json:"recordedVersion"`
	RuntimeSoftwareID *string `json:"runtimeSoftwareId,omitempty"`
	ManagedInstanceID *string `json:"managedInstanceId,omitempty"`
}

var (
	errApplicationServiceInstanceInUse     = errors.New("service instance has a runtime software reference")
	errApplicationServiceInstanceDuplicate = errors.New("environment already has this module")
)

func normalizeApplicationRecordedVersion(version *string) error {
	*version = strings.TrimSpace(*version)
	if utf8.RuneCountInString(*version) > 100 || strings.ContainsAny(*version, "\r\n\t\x00") || applicationSecretValue(*version) {
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

func insertApplicationServiceInstance(tx *sql.Tx, project applicationProject, in applicationServiceInstanceInput) error {
	var moduleExists bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM application_modules WHERE project_id=? AND id=?)", project.ID, in.ModuleID).Scan(&moduleExists); err != nil {
		return err
	}
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
		for _, deployment := range environment.Deployments {
			if deployment.ModuleID == in.ModuleID {
				return errApplicationServiceInstanceDuplicate
			}
		}
		if len(environment.Deployments) >= 40 {
			return applicationInvalid("每环境最多40个模块配置")
		}
		environmentID = environment.ID
		break
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
	newDeployment := applicationDeployment{
		ID: in.ID, ModuleID: in.ModuleID, HostID: in.HostID, RecordedVersion: in.RecordedVersion, RuntimeSoftwareID: softwareID,
		Ports: []applicationPort{}, Variables: []applicationVariable{}, Mounts: []applicationMount{},
	}
	if in.ManagedInstanceID != nil {
		newDeployment.ManagedInstanceID = *in.ManagedInstanceID
		if err := checkManagedInstance(tx, newDeployment.ManagedInstanceID, in.HostID, in.Environment); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO application_deployments(id,project_id,environment_id,module_id,host_id,runtime_software_id,config_json,position)
		VALUES(?,?,?,?,?,NULLIF(?,''),?,(SELECT COALESCE(MAX(position),-1)+1 FROM application_deployments WHERE environment_id=?))`, in.ID, project.ID, environmentID, in.ModuleID, in.HostID, softwareID, jsonText(newDeployment), environmentID)
	return err
}

func updateApplicationServiceInstance(tx *sql.Tx, project applicationProject, in applicationServiceInstanceInput, remove bool) error {
	var environmentID, environmentName, moduleID, hostID string
	var bound bool
	err := tx.QueryRow(`SELECT d.environment_id,e.name,d.module_id,COALESCE(d.host_id,''),
		(COALESCE(d.runtime_software_id,'')<>'' OR COALESCE(json_extract(d.config_json,'$.runtimeSoftwareId'),'')<>'')
		FROM application_deployments d JOIN application_environments e ON e.id=d.environment_id AND e.project_id=d.project_id
		WHERE d.project_id=? AND d.id=?`, project.ID, in.ID).Scan(&environmentID, &environmentName, &moduleID, &hostID, &bound)
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
	if in.HostID != "" && in.HostID != hostID {
		if bound {
			return errApplicationServiceInstanceInUse
		}
		if err := checkApplicationServiceInstanceHost(tx, in.HostID); err != nil {
			return err
		}
		var portConflict bool
		err := tx.QueryRow(`SELECT EXISTS(SELECT 1
			FROM application_deployments current, json_each(current.config_json,'$.ports') port
			JOIN application_deployments other ON other.id<>current.id AND other.environment_id=current.environment_id AND other.host_id=?
			JOIN json_each(other.config_json,'$.ports') other_port
			WHERE current.environment_id=? AND current.id=?
			AND json_extract(port.value,'$.hostPort')=json_extract(other_port.value,'$.hostPort')
			AND COALESCE(NULLIF(UPPER(json_extract(port.value,'$.protocol')),''),'TCP')=COALESCE(NULLIF(UPPER(json_extract(other_port.value,'$.protocol')),''),'TCP'))`, in.HostID, environmentID, in.ID).Scan(&portConflict)
		if err != nil {
			return err
		}
		if portConflict {
			return applicationInvalid("所选主机已有相同的端口配置，请更换主机")
		}
		hostID = in.HostID
		_, err = tx.Exec("UPDATE application_deployments SET host_id=?,config_json=json_set(config_json,'$.hostId',?) WHERE project_id=? AND id=?", hostID, hostID, project.ID, in.ID)
		if err != nil {
			return err
		}
	}
	var managedID string
	err = tx.QueryRow(`SELECT coalesce(json_extract(config_json,'$.managedInstanceId'),'') FROM application_deployments WHERE id=?`, in.ID).Scan(&managedID)
	if err != nil {
		return err
	}
	if in.ManagedInstanceID != nil {
		managedID = *in.ManagedInstanceID
	}
	if err = checkManagedInstance(tx, managedID, hostID, environmentName); err != nil {
		return err
	}
	if in.ManagedInstanceID != nil {
		if _, err = tx.Exec(`UPDATE application_deployments SET config_json=json_set(config_json,'$.managedInstanceId',?) WHERE id=?`, managedID, in.ID); err != nil {
			return err
		}
	}
	if in.RuntimeSoftwareID != nil {
		softwareID := *in.RuntimeSoftwareID
		if err = checkApplicationServiceRuntime(tx, hostID, softwareID); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE application_deployments SET runtime_software_id=NULLIF(?,''),config_json=json_set(config_json,'$.runtimeSoftwareId',?,'$.recordedVersion',?)
			WHERE project_id=? AND id=?`, softwareID, softwareID, in.RecordedVersion, project.ID, in.ID)
		return err
	}
	_, err = tx.Exec("UPDATE application_deployments SET config_json=json_set(config_json,'$.recordedVersion',?) WHERE project_id=? AND id=?", in.RecordedVersion, project.ID, in.ID)
	return err
}
