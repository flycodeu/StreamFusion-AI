package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const projectSourceRootsEnv = "SFOPS_PROJECT_SOURCE_ROOTS"

type applicationProjectSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	root string
}

type applicationProjectRecognition struct {
	Directory    string   `json:"directory"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Environment  string   `json:"environment"`
	StartCommand string   `json:"startCommand"`
	Evidence     []string `json:"evidence"`
}

type applicationProjectInspectRequest struct {
	SourceID  string `json:"sourceId"`
	Directory string `json:"directory"`
}

func applicationProjectSources(config string) []applicationProjectSource {
	items := []applicationProjectSource{}
	seen := map[string]bool{}
	for _, entry := range filepath.SplitList(config) {
		entry = strings.TrimSpace(entry)
		if !filepath.IsAbs(entry) || strings.HasPrefix(filepath.ToSlash(entry), "//") {
			continue
		}
		rootPath, err := filepath.EvalSymlinks(filepath.Clean(entry))
		if err != nil || strings.HasPrefix(filepath.ToSlash(rootPath), "//") {
			continue
		}
		root, err := openApplicationProjectSource(rootPath)
		if err != nil {
			continue
		}
		root.Close()
		digest := sha256.Sum256([]byte(rootPath))
		id := hex.EncodeToString(digest[:16])
		if seen[id] {
			continue
		}
		seen[id] = true
		name := filepath.Base(rootPath)
		if !validProjectSourceLabel.MatchString(name) {
			name = "已登记源码 " + id[:6]
		}
		items = append(items, applicationProjectSource{ID: id, Name: name, root: rootPath})
		if len(items) == 16 {
			break
		}
	}
	names := map[string]int{}
	for _, item := range items {
		names[strings.ToLower(item.Name)]++
	}
	for i := range items {
		if names[strings.ToLower(items[i].Name)] > 1 {
			items[i].Name += " · " + items[i].ID[:6]
		}
	}
	return items
}

var (
	validProjectSourceLabel = regexp.MustCompile(`^[\p{L}\p{N} ._-]{1,80}$`)
	validProjectDirectory   = regexp.MustCompile(`^[\p{L}\p{N} _.-]{1,80}$`)
	projectVersion          = regexp.MustCompile(`^[vV]?[0-9]{1,4}(?:\.(?:[0-9]{1,4}|[xX*])){0,3}$`)
	projectVersionRange     = regexp.MustCompile(`^[0-9<>=~^., |*xXvV-]{1,48}$`)
)

func openApplicationProjectSource(rootPath string) (*os.Root, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	git, err := root.Lstat(".git")
	if err != nil || !(git.IsDir() || git.Mode().IsRegular()) {
		root.Close()
		return nil, os.ErrInvalid
	}
	return root, nil
}

func (h *HTTP) listApplicationProjectSources(w http.ResponseWriter, r *http.Request) {
	h.success(w, r, map[string]any{"items": applicationProjectSources(os.Getenv(projectSourceRootsEnv))})
}

func (h *HTTP) inspectApplicationProject(w http.ResponseWriter, r *http.Request) {
	var in applicationProjectInspectRequest
	if decode(w, r, &in) != nil || in.SourceID == "" || len(in.Directory) > 320 {
		h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "请选择已登记的源码和有效的仓库内目录")
		return
	}
	for _, source := range applicationProjectSources(os.Getenv(projectSourceRootsEnv)) {
		if source.ID != in.SourceID {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := inspectApplicationProjectSource(ctx, source.root, in.Directory)
		if err != nil {
			h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "无法读取所选源码目录，可手动填写运行环境")
			return
		}
		h.success(w, r, map[string]any{"modules": items})
		return
	}
	h.fail(w, r, http.StatusBadRequest, "PROJECT_SOURCE_INVALID", "请选择已登记且可读的源码")
}

func inspectApplicationProjectSource(ctx context.Context, rootPath, selected string) ([]applicationProjectRecognition, error) {
	root, err := openApplicationProjectSource(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	items := []applicationProjectRecognition{}
	if selected == "" {
		return discoverApplicationProjectModules(ctx, root), ctx.Err()
	}
	if selected != "." {
		if path.Clean(selected) != selected {
			return nil, os.ErrInvalid
		}
		parts := strings.Split(selected, "/")
		if len(parts) > 8 {
			return nil, os.ErrInvalid
		}
		for index, name := range parts {
			if !projectVisibleDirectory(name) || !projectSourceFile(root, strings.Join(parts[:index+1], "/"), true) {
				return nil, os.ErrInvalid
			}
		}
	}
	if module, ok := recognizeApplicationProjectModule(root, selected); ok {
		items = append(items, module)
	}
	return items, ctx.Err()
}

func discoverApplicationProjectModules(ctx context.Context, root *os.Root) []applicationProjectRecognition {
	type directory struct {
		path  string
		depth int
	}
	queue := []directory{{path: "."}}
	items := []applicationProjectRecognition{}
	for i := 0; i < len(queue) && i < 160 && len(items) < 32 && ctx.Err() == nil; i++ {
		current := queue[i]
		if module, ok := recognizeApplicationProjectModule(root, current.path); ok {
			items = append(items, module)
		}
		if current.depth == 2 || len(queue) >= 160 {
			continue
		}
		file, err := root.Open(current.path)
		if err != nil {
			continue
		}
		entries, _ := file.ReadDir(256)
		file.Close()
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() && projectVisibleDirectory(entry.Name()) {
				queue = append(queue, directory{path.Join(current.path, entry.Name()), current.depth + 1})
				if len(queue) == 160 {
					break
				}
			}
		}
	}
	return items
}

func projectVisibleDirectory(name string) bool {
	if !validProjectDirectory.MatchString(name) || strings.HasPrefix(name, ".") {
		return false
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "target", "build", "dist", "venv", "state", "coverage", "__pycache__":
		return false
	}
	return true
}

func projectSourceFile(root *os.Root, name string, directory bool) bool {
	info, err := root.Lstat(name)
	return err == nil && (directory && info.IsDir() || !directory && info.Mode().IsRegular())
}

func projectSourceText(root *os.Root, name string) []byte {
	const maxSize = 128 << 10
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil
	}
	f, err := root.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil
	}
	content, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || len(content) > maxSize {
		return nil
	}
	return content
}

func recognizeApplicationProjectModule(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	for _, recognize := range []func(*os.Root, string) (applicationProjectRecognition, bool){
		recognizeProjectJava, recognizeProjectNode, recognizeProjectPython,
	} {
		if module, ok := recognize(root, directory); ok {
			module.Directory = directory
			if directory != "." {
				module.Name = path.Base(directory)
			}
			return module, true
		}
	}
	return applicationProjectRecognition{}, false
}

type projectMavenArtifact struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

func recognizeProjectJava(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	file := path.Join(directory, "pom.xml")
	var pom struct {
		XMLName      xml.Name               `xml:"project"`
		ArtifactID   string                 `xml:"artifactId"`
		Packaging    string                 `xml:"packaging"`
		Parent       projectMavenArtifact   `xml:"parent"`
		Plugins      []projectMavenArtifact `xml:"build>plugins>plugin"`
		Dependencies []projectMavenArtifact `xml:"dependencies>dependency"`
		Properties   struct {
			Java    string `xml:"java.version"`
			Release string `xml:"maven.compiler.release"`
			Target  string `xml:"maven.compiler.target"`
		} `xml:"properties"`
	}
	if xml.Unmarshal(projectSourceText(root, file), &pom) != nil || strings.TrimSpace(pom.ArtifactID) == "" || pom.Packaging == "pom" {
		return applicationProjectRecognition{}, false
	}
	module := applicationProjectRecognition{Name: "Java 后端", Kind: "BACKEND", Evidence: []string{file}}
	environment := []string{"Java"}
	for _, version := range []string{pom.Properties.Release, pom.Properties.Java, pom.Properties.Target} {
		if version = strings.TrimSpace(version); projectVersion.MatchString(version) {
			environment[0] = "JDK " + version
			break
		}
	}
	bootParent := pom.Parent.GroupID == "org.springframework.boot" && pom.Parent.ArtifactID == "spring-boot-starter-parent"
	bootPlugin := false
	for _, plugin := range pom.Plugins {
		bootPlugin = bootPlugin || plugin.GroupID == "org.springframework.boot" && plugin.ArtifactID == "spring-boot-maven-plugin"
	}
	if bootParent || bootPlugin {
		boot := "Spring Boot"
		if bootParent && projectVersion.MatchString(pom.Parent.Version) {
			boot += " " + pom.Parent.Version
		}
		environment = append(environment, boot)
	}
	maven, command := "Maven", "mvn"
	if name := path.Join(directory, "mvnw"); projectSourceFile(root, name, false) {
		maven, command = "Maven Wrapper", "sh mvnw"
		module.Evidence = append(module.Evidence, name)
	} else if name := path.Join(directory, "mvnw.cmd"); projectSourceFile(root, name, false) {
		maven, command = "Maven Wrapper", ".\\mvnw.cmd"
		module.Evidence = append(module.Evidence, name)
	}
	environment = append(environment, maven)
	if bootPlugin || bootParent {
		module.StartCommand = command + " spring-boot:run"
		if evidence := projectDocumentedCommand(root, directory, module.StartCommand+" -Dspring-boot.run.profiles=local"); evidence != "" {
			module.StartCommand += " -Dspring-boot.run.profiles=local"
			module.Evidence = append(module.Evidence, evidence)
		}
	}
	var services []string
	for _, dep := range pom.Dependencies {
		switch dep.GroupID + ":" + dep.ArtifactID {
		case "com.mysql:mysql-connector-j", "mysql:mysql-connector-java":
			services = append(services, "MySQL")
		case "org.springframework.boot:spring-boot-starter-data-redis":
			services = append(services, "Redis")
		}
	}
	if len(services) > 0 {
		environment = append(environment, "运行依赖线索："+strings.Join(services, "、"))
	}
	module.Environment = strings.Join(environment, "、")
	return module, true
}

func recognizeProjectNode(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	file := path.Join(directory, "package.json")
	var pkg struct {
		PackageManager  string                     `json:"packageManager"`
		Engines         map[string]string          `json:"engines"`
		Scripts         map[string]string          `json:"scripts"`
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if json.Unmarshal(projectSourceText(root, file), &pkg) != nil || len(pkg.Scripts) == 0 && len(pkg.Dependencies) == 0 && len(pkg.DevDependencies) == 0 {
		return applicationProjectRecognition{}, false
	}
	module := applicationProjectRecognition{Name: "Node.js 模块", Kind: "OTHER", Evidence: []string{file}}
	var environment []string
	for _, entry := range []struct{ dependency, label string }{{"vue", "Vue"}, {"vite", "Vite"}, {"react", "React"}} {
		if pkg.Dependencies[entry.dependency] != nil || pkg.DevDependencies[entry.dependency] != nil {
			environment = append(environment, entry.label)
			module.Kind = "FRONTEND"
			module.Name = "前端"
		}
	}
	node := "Node.js"
	if version := strings.TrimSpace(pkg.Engines["node"]); projectVersionRange.MatchString(version) {
		node += " " + version
	}
	nvm := path.Join(directory, ".nvmrc")
	if version := strings.TrimSpace(string(projectSourceText(root, nvm))); projectVersion.MatchString(version) {
		node += "（.nvmrc " + version + "）"
		module.Evidence = append(module.Evidence, nvm)
	}
	environment = append(environment, node)
	manager, version, _ := strings.Cut(pkg.PackageManager, "@")
	version, _, _ = strings.Cut(version, "+")
	if manager != "pnpm" && manager != "npm" && manager != "yarn" {
		manager, version = "npm", ""
		for _, lock := range []struct{ name, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"package-lock.json", "npm"}} {
			if filename := path.Join(directory, lock.name); projectSourceFile(root, filename, false) {
				manager = lock.manager
				module.Evidence = append(module.Evidence, filename)
				break
			}
		}
	}
	tool := manager
	if projectVersion.MatchString(version) {
		tool += " " + version
	}
	environment = append(environment, tool)
	for _, script := range []string{"dev", "start"} {
		if strings.TrimSpace(pkg.Scripts[script]) != "" {
			module.StartCommand = manager + " " + script
			if manager == "npm" && script == "dev" {
				module.StartCommand = "npm run dev"
			}
			break
		}
	}
	module.Environment = strings.Join(environment, "、")
	return module, true
}

var projectPyTable = regexp.MustCompile(`\A\[([a-zA-Z0-9_.-]+)\][ \t]*(?:#.*)?\z`)
var projectPyValue = regexp.MustCompile(`\A([a-z-]+)[ \t]*=[ \t]*["']([^"']*)["'][ \t]*(?:#.*)?\z`)

func recognizeProjectPython(root *os.Root, directory string) (applicationProjectRecognition, bool) {
	file := path.Join(directory, "pyproject.toml")
	var table, version string
	project := false
	for line := range strings.SplitSeq(string(projectSourceText(root, file)), "\n") {
		line = strings.TrimSpace(line)
		if match := projectPyTable.FindStringSubmatch(line); len(match) != 0 {
			table = match[1]
			project = project || table == "project"
		} else if strings.HasPrefix(line, "[") {
			table = ""
		}
		if table == "project" {
			if match := projectPyValue.FindStringSubmatch(line); len(match) != 0 && match[1] == "requires-python" && projectVersionRange.MatchString(match[2]) {
				version = match[2]
			}
		}
	}
	if !project {
		return applicationProjectRecognition{}, false
	}
	module := applicationProjectRecognition{Name: "Python 模块", Kind: "OTHER", Environment: "Python", Evidence: []string{file}}
	for segment := range strings.SplitSeq(strings.ToLower(directory), "/") {
		if strings.HasPrefix(segment, "algorithm") || strings.HasPrefix(segment, "algo-") || segment == "node-agent" || segment == "inference" {
			module.Kind = "ALGORITHM"
			module.Name = "算法模块"
			break
		}
	}
	if version != "" {
		module.Environment += " " + version
	}
	entry := path.Join(directory, "app/__main__.py")
	runnable := projectSourceFile(root, path.Join(directory, "app"), true) && projectSourceFile(root, entry, false)
	if lock := path.Join(directory, "uv.lock"); projectSourceFile(root, lock, false) {
		module.Environment += "、uv"
		module.Evidence = append(module.Evidence, lock)
		if runnable {
			module.StartCommand = "uv run --locked python -m app"
		}
	} else if runnable {
		module.StartCommand = "python -m app"
	}
	if runnable {
		module.Evidence = append(module.Evidence, entry)
		if readme := projectDocumentedCommand(root, directory, module.StartCommand); readme != "" {
			module.Evidence = append(module.Evidence, readme)
		}
	}
	return module, true
}

// Only fixed, detector-generated commands can be matched. README text is never returned or run.
func projectDocumentedCommand(root *os.Root, directory, command string) string {
	if command == "" {
		return ""
	}
	for _, dir := range []string{directory, path.Dir(directory), "."} {
		readme := path.Join(dir, "README.md")
		previous := ""
		for line := range strings.SplitSeq(string(projectSourceText(root, readme)), "\n") {
			ownsCommand := dir == directory || strings.Contains(line, path.Base(directory)) || strings.Contains(previous, path.Base(directory))
			matches := strings.TrimSpace(line) == command || strings.Contains(line, "`"+command+"`")
			if ownsCommand && matches {
				return readme
			}
			previous = line
		}
	}
	return ""
}
