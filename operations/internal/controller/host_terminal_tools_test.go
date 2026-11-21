package controller

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestHostTerminalPreparation(t *testing.T) {
	s, b := setup(t)
	b.rawCall("GET", "/hosts/"+uuid.NewString()+"/terminal-tools/download", nil, 401)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	host := f.create(t, b, "terminal host")
	id := host["id"].(string)
	path := "/hosts/" + id + "/terminal-tools"
	view := dataOf(t, b.call("GET", path, nil, 200))
	if view["onlineAvailable"] != true || view["expectedVersion"] != "1" || view["job"] != nil {
		t.Fatal(view)
	}
	download := b.call("GET", path+"/download", nil, 200)
	if download.Body.String() != serviceinstall.HostTerminalInstaller() || !strings.Contains(download.Header().Get("Content-Disposition"), "install-sf-docker.sh") {
		t.Fatal("incorrect installer download")
	}
	in := terminalInstallInput{ExpectedVersion: "1", Confirmed: true, Key: uuid.NewString(), SudoPassword: "only-in-ssh-stdin"}
	b.rawCall("POST", path+"/install", in, 400)
	in.ExpectedVersion = "0"
	b.call("POST", path+"/install", in, 409)
	in.ExpectedVersion = "1"
	var installs, checks atomic.Int32
	var receipt atomic.Value
	receipt.Store("SFOPS_TERMINAL_PENDING")
	handler := func(command string) *hostSSHResponse {
		if strings.Contains(command, in.SudoPassword) {
			t.Error("password in command")
		}
		switch {
		case strings.Contains(command, "SFOPS_PRIVILEGE_READY"):
			return &hostSSHResponse{Stdout: "SFOPS_PRIVILEGE_READY\n"}
		case strings.Contains(command, "&& cat >"):
			return &hostSSHResponse{Input: func(data []byte) {
				if string(data) != serviceinstall.HostTerminalInstaller() {
					t.Error("incorrect bytes uploaded")
				}
			}}
		case command == "/bin/date +%s":
			return &hostSSHResponse{Stdout: "1800000000\n"}
		case strings.Contains(command, "SFOPS_TERMINAL_APPLY"):
			count := installs.Add(1)
			if !strings.HasPrefix(command, "/usr/bin/sudo -S ") || !strings.Contains(command, "sha256sum") {
				t.Error("missing privileged integrity check")
			}
			result := &hostSSHResponse{Input: func(data []byte) {
				if string(data) != in.SudoPassword+"\n" {
					t.Error("wrong password input")
				}
			}}
			if count == 1 || count == 3 {
				result.Exit = 255
			} else if count == 4 {
				result.Exit = 1
				result.Stdout = "SFOPS_TERMINAL_FAILED_APT_UPDATE\n"
			} else {
				result.Stdout = "SFOPS_TERMINAL_READY\n"
			}
			return result
		case strings.Contains(command, "SFOPS_TERMINAL_CHECK"):
			checks.Add(1)
			if strings.Contains(command, "sudo") || strings.Contains(command, "SFOPS_TERMINAL_APPLY") {
				t.Error("reconciliation attempted a mutation")
			}
			return &hostSSHResponse{Stdout: receipt.Load().(string) + "\n"}
		case strings.Contains(command, "rmdir --"):
			return &hostSSHResponse{}
		}
		return nil
	}
	f.Responses.Store(&handler)
	job := dataOf(t, b.call("POST", path+"/install", in, 200))["jobId"].(string)
	result := func() map[string]any { return dataOf(t, b.call("GET", path, nil, 200))["job"].(map[string]any) }
	if result()["status"] != "UNKNOWN" || b.h.latestPreparationJob(id) != nil {
		t.Fatal("lost response was released or mixed in with executor preparation")
	}
	if reused := dataOf(t, b.call("POST", path+"/install", in, 200))["jobId"]; reused != job || installs.Load() != 1 {
		t.Fatal("receipt retry reapplied installer")
	}
	in.Key = uuid.NewString()
	if !strings.Contains(b.call("POST", path+"/install", in, 409).Body.String(), "HOST_BUSY") {
		t.Fatal("lost lock")
	}
	reconcile := map[string]string{"jobId": job, "expectedVersion": "1"}
	b.call("POST", "/hosts/"+id+"/preparation/reconcile", reconcile, 404)
	b.call("POST", path+"/reconcile", reconcile, 409)
	if result()["status"] != "UNKNOWN" {
		t.Fatal("pending read changed original outcome")
	}
	receipt.Store("SFOPS_TERMINAL_READY")
	b.call("POST", path+"/reconcile", reconcile, 200)
	if result()["status"] != "SUCCEEDED" || installs.Load() != 1 || checks.Load() != 2 {
		t.Fatal(result())
	}
	next := dataOf(t, b.call("POST", path+"/install", in, 200))["jobId"].(string)
	if next == job || result()["status"] != "SUCCEEDED" || installs.Load() != 2 {
		t.Fatal("repeat preparation failed")
	}
	in.Key = uuid.NewString()
	failedJob := dataOf(t, b.call("POST", path+"/install", in, 200))["jobId"].(string)
	receipt.Store("SFOPS_TERMINAL_FAILED_DPKG")
	b.call("POST", "/jobs/"+failedJob+"/reconcile", nil, 200)
	if failed := result(); failed["status"] != "FAILED" || failed["errorCode"] != "TERMINAL_TOOLS_DPKG" || !strings.Contains(failed["message"].(string), "dpkg --audit") {
		t.Fatal(failed)
	}
	in.Key = uuid.NewString()
	b.call("POST", path+"/install", in, 200)
	if failed := result(); failed["errorCode"] != "TERMINAL_TOOLS_APT_UPDATE" || !strings.Contains(failed["result"].(map[string]any)["message"].(string), "apt-get update") {
		t.Fatal(failed)
	}
	info := dataOf(t, b.call("GET", "/jobs/"+job, nil, 200))
	if info["subjectName"] != "主机终端工具" || info["instanceId"] != "" || info["softwareInstanceId"] != "" {
		t.Fatal(info)
	}
	var leaked int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs WHERE instr(input_json,?)>0 OR instr(result_json,?)>0", in.SudoPassword, in.SudoPassword).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("stored sudo credentials", err)
	}
}

