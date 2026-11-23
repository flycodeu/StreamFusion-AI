package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const jenkinsLogLimit = 512 << 10

var (
	errJenkinsNotConfigured = errors.New("JENKINS_NOT_CONFIGURED")
	errJenkinsConnection    = errors.New("JENKINS_UNAVAILABLE")
	errJenkinsPeer          = errors.New("JENKINS_PEER_UNVERIFIED")
	errJenkinsAuth          = errors.New("JENKINS_AUTH_REQUIRED")
	errJenkinsMissing       = errors.New("JENKINS_RESOURCE_NOT_FOUND")
	errJenkinsResponse      = errors.New("JENKINS_RESPONSE_INVALID")
	errJenkinsBusy          = errors.New("JENKINS_QUERY_BUSY")
)

type jenkinsStage struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
}

type jenkinsArtifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

type jenkinsBuildStatus struct {
	Configured      bool              `json:"configured"`
	Status          string            `json:"status"`
	QueueReason     string            `json:"queueReason,omitempty"`
	Number          int               `json:"number,omitempty"`
	Building        bool              `json:"building"`
	StartedAt       string            `json:"startedAt,omitempty"`
	DurationMS      int64             `json:"durationMs"`
	BuildURL        string            `json:"buildUrl,omitempty"`
	ConsoleURL      string            `json:"consoleUrl,omitempty"`
	CurrentStage    string            `json:"currentStage,omitempty"`
	StagesAvailable bool              `json:"stagesAvailable"`
	Stages          []jenkinsStage    `json:"stages,omitempty"`
	Artifacts       []jenkinsArtifact `json:"artifacts,omitempty"`
	ObservedAt      string            `json:"observedAt"`
}

type jenkinsBuildLog struct {
	Number     int    `json:"number"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"nextOffset"`
	Text       string `json:"text"`
	More       bool   `json:"more"`
	Truncated  bool   `json:"truncated"`
	ConsoleURL string `json:"consoleUrl"`
}

type jenkinsBuildClient struct {
	client   *http.Client
	job      url.URL
	version  string
	username string
	token    []byte
}

func (h *HTTP) jenkinsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		h.fail(w, r, 404, "NOT_FOUND", "项目或安装的 Jenkins 实例不存在")
	case errors.Is(err, errJenkinsBinding):
		h.fail(w, r, 409, errJenkinsBinding.Error(), "Jenkins 实例绑定已变化或无法核实，请检查安装记录并重新配置查询")
	case errors.Is(err, errJenkinsJob):
		h.fail(w, r, 400, errJenkinsJob.Error(), "请在项目中填写所选 Jenkins 实例的作业首页链接")
	case errors.Is(err, errJenkinsSecret):
		h.fail(w, r, 409, errJenkinsSecret.Error(), "Jenkins 查询凭据不可用，请重新配置 API Token")
	case errors.Is(err, errJenkinsTriggerUnknown):
		h.fail(w, r, 409, errJenkinsTriggerUnknown.Error(), "这次 QA 已受理或结果待确认，请刷新作业队列和构建记录")
	case errors.Is(err, errJenkinsConfigChanged):
		h.fail(w, r, 409, errJenkinsConfigChanged.Error(), "QA 配置已变更，请刷新后再操作")
	case errors.Is(err, errJenkinsArchived):
		h.fail(w, r, 409, errJenkinsArchived.Error(), "此项目已归档")
	case errors.Is(err, errJenkinsNotConfigured):
		h.fail(w, r, 409, errJenkinsNotConfigured.Error(), "请先配置 Jenkins 构建查询")
	case errors.Is(err, errJenkinsBusy):
		h.fail(w, r, 429, errJenkinsBusy.Error(), "Jenkins 查询繁忙，请稍后刷新")
	case errors.Is(err, errJenkinsAuth):
		h.fail(w, r, 502, errJenkinsAuth.Error(), "Jenkins 拒绝访问，请核对 API Token 和此作业的读取或构建权限")
	case errors.Is(err, errJenkinsMissing):
		h.fail(w, r, 404, errJenkinsMissing.Error(), "Jenkins 作业、构建或日志已不存在，请刷新并核对作业链接")
	case errors.Is(err, errJenkinsPeer):
		h.fail(w, r, 502, errJenkinsPeer.Error(), "无法确认 Jenkins 服务身份或版本，请检查配置的实例和端口")
	case errors.Is(err, errJenkinsConnection), errors.Is(err, context.DeadlineExceeded):
		h.fail(w, r, 502, errJenkinsConnection.Error(), "连接 Jenkins 失败，请检查主机接入和 Jenkins 服务")
	case errors.Is(err, errJenkinsResponse):
		h.fail(w, r, 502, errJenkinsResponse.Error(), "Jenkins 构建响应无效，请在 Jenkins 查看运行记录")
	default:
		h.internal(w, r, err)
	}
}

