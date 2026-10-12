package plugin

import (
	"strings"
	"testing"
)

func TestCatalogTrustAndVersionProbe(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range All() {
		if Validate(d) != nil || seen[d.Key+":"+d.Version] {
			t.Fatal("invalid/duplicate definition", d)
		}
		seen[d.Key+":"+d.Version] = true
		bad := d
		bad.Digest = "sha256:" + strings.Repeat("a", 64)
		if Validate(bad) == nil {
			t.Fatal("foreign digest accepted")
		}
		bad = d
		bad.Command = "sh"
		if Validate(bad) == nil {
			t.Fatal("foreign command accepted")
		}
		bad = d
		bad.Platform = "windows/amd64"
		if Validate(bad) == nil {
			t.Fatal("unsupported platform accepted")
		}
	}
	all := All()
	all[0].Version = "changed"
	if _, e := Find("java-runtime", "21-jre"); e != nil {
		t.Fatal("catalog exposed mutable state")
	}
	java, _ := Find("java-runtime", "21-jre")
	if !VersionMatches(java, `openjdk version "21.0.10"`) || VersionMatches(java, `openjdk version "17.0.18"`) {
		t.Fatal("Java version check")
	}
	maven, _ := Find("maven-jdk-build", "3.9.11-jdk-21")
	if !VersionMatches(maven, "Apache Maven 3.9.11\nJava version: 21.0.10") || VersionMatches(maven, "Apache Maven 3.9.11\nJava version: 17.0.18") {
		t.Fatal("Maven/JDK mismatch accepted")
	}
}
