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
	if (action == "DEPLOY" || action == "ROLLBACK") && p.project.Archived {
		return "", errJenkinsArchived
	}
	if p.host.ID == "" || p.host.Maintenance && action != "VERIFY" {
		return "", errors.New("PROJECT_HOST_UNAVAILABLE")
	}
	now := Now()
	if action == "RECOVER" {
		return h.enqueueProjectRecovery(tx, actor, id, token, trace, now)
	}
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
		if err = checkProjectRollback(tx, p, current, snap.Spec); err != nil {
			return "", err
		}
		snap.Spec.ExpectedRelease = current
		snap.ProjectVersion, snap.HostVersion = p.project.Version, p.host.Version
	case "VERIFY":
		reference := current
		if current == "" {
			err = tx.QueryRow(`SELECT id FROM project_deployment_jobs
				WHERE deployment_id=? AND action<>'VERIFY' AND status='UNKNOWN'`, id).Scan(&reference)
			if errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("PROJECT_NOT_DEPLOYED")
			}
			if err != nil {
				return "", err
			}
		}
		snap, err = readProjectJobSnapshot(tx, reference)
		if err != nil {
			return "", err
		}
		snap.Spec.ExpectedRelease = current
		snap.ProjectVersion, snap.HostVersion = p.project.Version, p.host.Version
	case "DECOMMISSION":
		if current == "" || token != current {
			return "", errors.New("PROJECT_DECOMMISSION_RELEASE_CHANGED")
		}
		snap, err = readProjectJobSnapshot(tx, current)
		if err != nil {
			return "", err
		}
		if err = checkProjectDecommissionDependencies(tx, p, snap); err != nil {
			return "", err
		}
		snap.Spec.ExpectedRelease = current
		snap.ProjectVersion, snap.HostVersion = p.project.Version, p.host.Version
	default:
		return "", errors.New("PROJECT_ACTION_INVALID")
	}
	if err = checkProjectDeploymentLocks(tx, id, p.host.ID, action != "VERIFY"); err != nil {
		return "", err
	}
	if err = checkProjectQueueSize(tx); err != nil {
		return "", err
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

func checkProjectQueueSize(q applicationReader) error {
	var size int
	if err := q.QueryRow(`SELECT count(*) FROM project_deployment_jobs WHERE status IN ('QUEUED','RUNNING')`).Scan(&size); err != nil {
		return err
	}
	if size >= 20 {
		return errors.New("PROJECT_QUEUE_FULL")
	}
	return nil
}

func (h *HTTP) enqueueProjectRecovery(tx *sql.Tx, actor *session, id, expectedJob, trace, now string) (string, error) {
	if err := checkProjectDeploymentLocks(tx, id, "", false); err != nil {
		return "", err
	}
	if err := checkProjectQueueSize(tx); err != nil {
		return "", err
	}
	result, err := tx.Exec(`UPDATE project_deployment_jobs SET status='QUEUED',phase='RECONCILING',updated_at=?,
		execution_log=substr(execution_log||?,-8192) WHERE id=? AND deployment_id=? AND action<>'VERIFY' AND status='UNKNOWN'`,
		now, projectExecutionLine(now, "RECONCILING", ""), expectedJob, id)
	if err != nil {
		return "", err
	}
	if n, err := result.RowsAffected(); err != nil {
		return "", err
	} else if n != 1 {
		return "", errors.New("PROJECT_RECOVERY_JOB_CHANGED")
	}
	if err = auditTx(tx, actor.Username, "PROJECT_DEPLOY_RECOVER", expectedJob, "SUCCESS", trace); err != nil {
		return "", err
	}
	return expectedJob, tx.Commit()
}

func projectExecutionLine(now, phase, code string) string {
	if code != "" {
		return now + "  " + phase + "  " + code + "\n"
	}
	return now + "  " + phase + "\n"
}

func (h *HTTP) runProjectDeployments(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	retrySweepAt := time.Time{}
	sweepNeeded := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// This is the only project worker and it executes jobs synchronously.
			// Between jobs, RUNNING rows can only be leftovers from an interrupted
			// worker or a failed terminal write. Retry even without a restart.
			if sweepNeeded && time.Now().After(retrySweepAt) {
				if err := h.recoverInterruptedProjectJobs(ctx); err != nil {
					h.logger.Print("error=PROJECT_INTERRUPTED_JOB_SWEEP_FAILED")
					retrySweepAt = time.Now().Add(5 * time.Second)
				} else {
					sweepNeeded = false
					retrySweepAt = time.Time{}
				}
			}
			if sweepNeeded {
				continue
			}
			job, err := scanProjectJob(h.store.DB.QueryRow(`SELECT ` + projectJobSummaryFields + ` FROM project_deployment_jobs
				WHERE status='QUEUED' AND (action='VERIFY' OR EXISTS (SELECT 1 FROM targets t
					WHERE t.id=project_deployment_jobs.target_id AND t.maintenance=0))
				ORDER BY updated_at,id LIMIT 1`))
			if err == nil {
				h.processProjectDeployment(ctx, job)
				sweepNeeded = true
			}
		}
	}
}

