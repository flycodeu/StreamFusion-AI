package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestDockerOfflinePlanRejectsUnrelatedChanges(t *testing.T) {
	packages := []dockerPackage{{"docker-ce", "5:29.9.2-1~ubuntu.24.04~noble"}, {"containerd.io", "2.4.2-3~ubuntu.24.04~noble"}}
	valid := "Inst containerd.io (2.4.2-3~ubuntu.24.04~noble local-deb [amd64])\nInst docker-ce (5:29.9.2-1~ubuntu.24.04~noble local-deb [amd64])\nConf docker-ce (5:29.9.2-1~ubuntu.24.04~noble local-deb [amd64])\n"
	if err := validateDockerAptPlan([]byte(valid), packages, false); err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string]string{
		"dependency added":   valid + "Inst iptables (1.8.10 Ubuntu [amd64])\n",
		"runtime removed":    valid + "Remv containerd [1.7]\n",
		"different version":  strings.Replace(valid, "5:29.9.2", "5:30.0.0", 1),
		"missing package":    "Inst docker-ce (5:29.9.2-1~ubuntu.24.04~noble local-deb [amd64])\n",
		"upgrading existing": strings.Replace(valid, "Inst docker-ce (", "Inst docker-ce [5:29.8.2] (", 1),
		"purge":              valid + "Purg iptables [1.8.10]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if validateDockerAptPlan([]byte(output), packages, false) == nil {
				t.Fatal("accepted unexpected package transaction")
			}
		})
	}
	if validateDockerAptPlan([]byte("Remv docker-ce [5:29.9.2-1~ubuntu.24.04~noble]\nRemv containerd.io [2.4.2-3~ubuntu.24.04~noble]\n"), packages, true) != nil {
		t.Fatal("valid exact removal rejected")
	}
	if validateDockerAptPlan([]byte("Remv docker-ce [5:29.9.2]\nRemv containerd.io [2.4.2]\nRemv unrelated [1.0]\n"), packages, true) == nil {
		t.Fatal("accepted unrelated removal")
	}
}

func TestDockerPackageCommandsNeverDownloadOrAutoremove(t *testing.T) {
	for _, remove := range []bool{false, true} {
		for _, simulate := range []bool{false, true} {
			args := strings.Join(dockerAptArgs(simulate, remove, []string{"/private/package.deb"}), " ")
			for _, want := range []string{"--no-download", "--no-install-recommends", "APT::Get::AutomaticRemove=false", "DPkg::Lock::Timeout=15", "-- /private/package.deb"} {
				if !strings.Contains(args, want) {
					t.Fatalf("missing bounded offline option %s", args)
				}
			}
			if strings.Contains(args, " purge ") || strings.Contains(args, " autoremove ") {
				t.Fatal(args)
			}
			if !remove && !strings.Contains(args, "--no-remove") {
				t.Fatal(args)
			}
		}
	}
}

func TestDockerRecipePackageIdentityPinsEpochAndRejectsMutation(t *testing.T) {
	d, err := serviceinstall.Find("docker", "29.9.2-ubuntu24.04")
	if err != nil {
		t.Fatal(err)
	}
	packages, err := dockerRecipePackages(d)
	if err != nil || len(packages) != 5 {
		t.Fatal(packages, err)
	}
	for _, p := range packages {
		if (p.Name == "docker-ce" || p.Name == "docker-ce-cli") && !strings.HasPrefix(p.Version, "5:") {
			t.Fatal("lost publisher epoch", p)
		}
	}
	d.Native.Parts[0].SHA256 = strings.Repeat("0", 64)
	if _, err = dockerRecipePackages(d); err == nil {
		t.Fatal("mutable package recipe accepted")
	}
}

func TestDockerStopAndUninstallDependenciesIncludeStoppedContainers(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		err       error
		allowed   bool
	}{
		{"empty", "\n", nil, true}, {"stopped", strings.Repeat("a", 64) + "\n", nil, false}, {"daemon stopped", "", errors.New("unreachable"), false}, {"truncated", strings.Repeat(" ", 64<<10), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := &executor{softwareRun: func(_ time.Duration, command string, args ...string) ([]byte, error) {
				if command != "/usr/bin/docker" || strings.Join(args, " ") != "container ls --all --no-trunc --format {{.ID}}" {
					t.Fatal(command, args)
				}
				return []byte(tc.out), tc.err
			}}
			if (x.dockerEngineHasNoContainers(context.Background()) == nil) != tc.allowed {
				t.Fatal("wrong dependency decision")
			}
		})
	}
}

func TestDockerPackageStateDoesNotTreatIncompleteInstallAsAbsence(t *testing.T) {
	for _, tc := range []struct {
		out string
		ok  bool
	}{
		{"base-files\tii \t13ubuntu\ndocker-ce\trc \t5:29.9.2\n", true},
		{"base-files\tii \t13ubuntu\ndocker-ce\tiU \t5:29.9.2\n", false},
		{"docker-ce\tii \t5:29.9.2\n", true},
		{"invalid-response", false},
	} {
		x := &executor{softwareRun: func(time.Duration, string, ...string) ([]byte, error) { return []byte(tc.out), nil }}
		state, err := x.installedDockerPackages(context.Background())
		if (err == nil) != tc.ok {
			t.Fatal(tc.out, err)
		}
		if strings.Contains(tc.out, "\trc ") && state["docker-ce"] != "" {
			t.Fatal("config files mistaken for installed program")
		}
	}
}
