package deploy

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Read-only integration check: the production runner must retain the inventory,
// not silently apply its smaller general-purpose command-output limit.
func TestPackageInventoryRunnerPreservesSystemOutput(t *testing.T) {
	const command = "/usr/bin/dpkg-query"
	if _, err := os.Stat(command); os.IsNotExist(err) {
		t.Skip("dpkg-query unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"--show", "--showformat=${binary:Package}\t${db:Status-Abbrev}\t${Version}\n"}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	want, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("reference package query", err)
	}
	if len(want) >= packageInventoryLimit {
		t.Skip("host inventory exceeds supported bound")
	}
	got, err := (&executor{}).softwareRunner(ctx, command, args...)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("inventory changed: expected %d bytes, got %d; %v", len(want), len(got), err)
	}
}
