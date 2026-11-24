package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func seedManagedMavenSoftware(t *testing.T, s *Store, hostID string) string {
	t.Helper()
	id := uuid.NewString()
	now := Now()
	installReq := serviceinstall.Request{
		SoftwareID:      id,
		DirectoryName:   "maven-3.9.11",
		BindingRevision: 1,
		Definition: serviceinstall.Definition{
			Key:          "maven",
			CatalogKey:   "maven",
			Name:         "Maven",
			Version:      "3.9.11",
			Kind:         "TOOL",
			RuntimeModes: []string{"DOCKER", "NATIVE"},
		},
		Config: serviceinstall.Config{
			Name:        "Maven 3.9",
			RuntimeMode: "DOCKER",
		},
	}
	if _, err := s.DB.Exec(
		`INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,desired_state,actual_state,installation_json,installation_status,created_at,updated_at)
		 VALUES(?,'Maven 3.9',?,'MAVEN','3.9.11','TOOL','image:maven39','MANAGED','INSTALLED','INSTALLED',?,'READY',?,?)`,
		id, hostID, jsonText(installReq), now, now,
	); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMavenSettingsReadValidateAndSave(t *testing.T) {
	s, b := setup(t)
	b.login()

	currentXML := "<settings><mirrors/></settings>\n"
	currentRev := deploy.ContentSHA256Hex(currentXML)
	var savedOnTarget string

	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		switch req.Action {
		case deploy.MavenConfigReadAction:
			if req.MavenConfig == nil || req.CommandSignature == "" || req.DeadlineAt == "" || len(req.Secrets) != 0 {
				t.Errorf("invalid maven read request: %+v", req)
			}
			out, _ := json.Marshal(deploy.MavenConfigResult{
				Path:     "/opt/target/software/maven-3.9.11/config/settings.xml",
				Scope:    "DOCKER",
				Exists:   true,
				Content:  currentXML,
				Revision: currentRev,
			})
			return out, true
		case deploy.MavenConfigWriteAction:
			if req.MavenConfig == nil || len(req.MavenConfig.ContentEnvelope) != 0 || req.CommandSignature == "" {
				t.Errorf("envelope not stripped or request unsigned: %+v", req.MavenConfig)
			}
			body := req.Secrets[deploy.MavenConfigSecretKey]
			if body == "" || deploy.ContentSHA256Hex(body) != req.MavenConfig.ContentSHA256 {
				t.Errorf("secret body sha mismatch")
			}
			savedOnTarget = body
			out, _ := json.Marshal(deploy.MavenConfigResult{
				Path:     "/opt/target/software/maven-3.9.11/config/settings.xml",
				Scope:    "DOCKER",
				Exists:   true,
				Content:  body,
				Revision: req.MavenConfig.ContentSHA256,
				Message:  "Maven settings.xml 已保存",
			})
			return out, true
		}
		return nil, false
	})

	mavenID := seedManagedMavenSoftware(t, s, host)

	// GET settings returns content, revision, and mirror presets.
	got := dataOf(t, b.call("GET", "/software-instances/"+mavenID+"/maven-settings", nil, 200))
	if got["content"] != currentXML || got["revision"] != currentRev {
		t.Fatalf("unexpected GET payload: %+v", got)
	}
	presets, ok := got["presets"].([]any)
	if !ok || len(presets) < 2 {
		t.Fatalf("missing mirror presets: %+v", got)
	}

	// Invalid XML fails locally with 400 + exact line number.
	badRes := b.call("PUT", "/software-instances/"+mavenID+"/maven-settings", map[string]any{
		"content":          "<settings>\n  <mirrors>\n</settings>",
		"expectedRevision": currentRev,
	}, 400)
	if !strings.Contains(badRes.Body.String(), "MAVEN_SETTINGS_XML_INVALID") || !strings.Contains(badRes.Body.String(), `"line"`) {
		t.Fatalf("expected MAVEN_SETTINGS_XML_INVALID with line, got %s", badRes.Body.String())
	}

	// Valid XML saves via sealed envelope -> unsealed on target.
	updatedXML := mavenSettingsDefaultContent
	putRes := dataOf(t, b.call("PUT", "/software-instances/"+mavenID+"/maven-settings", map[string]any{
		"content":          updatedXML,
		"expectedRevision": currentRev,
	}, 200))
	if savedOnTarget != updatedXML || putRes["revision"] != deploy.ContentSHA256Hex(updatedXML) {
		t.Fatalf("save mismatch: target=%q res=%+v", savedOnTarget, putRes)
	}

	// Maintenance host blocks PUT.
	if _, err := s.DB.Exec("UPDATE targets SET maintenance=1 WHERE id=?", host); err != nil {
		t.Fatal(err)
	}
	maintRes := b.call("PUT", "/software-instances/"+mavenID+"/maven-settings", map[string]any{
		"content":          updatedXML,
		"expectedRevision": putRes["revision"],
	}, 409)
	if !strings.Contains(maintRes.Body.String(), "HOST_MAINTENANCE") {
		t.Fatalf("expected HOST_MAINTENANCE, got %s", maintRes.Body.String())
	}
}
