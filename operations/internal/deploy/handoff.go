package deploy

import (
	"errors"
	"os"
	"path/filepath"
)

// The target-wide lock closes the read/activate race on this executor. It is
// released after the observed handoff; it is not a persistent Platform permit.
func (x *executor) handoffLock() (*os.File, error) {
	lock, err := targetLock(x.root)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		lock.Close()
		return nil, errors.New("TARGET_JOB_PENDING")
	}
	return lock, nil
}
