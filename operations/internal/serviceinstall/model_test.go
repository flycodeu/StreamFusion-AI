package serviceinstall

import (
	"strings"
	"testing"
)

const testCredential = "11111111-1111-1111-1111-111111111111"

func TestServiceCatalogAndRequestValidation(t *testing.T) {
	for _, d := range Catalog() {
		r := Request{SoftwareID: testCredential, BindingRevision: 1, Definition: d, Config: Config{Name: d.Name, BindAddress: "127.0.0.1", HostPort: d.Port, MemoryMiB: 1024}, Source: Source{Type: "REGISTRY"}}
		if d.Key != "nginx" && d.Key != "docker" && d.Kind != "TOOL" {
			r.Config.CredentialID, r.Config.CredentialRevision = testCredential, "1"
		}
		if d.Key == "seaweedfs" {
			r.Config.AccessKeyCredentialID, r.Config.AccessKeyCredentialRevision = testCredential, "2"
		}
		if d.RuntimeModes[0] == "NATIVE" {
			r.Config.RuntimeMode = "NATIVE"
			r.Source.Type = "ONLINE"
		}
		if err := r.Validate(); err != nil {
			t.Fatalf("%s valid request rejected: %v", d.Key, err)
		}
		if d.Kind == "TOOL" || d.Key == "docker" {
			r.Config.HostPort = 1
			if r.Validate() == nil {
				t.Fatal("tool accepted fake listening port")
			}
			r.Config.HostPort = 0
			r.Definition.Digest = "sha256:" + strings.Repeat("b", 64)
			if r.Validate() == nil {
				t.Fatal("mutated tool recipe accepted")
			}
			continue
		}
		r.Config.HostPort = 0
		if r.Validate() == nil {
			t.Fatal("invalid port accepted")
		}
		r.Config.HostPort = 65535
		r.Config.BindAddress = "0.0.0.0"
		if r.Validate() == nil {
			t.Fatal("wildcard exposed without explicit binding accepted")
		}
		r.Config.BindAddress = "192.0.2.10"
		r.Source = Source{Type: "ARCHIVE", SHA256: strings.Repeat("a", 64), ByteSize: ArchiveLimit}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		r.Definition.Digest = "sha256:" + strings.Repeat("b", 64)
		if r.Validate() == nil {
			t.Fatal("mutated recipe accepted")
		}
	}
	catalog := Catalog()
	catalog[0].RuntimeModes[0] = "NATIVE"
	if Catalog()[0].RuntimeModes[0] != "DOCKER" {
		t.Fatal("catalog backing data escaped")
	}
}

func TestMySQLInitialDatabaseAndCredentialsAreExplicit(t *testing.T) {
	d, _ := Find("mysql", "8.4.11")
	c := Config{Name: "db", BindAddress: "127.0.0.1", HostPort: 13306, MemoryMiB: 1024, CredentialID: testCredential, CredentialRevision: "1", Database: "business", Username: "business_user", UserCredentialID: testCredential, UserCredentialRevision: "2"}
	if err := c.Validate(d); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Config{
		func() Config { v := c; v.Username = "root"; return v }(),
		func() Config { v := c; v.Database = "mysql"; return v }(),
		func() Config { v := c; v.Database = "business; DROP DATABASE x"; return v }(),
		func() Config { v := c; v.UserCredentialRevision = ""; return v }(),
		func() Config { v := c; v.CredentialID = ""; return v }(),
	} {
		if bad.Validate(d) == nil {
			t.Fatal("invalid initialization accepted")
		}
	}
}