func TestHostTerminalReceiptScripts(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("exercises root-owned receipts in a Linux temporary directory")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "uploaded.sh")
	work := filepath.Join(dir, "receipts")
	job := uuid.NewString()
	owner := uuid.NewString() + "-" + uuid.NewString()
	script := func(op, sha string, deadline int64) string {
		s := terminalReceiptScript(op, owner, job, sha, deadline)
		return strings.NewReplacer("dir=/var/lib/streamfusion-ops-terminal", "dir="+shellQuote(work), "@STAGED@", shellQuote(input), "@SHA@", shellQuote(sha), "@LIMIT@", "4096").Replace(s)
	}
	run := func(body, want string, success bool) {
		t.Helper()
		cmd := exec.Command("/bin/bash", "-c", body)
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		out, err := cmd.Output()
		if (err == nil) != success || strings.TrimSpace(string(out)) != want {
			t.Fatalf("result %q (%v), want %q; stderr %q", out, err, want, diagnostics.String())
		}
	}
	const future = int64(9000000000)
	for _, test := range []struct {
		body, result string
		success      bool
	}{
		{"echo SFOPS_TERMINAL_STAGE=APT_UPDATE >&2; echo private-url >&2; exit 3", "SFOPS_TERMINAL_FAILED_APT_UPDATE", false},
		{"echo SFOPS_TERMINAL_READY; exit 1", "SFOPS_TERMINAL_FAILED", false},
		{"echo SFOPS_TERMINAL_STAGE=VERIFY >&2; echo SFOPS_TERMINAL_READY", "SFOPS_TERMINAL_READY", true},
	} {
		if err := os.WriteFile(input, []byte(test.body), 0600); err != nil {
			t.Fatal(err)
		}
		sha := fmt.Sprintf("%x", sha256.Sum256([]byte(test.body)))
		run(script("install", sha, future), test.result, test.success)
		run(script("check", sha, future), test.result, true)
	}
	foreign := strings.Repeat("0", 64)
	run(script("check", foreign, future), "SFOPS_TERMINAL_PENDING", true)
	run(script("check", foreign, 1), "SFOPS_TERMINAL_FAILED", true)
	run(script("install", foreign, future), "SFOPS_TERMINAL_FAILED_INTEGRITY", false)
	interrupted := "echo SFOPS_TERMINAL_STAGE=APT_INSTALL >&2; kill -TERM $$"
	if err := os.WriteFile(input, []byte(interrupted), 0600); err != nil {
		t.Fatal(err)
	}
	interruptedSHA := fmt.Sprintf("%x", sha256.Sum256([]byte(interrupted)))
	run(script("install", interruptedSHA, future), "SFOPS_TERMINAL_PENDING", false)
	run(script("check", interruptedSHA, future), "SFOPS_TERMINAL_FAILED", true)
	if err := os.WriteFile(filepath.Join(work, "receipt-"+owner), []byte(job+" "+foreign+" RUNNING\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("exec 8<"+shellQuote(work)+"; flock -x 8; "+script("check", foreign, future), "SFOPS_TERMINAL_PENDING", true)
	run(script("check", foreign, future), "SFOPS_TERMINAL_FAILED", true)
}

func TestHostTerminalLocalAndMissingSudo(t *testing.T) {
	s, b := setup(t)
	b.login()
	local := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "local", "kind": "LOCAL"}, 201))["id"].(string)
	in := terminalInstallInput{ExpectedVersion: "1", Confirmed: true, Key: uuid.NewString()}
	localPath := "/hosts/" + local + "/terminal-tools"
	if dataOf(t, b.call("GET", localPath, nil, 200))["onlineAvailable"] != false {
		t.Fatal("LOCAL offered an SSH installation")
	}
	b.call("GET", localPath+"/download", nil, 200)
	b.call("POST", localPath+"/install", in, 409)
	f := hostSSHServer(t, s, b, false, false)
	id := f.create(t, b, "password host")["id"].(string)
	handler := func(command string) *hostSSHResponse {
		if strings.Contains(command, "SFOPS_PRIVILEGE_READY") {
			return &hostSSHResponse{Exit: 1, Stderr: "sudo: a password is required"}
		}
		t.Error("unexpected command after denied privilege check")
		return nil
	}
	f.Responses.Store(&handler)
	path := "/hosts/" + id + "/terminal-tools"
	b.call("POST", path+"/install", in, 200)
	job := dataOf(t, b.call("GET", path, nil, 200))["job"].(map[string]any)
	if job["status"] != "FAILED" || job["errorCode"] != "PREPARATION_SUDO_REQUIRED" {
		t.Fatal(job)
	}
}
