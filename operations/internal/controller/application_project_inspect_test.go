package controller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
	modules := map[string]applicationProjectRecognition{}
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
