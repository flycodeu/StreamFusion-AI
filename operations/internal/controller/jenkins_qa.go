package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	errJenkinsTriggerUnknown = errors.New("JENKINS_TRIGGER_UNCONFIRMED")
	errJenkinsConfigChanged  = errors.New("JENKINS_CONFIG_CHANGED")
	errJenkinsArchived       = errors.New("APPLICATION_PROJECT_ARCHIVED")
)

// Persist intent before contacting Jenkins. A lost HTTP response must never
// cause a second build when the operator retries the same request.
func (h *HTTP) reserveQA(actor *session, scope jenkinsScope, version int, requestID, trace string) (int, error) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var archived bool
	err = tx.QueryRow("SELECT archived FROM application_projects WHERE id=?", scope.project).Scan(&archived)
	if err != nil {
		return 0, err
	}
	_, current, _, err := effectiveJenkinsMonitor(tx, scope)
	if err != nil {
		return 0, err
	}
	if current.Version != version {
		return 0, errJenkinsConfigChanged
	}
	if archived {
		return 0, errJenkinsArchived
	}
	var previous, queue int
	err = tx.QueryRow("SELECT monitor_version,queue_id FROM application_qa_requests WHERE scope_id=? AND request_id=?", scope.requestScope(), requestID).Scan(&previous, &queue)
	if err == nil {
		if previous != version {
			return 0, errJenkinsConfigChanged
		}
		if queue == 0 {
			return 0, errJenkinsTriggerUnknown
		}
		return queue, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	_, err = tx.Exec("INSERT INTO application_qa_requests(deployment_id,module_id,request_id,monitor_version) VALUES(NULLIF(?,''),NULLIF(?,''),?,?)", scope.instance, scope.module, requestID, version)
	if err == nil {
		err = auditTx(tx, actor.Username, "JENKINS_QA_REQUESTED", scope.requestScope(), "SUCCESS", trace)
	}
	if err != nil {
		return 0, err
	}
	return 0, tx.Commit()
}

