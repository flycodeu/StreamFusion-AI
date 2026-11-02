package deploy

import (
	"encoding/json"
	"path"
	"strings"
	"testing"
)

func TestNodeRegistrySnapshotAndLegacyContracts(t *testing.T) {
	n := testNodeContext()
	raw, _ := json.Marshal(n)
	if strings.Contains(string(raw), "observeRegistry") {
		t.Fatal("legacy context changed")
	}
	home := strings.Split(n.RootPath, "/.local/share/")[0]
	obs := &NodeRegistryObservation{Status: "DEFAULT", URL: defaultNodeRegistry, ConfigPath: path.Join(home, ".npmrc")}
	if obs.Validate(n) != nil {
		t.Fatal("valid default rejected")
	}
	obs.Status = "CONFIGURED"
	if obs.Validate(n) == nil {
		t.Fatal("configured registry without file accepted")
	}
	obs.Status = "DEFAULT"
	obs.URL = "https://example.com/"
	if obs.Validate(n) == nil {
		t.Fatal("nonofficial default accepted")
	}
	obs.Status = "REDACTED"
	obs.URL = "https://name:secret@example.com/"
	if obs.Validate(n) == nil {
		t.Fatal("credentials accepted")
	}
	obs.URL = ""
	if obs.Validate(n) != nil {
		t.Fatal("redacted observation rejected")
	}
	obs.ConfigPath = "/root/.npmrc"
	if obs.Validate(n) == nil {
		t.Fatal("foreign config path accepted")
	}
	req := NodeEnvironmentRequest{Context: n, Action: "SET_NPM_REGISTRY", NodeVersion: "v22.23.4", RegistryURL: defaultNodeRegistry}
	if req.Validate() == nil {
		t.Fatal("missing observation intent accepted")
	}
	req.Context.ObserveRegistry = true
	if req.Validate() != nil {
		t.Fatal("valid change rejected")
	}
	req.Action = "SET_DEFAULT"
	if req.Validate() == nil {
		t.Fatal("registry on another action accepted")
	}
}
