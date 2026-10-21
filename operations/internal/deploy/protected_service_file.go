package deploy

import (
	"os"
	"path/filepath"
)

func replaceProtectedServiceFile(filename string, content []byte) error {
	info, err := os.Lstat(filename)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return os.ErrPermission
	}
	uid, gid := nativeFileOwner(info)
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".config-replace-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, err = temporary.Write(content)
	if err == nil {
		err = temporary.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = temporary.Chown(uid, gid)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary.Name(), filename)
}
