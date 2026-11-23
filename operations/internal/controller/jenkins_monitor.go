package controller

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

var (
	errJenkinsBinding = errors.New("JENKINS_BINDING_CHANGED")
	errJenkinsJob     = errors.New("JENKINS_JOB_URL_INVALID")
	errJenkinsSecret  = errors.New("JENKINS_TOKEN_UNAVAILABLE")
	jenkinsUsername   = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)
	jenkinsToken      = regexp.MustCompile(`^[\x21-\x7e]{16,256}$`)
	jenkinsPathPart   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_. -]{0,127}$`)
)

type jenkinsMonitor struct {
	SoftwareID, JobURL, EndpointIdentity, Username string
	Version                                        int
	Ciphertext                                     []byte
}

type jenkinsInstanceOption struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	HostName string `json:"hostName"`
	URL      string `json:"url"`
}

type jenkinsEndpoint struct {
	software softwareInstance
	host     target
	base     url.URL
	dial     string
	identity string
}

type jenkinsScope struct {
	project  string
	instance string
}

func (s jenkinsScope) bindingID() string {
	if s.instance == "" {
		return s.project
	}
	return s.project + "/" + s.instance
}

func (h *HTTP) jenkinsProjectRoute(w http.ResponseWriter, r *http.Request, actor *session, scope jenkinsScope, route []string) bool {
	name := strings.Join(route, "/")
	switch r.Method + " " + name {
	case "GET jenkins-monitor":
		data, err := readJenkinsMonitorMetadata(h.store.DB, scope)
		if err != nil {
			h.opsError(w, r, err)
		} else {
			h.success(w, r, data)
		}
	case "POST jenkins-monitor":
		h.saveJenkinsMonitor(w, r, actor, scope)
	case "POST jenkins-build":
		if scope.instance == "" {
			return false
		}
		h.triggerJenkinsQA(w, r, actor, scope)
	case "GET jenkins-build":
		h.readJenkinsBuild(w, r, scope, false)
	case "GET jenkins-build/log":
		h.readJenkinsBuild(w, r, scope, true)
	default:
		return false
	}
	return true
}

func readJenkinsMonitor(q applicationReader, scope jenkinsScope) (string, jenkinsMonitor, error) {
	var job string
	var saved jenkinsMonitor
	if scope.instance != "" {
		err := q.QueryRow(`SELECT coalesce(m.job_url,''),coalesce(m.software_id,''),coalesce(m.job_url,''),coalesce(m.endpoint_identity,''),
            coalesce(m.username,''),coalesce(m.version,0),m.token_ciphertext FROM application_deployments d
            LEFT JOIN application_qa_monitors m ON m.deployment_id=d.id WHERE d.project_id=? AND d.id=?`, scope.project, scope.instance).
			Scan(&job, &saved.SoftwareID, &saved.JobURL, &saved.EndpointIdentity, &saved.Username, &saved.Version, &saved.Ciphertext)
		return job, saved, err
	}
	err := q.QueryRow(`SELECT p.jenkins_url,coalesce(m.software_id,''),coalesce(m.job_url,''),coalesce(m.endpoint_identity,''),
		coalesce(m.username,''),coalesce(m.version,0),m.token_ciphertext
		FROM application_projects p LEFT JOIN application_jenkins_monitors m ON m.project_id=p.id WHERE p.id=?`, scope.project).
		Scan(&job, &saved.SoftwareID, &saved.JobURL, &saved.EndpointIdentity, &saved.Username, &saved.Version, &saved.Ciphertext)
	return job, saved, err
}

func (m jenkinsMonitor) secretID(project string) string {
	return "jenkins-monitor:" + deploy.Digest([]string{project, m.SoftwareID, m.JobURL, m.EndpointIdentity, m.Username})
}

func (s *Store) verifyJenkinsMonitors(instances bool) error {
	query := `SELECT project_id,software_id,job_url,endpoint_identity,username,version,token_ciphertext FROM application_jenkins_monitors`
	if instances {
		query = `SELECT d.project_id||'/'||d.id,m.software_id,m.job_url,m.endpoint_identity,m.username,m.version,m.token_ciphertext
                 FROM application_qa_monitors m JOIN application_deployments d ON d.id=m.deployment_id`
	}
	rows, err := s.DB.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var project string
		var saved jenkinsMonitor
		if err = rows.Scan(&project, &saved.SoftwareID, &saved.JobURL, &saved.EndpointIdentity, &saved.Username, &saved.Version, &saved.Ciphertext); err != nil {
			return err
		}
		token, err := unseal(s.Key, saved.secretID(project), saved.Version, saved.Ciphertext)
		ok := err == nil && jenkinsToken.Match(token)
		clear(token)
		if !ok {
			return errors.New("KEY_MISMATCH_OR_JENKINS_TOKEN_DAMAGED")
		}
	}
	return rows.Err()
}

func readJenkinsEndpoint(q applicationReader, softwareID string) (jenkinsEndpoint, error) {
	var endpoint jenkinsEndpoint
	s, err := scanSoftware(q.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", softwareID))
	if err != nil {
		return endpoint, err
	}
	i, evidence := s.Installation, s.InstallationEvidence
	if !installationBound(s) || s.InstallationStatus != "READY" || i == nil || evidence == nil ||
		i.SoftwareID != s.ID || i.BindingRevision != s.BindingRevision || evidence.IdentityHash != s.IdentityHash ||
		installationKind(*evidence) != s.RuntimeKind || installationLocator(*evidence) != s.Locator ||
		!deploy.JenkinsUnlockRecipe(i.Definition) || evidence.ValidateFor(*i) != nil {
		return endpoint, errJenkinsBinding
	}
	t, err := scanHost(q.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", s.HostID))
	if err != nil {
		return endpoint, err
	}
	return buildJenkinsEndpoint(s, t)
}

func buildJenkinsEndpoint(s softwareInstance, t target) (jenkinsEndpoint, error) {
	var endpoint jenkinsEndpoint
	i := s.Installation
	if i == nil || i.Definition.Web == nil || i.Definition.Web.URLParameter != "" ||
		i.Config.HostPort < 1 || i.Config.HostPort > 65535 || (t.Kind != "LOCAL" && t.Kind != "SSH") {
		return endpoint, errJenkinsBinding
	}
	web := i.Definition.Web
	if (web.Scheme != "http" && web.Scheme != "https") || (web.PortKey != "" && web.PortKey != "main") {
		return endpoint, errJenkinsBinding
	}
	bind, err := netip.ParseAddr(i.Config.BindAddress)
	if err != nil || !bind.Is4() || bind.IsMulticast() || bind.IsLinkLocalUnicast() {
		return endpoint, errJenkinsBinding
	}
	if t.Kind == "LOCAL" && !bind.IsUnspecified() && !bind.IsLoopback() {
		return endpoint, errJenkinsBinding
	}
	dial := bind.String()
	if bind.IsUnspecified() {
		dial = "127.0.0.1"
	}
	basePath := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(web.Path, "/"), "/login"), "/")
	if !validJenkinsContext(basePath) {
		return endpoint, errJenkinsBinding
	}
	address := strings.Trim(t.Address, "[]")
	port := strconv.Itoa(i.Config.HostPort)
	base := url.URL{Scheme: web.Scheme, Host: net.JoinHostPort(address, port), Path: basePath}
	location := base.String()
	if normalizeApplicationJenkinsURL(&location) != nil || t.Kind == "SSH" && t.Fingerprint == "" {
		return endpoint, errJenkinsBinding
	}
	identity := deploy.Digest([]string{s.ID, s.IdentityHash, strconv.FormatInt(s.BindingRevision, 10), t.ID, t.Kind, t.Fingerprint, location, dial})
	return jenkinsEndpoint{software: s, host: t, base: base, dial: net.JoinHostPort(dial, port), identity: identity}, nil
}

func validJenkinsContext(value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "/") || len(value) > 200 {
		return false
	}
	for _, part := range strings.Split(value[1:], "/") {
		if !jenkinsPathPart.MatchString(part) || strings.Contains(part, " ") {
			return false
		}
	}
	return true
}

func jenkinsJobURL(endpoint jenkinsEndpoint, value string) (*url.URL, error) {
	if normalizeApplicationJenkinsURL(&value) != nil || value == "" {
		return nil, errJenkinsJob
	}
	job, err := url.Parse(value)
	if err != nil || job.Scheme != endpoint.base.Scheme || !sameJenkinsHost(job.Hostname(), endpoint.base.Hostname()) || jenkinsURLPort(job) != endpoint.base.Port() {
		return nil, errJenkinsJob
	}
	if !strings.HasPrefix(job.Path, endpoint.base.Path+"/job/") {
		return nil, errJenkinsJob
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(job.Path, endpoint.base.Path+"/"), "/"), "/")
	if len(parts)%2 != 0 || len(parts) > 12 {
		return nil, errJenkinsJob
	}
	for i := 0; i < len(parts); i += 2 {
		if parts[i] != "job" || !validJenkinsJobName(parts[i+1]) {
			return nil, errJenkinsJob
		}
	}
	canonical := endpoint.base
	canonical.Path += "/" + strings.Join(parts, "/") + "/"
	// Reject ambiguous escapes and repeated separators before building API paths.
	if strings.TrimSuffix(job.EscapedPath(), "/") != strings.TrimSuffix(canonical.EscapedPath(), "/") {
		return nil, errJenkinsJob
	}
	return &canonical, nil
}

func validJenkinsJobName(name string) bool {
	return len(name) > 0 && len(name) <= 128 && utf8.ValidString(name) && name != "." && name != ".." &&
		name == strings.TrimSpace(name) && !strings.ContainsAny(name, `/%\?#`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func sameJenkinsHost(a, b string) bool {
	if ai, err := netip.ParseAddr(a); err == nil {
		bi, err := netip.ParseAddr(b)
		return err == nil && ai == bi
	}
	return strings.EqualFold(a, b)
}

func jenkinsURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func jenkinsInstanceOptions(q applicationReader) ([]jenkinsInstanceOption, error) {
	rows, err := q.Query(`SELECT id FROM software_instances WHERE management_mode='MANAGED' AND installation_status='READY'
		AND json_extract(installation_json,'$.definition.catalogKey')='jenkins' ORDER BY name,id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	options := make([]jenkinsInstanceOption, 0, len(ids))
	for _, id := range ids {
		endpoint, err := readJenkinsEndpoint(q, id)
		if errors.Is(err, errJenkinsBinding) || errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		options = append(options, jenkinsInstanceOption{ID: id, Name: endpoint.software.Name, HostName: endpoint.host.Name, URL: endpoint.base.String() + "/"})
	}
	return options, nil
}

func readJenkinsMonitorMetadata(q applicationReader, scope jenkinsScope) (map[string]any, error) {
	job, saved, err := readJenkinsMonitor(q, scope)
	if err != nil {
		return nil, err
	}
	options, err := jenkinsInstanceOptions(q)
	if err != nil {
		return nil, err
	}
	return map[string]any{"configured": saved.Version > 0 && saved.JobURL == job, "hasToken": saved.Version > 0, "jobUrl": job,
		"version": strconv.Itoa(saved.Version), "softwareId": saved.SoftwareID, "username": saved.Username, "instances": options}, nil
}

func (h *HTTP) saveJenkinsMonitor(w http.ResponseWriter, r *http.Request, actor *session, scope jenkinsScope) {
	if !h.sensitiveResponseAllowed(r) {
		h.fail(w, r, 403, "JENKINS_TLS_REQUIRED", "请通过 HTTPS 或本机回环地址配置 Jenkins 查询凭据")
		return
	}
	var in struct {
		ExpectedVersion string `json:"expectedVersion"`
		SoftwareID      string `json:"softwareId"`
		Username        string `json:"username"`
		Token           string `json:"token"`
		JobURL          string `json:"jobUrl"`
	}
	if h.decodeSensitive(w, r, actor, &in) != nil {
		h.rejectSecureRequest(w, r)
		return
	}
	expected, err := strconv.Atoi(in.ExpectedVersion)
	in.Username = strings.TrimSpace(in.Username)
	if err != nil || expected < 0 || strconv.Itoa(expected) != in.ExpectedVersion || !deploy.ID.MatchString(in.SoftwareID) ||
		!jenkinsUsername.MatchString(in.Username) || in.Token != "" && !jenkinsToken.MatchString(in.Token) {
		h.fail(w, r, 400, "INVALID_INPUT", "请选择 Jenkins 实例，并填写有效的查询账号和 API Token")
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer tx.Rollback()
	job, old, err := readJenkinsMonitor(tx, scope)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if expected != old.Version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "Jenkins 查询配置已更新，请刷新后重试")
		return
	}
	endpoint, err := readJenkinsEndpoint(tx, in.SoftwareID)
	if err != nil {
		h.jenkinsError(w, r, err)
		return
	}
	if scope.instance != "" {
		job = strings.TrimSpace(in.JobURL)
	}
	canonical, err := jenkinsJobURL(endpoint, job)
	if err != nil {
		h.jenkinsError(w, r, err)
		return
	}
	if scope.instance != "" {
		job = canonical.String()
	}
	project := scope.bindingID()
	saved := jenkinsMonitor{SoftwareID: in.SoftwareID, JobURL: job, EndpointIdentity: endpoint.identity, Username: in.Username, Version: old.Version + 1}
	token := []byte(in.Token)
	if len(token) == 0 {
		if old.Version == 0 || old.secretID(project) != saved.secretID(project) {
			h.fail(w, r, 400, "JENKINS_TOKEN_REQUIRED", "首次配置或变更作业、实例及账号后，请重新填写 API Token")
			return
		}
		token, err = unseal(h.store.Key, old.secretID(project), old.Version, old.Ciphertext)
		if err != nil {
			h.jenkinsError(w, r, errJenkinsSecret)
			return
		}
	}
	defer clear(token)
	saved.Ciphertext, err = seal(h.store.Key, saved.secretID(project), saved.Version, token)
	if err == nil && scope.instance != "" {
		var used bool
		err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM application_qa_monitors WHERE software_id=? AND job_url=? AND deployment_id<>?)", saved.SoftwareID, job, scope.instance).Scan(&used)
		if err == nil && used {
			h.fail(w, r, 409, "JENKINS_JOB_IN_USE", "此 QA 作业已绑定到其他模块环境，请选择独立作业")
			return
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO application_qa_monitors(deployment_id,software_id,job_url,endpoint_identity,username,version,token_ciphertext)
                VALUES(?,?,?,?,?,?,?) ON CONFLICT(deployment_id) DO UPDATE SET software_id=excluded.software_id,job_url=excluded.job_url,
                endpoint_identity=excluded.endpoint_identity,username=excluded.username,version=excluded.version,token_ciphertext=excluded.token_ciphertext`,
				scope.instance, saved.SoftwareID, job, saved.EndpointIdentity, saved.Username, saved.Version, saved.Ciphertext)
		}
	} else if err == nil {
		_, err = tx.Exec(`INSERT INTO application_jenkins_monitors(project_id,software_id,job_url,endpoint_identity,username,version,token_ciphertext)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(project_id) DO UPDATE SET software_id=excluded.software_id, job_url=excluded.job_url,
			endpoint_identity=excluded.endpoint_identity, username=excluded.username, version=excluded.version, token_ciphertext=excluded.token_ciphertext`,
			project, saved.SoftwareID, job, saved.EndpointIdentity, saved.Username, saved.Version, saved.Ciphertext)
	}
	var data map[string]any
	if err == nil {
		data, err = readJenkinsMonitorMetadata(tx, scope)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, "JENKINS_MONITOR_CONFIGURED", project, "SUCCESS", r.Context().Value(traceKey{}).(string))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.success(w, r, data)
}
