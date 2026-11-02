//go:build linux

package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Runs under the existing isolated user + pinned real npm fixture. No business
// account is touched, and a dead local port proves setting config is offline.
func testNodeRegistryLinux(t *testing.T, x *executor, n serviceinstall.NodeContext, version string, request func() (Request, string)) {
	n.ObserveRegistry = true
	n.TerminalHash = ""
	_, account, err := resolveNodeAccount(n, false)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	config := filepath.Join(account.HomeDir, ".npmrc")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	observe := func() *NodeEnvironmentObservation {
		o, e := x.observeNode(ctx, n, false)
		if e != nil || o.Registry == nil {
			t.Fatalf("observe registry: %v", e)
		}
		return o
	}
	change := func(url string) (Result, string) {
		before := observe()
		req, dir := request()
		req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "SET_NPM_REGISTRY", NodeVersion: version, RegistryURL: url, ExpectedHash: before.Fingerprint}
		r, e := x.applyNodeEnvironment(req, dir)
		if e != nil || r.Status != "SUCCEEDED" {
			t.Fatalf("registry change: %s/%s %v", r.Status, r.ErrorCode, e)
		}
		return r, dir
	}
	before := observe()
	if before.Registry.Status != "DEFAULT" || before.Registry.Exists {
		t.Fatal("missing config should be default")
	}
	result, _ := change("http://127.0.0.1:9/offline-registry/")
	if result.NodeEnvironment.Registry.URL != "http://127.0.0.1:9/offline-registry/" {
		t.Fatal("URL changed")
	}
	st, _ := os.Stat(config)
	if st.Mode().Perm() != 0600 {
		t.Fatal("new npmrc is not private")
	}
	original := []byte("# preserve original auth and scoped registry\nregistry=https://registry.npmjs.org/\n@private:registry=http://127.0.0.1:4873/private/\n//registry.npmjs.org/:_authToken=fixture-secret-never-log\n//private.example/:_authToken=${NPM_TOKEN}\nsave-exact=true\nfetch-retries=2\n")
	if err = os.WriteFile(config, original, 0640); err != nil {
		t.Fatal(err)
	}
	_ = os.Chown(config, uid, gid)
	_ = os.Chmod(config, 0640)
	before = observe()
	req, dir := request()
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "SET_NPM_REGISTRY", NodeVersion: version, RegistryURL: "https://registry.npmmirror.com/", ExpectedHash: before.Fingerprint}
	_ = os.Chmod(config, 0600)
	if _, e := x.applyNodeEnvironment(req, dir); e == nil || e.Error() != "NODE_ENVIRONMENT_DRIFT" {
		t.Fatalf("mode drift not rejected: %v", e)
	}
	_ = os.Chmod(config, 0640)
	result, dir = change("https://registry.npmmirror.com/")
	raw, _ := json.Marshal(result)
	if bytes.Contains(raw, []byte("fixture-secret")) {
		t.Fatal("credential leaked in receipt")
	}
	actual, _ := os.ReadFile(config)
	if !bytes.Contains(actual, []byte("fixture-secret-never-log")) || !bytes.Contains(actual, []byte("@private:registry=http://127.0.0.1:4873/private/")) {
		t.Fatal("credential or scoped config lost")
	}
	if !bytes.Contains(actual, []byte("${NPM_TOKEN}")) {
		t.Fatal("auth environment reference changed")
	}
	st, _ = os.Stat(config)
	if st.Mode().Perm() != 0640 {
		t.Fatal("existing mode changed")
	}
	backup := filepath.Join(n.RootPath, "backups/npm-registry", filepath.Base(dir), ".npmrc")
	saved, _ := os.ReadFile(backup)
	if !bytes.Equal(saved, original) {
		t.Fatal("original backup not preserved")
	}
	st, _ = os.Stat(backup)
	if st.Mode().Perm() != 0600 {
		t.Fatal("backup not private")
	}
	// A durable marker with only its before state remains UNKNOWN; reconciliation
	// never reruns npm. Restoring the already-computed after file completes it.
	req, unknownDir := request()
	before = observe()
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "SET_NPM_REGISTRY", NodeVersion: version, RegistryURL: defaultNodeRegistry, ExpectedHash: before.Fingerprint}
	if err = atomicJSON(filepath.Join(unknownDir, "node-before.json"), before); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(unknownDir, "node-change.json"), req.NodeEnvironment); err != nil {
		t.Fatal(err)
	}
	if err = x.applyNodeRegistry(ctx, req, unknownDir, before); err != nil {
		t.Fatal(err)
	}
	completed, _ := os.ReadFile(config)
	_ = os.WriteFile(config, actual, 0640)
	if r := x.reconcileNodeEnvironment(req, unknownDir); r.Status == "SUCCEEDED" {
		t.Fatal("before state confirmed")
	}
	check, _ := os.ReadFile(config)
	if !bytes.Equal(check, actual) {
		t.Fatal("unknown replayed npm")
	}
	_ = os.WriteFile(config, completed, 0640)
	if r := x.reconcileNodeEnvironment(req, unknownDir); r.Status != "SUCCEEDED" {
		t.Fatalf("completed result not recoverable: %s", r.ErrorCode)
	}
	// Credentials embedded in registry itself are never returned. Other Node
	// operations remain available even when this optional read is unsafe.
	_ = os.WriteFile(config, []byte("registry=https://name:fixture-secret@example.com/\n"), 0640)
	obs := observe()
	raw, _ = json.Marshal(obs)
	if obs.Registry.Status != "REDACTED" || bytes.Contains(raw, []byte("fixture-secret")) {
		t.Fatal("registry credentials exposed")
	}
	req, _ = request()
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "SET_DEFAULT", NodeVersion: version}
	if p := x.nodePreflight(req); !p.Ready {
		t.Fatal("optional bad registry blocked existing action")
	}
	_ = os.WriteFile(config, []byte("registry=https://example.com/${TOKEN}\n"), 0640)
	if observe().Registry.Status != "REDACTED" {
		t.Fatal("variable registry not redacted")
	}
	_ = os.Remove(config)
	_ = os.Symlink(filepath.Join(account.HomeDir, ".profile"), config)
	if observe().Registry.Status != "UNSAFE" {
		t.Fatal("symlink accepted")
	}
	_ = os.Remove(config)
	_ = os.WriteFile(config, []byte("registry=https://registry.npmjs.org/\n"), 0600)
	_ = os.Chown(config, 0, 0)
	if observe().Registry.Status != "UNSAFE" {
		t.Fatal("wrong owner accepted")
	}
	_ = os.Remove(config)
	if !strings.HasPrefix(account.HomeDir, "/home/sfnode-") {
		t.Fatal("fixture escaped isolated home")
	}
	// Exercise the actual durable target journal, idempotency and ACK chain.
	before = observe()
	req, journalDir := request()
	req.ProtocolVersion = 1
	req.ExecutorVersion = Version
	req.Action = "NODE_ENVIRONMENT_CHANGE"
	req.SoftwareOperation = req.Action
	req.DeadlineAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	req.NodeEnvironment = &NodeEnvironmentRequest{Context: n, Action: "SET_NPM_REGISTRY", NodeVersion: version, RegistryURL: defaultNodeRegistry, ExpectedHash: before.Fingerprint}
	req.InputHash = ""
	req.InputHash = Digest(req)
	run := func(r Request) Result {
		var buffer bytes.Buffer
		if err := x.executeSoftware(r, &buffer); err != nil {
			t.Fatal(err)
		}
		var result Result
		if err := json.Unmarshal(buffer.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	completedResult := run(req)
	if completedResult.Status != "SUCCEEDED" {
		t.Fatalf("journal result: %s/%s", completedResult.Status, completedResult.ErrorCode)
	}
	if repeated := run(req); Digest(repeated) != Digest(completedResult) {
		t.Fatal("idempotent receipt changed")
	}
	ack := req
	ack.Action = "ACK"
	ack.ResultHash = Digest(completedResult)
	if !run(ack).Ack {
		t.Fatal("registry ACK failed")
	}
	if _, err = os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("ACK did not clear pending")
	}
	if _, err = os.Stat(filepath.Join(journalDir, "intent.json")); err != nil {
		t.Fatal("ACK deleted history")
	}
}
