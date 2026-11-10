package deploy

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Object reconciliation already verified the result. Complete only its final
// SYSTEM receipt; a missing or interrupted shell command still needs review.
func (x *executor) confirmBuiltinObservation(req Request, result Result) bool {
	if !builtinFlow(req) || result.Status != "SUCCEEDED" {
		return true
	}
	phases := builtinFlowPhases(req)
	var final *serviceinstall.CommandStep
	for _, step := range commandDefinition(req).Commands.Steps {
		if !slices.Contains(phases, step.Phase) || builtinStep(req, step.ID) == nil {
			continue
		}
		if step.Kind == "SYSTEM" && (step.Action == "INSTALL_CONFIRM" || step.Action == "UNINSTALL_CONFIRM") {
			copy := step
			final = &copy
			continue
		}
		var receipt commandStepReceipt
		if final != nil || readJSON(filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+step.ID+".json"), &receipt) != nil || !slices.Contains([]string{"SUCCEEDED", "SKIPPED"}, receipt.State) {
			return false
		}
	}
	if final == nil {
		return false
	}
	file := filepath.Join(x.root, "jobs", req.JobID, "builtin-step-"+final.ID+".json")
	var receipt commandStepReceipt
	if err := readJSON(file, &receipt); err != nil && !os.IsNotExist(err) {
		return false
	}
	if receipt.State == "SUCCEEDED" {
		return true
	}
	if receipt.StartedAt == "" {
		receipt.StartedAt = stamp()
	}
	receipt.Phase, receipt.Name, receipt.State, receipt.FinishedAt = final.Phase, final.Name, "SUCCEEDED", stamp()
	x.appendServiceLog(req, final.ID, "已核对服务器上的实际结果")
	return atomicJSON(file, receipt) == nil
}
