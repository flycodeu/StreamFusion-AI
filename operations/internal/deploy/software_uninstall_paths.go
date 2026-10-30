package deploy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Roots always end in the frozen software UUID. No caller can supply an
// arbitrary deletion path, and shared parent directories are never removed.
func validateUninstallTree(root string, deleteData bool) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || secureAncestors(root) != nil {
		return errors.New("SOFTWARE_UNINSTALL_PATH_UNSAFE")
	}
	if deleteData {
		if err = uninstallMountBoundary(root); err != nil {
			return err
		}
		return validateUninstallSubtree(root, true)
	}
	for _, name := range []string{"bin", "runtime"} {
		if err = uninstallMountBoundary(filepath.Join(root, name)); err != nil {
			return err
		}
		if err = validateUninstallSubtree(filepath.Join(root, name), false); err != nil {
			return err
		}
	}
	return nil
}

func validateUninstallSubtree(root string, forbidLinks bool) error {
	deadline, count := time.Now().Add(20*time.Second), 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		count++
		if count > 200000 || time.Now().After(deadline) {
			return errors.New("SOFTWARE_UNINSTALL_TREE_LIMIT")
		}
		if entry.Type()&os.ModeSymlink != 0 && forbidLinks {
			// Vendor runtimes and Docker layers contain internal links. Require
			// their resolved target to remain in this exact installation; removal
			// still unlinks the link itself through os.Root and never follows it.
			resolved, resolveErr := filepath.EvalSymlinks(path)
			relative, relativeErr := filepath.Rel(root, resolved)
			if resolveErr != nil || relativeErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				return errors.New("SOFTWARE_UNINSTALL_SYMLINK_FORBIDDEN")
			}
		}
		// Owned services may keep a Unix socket in their data directory while
		// running. It is safely unlinked after stopping; device files and pipes
		// have no place in an installation tree and remain a hard rejection.
		if entry.Type()&os.ModeSymlink == 0 && entry.Type()&os.ModeSocket == 0 && !entry.IsDir() && !entry.Type().IsRegular() {
			return errors.New("SOFTWARE_UNINSTALL_SPECIAL_FILE_FORBIDDEN")
		}
		return nil
	})
	return err
}

func removeOwnedUninstallPaths(root string, names []string, deleteRoot bool) error {
	if err := validateUninstallTree(root, deleteRoot); err != nil {
		return err
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	}
	base := root
	if deleteRoot {
		base = filepath.Dir(root)
		names = []string{filepath.Base(root)}
	}
	// os.Root confines every filesystem operation even if a service changes a
	// link after validation. RemoveAll unlinks links without following them.
	directory, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer directory.Close()
	for _, name := range names {
		if name == "." || name == "" || strings.ContainsAny(name, `/\\`) || name == ".." {
			return errors.New("SOFTWARE_UNINSTALL_PATH_INVALID")
		}
		if err = directory.RemoveAll(name); err != nil {
			return err
		}
	}
	return nil
}