func (s *Store) jenkinsBuildBinding(ctx context.Context, scope jenkinsScope) (jenkinsMonitor, jenkinsEndpoint, *url.URL, error) {
	var monitor jenkinsMonitor
	var endpoint jenkinsEndpoint
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return monitor, endpoint, nil, err
	}
	defer tx.Rollback()
	savedJob, monitor, err := readJenkinsMonitor(tx, scope)
	if err != nil {
		return monitor, endpoint, nil, err
	}
	if monitor.Version == 0 {
		return monitor, endpoint, nil, errJenkinsNotConfigured
	}
	if monitor.JobURL != savedJob {
		return monitor, endpoint, nil, errJenkinsBinding
	}
	endpoint, err = readJenkinsEndpoint(tx, monitor.SoftwareID)
	if err != nil {
		return monitor, endpoint, nil, err
	}
	if endpoint.identity != monitor.EndpointIdentity {
		return monitor, endpoint, nil, errJenkinsBinding
	}
	job, err := jenkinsJobURL(endpoint, savedJob)
	return monitor, endpoint, job, err
}

func (h *HTTP) readJenkinsBuild(w http.ResponseWriter, r *http.Request, scope jenkinsScope, log bool) {
	var number int
	var offset int64
	queueID := 0
	if !log && r.URL.Query().Has("queueId") {
		var e error
		queueID, e = strconv.Atoi(r.URL.Query().Get("queueId"))
		if scope.instance == "" || e != nil || queueID <= 0 || queueID > 1<<31-1 || r.URL.Query().Has("number") {
			h.fail(w, r, 400, "INVALID_INPUT", "队列号无效")
			return
		}
	}
	if log || r.URL.Query().Has("number") {
		var numErr, offsetErr error
		number, numErr = strconv.Atoi(r.URL.Query().Get("number"))
		if raw := r.URL.Query().Get("offset"); raw != "" {
			offset, offsetErr = strconv.ParseInt(raw, 10, 64)
		}
		if numErr != nil || offsetErr != nil || number <= 0 || number > 1<<31-1 || offset < 0 || offset > 1<<40 {
			h.fail(w, r, 400, "INVALID_INPUT", "请输入有效的 Jenkins 构建号和日志位置")
			return
		}
	}
	monitor, endpoint, job, err := h.store.jenkinsBuildBinding(r.Context(), scope)
	if err != nil {
		if errors.Is(err, errJenkinsNotConfigured) && !log {
			h.success(w, r, jenkinsBuildStatus{Status: "NOT_CONFIGURED", ObservedAt: Now()})
			return
		}
		h.jenkinsError(w, r, err)
		return
	}
	select {
	case h.hostCheckSlots <- struct{}{}:
		defer func() { <-h.hostCheckSlots }()
	default:
		h.jenkinsError(w, r, errJenkinsBusy)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	token, err := unseal(h.store.Key, monitor.secretID(scope.bindingID()), monitor.Version, monitor.Ciphertext)
	if err != nil || !jenkinsToken.Match(token) {
		clear(token)
		h.jenkinsError(w, r, errJenkinsSecret)
		return
	}
	defer clear(token)
	client, done, err := h.store.connectJenkinsBuild(ctx, endpoint, *job, monitor.Username, token)
	if err != nil {
		h.jenkinsError(w, r, err)
		return
	}
	defer done()
	if log {
		value, err := client.log(ctx, number, offset)
		if err != nil {
			h.jenkinsError(w, r, err)
		} else {
			h.success(w, r, value)
		}
		return
	}
	var value jenkinsBuildStatus
	switch {
	case queueID > 0:
		var known bool
		err = h.store.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM application_qa_requests WHERE deployment_id=? AND monitor_version=? AND queue_id=?)", scope.instance, monitor.Version, queueID).Scan(&known)
		if err == nil && !known {
			err = errJenkinsMissing
		}
		if err == nil {
			value, err = client.queueStatus(ctx, endpoint.base, queueID)
		}
	case number > 0:
		value, err = client.readRun(ctx, number, jenkinsBuildStatus{Configured: true, ObservedAt: Now()}, false)
	default:
		value, err = client.status(ctx)
	}
	if err != nil {
		h.jenkinsError(w, r, err)
	} else {
		h.success(w, r, value)
	}
}

