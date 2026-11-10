package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path"
	"reflect"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type softwareManagementState struct {
	TemplateID       string                                        `json:"templateId"`
	TemplateRevision int                                           `json:"templateRevision"`
	Definition       serviceinstall.Definition                     `json:"definition"`
	Parameters       map[string]string                             `json:"parameters"`
	CredentialRefs   map[string]serviceinstall.CredentialReference `json:"credentialRefs"`
	Resources        []serviceinstall.ResolvedResource             `json:"-"`
	Version          string                                        `json:"stateVersion"`
	LastJobID        string                                        `json:"lastJobId"`
}

type softwareOperationPlan struct {
	ID               string                             `json:"id"`
	SoftwareID       string                             `json:"softwareId"`
	TargetID         string                             `json:"targetId"`
	TemplateID       string                             `json:"templateId"`
	TemplateRevision int                                `json:"templateRevision"`
	StateVersion     string                             `json:"stateVersion"`
	Operation        serviceinstall.ManagementOperation `json:"operation"`
	Management       serviceinstall.ManagementRequest   `json:"management"`
	Checks           []serviceinstall.Check             `json:"checks"`
	Resources        []serviceinstall.ResolvedResource  `json:"resources"`
	ExpiresAt        string                             `json:"expiresAt"`
}

// Private fields are explicit in the durable snapshot, not hidden behind the
// public view's json:"-" tags. Installation snapshots remain unchanged.
type storedSoftwareOperationPlan struct {
	softwareOperationPlan
	SoftwareVersion   string                            `json:"softwareVersion"`
	TargetSnapshot    string                            `json:"targetSnapshot"`
	Binding           deploy.SoftwareRequest            `json:"binding"`
	Kind              string                            `json:"kind"`
	Definition        serviceinstall.Definition         `json:"definition"`
	Config            serviceinstall.Config             `json:"config"`
	PreviousResources []serviceinstall.ResolvedResource `json:"previousResources,omitempty"`
	FailedPortClaims  map[string]string                 `json:"failedPortClaims,omitempty"`
}

func (s *Store) readSoftwareManagement(id string) (softwareManagementState, error) {
	return scanSoftwareManagement(s.DB, id)
}

