package serviceinstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The fake daemon records argv, including empty and quoted arguments. It does
// not start containers or require a Docker socket on the development machine.
type terminalFixture struct {
	dir, bash                   string
	java17, java21, mvn, python string
}

func newTerminalFixture(t *testing.T) terminalFixture {
	t.Helper()
	f := terminalFixture{dir: t.TempDir(), bash: "/bin/bash", java17: "sha256:" + strings.Repeat("1", 64),
		java21: "sha256:" + strings.Repeat("2", 64), mvn: "sha256:" + strings.Repeat("3", 64), python: "sha256:" + strings.Repeat("4", 64)}
	if runtime.GOOS == "windows" {
		git, err := exec.LookPath("git")
		if err != nil || builtinTestGitBash(git) == "" {
			t.Skip("Git Bash unavailable")
		}
		f.bash = builtinTestGitBash(git)
	}
	table := "java|17|" + f.java17 + "\njava|21|" + f.java21 + "\nmvn|3.9|" + f.mvn + "\npython3|3.12|" + f.python + "|" + fixturePythonManifest
	src := commandScript("host-terminal/sf-docker.sh")
	if strings.Count(src, "@TOOL_IMAGES@") != 1 {
		t.Fatal("tool inventory placeholder changed")
	}
	f.write(t, "sf-docker", strings.ReplaceAll(src, "@TOOL_IMAGES@", table))
	f.write(t, "docker", `#!/usr/bin/env bash
set -eu
[[ $1 == --host && $2 == unix:///* ]] || exit 71
shift 2
if [[ ${DENY_DOCKER:-} ]]; then echo 'permission denied (fixture)' >&2; exit 73; fi
printf '%s\n' "$1" >> "$RECORD/calls"
case "$1 $2" in
  'image ls') if [[ ${3:-} == --no-trunc ]]; then cat "$RECORD/images"; else echo local-fixture-image; fi ;;
  'image inspect') [[ ${5:-} == fixture:local ]] || exit 1; head -1 "$RECORD/images" ;;
  'port '*) cat "$RECORD/ports" ;;
  'ps '*) cat "$RECORD/containers" ;;
  'run '*|'logs '*|'exec '*) printf '%s\0' "$@" > "$RECORD/args"; if [[ ! ${OMIT_STDIN:-} ]]; then cat > "$RECORD/input"; fi ;;
  *) exit 74 ;;
esac
`)
	for _, name := range []string{"mysql", "psql", "redis-cli"} {
		f.write(t, name, "#!/usr/bin/env bash\nprintf '%s\\0' \"${0##*/}\" \"$@\" > \"$RECORD/args\"\n")
	}
	f.write(t, "images", f.java17+"\n"+f.mvn+"\n"+f.python+"\n")
	f.write(t, "containers", "first-container\n")
	f.write(t, "ports", "3306/tcp -> 100.76.5.1:13306\n33060/tcp -> 0.0.0.0:33060\n")
	return f
}

const fixturePythonManifest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestHostTerminalFindsToolByManifestDigest(t *testing.T) {
	f := newTerminalFixture(t)
	f.write(t, "images", fixturePythonManifest+"\n")
	if args := f.run(t, "python3", "--version"); !slices.Contains(args, fixturePythonManifest) {
		t.Fatalf("wrong image %q", args)
	}
	f.write(t, "images", f.python+"\n"+fixturePythonManifest+"\n")
	if args := f.run(t, "python3", "--version"); !slices.Contains(args, f.python) {
		t.Fatalf("one version was counted twice %q", args)
	}
}

