package serviceinstall

import (
	"encoding/json"
	"strings"
	"testing"
)

func nativeConfigFixture(d Definition) Config {
	c := Config{Name: d.Name, RuntimeMode: "NATIVE", BindAddress: "10.0.0.8", HostPort: d.Port, MemoryMiB: 512}
	if d.Kind == "TOOL" {
		c.HostPort = 0
	} else if d.Key != "nginx" {
		c.CredentialID, c.CredentialRevision = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", "1"
	}
	if d.Key == "seaweedfs" {
		c.AccessKeyCredentialID, c.AccessKeyCredentialRevision = "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb", "1"
	}
	return c
}

func TestNativeCatalogUsesPinnedCompleteOfficialMaterials(t *testing.T) {
	for _, d := range Catalog() {
		t.Run(d.Key+"-"+d.Version, func(t *testing.T) {
			if d.Native == nil || len(d.Native.Parts) == 0 {
				t.Fatal("native recipe missing")
			}
			for _, p := range d.Native.Parts {
				if !strings.HasPrefix(p.URL, "https://") || !sha.MatchString(p.SHA256) || p.ByteSize < 1 {
					t.Fatalf("unfrozen material: %+v", p)
				}
			}
			c := nativeConfigFixture(d)
			if err := c.Validate(d); err != nil {
				t.Fatal(err)
			}
			if err := (Source{Type: "ONLINE"}).ValidateFor(d, c); err != nil {
				t.Fatal(err)
			}
			if (Source{Type: "REGISTRY"}).ValidateFor(d, c) == nil {
				t.Fatal("native registry accepted")
			}
		})
	}
}

func TestLegacyDefinitionsRetainSerializedIdentity(t *testing.T) {
	for _, old := range legacyDefinitions() {
		before, _ := json.Marshal(old)
		if err := ValidateDefinition(old); err != nil {
			t.Fatal(err)
		}
		after, _ := json.Marshal(old)
		if string(before) != string(after) || strings.Contains(string(before), `"native"`) {
			t.Fatal("legacy definition normalized")
		}
		old.ImageID = "sha256:" + strings.Repeat("0", 64)
		if ValidateDefinition(old) == nil {
			t.Fatal("legacy pin substitution accepted")
		}
	}
}

func TestNativePortsAndStorageAreExplicit(t *testing.T) {
	d, _ := Find("seaweedfs", "4.48")
	c := nativeConfigFixture(d)
	c.StorageRoot = "/srv/video-software"
	c.HostPort = 58333
	c.Ports = map[string]int{"master": 59333, "s3Grpc": 58334}
	if err := c.Validate(d); err != nil {
		t.Fatal(err)
	}
	mappings := PortMappings(d, c)
	seen := map[string]PortMapping{}
	for _, p := range mappings {
		seen[p.Key] = p
	}
	if seen["master"].BindAddress != "127.0.0.1" || seen["master"].HostPort != 59333 || seen["s3Grpc"].BindAddress != c.BindAddress || seen["s3Grpc"].HostPort != 58334 || seen["main"].HostPort != 58333 {
		t.Fatalf("implicit/wrong ports: %+v", mappings)
	}
	if InstallRoot("/opt/ops", "test-id", c) != "/srv/video-software/test-id" {
		t.Fatal("custom storage ignored")
	}
	for _, bad := range []string{"/srv/../etc", "/tmp/pkg", "/srv/path space", "C:/pkg", "/srv/", "/srv", "/srv/$(touch${IFS}/tmp/sfops-proof)", "/srv/`id`", "/srv/tab\tvalue", "/srv/%n", "/srv/quote'", "/srv/double\""} {
		if ValidStorageRoot(bad) {
			t.Fatalf("unsafe storage %q", bad)
		}
	}
}

func TestProtocolPortPairsCannotPartiallyEnable(t *testing.T) {
	for _, row := range []struct{ key, version, port string }{{"mediamtx", "1.21.2", "webrtc"}, {"zlm", "2026.10.21", "rtpTcp"}} {
		d, _ := Find(row.key, row.version)
		c := nativeConfigFixture(d)
		c.Ports = map[string]int{row.port: 18000}
		if c.Validate(d) == nil {
			t.Fatalf("partially enabled media protocol %s", row.key)
		}
	}
}

func TestConfigurationCannotMoveStorageOrReinitializeMySQL(t *testing.T) {
	d, _ := Find("mysql", "8.4.11")
	c := nativeConfigFixture(d)
	previous := Request{SoftwareID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", BindingRevision: 1, Definition: d, Config: c, Source: Source{Type: "ONLINE"}}
	change := ChangeRequest{Previous: previous, Config: c, ExpectedBindingRevision: 1}
	change.Config.HostPort = 13306
	if err := change.Validate(); err != nil {
		t.Fatal(err)
	}
	change.Config.CredentialRevision = "2"
	if change.Validate() == nil {
		t.Fatal("mysql root password reference silently changed")
	}
	change.Config = c
	change.Config.StorageRoot = "/srv/mysql"
	if change.Validate() == nil {
		t.Fatal("data root moved by configuration")
	}
}

func TestToolReceiptCannotClaimRunningContainer(t *testing.T) {
	d, _ := Find("java", "21")
	c := nativeConfigFixture(d)
	c.RuntimeMode = "DOCKER"
	r := Request{SoftwareID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", BindingRevision: 1, Definition: d, Config: c}
	e := Evidence{SoftwareID: r.SoftwareID, BindingRevision: 1, RecipeKey: d.Key, RecipeRevision: d.Revision, ConfigHash: Digest(c), Kind: "TOOL", Locator: "image:" + d.ImageID, ImageID: d.ImageID, IdentityHash: strings.Repeat("1", 64)}
	if err := e.ValidateFor(r); err != nil {
		t.Fatal(err)
	}
	e.ContainerID = strings.Repeat("1", 64)
	if e.ValidateFor(r) == nil {
		t.Fatal("tool container receipt accepted")
	}
}
