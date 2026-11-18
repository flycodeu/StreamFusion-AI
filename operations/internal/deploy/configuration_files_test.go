package deploy

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestConfigurationFileSecretsRequireMatchingPayloadOnlyForExecution(t *testing.T) {
	body := "server { listen 80; }\n"
	req := managementRequestFixture()
	req.Management.ConfigFile = &serviceinstall.ConfigurationFileRequest{Action: "save", SHA256: serviceinstall.Digest(body)}
	values := map[string]string{serviceinstall.ConfigurationFileSecret: body}
	for _, action := range []string{"SOFTWARE_OPERATION", "SOFTWARE_OPERATION_CHECK", "STATUS", "ACK"} {
		req.Action = action
		needed := action == "SOFTWARE_OPERATION" || action == "SOFTWARE_OPERATION_CHECK"
		if (validateManagementSecrets(req, values) == nil) != needed || (validateManagementSecrets(req, nil) == nil) == needed {
			t.Fatalf("incorrect payload requirement for %s", action)
		}
	}
	req.Action = "SOFTWARE_OPERATION"
	for _, wrong := range []map[string]string{
		{serviceinstall.ConfigurationFileSecret: "modified"},
		{serviceinstall.ConfigurationFileSecret: body, "unrelated": "secret"},
		{"unrelated": body},
	} {
		if validateManagementSecrets(req, wrong) == nil {
			t.Fatal("unrelated or modified configuration payload accepted")
		}
	}
}

func TestConfigurationFileRestorationPreservesFailedSystemdState(t *testing.T) {
	req := managementRequestFixture()
	for _, kind := range []string{"SYSTEMD", "DOCKER"} {
		req.Software.Kind = kind
		for _, file := range []bool{false, true} {
			req.Management.ConfigFile = nil
			if file {
				req.Management.ConfigFile = &serviceinstall.ConfigurationFileRequest{}
			}
			for _, state := range []string{"RUNNING", "STOPPED", "FAILED", "UNKNOWN", "PAUSED"} {
				want := state == "RUNNING" || state == "STOPPED" || state == "FAILED" && file && kind == "SYSTEMD"
				if restorableManagementState(req, state) != want {
					t.Fatalf("unexpected state boundary: %s file=%v state=%s", kind, file, state)
				}
			}
		}
	}
}

func TestNginxConfigurationParserProtectsStaticAndProxyRequests(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil || strings.Contains(strings.ToLower(python), "windowsapps") {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python is required to exercise the embedded Nginx parser")
	}
	_, source, ok := strings.Cut(nginxConfigurationFileScript, "<<'PY'\n")
	if !ok {
		t.Fatal("embedded Python source is missing")
	}
	source = strings.TrimSuffix(source, "PY\n")
	code := `import ast
source = ` + strconv.Quote(source) + `
tree = ast.parse(source)
scope = {}
nodes = [n for n in tree.body if isinstance(n, (ast.Import, ast.ImportFrom, ast.ClassDef, ast.FunctionDef))]
exec(compile(ast.Module(body=nodes, type_ignores=[]), 'nginx-config-parser', 'exec'), scope)
scope['allowed_listen'] = '127.0.0.1:8080'
# Filesystem confinement is separate from directive validation.
scope['allowed_root'] = lambda value, line: None
static = 'server { listen 127.0.0.1:8080; root /managed/www; disable_symlinks on; autoindex off; location / { try_files $uri $uri/ /index.html; } }'
proxy = 'server { listen 127.0.0.1:8080; location / { proxy_pass http://backend:8080/; proxy_ignore_headers X-Accel-Redirect; } }'
for text in [static, proxy, 'server { listen 127.0.0.1:8080; return 404; }', static + '\n' + proxy]:
    scope['validate'](text)
for text in [
    static.replace('disable_symlinks on;', ''),
    static.replace('disable_symlinks on;', 'disable_symlinks off;'),
    static.replace('autoindex off;', 'autoindex on;'),
    static.replace('location / {', 'location / { disable_symlinks off;'),
    static.replace('listen 127.0.0.1:8080;', 'listen 8081;'),
    static.replace('root /managed/www;', 'alias /managed/www/;'),
    static.replace('root /managed/www;', 'include /etc/passwd;'),
    proxy.replace('proxy_ignore_headers X-Accel-Redirect;', ''),
    proxy.replace('location / {', 'location /api/ {'),
    proxy.replace('proxy_pass http://backend:8080/;', 'try_files $uri =404;'),
    'server { listen 127.0.0.1:8080; }',
]:
    try:
        scope['validate'](text)
    except scope['ConfigError'] as exc:
        assert exc.line > 0, (text, exc.message)
    else:
        raise AssertionError('unsafe configuration accepted: ' + text)
old = static.replace('disable_symlinks on;', '').replace('autoindex off;', '')
scope['validate'](old, enforce_safety=False)
for actual, expected in [('active', 'RUNNING'), ('inactive', 'STOPPED'), ('failed', 'FAILED')]:
    scope.update(docker=False, locator='nginx.service', run=lambda args, value=actual: value)
    assert scope['state']() == expected
print('Nginx parser and service state checks passed')
`
	cmd := exec.Command(python, "-I", "-S", "-B", "-")
	cmd.Stdin = strings.NewReader(code)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Nginx parser regression failed: %v\n%s", err, output)
	}
}
