package deploy

import (
	"errors"
	"testing"
	"time"
)

func TestExecutorCapabilitiesSeparateDockerEngineFromCompose(t *testing.T) {
	for _, test := range []struct {
		name    string
		engine  bool
		compose bool
	}{
		{"both available", true, true},
		{"engine without compose", true, false},
		{"neither available", false, false},
		{"compose without daemon", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			fact := inspectExecutorCapabilities(Owner{}, "linux/amd64", func(limit time.Duration, args ...string) ([]byte, error) {
				calls++
				if limit != 2*time.Second {
					t.Fatal("capability probe lost its bounded timeout", limit)
				}
				available := test.engine
				if args[0] == "compose" {
					available = test.compose
				}
				if !available {
					return nil, errors.New("unavailable")
				}
				return []byte("version"), nil
			})
			if calls != 2 || fact["dockerEngineAvailable"] != test.engine || fact["dockerAvailable"] != (test.engine && test.compose) {
				t.Fatalf("capabilities conflated Engine with Compose: %+v", fact)
			}
			if fact["softwareLifecycleVersion"] != 1 {
				t.Fatal("F02 lifecycle capability version missing", fact)
			}
		})
	}
}
