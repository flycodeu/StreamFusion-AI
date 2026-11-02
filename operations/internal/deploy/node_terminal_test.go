package deploy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNodeTerminalBlockPreservesContentAndRejectsOtherManagers(t *testing.T) {
	n := testNodeContext()
	original := []byte("# existing user configuration\r\nexport KEEP_ME='unchanged'")
	result, connected, err := nodeTerminalContent(original, n)
	if err != nil || connected || !bytes.HasPrefix(result, original) {
		t.Fatalf("preserve: %v", err)
	}
	again, connected, err := nodeTerminalContent(result, n)
	if err != nil || !connected || !bytes.Equal(result, again) {
		t.Fatal("repeat changed profile")
	}
	for _, foreign := range []string{"export NVM_DIR=~/.nvm\n", ". ~/.nvm/nvm.sh\n", "# >>> streamfusion-ops node other >>>\n"} {
		if _, _, err = nodeTerminalContent([]byte(foreign), n); err == nil {
			t.Fatal("foreign manager accepted")
		}
	}
	malformed := bytes.Replace(result, []byte("--no-use"), []byte("--edited"), 1)
	if _, _, err = nodeTerminalContent(malformed, n); err == nil {
		t.Fatal("edited owned block silently overwritten")
	}
	if !bytes.Contains(result, []byte("--no-use")) || !bytes.Contains(result, []byte("nvm version default")) {
		t.Fatal("no-default compatibility missing")
	}
}
func TestNodeTerminalObservationValidationAndLegacyContext(t *testing.T) {
	n := testNodeContext()
	raw, _ := json.Marshal(n)
	if strings.Contains(string(raw), "connectTerminal") || strings.Contains(string(raw), "terminalHash") {
		t.Fatal("legacy input changed")
	}
	home := strings.Split(n.RootPath, "/.local/share/")[0]
	obs := &NodeTerminalObservation{Status: "CONNECTED", Profiles: []NodeTerminalProfile{{Path: home + "/.bashrc", SHA256: strings.Repeat("a", 64), Connected: true}, {Path: home + "/.profile", SHA256: strings.Repeat("b", 64), Connected: true}}}
	if obs.Validate(n) != nil {
		t.Fatal("valid snapshot rejected")
	}
	obs.Profiles[0].Path = "/etc/profile"
	if obs.Validate(n) == nil {
		t.Fatal("foreign path accepted")
	}
	obs.Profiles[0].Path = home + "/.bashrc"
	obs.Profiles[1].Connected = false
	if obs.Validate(n) == nil {
		t.Fatal("false connection accepted")
	}
}
