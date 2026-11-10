package serviceinstall

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed templates
var commandSources embed.FS

// Template files preserve their whitespace because scripts also form part of
// published recipe snapshots and hashes. Only checkout line endings normalize.
func commandScript(name string, args ...any) string {
	raw, err := commandSources.ReadFile("templates/" + name)
	if err != nil {
		panic(err)
	}
	script := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if len(args) > 0 {
		return fmt.Sprintf(script, args...)
	}
	return script
}
