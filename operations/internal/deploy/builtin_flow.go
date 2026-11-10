package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func builtinFlow(req Request) bool {
	d := commandDefinition(req)
	return d != nil && serviceinstall.IsBuiltinCommand(*d) && d.Commands.FlowVersion == 1
}

// Every visible step runs in list order. SYSTEM callbacks perform the existing
// protected file and identity operations; COMMAND callbacks execute saved shell.
func (x *executor) runBuiltinFlow(ctx context.Context, req Request, phases []string, system func(serviceinstall.CommandStep) error, command func(serviceinstall.CommandStep) ([]byte, error)) error {
	d := commandDefinition(req)
	if d == nil || d.Commands == nil {
		return errors.New("COMMAND_TEMPLATE_INVALID")
	}
	for _, step := range d.Commands.Steps {
		if !slices.Contains(phases, step.Phase) || builtinStep(req, step.ID) == nil {
			continue
		}
		if err := x.checkpoint(req, step.ID); err != nil {
			return err
		}
		if step.Kind != "SYSTEM" {
			var err error
			if command != nil {
				_, err = command(step)
			} else {
				_, _, err = x.runBuiltinStep(ctx, req, step.ID, nil, nil, "")
			}
			if err != nil {
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					x.appendServiceLog(req, step.ID, fmt.Sprintf("命令退出码：%d", exit.ExitCode()))
					return errors.New("COMMAND_STEP_FAILED")
				}
				return err
			}
			continue
		}
		file := filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+step.ID+".json")
		if _, err := os.Lstat(file); !os.IsNotExist(err) {
			return errors.New("COMMAND_STEP_RECONCILE_REQUIRED")
		}
		receipt := commandStepReceipt{Phase: step.Phase, Name: step.Name, State: "RUNNING", StartedAt: stamp()}
		if err := atomicJSON(file, receipt); err != nil {
			return err
		}
		x.appendServiceLog(req, step.ID, step.Name+builtinSystemDetails(req, step.Action))
		var err error
		if system == nil {
			err = errors.New("COMMAND_SYSTEM_ACTION_INVALID")
		} else {
			err = system(step)
		}
		receipt.State, receipt.FinishedAt = "SUCCEEDED", stamp()
		if err != nil {
			receipt.State = "FAILED"
		}
		if ctx.Err() != nil {
			receipt.State = "UNKNOWN"
			err = errors.New("COMMAND_STEP_INTERRUPTED")
		}
		if writeErr := atomicJSON(file, receipt); writeErr != nil {
			return writeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func builtinSystemDetails(req Request, action string) string {
	d := commandDefinition(req)
	if d == nil {
		return ""
	}
	var detail strings.Builder
	if action == "INSTALL_MATERIAL" && d.Native != nil {
		for _, part := range d.Native.Parts {
			fmt.Fprintf(&detail, "\n文件：%s\n来源：%s\nSHA-256：%s", part.Name, part.URL, part.SHA256)
		}
	}
	if i := commandInstallation(req); i != nil {
		if action == "INSTALL_MATERIAL" && i.Source.Type == "ARCHIVE" {
			fmt.Fprintf(&detail, "\n接收材料：%d bytes\nSHA-256：%s", i.Source.ByteSize, i.Source.SHA256)
		}
		if action == "OFFICIAL_PLAN" && i.Official != nil {
			for _, pkg := range i.Official.Packages {
				fmt.Fprintf(&detail, "\n软件包：%s=%s", pkg.Name, pkg.Version)
			}
		}
		if action == "DOCKER_PREPARE" {
			fmt.Fprintf(&detail, "\n镜像：%s@%s\n镜像 ID：%s", d.Image, d.Digest, d.ImageID)
		}
		if action == "NODE_PREPARE" && i.Node != nil {
			fmt.Fprintf(&detail, "\n账号：%s\n环境目录：%s", i.Node.Account, i.Node.RootPath)
		}
	}
	return detail.String()
}

func (x *executor) skipBuiltinStep(req Request, step serviceinstall.CommandStep, reason string) error {
	x.appendServiceLog(req, step.ID, step.Name+" · "+reason)
	return atomicJSON(filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+step.ID+".json"), commandStepReceipt{Phase: step.Phase, Name: step.Name, State: "SKIPPED", StartedAt: stamp(), FinishedAt: stamp()})
}

func builtinFlowPhases(req Request) []string {
	phases := []string{"INSTALL", "VERIFY"}
	if req.Uninstallation != nil || req.NodeEnvironment != nil && slices.Contains([]string{"REMOVE_ENVIRONMENT", "REMOVE_PNPM"}, req.NodeEnvironment.Action) {
		phases = []string{"UNINSTALL"}
	}
	if req.Uninstallation != nil && req.Uninstallation.DeleteData || req.NodeEnvironment != nil && req.NodeEnvironment.DeleteData {
		phases = append(phases, "DELETE_DATA")
	}
	return phases
}

func builtinFlowContext(req Request) (context.Context, context.CancelFunc) {
	limit := time.Hour
	if deadline, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil {
		limit = min(limit, time.Until(deadline))
	}
	return context.WithTimeout(context.Background(), limit)
}

// A failed later command must not erase an already identified installation.
// Unknown observations keep the operation pending; identified objects remain
// available for logs, repair and uninstall with a failed task receipt.
func failedBuiltinFlowResult(observed Result, cause error) Result {
	if observed.Installation == nil || observed.Software == nil || observed.Status == "UNKNOWN" {
		return observed
	}
	observed.Status, observed.Phase, observed.ErrorCode = "FAILED", "COMMAND_STEP_FAILED", cause.Error()
	observed.Health = "FAILED"
	observed.Installation.Health = "FAILED"
	return observed
}
