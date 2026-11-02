package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestManagedSoftwareReadChecksActualResultEnvelope(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, true, false)
	host := f.create(t, b, "read-managed-result")
	target, _ := s.readTarget(host["id"].(string))
	item := softwareInstance{ID: uuid.NewString(), HostID: target.ID, RuntimeKind: "SYSTEMD", Locator: "docker.service", IdentityHash: strings.Repeat("a", 64), Version: "1"}
	for _, scenario := range []string{"logs", "observe", "wrong-controller", "wrong-target", "wrong-kind", "wrong-locator", "wrong-identity", "missing-software", "wrong-protocol", "failed", "job-receipt", "wrong-time", "bare-observation", "invalid-json"} {
		t.Run(scenario, func(t *testing.T) {
			result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: s.controllerID(), TargetID: target.ID, Status: "SUCCEEDED", Phase: "SOFTWARE_OBSERVED", ObservedAt: Now(), Software: &deploy.SoftwareObservation{Kind: item.RuntimeKind, Locator: item.Locator, IdentityHash: item.IdentityHash, State: "RUNNING", ObservedAt: Now(), Log: "service ready token=hidden-test-secret", Truncated: true}}
			want := "EXECUTOR_RESPONSE_INVALID"
			switch scenario {
			case "logs", "observe":
				want = ""
			case "wrong-controller":
				result.ControllerID = uuid.NewString()
				want = "EXECUTOR_IDENTITY_MISMATCH"
			case "wrong-target":
				result.TargetID = uuid.NewString()
				want = "EXECUTOR_IDENTITY_MISMATCH"
			case "wrong-kind":
				result.Software.Kind = "DOCKER"
				want = "SOFTWARE_IDENTITY_DRIFT"
			case "wrong-locator":
				result.Software.Locator = "nginx.service"
				want = "SOFTWARE_IDENTITY_DRIFT"
			case "wrong-identity":
				result.Software.IdentityHash = strings.Repeat("b", 64)
				want = "SOFTWARE_IDENTITY_DRIFT"
			case "missing-software":
				result.Software = nil
			case "wrong-protocol":
				result.ProtocolVersion = 2
			case "failed":
				result.Status = "FAILED"
			case "job-receipt":
				result.JobID = uuid.NewString()
			case "wrong-time":
				result.Software.ObservedAt = "invalid"
			}
			body, _ := json.Marshal(result)
			if scenario == "bare-observation" {
				body, _ = json.Marshal(result.Software)
			}
			if scenario == "invalid-json" {
				body = []byte(`{"secret":"untrusted-parser-value"`)
			}
			handler := func(command string) *hostSSHResponse {
				if command == executorSSHCommand(target) {
					return &hostSSHResponse{Stdout: string(body)}
				}
				return nil
			}
			f.Responses.Store(&handler)
			action := "SOFTWARE_LOGS"
			if scenario == "observe" {
				action = "SOFTWARE_OBSERVE"
			}
			fact, err := b.h.readManagedSoftware(context.Background(), target, item, action)
			if got := executorFailureCode(err); got != want {
				t.Fatalf("got %s want %s", got, want)
			}
			if want != "" {
				if fact != nil {
					t.Fatal("unverified observation escaped")
				}
				return
			}
			if fact == nil || fact.State != "RUNNING" || !fact.Truncated || strings.Contains(fact.Log, "hidden-test-secret") || !strings.Contains(fact.Log, "REDACTED") {
				t.Fatal("verified result not extracted or redacted")
			}
		})
	}
}

func TestSoftwareReadDiagnosticsAcceptOnlyExactKnownCodes(t *testing.T) {
	for _, code := range []string{"SOFTWARE_IDENTITY_DRIFT", "SOFTWARE_OWNERSHIP_MISMATCH", "SOFTWARE_LOG_READ_DENIED"} {
		if got := executorFailureCode(classifyExecutorInvocation(errors.New("SSH failure"), code+"\n")); got != code {
			t.Fatal(code, got)
		}
		if got := executorFailureCode(classifyExecutorInvocation(errors.New("SSH failure"), "private data "+code)); got != "EXECUTOR_CALL_FAILED" {
			t.Fatal("untrusted diagnostic accepted", got)
		}
	}
}
