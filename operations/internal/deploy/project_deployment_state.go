package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var projectRecoveryRequired = errors.New("PROJECT_RECOVERY_REQUIRED")
var projectControlDrift = errors.New("PROJECT_SITE_OR_UNIT_OWNERSHIP_MISMATCH")
var projectInvocationID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type projectActive struct {
	ReleaseID string            `json:"releaseId"`
	Spec      ProjectDeployment `json:"spec"`
}

type projectPending struct {
	Candidate string `json:"candidate"`
	Baseline  string `json:"baseline"`
}

type projectStore struct {
	id, kind, base, releases, logs, control string
	reload                                  func(context.Context, ProjectDeployment, bool) error
	healthy                                 func(context.Context, ProjectDeployment, string) bool
	removeUnit                              func(context.Context, string) error
	verifyOwnedUnit                         func(context.Context, projectActive, string, string) error
	verifyStoppedUnit                       func(context.Context, string) error
	stillServing                            func(context.Context, ProjectDeployment) bool
}

func newProjectStore(p ProjectDeployment, base string) projectStore {
	s := projectStore{
		id: p.ID, kind: p.Kind, base: base, releases: filepath.Join("/var/lib/sfops-projects/releases", p.ID),
		control: projectControlFile(p), reload: projectReload, healthy: projectHealth,
		verifyOwnedUnit:   projectVerifyOwnedUnit,
		verifyStoppedUnit: projectVerifyStoppedUnit,
		removeUnit: func(ctx context.Context, id string) error {
			_, err := projectCommand(ctx, "/usr/bin/systemctl", "disable", "--now", projectUnit(id))
			return err
		},
	}
	if p.Kind == "STATIC" {
		s.releases = filepath.Join("/var/www/sfops", p.ID)
		s.logs = filepath.Join("/var/log/nginx/sfops", p.ID)
		s.stillServing = projectStaticReleaseServed
	}
	return s
}

func (s projectStore) dir(id string) string { return filepath.Join(s.releases, id) }
func (s projectStore) state() string        { return filepath.Join(s.base, "active.json") }
func (s projectStore) pending() string      { return filepath.Join(s.base, "pending.json") }

func projectReadRecord(filename string, out any) error {
	b, err := projectReadSmall(filename, 128<<10)
	if err != nil {
		return err
	}
	return StrictJSON(b, out)
}

func (s projectStore) valid(record projectActive, id string) bool {
	return record.ReleaseID == id && record.Spec.ReleaseID == id && ID.MatchString(id) &&
		record.Spec.ID == s.id && record.Spec.Kind == s.kind &&
		record.Spec.ExpectedRelease == "" && len(record.Spec.Variables) == 0 && len(record.Spec.Files) == 0 &&
		validProjectDeployment(&record.Spec, ProjectApply) == nil
}

func (s projectStore) readActive() (projectActive, error) {
	var active projectActive
	if err := projectReadRecord(s.state(), &active); os.IsNotExist(err) {
		return projectActive{}, nil
	} else if err != nil || !s.valid(active, active.ReleaseID) {
		return projectActive{}, errors.New("PROJECT_ACTIVE_STATE_INVALID")
	}
	return active, nil
}

func (s projectStore) readPending() (projectPending, error) {
	var p projectPending
	if err := projectReadRecord(s.pending(), &p); err != nil {
		return projectPending{}, err
	}
	if !ID.MatchString(p.Candidate) || p.Baseline != "" && !ID.MatchString(p.Baseline) || p.Candidate == p.Baseline {
		return projectPending{}, projectRecoveryRequired
	}
	return p, nil
}

func (s projectStore) resolveActive(ctx context.Context, active projectActive, action string, spec ProjectDeployment) (projectActive, error) {
	if action != ProjectDecommission && s.decommissionPending(active) {
		return active, projectRecoveryRequired
	}
	pending, err := s.readPending()
	if os.IsNotExist(err) {
		if action == ProjectRecover && active.ReleaseID != spec.ReleaseID && active.ReleaseID != spec.ExpectedRelease {
			return active, errors.New("PROJECT_RELEASE_CHANGED")
		}
		return active, nil
	}
	if action == ProjectRecover && err == nil && pending.Candidate == spec.ReleaseID && pending.Baseline == spec.ExpectedRelease {
		return s.reconcile(ctx, active)
	}
	return active, projectRecoveryRequired
}

