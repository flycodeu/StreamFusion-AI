package serviceinstall

import "testing"

func TestSuggestDirectoryNameAndShortRootCompatibility(t *testing.T) {
	id := "f3847ece-b060-4fcc-9600-39cbefbd972f"
	base := "/opt/streamfusion-ops-target"

	// Historical record without DirectoryName keeps exact UUID path.
	legacyCfg := Config{Name: "nginx", BindAddress: "127.0.0.1", HostPort: 80, MemoryMiB: 256}
	if got := InstallRoot(base, id, legacyCfg); got != base+"/software/"+id {
		t.Fatalf("legacy InstallRoot mismatch: %s", got)
	}
	nginxDef := Definition{Key: "nginx", Name: "Nginx", Version: "1.30.6", Revision: "nginx-docker-v1", Port: 80, RuntimeModes: []string{"DOCKER"}}
	nginxFull, _ := Find("nginx", "1.30.6")
	legacyReq := Request{SoftwareID: id, BindingRevision: 1, Definition: nginxFull, Config: legacyCfg, Source: Source{Type: "REGISTRY"}}
	if got := legacyReq.InstallRoot(base); got != base+"/software/"+id {
		t.Fatalf("legacy Request.InstallRoot mismatch: %s", got)
	}

	// Port-based software produces <slug>-<port> and increments on collision.
	if got := SuggestDirectoryName(nginxDef, legacyCfg, id, nil); got != "nginx-80" {
		t.Fatalf("expected nginx-80, got %s", got)
	}
	if got := SuggestDirectoryName(nginxDef, Config{Name: "nginx-2", HostPort: 8081}, id, map[string]bool{"nginx-80": true}); got != "nginx-8081" {
		t.Fatalf("expected nginx-8081, got %s", got)
	}

	// Portless software uses major/minor version and handles collisions cleanly.
	javaDef := Definition{Key: "java", Name: "Java", Kind: "TOOL", Version: "21.0.4"}
	javaCfg := Config{Name: "Java 21", MemoryMiB: 512}
	if got := SuggestDirectoryName(javaDef, javaCfg, id, nil); got != "java-21" {
		t.Fatalf("expected java-21, got %s", got)
	}
	if got := SuggestDirectoryName(javaDef, javaCfg, id, map[string]bool{"java-21": true}); got != "java-21-2" {
		t.Fatalf("expected java-21-2, got %s", got)
	}
	pythonDef := Definition{Key: "python", Name: "Python", Kind: "TOOL", Version: "3.12.7"}
	if got := SuggestDirectoryName(pythonDef, Config{Name: "Python 3.12", MemoryMiB: 256}, id, nil); got != "python-3-12" {
		t.Fatalf("expected python-3-12, got %s", got)
	}

	// Disk conflict suffix uses short UUID prefix within 64 chars.
	withID := DirectoryNameWithShortID("nginx-80", id)
	if withID != "nginx-80-f3847e" {
		t.Fatalf("expected nginx-80-f3847e, got %s", withID)
	}

	// InstallRootFor / Request.InstallRoot respect DirectoryName for both default software/ dir and custom StorageRoot.
	if got := InstallRootFor(base, id, "nginx-80", legacyCfg); got != base+"/software/nginx-80" {
		t.Fatalf("short InstallRootFor mismatch: %s", got)
	}
	shortReq := legacyReq
	shortReq.DirectoryName = "nginx-80"
	if got := shortReq.InstallRoot(base); got != base+"/software/nginx-80" {
		t.Fatalf("short Request.InstallRoot mismatch: %s", got)
	}
	if err := shortReq.Validate(); err != nil {
		t.Fatalf("expected valid short Request, got %v", err)
	}
	customCfg := legacyCfg
	customCfg.StorageRoot = "/srv/apps"
	if got := InstallRootFor(base, id, "nginx-80", customCfg); got != "/srv/apps/nginx-80" {
		t.Fatalf("custom storage short InstallRootFor mismatch: %s", got)
	}

	// Config Digest remains strictly invariant regardless of DirectoryName on Request.
	if Digest(legacyReq.Config) != Digest(shortReq.Config) {
		t.Fatalf("DirectoryName changed Config digest")
	}

	// Invalid DirectoryName values are rejected by ValidDirectoryName and Request.Validate.
	for _, bad := range []string{"../etc", "/abs", "a/b", ".hidden", "trail.", "UpperCase", "bad space"} {
		if ValidDirectoryName(bad) {
			t.Fatalf("expected %q to fail ValidDirectoryName", bad)
		}
		badReq := shortReq
		badReq.DirectoryName = bad
		if err := badReq.Validate(); err == nil {
			t.Fatalf("expected invalid Request.DirectoryName %q to be rejected", bad)
		}
	}
}