func scanSoftwareManagement(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (softwareManagementState, error) {
	state := softwareManagementState{Version: "0", Parameters: map[string]string{}, CredentialRefs: map[string]serviceinstall.CredentialReference{}}
	var definition, values, refs, resources string
	err := q.QueryRow("SELECT template_id,template_revision,definition_json,parameters_json,credential_refs_json,resources_json,CAST(version AS TEXT),coalesce(last_job_id,'') FROM software_management_state WHERE software_id=?", id).Scan(&state.TemplateID, &state.TemplateRevision, &definition, &values, &refs, &resources, &state.Version, &state.LastJobID)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err == nil {
		err = deploy.StrictJSON([]byte(definition), &state.Definition)
	}
	if err == nil {
		err = deploy.StrictJSON([]byte(values), &state.Parameters)
	}
	if err == nil {
		err = deploy.StrictJSON([]byte(refs), &state.CredentialRefs)
	}
	if err == nil {
		err = deploy.StrictJSON([]byte(resources), &state.Resources)
	}
	return state, err
}

func managementInstance(i softwareInstance) bool {
	return i.Installation != nil && i.ManagementMode == "MANAGED" && i.InstallationStatus == "READY" && installationBound(i)
}

// A catalog label is not an authorization boundary. An adopted revision must
// belong to the same immutable program and locator as the installed template.
func managementTemplateMatches(i softwareInstance, d serviceinstall.Definition) bool {
	if !managementInstance(i) || d.Management == nil || d.ValidateManagement() != nil || serviceinstall.ValidateDefinition(d) != nil {
		return false
	}
	a, b := i.Installation.Definition, d
	if serviceinstall.IsBuiltinCommand(b) {
		return (a.Commands == nil || serviceinstall.IsBuiltinCommand(a)) && serviceinstall.Digest(serviceinstall.BareDefinition(a)) == serviceinstall.Digest(serviceinstall.BareDefinition(b))
	}
	if a.Commands == nil || b.Commands == nil || a.Commands.Kind != b.Commands.Kind || a.Commands.Locator != b.Commands.Locator || a.Commands.BinaryPath != b.Commands.BinaryPath {
		return false
	}
	for _, v := range []*serviceinstall.Definition{&a, &b} {
		v.Commands, v.Management, v.Parameters = nil, nil, nil
		v.Revision, v.Name, v.Category = "", "", ""
		v.DefaultMemoryMiB = 0
		v.DefaultBindAddress, v.Web = "", nil
	}
	return reflect.DeepEqual(a, b)
}

func (s *Store) managementTemplate(i softwareInstance, templateID string, revision int) (commandTemplate, error) {
	var template commandTemplate
	if i.Installation == nil {
		return template, errors.New("MANAGEMENT_UNAVAILABLE")
	}
	expected := commandTemplateKey(i.Installation.Definition.Key, i.Installation.Definition.Version)
	if templateID != "" && revision > 0 {
		state, err := s.readSoftwareManagement(i.ID)
		if err != nil {
			return template, err
		}
		if state.TemplateID == templateID && state.TemplateRevision == revision && state.Version != "0" && managementTemplateMatches(i, state.Definition) {
			return commandTemplate{ID: state.TemplateID, Revision: state.TemplateRevision, Published: &state.Definition}, nil
		}
	}
	var err error
	if templateID == "" {
		template, err = scanCommandTemplate(s.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE recipe_key=?", expected))
	} else {
		template, err = scanCommandTemplate(s.DB.QueryRow("SELECT "+commandTemplateColumns+" FROM command_templates WHERE recipe_key=? AND id=?", expected, templateID))
	}
	if err != nil || template.Published == nil || revision > 0 && template.Revision != revision || !managementTemplateMatches(i, *template.Published) {
		return commandTemplate{}, errors.New("MANAGEMENT_TEMPLATE_MISMATCH")
	}
	return template, nil
}

func (h *HTTP) softwareManagementRoute(w http.ResponseWriter, r *http.Request, s *session, parts []string) bool {
	if len(parts) != 3 || !deploy.ID.MatchString(parts[1]) {
		return false
	}
	if parts[0] == "software-instances" && parts[2] == "management" && r.Method == "GET" {
		h.getSoftwareManagement(w, r, parts[1])
		return true
	}
	if parts[0] == "software-instances" && parts[2] == "operation-plans" && r.Method == "POST" {
		h.createSoftwareOperationPlan(w, r, s, parts[1])
		return true
	}
	if parts[0] == "software-operation-plans" && parts[2] == "submit" && r.Method == "POST" {
		h.submitSoftwareOperationPlan(w, r, s, parts[1])
		return true
	}
	return false
}

func (h *HTTP) getSoftwareManagement(w http.ResponseWriter, r *http.Request, id string) {
	i, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if !managementInstance(i) {
		h.fail(w, r, 409, "MANAGEMENT_UNAVAILABLE", "请先确认软件安装及管理身份")
		return
	}
	state, err := h.store.readSoftwareManagement(id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	template, templateErr := h.store.managementTemplate(i, "", 0)
	if state.Version == "0" {
		if templateErr != nil {
			h.fail(w, r, 409, "MANAGEMENT_UNAVAILABLE", "此软件尚未发布管理操作，可在软件配置中添加")
			return
		}
		state.TemplateID, state.TemplateRevision, state.Definition = template.ID, template.Revision, *template.Published
		for _, p := range state.Definition.Management.Parameters {
			if value, exists := i.Installation.Config.Parameters[p.Key]; exists && p.Type != "CREDENTIAL" {
				state.Parameters[p.Key] = value
			} else if p.Default != "" {
				state.Parameters[p.Key] = p.Default
			}
			if ref, exists := i.Installation.Config.ParameterCredentials[p.Key]; exists && p.Type == "CREDENTIAL" {
				state.CredentialRefs[p.Key] = ref
			} else if p.Key == "ADMIN_PASSWORD" && p.Type == "CREDENTIAL" && i.Installation.Definition.Key == "mysql" && serviceinstall.IsBuiltinCommand(i.Installation.Definition) && i.Installation.Config.CredentialID != "" {
				state.CredentialRefs[p.Key] = serviceinstall.CredentialReference{ID: i.Installation.Config.CredentialID, Revision: i.Installation.Config.CredentialRevision}
			}
		}
	}
	var available *softwareManagementState
	if templateErr == nil && template.Revision != state.TemplateRevision {
		available = &softwareManagementState{TemplateID: template.ID, TemplateRevision: template.Revision, Definition: *template.Published, Version: state.Version, Parameters: state.Parameters, CredentialRefs: state.CredentialRefs}
	}
	var lastOperation string
	if err := h.store.DB.QueryRow("SELECT id FROM jobs WHERE configuration_plan_id IN (SELECT id FROM software_configuration_plans WHERE software_id=?) AND kind='SOFTWARE_OPERATION' ORDER BY created_at DESC,id DESC LIMIT 1", id).Scan(&lastOperation); err == nil {
		state.LastJobID = lastOperation
	}
	h.success(w, r, map[string]any{"definition": state.Definition, "templateId": state.TemplateID, "templateRevision": state.TemplateRevision, "stateVersion": state.Version, "softwareVersion": i.Version, "parameters": state.Parameters, "credentialRefs": state.CredentialRefs, "operations": state.Definition.Management.Operations, "availableTemplate": available, "lastJobId": state.LastJobID})
}

func (h *HTTP) checkSoftwareOperation(ctx context.Context, t target, i softwareInstance, m serviceinstall.ManagementRequest) (serviceinstall.ManagementPreflight, error) {
	var result serviceinstall.ManagementPreflight
	var capability executorCapabilities
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &capability); err != nil {
		return result, err
	}
	if capability.Owner.ControllerID != h.store.controllerID() || capability.Owner.TargetID != t.ID || capability.Owner.ExecutorVersion != deploy.Version {
		return result, errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if capability.SoftwareManagementVersion < deploy.SoftwareManagementVersion || capability.CommandRecipeVersion < deploy.RequiredCommandRecipeVersion(m.Definition) {
		return result, errors.New("EXECUTOR_UPGRADE_REQUIRED")
	}
	if capability.CommandTrustFingerprint != deploy.CommandPublicKeyFingerprint(h.store.commandPublicKey()) {
		return result, errors.New("COMMAND_TRUST_REQUIRED")
	}
	if err := m.Validate(); err != nil {
		return result, err
	}
	if err := h.store.checkManagementCredentials(m); err != nil {
		return result, err
	}
	err := h.store.callTarget(ctx, t, deploy.Request{Action: "SOFTWARE_OPERATION_CHECK", Software: softwareRequest(i), Management: &m}, "", &result)
	if err != nil {
		return result, err
	}
	if !result.Ready {
		return result, nil
	}
	op, _ := m.Definition.ManagementOperation(m.OperationKey)
	if op.Rebind {
		message := "保存时会短暂停止服务，验证后恢复原运行状态"
		if op.Key == "migrate-data" {
			message = "迁移时停止服务，复制校验后切换目录；原目录保留"
		}
		result.Checks = append(result.Checks, serviceinstall.Check{Key: "IMPACT", Status: "PASS", Message: message})
	}
	wanted, err := m.ResolveResources(i.Locator)
	if err != nil || len(wanted) != len(result.Resources) || !deploy.Hex.MatchString(result.ResourceHash) || result.ResourceHash != deploy.Digest(result.Resources) {
		return result, errors.New("MANAGEMENT_PREFLIGHT_MISMATCH")
	}
	for index := range wanted {
		actual := result.Resources[index]
		actual.IdentityHash = ""
		if !reflect.DeepEqual(wanted[index], actual) {
			return result, errors.New("MANAGEMENT_PREFLIGHT_MISMATCH")
		}
	}
	return result, nil
}

func managementPreflightError(p serviceinstall.ManagementPreflight) string {
	for _, check := range p.Checks {
		if check.Status != "PASS" && validDiagnosticCode(check.Code) {
			return check.Code
		}
	}
	return "MANAGEMENT_CHECK_FAILED"
}

func (h *HTTP) createSoftwareOperationPlan(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in struct {
		ExpectedVersion           string                                        `json:"expectedVersion"`
		ExpectedManagementVersion string                                        `json:"expectedManagementVersion"`
		TemplateID                string                                        `json:"templateId"`
		TemplateRevision          int                                           `json:"templateRevision"`
		OperationKey              string                                        `json:"operationKey"`
		Parameters                map[string]string                             `json:"parameters"`
		CredentialRefs            map[string]serviceinstall.CredentialReference `json:"credentialRefs"`
		OperationParameters       map[string]string                             `json:"operationParameters"`
		OperationCredentialRefs   map[string]serviceinstall.CredentialReference `json:"operationCredentialRefs"`
		Account                   string                                        `json:"account,omitempty"`
	}
	if err := decode(w, r, &in); err != nil || in.TemplateRevision < 1 || !deploy.ID.MatchString(in.TemplateID) {
		h.fail(w, r, 400, "MANAGEMENT_INPUT_INVALID", "请选择已发布的管理方案和操作")
		return
	}
	i, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	state, err := h.store.readSoftwareManagement(id)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	if !managementInstance(i) || i.Version != in.ExpectedVersion || state.Version != in.ExpectedManagementVersion {
		h.fail(w, r, 409, "MANAGEMENT_STATE_CHANGED", "软件或管理配置已变化，请刷新后重新检查")
		return
	}
	template, err := h.store.managementTemplate(i, in.TemplateID, in.TemplateRevision)
	if err != nil {
		h.fail(w, r, 409, "MANAGEMENT_TEMPLATE_MISMATCH", "管理方案已变化或不属于此软件，请重新选择")
		return
	}
	t, err := h.store.readTarget(i.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if t.Maintenance {
		h.fail(w, r, 409, "HOST_MAINTENANCE", "主机维护中，请恢复后操作")
		return
	}
	m := serviceinstall.ManagementRequest{Definition: *template.Published, SoftwareID: i.ID, BindingRevision: i.BindingRevision, Root: softwareManagementRoot(i, t), OperationKey: in.OperationKey, Parameters: in.Parameters, ParameterCredentials: in.CredentialRefs, OperationParameters: in.OperationParameters, OperationCredentials: in.OperationCredentialRefs, Account: in.Account}
	if m.Account == "" {
		m.Account = i.Installation.Config.Account
	}
	m.ParameterCredentials = nonNilCredentialRefs(m.ParameterCredentials)
	m.OperationCredentials = nonNilCredentialRefs(m.OperationCredentials)
	if m.Parameters, err = serviceinstall.ResolveParameters(m.Definition.Management.Parameters, m.Parameters, m.ParameterCredentials); err == nil {
		var op serviceinstall.ManagementOperation
		if op, err = m.Definition.ManagementOperation(m.OperationKey); err == nil {
			if op.Rebind {
				installed := *i.Installation
				m.Installation = &installed
			}
			runtimeMode := "NATIVE"
			if i.RuntimeKind == "DOCKER" || i.RuntimeKind == "TOOL" && i.Installation.Config.Mode() == "DOCKER" {
				runtimeMode = "DOCKER"
			}
			if !op.SupportsRuntime(runtimeMode) {
				err = errors.New("MANAGEMENT_RUNTIME_UNSUPPORTED")
			} else {
				m.OperationParameters, err = serviceinstall.ResolveParameters(op.Parameters, m.OperationParameters, m.OperationCredentials)
			}
		}
	}
	if err == nil {
		err = m.Validate()
	}
	if err != nil {
		h.fail(w, r, 400, "MANAGEMENT_INPUT_INVALID", "请检查操作参数、执行账号和凭据引用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	checked, err := h.checkSoftwareOperation(ctx, t, i, m)
	if err != nil {
		h.fail(w, r, 409, configurationErrorCode(err), "操作检查未完成，请检查主机权限、组件能力和管理凭据")
		return
	}
	if !checked.Ready {
		h.fail(w, r, 409, managementPreflightError(checked), "操作资源、目录权限或软件身份未通过检查")
		return
	}
	m.ResourceHash = checked.ResourceHash
	failedPorts, err := h.store.confirmedFailedManagementPorts(t, checked)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	op, _ := m.Definition.ManagementOperation(m.OperationKey)
	p := storedSoftwareOperationPlan{softwareOperationPlan: softwareOperationPlan{ID: uuid.NewString(), SoftwareID: id, TargetID: i.HostID, TemplateID: template.ID, TemplateRevision: template.Revision, StateVersion: state.Version, Operation: op, Management: m, Checks: checked.Checks, Resources: checked.Resources, ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}, SoftwareVersion: i.Version, TargetSnapshot: pluginTargetSnapshot(t), Binding: *softwareRequest(i), Kind: "OPERATION", Definition: i.Installation.Definition, Config: i.Installation.Config, PreviousResources: state.Resources}
	p.FailedPortClaims = failedPorts
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	if err = verifySoftwareOperationState(tx, p); err != nil {
		h.fail(w, r, 409, configurationErrorCode(err), "检查期间软件、主机或管理配置已变化")
		return
	}
	if err = checkManagementCredentialsTx(tx, m); err != nil {
		h.fail(w, r, 409, "MANAGEMENT_CREDENTIAL_UNAVAILABLE", "操作凭据已不可用，请重新选择")
		return
	}
	if _, err = tx.Exec("UPDATE software_configuration_plans SET status='SUPERSEDED' WHERE actor_id=? AND software_id=? AND status='DRAFT'", actor.AdminID, id); err == nil {
		err = reserveManagementResources(tx, i.HostID, i.ID, "", p.Resources, resourceClaimExemptions{VerifiedPorts: p.FailedPortClaims})
	}
	if err != nil {
		h.fail(w, r, 409, configurationErrorCode(err), "此操作与现有软件或待处理计划的资源冲突")
		return
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM software_configuration_plans WHERE target_id=? AND status='DRAFT' AND expires_at>?", t.ID, Now()).Scan(&count); err != nil || count >= 20 {
		h.fail(w, r, 409, "CONFIGURATION_PLAN_LIMIT", "该主机待提交计划过多，请先处理原计划")
		return
	}
	_, err = tx.Exec("INSERT INTO software_configuration_plans VALUES(?,?,?,?,?,'DRAFT',?,?)", p.ID, actor.AdminID, p.TargetID, p.SoftwareID, jsonText(p), p.ExpiresAt, Now())
	if err == nil {
		for _, ref := range m.CredentialReferences() {
			if _, err = tx.Exec("INSERT OR IGNORE INTO software_configuration_credentials VALUES(?,?,?)", p.ID, ref.ID, ref.Revision); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = auditTx(tx, actor.Username, "SOFTWARE_OPERATION_PLAN_CREATED", id, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, p.softwareOperationPlan)
}

func verifySoftwareOperationState(tx *sql.Tx, p storedSoftwareOperationPlan) error {
	i, err := scanSoftware(tx.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", p.SoftwareID))
	if err != nil {
		return err
	}
	t, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", p.TargetID))
	if err != nil {
		return err
	}
	state, err := scanSoftwareManagement(tx, p.SoftwareID)
	if err != nil {
		return err
	}
	if !managementInstance(i) || i.Version != p.SoftwareVersion || deploy.Digest(softwareRequest(i)) != deploy.Digest(p.Binding) || state.Version != p.StateVersion || t.Maintenance || pluginTargetSnapshot(t) != p.TargetSnapshot {
		return errors.New("MANAGEMENT_STATE_CHANGED")
	}
	if p.Operation.Rebind && (p.Management.Installation == nil || deploy.Digest(p.Management.Installation) != deploy.Digest(i.Installation)) {
		return errors.New("MANAGEMENT_INSTALLATION_CHANGED")
	}
	if state.TemplateID != p.TemplateID || state.TemplateRevision != p.TemplateRevision || deploy.Digest(state.Definition) != deploy.Digest(p.Management.Definition) {
		var published string
		var revision int
		if err = tx.QueryRow("SELECT published_json,revision FROM command_templates WHERE id=?", p.TemplateID).Scan(&published, &revision); err != nil {
			return err
		}
		var definition serviceinstall.Definition
		if deploy.StrictJSON([]byte(published), &definition) != nil || deploy.Digest(definition) != deploy.Digest(p.Management.Definition) || revision != p.TemplateRevision {
			return errors.New("MANAGEMENT_TEMPLATE_MISMATCH")
		}
	}
	var busy int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key IN (?,?)", "target:"+p.TargetID, "software:"+p.SoftwareID).Scan(&busy); err != nil {
		return err
	}
	if busy != 0 {
		return errors.New("OPERATION_LOCKED")
	}
	return nil
}

func (s *Store) checkManagementCredentials(m serviceinstall.ManagementRequest) error {
	values := map[string]string{}
	for _, ref := range m.CredentialReferences() {
		secret, err := s.parameterSecret(ref)
		if err != nil {
			return err
		}
		values[serviceinstall.SecretKey(ref.ID, ref.Revision)] = string(secret)
	}
	return serviceinstall.ValidateCredentialValues(m.CredentialReferences(), values)
}

func (s *Store) parameterSecret(ref serviceinstall.CredentialReference) ([]byte, error) {
	var kind string
	if err := s.DB.QueryRow("SELECT kind FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND revoked_at IS NULL", ref.ID, ref.Revision).Scan(&kind); err != nil {
		return nil, errors.New("MANAGEMENT_CREDENTIAL_UNAVAILABLE")
	}
	return s.secret(ref.ID, ref.Revision, kind)
}

func checkManagementCredentialsTx(tx *sql.Tx, m serviceinstall.ManagementRequest) error {
	for _, ref := range m.CredentialReferences() {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND revoked_at IS NULL", ref.ID, ref.Revision).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("MANAGEMENT_CREDENTIAL_UNAVAILABLE")
		}
	}
	return nil
}

func softwareManagementRoot(i softwareInstance, t target) string {
	return path.Clean(serviceinstall.InstallRoot(t.InstallRoot, i.ID, i.Installation.Config))
}

func validManagementVersion(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == value
}

func decodeSoftwareOperationPlan(raw string) (storedSoftwareOperationPlan, error) {
	var p storedSoftwareOperationPlan
	err := deploy.StrictJSON([]byte(raw), &p)
	if err == nil && (p.Kind != "OPERATION" || !validManagementVersion(p.StateVersion) || p.Management.Validate() != nil || p.Management.SoftwareID != p.SoftwareID || p.Binding.ID != p.SoftwareID || p.Management.BindingRevision != p.Binding.Version) {
		err = errors.New("MANAGEMENT_PLAN_DAMAGED")
	}
	return p, err
}
