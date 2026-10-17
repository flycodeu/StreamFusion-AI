package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPreviewHandoffReadsOnlyProtectedIdentity(t *testing.T) {
	x := executor{root: t.TempDir()}
	release := Release{InstanceID: uuid.NewString()}
	dir := x.previewIdentity(release)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	connection := `{"nodeId":"preview-a","name":"preview-a","controlUri":"https://127.0.0.1:9443","certificateSha256":"` + strings.Repeat("a", 64) + `","tokenFile":"/REPLACE/with/protected/platform/machine-token","mediaAddress":"127.0.0.1"}`
	if err := os.WriteFile(filepath.Join(dir, "connection.json"), []byte(connection), 0600); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("b", 64)
	if err := os.WriteFile(filepath.Join(dir, "machine-token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	material, err := x.previewHandoff(release)
	if err != nil || material.Connection.NodeID != "preview-a" || material.MachineToken != token {
		t.Fatalf("handoff material wrong: %+v %v", material.Connection, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(filepath.Join(dir, "machine-token"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := x.previewHandoff(release); err == nil {
		t.Fatal("public token file accepted")
	}
}
