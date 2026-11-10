package controller

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// Only a signed, explicitly declared configuration operation may replace a
// managed service identity. Other management results retain the original check.
func validateManagementBindingResult(req deploy.Request, result deploy.Result) error {
	op, err := req.Management.Definition.ManagementOperation(req.Management.OperationKey)
	if err != nil || req.Software == nil || result.Software == nil {
		return errors.New("MANAGEMENT_EVIDENCE_MISMATCH")
	}
	old, fact := req.Software, result.Software
	if !op.Rebind {
		if result.Installation != nil || fact.Kind != old.Kind || fact.Locator != old.Locator || fact.IdentityHash != old.IdentityHash {
			return errors.New("MANAGEMENT_EVIDENCE_MISMATCH")
		}
		return nil
	}
	if result.Installation == nil || old.Ownership == nil || fact.Kind != old.Kind || fact.Locator != result.Installation.Locator || fact.IdentityHash != result.Installation.IdentityHash || result.Installation.Health != "READY" || strings.TrimSpace(result.Installation.ObservedVersion) == "" || (fact.State != "RUNNING" && fact.State != "STOPPED") {
		return errors.New("MANAGEMENT_REBIND_UNCONFIRMED")
	}
	if err := req.Management.ValidateRebindEvidence(*result.Installation); err != nil {
		return err
	}
	if fact.Kind == "SYSTEMD" && (fact.Locator != old.Locator || result.Installation.BinaryPath != old.Ownership.BinaryPath) || fact.Kind == "DOCKER" && result.Installation.ImageID != old.Ownership.ImageID {
		return errors.New("MANAGEMENT_REBIND_OWNERSHIP_MISMATCH")
	}
	if _, err := time.Parse(time.RFC3339Nano, result.Installation.ObservedAt); err != nil {
		return errors.New("MANAGEMENT_REBIND_UNCONFIRMED")
	}
	return nil
}

func commitManagementBinding(tx *sql.Tx, req deploy.Request, result deploy.Result) (sql.Result, error) {
	op, _ := req.Management.Definition.ManagementOperation(req.Management.OperationKey)
	if result.Status != "SUCCEEDED" || !op.Rebind {
		return tx.Exec("UPDATE software_instances SET error_code=?,version=version+1,updated_at=? WHERE id=? AND last_job_id=? AND binding_revision=? AND identity_hash=?", result.ErrorCode, Now(), req.Software.ID, req.JobID, req.Software.Version, req.Software.IdentityHash)
	}
	installed := *req.Management.Installation
	installed.BindingRevision++
	fact := result.Software
	host, err := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", req.TargetID))
	if err != nil {
		return nil, err
	}
	// The installation job continues to own the program/image. The configuration
	// job and its original/new evidence stay in the existing durable job history.
	return tx.Exec(`UPDATE software_instances SET locator=?,identity_hash=?,binding_revision=?,installation_json=?,installation_evidence_json=?,actual_state=?,observation_json=?,observed_at=?,observation_identity=?,attempt_json=?,observation_revision=observation_revision+1,error_code='',version=version+1,updated_at=? WHERE id=? AND last_job_id=? AND binding_revision=? AND identity_hash=?`, fact.Locator, fact.IdentityHash, installed.BindingRevision, jsonText(installed), jsonText(result.Installation), fact.State, jsonText(softwareObservation{SoftwareObservation: fact, Source: "EXECUTOR"}), fact.ObservedAt, observationIdentity(host), jsonText(observationAttempt{"READY", fact.ObservedAt, req.TraceID}), Now(), req.Software.ID, req.JobID, req.Software.Version, req.Software.IdentityHash)
}
