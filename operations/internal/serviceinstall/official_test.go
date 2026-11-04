package serviceinstall

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOfficialCatalogRemainsSeparateAndPlansAreFrozen(t *testing.T) {
	legacy := CatalogHash()
	if len(Catalog()) != 18 || len(OfficialCatalog()) != 10 {
		t.Fatal("catalog boundary changed")
	}
	for _, d := range OfficialCatalog() {
		if !IsOfficial(d) || ValidateDefinition(d) != nil {
			t.Fatalf("untrusted official definition %+v", d)
		}
		spec, err := OfficialRecipeSpec(d)
		if err != nil {
			t.Fatal(err)
		}
		packages := []OfficialPackage{}
		for _, name := range spec.Packages {
			packages = append(packages, OfficialPackage{Name: name, Version: "1.0", Origin: "Ubuntu:24.04/noble", Direct: true})
		}
		p := &OfficialPlan{Manager: "APT", OS: "ubuntu24.04", Architecture: "amd64", Packages: packages, Paths: spec.Paths, Commands: OfficialInstallCommands(d, packages)}
		if err = p.Validate(d); err != nil {
			t.Fatalf("valid plan %s %v", d.Key, err)
		}
		p.Commands[0].Executable = "/bin/sh"
		if p.Validate(d) == nil {
			t.Fatal("shell accepted")
		}
		config := Config{Name: "test", RuntimeMode: "NATIVE", HostPort: d.Port, MemoryMiB: 512}
		if d.Port != 0 {
			config.BindAddress = "127.0.0.1"
		}
		if d.Key == "nginx" {
			config.BindAddress = "0.0.0.0"
		}
		if config.Validate(d) != nil {
			t.Fatalf("fixed config rejected %s", d.Key)
		}
		config.StorageRoot = "/srv/custom"
		if config.Validate(d) == nil {
			t.Fatal("arbitrary official root accepted")
		}
		if err = (Source{Type: "OFFICIAL"}).ValidateFor(d, Config{RuntimeMode: "NATIVE"}); err != nil {
			t.Fatal(err)
		}
		if err = (Source{Type: "NATIVE_ARCHIVE", SHA256: strings.Repeat("a", 64), ByteSize: 1}).ValidateFor(d, Config{RuntimeMode: "NATIVE"}); err == nil {
			t.Fatal("offline masquerading as official accepted")
		}
	}
	if legacy != CatalogHash() {
		t.Fatal("legacy catalog hash changed")
	}
}

func TestOfficialOptionalFieldsPreserveLegacyJSON(t *testing.T) {
	raw, err := json.Marshal(Request{})
	if err != nil || strings.Contains(string(raw), `"official":`) {
		t.Fatal(string(raw), err)
	}
	raw, err = json.Marshal(Preflight{})
	if err != nil || strings.Contains(string(raw), `"official":`) {
		t.Fatal(string(raw), err)
	}
	d, _ := FindOfficial("nginx", "ubuntu24.04-repository")
	spec, _ := OfficialRecipeSpec(d)
	if !reflect.DeepEqual(spec.Paths.OwnedPaths, []string{"/etc/nginx", "/var/www/html", "/var/log/nginx"}) {
		t.Fatal("nginx deletion scope", spec.Paths)
	}
	d, _ = FindOfficial("java", "17-ubuntu24.04-repository")
	spec, _ = OfficialRecipeSpec(d)
	if len(spec.Paths.OwnedPaths) != 0 || spec.Paths.Binary != "/usr/lib/jvm/java-17-openjdk-amd64/bin/java" {
		t.Fatal("java shared-path ownership", spec.Paths)
	}
}
