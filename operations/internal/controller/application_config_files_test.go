package controller

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestPullModuleConfigTemplatesFromRepo(t *testing.T) {
	_, b := setup(t)
	b.login()

	originClone := cloneApplicationProjectRepo
	cloneApplicationProjectRepo = func(_ context.Context, _ string, _ string, destDir string, _ *applicationGitCloneAuth) (string, string, error) {
		writeProjectInspectionFile(t, destDir, ".git/HEAD", "ref: refs/heads/main\n")
		writeProjectInspectionFile(t, destDir, "platform-api/pom.xml", `<project><artifactId>platform-api</artifactId><parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId><version>3.5.16</version></parent><properties><java.version>21</java.version></properties></project>`)
		writeProjectInspectionFile(t, destDir, "platform-api/src/main/resources/application-test.yml", "server:\n  port: 18080\nspring:\n  datasource:\n    url: jdbc:mysql://127.0.0.1:3306/app\n")
		writeProjectInspectionFile(t, destDir, "platform-api/src/main/resources/application.yml", "spring:\n  profiles:\n    default: test\n")
		return "", "", nil
	}
	t.Cleanup(func() { cloneApplicationProjectRepo = originClone })

	created := dataOf(t, b.call("POST", "/application-projects/catalog", map[string]any{
		"name": "安防平台",
		"code": "sec-platform",
		"repositories": []map[string]any{
			{"id": "11111111-1111-4111-8111-111111111111", "name": "main", "url": "https://git.example/sec.git", "branch": "main"},
		},
		"modules": []map[string]any{
			{
				"id":           "22222222-2222-4222-8222-222222222222",
				"name":         "platform-api",
				"repositoryId": "11111111-1111-4111-8111-111111111111",
				"directory":    "platform-api",
				"catalog": map[string]any{
					"kind":         "BACKEND",
					"environment":  "JDK 21、Spring Boot",
					"startCommand": "java -jar app.jar",
					"runtimeMode":  "NATIVE",
				},
			},
		},
	}, 200))
	projectID := created["id"].(string)
	moduleID := "22222222-2222-4222-8222-222222222222"

	res := dataOf(t, b.call("POST", "/application-projects/"+projectID+"/modules/"+moduleID+"/config-templates", map[string]any{}, 200))
	tpls, ok := res["templates"].([]any)
	if !ok || len(tpls) != 2 {
		t.Fatalf("expected 2 templates, got %+v", res)
	}
	first := tpls[0].(map[string]any)
	if first["filename"] != "application-test.yml" || !strings.Contains(first["content"].(string), "port: 18080") {
		t.Fatalf("unexpected template order/content: %+v", first)
	}
}

