package deploy

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func previewTestConfig() Config {
	return Config{Environment: "preview", BindAddress: "192.0.2.5", HostPort: 9443, ContainerPort: 9443, MemoryMiB: 8192, HealthPath: "/preview-control/v1/snapshot", HealthSeconds: 60, DataCompatibility: "preview-ownership-v1", Preview: &PreviewConfig{NodeID: "a", ControlHost: "192.0.2.5", MediaAddress: "192.0.2.5", AllowedTargets: []string{"192.0.2.10:554"}, CPUs: 8, MaxSources: 64, MaxViewers: 64, MaxTranscodes: 64, MaxStarting: 4, IngressKbps: 655360, EgressKbps: 655360, SourceUpperKbps: 8192, OutputUpperKbps: 8192}}
}

func TestPreviewFixedRecipeLimitsAndTransition(t *testing.T) {
	good := previewTestConfig()
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Preview.MaxViewers = 65 }, func(c *Config) { c.Preview.MaxTranscodes = 65 },
		func(c *Config) { c.Preview.AllowedTargets = []string{"rtsp://secret@192.0.2.10:554/live"} },
		func(c *Config) { c.Preview.AllowedTargets = []string{"hostname:554"} },
		func(c *Config) { c.Preview.AllowedTargets = []string{"0.0.0.0:554"} },
		func(c *Config) { c.HostPort = 22 }, func(c *Config) { c.BindAddress = "0.0.0.0" },
		func(c *Config) { c.InitialPasswordCredentialID = "secret" }, func(c *Config) { c.BindingIDs = []string{"anything"} },
	} {
		c := previewTestConfig()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatalf("unsafe config accepted %+v", c)
		}
	}
	base := &Release{Config: good}
	candidate := &Release{Config: previewTestConfig()}
	permit := "11111111-1111-4111-8111-111111111111"
	if ValidatePreviewTransition(candidate, base, "") == nil {
		t.Fatal("maintenance permit bypass")
	}
	if err := ValidatePreviewTransition(candidate, base, permit); err != nil {
		t.Fatal(err)
	}
	candidate.Config.Preview.NodeID = "b"
	if ValidatePreviewTransition(candidate, base, permit) == nil {
		t.Fatal("identity replacement allowed")
	}
	candidate.Config.Preview = nil
	if ValidatePreviewTransition(candidate, base, permit) == nil {
		t.Fatal("recipe replacement allowed")
	}
	if ArchiveLimitFor("java-compose-v1") != 512<<20 || ArchiveLimitFor(PreviewRecipe) != 2<<30 {
		t.Fatal("archive limits changed")
	}
}

func TestPreviewArchiveRequiresMaintenanceCapableImageAndSeparateNamespace(t *testing.T) {
	for _, capability := range []string{"", "1"} {
		source, _, _, _ := testArchive(t, "amd64", nil)
		f, _ := os.Open(source)
		tr := tar.NewReader(f)
		files := map[string][]byte{}
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			files[h.Name], _ = io.ReadAll(tr)
		}
		f.Close()
		var manifest []map[string]any
		_ = json.Unmarshal(files["manifest.json"], &manifest)
		old := manifest[0]["Config"].(string)
		var config map[string]any
		_ = json.Unmarshal(files[old], &config)
		config["config"] = map[string]any{"Labels": map[string]string{"com.streamfusion.preview.maintenance": capability}}
		data, _ := json.Marshal(config)
		hash := sha256.Sum256(data)
		name := hex.EncodeToString(hash[:]) + ".json"
		delete(files, old)
		files[name] = data
		manifest[0]["Config"] = name
		manifest[0]["RepoTags"] = []string{"streamfusion/preview-node:s4-test"}
		files["manifest.json"], _ = json.Marshal(manifest)
		var out bytes.Buffer
		tw := tar.NewWriter(&out)
		for name, data := range files {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(data))})
			_, _ = tw.Write(data)
		}
		_ = tw.Close()
		path := filepath.Join(t.TempDir(), "preview.tar")
		_ = os.WriteFile(path, out.Bytes(), 0600)
		sum := sha256.Sum256(out.Bytes())
		expected := hex.EncodeToString(sum[:])
		_, err := ValidateRecipeArchive(path, expected, int64(out.Len()), PreviewRecipe)
		if (err == nil) != (capability == "1") {
			t.Fatalf("capability %q: %v", capability, err)
		}
		if _, err = ValidateArchive(path, expected, int64(out.Len())); err == nil {
			t.Fatal("preview image accepted as Java")
		}
	}
}
