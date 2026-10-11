package controller

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// One active 20MiB file and four archives; log inputs are controlled event codes.
type RotatingLog struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func NewLog(state string) (*RotatingLog, error) {
	dir := filepath.Join(state, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := privatePath(dir, true); err != nil {
		return nil, err
	}
	r := &RotatingLog{path: filepath.Join(dir, "controller.log")}
	err := r.open()
	return r, err
}
func (r *RotatingLog) open() error {
	if _, err := os.Lstat(r.path); err == nil {
		if err = privatePath(r.path, false); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	r.file = f
	info, err := f.Stat()
	if err == nil {
		r.size = info.Size()
	}
	return err
}
func (r *RotatingLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > 20<<20 {
		if err := r.file.Close(); err != nil {
			return 0, err
		}
		if err := os.Remove(r.path + ".4"); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		for i := 3; i >= 0; i-- {
			old := r.path
			if i > 0 {
				old += "." + strconv.Itoa(i)
			}
			if err := os.Rename(old, r.path+"."+strconv.Itoa(i+1)); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}
func (r *RotatingLog) Close() error { r.mu.Lock(); defer r.mu.Unlock(); return r.file.Close() }
