package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestFailedCommandInstallCanCreateFreshPlanOnlyAfterObjectCheck(t *testing.T) {
	for _, state := range []string{"absent", "old-python-missing", "other-dependency-missing", "changed-after-plan", "object-present", "probe-failed", "transport-failed", "old-executor", "unknown", "ack-pending"} {
		t.Run(state, func(t *testing.T) {
			s, b := setup(t)
			b.login()
			d := testCommandDefinition()
			d.Kind, d.Commands.Kind, d.Commands.Locator = "SERVICE", "SYSTEMD", "ops-demo-${OPS_SOFTWARE_ID}.service"
			d.Port = 8080
			d.Commands.Steps[1].RunAs = "ROOT"
			saved := dataOf(t, b.call("POST", "/command-templates", map[string]any{"definition": d}, 200))
			b.call("POST", "/command-templates/"+saved["id"].(string)+"/publish", map[string]any{"expectedVersion": saved["version"]}, 200)
			d, err := s.findInstallRecipe(d.Key, d.Version)
			if err != nil {
				t.Fatal(err)
			}
			var fixture *installFixture
			var oldID string
			oldProbes, runs := 0, 0
			host, f := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
				if req.Installation == nil {
					return nil, false
				}
				encode := func(v any) ([]byte, bool) { data, _ := json.Marshal(v); return data, true }
				switch req.Action {
				case "SOFTWARE_INSTALL_CHECK":
					i := *req.Installation
					root := serviceinstall.InstallRoot("/opt/plugin-test", i.SoftwareID, i.Config)
					checked := installPreflightFixture(i, root, true)
					checked.BinaryDirectory = root + "/bin"
					checked.Checks = []serviceinstall.Check{{Key: "EXISTING", Status: "PASS", Code: "COMMAND_OBJECT_ABSENT"}, {Key: "PORT_main", Status: "PASS", Code: "SERVICE_PORT_AVAILABLE"}}
					if i.SoftwareID == oldID {
						oldProbes++
						if state == "transport-failed" {
							return []byte("{broken target response"), true
						}
						checked.Ready = false
						checked.Checks = append(checked.Checks, serviceinstall.Check{Key: "DIRECTORY", Status: "FAIL", Code: "SERVICE_DATA_DIRECTORY_EXISTS"})
						if state == "object-present" || state == "changed-after-plan" && oldProbes > 1 {
							checked.Checks[0] = serviceinstall.Check{Key: "EXISTING", Status: "FAIL", Code: "COMMAND_OBJECT_ALREADY_EXISTS", Message: "上次安装的服务仍存在，请先核对服务状态"}
						}
						if state == "probe-failed" {
							checked.Checks[0] = serviceinstall.Check{Key: "EXISTING", Status: "FAIL", Code: "COMMAND_OBJECT_CHECK_UNAVAILABLE", Message: "无法读取上次安装的服务状态，请检查读取权限"}
						}
						if state == "old-executor" {
							checked.Checks = checked.Checks[2:]
						}
						if state == "old-python-missing" {
							checked.Checks = append(checked.Checks, serviceinstall.Check{Key: "PYTHON", Status: "FAIL", Code: "COMMAND_PYTHON_NOT_FOUND", Message: "旧解释器不存在"})
						}
						if state == "other-dependency-missing" {
							checked.Checks = append(checked.Checks, serviceinstall.Check{Key: "DEPENDENCY", Status: "FAIL", Code: "COMMAND_DEPENDENCY_MISSING", Message: "命令工具不可用"})
						}
					}
					return encode(checked)
				case "SOFTWARE_INSTALL":
					runs++
					result := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "FAILED", Phase: "SOFTWARE_REJECTED", ErrorCode: "COMMAND_STEP_FAILED", ObservedAt: Now()}
					fixture.mu.Lock()
					fixture.Results[req.JobID] = result
					fixture.mu.Unlock()
					if state == "unknown" {
						result.Status = "UNKNOWN"
					}
					return encode(result)
				}
				return nil, false
			})
			fixture = f
			fixture.NoAck.Store(state == "ack-pending")
			config := serviceinstall.Config{Name: "demo", RuntimeMode: "NATIVE", BindAddress: "127.0.0.1", HostPort: 18092, MemoryMiB: 256}
			planInput := map[string]any{"expectedHostVersion": "1", "recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "config": config, "source": map[string]string{"type": "ONLINE"}}
			p := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", planInput, 200))
			first := dataOf(t, b.call("POST", "/software-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "first-failed-command"}, 202))
			oldID = first["softwareInstanceId"].(string)
			job := first["jobId"].(string)
			var raw, receipt string
			if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			b.h.processSoftware(context.Background(), job, raw)
			if err = s.DB.QueryRow("SELECT result_json FROM jobs WHERE id=?", job).Scan(&receipt); err != nil {
				t.Fatal(err)
			}
			var newID, nextPlan string
			if state == "absent" || state == "old-python-missing" || state == "changed-after-plan" {
				next := dataOf(t, b.call("POST", "/hosts/"+host+"/software-install-plans", planInput, 200))
				nextPlan = next["id"].(string)
				if state == "changed-after-plan" {
					response := b.call("POST", "/software-install-plans/"+nextPlan+"/submit", map[string]string{"idempotencyKey": "fresh-after-failure"}, 409)
					if !strings.Contains(response.Body.String(), "MANAGEMENT_RESOURCE_CONFLICT") || oldProbes != 2 {
						t.Fatal("submission used stale absence evidence", response.Body.String(), oldProbes)
					}
				} else {
					accepted := dataOf(t, b.call("POST", "/software-install-plans/"+next["id"].(string)+"/submit", map[string]string{"idempotencyKey": "fresh-after-failure"}, 202))
					newID = accepted["softwareInstanceId"].(string)
					if accepted["softwareInstanceId"] == oldID || accepted["jobId"] == job || oldProbes != 2 {
						t.Fatal("retry reused original identity or omitted create/submit check", accepted, oldProbes)
					}
					var newRaw string
					if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", accepted["jobId"]).Scan(&newRaw); err != nil {
						t.Fatal(err)
					}
					var nextReq deploy.Request
					if json.Unmarshal([]byte(newRaw), &nextReq) != nil || deploy.Digest(nextReq.Installation.Config) != deploy.Digest(config) {
						t.Fatal("new job lost submitted configuration")
					}
				}
			} else {
				response := b.call("POST", "/hosts/"+host+"/software-install-plans", planInput, 409)
				code := "MANAGEMENT_RESOURCE_CONFLICT"
				if state == "old-executor" {
					code = "EXECUTOR_UPGRADE_REQUIRED"
				}
				if state == "transport-failed" {
					code = "EXECUTOR_RESPONSE_INVALID"
					if strings.Contains(response.Body.String(), "MANAGEMENT_RESOURCE_CONFLICT") || strings.Contains(response.Body.String(), "broken target response") {
						t.Fatal("target invocation error was masked or leaked", response.Body.String())
					}
				}
				if state == "object-present" && !strings.Contains(response.Body.String(), "上次安装的服务仍存在") || state == "probe-failed" && !strings.Contains(response.Body.String(), "检查读取权限") {
					t.Fatal("target check explanation was lost", response.Body.String())
				}
				if state == "unknown" || state == "ack-pending" {
					code = "OPERATION_LOCKED"
					if oldProbes != 0 {
						t.Fatal("probed unresolved original operation")
					}
				}
				if !strings.Contains(response.Body.String(), code) {
					t.Fatal(response.Body.String())
				}
			}
			var afterRaw, afterReceipt string
			if err = s.DB.QueryRow("SELECT input_json,result_json FROM jobs WHERE id=?", job).Scan(&afterRaw, &afterReceipt); err != nil || afterRaw != raw || afterReceipt != receipt || runs != 1 {
				t.Fatal("original operation was changed or replayed", err, runs)
			}
			if state == "absent" {
				original, err := s.readSoftware(oldID)
				if err != nil || original.InstallationStatus != "FAILED" || original.InstallationJobID != job {
					t.Fatal("original failed history was replaced", err)
				}
				tx, err := s.DB.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				oldRoot := serviceinstall.InstallRoot("/opt/plugin-test", oldID, config)
				proof := resourceClaimExemptions{AbsentInstallations: map[string]string{oldID: original.Version}}
				if err = reserveManagementResources(tx, host, newID, nextPlan, []serviceinstall.ResolvedResource{{Type: "PATH_WRITE", Value: oldRoot}}, proof); err == nil {
					t.Fatal("failed directory lost protection")
				}
				port := []serviceinstall.ResolvedResource{{Type: "PORT", Value: "18092", BindAddress: "127.0.0.1", Protocol: "TCP"}}
				if err = reserveManagementResources(tx, host, newID, nextPlan, port, proof); err != nil {
					t.Fatal("fresh absence proof rejected", err)
				}
				if err = reserveManagementResources(tx, host, newID, nextPlan, port, resourceClaimExemptions{AbsentInstallations: map[string]string{oldID: "stale"}}); err == nil {
					t.Fatal("stale absence proof accepted")
				}
				if _, err = tx.Exec("UPDATE jobs SET target_ack_pending=1 WHERE id=?", job); err != nil {
					t.Fatal(err)
				}
				if err = reserveManagementResources(tx, host, newID, nextPlan, port, proof); err == nil {
					t.Fatal("changed acknowledgement state accepted")
				}
			}
		})
	}
}
