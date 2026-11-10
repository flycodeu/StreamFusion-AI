package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestBindingResourcesOverlayConfirmedWebPortWithoutRevivingInstallPort(t *testing.T) {
	x := executor{root: t.TempDir()}
	req := commandTestRequest()
	i := req.Installation
	i.Definition.Kind = "SERVICE"
	i.Definition.Commands.Kind = "SYSTEMD"
	i.Definition.Web = &serviceinstall.WebAccessDefinition{PortKey: "main"}
	i.Definition.Port = 9924
	i.Definition.Ports = []serviceinstall.PortDescriptor{{Key: "main", Protocol: "TCP", ContainerPort: 9924}, {Key: "admin", Protocol: "TCP", DefaultPort: 9000, ContainerPort: 9000}}
	i.Config.BindAddress, i.Config.HostPort = "127.0.0.1", 9924
	i.Config.Ports = map[string]int{"admin": 9000}
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(dir, "intent.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	confirmed := serviceinstall.ResolvedResource{Key: "main", Type: "PORT", Value: "19924", BindAddress: "0.0.0.0", Protocol: "TCP", IdentityHash: Digest("confirmed")}
	binding := softwareBinding{Software: SoftwareRequest{ID: i.SoftwareID, Kind: "SYSTEMD", Locator: "test.service"}, JobID: req.JobID, Resources: []serviceinstall.ResolvedResource{confirmed}}
	ports := map[string]serviceinstall.ResolvedResource{}
	for _, resource := range x.bindingResources(binding) {
		if resource.Type == "PORT" {
			ports[resource.Value] = resource
		}
	}
	if _, exists := ports["9924"]; exists || len(ports) != 2 || ports["19924"] != confirmed || ports["9000"].BindAddress != "127.0.0.1" {
		t.Fatal("old Web claim survived or another listener moved", ports)
	}
}
