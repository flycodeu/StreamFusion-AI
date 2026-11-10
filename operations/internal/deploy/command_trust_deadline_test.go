package deploy

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"
)

func TestCommandCheckSignaturesAllowClockSkewBelowTargetCeiling(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	encoded := base64.StdEncoding.EncodeToString(public)
	for _, action := range []string{"SOFTWARE_INSTALL_CHECK", "NODE_ENVIRONMENT_CHECK", "SOFTWARE_OPERATION_CHECK"} {
		t.Run(action, func(t *testing.T) {
			req := commandTestRequest()
			req.Action, req.SoftwareOperation, req.DeadlineAt = action, "", ""
			before := time.Now()
			if err := SignCommandRequest(&req, private); err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			deadline, err := time.Parse(time.RFC3339, req.DeadlineAt)
			if err != nil || deadline.Before(before.Add(4*time.Minute-time.Second)) || deadline.After(after.Add(4*time.Minute)) {
				t.Fatalf("unexpected check deadline %q: %v", req.DeadlineAt, err)
			}
			for _, skew := range []time.Duration{-time.Second, 0, time.Second} {
				if err := verifyCommandCheckDeadline(req.DeadlineAt, after.Add(skew)); err != nil {
					t.Fatalf("target clock skew %v rejected check: %v", skew, err)
				}
			}
			if err := verifyCommandSignature(req, encoded); err != nil {
				t.Fatal(err)
			}
			req.DeadlineAt = deadline.Add(time.Second).Format(time.RFC3339)
			if err := verifyCommandSignature(req, encoded); err == nil || err.Error() != "COMMAND_SIGNATURE_INVALID" {
				t.Fatalf("changed deadline accepted: %v", err)
			}
		})
	}
}

func TestCommandSigningPreservesExplicitAndJobDeadlines(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(nil)
	for _, action := range []string{"SOFTWARE_INSTALL_CHECK", "NODE_ENVIRONMENT_CHECK", "SOFTWARE_OPERATION_CHECK", "SOFTWARE_INSTALL"} {
		t.Run(action, func(t *testing.T) {
			req := commandTestRequest()
			req.Action = action
			deadline := req.DeadlineAt
			if err := SignCommandRequest(&req, private); err != nil || req.DeadlineAt != deadline {
				t.Fatalf("explicit deadline changed: %q -> %q: %v", deadline, req.DeadlineAt, err)
			}
			if action == "SOFTWARE_INSTALL" {
				req.DeadlineAt = ""
				if err := SignCommandRequest(&req, private); err != nil || req.DeadlineAt != "" {
					t.Fatal("job request received a check deadline", err)
				}
			}
		})
	}
}

func TestCommandCheckDeadlineKeepsTargetExpiryAndFiveMinuteLimit(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, value string
		valid       bool
	}{
		{"invalid", "not-a-time", false},
		{"missing", "", false},
		{"expired", now.Add(-time.Second).Format(time.RFC3339), false},
		{"five-minute ceiling", now.Add(5 * time.Minute).Format(time.RFC3339), true},
		{"overlong", now.Add(5*time.Minute + time.Second).Format(time.RFC3339), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyCommandCheckDeadline(test.value, now)
			if test.valid && err != nil || !test.valid && (err == nil || err.Error() != "DEADLINE_EXCEEDED") {
				t.Fatalf("deadline validity=%v, got %v", test.valid, err)
			}
		})
	}
}