func (f terminalFixture) write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(data), 0755); err != nil {
		t.Fatal(err)
	}
}
func (f terminalFixture) read(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func (f terminalFixture) call(t *testing.T, env []string, entry string, args ...string) (string, error) {
	t.Helper()
	root := `export RECORD="$(pwd -P)" HOME="$(pwd -P)/home" PATH="$(pwd -P):$PATH"; mkdir -p home 'work, project'; cd 'work, project'; `
	cmd := exec.Command(f.bash, append([]string{"-c", root + entry, "fixture"}, args...)...)
	cmd.Dir = f.dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader("fixture standard input\n")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
func (f terminalFixture) run(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := f.call(t, nil, `exec bash "$RECORD/sf-docker" "$@"`, args...)
	if err != nil {
		t.Fatalf("%s: %v: %s", args[0], err, out)
	}
	return strings.Split(strings.TrimSuffix(f.read(t, "args"), "\x00"), "\x00")
}

func TestHostTerminalWorksWithoutPlatform(t *testing.T) {
	f := newTerminalFixture(t)
	for _, inventory := range []string{"first-container", "later-custom-container"} {
		f.write(t, "containers", inventory+"\n")
		out, err := f.call(t, nil, `exec bash "$RECORD/sf-docker" list`)
		if err != nil || !strings.Contains(out, inventory) || !strings.Contains(out, "local-fixture-image") {
			t.Fatalf("list: %v %s", err, out)
		}
	}
	args := []string{"exec", "-i", "later-custom-container", "app-cli", "two words", "", "$(unchanged)"}
	if got := f.run(t, append([]string{"exec"}, args[2:]...)...); !slices.Equal(got, args) {
		t.Fatalf("exec argv: %q", got)
	}
	if f.read(t, "input") != "fixture standard input\n" {
		t.Fatal("stdin not forwarded")
	}
	if got := f.run(t, "logs", "later-custom-container"); !slices.Equal(got, []string{"logs", "--tail", "100", "later-custom-container"}) {
		t.Fatalf("logs: %q", got)
	}
	if got := f.run(t, "connect", "my-mysql", "mysql", "--user=operator"); !slices.Equal(got, []string{"mysql", "--protocol=TCP", "--host=100.76.5.1", "--port=13306", "--password", "--user=operator"}) {
		t.Fatalf("mysql: %q", got)
	}
	f.write(t, "ports", "6379/tcp -> [::]:16379\n5432/tcp -> 0.0.0.0:15432\n")
	out, err := f.call(t, nil, `exec bash "$RECORD/sf-docker" connect mixed`)
	if err == nil || !strings.Contains(out, "多种数据库") {
		t.Fatalf("ambiguous: %v %s", err, out)
	}
	if got := f.run(t, "connect", "mixed", "redis", "--askpass"); !slices.Equal(got, []string{"redis-cli", "-h", "::1", "-p", "16379", "--askpass"}) {
		t.Fatalf("redis: %q", got)
	}
	if got := f.run(t, "connect", "mixed", "postgres", "-U", "app"); !slices.Equal(got, []string{"psql", "-h", "127.0.0.1", "-p", "15432", "-U", "app"}) {
		t.Fatalf("psql: %q", got)
	}
	out, err = f.call(t, []string{"DENY_DOCKER=1"}, `exec bash "$RECORD/sf-docker" java -version`)
	if err == nil || !strings.Contains(out, "permission denied") {
		t.Fatalf("daemon error must stay visible: %v %s", err, out)
	}
}

func TestHostTerminalToolsUseLocalImageAndWorkspace(t *testing.T) {
	f := newTerminalFixture(t)
	got := f.run(t, "mvn", "test", "-Dname=a b")
	for _, key := range []string{"--pull=never", "--user", "--workdir", "--mount", "MAVEN_OPTS=-Duser.home=/tmp", "MAVEN_CONFIG=/tmp/.m2", f.mvn} {
		if !slices.Contains(got, key) {
			t.Fatalf("missing %s: %q", key, got)
		}
	}
	if !slices.Equal(got[len(got)-5:], []string{"--entrypoint", "mvn", f.mvn, "test", "-Dname=a b"}) {
		t.Fatalf("maven: %q", got)
	}
	if work := got[slices.Index(got, "--workdir")+1]; !strings.HasSuffix(work, "/work, project") || !slices.Contains(got, `type=bind,"source=`+work+`","target=`+work+`"`) {
		t.Fatalf("workspace mount: %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "home", ".m2")); err != nil {
		t.Fatal(err)
	}
	got = f.run(t, "python3", "-c", "print('two words')")
	if !slices.Equal(got[len(got)-5:], []string{"--entrypoint", "python3", f.python, "-c", "print('two words')"}) {
		t.Fatalf("python: %q", got)
	}
	f.write(t, "images", f.java21+"\n"+f.java17+"\n")
	out, err := f.call(t, nil, `exec bash "$RECORD/sf-docker" java -version`)
	if err == nil || !strings.Contains(out, "17 21") {
		t.Fatalf("ambiguous tool: %v %s", err, out)
	}
	got = f.run(t, "tool", "java", "21", "-version")
	if !slices.Equal(got[len(got)-4:], []string{"--entrypoint", "java", f.java21, "-version"}) {
		t.Fatalf("selected java: %q", got)
	}
	got = f.run(t, "run", "fixture:local", "custom-cli", "-V")
	if !slices.Equal(got[len(got)-4:], []string{"--entrypoint", "custom-cli", f.java21, "-V"}) {
		t.Fatalf("generic local run: %q", got)
	}
	for _, arguments := range [][]string{{"tool", "java", "99"}, {"run", "not:local", "test"}, {"exec", "--privileged", "test"}} {
		if _, err := f.call(t, nil, `exec bash "$RECORD/sf-docker" "$@"`, arguments...); err == nil {
			t.Fatalf("unexpected success %q", arguments)
		}
	}
	if strings.Contains(f.read(t, "calls"), "pull") {
		t.Fatal("terminal pulled an image")
	}
}

func TestHostTerminalShortcutPrefersNewNativeProgram(t *testing.T) {
	f := newTerminalFixture(t)
	launcher := strings.Replace(f.read(t, "sf-docker"), "native=/usr/bin/$command", "native=$RECORD/native-$command", 1)
	f.write(t, "java", launcher)
	out, err := f.call(t, nil, `exec bash "$RECORD/java" -version`)
	if err != nil || !strings.Contains(f.read(t, "args"), f.java17) {
		t.Fatalf("shortcut did not select its installed image: %v %s", err, out)
	}
	f.write(t, "native-java", "#!/usr/bin/env bash\nprintf 'system java %s' \"$1\"\n")
	out, err = f.call(t, []string{"DENY_DOCKER=1"}, `exec bash "$RECORD/java" -version`)
	if err != nil || out != "system java -version" {
		t.Fatalf("native shortcut: %v %s", err, out)
	}
	out, err = f.call(t, []string{"DENY_DOCKER=1"}, `exec bash "$RECORD/sf-docker" java -version`)
	if err == nil || !strings.Contains(out, "permission denied") {
		t.Fatalf("explicit Docker entry unexpectedly used native client: %v %s", err, out)
	}
}

func TestHostTerminalInteractiveExec(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux pseudo terminal")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("util-linux script unavailable")
	}
	f := newTerminalFixture(t)
	out, err := f.call(t, []string{"OMIT_STDIN=1"}, `exec script -qec 'bash "$RECORD/sf-docker" exec demo app-cli' /dev/null`)
	if err != nil || !slices.Equal(strings.Split(f.read(t, "args"), "\x00"), []string{"exec", "-i", "-t", "demo", "app-cli", ""}) {
		t.Fatalf("interactive tty: %v %s", err, out)
	}
}