func (s *Store) connectJenkinsBuild(ctx context.Context, endpoint jenkinsEndpoint, job url.URL, user string, token []byte) (jenkinsBuildClient, func(), error) {
	var result jenkinsBuildClient
	dial := (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	done := func() {}
	if endpoint.host.Kind == "SSH" {
		client, stop, err := s.connectSSH(ctx, endpoint.host)
		if err != nil {
			return result, nil, errJenkinsConnection
		}
		dial, done = client.DialContext, stop
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, "tcp", endpoint.dial)
		},
		ResponseHeaderTimeout:  6 * time.Second,
		TLSHandshakeTimeout:    5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
	}
	closeConnection := func() { transport.CloseIdleConnections(); done() }
	result = jenkinsBuildClient{client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}, job: job, version: endpoint.software.Installation.Definition.Version, username: user, token: token}
	login := endpoint.base
	login.Path += "/login"
	resp, err := result.get(ctx, login, false)
	if err != nil {
		closeConnection()
		return result, nil, err
	}
	resp.Body.Close()
	// Some already-configured Jenkins authentication realms redirect /login.
	if resp.StatusCode >= 500 {
		closeConnection()
		return result, nil, errJenkinsConnection
	}
	return result, closeConnection, nil
}

func (c jenkinsBuildClient) jobPath(suffix string, query url.Values) url.URL {
	u := c.job
	u.Path += suffix
	u.RawQuery = query.Encode()
	return u
}

func (c jenkinsBuildClient) get(ctx context.Context, u url.URL, authenticate bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errJenkinsResponse
	}
	if authenticate {
		req.SetBasicAuth(c.username, string(c.token))
	}
	res, err := c.client.Do(req)
	if err != nil {
		return nil, errJenkinsConnection
	}
	if res.Header.Get("X-Jenkins") != c.version {
		res.Body.Close()
		return nil, errJenkinsPeer
	}
	if !authenticate || res.StatusCode == http.StatusOK {
		return res, nil
	}
	res.Body.Close()
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errJenkinsAuth
	case http.StatusNotFound:
		return nil, errJenkinsMissing
	default:
		return nil, errJenkinsResponse
	}
}

func (c jenkinsBuildClient) readJSON(ctx context.Context, suffix, tree string, v any) error {
	query := url.Values{}
	if tree != "" {
		query.Set("tree", tree)
	}
	return c.readJSONAt(ctx, c.jobPath(suffix, query), v)
}

