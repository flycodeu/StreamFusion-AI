package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const pluginLogLimit = 16 << 10

func (x *executor) pluginLog(req Request, phase, code string) error {
	file := filepath.Join(x.root, "jobs", req.JobID, "plugin.log")
	f, e := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	line := fmt.Sprintf("%s %s %s traceId=%s jobId=%s\n", stamp(), phase, code, req.TraceID, req.JobID)
	if info.Size()+int64(len(line)) > pluginLogLimit {
		return nil
	}
	if _, e = f.WriteString(line); e != nil {
		return e
	}
	return f.Sync()
}

func (x *executor) pluginLogs(req Request) (string, error) {
	if e := validatePluginInput(req); e != nil {
		return "", e
	}
	var intent Request
	if readJSON(filepath.Join(x.root, "jobs", req.JobID, "intent.json"), &intent) != nil || intent.InputHash != req.InputHash {
		return "", errors.New("TARGET_INTENT_MISMATCH")
	}
	f, e := os.Open(filepath.Join(x.root, "jobs", req.JobID, "plugin.log"))
	if e != nil {
		return "", e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, pluginLogLimit))
	return string(b), e
}
