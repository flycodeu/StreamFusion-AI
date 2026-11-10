package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestOfficialPathFailureNamesPathWithoutClaimingExistingSoftware(t *testing.T) {
	err := &officialPathCheckError{"OFFICIAL_PATH_NOT_PROTECTED", "/lib/systemd/system/docker.service"}
	message := officialExistingMessage(err)
	if !strings.Contains(message, err.path) || !strings.Contains(message, "父目录") || strings.Contains(message, "已有") || strings.Contains(message, "检测登记") {
		t.Fatal(message)
	}
	err.code = "OFFICIAL_EXISTING_CONFIGURATION_OR_DATA"
	if message = officialExistingMessage(err); !strings.Contains(message, err.path) || !strings.Contains(message, "已存在") {
		t.Fatal(message)
	}
}

func TestOfficialAptPlanRejectsUpgradesRemovalsAndUntrustedOrigin(t *testing.T) {
	good := "Inst redis-tools (5:7.0.15-1ubuntu0.24.04.5 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])\nInst redis-server (5:7.0.15-1ubuntu0.24.04.5 Ubuntu:24.04/noble-updates [amd64])\nInst helper (1.0 Ubuntu:24.04/noble [all])\n"
	packages, err := parseOfficialAptPlan([]byte(good), []string{"redis-server", "redis-tools"}, map[string]string{})
	if err != nil || len(packages) != 3 || packages[0].Direct || !packages[1].Direct || packages[2].Origin != "Ubuntu:24.04/noble-security, Ubuntu:24.04/noble-updates" {
		t.Fatalf("valid complete plan: %+v %v", packages, err)
	}
	for name, output := range map[string]string{
		"removal":   good + "Remv libc6 [2.39]\n",
		"purge":     good + "Purg old-config\n",
		"upgrade":   strings.Replace(good, "Inst helper (", "Inst helper [0.9] (", 1),
		"vendor":    strings.ReplaceAll(good, "Ubuntu:24.04/noble-updates", "Vendor:24.04/noble-updates"),
		"backports": strings.ReplaceAll(good, "Ubuntu:24.04/noble-updates", "Ubuntu:24.04/noble-backports"),
		"missing":   strings.Replace(good, "Inst redis-server", "Conf redis-server", 1),
		"duplicate": good + "Inst helper (1.0 Ubuntu:24.04/noble [all])\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOfficialAptPlan([]byte(output), []string{"redis-server", "redis-tools"}, nil); err == nil {
				t.Fatal("unsafe plan accepted")
			}
		})
	}
	if _, err := parseOfficialAptPlan([]byte(good), []string{"redis-server", "redis-tools"}, map[string]string{"helper": "1.0"}); err == nil {
		t.Fatal("existing dependency upgrade accepted")
	}
}

func TestOfficialPolicyRequiresConfiguredUbuntuIndexForEverySelectedVersion(t *testing.T) {
	packages := []serviceinstall.OfficialPackage{{Name: "redis-server", Version: "5:7.0.15-1ubuntu0.24.04.5"}}
	good := "redis-server:\n  Installed: (none)\n  Candidate: 5:7.0.15-1ubuntu0.24.04.5\n  Version table:\n     5:7.0.15-1ubuntu0.24.04.5 500\n        500 http://security.ubuntu.com/ubuntu noble-security/universe amd64 Packages\n        500 http://archive.ubuntu.com/ubuntu noble-updates/universe amd64 Packages\n"
	indexes := []byte("500 http://security.ubuntu.com/ubuntu noble-security/universe amd64 Packages\n release v=24.04,o=Ubuntu,a=noble-security,n=noble,l=Ubuntu,c=universe,b=amd64\n500 http://archive.ubuntu.com/ubuntu noble-updates/universe amd64 Packages\n release v=24.04,o=Ubuntu,a=noble-updates,n=noble,l=Ubuntu,c=universe,b=amd64\n")
	if err := verifyOfficialPolicy([]byte(good), packages, indexes); err != nil {
		t.Fatal(err)
	}
	mirror := strings.ReplaceAll(good, "archive.ubuntu.com", "mirrors.tencentyun.com")
	mirrorIndexes := []byte(strings.ReplaceAll(string(indexes), "archive.ubuntu.com", "mirrors.tencentyun.com"))
	if err := verifyOfficialPolicy([]byte(mirror), packages, mirrorIndexes); err != nil {
		t.Fatal("configured Ubuntu mirror rejected", err)
	}
	if verifyOfficialPolicy([]byte(mirror), packages, []byte(strings.ReplaceAll(string(mirrorIndexes), "o=Ubuntu", "o=Other"))) == nil {
		t.Fatal("non-Ubuntu index accepted")
	}
	for name, output := range map[string]string{"mirror": strings.Replace(good, "archive.ubuntu.com", "attacker.invalid", 1), "origin-impostor": strings.Replace(good, "archive.ubuntu.com", "archive.ubuntu.com.attacker.invalid", 1), "missing-version": strings.ReplaceAll(good, "5:7.0.15-1ubuntu0.24.04.5", "5:7.0.15-1ubuntu0.24.04.4"), "path": strings.ReplaceAll(good, "/ubuntu ", "/untrusted "), "query": strings.ReplaceAll(good, "/ubuntu ", "/ubuntu?repo=evil ")} {
		t.Run(name, func(t *testing.T) {
			if verifyOfficialPolicy([]byte(output), packages, indexes) == nil {
				t.Fatal("unsafe repository accepted")
			}
		})
	}
}

