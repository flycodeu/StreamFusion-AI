package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// Only named display fields are projected from the frozen request. The request
// itself can contain credential references or secrets and never leaves here.
const jobHistorySQL = `WITH inputs AS (
 SELECT j.*,CASE WHEN json_valid(input_json) THEN input_json ELSE '{}' END AS safe_input FROM jobs j
), history AS (
 SELECT j.id,j.kind,coalesce(j.instance_id,'') AS instance_id,coalesce(j.target_id,'') AS target_id,
 j.status,j.phase,j.error_code,j.target_ack_pending,j.trace_id,j.result_json,j.created_at,j.updated_at,
 coalesce(nullif(json_extract(j.safe_input,'$.software.id'),''),
          nullif(json_extract(j.safe_input,'$.installation.softwareId'),''),
          nullif(json_extract(j.safe_input,'$.reconfiguration.softwareId'),''),'') AS software_id,
 coalesce(nullif(json_extract(j.safe_input,'$.installation.config.name'),''),
          nullif(json_extract(j.safe_input,'$.reconfiguration.config.name'),''),
          nullif(s.name,''),nullif(json_extract(j.safe_input,'$.management.definition.name'),''),
          nullif(i.name,''),nullif(json_extract(j.safe_input,'$.plugin.key'),''),
          CASE WHEN j.kind='NODE_ENVIRONMENT_CHANGE' THEN 'Node.js 环境'
               WHEN j.kind='GIT_REPOSITORY_CHANGE' THEN 'Git 仓库'
               WHEN j.kind='GIT_ACCOUNT_CHANGE' THEN 'Git 账号' ELSE '' END) AS subject_name,
 coalesce(json_extract(j.safe_input,'$.management.operationKey'),'') AS operation_key,
 coalesce(t.name,'') AS host_name
 FROM inputs j LEFT JOIN targets t ON t.id=j.target_id LEFT JOIN instances i ON i.id=j.instance_id
 LEFT JOIN software_instances s ON s.id=coalesce(nullif(json_extract(j.safe_input,'$.software.id'),''),
    nullif(json_extract(j.safe_input,'$.installation.softwareId'),''),json_extract(j.safe_input,'$.reconfiguration.softwareId'))
) SELECT id,kind,instance_id,target_id,status,phase,error_code,target_ack_pending,trace_id,result_json,
 created_at,updated_at,software_id,subject_name,operation_key,host_name FROM history `

type jobSummary struct {
	ID                 string          `json:"id"`
	Kind               string          `json:"kind"`
	InstanceID         string          `json:"instanceId"`
	TargetID           string          `json:"targetId"`
	Status             string          `json:"status"`
	Phase              string          `json:"phase"`
	ErrorCode          string          `json:"errorCode"`
	TargetAckPending   bool            `json:"targetAckPending"`
	TraceID            string          `json:"traceId"`
	Result             json.RawMessage `json:"result"`
	CreatedAt          string          `json:"createdAt"`
	UpdatedAt          string          `json:"updatedAt"`
	SoftwareInstanceID string          `json:"softwareInstanceId"`
	SubjectName        string          `json:"subjectName"`
	OperationKey       string          `json:"operationKey"`
	HostName           string          `json:"hostName"`
}

func scanJobSummary(row interface{ Scan(...any) error }) (jobSummary, error) {
	var job jobSummary
	var result string
	err := row.Scan(&job.ID, &job.Kind, &job.InstanceID, &job.TargetID, &job.Status, &job.Phase,
		&job.ErrorCode, &job.TargetAckPending, &job.TraceID, &result, &job.CreatedAt,
		&job.UpdatedAt, &job.SoftwareInstanceID, &job.SubjectName, &job.OperationKey, &job.HostName)
	job.Result = json.RawMessage(result)
	return job, err
}