func (s projectStore) staged(id string) (projectActive, error) {
	if !ID.MatchString(id) {
		return projectActive{}, errors.New("PROJECT_ROLLBACK_RELEASE_INVALID")
	}
	dir := s.dir(id)
	var record projectActive
	if err := projectReadRecord(filepath.Join(dir, ".release.json"), &record); err != nil {
		return projectActive{}, err
	}
	if !s.valid(record, id) {
		return projectActive{}, errors.New("PROJECT_STAGED_RELEASE_INVALID")
	}
	files := []string{filepath.Join(dir, "www", "index.html")}
	if s.kind == "JAVA" {
		files = []string{filepath.Join(dir, "app.jar"), filepath.Join(dir, "runtime.env")}
		if err := secureAncestors(filepath.Join(dir, "config")); err != nil {
			return projectActive{}, errors.New("PROJECT_STAGED_RELEASE_INVALID")
		}
	}
	for _, file := range files {
		if err := secureAncestors(file); err != nil {
			return projectActive{}, errors.New("PROJECT_STAGED_RELEASE_INVALID")
		}
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || (filepath.Base(file) != "runtime.env" && info.Size() == 0) {
			return projectActive{}, errors.New("PROJECT_STAGED_RELEASE_INVALID")
		}
	}
	return record, nil
}

func (s projectStore) body(release projectActive) ([]byte, error) {
	if release.ReleaseID == "" {
		return nil, nil
	}
	if !s.valid(release, release.ReleaseID) {
		return nil, errors.New("PROJECT_ACTIVE_STATE_INVALID")
	}
	dir := s.dir(release.ReleaseID)
	if s.kind == "JAVA" {
		return []byte(projectJavaUnit(release.Spec, dir, false)), nil
	}
	body, err := release.Spec.NginxConfig(filepath.Join(dir, "www"), s.logs)
	return []byte(body), err
}

func (s projectStore) currentControl() ([]byte, error) {
	body, err := projectReadSmall(s.control, 64<<10)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err == nil && len(body) == 0 {
		return nil, errors.New("PROJECT_SITE_OR_UNIT_OWNERSHIP_MISMATCH")
	}
	return body, err
}

func (s projectStore) matchesControl(found, wanted []byte, release projectActive) bool {
	if bytes.Equal(found, wanted) {
		return true
	}
	if release.ReleaseID == "" {
		return false
	}
	// Match only known templates from earlier executors.
	dir := s.dir(release.ReleaseID)
	if s.kind == "JAVA" {
		return string(found) == projectJavaUnit(release.Spec, dir, true)
	}
	if release.Spec.Site != nil && release.Spec.Site.HealthPath == StreamFusionSiteHealthPath {
		legacy, err := release.Spec.nginxConfig(filepath.Join(dir, "www"), s.logs, false)
		return err == nil && string(found) == legacy
	}
	return false
}

func (s projectStore) health(ctx context.Context, release projectActive) bool {
	return release.ReleaseID != "" && s.healthy(ctx, release.Spec, s.dir(release.ReleaseID))
}

func (s projectStore) observe(ctx context.Context, active projectActive) (string, error) {
	found, err := s.currentControl()
	expected, bodyErr := s.body(active)
	if err != nil || bodyErr != nil || !s.matchesControl(found, expected, active) {
		return "UNKNOWN", projectControlDrift
	}
	if active.ReleaseID == "" {
		return "NOT_DEPLOYED", nil
	}
	if s.health(ctx, active) {
		return "READY", nil
	}
	return "UNHEALTHY", nil
}

func (s projectStore) javaLogs(ctx context.Context, active projectActive) (string, error) {
	secrets, err := projectLogSecrets(s.dir(active.ReleaseID))
	if err != nil {
		return "", err
	}
	// Select only the current systemd invocation. Earlier failed releases may
	// have had credentials which do not occur in the active environment.
	id, err := projectCommand(ctx, "/usr/bin/systemctl", "show", "--property=InvocationID", "--value", projectUnit(s.id))
	id = strings.TrimSpace(id)
	if err != nil || !projectInvocationID.MatchString(id) || id == strings.Repeat("0", 32) {
		return "", errors.New("PROJECT_LOGS_UNAVAILABLE")
	}
	if !projectJavaUnitRuns(ctx, active.Spec.ID, s.dir(active.ReleaseID)) {
		return "", errors.New("PROJECT_LOGS_UNAVAILABLE")
	}
	output, err := projectCommand(ctx, "/usr/bin/journalctl", "--unit", projectUnit(s.id), "--no-pager",
		"--lines=120", "--output=short-iso", "_SYSTEMD_INVOCATION_ID="+id)
	if err != nil {
		return "", err
	}
	return redactProjectLogs(output, secrets), nil
}

