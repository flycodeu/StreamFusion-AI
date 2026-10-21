package deploy

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func bindingExecutorFixture(t *testing.T) (*executor, Request) {
	t.Helper()
	x := pluginExecutorFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	b := Binding{ID: uuid.NewString(), Kind: "REDIS", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Database: "0", CredentialID: uuid.NewString(), CredentialRevision: "1"}
	r := Release{ID: uuid.NewString(), InstanceID: uuid.NewString(), ArtifactID: uuid.NewString(), ArchiveSHA256: strings.Repeat("a", 64), ByteSize: 1, ImageID: "sha256:" + strings.Repeat("b", 64), Config: Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/actuator/health", HealthSeconds: 10, DataCompatibility: "v1", BindingIDs: []string{b.ID}}, Bindings: []Binding{b}}
	x.secrets = map[string]string{b.ID: "private-probe-password"}
	if err := atomicJSON(filepath.Join(x.root, "probe-fixture.json"), map[string]any{"release": r, "controllerId": x.owner.ControllerID}); err != nil {
		t.Fatal(err)
	}
	code := `#!/usr/bin/python3
import json,pathlib,sys
root=pathlib.Path(__file__).parent
p=root/'calls.json'
s=json.loads(p.read_text()) if p.exists() else {'calls':[]}
a=sys.argv[3:];s['calls'].append(a)
p.write_text(json.dumps(s))
f=json.loads((root/'probe-fixture.json').read_text());r=f['release']
if a[:2]==['container','inspect']:
 print(json.dumps([{'Id':'actual-container-id','Image':r['imageId'],'Config':{'Labels':{'com.streamfusion.ops.controller':f['controllerId'],'com.streamfusion.ops.instance':r['instanceId'],'com.streamfusion.ops.release':r['id'],'com.streamfusion.ops.config':f['configHash']}},'State':{'Running':True}}]))
elif a[0]=='run':
 data=json.load(sys.stdin)
 s['passwordMatched']=data['password']=='private-probe-password'
 p.write_text(json.dumps(s))
 if (root/'fail-run').exists(): sys.exit(1)
 print(json.dumps({'applicationNetwork':{'layer':'APPLICATION_NETWORK_TCP','executed':True,'reachable':True},'authentication':{'layer':'AUTHENTICATION','executed':True,'reachable':True,'authenticated':True},'credentialRevision':'1'}))
elif a[:2]==['rm','-f']: pass
else: sys.exit(1)
`
	var fixture map[string]any
	if err := readJSON(filepath.Join(x.root, "probe-fixture.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	fixture["configHash"] = Digest(r.Config)
	if err := atomicJSON(filepath.Join(x.root, "probe-fixture.json"), fixture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(x.owner.DockerBinary, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	return x, Request{Action: "BINDING_PROBE", Candidate: &r, Binding: &b}
}

func TestApplicationBindingProbeUsesActualNetworkAndAlwaysCleansContainer(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			x, req := bindingExecutorFixture(t)
			if fail {
				if err := os.WriteFile(filepath.Join(x.root, "fail-run"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := x.bindingProbe(req)
			if fail {
				if err == nil || err.Error() != "APPLICATION_PROBE_UNAVAILABLE" {
					t.Fatal(err)
				}
			} else if err != nil || !result.Reachable || result.Authentication == nil || !result.Authentication.Authenticated || result.ReleaseID != req.Candidate.ID || result.CredentialRevision != "1" {
				t.Fatal(result, err)
			}
			var state struct {
				Calls           [][]string
				PasswordMatched bool
			}
			if err := readJSON(filepath.Join(x.root, "calls.json"), &state); err != nil {
				t.Fatal(err)
			}
			if len(state.Calls) != 3 || !state.PasswordMatched {
				t.Fatal(state)
			}
			run, remove := state.Calls[1], state.Calls[2]
			args := strings.Join(run, " ")
			for _, wanted := range []string{"--network container:actual-container-id", "--read-only", "--no-healthcheck", "--user 10001:10001", "--cap-drop ALL", "--security-opt no-new-privileges:true", "--pids-limit 32", "--memory 64m", "--cpus 0.25", "--entrypoint /sfops-probe", req.Candidate.ImageID + " binding-probe", "target=/sfops-probe,readonly"} {
				if !strings.Contains(args, wanted) {
					t.Fatal("missing isolation", wanted, args)
				}
			}
			for _, forbidden := range []string{"--privileged", "docker.sock", "private-probe-password", "/etc/"} {
				if strings.Contains(args, forbidden) {
					t.Fatal("unsafe probe argument", args)
				}
			}
			if len(remove) != 3 || remove[0] != "rm" || remove[1] != "-f" || !strings.HasPrefix(remove[2], "sfops-probe-") || !strings.Contains(args, "--name "+remove[2]) {
				t.Fatal("cleanup does not match temporary container", state)
			}
		})
	}
}

func TestApplicationBindingProbeRejectsPendingAndMismatchedBinding(t *testing.T) {
	x, req := bindingExecutorFixture(t)
	if err := os.WriteFile(filepath.Join(x.root, "pending.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := x.bindingProbe(req); err == nil || err.Error() != "TARGET_JOB_PENDING" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(x.root, "calls.json")); !os.IsNotExist(err) {
		t.Fatal("pending job allowed target command")
	}
	os.Remove(filepath.Join(x.root, "pending.json"))
	changed := *req.Binding
	changed.Database = "1"
	req.Binding = &changed
	if _, err := x.bindingProbe(req); err == nil || err.Error() != "PROBE_BINDING_NOT_IN_RELEASE" {
		t.Fatal(err)
	}
}

func TestJavaRuntimeExportIsReadOnlyAndChecksFrozenInstallation(t *testing.T) {
	x := pluginExecutorFixture(t)
	r := pluginTestRequest(x, "21-jre", "PLUGIN_INSTALL")
	result := runPluginTest(t, x, r)
	ackPluginTest(t, x, r, result)
	definition, err := requestPluginDefinition(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(x.root, "runtime-image-id"), []byte(result.ImageID), 0600); err != nil {
		t.Fatal(err)
	}
	code := `#!/usr/bin/python3
import json,pathlib,sys
root=pathlib.Path(__file__).parent
a=sys.argv[3:]
p=root/'export-calls.json'
calls=json.loads(p.read_text()) if p.exists() else []
calls.append(a);p.write_text(json.dumps(calls))
if a[:2]==['image','inspect']:
 print((root/'runtime-image-id').read_text().strip()+'|linux/amd64|'+a[-1])
elif a[0]=='run': print('openjdk version "21.0.10"')
elif a[:2]==['image','save']: sys.stdout.buffer.write(b'verified-runtime-archive')
else: sys.exit(1)
`
	if err := os.WriteFile(x.owner.DockerBinary, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	r.Action = "JAVA_RUNTIME_EXPORT"
	var out bytes.Buffer
	if err := x.exportRuntime(r, &out); err != nil || out.String() != "verified-runtime-archive" {
		t.Fatal(out.String(), err)
	}
	var calls [][]string
	if err := readJSON(filepath.Join(x.root, "export-calls.json"), &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || strings.Join(calls[2], " ") != "image save "+result.ImageID {
		t.Fatal(calls)
	}
	for _, call := range calls {
		if call[0] == "pull" || call[0] == "build" || call[0] == "rm" {
			t.Fatal("runtime export mutated environment", calls)
		}
	}
	if _, err := os.Stat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("read-only export created pending state")
	}
	if err := atomicJSON(filepath.Join(x.root, "plugins", r.PluginInstallationID+".json"), pluginMaterial{InstallationID: r.PluginInstallationID, Definition: definition, ImageID: "sha256:" + strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := x.exportRuntime(r, &bytes.Buffer{}); err == nil || err.Error() != "RUNTIME_ACTUAL_STATE_DRIFT" {
		t.Fatal(err)
	}
	r17 := pluginTestRequest(x, "17-jre", "PLUGIN_INSTALL")
	if err := x.exportRuntime(r17, &bytes.Buffer{}); err == nil || err.Error() != "JAVA21_ENVIRONMENT_REQUIRED" {
		t.Fatal(err)
	}
}
