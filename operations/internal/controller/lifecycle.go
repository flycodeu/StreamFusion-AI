package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type localRuntime struct {
	PID       int    `json:"pid"`
	Listen    string `json:"listen"`
	StartedAt string `json:"startedAt"`
	Token     string `json:"token"`
}

func localRecord(path string, value any) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("LOCAL_CONTROL_PATH_INVALID")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".control-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(value)
	if err == nil {
		err = f.Sync()
	}
	if ce := f.Close(); err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func readLocalRuntime(path string) (localRuntime, error) {
	var record localRuntime
	info, err := os.Lstat(path)
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return record, errors.New("LOCAL_CONTROL_RECORD_INVALID")
	}
	f, err := os.Open(path)
	if err != nil {
		return record, err
	}
	defer f.Close()
	err = json.NewDecoder(io.LimitReader(f, 4097)).Decode(&record)
	if err == nil && (record.PID <= 0 || len(record.Token) != 64) {
		err = errors.New("LOCAL_CONTROL_RECORD_INVALID")
	}
	return record, err
}

// The state directory remains the access boundary. No shutdown HTTP endpoint,
// process-name killing or reusable PID file is needed.
func (s *Store) LocalControl(ctx context.Context, listen string, stop func()) (func(), error) {
	runtimePath := filepath.Join(s.Dir, "controller.runtime.json")
	requestPath := filepath.Join(s.Dir, "controller.stop.json")
	if info, err := os.Lstat(requestPath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("LOCAL_CONTROL_PATH_INVALID")
		}
		if err = os.Remove(requestPath); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	record := localRuntime{os.Getpid(), listen, Now(), randomToken()}
	if err := localRecord(runtimePath, record); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				request, err := readLocalRuntime(requestPath)
				if err == nil && request.Token == record.Token {
					stop()
					return
				}
			}
		}
	}()
	return func() { <-done; _ = os.Remove(requestPath); _ = os.Remove(runtimePath) }, nil
}

func stateLocked(dir string) (bool, error) {
	path := filepath.Join(dir, "controller.lock")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("LOCAL_CONTROL_PATH_INVALID")
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	return lockFile(f) != nil, nil
}

// Stop waits for lock release, not just a disappearing listener. A nonce binds
// the request to one serve invocation, so a stale record cannot stop its successor.
func Stop(dir string, timeout time.Duration) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	locked, err := stateLocked(abs)
	if err != nil {
		return err
	}
	if !locked {
		fmt.Println("控制器已停止")
		return nil
	}
	runtimePath := filepath.Join(abs, "controller.runtime.json")
	record, err := readLocalRuntime(runtimePath)
	if err != nil {
		return errors.New("STOP_UNAVAILABLE: 占用进程没有本地关闭记录；前台服务请按 Ctrl+C，旧版后台服务请先核实进程后关闭。不要删除 controller.lock")
	}
	if err = localRecord(filepath.Join(abs, "controller.stop.json"), record); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			return fmt.Errorf("STOP_TIMEOUT: PID %d 尚未释放状态锁，请检查运行日志；未强制结束进程", record.PID)
		case <-ticker.C:
			locked, err = stateLocked(abs)
			if err != nil {
				return err
			}
			if !locked {
				fmt.Printf("控制器已停止，PID %d，状态锁已释放\n", record.PID)
				return nil
			}
			current, err := readLocalRuntime(runtimePath)
			if err == nil && current.Token != record.Token {
				return errors.New("STOP_INSTANCE_CHANGED: 服务已重新启动，请重新执行 stop")
			}
		}
	}
}
