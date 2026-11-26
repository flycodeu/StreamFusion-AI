package projectinspect

import (
	"os"
	"path"
	"regexp"
	"strings"
)

var projectPyTable = regexp.MustCompile(`\A\[([a-zA-Z0-9_.-]+)\][ \t]*(?:#.*)?\z`)
var projectPyValue = regexp.MustCompile(`\A([a-z-]+)[ \t]*=[ \t]*["']([^"']*)["'][ \t]*(?:#.*)?\z`)

func recognizeProjectPython(root *os.Root, directory string) (Module, bool) {
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
		return Module{}, false
	}
	module := Module{
		Name:                   "Python 模块",
		Kind:                   "OTHER",
		Environment:            "Python",
		Evidence:               []string{file},
		RecommendedRuntimeMode: "NATIVE",
		ArtifactPath:           ".",
		Dependencies:           []string{"Python"},
	}
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
		module.Dependencies = appendUniqueStrings(module.Dependencies, "uv")
		module.Evidence = append(module.Evidence, lock)
		module.BuildCommand = "uv sync --locked"
		module.TestCommand = "uv run --locked python -m pytest -q"
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
	if projectSourceFile(root, path.Join(directory, "Dockerfile"), false) {
		module.SupportsContainer = true
	}
	module.ConfigTemplates = collectDirectoryConfigTemplates(root, directory, []string{"config.yaml", "config.yml", ".env", ".env.example"})
	return module, true
}

// Only fixed, detector-generated commands can be matched. README text is never returned or run.
