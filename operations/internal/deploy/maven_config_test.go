package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanToolVersionOutputStripsMavenJansiNoise(t *testing.T) {
	noisy := `Failed to load native library:jansi-2.4.2-c540835401c0e949-libjansi.so. The native library file at /tmp/jansi-2.4.2-c540835401c0e949-libjansi.so is not executable, make sure that the directory is mounted on a partition without the noexec flag, or set the jansi.tmpdir system property to point to a proper location. osinfo: Linux/x86_64
java.lang.UnsatisfiedLinkError: /tmp/jansi-2.4.2-c540835401c0e949-libjansi.so: /tmp/jansi-2.4.2-c540835401c0e949-libjansi.so: failed to map segment from shared object
Apache Maven 3.9.11 (3e54c93a704957b63ee3494413a2b544fd3d825b)
Maven home: /usr/share/maven
Java version: 21.0.9, vendor: Eclipse Adoptium`
	got := cleanToolVersionOutput("MAVEN", noisy)
	if !strings.HasPrefix(got, "Apache Maven 3.9.11") || strings.Contains(got, "jansi") || strings.Contains(got, "UnsatisfiedLinkError") {
		t.Fatalf("unexpected cleaned version: %q", got)
	}
	if other := cleanToolVersionOutput("JAVA", "openjdk version \"21.0.9\""); other != "openjdk version \"21.0.9\"" {
		t.Fatalf("unexpected java output change: %q", other)
	}
}

func TestValidateMavenSettingsXML(t *testing.T) {
	valid := `<settings><mirrors><mirror><id>aliyun</id><url>https://maven.aliyun.com/repository/public</url></mirror></mirrors></settings>`
	if line, err := ValidateMavenSettingsXML(valid); err != nil || line != 0 {
		t.Fatalf("expected valid XML, got line=%d err=%v", line, err)
	}
	badSyntax := "<settings>\n  <mirrors>\n    <mirror>\n</settings>"
	if line, err := ValidateMavenSettingsXML(badSyntax); err == nil || err.Error() != "MAVEN_SETTINGS_XML_INVALID" || line < 2 {
		t.Fatalf("expected syntax error with line>=2, got line=%d err=%v", line, err)
	}
	wrongRoot := "<configuration><mirrors/></configuration>"
	if _, err := ValidateMavenSettingsXML(wrongRoot); err == nil || err.Error() != "MAVEN_SETTINGS_ROOT_INVALID" {
		t.Fatalf("expected MAVEN_SETTINGS_ROOT_INVALID, got %v", err)
	}
	if !MavenSettingsHasSecret("<settings><servers><server><password>secret123</password></server></servers></settings>") {
		t.Fatal("expected secret tag detection")
	}
	if MavenSettingsHasSecret(valid) {
		t.Fatal("unexpected secret detection on mirror-only settings")
	}
}

func TestApplyMavenConfigOpDockerMirrorAndConflict(t *testing.T) {
	execRoot := t.TempDir()
	installRoot := filepath.Join(execRoot, "software", "maven-3.9.11")
	id := "11111111-1111-4111-8111-111111111111"

	// Read ABSENT initially.
	readReq := Request{
		Action: MavenConfigReadAction,
		MavenConfig: &MavenConfigRequest{
			SoftwareID:  id,
			RuntimeMode: "DOCKER",
			InstallRoot: filepath.ToSlash(installRoot),
		},
	}
	res, err := applyMavenConfigOp(execRoot, readReq, "")
	if err != nil {
		t.Fatalf("read ABSENT failed: %v", err)
	}
	if res.Exists || res.Revision != "ABSENT" || res.Scope != "DOCKER" {
		t.Fatalf("unexpected read ABSENT result: %+v", res)
	}

	// Write first settings.xml (DOCKER + mirror).
	body1 := "<settings><mirrors><mirror><id>ali</id></mirror></mirrors></settings>\n"
	writeReq := Request{
		Action: MavenConfigWriteAction,
		MavenConfig: &MavenConfigRequest{
			SoftwareID:       id,
			RuntimeMode:      "DOCKER",
			InstallRoot:      filepath.ToSlash(installRoot),
			ExpectedRevision: "ABSENT",
			ContentSHA256:    ContentSHA256Hex(body1),
		},
	}
	res, err = applyMavenConfigOp(execRoot, writeReq, body1)
	if err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if !res.Exists || res.Revision != ContentSHA256Hex(body1) {
		t.Fatalf("unexpected write result: %+v", res)
	}
	mirrorBytes, err := os.ReadFile(filepath.Join(execRoot, "config", "maven-settings.xml"))
	if err != nil || string(mirrorBytes) != body1 {
		t.Fatalf("docker maven mirror not synced: err=%v body=%q", err, string(mirrorBytes))
	}

	// Conflict detection with stale revision.
	body2 := "<settings><mirrors/></settings>\n"
	staleReq := Request{
		Action: MavenConfigWriteAction,
		MavenConfig: &MavenConfigRequest{
			SoftwareID:       id,
			RuntimeMode:      "DOCKER",
			InstallRoot:      filepath.ToSlash(installRoot),
			ExpectedRevision: "ABSENT",
			ContentSHA256:    ContentSHA256Hex(body2),
		},
	}
	if _, err = applyMavenConfigOp(execRoot, staleReq, body2); err == nil || err.Error() != "MAVEN_CONFIG_CONFLICT" {
		t.Fatalf("expected MAVEN_CONFIG_CONFLICT, got %v", err)
	}

	// Update with valid expectedRevision creates .bak and updates both files.
	staleReq.MavenConfig.ExpectedRevision = res.Revision
	res2, err := applyMavenConfigOp(execRoot, staleReq, body2)
	if err != nil || res2.Content != body2 {
		t.Fatalf("update failed: %v %+v", err, res2)
	}
	bak, err := os.ReadFile(filepath.Join(installRoot, "config", "settings.xml.bak"))
	if err != nil || string(bak) != body1 {
		t.Fatalf("expected backup of body1, got err=%v bak=%q", err, string(bak))
	}

	// Native path resolution & traversal rejection.
	nativeReq := Request{
		Action: MavenConfigWriteAction,
		MavenConfig: &MavenConfigRequest{
			SoftwareID:       id,
			RuntimeMode:      "NATIVE",
			InstallRoot:      filepath.ToSlash(installRoot),
			ExpectedRevision: "ABSENT",
			ContentSHA256:    ContentSHA256Hex(body1),
		},
	}
	nativeRes, err := applyMavenConfigOp(execRoot, nativeReq, body1)
	if err != nil || !strings.HasSuffix(nativeRes.Path, "/runtime/maven/conf/settings.xml") {
		t.Fatalf("unexpected native result: %v %+v", err, nativeRes)
	}
	badPathReq := readReq
	badPathReq.MavenConfig = &MavenConfigRequest{
		SoftwareID:  id,
		RuntimeMode: "NATIVE",
		InstallRoot: filepath.ToSlash(installRoot) + "/../escape",
	}
	if _, err = applyMavenConfigOp(execRoot, badPathReq, ""); err == nil || err.Error() != "MAVEN_CONFIG_PATH_INVALID" {
		t.Fatalf("expected MAVEN_CONFIG_PATH_INVALID, got %v", err)
	}
}