func TestOfficialDockerPlansMissingDependenciesWithApt(t *testing.T) {
	d, err := serviceinstall.FindOfficial("docker", "29.9.2-official-ubuntu24.04")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	x := &executor{softwareRun: func(_ time.Duration, command string, args ...string) ([]byte, error) {
		calls++
		if command == "/usr/bin/apt-get" && strings.Contains(strings.Join(args, " "), "--simulate") {
			return []byte("Inst iptables (1.8.10 Ubuntu:24.04/noble [amd64])\n"), nil
		}
		if command == "/usr/bin/apt-cache" && reflect.DeepEqual(args, []string{"policy", "iptables"}) {
			return []byte("iptables:\n  Installed: (none)\n  Candidate: 1.8.10\n  Version table:\n     1.8.10 500\n        500 http://mirrors.tencentyun.com/ubuntu noble/main amd64 Packages\n"), nil
		}
		if command == "/usr/bin/apt-cache" && reflect.DeepEqual(args, []string{"policy"}) {
			return []byte("500 http://mirrors.tencentyun.com/ubuntu noble/main amd64 Packages\n release v=24.04,o=Ubuntu,a=noble,n=noble,l=Ubuntu,c=main,b=amd64\n"), nil
		}
		t.Fatalf("preflight performed mutation or unexpected probe %s %v", command, args)
		return nil, nil
	}}
	plan, err := x.officialCandidatePlan(context.Background(), d, map[string]string{})
	if err != nil || calls != 3 || len(plan.Packages) != 6 {
		t.Fatalf("plan=%+v calls=%d err=%v", plan, calls, err)
	}
	args := strings.Join(plan.Commands[1].Args, " ")
	if !strings.Contains(args, "iptables=1.8.10") || strings.Contains(args, "--no-download") || !strings.Contains(args, "./docker-ce_") {
		t.Fatal("online plan cannot install missing dependency", args)
	}
	if plan.Commands[0].Args[len(plan.Commands[0].Args)-1] != "update" || plan.Commands[2].Executable != "/usr/bin/systemctl" || !reflect.DeepEqual(plan.Commands[2].Args, []string{"enable", "--now", "--", "docker.service"}) {
		t.Fatal("refresh or service start missing from visible commands", plan.Commands)
	}
}