func (c jenkinsBuildClient) readJSONAt(ctx context.Context, location url.URL, v any) error {
	res, err := c.get(ctx, location, true)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	const maxJSON = 512 << 10
	body, err := io.ReadAll(io.LimitReader(res.Body, maxJSON+1))
	if err != nil || len(body) > maxJSON || json.Unmarshal(body, v) != nil {
		return errJenkinsResponse
	}
	return nil
}

func normalizedJenkinsResult(result string, running bool) string {
	if running {
		return "RUNNING"
	}
	switch result {
	case "SUCCESS", "FAILURE", "ABORTED", "UNSTABLE", "NOT_BUILT":
		return result
	default:
		return "UNKNOWN"
	}
}

func (c jenkinsBuildClient) status(ctx context.Context) (jenkinsBuildStatus, error) {
	out := jenkinsBuildStatus{Configured: true, ObservedAt: Now(), Status: "NEVER_BUILT"}
	var job struct {
		InQueue   bool `json:"inQueue"`
		QueueItem *struct {
			Why string `json:"why"`
		} `json:"queueItem"`
		LastBuild *struct {
			Number int `json:"number"`
		} `json:"lastBuild"`
	}
	if err := c.readJSON(ctx, "api/json", "inQueue,queueItem[why],lastBuild[number]", &job); err != nil {
		return out, err
	}
	if job.InQueue {
		out.Status = "QUEUED"
		if job.QueueItem != nil {
			out.QueueReason = c.label(job.QueueItem.Why)
		}
	}
	if job.LastBuild == nil {
		return out, nil
	}
	return c.readRun(ctx, job.LastBuild.Number, out, job.InQueue)
}

func (c jenkinsBuildClient) readRun(ctx context.Context, number int, out jenkinsBuildStatus, queued bool) (jenkinsBuildStatus, error) {
	if number <= 0 || number > 1<<31-1 {
		return out, errJenkinsResponse
	}
	prefix := strconv.Itoa(number) + "/"
	var run struct {
		Number    int    `json:"number"`
		Building  bool   `json:"building"`
		Result    string `json:"result"`
		Timestamp int64  `json:"timestamp"`
		Duration  int64  `json:"duration"`
		Artifacts []struct {
			FileName     string `json:"fileName"`
			RelativePath string `json:"relativePath"`
		} `json:"artifacts"`
	}
	if err := c.readJSON(ctx, prefix+"api/json", "number,building,result,timestamp,duration,artifacts[fileName,relativePath]", &run); err != nil {
		return out, err
	}
	if run.Number != number || run.Timestamp < 0 || run.Timestamp > 253402300799999 || run.Duration < 0 {
		return out, errJenkinsResponse
	}
	if run.Building || !queued {
		out.Status = normalizedJenkinsResult(run.Result, run.Building)
	}
	build, console := c.jobPath(prefix, nil), c.jobPath(prefix+"console", nil)
	out.Number, out.Building, out.DurationMS, out.BuildURL, out.ConsoleURL = number, run.Building, run.Duration, build.String(), console.String()
	if run.Timestamp > 0 {
		out.StartedAt = time.UnixMilli(run.Timestamp).UTC().Format(time.RFC3339)
	}
	for _, artifact := range run.Artifacts {
		if len(out.Artifacts) == 100 {
			break
		}
		p := artifact.RelativePath
		if safeJenkinsArtifactPath(p) && !strings.Contains(p, string(c.token)) {
			u := c.jobPath(prefix+"artifact/"+p, nil)
			name := c.label(artifact.FileName)
			if name == "" {
				name = path.Base(p)
			}
			out.Artifacts = append(out.Artifacts, jenkinsArtifact{Name: name, Path: p, URL: u.String()})
		}
	}
	c.readStages(ctx, &out, prefix)
	out.ObservedAt = Now()
	return out, nil
}

func safeJenkinsArtifactPath(p string) bool {
	return p != "" && p != "." && p != ".." && len(p) <= 512 && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "../") &&
		path.Clean(p) == p && !strings.ContainsAny(p, `%\?#`) && !strings.ContainsFunc(p, unicode.IsControl)
}

