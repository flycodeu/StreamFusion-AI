package deploy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/plugin"
)

// Root-only Linux fixture: a protected Docker command stub tests executor
// behavior without talking to any Docker daemon or existing host service.
func pluginExecutorFixture(t *testing.T) *executor {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root required for protected executor fixture")
	}
	if _, e := os.Stat("/usr/bin/python3"); e != nil {
		t.Skip("python3 required")
	}
	// Hosted runners make /opt writable by ordinary users. The command fixture
	// must live below protected ancestors, just like a real executor binary.
	root, e := os.MkdirTemp("/var/lib", "sfops-plugin-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if e = os.Mkdir(filepath.Join(root, "jobs"), 0700); e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(root, "docker-fixture")
	code := `#!/usr/bin/python3
import hashlib, json, pathlib, sys
root=pathlib.Path(__file__).parent
f=root/'docker-state.json'
s=json.loads(f.read_text()) if f.exists() else {'images':{},'calls':[]}
a=sys.argv[3:]
s['calls'].append(a)
mode=(root/'mode').read_text().strip() if (root/'mode').exists() else ''
def save(): f.write_text(json.dumps(s))
def image_id(image): return 'sha256:'+hashlib.sha256(image.encode()).hexdigest()
if a[:2]==['image','inspect']:
    image=a[-1]
    if image not in s['images']:
        save(); print('Error: No such image',file=sys.stderr);sys.exit(1)
    digest=image.split('@')[1]
    if mode=='digest': digest='sha256:'+'b'*64
    if '--format' in a: print(image_id(image)+'|linux/amd64|'+image.split('@')[0]+'@'+digest)
    else: print('[]')
elif a[0]=='info': print(root)
elif a[0]=='pull':
    if mode=='pull': save();sys.exit(1)
    s['images'][a[-1]]=True
elif a[0]=='run':
    image=next((ref for ref in s['images'] if image_id(ref)==a[-2]),'')
    if not image: save();sys.exit(1)
    if mode=='version': print('unexpected version')
    elif 'maven' in image: print('Apache Maven 3.9.11\nJava version: 21.0.10')
    elif ':17-' in image: print('openjdk version "17.0.18"')
    else: print('openjdk version "21.0.10"')
elif a[:2]==['image','rm']:
    if mode=='remove': save();sys.exit(1)
    s['images'].pop(a[-1],None)
else: save();sys.exit(1)
save()
`
	if e = os.WriteFile(bin, []byte(code), 0700); e != nil {
		t.Fatal(e)
	}
	if e = secureAncestors(bin); e != nil {
		t.Fatalf("command fixture is not protected: %v", e)
	}
	return &executor{root: root, owner: Owner{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString(), DockerBinary: bin, DockerSocket: "/var/run/fixture.sock"}}
}

func TestPluginExecutorRejectsWritableFixtureAncestor(t *testing.T) {
	x := pluginExecutorFixture(t)
	if e := os.Chmod(x.root, 0777); e != nil {
		t.Fatal(e)
	}
	if _, e := x.docker("image", "inspect", "unused"); e == nil || e.Error() != "EXECUTOR_PATH_NOT_PROTECTED" {
		t.Fatalf("writable command ancestor was not rejected: %v", e)
	}
	if _, e := os.Stat(filepath.Join(x.root, "docker-state.json")); !os.IsNotExist(e) {
		t.Fatalf("unprotected command fixture ran: %v", e)
	}
}

func pluginTestRequest(x *executor, version, action string) Request {
	d, _ := plugin.Find("java-runtime", version)
	r := Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, JobID: uuid.NewString(), PluginInstallationID: uuid.NewString(), PluginOperation: action, Action: action, Plugin: &PluginRequest{Key: d.Key, Version: d.Version, Kind: d.Kind, Platform: d.Platform, Image: d.Image, Digest: d.Digest, RecipeRevision: d.RecipeRevision, Command: d.Command, Argument: d.Argument}, DeadlineAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)}
	r.InputHash = Digest(r)
	return r
}

func runPluginTest(t *testing.T, x *executor, r Request) Result {
	t.Helper()
	var b bytes.Buffer
	if e := x.executePlugin(r, &b); e != nil {
		t.Fatal(e)
	}
	var result Result
	if e := StrictJSON(b.Bytes(), &result); e != nil {
		t.Fatal(e, b.String())
	}
	return result
}