func TestApplicationModuleConfigFilesSyncCredentialExpansionAndPersistence(t *testing.T) {
	s, b := setup(t)
	b.login()

	cred := dataOf(t, b.call("POST", "/credentials", map[string]string{
		"name":   "mysql-test-pass",
		"kind":   "PASSWORD",
		"secret": "super-secret-mysql-pwd",
	}, 201))
	_ = cred

	var lastSlug, lastFilename, lastTemplateBody, lastExpandedSecret string
	host, _ := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		switch req.Action {
		case deploy.AppConfigListAction:
			out, _ := json.Marshal(deploy.AppConfigResult{
				ConfigDir: "/opt/target/apps/" + req.AppConfig.AppSlug + "/config",
				Files: []deploy.AppConfigFileSummary{
					{Filename: "application-test.yml", Path: "/opt/target/apps/" + req.AppConfig.AppSlug + "/config/application-test.yml", Revision: deploy.ContentSHA256Hex(lastTemplateBody), ByteSize: 120},
				},
			})
			return out, true
		case deploy.AppConfigReadAction:
			out, _ := json.Marshal(deploy.AppConfigResult{
				ConfigDir: "/opt/target/apps/" + req.AppConfig.AppSlug + "/config",
				Path:      "/opt/target/apps/" + req.AppConfig.AppSlug + "/config/" + req.AppConfig.Filename,
				Filename:  req.AppConfig.Filename,
				Exists:    true,
				Content:   lastTemplateBody,
				Revision:  deploy.ContentSHA256Hex(lastTemplateBody),
			})
			return out, true
		case deploy.AppConfigWriteAction:
			if req.AppConfig == nil || len(req.AppConfig.ContentEnvelope) != 0 || req.CommandSignature == "" {
				t.Errorf("expected signed request with envelope cleared")
			}
			lastSlug = req.AppConfig.AppSlug
			lastFilename = req.AppConfig.Filename
			lastTemplateBody = req.Secrets[deploy.AppConfigBodySecretKey]
			lastExpandedSecret = req.Secrets[deploy.AppConfigCredSecretPrefix+"mysql-test-pass"]
			out, _ := json.Marshal(deploy.AppConfigResult{
				ConfigDir: "/opt/target/apps/" + lastSlug + "/config",
				Path:      "/opt/target/apps/" + lastSlug + "/config/" + lastFilename,
				Filename:  lastFilename,
				Exists:    true,
				Content:   lastTemplateBody,
				Revision:  deploy.ContentSHA256Hex(lastTemplateBody),
				Message:   "配置文件已同步至目标服务器",
			})
			return out, true
		case deploy.AppConfigDeleteAction:
			out, _ := json.Marshal(deploy.AppConfigResult{
				ConfigDir: "/opt/target/apps/" + req.AppConfig.AppSlug + "/config",
				Path:      "/opt/target/apps/" + req.AppConfig.AppSlug + "/config/" + req.AppConfig.Filename,
				Filename:  req.AppConfig.Filename,
				Exists:    false,
				Message:   "已移除",
			})
			return out, true
		}
		return nil, false
	})

	created := dataOf(t, b.call("POST", "/application-projects/catalog", map[string]any{
		"name": "安防综合管理平台",
		"modules": []map[string]any{
			{
				"id":           "33333333-3333-4333-8333-333333333333",
				"name":         "platform-api",
				"repositoryId": "",
				"directory":    "platform-api",
				"catalog": map[string]any{
					"kind":         "BACKEND",
					"environment":  "JDK 21",
					"startCommand": "java -jar app.jar",
					"runtimeMode":  "NATIVE",
				},
			},
		},
	}, 200))
	projectID := created["id"].(string)
	moduleID := "33333333-3333-4333-8333-333333333333"
	version := created["version"].(string)

	// Missing credential placeholder rejected with 400 CREDENTIAL_NOT_FOUND.
	badCred := b.call("PUT", "/application-projects/"+projectID+"/modules/"+moduleID+"/config-files/application-test.yml", map[string]any{
		"hostId":           host,
		"environment":      "test",
		"expectedRevision": "ABSENT",
		"content":          "spring:\n  datasource:\n    password: ${SFOPS_CRED:missing-cred}\n",
	}, 400)
	if !strings.Contains(badCred.Body.String(), "CREDENTIAL_NOT_FOUND") {
		t.Fatalf("expected CREDENTIAL_NOT_FOUND, got %s", badCred.Body.String())
	}

	// YAML tab indent fails locally with exact line number.
	badTab := b.call("PUT", "/application-projects/"+projectID+"/modules/"+moduleID+"/config-files/application-test.yml", map[string]any{
		"hostId":           host,
		"environment":      "test",
		"expectedRevision": "ABSENT",
		"content":          "server:\n\tport: 18080\n",
	}, 400)
	if !strings.Contains(badTab.Body.String(), "APP_CONFIG_YAML_TAB_INDENT") {
		t.Fatalf("expected APP_CONFIG_YAML_TAB_INDENT, got %s", badTab.Body.String())
	}

	// Valid YAML with ${SFOPS_CRED:mysql-test-pass} syncs cleanly and resolves secret into executor Secrets.
	yamlBody := "server:\n  port: 18080\nspring:\n  datasource:\n    password: ${SFOPS_CRED:mysql-test-pass}\n"
	syncRes := dataOf(t, b.call("PUT", "/application-projects/"+projectID+"/modules/"+moduleID+"/config-files/application-test.yml", map[string]any{
		"hostId":           host,
		"environment":      "test",
		"expectedRevision": "ABSENT",
		"content":          yamlBody,
	}, 200))
	if !deploy.ValidAppConfigSlug(lastSlug) || lastExpandedSecret != "super-secret-mysql-pwd" {
		t.Fatalf("unexpected sync state slug=%q expanded=%q", lastSlug, lastExpandedSecret)
	}
	if strings.Contains(jsonText(syncRes), "super-secret-mysql-pwd") {
		t.Fatal("credential secret leaked into HTTP response")
	}

	// Read back from server preserves placeholder without exposing resolved secret.
	readRes := dataOf(t, b.call("GET", "/application-projects/"+projectID+"/modules/"+moduleID+"/config-files/application-test.yml?hostId="+host+"&environment=test", nil, 200))
	if readRes["content"] != yamlBody {
		t.Fatalf("unexpected read content: %+v", readRes)
	}

	// Save service instance metadata referencing synced configDir & configFiles.
	savedProj := dataOf(t, b.call("POST", "/application-projects/"+projectID+"/service-instances", map[string]any{
		"version":     version,
		"moduleId":    moduleID,
		"environment": "test",
		"hostId":      host,
		"deployMode":  "NATIVE",
		"servicePort": 18080,
		"configDir":   syncRes["configDir"],
		"configFiles": []map[string]any{
			{
				"filename":        "application-test.yml",
				"remotePath":      syncRes["path"],
				"revision":        syncRes["revision"],
				"credentialNames": []string{"mysql-test-pass"},
			},
		},
	}, 200))
	envs := savedProj["environments"].([]any)
	dep := envs[0].(map[string]any)["deployments"].([]any)[0].(map[string]any)
	if dep["configDir"] != syncRes["configDir"] || len(dep["configFiles"].([]any)) != 1 {
		t.Fatalf("deployment metadata did not persist configDir/configFiles: %+v", dep)
	}

	// Delete remote config file.
	b.call("DELETE", "/application-projects/"+projectID+"/modules/"+moduleID+"/config-files/application-test.yml?hostId="+host+"&environment=test&expectedRevision="+syncRes["revision"].(string), nil, 200)
}

var _ = exec.ErrNotFound
