package serviceinstall

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

// Check production source, not test fixtures. Commands belong in templates so
// an installation change has one visible maintenance location.
func TestInstallationCommandsStayInTemplateSources(t *testing.T) {
	shell := regexp.MustCompile(`(?m)^\s*(?:(?:/[^\s]+/)?(?:apt-get|systemctl|docker|rm|mkdir|cp|install|printf|curl|tar|make|cmake|git|java|javac|mvn|node|npm|pnpm|nvm|ffmpeg|mysqld|redis-server|nginx|echo|chmod|chown|exec)[ \t]+|[A-Za-z_][A-Za-z0-9_]*=["']?\$)|\$\([^\n]+\)|(?s)\$\{?OPS_[^\n]*[ \t]+[^\n]+`)
	argvExecutable := regexp.MustCompile(`^(?:/(?:usr/)?s?bin/)?(?:apt-get|systemctl|make|cmake|bash|sh)$`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.KeyValueExpr:
				if key, ok := node.Key.(*ast.Ident); ok {
					// Executable paths in dependency metadata are not commands.
					if key.Name == "RequiredTools" {
						return false
					}
					if key.Name == "Script" || key.Name == "Executable" || key.Name == "Args" {
						ast.Inspect(node.Value, func(value ast.Node) bool {
							if call, ok := value.(*ast.CallExpr); ok {
								if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "commandScript" {
									return false
								}
							}
							if literal, ok := value.(*ast.BasicLit); ok && literal.Kind == token.STRING && literal.Value != `""` {
								t.Errorf("%s: move inline %s to templates/", positions.Position(literal.Pos()), key.Name)
							}
							return true
						})
					}
				}
			case *ast.CompositeLit:
				if list, ok := node.Type.(*ast.ArrayType); ok && list.Len == nil && len(node.Elts) > 0 {
					if item, ok := list.Elt.(*ast.Ident); ok && item.Name == "string" {
						if first, ok := node.Elts[0].(*ast.BasicLit); ok && first.Kind == token.STRING {
							executable, _ := strconv.Unquote(first.Value)
							if argvExecutable.MatchString(executable) {
								t.Errorf("%s: move fixed command argv to templates/", positions.Position(first.Pos()))
							}
						}
					}
				}
			}
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			if shell.MatchString(value) {
				t.Errorf("%s: move installation shell to templates/: %q", positions.Position(literal.Pos()), value)
			}
			return true
		})
	}
}
