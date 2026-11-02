package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestOfficialUninstallRemovesOnlyDirectPackages(t *testing.T) {
	plan := *officialTestRequest(t).Installation.Official
	plan.Packages = append(plan.Packages, serviceinstall.OfficialPackage{Name: "libjemalloc2", Version: "5.3", Origin: "Ubuntu:24.04/noble"})
	good := "Remv redis-server [5:7.0.15]\nRemv redis-tools [5:7.0.15]\n"
	if err := validateOfficialRemovalPlan([]byte(good), plan); err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string]string{
		"dependency":    good + "Remv libjemalloc2 [5.3]\n",
		"other-service": good + "Remv nginx [1.24]\n",
		"purge":         good + "Purg redis-server\n",
		"install":       good + "Inst helper (1.0 Ubuntu:24.04/noble [amd64])\n",
		"missing":       "Remv redis-server [5:7.0.15]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if validateOfficialRemovalPlan([]byte(output), plan) == nil {
				t.Fatal("unsafe removal accepted")
			}
		})
	}
	args := strings.Join(officialRemovalArgs(false, plan), " ")
	if !strings.Contains(args, "AutomaticRemove=false") || !strings.Contains(args, " remove -- redis-server redis-tools") || strings.Contains(args, "libjemalloc2") || strings.Contains(args, "purge") {
		t.Fatal(args)
	}
}

func TestOfficialUninstallProtectsSharedRoots(t *testing.T) {
	for _, directory := range []string{"/", "/etc", "/usr", "/var", "/var/lib", "/var/log", "/var/www", "/var/lib/containerd", "/var/lib/../lib", "relative"} {
		if validateOfficialUninstallDirectory(directory) == nil {
			t.Fatalf("accepted shared or ambiguous deletion root %q", directory)
		}
	}
	plan := *officialTestRequest(t).Installation.Official
	plan.Paths.OwnedPaths = []string{"/"}
	if validateOfficialUninstallPaths(officialTestRequest(t).Installation.Definition, plan) == nil {
		t.Fatal("changed ownership paths accepted")
	}
}

func TestOfficialUninstallDoesNotTreatDpkgFailureAsAbsence(t *testing.T) {
	i := *officialTestRequest(t).Installation
	req := Request{Uninstallation: &SoftwareUninstallRequest{Installation: i}}
	x := &executor{softwareRun: func(_ time.Duration, command string, _ ...string) ([]byte, error) {
		if command != "/usr/bin/dpkg-query" {
			t.Fatalf("unexpected mutation %s", command)
		}
		return nil, errors.New("dpkg unavailable")
	}}
	if x.officialProgramAbsent(context.Background(), req) {
		t.Fatal("command failure confirmed program removal")
	}
}
