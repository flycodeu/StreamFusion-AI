package controller

import (
	"errors"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// Admission already holds the target/software locks. Recheck the frozen actor
// generation and both entity versions before dispatching a privileged change.
func (h *HTTP) startDockerAccessJob(id string, req deploy.Request) bool {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.store.DB.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	var state, raw, hostVersion, softwareVersion, mode, identity string
	var generation int
	var maintenance bool
	err = tx.QueryRow(`SELECT j.status,j.input_json,a.session_generation,CAST(t.version AS TEXT),t.maintenance,CAST(s.version AS TEXT),s.management_mode,s.identity_hash FROM jobs j JOIN admins a ON a.id=j.actor_id JOIN targets t ON t.id=j.target_id JOIN software_instances s ON s.id=? AND s.last_job_id=j.id WHERE j.id=?`, req.Software.ID, id).Scan(&state, &raw, &generation, &hostVersion, &maintenance, &softwareVersion, &mode, &identity)
	if err != nil || state != "QUEUED" || raw != jsonText(req) {
		return false
	}
	code := ""
	if generation != req.DockerAccess.ActorGeneration {
		code = "DOCKER_ACCESS_ACTOR_CHANGED"
	} else if hostVersion != req.DockerAccess.ExpectedHostVersion || maintenance {
		code = "HOST_CHANGED"
	} else if softwareVersion != req.DockerAccess.ExpectedSoftwareVersion || mode != "MANAGED" || identity != req.Software.IdentityHash {
		code = "SOFTWARE_IDENTITY_DRIFT"
	}
	var locks int
	if err = tx.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=? AND resource_key IN (?,?)", id, "target:"+req.TargetID, "software:"+req.Software.ID).Scan(&locks); err != nil {
		return false
	}
	if locks != 2 {
		code = "DOCKER_ACCESS_LOCK_MISMATCH"
	}
	if code != "" {
		_, err = tx.Exec("UPDATE jobs SET status='FAILED',phase='NOT_STARTED',error_code=?,updated_at=? WHERE id=?", code, Now(), id)
		if err == nil {
			_, err = tx.Exec("DELETE FROM operation_locks WHERE job_id=?", id)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE software_instances SET error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=?", code, Now(), req.Software.ID, id)
		}
		if err == nil {
			err = auditTx(tx, "worker", "SOFTWARE_DOCKER_ACCESS_NOT_STARTED", req.Software.ID, code, req.TraceID)
		}
		if err == nil {
			err = tx.Commit()
		}
		return false
	}
	_, err = tx.Exec("UPDATE jobs SET status='RUNNING',phase='TARGET_EXECUTION',updated_at=? WHERE id=? AND status='QUEUED'", Now(), id)
	if err != nil {
		return false
	}
	return tx.Commit() == nil
}

func dockerAccount(o deploy.DockerAccessObservation, name string) *deploy.DockerAccessAccount {
	for _, a := range o.Accounts {
		if a.Name == name {
			return &a
		}
	}
	return nil
}
func dockerOtherGroups(a *deploy.DockerAccessAccount) map[string]string {
	groups := map[string]string{}
	for _, g := range a.Groups {
		if g.Name != "docker" {
			groups[g.Name] = g.GID
		}
	}
	return groups
}
func verifyDockerAccessResult(req deploy.Request, result deploy.Result) error {
	c := req.DockerAccess
	e := result.DockerAccess
	if c == nil || e == nil || c.Validate() != nil || e.Observation.Validate() != nil || result.Phase != "DOCKER_ACCESS_UPDATED" {
		return errors.New("DOCKER_ACCESS_RESULT_MISMATCH")
	}
	before, after := dockerAccount(c.Expected, c.Account), dockerAccount(e.Observation, c.Account)
	if before == nil || after == nil || e.Account != c.Account || e.UID != before.UID || e.Action != c.Action || e.Member != (c.Action == "GRANT") || after.Member != e.Member || e.Observation.SoftwareID != req.Software.ID || e.Observation.EngineIdentityHash != req.Software.IdentityHash || e.Observation.DockerGID != c.Expected.DockerGID || e.Observation.UIDMin != c.Expected.UIDMin || e.Observation.UIDMax != c.Expected.UIDMax || after.UID != before.UID || after.GID != before.GID || after.Shell != before.Shell || after.PrimaryGroup != before.PrimaryGroup || !after.Eligible || deploy.Digest(dockerOtherGroups(before)) != deploy.Digest(dockerOtherGroups(after)) {
		return errors.New("DOCKER_ACCESS_RESULT_MISMATCH")
	}
	if c.Action == "GRANT" && !e.Observation.Socket.Ready {
		return errors.New("DOCKER_ACCESS_RESULT_MISMATCH")
	}
	if _, err := time.Parse(time.RFC3339Nano, e.ObservedAt); err != nil {
		return errors.New("DOCKER_ACCESS_RESULT_MISMATCH")
	}
	return nil
}
