package deploy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSoftwareCommandsStayInSources(t *testing.T) {
	// Limit this check to software installation, account environments and their
	// templates. Firewall rules and other executor protocols have separate owners.
	scope := regexp.MustCompile(`^(?:(?:git|node|native|official|software|builtin|tool|docker)(?:_.+)?|service_templates|command_recipe|script_sources|installation_log)\.go$`)
	shellOrConfig := regexp.MustCompile(`(?m)^\s*(?:#!/|(?:set|export|if|then|fi|for|while|do|done|case|esac|rm|mkdir|cp|install|printf|curl|tar|make|cmake|git|docker|systemctl|apt-get|chmod|chown|exec)\b|[A-Za-z_][A-Za-z0-9_]*=|\[[^\n]+\]|[A-Za-z_][A-Za-z0-9_.-]*[ \t]+[^\n]+[;{]|[A-Za-z_][A-Za-z0-9_.-]*[ \t]*=|[\[{])`)
	mutations := strings.Fields("install update upgrade remove purge autoremove download enable disable start stop restart reload daemon-reload mask unmask link preset clean pull load run build create kill rm rmi tag rename prune")
	stringValue := func(expr ast.Expr) string {
		if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			value, _ := strconv.Unquote(literal.Value)
			return value
		}
		return ""
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || !scope.MatchString(path) {
			continue
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				value := stringValue(literal)
				lines := 0
				for _, line := range strings.Split(value, "\n") {
					if strings.TrimSpace(line) != "" {
						lines++
					}
				}
				if lines > 1 && shellOrConfig.MatchString(value) {
					t.Errorf("%s: move inline installation shell/config to scripts/", positions.Position(literal.Pos()))
				}
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			start, executable := 0, ""
			switch method.Sel.Name {
			case "softwareRunner":
				start = 2
			case "loggedSoftwareCommand":
				start = 4
			case "serviceDocker":
				start, executable = 3, "docker"
			default:
				return true
			}
			if executable == "" {
				if len(call.Args) < start {
					return true
				}
				executable = filepath.Base(stringValue(call.Args[start-1]))
			}
			args := []string{}
			for _, argument := range call.Args[start:] {
				ast.Inspect(argument, func(value ast.Node) bool {
					if literal, ok := value.(*ast.BasicLit); ok && literal.Kind == token.STRING {
						args = append(args, stringValue(literal))
					}
					return true
				})
			}
			mutates := false
			switch executable {
			case "apt-get":
				if !slices.Contains(args, "--simulate") && !slices.Contains(args, "--dry-run") && !slices.Contains(args, "-s") {
					for _, arg := range args {
						mutates = mutates || slices.Contains(mutations, arg)
					}
				}
			case "docker", "systemctl":
				verbs := args
				for len(verbs) > 0 && strings.HasPrefix(verbs[0], "-") {
					verbs = verbs[1:]
				}
				if executable == "docker" && len(verbs) > 0 && slices.Contains([]string{"container", "image", "network", "volume"}, verbs[0]) {
					verbs = verbs[1:]
				}
				mutates = len(verbs) > 0 && slices.Contains(mutations, verbs[0])
			case "make", "cmake":
				mutates = !slices.Contains(args, "--version") && !slices.Contains(args, "--help")
			case "useradd", "usermod", "groupadd", "gpasswd", "rm", "mkdir", "cp", "mv", "ln", "install", "chmod", "chown":
				mutates = true
			}
			if mutates {
				t.Errorf("%s: move fixed %s mutation argv to scripts/*/commands.json", positions.Position(call.Pos()), executable)
			}
			return true
		})
	}
}
