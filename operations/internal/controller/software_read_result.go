package controller

import (
	"context"
	"errors"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// Managed reads return the same Result envelope as other target operations.
// Unwrap only after checking the host and exact recorded software identity;
// a successful reply from another service must never become this one's log.
func (h *HTTP) readManagedSoftware(ctx context.Context, t target, i softwareInstance, action string) (*deploy.SoftwareObservation, error) {
	if action != "SOFTWARE_OBSERVE" && action != "SOFTWARE_LOGS" {
		return nil, errors.New("EXECUTOR_RESPONSE_INVALID")
	}
	var result deploy.Result
	if err := h.store.callTarget(ctx, t, deploy.Request{Action: action, Software: softwareRequest(i)}, "", &result); err != nil {
		return nil, err
	}
	if result.ControllerID != h.store.controllerID() || result.TargetID != t.ID {
		return nil, errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if result.ProtocolVersion != 1 || result.ExecutorVersion != deploy.Version || result.Status != "SUCCEEDED" || result.Phase != "SOFTWARE_OBSERVED" || result.ErrorCode != "" || result.Software == nil || result.JobID != "" || result.InputHash != "" || result.Ack {
		return nil, errors.New("EXECUTOR_RESPONSE_INVALID")
	}
	fact := result.Software
	if fact.Kind != i.RuntimeKind || fact.Locator != i.Locator || i.IdentityHash == "" || fact.IdentityHash != i.IdentityHash {
		return nil, errors.New("SOFTWARE_IDENTITY_DRIFT")
	}
	if _, err := time.Parse(time.RFC3339Nano, fact.ObservedAt); err != nil {
		return nil, errors.New("EXECUTOR_RESPONSE_INVALID")
	}
	switch fact.State {
	case "RUNNING", "STOPPED", "FAILED", "UNKNOWN", "INSTALLED", "PAUSED":
	default:
		return nil, errors.New("EXECUTOR_RESPONSE_INVALID")
	}
	fact.Log = deploy.RedactSoftwareLogs(fact.Log)
	return fact, nil
}
