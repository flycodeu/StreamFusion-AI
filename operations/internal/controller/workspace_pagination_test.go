package controller

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestWorkspacePagesKeepAllHistoryAndLegacyArrays(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "分页主机", "kind": "LOCAL"}, 201))["id"].(string)
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	config := deploy.Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/health", HealthSeconds: 10, DataCompatibility: "v1"}
	instanceID := "22222222-2222-4222-8222-000000000000"
	for index := range 127 {
		artifact := fmt.Sprintf("11111111-1111-4111-8111-%012d", index)
		instance := fmt.Sprintf("22222222-2222-4222-8222-%012d", index)
		release := fmt.Sprintf("33333333-3333-4333-8333-%012d", index)
		binding := fmt.Sprintf("44444444-4444-4444-8444-%012d", index)
		created := "2026-11-10T00:00:00Z"
		for _, insert := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?,?,?,1,'sha256:test',?)", []any{artifact, artifact, hashToken(artifact), created}},
			{"INSERT INTO instances(id,name,target_id,draft_json,created_at) VALUES(?,?,?,?,?)", []any{instance, instance, host, jsonText(config), created}},
			{"INSERT INTO releases VALUES(?,?,?,?,?)", []any{release, instanceID, artifact, jsonText(deploy.Release{ID: release, InstanceID: instanceID, ArtifactID: artifact, ImageID: "sha256:test", Config: config}), created}},
			{"INSERT INTO component_bindings(id,config_json,created_at) VALUES(?,?,?)", []any{binding, jsonText(deploy.Binding{ID: binding, Kind: "MYSQL", Host: "localhost", Port: 3306, Database: "test"}), created}},
		} {
			if _, err = tx.Exec(insert.query, insert.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/artifacts", "/instances", "/bindings", "/instances/" + instanceID + "/releases"} {
		t.Run(path, func(t *testing.T) {
			var legacy struct {
				Data []map[string]any `json:"data"`
			}
			if err := json.Unmarshal(b.call("GET", path, nil, 200).Body.Bytes(), &legacy); err != nil || len(legacy.Data) != 100 {
				t.Fatalf("legacy array changed: count=%d err=%v", len(legacy.Data), err)
			}
			seen := map[string]bool{}
			for page := 1; page <= 13; page++ {
				data := dataOf(t, b.call("GET", fmt.Sprintf("%s?page=%d", path, page), nil, 200))
				items := data["items"].([]any)
				want := 10
				if page == 13 {
					want = 7
				}
				if data["total"] != float64(127) || len(items) != want {
					t.Fatalf("page %d: total=%v length=%d", page, data["total"], len(items))
				}
				for _, raw := range items {
					id := raw.(map[string]any)["id"].(string)
					if seen[id] {
						t.Fatalf("duplicate row at page boundary: %s", id)
					}
					seen[id] = true
				}
			}
			if len(seen) != 127 {
				t.Fatal("history was truncated")
			}
			last := dataOf(t, b.call("GET", path+"?page=2&pageSize=100", nil, 200))
			if len(last["items"].([]any)) != 27 {
				t.Fatal("page size was ignored")
			}
			for _, query := range []string{"page=0", "page=-1", "page=bad", "pageSize=0", "pageSize=101", "page=9223372036854775807&pageSize=100"} {
				b.call("GET", path+"?"+query, nil, 400)
			}
		})
	}
	if data := dataOf(t, b.call("GET", "/instances/22222222-2222-4222-8222-000000000001/releases?page=1", nil, 200)); data["total"] != float64(0) || len(data["items"].([]any)) != 0 {
		t.Fatal("release total included another instance")
	}
}

func TestJobHistoryCancelledFilterUsesExistingCancellationPhase(t *testing.T) {
	s, b := setup(t)
	b.login()
	for _, phase := range []string{"CANCELLED", "INSTALL_FAILED"} {
		if _, err := s.DB.Exec(`INSERT INTO jobs(id,actor_id,kind,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_INSTALL',?,?,'FAILED',?,'{}','history',?,?)`, phase, b.currentAdmin(), phase, hashToken(phase), phase, Now(), Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"CANCELLED", "FAILED"} {
		items := dataOf(t, b.call("GET", "/jobs?limit=10&status="+status+"&kind=SOFTWARE_INSTALL", nil, 200))["items"].([]any)
		if len(items) != 1 || (items[0].(map[string]any)["phase"] == "CANCELLED") != (status == "CANCELLED") {
			t.Fatalf("%s filter mixed cancellation and failure: %v", status, items)
		}
	}
}
