package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (x *executor) verifyStopped(r Release) error {
	out, err := x.docker("ps", "-a", "--filter", "name=^/sfops-"+strings.ReplaceAll(r.InstanceID, "-", "")+"$", "--format", "{{.ID}}")
	if err != nil || len(bytes.TrimSpace(out)) != 0 {
		return errors.New("EMPTY_BASELINE_NOT_VERIFIED")
	}
	return nil
}

// Called only while holding the target execution lock. Missing files alone are
// insufficient: prove that no pending intent exists and the frozen baseline is
// still actual before recording a failed, never-activated operation.
func (x *executor) recordNotStarted(req Request, dir string, output io.Writer) error {
	if err := validateRelease(req.Candidate); err != nil {
		return err
	}
	if req.Baseline != nil {
		if err := validateRelease(req.Baseline); err != nil {
			return err
		}
		if req.Baseline.InstanceID != req.Candidate.InstanceID {
			return errors.New("BASELINE_INSTANCE_MISMATCH")
		}
	}
	frozen := req
	frozen.Action = "ACTIVATE"
	frozen.InputHash = ""
	if Digest(frozen) != req.InputHash {
		return errors.New("INPUT_HASH_MISMATCH")
	}
	for _, name := range []string{"intent.json", "progress.json", "result.json", "ack.json"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			return errors.New("TARGET_EVIDENCE_PRESENT_OR_DAMAGED")
		}
	}
	res := Result{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "FAILED", Phase: "NOT_STARTED", ErrorCode: "TARGET_NOT_STARTED", ObservedAt: stamp(), Health: "STOPPED"}
	if req.Baseline != nil {
		actual, err := x.healthy(*req.Baseline)
		if err != nil {
			return errors.New("BASELINE_NOT_VERIFIED")
		}
		res.ReleaseID, res.ImageID, res.ConfigHash, res.ContainerID, res.Health = actual.ReleaseID, actual.ImageID, actual.ConfigHash, actual.ContainerID, actual.Health
		res.Preview = actual.Preview
	} else if err := x.verifyStopped(*req.Candidate); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	req.Action = "ACTIVATE"
	if err := atomicJSON(filepath.Join(x.root, "pending.json"), req); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "intent.json"), req); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "result.json"), res); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(res)
}
