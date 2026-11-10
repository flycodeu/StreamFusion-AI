package deploy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandUninstallRefusesChangedOrUnreadableObjectBeforeScripts(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		name := "changed binary"
		if unreadable {
			name = "unreadable binary"
		}
		t.Run(name, func(t *testing.T) {
			req := commandTestRequest()
			i := *req.Installation
			binaryHash := Digest("installed binary")
			s := SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: "TOOL", Locator: "/opt/demo/bin/demo", SoftwareKey: "CUSTOM-DEMO", ExpectedVersion: "1", Ownership: &SoftwareOwnership{ControllerID: req.ControllerID, TargetID: req.TargetID, SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), RuntimeMode: "NATIVE", BinaryPath: "/opt/demo/bin/demo", BinaryHash: binaryHash, CommandTemplateHash: Digest(i.Definition)}}
			s.IdentityHash = Digest([]string{"TOOL", s.Locator, binaryHash})
			req.Installation, req.Software = nil, &s
			req.Action, req.SoftwareOperation = "SOFTWARE_UNINSTALL", "SOFTWARE_UNINSTALL"
			req.Uninstallation = &SoftwareUninstallRequest{Installation: i}
			x := executor{root: t.TempDir()}
			if err := os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
				t.Fatal(err)
			}
			binding, _ := json.Marshal(softwareBinding{Software: s, JobID: req.JobID})
			if err := os.WriteFile(x.softwareFile(s.ID), binding, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
				calls++
				if command != "/usr/bin/sha256sum" || len(args) != 2 || args[1] != s.Locator {
					t.Fatalf("unexpected target command: %s %v", command, args)
				}
				if unreadable {
					return nil, errors.New("read denied")
				}
				return []byte(Digest("replacement binary") + "  " + s.Locator + "\n"), nil
			}
			dir := t.TempDir()
			result, err := x.uninstallCommands(req, dir)
			if err == nil || err.Error() != "SOFTWARE_NATIVE_BINARY_DRIFT" || calls != 1 || result.Uninstallation != nil || result.Status == "SUCCEEDED" {
				t.Fatalf("unsafe uninstall: %+v, %v, calls=%d", result, err, calls)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("uninstall wrote step or completion evidence: %v, %v", entries, err)
			}
			if data, err := os.ReadFile(x.softwareFile(s.ID)); err != nil || string(data) != string(binding) {
				t.Fatalf("binding changed: %v", err)
			}
		})
	}
}
