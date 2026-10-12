package deploy

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testArchive(t *testing.T, platform string, extra *tar.Header) (string, string, int64, string) {
	t.Helper()
	var lb bytes.Buffer
	lt := tar.NewWriter(&lb)
	lt.WriteHeader(&tar.Header{Name: "app/app.jar", Typeflag: tar.TypeReg, Mode: 0644, Size: 4})
	lt.Write([]byte("test"))
	lt.Close()
	layer := lb.Bytes()
	layerHash := sha256.Sum256(layer)
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": platform, "rootfs": map[string]any{"diff_ids": []string{"sha256:" + hex.EncodeToString(layerHash[:])}}})
	configHash := sha256.Sum256(config)
	confName := hex.EncodeToString(configHash[:]) + ".json"
	manifest, _ := json.Marshal([]map[string]any{{"Config": confName, "RepoTags": []string{"sfops/test:a"}, "Layers": []string{"layer/layer.tar"}}})
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for name, data := range map[string][]byte{confName: config, "manifest.json": manifest, "layer/layer.tar": layer} {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(data))}); e != nil {
			t.Fatal(e)
		}
		tw.Write(data)
	}
	if extra != nil {
		tw.WriteHeader(extra)
	}
	tw.Close()
	filename := filepath.Join(t.TempDir(), "image.tar")
	os.WriteFile(filename, b.Bytes(), 0600)
	hash := sha256.Sum256(b.Bytes())
	return filename, hex.EncodeToString(hash[:]), int64(b.Len()), "sha256:" + hex.EncodeToString(configHash[:])
}
func TestLayerRejectsNonTarAndTruncation(t *testing.T) {
	for _, b := range [][]byte{[]byte("not a layer"), bytes.Repeat([]byte("x"), 1024)} {
		if validateLayer(bytes.NewReader(b), int64(len(b))) == nil {
			t.Fatal("invalid nested layer accepted")
		}
	}
}
func TestArchiveIdentityAndUnsafeInputs(t *testing.T) {
	p, h, n, image := testArchive(t, "amd64", nil)
	actual, e := ValidateArchive(p, h, n)
	if e != nil || actual != image {
		t.Fatal(actual, e)
	}
	if _, e = ValidateArchive(p, h, n-1); e == nil {
		t.Fatal("size mismatch accepted")
	}
	if _, e = ValidateArchive(p, string(bytes.Repeat([]byte("0"), 64)), n); e == nil {
		t.Fatal("checksum mismatch accepted")
	}
	for _, hdr := range []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}, {Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}, {Name: "manifest.json", Typeflag: tar.TypeReg}} {
		p, h, n, _ = testArchive(t, "amd64", hdr)
		if _, e = ValidateArchive(p, h, n); e == nil {
			t.Fatalf("unsafe entry accepted %v", hdr)
		}
	}
	p, h, n, _ = testArchive(t, "arm64", nil)
	if _, e = ValidateArchive(p, h, n); e == nil {
		t.Fatal("wrong platform accepted")
	}
}
func TestConfigRejectsWildcardAndShellInput(t *testing.T) {
	c := Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/actuator/health", HealthSeconds: 30, DataCompatibility: "v1"}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.BindAddress = "0.0.0.0"
	if c.Validate() == nil {
		t.Fatal("wildcard accepted")
	}
	c.BindAddress = "127.0.0.1"
	c.Profile = "test;rm"
	if c.Validate() == nil {
		t.Fatal("shell-like profile accepted")
	}
	if StrictJSON([]byte(`{} rubbish`), new(Request)) == nil {
		t.Fatal("trailing input accepted")
	}
}
