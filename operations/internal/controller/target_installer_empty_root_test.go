package controller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTargetInstallerEmptyDirectoryKeepsExistingContentAndRejectsRaces(t *testing.T) {
	bash := additionalTestBash(t)
	for _, scenario := range []string{"missing", "empty-account", "empty-root", "nonempty", "foreign-owner", "unsafe-parent", "symlink", "mount", "concurrent-file"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			root := filepath.Join(directory, "managed")
			if scenario != "missing" {
				if err := os.Mkdir(root, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "nonempty" {
				if err := os.WriteFile(filepath.Join(root, "untouched"), []byte("existing data"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				other := filepath.Join(directory, "actual")
				if err := os.Rename(root, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, root); err != nil {
					t.Skip("symlink permission unavailable")
				}
			}
			// Stat/UID responses model the protected Linux parent chain. All
			// directory removal and race checks use real isolated filesystem calls.
			script := `set -euo pipefail
root=$(realpath -m -- "$1")
scenario=$2
fail(){ printf '%s\n' "$1" >&2; exit 1; }
id(){ printf '1000\n'; }
stat(){
  if [[ $2 == %u ]]; then
    if [[ ${4:-} == "$root" ]]; then
      case "$scenario" in empty-root) printf '0\n';; foreign-owner) printf '2000\n';; *) printf '1000\n';; esac
    else printf '0\n'; fi
  elif [[ $scenario == unsafe-parent ]]; then printf '777\n'; else printf '755\n'; fi
}
rmdir(){
  if [[ $scenario == mount ]]; then return 1; fi
  if [[ $scenario == concurrent-file ]]; then printf 'concurrent data' > "$root/raced"; fi
  command rmdir "$@"
}
` + strings.Replace(controllerScript("preparation/empty-root.sh"), `parent=$(dirname -- "$root")`, "parent=/", 1) + `
prepare_empty_root "$root" ubuntu
[[ -e "$root" ]] && printf 'existing\n' || printf 'create\n'
`
			if scenario == "symlink" {
				// Keep the final component unresolved so the real link guard runs.
				script = strings.Replace(script, `root=$(realpath -m -- "$1")`, `root="$(cd -- "$(dirname -- "$1")" && pwd)/$(basename -- "$1")"`, 1)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", script, "empty-root-fixture", filepath.ToSlash(root), scenario)
			output, err := command.CombinedOutput()
			allowed := scenario == "missing" || scenario == "empty-account" || scenario == "empty-root"
			if allowed {
				if err != nil || string(output) != "create\n" {
					t.Fatalf("empty root not prepared: %v %s", err, output)
				}
				if _, err = os.Lstat(root); !os.IsNotExist(err) {
					t.Fatal("root still exists", err)
				}
			} else if scenario == "nonempty" {
				data, readErr := os.ReadFile(filepath.Join(root, "untouched"))
				if err != nil || string(output) != "existing\n" || readErr != nil || string(data) != "existing data" {
					t.Fatal("nonempty directory changed", err, string(output), readErr)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe directory accepted", string(output))
				}
				if _, err = os.Lstat(root); err != nil {
					t.Fatal("rejected directory removed", err)
				}
				if scenario == "concurrent-file" {
					data, readErr := os.ReadFile(filepath.Join(root, "raced"))
					if readErr != nil || string(data) != "concurrent data" {
						t.Fatal("concurrent data lost", readErr)
					}
				}
			}
		})
	}
}
