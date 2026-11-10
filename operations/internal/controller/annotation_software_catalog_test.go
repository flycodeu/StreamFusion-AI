package controller

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnnotationFramesDoNotRelaxOpsScriptsConnectionsOrAncestorPolicy(t *testing.T) {
	_, b := setup(t)
	response := b.call("GET", "/health", nil, 200)
	policy := response.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"frame-src 'self' http: https:", "default-src 'self'", "script-src 'self';", "connect-src 'self';", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, directive) {
			t.Fatal("missing security directive", directive)
		}
	}
	if response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("Ops frame or referrer protections changed")
	}
}

func TestAnnotationCatalogSeedingPreservesPublishedAndUnpublishedUserEdits(t *testing.T) {
	s, _ := setup(t)
	definitions := annotationSoftwareDefinitions()
	if len(definitions) != 8 {
		t.Fatalf("annotation definitions=%d", len(definitions))
	}
	for _, definition := range definitions {
		if definition.Category != "ANNOTATION" || definition.Web == nil || definition.DefaultBindAddress != "0.0.0.0" || definition.ValidateCommands() != nil {
			t.Fatalf("incomplete annotation template: %s", definition.Key)
		}
	}
	key := "custom-ytlabel-native-online"
	if _, err := s.DB.Exec("UPDATE command_templates SET draft_json=json_set(draft_json,'$.name','用户标注环境'),published_json='',version=9,revision=7 WHERE recipe_key=?", key); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = seedAnnotationSoftwareTemplates(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var draft, published string
	var version, revision int
	if err = s.DB.QueryRow("SELECT draft_json,published_json,version,revision FROM command_templates WHERE recipe_key=?", key).Scan(&draft, &published, &version, &revision); err != nil {
		t.Fatal(err)
	}
	var edited map[string]any
	if err = json.Unmarshal([]byte(draft), &edited); err != nil || edited["name"] != "用户标注环境" || published != "" || version != 9 || revision != 7 {
		t.Fatal("initialization changed the administrator's draft or publication state", err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM command_templates WHERE json_extract(draft_json,'$.category')='ANNOTATION'").Scan(&count); err != nil || count != 8 {
		t.Fatal("annotation templates duplicated", count, err)
	}
}
