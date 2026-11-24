package controller

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

//go:embed scripts/jenkins-module-qa.groovy.tmpl
var qaPipelineSource string

var qaPipelineTemplate = template.Must(template.New("jenkins-module").Parse(strings.ReplaceAll(qaPipelineSource, "\r\n", "\n")))
var errJenkinsJobConflict = errors.New("JENKINS_JOB_CONFLICT")

type jenkinsAutomation struct {
	name, script, tokenID string
	tokenVersion          int
}

func groovyLiteral(value string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(value) + "'"
}

// A job identifies one checkout and template. Editing a module or changing
// its environment must never queue a build for a different revision of the job.
func effectiveJenkinsMonitor(q applicationReader, scope jenkinsScope) (string, jenkinsMonitor, string, error) {
	job, saved, err := readJenkinsMonitor(q, scope)
	if err != nil || scope.instance == "" && scope.module == "" {
		return job, saved, "", err
	}
	connScope := jenkinsScope{project: scope.project, connection: true}
	_, conn, err := readJenkinsMonitor(q, connScope)
	if err != nil {
		return "", jenkinsMonitor{}, "", err
	}
	if conn.Version == 0 {
		if saved.Version > 0 {
			return job, saved, "", nil
		}
		return "", jenkinsMonitor{}, "请先在项目维护中选择 Jenkins。", nil
	}
	var kind, toolchain, directory, repository, branch, credential, host, environment, module string
	var skipTests bool
	const moduleQuery = `SELECT coalesce(json_extract(m.config_json,'$.catalog.kind'),''),coalesce(json_extract(m.config_json,'$.catalog.environment'),''),
	    m.directory,coalesce(r.url,''),coalesce(r.branch,''),coalesce(r.jenkins_credential_id,''),coalesce(json_extract(m.config_json,'$.catalog.skipTests'),0)
	    FROM application_modules m LEFT JOIN application_repositories r ON r.id=m.repository_id AND r.project_id=m.project_id WHERE m.project_id=? AND m.id=?`
	module = scope.module
	if scope.instance != "" {
		err = q.QueryRow("SELECT module_id,coalesce(host_id,''),environment_id FROM application_deployments WHERE project_id=? AND id=?", scope.project, scope.instance).Scan(&module, &host, &environment)
		if err != nil {
			return "", jenkinsMonitor{}, "", err
		}
	}
	err = q.QueryRow(moduleQuery, scope.project, module).Scan(&kind, &toolchain, &directory, &repository, &branch, &credential, &skipTests)
	if err != nil {
		return "", jenkinsMonitor{}, "", err
	}
	if kind == "OTHER" && (toolchain == "Python" || strings.HasPrefix(toolchain, "Python ") || strings.HasPrefix(toolchain, "Python、")) {
		kind = "ALGORITHM" // Python projects use the same uv workflow regardless of their catalog category.
	}
	if kind != "BACKEND" && kind != "FRONTEND" && kind != "ALGORITHM" {
		return "", jenkinsMonitor{}, "请在项目维护中选择支持 QA 的模块类型。", nil
	}
	if repository == "" {
		return "", jenkinsMonitor{}, "请在项目维护中填写模块的 Git 仓库。", nil
	}
	if branch == "" {
		branch = "main"
	}
	if directory == "" {
		directory = "."
	}
	if _, err = applicationRepositoryEndpoint(repository); err != nil || !applicationRelative(directory) ||
		(credential != "" && !applicationJenkinsCredentialID.MatchString(credential)) ||
		strings.ContainsFunc(repository+branch+directory, unicode.IsControl) ||
		strings.ContainsAny(branch, `*?[:^~\`) || strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		return "", jenkinsMonitor{}, "请核对项目维护中的仓库、分支和模块相对目录。", nil
	}
	label, ok := resolveJenkinsBuildAgentLabel(nil, conn.BuildAgentLabel)
	if !ok {
		return "", jenkinsMonitor{}, "请在项目维护中修正 Jenkins 构建节点标签。", nil
	}
	var script bytes.Buffer
	credentialArg := ""
	if credential != "" {
		credentialArg = groovyLiteral(credential)
	}
	err = qaPipelineTemplate.Execute(&script, struct {
		Kind, Repository, Branch, Directory, Credential, AgentLabel string
		SkipTests                                                   bool
	}{kind, groovyLiteral(repository), groovyLiteral(branch), groovyLiteral(directory), credentialArg, groovyLiteral(label), skipTests})
	if err != nil {
		return "", jenkinsMonitor{}, "", err
	}
	source := script.String()
	identity := deploy.Digest([]string{scope.project, module, environment, scope.instance, host, source})
	name := "sfops-qa-" + identity[:24]
	endpoint, err := readJenkinsEndpoint(q, conn.SoftwareID)
	if err != nil || endpoint.identity != conn.EndpointIdentity {
		if err == nil {
			err = errJenkinsBinding
		}
		return "", jenkinsMonitor{}, "", err
	}
	jobURL := endpoint.base
	jobURL.Path += "/job/" + name + "/"
	conn.automation = &jenkinsAutomation{name: name, script: source, tokenID: conn.secretID(connScope.bindingID()), tokenVersion: conn.Version}
	conn.JobURL = jobURL.String()
	// Fits both SQLite INTEGER and the exact integer range in JS clients.
	epoch, _ := strconv.ParseUint(deploy.Digest([]string{identity, conn.EndpointIdentity, strconv.Itoa(conn.Version)})[:13], 16, 64)
	conn.Version = int(epoch | 1<<52)
	return conn.JobURL, conn, "", nil
}

func (m jenkinsMonitor) openToken(key []byte, scope jenkinsScope) ([]byte, error) {
	id, version := m.secretID(scope.bindingID()), m.Version
	if m.automation != nil {
		id, version = m.automation.tokenID, m.automation.tokenVersion
	}
	return unseal(key, id, version, m.Ciphertext)
}

type jenkinsWorkflow struct {
	XMLName     xml.Name `xml:"flow-definition"`
	Description string   `xml:"description"`
	Definition  struct {
		Class   string `xml:"class,attr"`
		Script  string `xml:"script"`
		Sandbox bool   `xml:"sandbox"`
	} `xml:"definition"`
}

func expectedJenkinsWorkflow(auto *jenkinsAutomation) jenkinsWorkflow {
	var expected jenkinsWorkflow
	expected.Description = "StreamFusion Ops · " + auto.name
	expected.Definition.Class = "org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition"
	expected.Definition.Script = auto.script
	expected.Definition.Sandbox = true
	return expected
}

func parseJenkinsWorkflow(content []byte, actual *jenkinsWorkflow) error {
	// Jenkins serializes config.xml using XML 1.1. Its workflow fields use
	// the XML 1.0 subset, which is what encoding/xml accepts.
	const declaration = `<?xml version="1.1" encoding="UTF-8"?>`
	return xml.Unmarshal(bytes.TrimPrefix(content, []byte(declaration)), actual)
}

func (c jenkinsBuildClient) inspectWorkflow(ctx context.Context, expected jenkinsWorkflow) error {
	res, err := c.getWithVersionPolicy(ctx, c.jobPath("config.xml", nil), true, true)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	const limit = 256 << 10
	content, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	var actual jenkinsWorkflow
	if err != nil || len(content) > limit || parseJenkinsWorkflow(content, &actual) != nil ||
		actual.Description != expected.Description || actual.Definition != expected.Definition {
		return errJenkinsJobConflict
	}
	return nil
}

// Only reuse a matching sandboxed workflow. Never overwrite another job.
func (c jenkinsBuildClient) ensureWorkflow(ctx context.Context, base url.URL, auto *jenkinsAutomation) error {
	expected := expectedJenkinsWorkflow(auto)
	inspect := func() error { return c.inspectWorkflow(ctx, expected) }
	if err := inspect(); !errors.Is(err, errJenkinsMissing) {
		return err
	}
	content, err := xml.Marshal(expected)
	if err != nil {
		return err
	}
	base.Path += "/createItem"
	base.RawQuery = url.Values{"name": {auto.name}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(content))
	if err != nil {
		return errJenkinsResponse
	}
	req.SetBasicAuth(c.username, string(c.token))
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	res, err := c.client.Do(req)
	if err != nil {
		return errJenkinsConnection
	}
	defer res.Body.Close()
	hdr := res.Header.Get("X-Jenkins")
	if hdr != "" && hdr != c.version {
		return errJenkinsPeer
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if hdr != c.version {
			// Jenkins can omit its version when createItem succeeds. Only
			// accept the result after reading back the exact managed job.
			res.Body.Close()
			if err := inspect(); err == nil {
				return nil
			}
			return errJenkinsPeer
		}
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return errJenkinsAuth
	case http.StatusBadRequest, http.StatusConflict:
		body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<10))
		if jenkinsMissingPipelinePlugin(string(body)) {
			return errJenkinsPluginMissing
		}
		// Jenkins may have received a concurrent identical creation request.
		if err := inspect(); !errors.Is(err, errJenkinsMissing) {
			return err
		}
		return errJenkinsResponse
	default:
		if res.StatusCode >= 400 && res.StatusCode < 600 {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<10))
			if jenkinsMissingPipelinePlugin(string(body)) {
				return errJenkinsPluginMissing
			}
		}
		return errJenkinsResponse
	}
}

func jenkinsMissingPipelinePlugin(body string) bool {
	lower := strings.ToLower(body)
	missingClass := strings.Contains(lower, "cannotresolveclassexception") ||
		strings.Contains(lower, "classnotfoundexception") ||
		strings.Contains(lower, "noclassdeffounderror")
	pipelineClass := strings.Contains(lower, "flow-definition") ||
		strings.Contains(lower, "cpsflowdefinition") ||
		strings.Contains(lower, "workflowjob")
	return missingClass && pipelineClass
}