func (s projectStore) stage(archive io.Reader, p ProjectDeployment, secrets map[string]string) (projectActive, error) {
	var result projectActive
	if !projectSafePath(s.base) || !projectSafePath(s.releases) || archive == nil {
		return result, errors.New("PROJECT_RUNTIME_PATH_UNSAFE")
	}
	if err := projectSecureDir(s.base, 0755); err != nil {
		return result, err
	}
	if err := projectSecureDir(s.releases, 0755); err != nil {
		return result, err
	}
	if _, err := os.Lstat(s.dir(p.ReleaseID)); !os.IsNotExist(err) {
		return result, errors.New("PROJECT_RELEASE_PATH_EXISTS")
	}
	dir, err := os.MkdirTemp(s.releases, ".staging-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	artifact := filepath.Join(dir, "download")
	if err = projectReceiveArtifact(archive, artifact, p); err != nil {
		return result, err
	}
	if p.Kind == "JAVA" {
		if err = projectWriteJava(p, dir, artifact, secrets); err != nil {
			return result, err
		}
	} else {
		web := filepath.Join(dir, "www")
		if err = projectSecureDir(web, 0755); err != nil {
			return result, err
		}
		if err = extractProjectWebsite(artifact, web); err != nil {
			return result, err
		}
		if err = os.Remove(artifact); err != nil {
			return result, err
		}
		if err = projectSecureDir(s.logs, 0755); err != nil {
			return result, err
		}
	}
	p.ExpectedRelease = ""
	p.Variables = nil
	p.Files = nil
	result = projectActive{p.ReleaseID, p}
	if err = atomicJSON(filepath.Join(dir, ".release.json"), result); err != nil {
		return result, err
	}
	if err = os.Chmod(dir, 0755); err != nil {
		return result, err
	}
	if err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return projectSyncDir(path)
		}
		return nil
	}); err != nil {
		return result, err
	}
	if err = os.Rename(dir, s.dir(p.ReleaseID)); err != nil {
		return result, err
	}
	return result, projectSyncDir(s.releases)
}

func (s projectStore) safeLogs() error {
	if s.kind != "STATIC" {
		return nil
	}
	if err := secureAncestors(s.logs); err != nil {
		return err
	}
	for _, name := range []string{"access.log", "error.log"} {
		if err := validateProjectLogFile(filepath.Join(s.logs, name)); err != nil {
			return err
		}
	}
	return nil
}

func (s projectStore) clearPending() error {
	if err := os.Remove(s.pending()); err != nil {
		return err
	}
	return projectSyncDir(s.base)
}

// For an uncommitted publication, only restore known targets. A manual edit to
// the managed file is drift and is never silently overwritten during recovery.
func (s projectStore) restore(ctx context.Context, candidate, baseline projectActive, current, candidateBody, baselineBody []byte) error {
	if err := s.safeLogs(); err != nil {
		return err
	}
	isBaseline := s.matchesControl(current, baselineBody, baseline)
	if len(current) == 0 && baseline.ReleaseID != "" || !isBaseline && !s.matchesControl(current, candidateBody, candidate) {
		return projectRecoveryRequired
	}
	if baseline.ReleaseID != "" {
		if isBaseline && s.health(ctx, baseline) {
			return s.clearPending()
		}
		if err := projectAtomicFile(s.control, baselineBody, 0644); err != nil {
			return err
		}
		if err := s.reload(ctx, baseline.Spec, false); err != nil {
			return err
		}
		if err := s.waitHealthy(ctx, baseline); err != nil {
			return err
		}
	} else {
		if len(current) != 0 {
			if s.kind == "JAVA" {
				if err := s.removeUnit(ctx, s.id); err != nil {
					return err
				}
			}
			if err := os.Remove(s.control); err != nil {
				return err
			}
		}
		// Repeat reload after a crash between removing the file and reloading it.
		if err := s.reload(ctx, candidate.Spec, true); err != nil {
			return err
		}
	}
	return s.clearPending()
}