func ackPluginTest(t *testing.T, x *executor, r Request, result Result) {
	t.Helper()
	r.Action = "ACK"
	r.ResultHash = Digest(result)
	if ack := runPluginTest(t, x, r); !ack.Ack {
		t.Fatal("ACK not confirmed", ack)
	}
	if _, e := os.Stat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(e) {
		t.Fatal("pending remains", e)
	}
}

func TestPluginExecutorInstallStatusAckAndRemove(t *testing.T) {
	x := pluginExecutorFixture(t)
	r := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
	result := runPluginTest(t, x, r)
	if result.Status != "SUCCEEDED" || result.ObservedVersion == "" {
		t.Fatal(result)
	}
	logs, e := x.pluginLogs(r)
	if e != nil || !strings.Contains(logs, "PULLING") || !strings.Contains(logs, "VERSION_CHECK") || len(logs) > pluginLogLimit {
		t.Fatal("bounded logs missing", e, logs)
	}
	again := runPluginTest(t, x, r)
	if Digest(result) != Digest(again) {
		t.Fatal("duplicate request changed result")
	}
	query := r
	query.Action = "STATUS"
	if runPluginTest(t, x, query).Status != "SUCCEEDED" {
		t.Fatal("STATUS changed hash")
	}
	ackPluginTest(t, x, r, result)
	ackPluginTest(t, x, r, result)
	r17 := pluginTestRequest(x, "17-jre", "PLUGIN_INSTALL")
	result17 := runPluginTest(t, x, r17)
	if result17.Status != "SUCCEEDED" || !strings.Contains(result17.ObservedVersion, `openjdk version "17.`) {
		t.Fatal(result17)
	}
	ackPluginTest(t, x, r17, result17)
	maven := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
	d, _ := plugin.Find("maven-jdk-build", "3.9.11-jdk-21")
	maven.Plugin = &PluginRequest{Key: d.Key, Version: d.Version, Kind: d.Kind, Platform: d.Platform, Image: d.Image, Digest: d.Digest, RecipeRevision: d.RecipeRevision, Command: d.Command, Argument: d.Argument}
	maven.InputHash = ""
	maven.InputHash = Digest(maven)
	mavenResult := runPluginTest(t, x, maven)
	if mavenResult.Status != "SUCCEEDED" || !strings.Contains(mavenResult.ObservedVersion, "Java version: 21.") {
		t.Fatal(mavenResult)
	}
	ackPluginTest(t, x, maven, mavenResult)
	remove := r
	remove.JobID = uuid.NewString()
	remove.Action = "PLUGIN_REMOVE"
	remove.PluginOperation = remove.Action
	remove.InputHash = ""
	remove.InputHash = Digest(remove)
	removed := runPluginTest(t, x, remove)
	if removed.Status != "SUCCEEDED" || removed.Health != "PLUGIN_REMOVED" {
		t.Fatal(removed)
	}
	ackPluginTest(t, x, remove, removed)
	def17, _ := requestPluginDefinition(r17)
	if _, e := x.inspectPlugin(def17); e != nil {
		t.Fatal("removing 21 damaged Java 17", e)
	}
}

func TestPluginExecutorFailuresAndExternalImage(t *testing.T) {
	for _, mode := range []string{"pull", "digest", "version"} {
		t.Run(mode, func(t *testing.T) {
			x := pluginExecutorFixture(t)
			os.WriteFile(filepath.Join(x.root, "mode"), []byte(mode), 0600)
			r := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
			result := runPluginTest(t, x, r)
			if result.Status != "FAILED" || !strings.HasPrefix(result.ErrorCode, "PLUGIN_") {
				t.Fatal(result)
			}
			ackPluginTest(t, x, r, result)
		})
	}
	x := pluginExecutorFixture(t)
	r := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
	d, _ := requestPluginDefinition(r)
	state := map[string]any{"images": map[string]bool{d.Image + "@" + d.Digest: true}, "calls": []any{}}
	atomicJSON(filepath.Join(x.root, "docker-state.json"), state)
	result := runPluginTest(t, x, r)
	ackPluginTest(t, x, r, result)
	remove := r
	remove.JobID = uuid.NewString()
	remove.Action = "PLUGIN_REMOVE"
	remove.PluginOperation = remove.Action
	remove.InputHash = ""
	remove.InputHash = Digest(remove)
	removed := runPluginTest(t, x, remove)
	ackPluginTest(t, x, remove, removed)
	if _, e := x.inspectPlugin(d); e != nil {
		t.Fatal("external image deleted", e)
	}
}

