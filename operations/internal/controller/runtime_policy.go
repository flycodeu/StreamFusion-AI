package controller

import (
	"database/sql"
	"errors"
	"reflect"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/runtimepolicy"
)

func validateRuntimePolicyResult(req deploy.Request, result deploy.Result, kind string) error {
	state := result.RuntimePolicy
	if req.RuntimePolicy == nil || req.RuntimePolicy.Version != runtimepolicy.Version || state == nil || state.Configured == nil || state.Actual == nil || state.Configured.Validate(kind) != nil || state.Actual.Validate(kind) != nil {
		return errors.New("RUNTIME_POLICY_UNCONFIRMED")
	}
	if _, err := time.Parse(time.RFC3339Nano, state.ObservedAt); err != nil {
		return errors.New("RUNTIME_POLICY_UNCONFIRMED")
	}
	if req.RuntimePolicy.Policy != nil && !reflect.DeepEqual(req.RuntimePolicy.Policy, state.Configured) {
		return errors.New("RUNTIME_POLICY_MISMATCH")
	}
	effective := *state.Configured
	if state.Held {
		effective.AutoStart = false
		if kind == "DOCKER" {
			effective.Restart, effective.MaxRetries = "no", 0
		}
	}
	if !reflect.DeepEqual(&effective, state.Actual) {
		return errors.New("RUNTIME_POLICY_MISMATCH")
	}
	if (req.Action == "SOFTWARE_STOP" || req.Action == "APP_STOP") && !state.Held || (req.Action == "SOFTWARE_START" || req.Action == "SOFTWARE_RESTART" || req.Action == "APP_START" || req.Action == "APP_RESTART") && state.Held {
		return errors.New("RUNTIME_POLICY_STOP_UNCONFIRMED")
	}
	return nil
}

func validateRuntimePolicyBinding(req deploy.Request, result deploy.Result) error {
	if result.RuntimePolicyBinding == nil {
		return nil
	}
	if req.RuntimePolicy == nil || req.Software == nil || result.Software == nil {
		return errors.New("RUNTIME_POLICY_BINDING_INVALID")
	}
	old, next := *req.Software, *result.RuntimePolicyBinding
	if next.Version != old.Version+1 || !deploy.Hex.MatchString(next.IdentityHash) || result.Software.IdentityHash != next.IdentityHash || result.Software.Kind != next.Kind || result.Software.Locator != next.Locator {
		return errors.New("RUNTIME_POLICY_BINDING_INVALID")
	}
	next.Version, next.IdentityHash = old.Version, old.IdentityHash
	if !reflect.DeepEqual(old, next) {
		return errors.New("RUNTIME_POLICY_BINDING_INVALID")
	}
	return nil
}

func commitRuntimePolicyBinding(tx *sql.Tx, req deploy.Request, result deploy.Result) error {
	if result.Status != "SUCCEEDED" || result.RuntimePolicyBinding == nil {
		return nil
	}
	i, err := scanSoftware(tx.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", req.Software.ID))
	if err != nil {
		return err
	}
	next := result.RuntimePolicyBinding
	var installed, evidence any
	if i.Installation != nil {
		copy := *i.Installation
		copy.BindingRevision = next.Version
		installed = jsonText(copy)
	}
	if i.InstallationEvidence != nil {
		copy := *i.InstallationEvidence
		copy.BindingRevision, copy.IdentityHash = next.Version, next.IdentityHash
		evidence = jsonText(copy)
	}
	res, err := tx.Exec("UPDATE software_instances SET binding_revision=?,identity_hash=?,installation_json=coalesce(?,installation_json),installation_evidence_json=coalesce(?,installation_evidence_json) WHERE id=? AND last_job_id=? AND binding_revision=? AND identity_hash=?", next.Version, next.IdentityHash, installed, evidence, req.Software.ID, req.JobID, req.Software.Version, req.Software.IdentityHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("RUNTIME_POLICY_BINDING_CONFLICT")
	}
	return nil
}
