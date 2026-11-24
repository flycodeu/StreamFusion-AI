package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func readProjectJobSnapshot(q applicationReader, id string) (snap projectDeploymentSnapshot, err error) {
	var raw string
	err = q.QueryRow("SELECT snapshot_json FROM project_deployment_jobs WHERE id=?", id).Scan(&raw)
	if err == nil {
		err = deploy.StrictJSON([]byte(raw), &snap)
	}
	return
}

func (h *HTTP) queueProjectDeployment(w http.ResponseWriter, r *http.Request, actor *session, projectID, id, action, token string) {
	jobID, err := h.enqueueProjectDeployment(actor, projectID, id, action, token, r.Context().Value(traceKey{}).(string))
	if err != nil {
		h.projectDeployError(w, r, err)
		return
	}
	w.Header().Set("Location", api+"/jobs/"+jobID)
	h.reply(w, r, http.StatusAccepted, "SUCCESS", "", map[string]string{"jobId": jobID})
}

func (h *HTTP) enqueueProjectDeployment(actor *session, projectID, id, action, token, trace string) (string, error) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	p, err := readProjectDeployment(tx, projectID, id)
	if err != nil {
		return "", err
	}
	if action != "VERIFY" && p.project.Archived {
		return "", errJenkinsArchived
	}
	if p.host.ID == "" || p.host.Maintenance {
		return "", errors.New("PROJECT_HOST_UNAVAILABLE")
	}
	now := Now()
	job := uuid.NewString()
	var snap projectDeploymentSnapshot
	var planHash string
	current, previous, err := readProjectVersions(tx, id)
	if err != nil {
		return "", err
	}
	switch action {
	case "DEPLOY":
		planHash = hashToken(token)
		var raw, expiry, accepted string
		err = tx.QueryRow(`SELECT snapshot_json,expires_at,coalesce(job_id,'')
			FROM project_deployment_plans WHERE token_hash=? AND deployment_id=? AND actor_id=?`, planHash, id, actor.AdminID).Scan(&raw, &expiry, &accepted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if accepted != "" {
			return accepted, nil
		}
		if errors.Is(err, sql.ErrNoRows) || expiry <= now {
			return "", errors.New("PROJECT_PLAN_EXPIRED")
		}
		if err = deploy.StrictJSON([]byte(raw), &snap); err != nil {
			return "", err
		}
		if _, err = validateProjectDeploySnapshot(tx, snap); err != nil {
			return "", err
		}
		job = snap.Spec.ReleaseID
	case "ROLLBACK":
		if previous == "" {
			return "", errors.New("PROJECT_NO_ROLLBACK")
		}
		if previous != token {
			return "", errors.New("PROJECT_ROLLBACK_PREVIOUS_CHANGED")
		}
		snap, err = readProjectJobSnapshot(tx, previous)
		if err != nil {
			return "", err
		}
		if snap.Spec.Kind == "STATIC" {
			if err = checkProjectSiteConflict(tx, p, snap.Spec); err != nil {
				return "", err
			}
		}
		snap.Spec.ExpectedRelease = current
		snap.ProjectVersion, snap.HostVersion = p.project.Version, p.host.Version
	case "VERIFY":
		var pending string
		err = tx.QueryRow(`SELECT id FROM project_deployment_jobs WHERE deployment_id=? AND status='UNKNOWN'`, id).Scan(&pending)
		if err == nil {
			_, err = tx.Exec(`UPDATE project_deployment_jobs SET status='QUEUED',phase='RECONCILING',updated_at=?,
				execution_log=substr(execution_log||?,-8192) WHERE id=?`, now, projectExecutionLine(now, "RECONCILING", ""), pending)
			if err == nil {
				err = auditTx(tx, actor.Username, "PROJECT_DEPLOY_RECONCILE", pending, "SUCCESS", trace)
			}
			if err != nil {
				return "", err
			}
			return pending, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if current == "" {
			return "", errors.New("PROJECT_NOT_DEPLOYED")
		}
		snap, err = readProjectJobSnapshot(tx, current)
		if err != nil {
			return "", err
		}
		snap.Spec.ExpectedRelease = current
		snap.ProjectVersion, snap.HostVersion = p.project.Version, p.host.Version
	}
	if err = checkProjectDeploymentLocks(tx, id, p.host.ID, action != "VERIFY"); err != nil {
		return "", err
	}
	var queueSize int
	if err := tx.QueryRow(`SELECT count(*) FROM project_deployment_jobs WHERE status IN ('QUEUED','RUNNING')`).Scan(&queueSize); err != nil {
		return "", err
	}
	if queueSize >= 20 {
		return "", errors.New("PROJECT_QUEUE_FULL")
	}
	// Rollback uses the frozen version already on the host; no saved credentials
	// or Jenkins connection are needed to verify or restore it.
	if action != "DEPLOY" {
		snap.Spec.Files, snap.Spec.Variables, snap.Spec.DependencyChecks = nil, nil, nil
	}
	_, err = tx.Exec(`INSERT INTO project_deployment_jobs(id,deployment_id,target_id,actor_id,action,status,phase,
		host_name,subject_name,build_number,commit_id,release_id,baseline_id,snapshot_json,trace_id,created_at,updated_at,execution_log)
		VALUES(?,?,?,?,?,'QUEUED','QUEUED',?,?,?,?,?,?,?,?,?,?,?)`, job, id, p.host.ID, actor.AdminID, action,
		p.host.Name, p.project.Name+" / "+p.env.Name+" / "+p.module.Name, snap.BuildNumber, snap.Artifact.Commit,
		snap.Spec.ReleaseID, current, jsonText(snap), trace, now, now, projectExecutionLine(now, "QUEUED", ""))
	if err == nil && planHash != "" {
		_, err = tx.Exec(`UPDATE project_deployment_plans SET job_id=?,snapshot_json='{}' WHERE token_hash=?`, job, planHash)
	}
	if err == nil {
		err = auditTx(tx, actor.Username, "PROJECT_DEPLOY_"+action, job, "SUCCESS", trace)
	}
	if err != nil {
		return "", err
	}
	return job, tx.Commit()
}

func projectExecutionLine(now, phase, code string) string {
	if code != "" {
		return now + "  " + phase + "  " + code + "\n"
	}
	return now + "  " + phase + "\n"
}

func (h *HTTP) runProjectDeployments(ctx context.Context) {
	_, _ = h.store.DB.Exec(`UPDATE project_deployment_jobs SET status='UNKNOWN',error_code='CONTROLLER_RESTARTED',
		updated_at=?,execution_log=substr(execution_log||?,-8192) WHERE status='RUNNING'`,
		Now(), projectExecutionLine(Now(), "UNKNOWN", "CONTROLLER_RESTARTED"))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			job, err := scanProjectJob(h.store.DB.QueryRow(`SELECT ` + projectJobSummaryFields + ` FROM project_deployment_jobs
				WHERE status='QUEUED' ORDER BY updated_at,id LIMIT 1`))
			if err == nil {
				h.processProjectDeployment(ctx, job)
			}
		}
	}
}

