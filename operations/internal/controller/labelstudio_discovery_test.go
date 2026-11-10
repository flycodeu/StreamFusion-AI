package controller

import (
	"strings"
	"testing"
)

func TestLabelStudioDiscoveryRegistersExistingServiceReadOnly(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
		switch {
		case strings.Contains(command, "'list-unit-files'"):
			return []byte("label-studio.service enabled enabled\n"), true
		case strings.Contains(command, "'list-units'"):
			return []byte("label-studio.service loaded active running Label Studio\n"), true
		case strings.Contains(command, "'software-metadata'"):
			return []byte("FILE\tlabel-studio.service\t/etc/systemd/system/label-studio.service\nEXE\tlabel-studio.service\t/opt/anaconda3/bin/python3.11\nPACKAGE\tlabel-studio.service\tlabel-studio\t1.22.0\n"), true
		}
		out, ok := unitReadResponse(command)
		return []byte(strings.ReplaceAll(string(out), "zlm.service", "label-studio.service")), ok
	})
	host := f.create(t, b, "annotation host")
	hostID := host["id"].(string)
	report := readDiscovery(t, b, hostID, "1")
	var found bool
	for _, item := range report.Candidates {
		if item.Locator == "label-studio.service" {
			found = item.SoftwareType == "LABEL_STUDIO" && item.State == "RUNNING" && item.Version == "1.22.0" && item.RegisteredID == ""
		}
	}
	if !found {
		t.Fatal("existing Label Studio was not discovered", report)
	}
	saved := dataOf(t, b.call("POST", "/software-instances", map[string]string{"name": "Label Studio", "hostId": hostID, "softwareType": "LABEL_STUDIO", "softwareVersion": "1.22.0", "runtimeKind": "SYSTEMD", "locator": "label-studio.service"}, 201))
	id := saved["id"].(string)
	if saved["managementMode"] != "OBSERVE" || saved["dataOwnership"] != "EXTERNAL" || saved["installation"] != nil {
		t.Fatal("external service was claimed as an Ops installation", saved)
	}
	checked := dataOf(t, b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200))
	if checked["actualState"] != "RUNNING" || checked["managementMode"] != "OBSERVE" || checked["observation"].(map[string]any)["version"] != "1.22.0" {
		t.Fatal(checked)
	}
	b.call("POST", "/software-instances/"+id+"/actions", map[string]string{"expectedVersion": checked["version"].(string), "action": "STOP", "idempotencyKey": "no-label-takeover"}, 409)
	var jobs int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("discovery/registration created a mutation job", jobs, err)
	}
	for _, item := range readDiscovery(t, b, hostID, "1").Candidates {
		if item.Locator == "label-studio.service" && item.RegisteredID != id {
			t.Fatal("registered service still offered as a new registration", item)
		}
	}
}
