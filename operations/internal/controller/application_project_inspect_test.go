package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/projectconfig"
	"github.com/streamfusion-ai/operations/internal/projectinspect"
)

func writeProjectInspectionFile(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationProjectSourcesHideAbsolutePaths(t *testing.T) {
	parent := t.TempDir()
	left := filepath.Join(parent, "left", "my-app")
	right := filepath.Join(parent, "right", "my-app")
	for _, root := range []string{left, right} {
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	config := strings.Join([]string{parent, "relative", left, left, right}, string(os.PathListSeparator))
	sources := applicationProjectSources(config)
	if len(sources) != 2 || sources[0].ID == sources[1].ID || sources[0].Name == sources[1].Name {
		t.Fatalf("duplicate source or name: %+v", sources)
	}
	encoded, err := json.Marshal(sources)
	if err != nil || strings.Contains(string(encoded), "left") || strings.Contains(string(encoded), "right") {
		t.Fatalf("source discloses absolute checkout location: %q (%v)", encoded, err)
	}
}

func TestApplicationProjectRecognitionFromModuleManifests(t *testing.T) {
	root := t.TempDir()
	writeProjectInspectionFile(t, root, ".git/HEAD", "ref: refs/heads/main")
	writeProjectInspectionFile(t, root, "README.md", "For platform-api use `sh mvnw spring-boot:run -Dspring-boot.run.profiles=local`.")
	writeProjectInspectionFile(t, root, "platform-api/pom.xml", `<project><artifactId>platform-api</artifactId><parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId><version>3.5.16</version></parent><properties><java.version>21</java.version></properties><dependencies><dependency><groupId>com.mysql</groupId><artifactId>mysql-connector-j</artifactId></dependency><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-data-redis</artifactId></dependency></dependencies></project>`)
	writeProjectInspectionFile(t, root, "platform-api/mvnw", "This wrapper is not executed by detection")
	writeProjectInspectionFile(t, root, "platform-api/Dockerfile", "not a production runtime decision")
	writeProjectInspectionFile(t, root, "platform-web/package.json", `{"name":"secret-project-id","packageManager":"pnpm@10.34.5","scripts":{"dev":"do-not-execute-me"},"devDependencies":{"vite":"^8"},"dependencies":{"vue":"3.5.42"},"engines":{"node":"24.x"}}`)
	writeProjectInspectionFile(t, root, "platform-web/.nvmrc", "24.21.0\n")
	for _, dir := range []string{"algorithm-node/node-agent", "algorithm-node/runtime"} {
		writeProjectInspectionFile(t, root, dir+"/pyproject.toml", "[project]\nname = \"agent\"\nrequires-python = \">=3.12,<3.13\"\n")
		writeProjectInspectionFile(t, root, dir+"/uv.lock", "version = 1")
		writeProjectInspectionFile(t, root, dir+"/app/__main__.py", "# metadata only")
	}
	writeProjectInspectionFile(t, root, "algorithm-node/README.md", "# node-agent directory\nuv run --locked python -m app\n# runtime directory\nuv run --locked python -m app\n")
	writeProjectInspectionFile(t, root, "node_modules/ignore/package.json", `{"scripts":{"start":"ignore"}}`)
	writeProjectInspectionFile(t, root, "unknown/.env", "DATA_PASSWORD=never-return-this")
	got, err := inspectApplicationProjectSource(context.Background(), root, "")
	if err != nil || len(got) != 4 {
		t.Fatalf("want four recognized modules, got %+v (%v)", got, err)
	}
	modules := map[string]projectinspect.Module{}
	for _, module := range got {
		modules[module.Directory] = module
	}
	backend := modules["platform-api"]
	if backend.Kind != "BACKEND" || !strings.Contains(backend.Environment, "JDK 21、Spring Boot 3.5.16、Maven Wrapper") || !strings.HasSuffix(backend.Environment, "MySQL、Redis") || backend.StartCommand != "sh mvnw spring-boot:run -Dspring-boot.run.profiles=local" || !slices.Contains(backend.Evidence, "README.md") {
		t.Fatalf("unexpected Java inference: %+v", backend)
	}
	frontend := modules["platform-web"]
	if frontend.Kind != "FRONTEND" || !strings.Contains(frontend.Environment, "Vue、Vite、Node.js 24.x（.nvmrc 24.21.0）、pnpm 10.34.5") || frontend.StartCommand != "pnpm dev" {
		t.Fatalf("unexpected Node inference: %+v", frontend)
	}
	for _, dir := range []string{"algorithm-node/node-agent", "algorithm-node/runtime"} {
		module := modules[dir]
		if module.Kind != "ALGORITHM" || module.Environment != "Python >=3.12,<3.13、uv" || module.StartCommand != "uv run --locked python -m app" || !slices.Contains(module.Evidence, "algorithm-node/README.md") {
			t.Fatalf("unexpected Python inference: %+v", module)
		}
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), "never-return-this") || strings.Contains(string(encoded), "secret-project-id") || strings.Contains(string(encoded), "do-not-execute-me") || strings.Contains(string(encoded), "Docker") {
		t.Fatalf("result included unrelated metadata or runtime guesses: %v (%v)", got, err)
	}
	for _, dir := range []string{".", "unknown"} {
		modules, err := inspectApplicationProjectSource(context.Background(), root, dir)
		if err != nil || len(modules) != 0 {
			t.Fatalf("a specified, unrecognized directory should return no findings: %+v, %v", modules, err)
		}
	}
	writeProjectInspectionFile(t, root, "README.md", "For platform-api use `sh mvnw spring-boot:run -Dspring-boot.run.profiles=local-test`.")
	only, err := inspectApplicationProjectSource(context.Background(), root, "platform-api")
	if err != nil || len(only) != 1 || only[0].StartCommand != "sh mvnw spring-boot:run" {
		t.Fatalf("only suggest the exact documented profile for the specified module: %+v, %v", only, err)
	}
	for _, dir := range []string{"../outside", "platform-api/../platform-web", ".git", "node_modules/ignore", filepath.VolumeName(root) + "/outside"} {
		if _, err := inspectApplicationProjectSource(context.Background(), root, dir); err == nil {
			t.Fatalf("accepted invalid or disallowed directory %q", dir)
		}
	}
}

func TestApplicationProjectRecognitionDoesNotFollowManifestLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeProjectInspectionFile(t, root, ".git/HEAD", "")
	writeProjectInspectionFile(t, outside, "package.json", `{"scripts":{"start":"not a project manifest"}}`)
	if err := os.Symlink(filepath.Join(outside, "package.json"), filepath.Join(root, "package.json")); err != nil {
		t.Skip("symlinks unavailable in this test environment")
	}
	got, err := inspectApplicationProjectSource(context.Background(), root, ".")
	if err != nil || len(got) != 0 {
		t.Fatalf("read a manifest through a symlink: %v (%v)", got, err)
	}
}

