package runtimepolicy

import "testing"

func TestPolicyRejectsUnsupportedSupervisorCombinations(t *testing.T) {
	valid := []struct {
		kind string
		p    Policy
	}{
		{"DOCKER", Policy{Restart: "unless-stopped", AutoStart: true, StopTimeoutSeconds: 10}},
		{"DOCKER", Policy{Restart: "on-failure", MaxRetries: 3, StopTimeoutSeconds: 10}},
		{"SYSTEMD", Policy{Restart: "on-failure", AutoStart: true, RestartDelaySeconds: 5, StopTimeoutSeconds: 30}},
	}
	for _, test := range valid {
		if err := test.p.Validate(test.kind); err != nil {
			t.Fatal(test, err)
		}
	}
	invalid := []struct {
		kind string
		p    Policy
	}{
		{"DOCKER", Policy{Restart: "always", StopTimeoutSeconds: 10}},
		{"DOCKER", Policy{Restart: "on-failure", AutoStart: true, StopTimeoutSeconds: 10}},
		{"DOCKER", Policy{Restart: "always", AutoStart: true, RestartDelaySeconds: 5, StopTimeoutSeconds: 10}},
		{"SYSTEMD", Policy{Restart: "unless-stopped", AutoStart: true, StopTimeoutSeconds: 30}},
		{"SYSTEMD", Policy{Restart: "on-failure", MaxRetries: 3, StopTimeoutSeconds: 30}},
		{"TOOL", Policy{Restart: "no", StopTimeoutSeconds: 10}},
		{"SYSTEMD", Policy{Restart: "no", StopTimeoutSeconds: 0}},
	}
	for _, test := range invalid {
		if test.p.Validate(test.kind) == nil {
			t.Fatal("unsupported policy accepted", test)
		}
	}
}
