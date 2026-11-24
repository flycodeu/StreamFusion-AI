package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppConfigValidationAndCredentialExpansion(t *testing.T) {
	if !ValidAppConfigSlug("sf-platform-api-test-111111") || ValidAppConfigSlug("../bad") || ValidAppConfigSlug("") {
		t.Fatal("unexpected slug validation behavior")
	}
	if !ValidAppConfigFilename("application-test.yml") || !ValidAppConfigFilename(".env.production") || !ValidAppConfigFilename(".env") {
		t.Fatal("expected valid config filenames to pass")
	}
	if ValidAppConfigFilename("../secret.yml") || ValidAppConfigFilename(".hidden.yml") || ValidAppConfigFilename("a/b.yml") {
		t.Fatal("expected unsafe filenames to fail")
	}
	if line, err := ValidateAppConfigContent("application-test.yml", "server:\n\tport: 8080\n"); err == nil || err.Error() != "APP_CONFIG_YAML_TAB_INDENT" || line != 2 {
		t.Fatalf("expected tab indent error at line 2, got line=%d err=%v", line, err)
	}
	if _, err := ValidateAppConfigContent("config.json", "{bad}"); err == nil || err.Error() != "APP_CONFIG_JSON_INVALID" {
		t.Fatalf("expected JSON error, got %v", err)
	}
	tpl := "spring:\n  datasource:\n    password: ${SFOPS_CRED:db-pass}\n  redis:\n    password: ${SFOPS_CRED:redis-pass}\n"
	expanded, err := ExpandAppConfigCredentials(tpl, map[string]string{
		AppConfigCredSecretPrefix + "db-pass":    "s3cr3t-db",
		AppConfigCredSecretPrefix + "redis-pass": "s3cr3t-redis",
	})
	if err != nil || !strings.Contains(expanded, "password: s3cr3t-db") || strings.Contains(expanded, "SFOPS_CRED") {
		t.Fatalf("unexpected credential expansion: err=%v out=%q", err, expanded)
	}
	if _, err := ExpandAppConfigCredentials(tpl, map[string]string{}); err == nil || err.Error() != "APP_CONFIG_CREDENTIAL_MISSING" {
		t.Fatalf("expected APP_CONFIG_CREDENTIAL_MISSING, got %v", err)
	}
}

func TestApplyAppConfigOpTemplateVsRuntimeIsolationAndLifecycle(t *testing.T) {
	execRoot := t.TempDir()
	slug := "sf-platform-api-test-111111"
	tpl := "server:\n  port: 18080\nspring:\n  datasource:\n    password: ${SFOPS_CRED:mysql-pwd}\n"
	secrets := map[string]string{
		AppConfigBodySecretKey:                  tpl,
		AppConfigCredSecretPrefix + "mysql-pwd": "real-db-password-value",
	}

	writeReq := Request{
		Action: AppConfigWriteAction,
		AppConfig: &AppConfigRequest{
			AppSlug:          slug,
			Filename:         "application-test.yml",
			ExpectedRevision: "ABSENT",
			ContentSHA256:    ContentSHA256Hex(tpl),
			CredentialRefs:   map[string]string{"mysql-pwd": "cred-1:2"},
		},
	}
	writeRes, err := applyAppConfigOp(execRoot, writeReq, secrets)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if !writeRes.Exists || writeRes.Revision != ContentSHA256Hex(tpl) {
		t.Fatalf("unexpected write result: %+v", writeRes)
	}

	// Runtime file on server has expanded secret; read returns template placeholder only.
	runtimeRaw, err := os.ReadFile(filepath.Join(execRoot, "apps", slug, "config", "application-test.yml"))
	if err != nil || !strings.Contains(string(runtimeRaw), "real-db-password-value") {
		t.Fatalf("runtime file missing expanded credential: %v %q", err, string(runtimeRaw))
	}
	readRes, err := applyAppConfigOp(execRoot, Request{
		Action:    AppConfigReadAction,
		AppConfig: &AppConfigRequest{AppSlug: slug, Filename: "application-test.yml"},
	}, nil)
	if err != nil || !readRes.Exists || strings.Contains(readRes.Content, "real-db-password-value") || !strings.Contains(readRes.Content, "${SFOPS_CRED:mysql-pwd}") {
		t.Fatalf("read leaked secret or lost placeholder: %v %+v", err, readRes)
	}

	// List includes file without internal dot-directories.
	listRes, err := applyAppConfigOp(execRoot, Request{
		Action:    AppConfigListAction,
		AppConfig: &AppConfigRequest{AppSlug: slug},
	}, nil)
	if err != nil || len(listRes.Files) != 1 || listRes.Files[0].Filename != "application-test.yml" {
		t.Fatalf("unexpected list: %v %+v", err, listRes)
	}

	// Conflict with wrong ExpectedRevision.
	stale := writeReq
	stale.AppConfig.ExpectedRevision = "ABSENT"
	if _, err := applyAppConfigOp(execRoot, stale, secrets); err == nil || err.Error() != "APP_CONFIG_CONFLICT" {
		t.Fatalf("expected APP_CONFIG_CONFLICT, got %v", err)
	}

	// Update creates backup; delete removes runtime & template while keeping backup.
	plainUpdated := "server:\n  port: 18081\n"
	updateReq := Request{
		Action: AppConfigWriteAction,
		AppConfig: &AppConfigRequest{
			AppSlug:          slug,
			Filename:         "application-test.yml",
			ExpectedRevision: writeRes.Revision,
			ContentSHA256:    ContentSHA256Hex(plainUpdated),
		},
	}
	updateRes, err := applyAppConfigOp(execRoot, updateReq, map[string]string{AppConfigBodySecretKey: plainUpdated})
	if err != nil || updateRes.Content != plainUpdated {
		t.Fatalf("update failed: %v %+v", err, updateRes)
	}
	if _, err := os.Stat(filepath.Join(execRoot, "apps", slug, "config", ".backup", "application-test.yml.bak")); err != nil {
		t.Fatalf("missing backup: %v", err)
	}

	delRes, err := applyAppConfigOp(execRoot, Request{
		Action:    AppConfigDeleteAction,
		AppConfig: &AppConfigRequest{AppSlug: slug, Filename: "application-test.yml", ExpectedRevision: updateRes.Revision},
	}, nil)
	if err != nil || delRes.Exists {
		t.Fatalf("delete failed: %v %+v", err, delRes)
	}
	if _, err := os.Stat(filepath.Join(execRoot, "apps", slug, "config", "application-test.yml")); !os.IsNotExist(err) {
		t.Fatalf("runtime file still present: %v", err)
	}
}
