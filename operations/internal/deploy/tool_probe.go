package deploy

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TOOL_PROBE has the original probe envelope, but Compose is optional. The
// original PROBE still gates application Compose releases independently.
func (x *executor) toolProbe(output io.Writer) error {
	v, err := x.docker("version", "--format", "{{.Server.Version}}")
	if err != nil {
		return err
	}
	c, _ := x.docker("compose", "version", "--short")
	storage, free, err := x.dockerStorage()
	if err != nil {
		return err
	}
	pendingID := ""
	var pending Request
	if readJSON(filepath.Join(x.root, "pending.json"), &pending) == nil {
		pendingID = pending.JobID
	} else if _, err := os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{"owner": x.owner, "dockerVersion": strings.TrimSpace(string(v)), "composeVersion": strings.TrimSpace(string(c)), "platform": "linux/amd64", "pendingJobId": pendingID, "dockerRootDir": storage, "dockerAvailableBytes": free, "pluginCatalogHash": pluginCatalogHash()})
}
