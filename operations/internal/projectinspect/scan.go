package projectinspect

import (
	"context"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	validProjectDirectory = regexp.MustCompile(`^[\p{L}\p{N} _.-]{1,80}$`)
	projectVersion        = regexp.MustCompile(`^[vV]?[0-9]{1,4}(?:\.(?:[0-9]{1,4}|[xX*])){0,3}$`)
	projectVersionRange   = regexp.MustCompile(`^[0-9<>=~^., |*xXvV-]{1,48}$`)
)

// OpenCheckout opens a caller-selected Git checkout without executing its contents.
func OpenCheckout(rootPath string) (*os.Root, error) {
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

// Scan finds modules in a checkout selected by the caller. It reads source files
// and does not run build tools or use repository credentials.
func Scan(ctx context.Context, root *os.Root, selected string) ([]Module, error) {
	items := []Module{}
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

func discoverApplicationProjectModules(ctx context.Context, root *os.Root) []Module {
	type directory struct {
		path  string
		depth int
	}
	queue := []directory{{path: "."}}
	items := []Module{}
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

func recognizeApplicationProjectModule(root *os.Root, directory string) (Module, bool) {
	for _, recognize := range []func(*os.Root, string) (Module, bool){
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
	return Module{}, false
}

func appendUniqueStrings(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, existing := range dst {
		seen[strings.ToLower(existing)] = true
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[strings.ToLower(item)] {
			continue
		}
		seen[strings.ToLower(item)] = true
		dst = append(dst, item)
	}
	return dst
}

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
