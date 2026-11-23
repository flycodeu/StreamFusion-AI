package controller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestJenkinsDockerInstallUsesVerifiedImageOnBothDockerStores(t *testing.T) {
	bash := managementTestBash(t)
	var online, offline serviceinstall.Definition
	for _, definition := range additionalSoftwareDefinitions() {
		switch definition.Key {
		case "custom-jenkins-docker-online":
			online = definition
		case "custom-jenkins-docker-offline":
			offline = definition
		}
	}
	if online.Commands == nil || offline.Commands == nil || online.ImageID == "" || online.Digest == "" {
		t.Fatal("missing Jenkins Docker recipes or identity pins")
	}
	for _, tc := range []struct {
		name, source, resolvedID, platform string
		cached, wantPull, wantRun          bool
	}{
		{name: "classic store", source: "REGISTRY", resolvedID: online.ImageID, platform: "linux/amd64", wantPull: true, wantRun: true},
		{name: "containerd store", source: "REGISTRY", resolvedID: online.Digest, platform: "linux/amd64", wantPull: true, wantRun: true},
		{name: "already cached", source: "REGISTRY", resolvedID: online.Digest, platform: "linux/amd64", cached: true, wantRun: true},
		{name: "unexpected image identity", source: "REGISTRY", resolvedID: "sha256:" + strings.Repeat("6", 64), platform: "linux/amd64", wantPull: true},
		{name: "unexpected image platform", source: "REGISTRY", resolvedID: online.Digest, platform: "linux/arm64", wantPull: true},
		{name: "archive exact config", source: "ARCHIVE", resolvedID: offline.ImageID, platform: "linux/amd64", wantRun: true},
		{name: "archive rejects manifest identity", source: "ARCHIVE", resolvedID: offline.Digest, platform: "linux/amd64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := online
			if tc.source == "ARCHIVE" {
				definition = offline
			}
			root := t.TempDir()
			for _, directory := range []string{"config", "data"} {
				if err := os.Mkdir(filepath.Join(root, directory), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "artifact.tar"), []byte("docker load fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			rootCommand := exec.Command(bash, "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(root))
			rootOutput, err := rootCommand.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(rootOutput))
			}
			posix := strings.TrimSpace(string(rootOutput))
			if tc.cached {
				if err := os.WriteFile(filepath.Join(root, "image-ready"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			registryRef := "docker.io/jenkins/jenkins@" + definition.Digest
			imageRef := registryRef
			if tc.source == "ARCHIVE" {
				imageRef = definition.ImageID
			}
			environment := append(os.Environ(),
				"OPS_INSTALL_ROOT="+posix, "OPS_SOFTWARE_ID=jenkins-fixture", "OPS_SOURCE="+tc.source,
				"OPS_ARTIFACT_PATH="+posix+"/artifact.tar", "OPS_MEMORY_MIB=2048", "OPS_BIND_ADDRESS=127.0.0.1", "OPS_HOST_PORT=18080",
				"FIXTURE_REGISTRY_REF="+registryRef, "FIXTURE_REGISTRY_TAG_REF="+definition.Image+"@"+definition.Digest,
				"FIXTURE_IMAGE_REF="+imageRef, "FIXTURE_CONFIG_ID="+definition.ImageID,
				"FIXTURE_IMAGE_ID="+tc.resolvedID, "FIXTURE_PLATFORM="+tc.platform,
			)
			steps := definition.Commands.Steps
			if definition.Management == nil || len(steps) < 2 {
				t.Fatal("missing Jenkins signed command context or install steps")
			}
			for index, step := range steps[:2] {
				if step.Phase != "INSTALL" {
					t.Fatal("expected two Jenkins install steps")
				}
				cmd := exec.Command(bash, "--noprofile", "--norc")
				cmd.Env = environment
				cmd.Stdin = strings.NewReader(jenkinsDockerImageFixture + "\n" + definition.Management.ScriptPrelude + "\n" + step.Script)
				output, err := cmd.CombinedOutput()
				if err != nil {
					if tc.wantRun {
						t.Fatalf("%s: %v\n%s", step.Name, err, output)
					}
					break
				}
				if index == 0 && !tc.wantRun {
					t.Fatal("material validation accepted an unexpected image before container creation")
				}
			}
			logBytes, err := os.ReadFile(filepath.Join(root, "docker.log"))
			if err != nil {
				t.Fatal(err)
			}
			log := string(logBytes)
			if got := strings.Contains(log, "\npull "); got != tc.wantPull {
				t.Fatalf("unexpected registry fetch\n%s", log)
			}
			if tc.wantPull && !strings.Contains(log, "\npull --platform linux/amd64 "+registryRef+"\n") {
				t.Fatalf("expected the fixed repository digest\n%s", log)
			}
			if got := strings.Contains(log, "\nload "); got != (tc.source == "ARCHIVE") {
				t.Fatalf("unexpected offline import\n%s", log)
			}
			if got := strings.Contains(log, "\nrun "); got != tc.wantRun {
				t.Fatalf("unexpected container creation\n%s", log)
			}
			if !strings.Contains(log, " "+imageRef+"\n") {
				t.Fatalf("expected the source-specific image identity\n%s", log)
			}
		})
	}
}

const jenkinsDockerImageFixture = `
curl() { return 0; }
chown() { return 0; }
docker() {
  printf '%s\n' "$*" >> "$OPS_INSTALL_ROOT/docker.log"
  case "$1" in
    info) printf 'linux\n' ;;
    image)
      [[ "$2" == inspect && -f "$OPS_INSTALL_ROOT/image-ready" ]] || return 1
      local reference="${!#}"
      if [[ "$reference" != "$FIXTURE_IMAGE_REF" && "$reference" != "$FIXTURE_CONFIG_ID" ]] ||
         [[ "$reference" == "$FIXTURE_CONFIG_ID" && "$OPS_SOURCE" == REGISTRY && "$FIXTURE_IMAGE_ID" != "$FIXTURE_CONFIG_ID" ]]; then
        printf 'Error response from daemon: No such image: %s\n' "$reference" >&2
        return 1
      fi
      printf '%s %s\n' "$FIXTURE_IMAGE_ID" "$FIXTURE_PLATFORM"
      ;;
    pull)
      [[ "$*" == "pull --platform linux/amd64 $FIXTURE_REGISTRY_REF" || "$*" == "pull --platform linux/amd64 $FIXTURE_REGISTRY_TAG_REF" ]] || return 1
      touch "$OPS_INSTALL_ROOT/image-ready"
      ;;
    load)
      [[ "$*" == "load --input $OPS_ARTIFACT_PATH" ]] || return 1
      touch "$OPS_INSTALL_ROOT/image-ready"
      ;;
    ps) ;;
    run)
      [[ "${!#}" == "$FIXTURE_IMAGE_REF" ]] || return 1
      if [[ "$OPS_SOURCE" == REGISTRY ]]; then
        [[ " $* " == *' --pull=never '* || " $* " == *' --pull never '* ]] || return 1
        [[ " $* " == *' --platform linux/amd64 '* ]] || return 1
      fi
      printf 'container-id\n'
      ;;
    *) return 1 ;;
  esac
}
`
