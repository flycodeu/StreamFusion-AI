package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func nativeTarFixture(t *testing.T, headers []*tar.Header, contents []string) string {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for index, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if index < len(contents) {
			if _, err := tw.Write([]byte(contents[index])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "part.tgz")
	if err := os.WriteFile(file, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestNativeExtractorRejectsPathsLinksSpecialAndExpansion(t *testing.T) {
	for _, row := range []struct {
		name    string
		header  tar.Header
		content string
	}{
		{"traversal", tar.Header{Name: "../escape", Mode: 0600, Size: 1}, "x"},
		{"absolute", tar.Header{Name: "/etc/escape", Mode: 0600, Size: 1}, "x"},
		{"symlink", tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"}, ""},
		{"device", tar.Header{Name: "device", Typeflag: tar.TypeChar}, ""},
		{"expansion", tar.Header{Name: "file", Mode: 0600, Size: 256 << 10}, strings.Repeat("0", 256<<10)},
	} {
		t.Run(row.name, func(t *testing.T) {
			file := nativeTarFixture(t, []*tar.Header{&row.header}, []string{row.content})
			if extractNativePart(context.Background(), serviceinstall.NativePart{Format: "TAR_GZIP"}, file, filepath.Join(t.TempDir(), "out")) == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestNativeExtractorPreservesVendorBinaryAndRejectsDuplicate(t *testing.T) {
	header := tar.Header{Name: "vendor/bin/tool", Mode: 0755, Size: 9, Typeflag: tar.TypeReg}
	file := nativeTarFixture(t, []*tar.Header{&header}, []string{"vendorbin"})
	out := filepath.Join(t.TempDir(), "out")
	if err := extractNativePart(context.Background(), serviceinstall.NativePart{Format: "TAR_GZIP", StripComponents: 1}, file, out); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(out, "bin/tool"))
	if err != nil || string(content) != "vendorbin" {
		t.Fatal("vendor binary not preserved")
	}
	file = nativeTarFixture(t, []*tar.Header{&header, &header}, []string{"vendorbin", "vendorbin"})
	if extractNativePart(context.Background(), serviceinstall.NativePart{Format: "TAR_GZIP", StripComponents: 1}, file, filepath.Join(t.TempDir(), "out")) == nil {
		t.Fatal("duplicate archive path accepted")
	}
}

func TestNativeUnitAndProtocolTemplatesStayNonrootAndBounded(t *testing.T) {
	_, req := serviceRequestFixture(t)
	req.Installation.Config.RuntimeMode = "NATIVE"
	unit := renderNativeUnit(req, "/srv/pkg/id", "sfopsfixture")
	for _, want := range []string{"User=sfopsfixture", "KillMode=control-group", "ProtectSystem=strict", "NoNewPrivileges=true", "MemoryMax=256M", "StandardOutput=journal", "ReadWritePaths=/srv/pkg/id/data /srv/pkg/id/logs"} {
		if !strings.Contains(unit, want) {
			t.Fatal(want)
		}
	}
	d, _ := serviceinstall.Find("zlm", "2026.10.21")
	req.Installation.Definition = d
	args := strings.Join(nativeServiceArguments(*req.Installation, "/srv/pkg/id"), " ")
	if !strings.Contains(args, "--log-dir "+filepath.Join("/srv/pkg/id", "logs")) || !strings.Contains(args, "--log-slice 3") {
		t.Fatal("unbounded/native unwritable ZLM logs")
	}
	conf := zlmConfiguration(*req.Installation, "fixture-secret", "/srv/pkg/id/data", true)
	if !strings.Contains(conf, "listen_ip=127.0.0.1") || !strings.Contains(conf, "[shell]\nport=0") {
		t.Fatal("ZLM binding defaults leaked")
	}
	d, _ = serviceinstall.Find("mediamtx", "1.21.2")
	req.Installation.Definition = d
	var media map[string]any
	if json.Unmarshal(mediaMTXConfiguration(*req.Installation, "secret", true), &media) != nil {
		t.Fatal("invalid config")
	}
	for _, key := range []string{"moq", "metrics", "pprof", "playback", "hls", "rtmp", "webrtc", "srt"} {
		if media[key] != false {
			t.Fatalf("implicit listener %s", key)
		}
	}
}

func TestNativeInterruptedPreparationAndConfigurationNeverReplay(t *testing.T) {
	x, req := serviceRequestFixture(t)
	req.Installation.Config.RuntimeMode = "NATIVE"
	dir := t.TempDir()
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
		t.Fatal("interrupted preparation replayed a command")
		return nil, nil
	}
	marker := nativeInstallation{SoftwareID: req.Installation.SoftwareID, JobID: req.JobID, ConfigHash: Digest(req.Installation.Config)}
	body, _ := json.Marshal(marker)
	if err := os.WriteFile(filepath.Join(dir, "native-owned.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if result := x.reconcileNative(req, dir); result.Status != "FAILED" || result.Phase != "NATIVE_NOT_STARTED" {
		t.Fatalf("%+v", result)
	}
	req.Reconfiguration = &serviceinstall.ChangeRequest{Previous: *req.Installation, Config: req.Installation.Config, ExpectedBindingRevision: 1}
	req.Installation = nil
	req.Software = &SoftwareRequest{ID: req.Reconfiguration.Previous.SoftwareID}
	cm := configurationMarker{JobID: req.JobID, InputHash: req.InputHash, Previous: *req.Software}
	body, _ = json.Marshal(cm)
	if err := os.WriteFile(filepath.Join(dir, "configuration.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if result := x.reconcileConfiguration(req, dir); result.Status != "UNKNOWN" {
		t.Fatal("unconfirmed stop/create treated as terminal")
	}
}

func TestDockerToolBridgeRecoveryOnlyObservesPinnedImage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	x, req := serviceRequestFixture(t)
	d, _ := serviceinstall.Find("java", "21")
	req.Installation.Definition = d
	req.Installation.Config.HostPort = 0
	old, err := toolPluginDefinition(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(x.root, "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(x.root, "software"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(x.root, "plugins", req.Installation.SoftwareID+".json"), pluginMaterial{InstallationID: req.Installation.SoftwareID, Definition: old, Managed: true, JobID: req.JobID, ImageID: d.ImageID}); err != nil {
		t.Fatal(err)
	}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "image inspect") {
			return json.Marshal(map[string]string{"id": d.ImageID, "os": "linux", "arch": "amd64"})
		}
		if strings.HasPrefix(joined, "run --rm --pull never --network none") && strings.Contains(joined, d.ImageID) {
			return []byte("openjdk version \"21.0.12\""), nil
		}
		return nil, errors.New("unexpected mutation: " + command + " " + joined)
	}
	result := x.reconcileDockerTool(req, t.TempDir())
	if result.Status != "SUCCEEDED" || result.Software.State != "INSTALLED" {
		t.Fatalf("%+v", result)
	}
	if x.verifyOwnedInstallation(req, result) != nil {
		t.Fatal("bridge/binding recovery not verifiable")
	}
}

func TestNativeDTLSCertificateHasUniqueKeyAndRejectsMissingMaterial(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.pem")
	second := filepath.Join(t.TempDir(), "second.pem")
	for _, file := range []string{first, second} {
		// Windows cannot apply the Linux root/group ownership policy, but the
		// generated bytes remain fully verifiable without target installation.
		err := createNativeCertificate(file, "instance-test", 0)
		if err != nil && runtime.GOOS == "linux" {
			t.Fatal(err)
		}
		if err = validateNativeCertificate(file); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if bytes.Equal(a, b) {
		t.Fatal("shared DTLS certificate/private key")
	}
	if validateNativeCertificate(filepath.Join(t.TempDir(), "missing.pem")) == nil {
		t.Fatal("missing certificate accepted")
	}
	if err := os.WriteFile(first, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	if validateNativeCertificate(first) == nil {
		t.Fatal("invalid DTLS key accepted")
	}
}

func TestNativePathsRejectCommandAndSystemdSubstitution(t *testing.T) {
	for _, value := range []string{"/srv/$(touch${IFS}/tmp/sfops-proof)/bin/mvn", "/srv/`id`/bin/java", "/srv/%n/bin/tool", "/srv/a b/tool", "/srv/a'quote/tool"} {
		if validNativeBinaryPath(value) {
			t.Fatalf("unsafe native executable path: %s", value)
		}
	}
	if got := posixSingleQuote("a'$(id)`id`%"); got != "'a'\"'\"'$(id)`id`%'" {
		t.Fatalf("unsafe shell quoting: %s", got)
	}
	wrapper := nativeMavenWrapper("/srv/$(touch${IFS}/tmp/sfops-proof)")
	if !strings.Contains(wrapper, "JAVA_HOME="+posixSingleQuote(filepath.Join("/srv/$(touch${IFS}/tmp/sfops-proof)", "runtime/jdk"))) || strings.Contains(wrapper, "JAVA_HOME=\"") {
		t.Fatal("Maven wrapper enables command substitution")
	}
}

func TestConfigurationEarlyRejectionHasDurableNotAppliedReceipt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("target durable directory fsync requires Linux")
	}
	x, req := serviceRequestFixture(t)
	previous := *req.Installation
	s := SoftwareRequest{ID: previous.SoftwareID, Version: 1, Kind: "DOCKER", Locator: strings.Repeat("a", 64), IdentityHash: strings.Repeat("b", 64), SoftwareKey: "NGINX", Ownership: x.installOwnership(req)}
	req.Software = &s
	req.Installation = nil
	req.Reconfiguration = &serviceinstall.ChangeRequest{Previous: previous, Config: previous.Config, ExpectedBindingRevision: 1}
	req.Action, req.SoftwareOperation, req.InputHash = "SOFTWARE_CONFIGURE", "SOFTWARE_CONFIGURE", ""
	req.InputHash = Digest(req)
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
		t.Fatal("rejected binding caused a live command")
		return nil, nil
	}
	var out bytes.Buffer
	if err := x.softwareLocked(req, &out); err != nil {
		t.Fatal(err)
	}
	var result Result
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Status != "FAILED" || result.Phase != "CONFIGURATION_NOT_APPLIED" || result.Installation != nil {
		t.Fatalf("%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(x.root, "jobs", req.JobID, "configuration.json")); !os.IsNotExist(err) {
		t.Fatal("early rejection crossed stop boundary")
	}
	req.Action = "ACK"
	req.ResultHash = Digest(result)
	out.Reset()
	if err := x.softwareLocked(req, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("early rejection cannot release pending job")
	}
}