func (c jenkinsBuildClient) label(s string) string {
	s = strings.ReplaceAll(s, string(c.token), "[已屏蔽]")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if runes := []rune(s); len(runes) > 240 {
		s = string(runes[:240])
	}
	return strings.TrimSpace(s)
}

func (c jenkinsBuildClient) readStages(ctx context.Context, out *jenkinsBuildStatus, prefix string) {
	var phases struct {
		Stages []struct {
			Name     string `json:"name"`
			Status   string `json:"status"`
			Duration int64  `json:"durationMillis"`
		} `json:"stages"`
	}
	if err := c.readJSON(ctx, prefix+"wfapi/describe", "", &phases); err == nil && phases.Stages != nil {
		out.StagesAvailable = true
		for _, phase := range phases.Stages[:min(len(phases.Stages), 80)] {
			stage := jenkinsStage{Name: c.label(phase.Name), Status: normalizedJenkinsStage(phase.Status), DurationMS: max(phase.Duration, 0)}
			if stage.Name == "" {
				continue
			}
			out.Stages = append(out.Stages, stage)
			if out.Building && (stage.Status == "IN_PROGRESS" || stage.Status == "PAUSED_PENDING_INPUT") {
				out.CurrentStage = stage.Name
			}
		}
		return
	}
	// The first-party pipeline prints stage markers independently of the optional Stage View plugin.
	logs, err := c.log(ctx, out.Number, 0)
	if err != nil {
		return
	}
	for _, line := range strings.Split(logs.Text, "\n") {
		if value, ok := strings.CutPrefix(line, "SFOPS_STAGE: "); ok && len(out.Stages) < 80 {
			if name := c.label(value); name != "" {
				out.Stages = append(out.Stages, jenkinsStage{Name: name, Status: "UNKNOWN"})
			}
		}
	}
	out.StagesAvailable = len(out.Stages) != 0
	if out.Building && !logs.Truncated && len(out.Stages) > 0 {
		last := &out.Stages[len(out.Stages)-1]
		last.Status, out.CurrentStage = "IN_PROGRESS", last.Name
	}
}

func normalizedJenkinsStage(value string) string {
	switch value {
	case "IN_PROGRESS", "PAUSED_PENDING_INPUT", "SUCCESS", "FAILURE", "FAILED", "ABORTED", "UNSTABLE", "NOT_EXECUTED", "NOT_BUILT", "QUEUED":
		return value
	default:
		return "UNKNOWN"
	}
}

func (c jenkinsBuildClient) log(ctx context.Context, number int, offset int64) (jenkinsBuildLog, error) {
	prefix := strconv.Itoa(number) + "/"
	console := c.jobPath(prefix+"console", nil)
	out := jenkinsBuildLog{Number: number, Offset: offset, NextOffset: offset, ConsoleURL: console.String()}
	query := url.Values{"start": {strconv.FormatInt(offset, 10)}}
	res, err := c.get(ctx, c.jobPath(prefix+"logText/progressiveText", query), true)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, jenkinsLogLimit+1))
	if err != nil {
		return out, errJenkinsConnection
	}
	if size := res.Header.Get("X-Text-Size"); size != "" {
		next, err := strconv.ParseInt(size, 10, 64)
		if err != nil || next < offset || next > 1<<40 {
			return out, errJenkinsResponse
		}
		out.NextOffset = next
	}
	out.More = res.Header.Get("X-More-Data") == "true"
	if len(body) > jenkinsLogLimit {
		body = body[:jenkinsLogLimit]
		// Jenkins offsets count its log file bytes, which differ from the decoded text length.
		out.Truncated, out.More = true, false
	}
	out.Text = strings.ReplaceAll(strings.ToValidUTF8(string(body), "�"), string(c.token), "[已屏蔽]")
	return out, nil
}
