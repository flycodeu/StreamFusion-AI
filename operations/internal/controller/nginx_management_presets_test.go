package controller

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func nginxManagementFixture(t *testing.T) serviceinstall.Definition {
	t.Helper()
	for _, d := range serviceinstall.BuiltinCommandCatalog() {
		if d.Key == "nginx" && !serviceinstall.IsOfficial(d) {
			d.Management = nginxManagementDefinition(d)
			return d
		}
	}
	t.Fatal("nginx builtin missing")
	return serviceinstall.Definition{}
}

func TestNginxManagementPresetsHaveBoundedPublishedScriptsAndRequestEnvelope(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(nil)
	bash := managementTestBash(t)
	for _, d := range serviceinstall.BuiltinCommandCatalog() {
		if d.Key != "nginx" {
			continue
		}
		d.Management = nginxManagementDefinition(d)
		encoded, _ := json.Marshal(d)
		if err := d.ValidateCommands(); err != nil {
			t.Fatalf("%s %s template=%d bytes: %v", d.Key, d.Version, len(encoded), err)
		}
		for _, op := range d.Management.Operations {
			for _, step := range append(op.Steps, op.VerifySteps...) {
				cmd := exec.Command(bash, "-n")
				cmd.Stdin = strings.NewReader(step.Script)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s syntax: %v %s", op.Key, err, output)
				}
			}
			parameters, err := serviceinstall.ResolveParameters(op.Parameters, nil, nil)
			if err != nil {
				t.Fatal(op.Key, err)
			}
			id := uuid.NewString()
			management := serviceinstall.ManagementRequest{Definition: d, SoftwareID: id, BindingRevision: 1, Root: "/opt/streamfusion-ops-target/software/" + id, OperationKey: op.Key, OperationParameters: parameters, ResourceHash: strings.Repeat("a", 64)}
			req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString(), JobID: uuid.NewString(), Action: "SOFTWARE_OPERATION", SoftwareOperation: "SOFTWARE_OPERATION", InputHash: strings.Repeat("a", 64), DeadlineAt: "2026-11-09T22:00:00Z", Software: &deploy.SoftwareRequest{ID: id, Version: 1, Kind: "SYSTEMD", Locator: "nginx.service", IdentityHash: strings.Repeat("b", 64)}, Management: &management}
			if err = deploy.SignCommandRequest(&req, private); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(req)
			if len(payload)+1 > 64<<10 {
				t.Fatalf("%s request=%d exceeds target envelope", op.Key, len(payload)+1)
			}
		}
		d.Key, d.CatalogKey = "custom-nginx-copy", "nginx"
		if nginxManagementDefinition(d) != nil {
			t.Fatal("custom catalog label acquired builtin management")
		}
	}
}

