package serviceinstall

import (
	"fmt"
	"strings"
)

// NodeCommandScript shares account isolation, backup and path checks with the
// action scripts published to the command editor.
var NodeCommandScript = commandScript("node/commands.sh")

func nodeTemplateScript(actions ...string) string {
	start := strings.Index(NodeCommandScript, "\ncase \"$action\" in\n")
	header := NodeCommandScript[:start]
	for index, name := range []string{"action", "root", "version", "package", "material", "job", "default", "configs"} {
		header = strings.ReplaceAll(header, fmt.Sprintf("%s=$%d", name, index+1), name+"=\"${OPS_NODE_"+strings.ToUpper(name)+"}\"")
	}
	// The inspection-only helper is not part of mutable installation steps.
	if index := strings.Index(header, "\ninspect_version() {"); index >= 0 {
		header = header[:index]
	}
	result := ""
	end := "    ;;\n"
	for _, action := range actions {
		needle := "  " + action + ")\n"
		a := strings.Index(NodeCommandScript, needle)
		if a < 0 {
			panic("missing node action " + action)
		}
		b := a + strings.Index(NodeCommandScript[a:], end) + len(end)
		result += NodeCommandScript[a:b]
	}
	return commandScript("node/action-wrapper.sh.tmpl", header, result)
}
