package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestBuiltinFlowRunsInsertedCommandsAndVerificationInListOrder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	x, req := builtinFixture(t, "java", "21-ubuntu24.04-repository")
	steps := req.Installation.Definition.Commands.Steps
	verify := slices.IndexFunc(steps, func(s serviceinstall.CommandStep) bool { return s.ID == "VERIFY" })
	inserted := serviceinstall.CommandStep{ID: "after-verify", Kind: "COMMAND", Phase: "INSTALL", Name: "验证后写入配置", RunAs: "ROOT", Script: "printf after-verify", TimeoutSeconds: 10}
	steps = slices.Insert(steps, verify+1, inserted)
	steps = slices.DeleteFunc(steps, func(s serviceinstall.CommandStep) bool { return s.ID == "OFFICIAL_PACKAGES_APPLY" })
	req.Installation.Definition.Commands.Steps = steps
	got := []string{}
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) { return nil, nil }
	err := x.runBuiltinFlow(context.Background(), req, []string{"INSTALL", "VERIFY"}, func(step serviceinstall.CommandStep) error { got = append(got, step.ID); return nil }, func(step serviceinstall.CommandStep) ([]byte, error) {
		got = append(got, step.ID)
		out, _, err := x.runBuiltinStep(context.Background(), req, step.ID, nil, nil, "")
		return out, err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"OFFICIAL_REFRESH", "OFFICIAL_PLAN_NATIVE", "OFFICIAL_OWNERSHIP_NATIVE", "VERIFY", "after-verify", "INSTALL_CONFIRM_NATIVE"}
	if !slices.Equal(got, want) {
		t.Fatalf("order=%v", got)
	}
	if !x.builtinResultConfirmed(req, Result{Status: "SUCCEEDED"}) {
		t.Fatal("complete flow not confirmed")
	}
	if err = x.runBuiltinFlow(context.Background(), req, []string{"INSTALL", "VERIFY"}, nil, nil); err == nil {
		t.Fatal("flow replayed")
	}
}

func TestBuiltinFlowStopsBeforeLaterCommandsOnFailedSystemStep(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	x, req := builtinFixture(t, "java", "21-ubuntu24.04-repository")
	count := 0
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) { count++; return nil, nil }
	err := x.runBuiltinFlow(context.Background(), req, []string{"INSTALL", "VERIFY"}, func(serviceinstall.CommandStep) error { return errors.New("OFFICIAL_PLAN_CHANGED") }, nil)
	if err == nil || err.Error() != "OFFICIAL_PLAN_CHANGED" || count != 1 {
		t.Fatalf("err=%v commands=%d", err, count)
	}
	var receipt commandStepReceipt
	if readJSON(filepath.Join(x.root, "jobs", req.JobID, "builtin-step-OFFICIAL_PLAN_NATIVE.json"), &receipt) != nil || receipt.State != "FAILED" {
		t.Fatal("failure receipt missing")
	}
}

func TestOnlyUnmodifiedPreparationCanResume(t *testing.T) {
	_, req := builtinFixture(t, "java", "21-ubuntu24.04-repository")
	step := *builtinStep(req, "OFFICIAL_REFRESH")
	if !replayableBuiltinPreparation(req, step) {
		t.Fatal("default refresh cannot resume")
	}
	step.Script += "\nprintf extra"
	if replayableBuiltinPreparation(req, step) {
		t.Fatal("edited script is not proven read-only preparation")
	}
	if replayableBuiltinPreparation(req, *builtinStep(req, "OFFICIAL_PLAN_NATIVE")) {
		t.Fatal("package-plan boundary can be replayed")
	}
}
