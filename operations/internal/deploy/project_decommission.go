package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A pending marker authorizes retry after any interruption; the completed
// receipt lets a controller retry when only the target's response was lost.
type projectRetirement struct {
	ReleaseID string `json:"releaseId"`
	Completed bool   `json:"completed"`
}

func (s projectStore) retirement() string { return filepath.Join(s.base, "decommission.json") }

func (s projectStore) readRetirement() (projectRetirement, error) {
	var record projectRetirement
	err := projectReadRecord(s.retirement(), &record)
	if err == nil && !ID.MatchString(record.ReleaseID) {
		err = projectRecoveryRequired
	}
	return record, err
}

func (s projectStore) decommissionPending(active projectActive) bool {
	record, err := s.readRetirement()
	return !os.IsNotExist(err) && (err != nil || !record.Completed || active.ReleaseID == record.ReleaseID)
}

func (s projectStore) decommission(ctx context.Context, active projectActive, releaseID string) error {
	record, err := s.readRetirement()
	fresh := os.IsNotExist(err) || err == nil && record.Completed &&
		active.ReleaseID != "" && active.ReleaseID != record.ReleaseID
	if err != nil && !os.IsNotExist(err) {
		return projectRecoveryRequired
	}
	// A stale retry must never act on a subsequently published version.
	if active.ReleaseID != "" && active.ReleaseID != releaseID || !fresh && record.ReleaseID != releaseID ||
		fresh && active.ReleaseID == "" {
		return errors.New("PROJECT_RELEASE_CHANGED")
	}
	found, err := s.currentControl()
	if err != nil {
		return projectControlDrift
	}
	if !fresh && record.Completed && active.ReleaseID == "" {
		// Recheck only the managed file. A new unmanaged file with this
		// name is drift and must never be removed on a second request.
		if len(found) != 0 {
			return projectControlDrift
		}
		if projectSyncDir(s.base) != nil {
			return projectRecoveryRequired
		}
		return s.verifyRetired(ctx, releaseID)
	}
	release := active
	if release.ReleaseID == "" {
		release, err = s.staged(releaseID)
		if err != nil {
			return projectRecoveryRequired
		}
	}
	wanted, err := s.body(release)
	if err != nil || len(found) != 0 && !s.matchesControl(found, wanted, release) ||
		fresh && len(found) == 0 {
		return projectControlDrift
	}
	if err = s.safeLogs(); err != nil {
		return err
	}
	if s.kind == "JAVA" && len(found) != 0 {
		if err := s.verifyOwnedUnit(ctx, release, s.dir(releaseID), s.control); err != nil {
			if !fresh || !errors.Is(err, projectControlDrift) {
				return projectRecoveryRequired
			}
			return projectControlDrift
		}
	}
	if fresh {
		record = projectRetirement{ReleaseID: releaseID}
		if err := atomicJSON(s.retirement(), record); err != nil {
			return projectRecoveryRequired
		}
	}
	if len(found) != 0 {
		if s.kind == "JAVA" {
			if err := s.removeUnit(ctx, s.id); err != nil {
				return projectRecoveryRequired
			}
		}
		// Recheck ownership right before removal, including after the
		// potentially long systemd stop.
		now, err := s.currentControl()
		if err != nil || !s.matchesControl(now, wanted, release) {
			return projectRecoveryRequired
		}
		if err := os.Remove(s.control); err != nil {
			return projectRecoveryRequired
		}
	}
	if projectSyncDir(filepath.Dir(s.control)) != nil || s.reload(ctx, release.Spec, true) != nil {
		return projectRecoveryRequired
	}
	if err := s.verifyRetired(ctx, releaseID); err != nil {
		return err
	}
	// Report success only after the managed configuration is durably removed
	// and the reload succeeded. If the final receipt write fails, the same
	// release can resume from the retained pending marker and artifact.
	if err := os.Remove(s.state()); err != nil && !os.IsNotExist(err) {
		return projectRecoveryRequired
	}
	if projectSyncDir(s.base) != nil {
		return projectRecoveryRequired
	}
	record.Completed = true
	if err := atomicJSON(s.retirement(), record); err != nil {
		return projectRecoveryRequired
	}
	return nil
}

func (s projectStore) verifyRetired(ctx context.Context, releaseID string) error {
	if ctx.Err() != nil {
		return projectRecoveryRequired
	}
	if s.kind == "JAVA" {
		if s.verifyStoppedUnit(ctx, s.id) != nil {
			return projectRecoveryRequired
		}
	} else {
		release, err := s.staged(releaseID)
		if err != nil {
			return projectRecoveryRequired
		}
		// Nginx reload signals the master before the new workers bind.
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		tick := time.NewTicker(300 * time.Millisecond)
		defer tick.Stop()
		for s.stillServing(probeCtx, release.Spec) {
			select {
			case <-probeCtx.Done():
				return projectRecoveryRequired
			case <-tick.C:
			}
		}
		if probeCtx.Err() != nil {
			return projectRecoveryRequired
		}
	}
	return nil
}

// Refuse an override or a foreign service with the same unit name. The
// protected on-disk fragment was already verified before calling systemd.
func projectVerifyOwnedUnit(ctx context.Context, release projectActive, dir, control string) error {
	unit := projectUnit(release.Spec.ID)
	data, err := projectCommand(ctx, "/usr/bin/systemctl", "show",
		"--property=FragmentPath,DropInPaths,ActiveState,MainPID", unit)
	if err != nil {
		return err
	}
	values := projectUnitProperties(data)
	if values["FragmentPath"] != control || values["DropInPaths"] != "" ||
		(values["ActiveState"] == "active" || values["MainPID"] != "" && values["MainPID"] != "0") &&
			!projectJavaUnitRuns(ctx, release.Spec.ID, dir) {
		return projectControlDrift
	}
	return nil
}

func projectVerifyStoppedUnit(ctx context.Context, id string) error {
	data, err := projectCommand(ctx, "/usr/bin/systemctl", "show",
		"--property=FragmentPath,ActiveState,MainPID,LoadState", projectUnit(id))
	if err != nil {
		return err
	}
	values := projectUnitProperties(data)
	if values["LoadState"] != "not-found" || values["FragmentPath"] != "" ||
		values["ActiveState"] != "inactive" && values["ActiveState"] != "failed" ||
		values["MainPID"] != "0" {
		return projectRecoveryRequired
	}
	return nil
}

func projectUnitProperties(data string) map[string]string {
	props := make(map[string]string)
	for line := range strings.SplitSeq(data, "\n") {
		if key, value, found := strings.Cut(line, "="); found {
			props[key] = value
		}
	}
	return props
}
