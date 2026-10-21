package controller

import (
	"strings"
	"testing"
)

func TestExistingMediaMTXAndMinIORegisterWithoutExecutor(t *testing.T) {
	for _, typ := range []string{"MEDIAMTX", "MINIO"} {
		t.Run(typ, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			unit := strings.ToLower(typ) + ".service"
			f := hostSSHServer(t, s, b, false, false, func(command string) ([]byte, bool) {
				out, ok := unitReadResponse(command)
				return []byte(strings.ReplaceAll(string(out), "zlm.service", unit)), ok
			})
			host := f.create(t, b, "existing service")
			item := registerTestSoftware(t, b, host["id"].(string), "SYSTEMD", unit, typ)
			id := item["id"].(string)
			checked := dataOf(t, b.call("POST", "/software-instances/"+id+"/observe", map[string]any{}, 200))
			if checked["actualState"] != "RUNNING" || checked["managementMode"] != "OBSERVE" || checked["softwareType"] != typ || checked["dataOwnership"] != "EXTERNAL" {
				t.Fatal(checked)
			}
			obs := checked["observation"].(map[string]any)
			if obs["version"] != "" || obs["identityHash"] == "" {
				t.Fatal("unproven version or missing identity", obs)
			}
			logs := b.call("GET", "/software-instances/"+id+"/logs", nil, 200).Body.String()
			if strings.Contains(logs, "secret-example") || !strings.Contains(logs, "REDACTED") {
				t.Fatal("external logs not redacted")
			}
			b.call("POST", "/software-instances/"+id+"/actions", map[string]any{"expectedVersion": checked["version"], "action": "STOP", "idempotencyKey": "readonly-no-stop"}, 409)
			var jobs int
			if err := s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
				t.Fatal("registration changed the remote service", jobs, err)
			}
			b.call("POST", "/software-instances/"+id+"/unmanage", map[string]any{"expectedVersion": checked["version"], "idempotencyKey": "release-external"}, 200)
		})
	}
}
