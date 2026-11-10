package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestHostPreparationShellStaysInSources(t *testing.T) {
	// SQL, messages and other controller operations are outside this boundary.
	scope := regexp.MustCompile(`^(?:target_installer|host_remote_preparation|host_installation_authorization)(?:_.+)?\.go$`)
	shell := regexp.MustCompile(`(?m)^\s*(?:#!/|(?:/[^\s]+/)?(?:set|export|if|then|fi|for|while|do|done|case|esac|rm|mkdir|cp|install|printf|curl|tar|make|cmake|git|docker|systemctl|apt-get|chmod|chown|exec|test|id|uname)[ \t]+|[A-Za-z_][A-Za-z0-9_]*=["']?\$)|\$\([^\n]+\)`)
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
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			if shell.MatchString(value) {
				t.Errorf("%s: move host preparation shell to scripts/preparation/", positions.Position(literal.Pos()))
			}
			return true
		})
	}
}