func TestNginxManagementNativeRollbackAndDockerMountConfiguration(t *testing.T) {
	bash := managementTestBash(t)
	d := nginxManagementFixture(t)
	for _, mode := range []string{"NATIVE", "DOCKER"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range []string{"work", "config", "bin", "fake-bin", "data/public"} {
				if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(bash, "--noprofile", "--norc", "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(root))
			data, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(data))
			}
			posixRoot := strings.TrimSpace(string(data))
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			id := uuid.NewString()
			original := "# " + id + "\nevents {}\nhttp { server { listen 80; } }\n"
			write("config/nginx.conf", original)
			write("data/public/index.html", "<!doctype html><title>fixture</title>")
			write("fake-bin/runuser", "#!/bin/bash\nset -e\nshift 3\n\"$@\"\n")
			write("bin/nginx", `#!/bin/bash
set -e
printf '%s\n' "$*" >> "$OPS_INSTALL_ROOT/commands"
if [[ "$*" == *'-t'* && -f "$OPS_INSTALL_ROOT/fail-once" ]]; then rm -f "$OPS_INSTALL_ROOT/fail-once"; exit 1; fi
`)
			write("fake-bin/docker", `#!/bin/bash
set -e
if [[ "$1" == inspect ]]; then
 case "$3" in
  *NetworkSettings*) printf '80/tcp|127.0.0.1|8080\n';;
  *) printf '%s|/parent\n%s|/specific\n' "$OPS_INSTALL_ROOT/data" "$OPS_INSTALL_ROOT/data/public";;
 esac
elif [[ "$1" == exec && "$2" == --user ]]; then
 shift 4
 [[ "$1" == test && ( "$3" == /specific || "$3" == /specific/index.html ) ]]
elif [[ "$1" == exec && "$3" == nginx ]]; then
 shift 3; "$OPS_INSTALL_ROOT/bin/nginx" "$@"
elif [[ "$1" == exec && "$3" == test ]]; then
 [[ "$5" == /specific ]]
else exit 1; fi
`)
			runtimeKind := "SYSTEMD"
			if mode == "DOCKER" {
				runtimeKind = "DOCKER"
			}
			base := []string{"PATH=" + posixRoot + "/fake-bin:/usr/bin:/bin", "OPS_INSTALL_ROOT=" + posixRoot, "OPS_CONFIG_ROOT=" + posixRoot + "/config", "OPS_SOFTWARE_ID=" + id, "OPS_SOFTWARE_LOCATOR=fixture-nginx", "OPS_JOB_ID=" + uuid.NewString(), "OPS_RUNTIME_KIND=" + runtimeKind}
			run := func(key string, verify bool, overrides map[string]string) (string, error) {
				t.Helper()
				op, err := d.ManagementOperation(key)
				if err != nil {
					t.Fatal(err)
				}
				values, err := serviceinstall.ResolveParameters(op.Parameters, overrides, nil)
				if err != nil {
					t.Fatal(err)
				}
				env := append(append(os.Environ(), base...), "OPS_OPERATION_KEY="+key)
				keys := make([]string, 0, len(values))
				for key := range values {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					env = append(env, "OPS_PARAM_"+key+"="+values[key])
				}
				steps := op.Steps
				if verify {
					steps = op.VerifySteps
				}
				output := ""
				for _, step := range steps {
					cmd := exec.Command(bash, "--noprofile", "--norc")
					cmd.Env, cmd.Stdin = env, strings.NewReader(step.Script)
					data, err := cmd.CombinedOutput()
					output += string(data)
					if err != nil {
						return output, err
					}
				}
				return output, nil
			}
			staticKey := "publish-static"
			static := map[string]string{"SITE": "frontend", "STATIC_ROOT": posixRoot + "/data/public", "PORT": "8080"}
			before, _ := os.Stat(filepath.Join(root, "config/nginx.conf"))
			if output, err := run(staticKey, false, static); err != nil {
				t.Fatal("publish static", err, output)
			}
			if output, err := run(staticKey, true, static); err != nil {
				t.Fatal("verify static", err, output)
			}
			after, _ := os.Stat(filepath.Join(root, "config/nginx.conf"))
			if !os.SameFile(before, after) {
				t.Fatal("main configuration inode replaced")
			}
			main, _ := os.ReadFile(filepath.Join(root, "config/nginx.conf"))
			if mode == "DOCKER" && (!strings.Contains(string(main), "root /specific;") || strings.Contains(string(main), "root /parent/public;")) {
				t.Fatal("Docker selected incorrect nested mount", string(main))
			}
			write("fail-once", "fail")
			if output, err := run(staticKey, false, static); err == nil || !strings.Contains(output, "已恢复原配置") {
				t.Fatal("failed nginx test did not restore configuration", err, output)
			}
			restored, _ := os.ReadFile(filepath.Join(root, "config/nginx.conf"))
			if string(restored) != string(main) {
				t.Fatal("rollback changed the previous main configuration")
			}
			proxyKey := "publish-proxy"
			proxy := map[string]string{"SITE": "backend", "SERVER_NAME": "api.example.com", "PORT": "8080", "UPSTREAM": "http://127.0.0.1:9000", "LOCATION": "/api/", "STRIP_PREFIX": "true", "WEBSOCKET": "true", "SSE": "true"}
			if output, err := run(proxyKey, false, proxy); err != nil {
				t.Fatal("publish proxy", err, output)
			}
			if output, err := run(proxyKey, true, proxy); err != nil {
				t.Fatal("verify proxy", err, output)
			}
			fragment, _ := os.ReadFile(filepath.Join(root, "config/ops-sites/backend.conf"))
			for _, text := range []string{"proxy_pass http://127.0.0.1:9000/;", "proxy_set_header Upgrade $http_upgrade;", "proxy_buffering off;", "gzip off;"} {
				if !strings.Contains(string(fragment), text) {
					t.Fatal("proxy setting missing", text, string(fragment))
				}
			}
			output, err := run("sites", false, nil)
			var table serviceinstall.ManagementTable
			if err != nil || json.Unmarshal([]byte(output), &table) != nil || table.Total == nil || *table.Total != 2 || len(table.Rows) != 2 {
				t.Fatal("site list", err, output)
			}
			for _, enabled := range []string{"false", "true"} {
				for _, verify := range []bool{false, true} {
					if output, err := run("set-site-state", verify, map[string]string{"SITE": "backend", "ENABLED": enabled}); err != nil {
						t.Fatal("toggle site", enabled, err, output)
					}
				}
			}
			for _, verify := range []bool{false, true} {
				if output, err := run("delete-site", verify, map[string]string{"SITE": "backend"}); err != nil {
					t.Fatal("delete site", err, output)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "config/ops-sites/backend.conf")); !os.IsNotExist(err) {
				t.Fatal("site file retained", err)
			}
		})
	}
}