func TestApplicationProjectDeepRecognitionSpringBootAndVite(t *testing.T) {
	root := t.TempDir()
	writeProjectInspectionFile(t, root, ".git/HEAD", "ref: refs/heads/main")
	writeProjectInspectionFile(t, root, "platform-api/pom.xml", `<project><artifactId>platform-api</artifactId><parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId><version>3.5.16</version></parent><properties><java.version>21</java.version></properties><dependencies><dependency><groupId>com.mysql</groupId><artifactId>mysql-connector-j</artifactId></dependency><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-data-redis</artifactId></dependency><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-actuator</artifactId></dependency></dependencies></project>`)
	writeProjectInspectionFile(t, root, "platform-api/mvnw", "wrapper")
	writeProjectInspectionFile(t, root, "platform-api/src/main/resources/application.yml", "spring:\n  profiles:\n    default: test\n  datasource:\n    url: ${DB_URL}\nserver:\n  port: ${SERVER_PORT:8080}\n")
	writeProjectInspectionFile(t, root, "platform-api/src/main/resources/application-test.yml", "spring:\n  datasource:\n    url: ${DB_URL:jdbc:mysql://127.0.0.1:3306/streamfusion}\n    password: ${DB_PASSWORD:unsafe-default-should-be-redacted}\n  data:\n    redis:\n      host: ${REDIS_HOST:127.0.0.1}\n      password: ${REDIS_PASSWORD}\nserver:\n  port: ${SERVER_PORT:18080}\n")
	writeProjectInspectionFile(t, root, "platform-api/src/main/resources/application-prod.yml", "spring:\n  datasource:\n    url: ${DB_URL}\n")
	writeProjectInspectionFile(t, root, "platform-web/package.json", `{"packageManager":"pnpm@10.28.0","scripts":{"dev":"vite","build":"vite build","test":"vitest run"},"dependencies":{"vue":"3.5.0"},"devDependencies":{"vite":"8.0.0"}}`)
	writeProjectInspectionFile(t, root, "platform-web/pnpm-lock.yaml", "lockfileVersion: '9.0'")
	writeProjectInspectionFile(t, root, "platform-web/vite.config.ts", `export default { server: { proxy: { '^/api(?:/|$)': { target: env.API_TARGET || 'http://127.0.0.1:8080', rewrite: p => p.replace(/^\/api/, '') } } }, build: { outDir: 'dist' } }`)

	got, err := inspectApplicationProjectSource(context.Background(), root, "")
	if err != nil || len(got) != 2 {
		t.Fatalf("want 2 deeply recognized modules, got %+v (%v)", got, err)
	}
	byDir := map[string]projectinspect.Module{}
	for _, m := range got {
		byDir[m.Directory] = m
	}
	api := byDir["platform-api"]
	if api.RecommendedRuntimeMode != "NATIVE" || api.DefaultProfile != "test" || api.DefaultPort != 18080 || api.HealthPath != "/actuator/health" {
		t.Fatalf("unexpected Spring Boot metadata: %+v", api)
	}
	if !slices.Equal(api.Profiles, []string{"prod", "test"}) || !slices.Contains(api.Dependencies, "MySQL") || !slices.Contains(api.Dependencies, "Redis") {
		t.Fatalf("unexpected profiles/dependencies: %+v", api)
	}
	hints := map[string]projectconfig.EnvHint{}
	for _, h := range api.EnvHints {
		hints[h.Name] = h
	}
	if !hints["DB_PASSWORD"].Sensitive || hints["DB_PASSWORD"].DefaultValue != "" {
		t.Fatalf("sensitive env hint leaked or not flagged: %+v", hints["DB_PASSWORD"])
	}
	if hints["DB_URL"].DefaultValue != "jdbc:mysql://127.0.0.1:3306/streamfusion" || hints["REDIS_HOST"].DefaultValue != "127.0.0.1" {
		t.Fatalf("expected profile defaults preserved: %+v", hints)
	}
	web := byDir["platform-web"]
	if web.RecommendedRuntimeMode != "STATIC" || web.BuildCommand != "pnpm run build" || web.ArtifactPath != "dist" || !slices.Contains(web.Dependencies, "Nginx") {
		t.Fatalf("unexpected frontend recognition: %+v", web)
	}
	if len(web.ProxyHints) != 1 || web.ProxyHints[0].Location != "/api" || !web.ProxyHints[0].StripPrefix || web.ProxyHints[0].TargetPort != 8080 {
		t.Fatalf("unexpected vite proxy hints: %+v", web.ProxyHints)
	}
}