func (h *HTTP) listJobs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 20
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			h.fail(w, r, 400, "INVALID_INPUT", "每页条数须为1–100")
			return
		}
		limit = value
	}
	before := query.Get("before")
	created, id := "", ""
	if before != "" {
		var found bool
		created, id, found = strings.Cut(before, "|")
		_, err := time.Parse(time.RFC3339Nano, created)
		if !found || err != nil || id == "" || len(id) > 80 || strings.Contains(id, "|") {
			h.fail(w, r, 400, "INVALID_INPUT", "分页位置无效，请刷新记录")
			return
		}
	}
	host, kind, status, search := query.Get("hostId"), query.Get("kind"), query.Get("status"), strings.TrimSpace(query.Get("query"))
	for _, value := range []string{host, kind, status, search, id} {
		if len([]rune(value)) > 120 || strings.ContainsFunc(value, unicode.IsControl) {
			h.fail(w, r, 400, "INVALID_INPUT", "筛选条件无效")
			return
		}
	}
	if status != "" && status != "QUEUED" && status != "RUNNING" && status != "SUCCEEDED" && status != "FAILED" && status != "UNKNOWN" && status != "CANCELLED" {
		h.fail(w, r, 400, "INVALID_INPUT", "任务状态无效")
		return
	}
	if kind != "" && strings.ContainsFunc(kind, func(c rune) bool { return c != '_' && (c < 'A' || c > 'Z') && (c < '0' || c > '9') }) {
		h.fail(w, r, 400, "INVALID_INPUT", "操作类型无效")
		return
	}
	search = "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(search) + "%"
	rows, e := h.store.DB.QueryContext(r.Context(), jobHistorySQL+` WHERE
 (?='' OR target_id=?) AND (?='' OR (?='CANCELLED' AND (status='CANCELLED' OR phase='CANCELLED')) OR (?<>'CANCELLED' AND status=? AND phase<>'CANCELLED'))
 AND (?='' OR kind=? OR (?='SOFTWARE' AND (substr(kind,1,9)='SOFTWARE_' OR substr(kind,1,7)='PLUGIN_' OR kind IN ('NODE_ENVIRONMENT_CHANGE','GIT_REPOSITORY_CHANGE','GIT_ACCOUNT_CHANGE'))))
 AND (subject_name LIKE ? ESCAPE '\' OR host_name LIKE ? ESCAPE '\')
 AND (?='' OR created_at<? OR (created_at=? AND id<?))
 ORDER BY created_at DESC,id DESC LIMIT ?`, host, host, status, status, status, status, kind, kind, kind,
		search, search, before, created, created, id, limit+1)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer rows.Close()
	out := []jobSummary{}
	for rows.Next() {
		job, err := scanJobSummary(rows)
		if err != nil {
			h.internal(w, r, err)
			return
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		h.internal(w, r, err)
		return
	}
	cursor := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		cursor = last.CreatedAt + "|" + last.ID
	}
	h.success(w, r, map[string]any{"items": out, "nextCursor": cursor})
}
func (h *HTTP) getJob(w http.ResponseWriter, r *http.Request, id string) {
	job, e := scanJobSummary(h.store.DB.QueryRowContext(r.Context(), jobHistorySQL+" WHERE id=?", id))
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	h.success(w, r, job)
}
func (h *HTTP) RunOperations(ctx context.Context) {
	h.store.recoverInstallationMaterials()
	_, _ = h.store.DB.Exec("UPDATE jobs SET status='UNKNOWN',error_code='CONTROLLER_RESTARTED' WHERE status='RUNNING' AND kind<>'IMPORT'; UPDATE jobs SET status='FAILED',error_code='UPLOAD_INTERRUPTED' WHERE kind='IMPORT' AND (phase='UPLOADING' OR status='RUNNING')")
	_, _ = h.store.DB.Exec("UPDATE plugin_installations SET status='UNKNOWN',error_code='CONTROLLER_RESTARTED' WHERE last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	_, _ = h.store.DB.Exec("UPDATE software_instances SET actual_state='UNKNOWN',error_code='CONTROLLER_RESTARTED' WHERE last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	_, _ = h.store.DB.Exec("UPDATE software_instances SET installation_status='UNKNOWN' WHERE installation_status='INSTALLING' AND last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	_, _ = h.store.DB.Exec("UPDATE node_environments SET error_code='CONTROLLER_RESTARTED' WHERE last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	_, _ = h.store.DB.Exec("UPDATE git_repositories SET error_code='CONTROLLER_RESTARTED' WHERE last_job_id IN (SELECT id FROM jobs WHERE status='UNKNOWN')")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.finishPendingAck(ctx)
			var id, kind, raw string
			e := h.store.DB.QueryRow("SELECT id,kind,input_json FROM jobs WHERE status='QUEUED' AND kind<>'HOST_PREPARE' AND phase NOT IN ('WAITING_UPLOAD','UPLOADING','UPLOADED') AND (target_id IS NULL OR EXISTS(SELECT 1 FROM targets WHERE targets.id=jobs.target_id AND maintenance=0)) AND (kind<>'SOFTWARE_INSTALL' OR NOT EXISTS(SELECT 1 FROM operation_locks WHERE resource_key='target:'||jobs.target_id AND job_id<>jobs.id)) ORDER BY created_at,id LIMIT 1").Scan(&id, &kind, &raw)
			if e == nil {
				if kind == "IMPORT" {
					h.processImport(id, raw)
				} else if kind == "PLUGIN_INSTALL" || kind == "PLUGIN_REMOVE" {
					h.processPlugin(ctx, id, raw)
				} else if kind == "SOFTWARE_OPERATION" {
					h.processSoftwareOperation(ctx, id, raw)
				} else if kind == "GIT_REPOSITORY_CHANGE" {
					h.processGitRepository(ctx, id, raw)
				} else if kind == "NODE_ENVIRONMENT_CHANGE" {
					h.processNodeEnvironment(ctx, id, raw)
				} else if kind == "FIREWALL_CHANGE" {
					h.processFirewall(ctx, id, raw)
				} else if len(kind) > 9 && kind[:9] == "SOFTWARE_" {
					h.processSoftware(ctx, id, raw)
				} else {
					h.processRelease(ctx, id, raw)
				}
			}
		}
	}
}
func (h *HTTP) finishPendingAck(ctx context.Context) {
	var raw, resultRaw string
	e := h.store.DB.QueryRow("SELECT input_json,result_json FROM jobs WHERE target_ack_pending=1 AND status IN ('SUCCEEDED','FAILED') ORDER BY updated_at LIMIT 1").Scan(&raw, &resultRaw)
	if e != nil {
		return
	}
	var req deploy.Request
	var result deploy.Result
	if deploy.StrictJSON([]byte(raw), &req) != nil || deploy.StrictJSON([]byte(resultRaw), &result) != nil {
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e == nil {
		h.ackResult(ctx, t, req, result)
	}
}
func (h *HTTP) jobUnknown(id, code string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	res, err := h.store.DB.Exec("UPDATE jobs SET status='UNKNOWN',error_code=?,updated_at=? WHERE id=? AND status IN ('QUEUED','RUNNING','UNKNOWN') AND phase<>'CANCELLED'", code, Now(), id)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	_, _ = h.store.DB.Exec("UPDATE node_environments SET error_code=?,updated_at=? WHERE last_job_id=?", code, Now(), id)
	_, _ = h.store.DB.Exec("UPDATE git_repositories SET error_code=?,updated_at=? WHERE last_job_id=?", code, Now(), id)
	_, _ = h.store.DB.Exec("UPDATE plugin_installations SET status='UNKNOWN',error_code=?,updated_at=? WHERE last_job_id=?", code, Now(), id)
	_, _ = h.store.DB.Exec("UPDATE software_instances SET actual_state='UNKNOWN',error_code=?,updated_at=? WHERE last_job_id=?", code, Now(), id)
	_, _ = h.store.DB.Exec("UPDATE software_instances SET installation_status='UNKNOWN' WHERE installation_status='INSTALLING' AND last_job_id=?", id)
}
func (h *HTTP) processRelease(ctx context.Context, id, raw string) {
	var req deploy.Request
	if deploy.StrictJSON([]byte(raw), &req) != nil {
		h.jobUnknown(id, "INPUT_DAMAGED")
		return
	}
	t, e := h.store.readTarget(req.TargetID)
	if e != nil {
		h.jobUnknown(id, "TARGET_MISSING")
		return
	}
	h.store.mu.Lock()
	claim, e := h.store.DB.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED' AND EXISTS(SELECT 1 FROM targets WHERE id=? AND maintenance=0)", Now(), id, req.TargetID)
	h.store.mu.Unlock()
	if e != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n != 1 {
		return
	}
	execCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	var result deploy.Result
	progressCtx, stopProgress := context.WithCancel(execCtx)
	progressDone := make(chan struct{})
	go func() { defer close(progressDone); h.pollProgress(progressCtx, t, req) }()
	e = h.store.callTarget(execCtx, t, req, h.store.artifactPath(req.Candidate.ArtifactID), &result)
	stopProgress()
	<-progressDone
	if e != nil {
		h.logger.Printf("traceId=%s jobId=%s target=UNCONFIRMED", req.TraceID, id)
		h.jobUnknown(id, "TARGET_RESULT_UNKNOWN")
		return
	}
	if e = h.commitResult(id, req, result); e != nil {
		h.jobUnknown(id, "RESULT_COMMIT_FAILED")
		return
	}
	h.ackResult(ctx, t, req, result)
}