func (s projectStore) reconcile(ctx context.Context, active projectActive) (projectActive, error) {
	p, err := s.readPending()
	if os.IsNotExist(err) {
		return active, nil
	}
	if err != nil || (active.ReleaseID != p.Candidate && active.ReleaseID != p.Baseline) {
		return active, projectRecoveryRequired
	}
	candidate, err := s.staged(p.Candidate)
	if err != nil {
		return active, projectRecoveryRequired
	}
	target, err := s.body(candidate)
	if err != nil {
		return active, projectRecoveryRequired
	}
	current, err := s.currentControl()
	if err != nil {
		return active, projectRecoveryRequired
	}
	if active.ReleaseID == p.Candidate {
		// active.json was made durable before removing the journal. The
		// candidate already committed; never turn a missing reply into undo.
		if !s.matchesControl(current, target, candidate) || projectSyncDir(s.base) != nil || s.clearPending() != nil {
			return active, projectRecoveryRequired
		}
		return active, nil
	}
	old, err := s.body(active)
	if err != nil || s.restore(ctx, candidate, active, current, target, old) != nil {
		return active, projectRecoveryRequired
	}
	return active, nil
}

func (s projectStore) observePending(ctx context.Context, active projectActive) (string, string) {
	p, err := s.readPending()
	if err == nil {
		for _, id := range []string{p.Candidate, p.Baseline} {
			if id == "" {
				continue
			}
			if release, err := s.staged(id); err == nil && s.health(ctx, release) {
				return id, "READY"
			}
		}
	}
	if s.health(ctx, active) {
		return active.ReleaseID, "READY"
	}
	return "", "UNKNOWN"
}

func (s projectStore) waitHealthy(ctx context.Context, release projectActive) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for !s.health(ctx, release) {
		select {
		case <-ctx.Done():
			return errors.New("PROJECT_HEALTH_CHECK_FAILED")
		case <-tick.C:
		}
	}
	return nil
}

func (s projectStore) activate(ctx context.Context, candidate, baseline projectActive, failureLog *string) error {
	if err := s.safeLogs(); err != nil {
		return err
	}
	found, err := s.currentControl()
	if err != nil {
		return err
	}
	old, err := s.body(baseline)
	if err != nil || !s.matchesControl(found, old, baseline) {
		return errors.New("PROJECT_SITE_OR_UNIT_OWNERSHIP_MISMATCH")
	}
	body, err := s.body(candidate)
	if err != nil {
		return err
	}
	if err = atomicJSON(s.pending(), projectPending{candidate.ReleaseID, baseline.ReleaseID}); err != nil {
		if _, probe := os.Lstat(s.pending()); !os.IsNotExist(probe) {
			return projectRecoveryRequired
		}
		return err
	}
	recoverAfter := func(cause error) error {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		observed, err := s.readActive()
		if err != nil || observed.ReleaseID == candidate.ReleaseID {
			// Rename may have committed active.json before its directory fsync
			// failed. Leave the journal for the next explicit recovery.
			return projectRecoveryRequired
		}
		if _, err = s.reconcile(recoveryCtx, observed); err != nil {
			return projectRecoveryRequired
		}
		return cause
	}
	if err = projectAtomicFile(s.control, body, 0644); err != nil {
		return recoverAfter(err)
	}
	startedAt := time.Now()
	failedStart := func(err error) error {
		if candidate.Spec.Kind == "JAVA" && failureLog != nil {
			logCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			state := projectJavaStartupState(logCtx, candidate.Spec.ID)
			logs := projectJavaStartupLogs(logCtx, candidate.Spec, s.dir(candidate.ReleaseID), startedAt)
			*failureLog = strings.TrimSpace(state + "\n" + logs)
		}
		return recoverAfter(err)
	}
	if err = s.reload(ctx, candidate.Spec, false); err != nil {
		if err.Error() == "PROJECT_JAVA_START_FAILED" {
			return failedStart(err)
		}
		return recoverAfter(err)
	}
	healthCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	if err = s.waitHealthy(healthCtx, candidate); err != nil {
		return failedStart(err)
	}
	if err = atomicJSON(s.state(), candidate); err != nil {
		return recoverAfter(err)
	}
	if err = s.clearPending(); err != nil {
		return projectRecoveryRequired
	}
	return nil
}
