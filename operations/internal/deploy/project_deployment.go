package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

func (x *executor) executeProjectDeployment(req Request, output io.Writer) error {
	p := req.ProjectDeployment
	if err := x.verifyCommandRequest(req); err != nil {
		return err
	}
	if err := validProjectDeployment(p, req.Action); err != nil {
		return err
	}
	if err := validProjectSecrets(p, req.Action, x.secrets); err != nil {
		return err
	}
	deadline, _ := time.Parse(time.RFC3339, req.DeadlineAt)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	r := ProjectDeployResult{Status: "SUCCEEDED", Phase: "VERIFIED"}
	send := func(err error) error {
		if err != nil {
			r.Status = "FAILED"
			r.ErrorCode = err.Error()
			if !regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`).MatchString(r.ErrorCode) {
				r.ErrorCode = "PROJECT_TARGET_FAILED"
			}
			if r.ErrorCode == "PROJECT_RECOVERY_REQUIRED" {
				r.Status = "UNKNOWN"
			}
		}
		r.ObservedAt = stamp()
		return json.NewEncoder(output).Encode(r)
	}
	lock, err := targetLock(x.root)
	if err != nil {
		return send(err)
	}
	defer lock.Close()
	base := filepath.Join(x.root, "project-apps", p.ID)
	s := newProjectStore(*p, base)
	active, err := s.readActive()
	if err != nil {
		return send(err)
	}
	if req.Action == ProjectObserve {
		active, err = s.reconcile(ctx, active)
	} else if _, pendingErr := s.readPending(); !os.IsNotExist(pendingErr) {
		err = projectRecoveryRequired
	}
	if err != nil {
		r.ReleaseID, r.Health = s.observePending(ctx, active)
		return send(err)
	}
	r.ReleaseID = active.ReleaseID
	switch req.Action {
	case ProjectProbe:
		r.Phase = "PREFLIGHT"
		if p.ExpectedRelease != active.ReleaseID {
			return send(errors.New("PROJECT_RELEASE_CHANGED"))
		}
		return send(projectCheckTools(ctx, *p))
	case ProjectObserve:
		if active.ReleaseID == "" {
			r.Health = "NOT_DEPLOYED"
			return send(nil)
		}
		if s.health(ctx, active) {
			r.Health = "READY"
		} else {
			r.Health = "UNHEALTHY"
		}
		return send(nil)
	case ProjectLogs:
		if active.ReleaseID == "" {
			return send(errors.New("PROJECT_NOT_DEPLOYED"))
		}
		r.Phase = "LOGS"
		if p.Kind == "JAVA" {
			r.Output, err = s.javaLogs(ctx, active)
		} else {
			r.Output, err = projectNginxLogs(p.ID)
		}
		if err != nil {
			r.Output = ""
			return send(errors.New("PROJECT_LOGS_UNAVAILABLE"))
		}
		r.Output = RedactSoftwareLogs(r.Output)
		return send(nil)
	case ProjectApply, ProjectRollback:
		r.Phase = "TARGET_EXECUTION"
		if p.ExpectedRelease != active.ReleaseID {
			return send(errors.New("PROJECT_RELEASE_CHANGED"))
		}
		if p.ReleaseID == active.ReleaseID {
			return send(errors.New("PROJECT_RELEASE_CHANGED"))
		}
		var candidate projectActive
		if req.Action == ProjectRollback {
			candidate, err = s.staged(p.ReleaseID)
			if os.IsNotExist(err) {
				err = errors.New("PROJECT_ROLLBACK_RELEASE_MISSING")
			}
		} else {
			if err = projectCheckTools(ctx, *p); err == nil {
				candidate, err = s.stage(x.installationInput, *p, x.secrets)
			}
		}
		if err != nil {
			return send(err)
		}
		if req.Action == ProjectRollback {
			if err = projectCheckTools(ctx, candidate.Spec); err != nil {
				return send(err)
			}
		}
		if err = s.activate(ctx, candidate, active, &r.Output); err != nil {
			if err.Error() == "PROJECT_RECOVERY_REQUIRED" {
				observation, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				r.ReleaseID, r.Health = s.observePending(observation, active)
			}
			return send(err)
		}
		r.ReleaseID = p.ReleaseID
		r.Health = "READY"
		return send(nil)
	default:
		return send(errors.New("PROJECT_ACTION_INVALID"))
	}
}