func (h *HTTP) triggerJenkinsQA(w http.ResponseWriter, r *http.Request, actor *session, scope jenkinsScope) {
	var in struct {
		RequestID       string `json:"requestId"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "请携带 QA 请求 ID 和配置版本")
		return
	}
	id, idErr := uuid.Parse(in.RequestID)
	version, versionErr := strconv.Atoi(in.ExpectedVersion)
	if idErr != nil || id.String() != in.RequestID || versionErr != nil || version <= 0 {
		h.fail(w, r, 400, "INVALID_INPUT", "请携带有效的 QA 请求 ID 和配置版本")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	select {
	case h.hostCheckSlots <- struct{}{}:
		defer func() { <-h.hostCheckSlots }()
	default:
		h.jenkinsError(w, r, errJenkinsBusy)
		return
	}
	monitor, endpoint, job, err := h.store.jenkinsBuildBinding(ctx, scope)
	if err != nil {
		h.jenkinsError(w, r, err)
		return
	}
	if monitor.Version != version {
		h.jenkinsError(w, r, errJenkinsConfigChanged)
		return
	}
	trace := r.Context().Value(traceKey{}).(string)
	queue, err := h.reserveQA(actor, scope, version, in.RequestID, trace)
	if err != nil {
		h.jenkinsError(w, r, err)
		return
	}
	if queue > 0 {
		h.success(w, r, map[string]any{"queueId": queue, "status": "QUEUED"})
		return
	}
	// Before a POST or after an explicit rejection, retrying is safe.
	safeToRetry := true
	defer func() {
		if safeToRetry {
			if _, e := h.store.DB.Exec("DELETE FROM application_qa_requests WHERE scope_id=? AND request_id=? AND queue_id=0", scope.requestScope(), in.RequestID); e != nil {
				h.logger.Printf("traceId=%s error=QA_REQUEST_CLEANUP_FAILED", trace)
			}
		}
	}()
	token, err := monitor.openToken(h.store.Key, scope)
	defer clear(token)
	if err != nil || !jenkinsToken.Match(token) {
		h.jenkinsError(w, r, errJenkinsSecret)
		return
	}
	client, done, err := h.store.connectJenkinsBuild(ctx, endpoint, *job, monitor.Username, token)
	if err != nil {
		h.jenkinsError(w, r, err)
		return
	}
	defer done()
	if monitor.automation != nil {
		if err = client.ensureWorkflow(ctx, endpoint.base, monitor.automation); err != nil {
			h.jenkinsError(w, r, err)
			return
		}
	}
	queue, safeToRetry, err = client.enqueue(ctx, endpoint.base)
	if err != nil {
		h.jenkinsError(w, r, err)
		return
	}
	result, err := h.store.DB.Exec("UPDATE application_qa_requests SET queue_id=? WHERE scope_id=? AND request_id=? AND monitor_version=?", queue, scope.requestScope(), in.RequestID, version)
	if err == nil {
		var updated int64
		updated, err = result.RowsAffected()
		if err == nil && updated != 1 {
			err = errJenkinsTriggerUnknown
		}
	}
	if err != nil {
		h.jenkinsError(w, r, errJenkinsTriggerUnknown)
		return
	}
	h.success(w, r, map[string]any{"queueId": queue, "status": "QUEUED"})
}

func (c jenkinsBuildClient) enqueue(ctx context.Context, base url.URL) (int, bool, error) {
	target := c.jobPath("build", nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return 0, true, errJenkinsResponse
	}
	req.SetBasicAuth(c.username, string(c.token))
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, false, errJenkinsTriggerUnknown
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Jenkins") != c.version {
		return 0, false, errJenkinsTriggerUnknown
	}
	switch resp.StatusCode {
	case http.StatusCreated:
		// Only the numeric ID is used. Never follow a Location with credentials.
		location, err := url.Parse(resp.Header.Get("Location"))
		if err == nil && location.User == nil && location.RawQuery == "" && location.Fragment == "" {
			part, ok := strings.CutPrefix(strings.TrimSuffix(location.EscapedPath(), "/"), base.EscapedPath()+"/queue/item/")
			queue, e := strconv.Atoi(part)
			if ok && e == nil && queue > 0 && queue <= 1<<31-1 && strconv.Itoa(queue) == part {
				return queue, false, nil
			}
		}
	case http.StatusNotFound:
		return 0, true, errJenkinsMissing
	case http.StatusUnauthorized, http.StatusForbidden:
		return 0, true, errJenkinsAuth
	case http.StatusBadRequest, http.StatusConflict, http.StatusMethodNotAllowed:
		return 0, true, errJenkinsResponse
	}
	return 0, false, errJenkinsTriggerUnknown
}

func (c jenkinsBuildClient) queueStatus(ctx context.Context, base url.URL, id int) (jenkinsBuildStatus, error) {
	out := jenkinsBuildStatus{Configured: true, Status: "QUEUED", ObservedAt: Now()}
	base.Path += "/queue/item/" + strconv.Itoa(id) + "/api/json"
	base.RawQuery = url.Values{"tree": {"id,why,cancelled,task[url],executable[number]"}}.Encode()
	var item struct {
		ID        int    `json:"id"`
		Why       string `json:"why"`
		Cancelled bool   `json:"cancelled"`
		Task      struct {
			URL string `json:"url"`
		} `json:"task"`
		Executable *struct {
			Number int `json:"number"`
		} `json:"executable"`
	}
	err := c.readJSONAt(ctx, base, &item)
	if errors.Is(err, errJenkinsMissing) {
		// Jenkins garbage collects started queue items. Match queueId, even
		// when another operator has already started a later build.
		var history struct {
			Builds []struct {
				Number  int
				QueueID int `json:"queueId"`
			}
		}
		if err = c.readJSON(ctx, "api/json", "builds[number,queueId]{0,100}", &history); err != nil {
			return out, err
		}
		for _, run := range history.Builds {
			if run.QueueID == id {
				return c.readRun(ctx, run.Number, out, false)
			}
		}
		return out, errJenkinsMissing
	}
	if err != nil {
		return out, err
	}
	task, err := url.Parse(item.Task.URL)
	if err != nil || item.ID != id || strings.TrimSuffix(task.Path, "/") != strings.TrimSuffix(c.job.Path, "/") {
		return out, errJenkinsResponse
	}
	if item.Cancelled {
		out.Status = "ABORTED"
		return out, nil
	}
	if item.Executable != nil {
		return c.readRun(ctx, item.Executable.Number, out, false)
	}
	out.QueueReason = c.label(item.Why)
	return out, nil
}
