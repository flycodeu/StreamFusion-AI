package projectinspect

import (
	"encoding/json"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/projectconfig"
)

var (
	viteOutDirPattern   = regexp.MustCompile(`outDir\s*:\s*['"]([a-zA-Z0-9_./-]{1,64})['"]`)
	viteProxyKeyPattern = regexp.MustCompile(`['"]\^?(/[a-zA-Z0-9_-]+)(?:\(\?:[^'"]*)?['"]\s*:`)
	viteLocalTargetPort = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost):([0-9]{2,5})`)
)

func recognizeProjectNode(root *os.Root, directory string) (Module, bool) {
	file := path.Join(directory, "package.json")
	var pkg struct {
		PackageManager  string                     `json:"packageManager"`
		Engines         map[string]string          `json:"engines"`
		Scripts         map[string]string          `json:"scripts"`
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if json.Unmarshal(projectSourceText(root, file), &pkg) != nil || len(pkg.Scripts) == 0 && len(pkg.Dependencies) == 0 && len(pkg.DevDependencies) == 0 {
		return Module{}, false
	}
	module := Module{
		Name:                   "Node.js 模块",
		Kind:                   "OTHER",
		Evidence:               []string{file},
		RecommendedRuntimeMode: "NATIVE",
	}
	var environment []string
	var deps []string
	for _, entry := range []struct{ dependency, label string }{{"vue", "Vue"}, {"vite", "Vite"}, {"react", "React"}} {
		if pkg.Dependencies[entry.dependency] != nil || pkg.DevDependencies[entry.dependency] != nil {
			environment = append(environment, entry.label)
			deps = appendUniqueStrings(deps, entry.label)
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
	deps = appendUniqueStrings(deps, "Node.js")
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
	deps = appendUniqueStrings(deps, manager)
	for _, script := range []string{"dev", "start"} {
		if strings.TrimSpace(pkg.Scripts[script]) != "" {
			module.StartCommand = manager + " " + script
			if manager == "npm" && script == "dev" {
				module.StartCommand = "npm run dev"
			}
			break
		}
	}
	if strings.TrimSpace(pkg.Scripts["build"]) != "" {
		if manager == "yarn" {
			module.BuildCommand = "yarn build"
		} else {
			module.BuildCommand = manager + " run build"
		}
	}
	if strings.TrimSpace(pkg.Scripts["test"]) != "" {
		if manager == "pnpm" {
			module.TestCommand = "pnpm run test"
		} else {
			module.TestCommand = manager + " test"
		}
	}
	if module.Kind == "FRONTEND" {
		module.RecommendedRuntimeMode = "STATIC"
		module.ArtifactPath = "dist"
		deps = appendUniqueStrings(deps, "Nginx")
		for _, viteName := range []string{"vite.config.ts", "vite.config.mts", "vite.config.js", "vite.config.mjs"} {
			vitePath := path.Join(directory, viteName)
			viteText := string(projectSourceText(root, vitePath))
			if viteText == "" {
				continue
			}
			module.Evidence = append(module.Evidence, vitePath)
			if match := viteOutDirPattern.FindStringSubmatch(viteText); len(match) == 2 && projectconfig.ValidRelativePath(match[1]) {
				module.ArtifactPath = match[1]
			}
			lines := strings.Split(strings.ReplaceAll(viteText, "\r\n", "\n"), "\n")
			seenLoc := map[string]bool{}
			for i, line := range lines {
				m := viteProxyKeyPattern.FindStringSubmatch(line)
				if len(m) != 2 || seenLoc[m[1]] || len(module.ProxyHints) >= 8 {
					continue
				}
				seenLoc[m[1]] = true
				hint := projectconfig.ProxyHint{Location: m[1]}
				end := i + 20
				if end > len(lines) {
					end = len(lines)
				}
				block := strings.Join(lines[i:end], "\n")
				if strings.Contains(block, "rewrite") {
					hint.StripPrefix = true
				}
				if pm := viteLocalTargetPort.FindStringSubmatch(block); len(pm) == 2 {
					if port, convErr := strconv.Atoi(pm[1]); convErr == nil && port >= 1 && port <= 65535 {
						hint.TargetPort = port
					}
				}
				module.ProxyHints = append(module.ProxyHints, hint)
			}
			break
		}
	}
	if projectSourceFile(root, path.Join(directory, "Dockerfile"), false) {
		module.SupportsContainer = true
	}
	module.ConfigTemplates = collectDirectoryConfigTemplates(root, directory, []string{".env.production", ".env.test", ".env.development", ".env.local", ".env", ".env.example"})
	module.Dependencies = deps
	module.Environment = strings.Join(environment, "、")
	return module, true
}