func officialTestRequest(t *testing.T) Request {
	t.Helper()
	d, err := serviceinstall.FindOfficial("redis", "ubuntu24.04-repository")
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := serviceinstall.OfficialRecipeSpec(d)
	packages := []serviceinstall.OfficialPackage{{Name: "redis-server", Version: "5:7.0.15-1ubuntu0.24.04.5", Origin: "Ubuntu:24.04/noble-updates", Direct: true}, {Name: "redis-tools", Version: "5:7.0.15-1ubuntu0.24.04.5", Origin: "Ubuntu:24.04/noble-updates", Direct: true}}
	plan := &serviceinstall.OfficialPlan{Manager: "APT", OS: "ubuntu24.04", Architecture: "amd64", Packages: packages, Paths: spec.Paths, Commands: serviceinstall.OfficialInstallCommands(d, packages)}
	return Request{JobID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", InputHash: strings.Repeat("a", 64), Installation: &serviceinstall.Request{SoftwareID: "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb", BindingRevision: 1, Definition: d, Official: plan, Source: serviceinstall.Source{Type: "OFFICIAL"}, Config: serviceinstall.Config{Name: "redis", RuntimeMode: "NATIVE", BindAddress: "127.0.0.1", HostPort: 6379, MemoryMiB: 512}}}
}

func TestOfficialRecoveryDoesNotReplayPreparationOrPackageCommands(t *testing.T) {
	req := officialTestRequest(t)
	root := t.TempDir()
	dir := filepath.Join(root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	x := &executor{root: root}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		if command != "/usr/bin/dpkg-query" {
			t.Fatalf("recovery replayed command: %s %v", command, args)
		}
		return []byte(""), nil
	}
	marker := officialInstallation{JobID: req.JobID, InputHash: req.InputHash, SoftwareID: req.Installation.SoftwareID, ConfigHash: Digest(req.Installation.Config), Plan: *req.Installation.Official}
	writeMarker := func(name string, value officialInstallation) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name), data, 0600)
	}
	if err := writeMarker("official-update.json", marker); err != nil {
		t.Fatal(err)
	}
	result := x.reconcileOfficialSoftware(req, dir)
	if result.Status != "FAILED" || result.ErrorCode != "OFFICIAL_PREPARATION_INTERRUPTED" {
		t.Fatalf("preparation result %+v", result)
	}
	if err := writeMarker("official-apply.json", marker); err != nil {
		t.Fatal(err)
	}
	result = x.reconcileOfficialSoftware(req, dir)
	if result.Status != "UNKNOWN" {
		t.Fatalf("partial packages should stay unknown: %+v", result)
	}
	if err := os.WriteFile(filepath.Join(dir, "official-apply.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := x.reconcileOfficialSoftware(req, dir); result.Status != "UNKNOWN" {
		t.Fatal("corrupt apply marker was treated as absent")
	}
	marker.InputHash = strings.Repeat("c", 64)
	if err := writeMarker("official-apply.json", marker); err != nil {
		t.Fatal(err)
	}
	result = x.reconcileOfficialSoftware(req, dir)
	if result.Status != "UNKNOWN" {
		t.Fatal("changed intent accepted")
	}
}

func TestOfficialOwnershipAndToolObservationUseFrozenSystemPaths(t *testing.T) {
	req := officialTestRequest(t)
	x := &executor{}
	marker := officialInstallation{Plan: *req.Installation.Official, BinaryHash: strings.Repeat("a", 64)}
	s := x.officialSoftware(req, marker)
	if !validOfficialOwnership(s) {
		t.Fatal("trusted system ownership rejected")
	}
	copyPlan := *s.Ownership.Official
	copyPlan.Paths.Data = "/"
	s.Ownership.Official = &copyPlan
	if validOfficialOwnership(s) {
		t.Fatal("arbitrary path accepted")
	}
	d, _ := serviceinstall.FindOfficial("python", "ubuntu24.04-repository")
	spec, _ := serviceinstall.OfficialRecipeSpec(d)
	s.SoftwareKey = "PYTHON"
	s.Kind = "TOOL"
	s.Locator = spec.Paths.Binary
	s.Ownership.BinaryPath = s.Locator
	s.Ownership.Official = &serviceinstall.OfficialPlan{Paths: spec.Paths}
	commands := []string{}
	run := func(_ context.Context, command string, args ...string) ([]byte, error) {
		commands = append(commands, command)
		if command == "/usr/bin/sha256sum" {
			return []byte(s.Ownership.BinaryHash + "  " + s.Locator), nil
		}
		if command == s.Locator && reflect.DeepEqual(args, []string{"-I", "--version"}) {
			return []byte("Python 3.12.3"), nil
		}
		t.Fatalf("unexpected probe %s %v", command, args)
		return nil, nil
	}
	actual, err := observeInstalledTool(context.Background(), s, run)
	if err != nil || actual.Version != "Python 3.12.3" || len(commands) != 2 {
		t.Fatalf("official tool probe %+v %v %v", actual, err, commands)
	}
}
