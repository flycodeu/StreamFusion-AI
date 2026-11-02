//go:build linux

package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// This integration test creates and removes only its own non-root account and
// unique HOME. Enable in CI with SFOPS_TEST_NODE_LINUX=1. Vendor downloads are
// checksum-pinned by the same material code used for the product.
func TestNodeLinuxIsolatedLifecycle(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("SFOPS_TEST_NODE_LINUX") != "1" {
		t.Skip("requires isolated Linux root runner and SFOPS_TEST_NODE_LINUX=1")
	}
	name := fmt.Sprintf("sfnode-%d-%d", os.Getpid(), time.Now().Unix()%100000)
	home := "/home/" + name
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("fixture HOME exists")
	}
	if out, err := exec.Command("/usr/sbin/useradd", "--create-home", "--home-dir", home, "--shell", "/bin/bash", name).CombinedOutput(); err != nil {
		t.Fatalf("isolated account: %s %v", out, err)
	}
	t.Cleanup(func() {
		_ = exec.Command("/usr/sbin/userdel", name).Run()
		if strings.HasPrefix(home, "/home/sfnode-") {
			_ = os.RemoveAll(home)
		}
	})
	account, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	profileBefore, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	n := testNodeContext()
	n.Account = name
	n.AccountUID = account.Uid
	n.RootPath = filepath.Join(home, ".local/share/streamfusion-ops/node", n.EnvironmentID)
	x := &executor{root: t.TempDir(), owner: Owner{ControllerID: "22222222-2222-4222-8222-222222222222", TargetID: "33333333-3333-4333-8333-333333333333"}}
	sequence := 0
	request := func() (Request, string) {
		sequence++
		id := fmt.Sprintf("44444444-4444-4444-8444-%012d", sequence)
		dir := filepath.Join(x.root, "jobs", id)
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		return Request{ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, JobID: id, InputHash: strings.Repeat("a", 64)}, dir
	}
	checkResult := func(result Result, err error, dir string) {
		if err != nil || result.Status != "SUCCEEDED" {
			raw, _ := os.ReadFile(filepath.Join(dir, "service-install.log"))
			t.Fatalf("result %s/%s err=%v log=%s dir=%s", result.Status, result.ErrorCode, err, raw, dir)
		}
	}
	change := func(action, version, pkg string, del bool) Result {
		req, dir := request()
		req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: action, NodeVersion: version, PackageVersion: pkg, DeleteData: del}
		if _, e := serviceinstall.NodeEnvironmentRecipe(action, pkg); e == nil {
			req.NodeEnvironment.Source.Type = "OFFICIAL"
		}
		r, e := x.applyNodeEnvironment(req, dir)
		checkResult(r, e, dir)
		n.ExpectedDefault = r.NodeEnvironment.DefaultVersion
		return r
	}
	change("INITIALIZE", "", "", false)
	definitions := serviceinstall.NodeCatalog()
	installed := []Request{}
	for index, d := range definitions {
		req, dir := request()
		copy := n
		copy.MakeDefault = index == 1
		req.Installation = &serviceinstall.Request{SoftwareID: fmt.Sprintf("55555555-5555-4555-8555-%012d", index+1), BindingRevision: 1, Definition: d, Config: serviceinstall.Config{Name: "node-test", RuntimeMode: "NATIVE", MemoryMiB: 512}, Source: serviceinstall.Source{Type: "OFFICIAL"}, Node: &copy}
		if index == 0 {
			material := filepath.Join(dir, "fixture-offline-material")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			err = serviceinstall.PrepareNativeMaterial(ctx, d, material)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			info, e := os.Stat(material)
			if e != nil {
				t.Fatal(e)
			}
			hash, e := fileSHA(material)
			if e != nil {
				t.Fatal(e)
			}
			input, e := os.Open(material)
			if e != nil {
				t.Fatal(e)
			}
			defer input.Close()
			x.installationInput = input
			req.Installation.Source = serviceinstall.Source{Type: "ARCHIVE", SHA256: hash, ByteSize: info.Size()}
		}
		r, e := x.installNodeVersion(req, dir)
		checkResult(r, e, dir)
		if e = x.verifyOwnedInstallation(req, r); e != nil {
			t.Fatal(e)
		}
		n.ExpectedDefault = r.NodeEnvironment.DefaultVersion
		installed = append(installed, req)
		// Reconciliation observes the exact archive binary and does not install again.
		recovered := x.reconcileNodeInstall(req, dir)
		if recovered.Status != "SUCCEEDED" {
			t.Fatal("install recovery")
		}
	}
	change("SET_DEFAULT", definitions[0].Version, "", false)
	change("INSTALL_PNPM", definitions[0].Version, "10.20.0", false)
	change("UPDATE_NPM", definitions[0].Version, "11.6.2", false)
	change("REMOVE_PNPM", definitions[0].Version, "", false)
	change("UPDATE_NVM", "", "", false)
	// Remove non-default first; final default requires explicit clear confirmation.
	for _, index := range []int{1, 0} {
		old := installed[index]
		req, dir := request()
		var binding softwareBinding
		if readJSON(x.softwareFile(old.Installation.SoftwareID), &binding) != nil {
			t.Fatal("missing binding")
		}
		i := *old.Installation
		copy := n
		i.Node = &copy
		req.Software = &binding.Software
		req.Uninstallation = &SoftwareUninstallRequest{Installation: i, DeleteData: true, ClearNodeDefault: index == 0}
		r, e := x.uninstallNodeVersion(req, dir)
		checkResult(r, e, dir)
		if e = x.verifyNodeUninstall(req, r); e != nil {
			t.Fatal(e)
		}
		n.ExpectedDefault = r.NodeEnvironment.DefaultVersion
	}
	sentinel := filepath.Join(n.RootPath, "backups", "keep.txt")
	if err = os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	change("REMOVE_ENVIRONMENT", "", "", false)
	profileAfter, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if string(profileBefore) != string(profileAfter) {
		t.Fatal("shell profile changed")
	}
	if _, err = os.Stat(sentinel); err != nil {
		t.Fatal("default removal deleted backups")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	obs, err := x.observeNode(ctx, n, false)
	if err != nil || obs.Exists {
		t.Fatalf("manager still detected: %#v %v", obs, err)
	}
}

func TestNodeLinuxRejectsRootOwnedRunningProcess(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("SFOPS_TEST_NODE_LINUX") != "1" {
		t.Skip("requires isolated Linux root runner")
	}
	resolved, err := filepath.EvalSymlinks("/usr/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(resolved, "30")
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Process.Kill(); _ = process.Wait() }()
	if err = nodeVersionUnused(filepath.Dir(resolved)); err == nil || err.Error() != "NODE_VERSION_RUNNING" {
		t.Fatalf("root-owned process not detected: %v", err)
	}
}
