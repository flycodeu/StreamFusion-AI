package deploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const projectRetentionWarning = "PROJECT_RELEASE_RETENTION_PENDING"

// Only the two versions the controller can activate are needed after a
// successful switch. Never alter retention while either journal is open.
// Bound each pass so a pile of old releases cannot monopolize publication.
func (s projectStore) retainReleases(ctx context.Context, active projectActive, previous string) string {
	if active.ReleaseID == "" || s.decommissionPending(active) {
		return projectRetentionWarning
	}
	if _, err := s.readPending(); !os.IsNotExist(err) {
		return projectRetentionWarning
	}
	current, err := s.currentControl()
	wanted, wantErr := s.body(active)
	if err != nil || wantErr != nil || !s.matchesControl(current, wanted, active) ||
		!projectSafePath(s.releases) || secureAncestors(s.releases) != nil {
		return projectRetentionWarning
	}
	root, err := os.OpenRoot(s.releases)
	if err != nil {
		return projectRetentionWarning
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return projectRetentionWarning
	}
	defer dir.Close()
	timeout, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	warning, removed, scanned := "", 0, 0
	for {
		names, err := dir.Readdirnames(32)
		if err != nil && !errors.Is(err, io.EOF) {
			return projectRetentionWarning
		}
		for _, name := range names {
			if timeout.Err() != nil || scanned >= 256 || removed >= 8 {
				return projectRetentionWarning
			}
			scanned++
			if !ID.MatchString(name) || name == active.ReleaseID || name == previous {
				continue
			}
			var record projectActive
			if projectReadRecord(filepath.Join(s.dir(name), ".release.json"), &record) != nil ||
				!s.valid(record, name) {
				warning = projectRetentionWarning
				continue
			}
			if root.RemoveAll(name) != nil || dir.Sync() != nil {
				return projectRetentionWarning
			}
			removed++
		}
		if len(names) == 0 || errors.Is(err, io.EOF) {
			return warning
		}
	}
}