func (h *HTTP) updateProjectJobPhase(id, phase string) error {
	now := Now()
	res, err := h.store.DB.Exec(`UPDATE project_deployment_jobs SET status='RUNNING',phase=?,error_code='',
		updated_at=?,execution_log=substr(execution_log||?,-8192) WHERE id=? AND status IN ('QUEUED','RUNNING')`,
		phase, now, projectExecutionLine(now, phase, ""), id)
	if err == nil {
		var count int64
		count, err = res.RowsAffected()
		if err == nil && count != 1 {
			err = errors.New("PROJECT_JOB_CHANGED")
		}
	}
	return err
}

var projectResultCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`)

func (h *HTTP) processProjectDeployment(ctx context.Context, job projectDeploymentJob) {
	snap, err := readProjectJobSnapshot(h.store.DB, job.JobID)
	if err != nil {
		h.finishProjectDeployment(job, snap, "FAILED", "PREFLIGHT", "PROJECT_JOB_DAMAGED", "", false)
		return
	}
	action := deploy.ProjectObserve
	phase := "OBSERVING"
	reconcile := job.Phase == "RECONCILING"
	if reconcile {
		phase = "RECONCILING"
	} else if job.Action == "DEPLOY" {
		action, phase = deploy.ProjectApply, "ARTIFACT_DOWNLOAD"
	} else if job.Action == "ROLLBACK" {
		action, phase = deploy.ProjectRollback, "TARGET_EXECUTION"
	}
	if err = h.updateProjectJobPhase(job.JobID, phase); err != nil {
		return
	}
	fail := func(code string) {
		status := "FAILED"
		if reconcile {
			status = "UNKNOWN"
		}
		h.finishProjectDeployment(job, snap, status, phase, code, "", false)
	}
	host, err := h.store.readTarget(snap.HostID)
	if err != nil || host.Maintenance {
		fail("PROJECT_HOST_UNAVAILABLE")
		return
	}
	if !reconcile && host.Version != snap.HostVersion {
		fail("PROJECT_PLAN_CHANGED")
		return
	}
	archive := ""
	if action == deploy.ProjectApply {
		if _, err = h.checkQueuedProjectDeployment(ctx, snap); err != nil {
			fail(projectWorkerCode(err, "PROJECT_PLAN_CHANGED"))
			return
		}
		temp, err := os.CreateTemp(h.store.Dir, "project-flight-")
		if err != nil {
			fail("PROJECT_ARTIFACT_TEMP_FAILED")
			return
		}
		archive = temp.Name()
		defer os.Remove(archive)
		project, module, err := h.loadApplicationProjectAndModule(ctx, snap.ProjectID, snap.ModuleID)
		var artifact projectBuildArtifact
		if err == nil {
			artifact, err = h.fetchProjectReleaseArtifact(ctx, project, module, snap.BuildNumber, temp)
		}
		closeErr := temp.Close()
		if err != nil || closeErr != nil || artifact != snap.Artifact {
			if err == nil {
				err = errJenkinsReleaseMismatch
			}
			fail(projectWorkerCode(err, "JENKINS_RELEASE_ARTIFACT_UNAVAILABLE"))
			return
		}
		phase = "TARGET_EXECUTION"
		if err := h.updateProjectJobPhase(job.JobID, phase); err != nil {
			return
		}
	}
	targetCtx, cancel := context.WithTimeout(ctx, 4*time.Minute+45*time.Second)
	defer cancel()
	if err = h.projectTargetCapabilities(targetCtx, host); err != nil {
		fail(projectWorkerCode(err, "PROJECT_TARGET_UNAVAILABLE"))
		return
	}
	if action == deploy.ProjectApply {
		// Fence draft/host changes until the signed remote request has finished.
		// The queued job locks the host; the transaction rechecks draft and dependency identity.
		if host, err = h.checkQueuedProjectDeployment(ctx, snap); err != nil {
			fail(projectWorkerCode(err, "PROJECT_PLAN_CHANGED"))
			return
		}
	}
	req := projectTargetRequest(action, job.JobID, job.TraceID, snap.Spec)
	var result deploy.ProjectDeployResult
	err = h.store.callTarget(targetCtx, host, req, archive, &result)
	if err != nil {
		code := projectWorkerCode(err, "PROJECT_TARGET_UNAVAILABLE")
		if action == deploy.ProjectApply && (code == "PROJECT_CREDENTIAL_UNAVAILABLE" || code == "PROJECT_CREDENTIAL_INVALID" || code == "INSTALL_REQUEST_TOO_LARGE") {
			fail(code)
			return
		}
		status := "FAILED"
		if job.Action != "VERIFY" || reconcile {
			status = "UNKNOWN"
		}
		h.finishProjectDeployment(job, snap, status, phase, code, "", false)
		return
	}
	h.recordProjectTargetResult(job, snap, result, reconcile)
}

func (h *HTTP) checkQueuedProjectDeployment(ctx context.Context, snap projectDeploymentSnapshot) (host target, err error) {
	tx, err := h.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err == nil {
		defer tx.Rollback()
		var state projectDeploymentContext
		state, err = validateProjectDeploySnapshot(tx, snap)
		host = state.host
	}
	return
}

func projectWorkerCode(err error, fallback string) string {
	if err != nil && projectResultCode.MatchString(err.Error()) {
		return err.Error()
	}
	return fallback
}

func (h *HTTP) recordProjectTargetResult(job projectDeploymentJob, snap projectDeploymentSnapshot, result deploy.ProjectDeployResult, reconcile bool) {
	log := ""
	if !reconcile && (job.Action == "DEPLOY" || job.Action == "ROLLBACK") && snap.Spec.Kind == "JAVA" && result.Phase == "TARGET_EXECUTION" {
		log = projectStartupLog(result.Output)
	}
	if result.Status == "UNKNOWN" {
		h.finishProjectDeploymentWithLog(job, snap, "UNKNOWN", "RECONCILING", "PROJECT_RECOVERY_REQUIRED", "", false, log)
		return
	}
	if result.Status != "SUCCEEDED" {
		code := projectWorkerCode(errors.New(result.ErrorCode), "PROJECT_TARGET_FAILED")
		status := "FAILED"
		if reconcile || code == "PROJECT_RELEASE_CHANGED" {
			status = "UNKNOWN"
		}
		h.finishProjectDeploymentWithLog(job, snap, status, "TARGET_EXECUTION", code, "", false, log)
		return
	}
	wanted := snap.Spec.ReleaseID
	if job.Action == "VERIFY" {
		wanted = snap.Spec.ExpectedRelease
	}
	if result.ReleaseID == wanted {
		// A candidate is promoted only after observing the expected healthy version.
		if job.Action != "VERIFY" && result.Health != "READY" {
			h.finishProjectDeployment(job, snap, "UNKNOWN", "RECONCILING", "PROJECT_HEALTH_UNCONFIRMED", result.Health, false)
			return
		}
		h.finishProjectDeployment(job, snap, "SUCCEEDED", "VERIFIED", "", result.Health, job.Action != "VERIFY")
	} else if reconcile && result.ReleaseID == snap.Spec.ExpectedRelease {
		h.finishProjectDeployment(job, snap, "FAILED", "BASELINE_RETAINED", "PROJECT_CHANGE_NOT_ACTIVE", result.Health, false)
	} else {
		h.finishProjectDeployment(job, snap, "UNKNOWN", "RECONCILING", "PROJECT_RELEASE_CHANGED", "", false)
	}
}

func (h *HTTP) finishProjectDeployment(job projectDeploymentJob, snap projectDeploymentSnapshot, status, phase, code, health string, promote bool) {
	h.finishProjectDeploymentWithLog(job, snap, status, phase, code, health, promote, "")
}

func projectStartupLog(output string) string {
	log := strings.TrimSpace(deploy.RedactSoftwareLogs(output))
	if log == "" {
		return ""
	}
	const limit = 5500
	if len(log) > limit {
		log = "[较早的日志已截断]\n" + strings.ToValidUTF8(log[len(log)-limit:], "")
	}
	return "[本次 Java 启动日志]\n" + log + "\n"
}

func (h *HTTP) finishProjectDeploymentWithLog(job projectDeploymentJob, snap projectDeploymentSnapshot, status, phase, code, health string, promote bool, log string) {
	err := h.commitProjectDeployment(job, snap, status, phase, code, health, promote, log)
	if err != nil {
		h.logger.Printf("traceId=%s jobId=%s error=PROJECT_STATE_COMMIT_FAILED", job.TraceID, job.JobID)
		_ = h.commitProjectDeployment(job, snap, "UNKNOWN", "RECONCILING", "PROJECT_STATE_COMMIT_FAILED", "", false, log)
	}
}

func (h *HTTP) commitProjectDeployment(job projectDeploymentJob, snap projectDeploymentSnapshot, status, phase, code, health string, promote bool, log string) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if promote {
		active, _, err := readProjectVersions(tx, snap.Spec.ID)
		if err != nil {
			return err
		}
		if active != snap.Spec.ExpectedRelease {
			return errors.New("PROJECT_RELEASE_CHANGED")
		}
		_, err = tx.Exec(`INSERT INTO project_deployment_running(deployment_id,current_id,previous_id)
			VALUES(?,?,nullif(?,'')) ON CONFLICT(deployment_id) DO UPDATE SET current_id=excluded.current_id,previous_id=excluded.previous_id`,
			snap.Spec.ID, snap.Spec.ReleaseID, active)
		if err != nil {
			return err
		}
	}
	now := Now()
	_, err = tx.Exec(`UPDATE project_deployment_jobs SET status=?,phase=?,error_code=?,health=?,updated_at=?,
		execution_log=substr(execution_log||?,-8192) WHERE id=? AND status IN ('RUNNING','QUEUED')`,
		status, phase, code, health, now, projectExecutionLine(now, phase, code)+log, job.JobID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (h *HTTP) projectDeploymentLogs(w http.ResponseWriter, r *http.Request, projectID, id string) {
	tx, err := h.store.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		h.internal(w, r, err)
		return
	}
	p, err := readProjectDeployment(tx, projectID, id)
	var snap projectDeploymentSnapshot
	if err == nil {
		var current string
		current, _, err = readProjectVersions(tx, id)
		if err == nil && current == "" {
			err = errors.New("PROJECT_NOT_DEPLOYED")
		}
		if err == nil {
			snap, err = readProjectJobSnapshot(tx, current)
		}
	}
	_ = tx.Rollback()
	if err != nil {
		h.projectDeployError(w, r, err)
		return
	}
	select {
	case h.hostCheckSlots <- struct{}{}:
		defer func() { <-h.hostCheckSlots }()
	default:
		h.projectDeployError(w, r, errJenkinsBusy)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.ProjectDeployResult
	req := projectTargetRequest(deploy.ProjectLogs, uuid.NewString(), r.Context().Value(traceKey{}).(string), snap.Spec)
	if err = h.store.callTarget(ctx, p.host, req, "", &result); err != nil || result.Status != "SUCCEEDED" {
		h.projectDeployError(w, r, errors.New("PROJECT_LOGS_UNAVAILABLE"))
		return
	}
	h.success(w, r, map[string]string{"output": result.Output, "observedAt": result.ObservedAt})
}

func (h *HTTP) cleanupProjectDownloads() {
	paths, _ := filepath.Glob(filepath.Join(h.store.Dir, "project-flight-*"))
	for _, name := range paths {
		if info, err := os.Lstat(name); err == nil && info.Mode().IsRegular() {
			if err = os.Remove(name); err != nil {
				h.logger.Print("error=PROJECT_ARTIFACT_CLEANUP_FAILED")
			}
		}
	}
}