func (h *HTTP) recoverInterruptedProjectJobs(ctx context.Context) error {
	now := Now()
	_, err := h.store.DB.ExecContext(ctx, `UPDATE project_deployment_jobs
		SET status=CASE WHEN action='VERIFY' OR phase='ARTIFACT_DOWNLOAD' THEN 'FAILED' ELSE 'UNKNOWN' END,
			error_code='PROJECT_JOB_INTERRUPTED', health='', updated_at=?,execution_log=substr(execution_log||?,-8192)
		WHERE status='RUNNING'`, now, projectExecutionLine(now, "INTERRUPTED", "PROJECT_JOB_INTERRUPTED"))
	return err
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
	action, phase, reconcile := projectJobExecution(job.Action, job.Phase)
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
	if err != nil || host.Maintenance && job.Action != "VERIFY" {
		fail("PROJECT_HOST_UNAVAILABLE")
		return
	}
	if !reconcile && job.Action != "VERIFY" && host.Version != snap.HostVersion {
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
		if err == nil && snap.Spec.Site != nil && snap.Spec.Site.HealthPath == deploy.StreamFusionSiteHealthPath {
			err = validateStreamFusionWebArtifact(artifact)
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
	if action == deploy.ProjectRollback {
		if err := h.checkQueuedProjectRollback(ctx, snap); err != nil {
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

func checkProjectRollback(q applicationReader, p projectDeploymentContext, current string, spec deploy.ProjectDeployment) error {
	if spec.Kind != "STATIC" {
		return checkProjectBackendEndpointChange(q, p, current, spec)
	}
	if err := checkStreamFusionSiteRollback(q, p, spec.Site); err != nil {
		return err
	}
	if spec.Site == nil {
		return errors.New("PROJECT_NGINX_CONFIG_INVALID")
	}
	if err := checkProjectSiteUpstreams(q, p, spec.Site); err != nil {
		return err
	}
	return checkProjectSiteConflict(q, p, spec)
}

func (h *HTTP) checkQueuedProjectRollback(ctx context.Context, snap projectDeploymentSnapshot) error {
	tx, err := h.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readProjectDeployment(tx, snap.ProjectID, snap.Spec.ID)
	if err != nil {
		return err
	}
	current, previous, err := readProjectVersions(tx, snap.Spec.ID)
	if err != nil {
		return err
	}
	if current != snap.Spec.ExpectedRelease || previous != snap.Spec.ReleaseID || p.host.ID != snap.HostID {
		return errors.New("PROJECT_PLAN_CHANGED")
	}
	return checkProjectRollback(tx, p, current, snap.Spec)
}

func projectWorkerCode(err error, fallback string) string {
	if err != nil && projectResultCode.MatchString(err.Error()) {
		return err.Error()
	}
	return fallback
}

func (h *HTTP) recordProjectTargetResult(job projectDeploymentJob, snap projectDeploymentSnapshot, result deploy.ProjectDeployResult, reconcile bool) {
	outcome := resolveProjectTargetOutcome(job.Action, snap.Spec, result, reconcile)
	log := ""
	if outcome.keepTargetLog {
		if !reconcile && (job.Action == "DEPLOY" || job.Action == "ROLLBACK") && snap.Spec.Kind == "JAVA" && result.Phase == "TARGET_EXECUTION" {
			log = projectStartupLog(result.Output)
		}
		if result.WarningCode == "PROJECT_RELEASE_RETENTION_PENDING" {
			log += projectExecutionLine(Now(), "RELEASE_RETENTION", result.WarningCode)
		}
	}
	h.finishProjectDeploymentWithLog(job, snap, outcome.status, outcome.phase, outcome.code, outcome.health, outcome.promote, log)
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
		fallbackStatus, fallbackPhase := "UNKNOWN", "RECONCILING"
		if job.Action == "VERIFY" {
			fallbackStatus, fallbackPhase = "FAILED", "OBSERVING"
		}
		if fallbackErr := h.commitProjectDeployment(job, snap, fallbackStatus, fallbackPhase, "PROJECT_STATE_COMMIT_FAILED", "", false, log); fallbackErr != nil {
			h.logger.Printf("traceId=%s jobId=%s error=PROJECT_STATE_FALLBACK_FAILED", job.TraceID, job.JobID)
		}
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
		if job.Action == "DECOMMISSION" {
			_, err = tx.Exec("DELETE FROM project_deployment_running WHERE deployment_id=? AND current_id=?", snap.Spec.ID, active)
		} else {
			_, err = tx.Exec(`INSERT INTO project_deployment_running(deployment_id,current_id,previous_id)
				VALUES(?,?,nullif(?,'')) ON CONFLICT(deployment_id) DO UPDATE SET current_id=excluded.current_id,previous_id=excluded.previous_id`,
				snap.Spec.ID, snap.Spec.ReleaseID, active)
		}
		if err != nil {
			return err
		}
	}
	now := Now()
	result, err := tx.Exec(`UPDATE project_deployment_jobs SET status=?,phase=?,error_code=?,health=?,updated_at=?,
		execution_log=substr(execution_log||?,-8192) WHERE id=? AND status IN ('RUNNING','QUEUED')`,
		status, phase, code, health, now, projectExecutionLine(now, phase, code)+log, job.JobID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return errors.New("PROJECT_JOB_CHANGED")
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