func TestApplicationProjectInspectRemoteGitRepository(t *testing.T) {
	_, b := setup(t)
	b.login()

	for _, bad := range []map[string]string{
		{"repositoryUrl": "file:///etc/passwd"},
		{"repositoryUrl": "https://user:secret@github.com/org/repo.git"},
		{"repositoryUrl": "https://github.com/org/repo.git", "branch": "--upload-pack=bad"},
	} {
		raw, _ := json.Marshal(bad)
		b.call("POST", "/application-projects/inspect", json.RawMessage(bytes.TrimSpace(raw)), http.StatusBadRequest)
	}

	prev := cloneApplicationProjectRepo
	defer func() { cloneApplicationProjectRepo = prev }()
	var clonedURL, clonedBranch string
	cloneApplicationProjectRepo = func(_ context.Context, repoURL, branch, destDir string, _ *applicationGitCloneAuth) (string, string, error) {
		clonedURL, clonedBranch = repoURL, branch
		writeProjectInspectionFile(t, destDir, ".git/HEAD", "ref: refs/heads/main")
		writeProjectInspectionFile(t, destDir, "web/package.json", `{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.0"}}`)
		return "", "", nil
	}

	project := applicationResponse(t, b.call("POST", "/application-projects/catalog", map[string]any{
		"name":         "远程仓库识别",
		"repositories": []applicationCatalogRepository{{Name: "主仓库", URL: "https://github.com/example/demo.git", Branch: "develop"}},
	}, http.StatusOK))

	resp := dataOf(t, b.call("POST", "/application-projects/inspect", map[string]string{
		"projectId": project.ID,
	}, http.StatusOK))
	if clonedURL != "https://github.com/example/demo.git" || clonedBranch != "develop" {
		t.Fatalf("unexpected cloned repo=%q branch=%q", clonedURL, clonedBranch)
	}
	modules, _ := resp["modules"].([]any)
	if len(modules) != 1 {
		t.Fatalf("expected 1 inspected module from remote git, got %+v", resp)
	}
}
