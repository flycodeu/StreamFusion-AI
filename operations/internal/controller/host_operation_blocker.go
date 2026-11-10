package controller

import (
	"database/sql"
	"errors"
	"net/http"
)

type hostOperation struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
	Ack    bool   `json:"targetAckPending"`
}

func hostOperationLock(q interface{ QueryRow(string, ...any) *sql.Row }, hostID string) (hostOperation, error) {
	var job hostOperation
	err := q.QueryRow(`SELECT j.id,j.kind,j.status,j.phase,j.target_ack_pending
		FROM operation_locks l JOIN jobs j ON j.id=l.job_id WHERE l.resource_key=?`, "target:"+hostID).
		Scan(&job.ID, &job.Kind, &job.Status, &job.Phase, &job.Ack)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return job, err
}

func (j hostOperation) blocksInstall() bool {
	return j.ID != "" && (j.Kind != "SOFTWARE_INSTALL" || j.Status != "QUEUED" && j.Status != "RUNNING")
}

func (h *HTTP) operationBlocked(w http.ResponseWriter, r *http.Request, job hostOperation) {
	message := "主机正在执行其他操作，请完成后重试"
	if job.Status == "UNKNOWN" || job.Ack {
		message = "主机有结果待确认的操作，请核对原作业后重试"
	}
	h.reply(w, r, http.StatusConflict, "OPERATION_LOCKED", message, job)
}