func TestPluginExecutorUnknownAndLostAck(t *testing.T) {
	x := pluginExecutorFixture(t)
	r := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
	result := runPluginTest(t, x, r)
	dir := filepath.Join(x.root, "jobs", r.JobID)
	if e := os.Remove(filepath.Join(dir, "result.json")); e != nil {
		t.Fatal(e)
	}
	query := r
	query.Action = "STATUS"
	result = runPluginTest(t, x, query)
	if result.Status != "SUCCEEDED" {
		t.Fatal("lost result not reconciled", result)
	}
	// An ACK receipt may survive a crash before pending.json is removed.
	atomicJSON(filepath.Join(dir, "ack.json"), map[string]string{"ResultHash": Digest(result)})
	ackPluginTest(t, x, r, result)
	other := pluginTestRequest(x, "17-jre", "PLUGIN_INSTALL")
	atomicJSON(filepath.Join(x.root, "pending.json"), other)
	query = r
	query.Action = "ACK"
	query.ResultHash = Digest(result)
	if !runPluginTest(t, x, query).Ack {
		t.Fatal("lost ACK not repeatable")
	}
	var pending Request
	if readJSON(filepath.Join(x.root, "pending.json"), &pending) != nil || pending.JobID != other.JobID {
		t.Fatal("old ACK deleted newer pending intent")
	}
	if e := os.Remove(filepath.Join(x.root, "pending.json")); e != nil {
		t.Fatal(e)
	}
	query = other
	query.Action = "STATUS"
	notStarted := runPluginTest(t, x, query)
	if notStarted.Status != "FAILED" || notStarted.Phase != "NOT_STARTED" {
		t.Fatal(notStarted)
	}
	ackPluginTest(t, x, other, notStarted)
}

func TestPluginExecutorRejectsUntrustedRequest(t *testing.T) {
	x := pluginExecutorFixture(t)
	r := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
	r.Plugin.Command = "sh"
	r.InputHash = ""
	r.InputHash = Digest(r)
	if e := x.executePlugin(r, &bytes.Buffer{}); e == nil {
		t.Fatal("untrusted command executed")
	}
	if _, e := os.Stat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(e) {
		t.Fatal("untrusted request wrote intent")
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "secret") {
		t.Fatal("fixture contains secret")
	}
}

func TestPluginExecutorRemoveBlockedAndInterruptedInstall(t *testing.T) {
	x := pluginExecutorFixture(t)
	r := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
	result := runPluginTest(t, x, r)
	ackPluginTest(t, x, r, result)
	os.WriteFile(filepath.Join(x.root, "mode"), []byte("remove"), 0600)
	remove := r
	remove.JobID = uuid.NewString()
	remove.Action = "PLUGIN_REMOVE"
	remove.PluginOperation = remove.Action
	remove.InputHash = ""
	remove.InputHash = Digest(remove)
	failed := runPluginTest(t, x, remove)
	if failed.Status != "FAILED" || failed.ErrorCode != "PLUGIN_REMOVE_BLOCKED" {
		t.Fatal(failed)
	}
	ackPluginTest(t, x, remove, failed)
	if _, e := os.Stat(filepath.Join(x.root, "plugins", r.PluginInstallationID+".json")); e != nil {
		t.Fatal("failed remove lost record", e)
	}
	interrupted := pluginTestRequest(x, "17-jre", "PLUGIN_INSTALL")
	dir := filepath.Join(x.root, "jobs", interrupted.JobID)
	os.MkdirAll(dir, 0700)
	atomicJSON(filepath.Join(dir, "intent.json"), interrupted)
	atomicJSON(filepath.Join(x.root, "pending.json"), interrupted)
	d, _ := requestPluginDefinition(interrupted)
	atomicJSON(filepath.Join(dir, "plugin-material.json"), pluginMaterial{InstallationID: interrupted.PluginInstallationID, Definition: d, Managed: true, JobID: interrupted.JobID})
	query := interrupted
	query.Action = "STATUS"
	missing := runPluginTest(t, x, query)
	if missing.Status != "FAILED" || missing.ErrorCode != "PLUGIN_IMAGE_MISSING" {
		t.Fatal(missing)
	}
	ackPluginTest(t, x, interrupted, missing)
}
