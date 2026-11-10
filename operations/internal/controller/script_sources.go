package controller

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed scripts
var controllerSources embed.FS

func controllerScript(name string) string {
	data, err := controllerSources.ReadFile("scripts/" + name)
	if err != nil {
		panic(fmt.Sprintf("missing controller source %q: %v", name, err))
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func controllerTemplate(name string, args ...any) string {
	return fmt.Sprintf(controllerScript(name), args...)
}
