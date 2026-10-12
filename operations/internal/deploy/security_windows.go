package deploy

import (
	"errors"
	"os"
)

func secureAncestors(string) error          { return errors.New("LINUX_ONLY") }
func targetLock(string) (*os.File, error)   { return nil, errors.New("LINUX_ONLY") }
func availableBytes(string) (uint64, error) { return 0, errors.New("LINUX_ONLY") }
