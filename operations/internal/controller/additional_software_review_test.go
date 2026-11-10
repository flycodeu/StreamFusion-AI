package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestAdditionalSoftwareFullSignedInstallationEnvelopesFitTargetLimit(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(nil)
	for _, d := range additionalSoftwareDefinitions() {
		config := serviceinstall.Config{Name: d.Name, RuntimeMode: d.RuntimeModes[0], MemoryMiB: d.DefaultMemoryMiB, BindAddress: "127.0.0.1", HostPort: d.Port, Parameters: map[string]string{}, ParameterCredentials: map[string]serviceinstall.CredentialReference{}}
		secrets := map[string]string{}
		for _, parameter := range d.Parameters {
			if parameter.Type == "CREDENTIAL" {
				ref := serviceinstall.CredentialReference{ID: uuid.NewString(), Revision: "1"}
				config.ParameterCredentials[parameter.Key] = ref
				secrets[serviceinstall.SecretKey(ref.ID, ref.Revision)] = "test-only-secret-32-characters-long"
			}
		}
		values, err := serviceinstall.ResolveParameters(d.Parameters, nil, config.ParameterCredentials)
		if err != nil {
			t.Fatal(d.Key, err)
		}
		config.Parameters = values
		source := serviceinstall.Source{Type: d.SourceModes[0]}
		if source.Type == "ARCHIVE" {
			source.SHA256, source.ByteSize = strings.Repeat("a", 64), 1024
		}
		installation := serviceinstall.Request{SoftwareID: uuid.NewString(), BindingRevision: 1, Definition: d, Config: config, Source: source}
		if err = installation.Validate(); err != nil {
			t.Fatal(d.Key, err)
		}
		req := deploy.Request{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: uuid.NewString(), TargetID: uuid.NewString(), JobID: uuid.NewString(), Action: "SOFTWARE_INSTALL", SoftwareOperation: "SOFTWARE_INSTALL", DeadlineAt: "2026-11-10T00:00:00Z", Installation: &installation}
		req.InputHash = deploy.CommandRequestDigest(req)
		if err = deploy.SignCommandRequest(&req, private); err != nil {
			t.Fatal(err)
		}
		req.Secrets = secrets
		payload, _ := json.Marshal(req)
		definition, _ := json.Marshal(d)
		if len(definition) > 48<<10 || len(payload)+1 > 64<<10 {
			t.Fatalf("%s definition=%d envelope=%d bytes", d.Key, len(definition), len(payload)+1)
		}
		t.Logf("%s definition=%d envelope=%d", d.Key, len(definition), len(payload)+1)
	}
}

func TestAdditionalSoftwareOfflinePackageManifestMatchesEveryArchivePackage(t *testing.T) {
	bash := managementTestBash(t)
	var app additionalSoftware
	for _, candidate := range additionalSoftwareCatalog {
		if candidate.key == "postgresql" {
			app = candidate
		}
	}
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete-manifest", true: "duplicate-omits-dependency"}[duplicate], func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"config", "work", "fake-bin"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(bash, "-c", `cd "$1" && pwd -P`, "fixture", filepath.ToSlash(root))
			data, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(data))
			}
			posix := strings.TrimSpace(string(data))
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("config/ops-owner", "postgresql:fixture-id")
			write("fake-bin/apt-get", "#!/bin/bash\nexit 0\n")
			// Git Bash cannot reproduce Linux directory-mode changes on NTFS.
			write("fake-bin/install", "#!/bin/bash\nset -e\n[[ $1 == -d && $2 == -m ]]\nshift 3\nmkdir -p -- \"$@\"\n")
			write("fake-bin/dpkg-deb", `#!/bin/bash
case "$3" in
 Architecture) printf 'amd64\n';;
 Package) if [[ "$2" == *'/postgresql-18_'* ]]; then printf 'postgresql-18\n'; else printf 'review-dependency\n'; fi;;
 Version) printf '18.6-review\n';;
 *) exit 1;;
esac
`)
			a, b := "packages/postgresql-18_18.6_amd64.deb", "packages/review-dependency_1_amd64.deb"
			hash := func(value string) string {
				digest := sha256.Sum256([]byte(value))
				return hex.EncodeToString(digest[:])
			}
			manifest := fmt.Sprintf("%s  %s\n%s  %s\n", hash("primary"), a, hash("dependency"), b)
			if duplicate {
				manifest = fmt.Sprintf("%s  %s\n%s  %s\n", hash("primary"), a, hash("primary"), a)
			}
			var bundle bytes.Buffer
			gzipWriter := gzip.NewWriter(&bundle)
			tarWriter := tar.NewWriter(gzipWriter)
			for _, entry := range []struct{ name, data string }{{"SHA256SUMS", manifest}, {a, "primary"}, {b, "dependency"}} {
				if err := tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.data))}); err != nil {
					t.Fatal(err)
				}
				_, _ = tarWriter.Write([]byte(entry.data))
			}
			if err := tarWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gzipWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "bundle.tar.gz"), bundle.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			cmd = exec.Command(bash, "--noprofile", "--norc")
			cmd.Env = append(os.Environ(), "PATH="+posix+"/fake-bin:/usr/bin:/bin", "OPS_INSTALL_ROOT="+posix, "OPS_SOFTWARE_ID=fixture-id", "OPS_SOURCE=ARCHIVE", "OPS_ARTIFACT_PATH="+posix+"/bundle.tar.gz")
			cmd.Stdin = strings.NewReader("export PATH=\"$OPS_INSTALL_ROOT/fake-bin:/usr/bin:/bin\"; hash -r\n" + app.script("native-material"))
			output, err := cmd.CombinedOutput()
			if duplicate && err == nil {
				t.Fatal("duplicated manifest allowed an unverified dependency package", string(output))
			}
			if !duplicate && err != nil {
				t.Fatal("complete package manifest rejected", err, string(output))
			}
		})
	}
}

func TestAdditionalSoftwareReleasePackageContract(t *testing.T) {
	directory := os.Getenv("SFOPS_VERIFY_TARGET_PACKAGE_DIR")
	if directory == "" {
		t.Skip("set SFOPS_VERIFY_TARGET_PACKAGE_DIR to verify a built local target package")
	}
	material, err := readTargetPackage(directory)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := elf.Open(material.Binary.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer binary.Close()
	if binary.Class != elf.ELFCLASS64 || binary.Machine != elf.EM_X86_64 || binary.Data != elf.ELFDATA2LSB {
		t.Fatal("target package is not Linux amd64 ELF")
	}
	for _, segment := range binary.Progs {
		if segment.Type == elf.PT_INTERP {
			t.Fatal("target executor unexpectedly requires a dynamic loader")
		}
	}
	cmd := exec.Command(managementTestBash(t), "-n", filepath.ToSlash(material.Script.Path))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("target installation script syntax", err, string(output))
	}
	t.Logf("binary bytes=%d sha256=%s; script bytes=%d sha256=%s", material.Binary.Size, material.Binary.Hash, material.Script.Size, material.Script.Hash)
}
