package controller

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func failedManagementPortFixture(t *testing.T, s *Store, b *browser, host string) (string, string) {
	t.Helper()
	id, plan, job, now := uuid.NewString(), uuid.NewString(), uuid.NewString(), Now()
	d := testCommandDefinition()
	d.Kind, d.Port, d.Commands.Kind, d.Commands.Locator = "SERVICE", 19443, "SYSTEMD", "ops-app-${OPS_SOFTWARE_ID}.service"
	i := serviceinstall.Request{SoftwareID: id, Definition: d, Config: serviceinstall.Config{Name: "old failed", RuntimeMode: "NATIVE", BindAddress: "127.0.0.1", HostPort: 19443, MemoryMiB: 256}, Source: serviceinstall.Source{Type: "ONLINE"}}
	if _, err := s.DB.Exec("INSERT INTO software_install_plans(id,actor_id,target_id,snapshot_json,status,expires_at,created_at) VALUES(?,?,?,?,'SUBMITTED',?,?)", plan, b.currentAdmin(), host, "{}", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,software_plan_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_INSTALL',?,?,?,?,'FAILED','COMMAND_INSTALL','{}',?,?,?)", job, b.currentAdmin(), host, plan, "old-failed-"+id, id, uuid.NewString(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO software_instances(id,name,host_id,software_type,runtime_kind,locator,install_plan_id,installation_json,installation_status,last_job_id,created_at,updated_at) VALUES(?,'old failed',?,'CUSTOM','SYSTEMD',?,?,?,'FAILED',?,?,?)", id, host, "pending:"+id, plan, jsonText(i), job, now, now); err != nil {
		t.Fatal(err)
	}
	return id, job
}

func TestManagementVerifiedPortIgnoresOnlyUnboundFinalFailedClaims(t *testing.T) {
	for _, mode := range []string{"ready", "version-changed", "unknown", "ack-pending", "has-identity", "no-port-proof", "target-changed"} {
		t.Run(mode, func(t *testing.T) {
			s, b, id, template, fixture := readyManagementFixture(t)
			i, _ := s.readSoftware(id)
			d := *template.Published
			d.Management.Resources = append(d.Management.Resources, serviceinstall.ResourceDefinition{Key: "listener", Type: "PORT", Value: "19443", BindAddress: "0.0.0.0", Protocol: "TCP"})
			template.Published = &d
			if _, err := s.DB.Exec("UPDATE command_templates SET published_json=?,draft_json=? WHERE id=?", jsonText(d), jsonText(d), template.ID); err != nil {
				t.Fatal(err)
			}
			old, job := failedManagementPortFixture(t, s, b, i.HostID)
			switch mode {
			case "unknown":
				s.DB.Exec("UPDATE jobs SET status='UNKNOWN' WHERE id=?", job)
			case "ack-pending":
				s.DB.Exec("UPDATE jobs SET target_ack_pending=1 WHERE id=?", job)
			case "has-identity":
				s.DB.Exec("UPDATE software_instances SET identity_hash='existing-proof' WHERE id=?", old)
			case "no-port-proof":
				fixture.noPortIdentity.Store(true)
			}
			input := map[string]any{"expectedVersion": i.Version, "expectedManagementVersion": "0", "templateId": template.ID, "templateRevision": template.Revision, "operationKey": "apply"}
			if mode == "unknown" || mode == "ack-pending" || mode == "has-identity" || mode == "no-port-proof" {
				response := b.call("POST", "/software-instances/"+id+"/operation-plans", input, 409)
				if !strings.Contains(response.Body.String(), "MANAGEMENT_RESOURCE_CONFLICT") {
					t.Fatal(response.Body.String())
				}
				return
			}
			response := b.call("POST", "/software-instances/"+id+"/operation-plans", input, 200)
			if strings.Contains(response.Body.String(), "failedPortClaims") {
				t.Fatal("private conflict proof was exposed")
			}
			plan := dataOf(t, response)
			var raw string
			if err := s.DB.QueryRow("SELECT snapshot_json FROM software_configuration_plans WHERE id=?", plan["id"]).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			stored, err := decodeSoftwareOperationPlan(raw)
			if err != nil || stored.FailedPortClaims[old] != "1" {
				t.Fatal("failed instance/version was not frozen", err)
			}
			want := 202
			if mode == "version-changed" {
				s.DB.Exec("UPDATE software_instances SET version=version+1 WHERE id=?", old)
				want = 409
			}
			if mode == "target-changed" {
				fixture.checkBlocked.Store(true)
				want = 409
			}
			b.call("POST", "/software-operation-plans/"+plan["id"].(string)+"/submit", map[string]string{"idempotencyKey": "verified-port-" + mode}, want)
		})
	}
}

func TestManagementVerifiedPortProofRetainsDirectoryUnitAndUnknownProtection(t *testing.T) {
	s, b, id, _, _ := readyManagementFixture(t)
	i, _ := s.readSoftware(id)
	host, _ := s.readTarget(i.HostID)
	old, job := failedManagementPortFixture(t, s, b, i.HostID)
	port := serviceinstall.ResolvedResource{Type: "PORT", Value: "19443", BindAddress: "0.0.0.0", Protocol: "TCP", IdentityHash: hashToken("target-confirmed")}
	proof := resourceClaimExemptions{VerifiedPorts: map[string]string{old: "1"}}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = reserveManagementResources(tx, i.HostID, id, "", []serviceinstall.ResolvedResource{port}, proof); err != nil {
		t.Fatal("confirmed port still blocked", err)
	}
	for _, protected := range []serviceinstall.ResolvedResource{{Type: "PATH_WRITE", Value: host.InstallRoot + "/software/" + old}, {Type: "UNIT", Value: "ops-app-" + old + ".service"}} {
		if err = reserveManagementResources(tx, i.HostID, id, "", []serviceinstall.ResolvedResource{port, protected}, proof); err == nil {
			t.Fatal("port evidence released another resource", protected.Type)
		}
	}
	if _, err = tx.Exec("UPDATE jobs SET target_ack_pending=1 WHERE id=?", job); err != nil {
		t.Fatal(err)
	}
	if err = reserveManagementResources(tx, i.HostID, id, "", []serviceinstall.ResolvedResource{port}, proof); err == nil {
		t.Fatal("proof accepted after acknowledgement state changed")
	}
}
