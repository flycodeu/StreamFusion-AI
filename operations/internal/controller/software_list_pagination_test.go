package controller

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestSoftwareListsPagePastLegacyLimitsWithoutChangingArrayResponses(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "pagination", "kind": "LOCAL"}, 201))["id"].(string)
	environment := uuid.NewString()
	created := "2026-11-10T00:00:00Z"
	actor := b.currentAdmin()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range 1005 {
		id := fmt.Sprintf("10000000-0000-4000-8000-%012d", index)
		if _, err = tx.Exec(`INSERT INTO software_instances(id,name,host_id,software_type,runtime_kind,locator,created_at,updated_at) VALUES(?,? ,?,'JAVA','TOOL',?,?,?)`, id, fmt.Sprintf("Java %d", index), host, fmt.Sprintf("/opt/java/%d/bin/java", index), created, created); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 205 {
		if _, err = tx.Exec(`INSERT INTO plugin_installations(id,target_id,plugin_key,plugin_version,kind,install_mode,image_ref,image_digest,image_id,recipe_revision,platform,status,last_job_id,created_at,updated_at) VALUES(?,?,'java-runtime',?,'JAVA_RUNTIME','DOCKER_IMAGE','java:fixture',?,'image','fixture','linux/amd64','READY','',?,?)`, uuid.NewString(), host, fmt.Sprint(index), fmt.Sprint(index), created, created); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO node_environments(id,host_id,name,account,account_uid,root_path,manager_type,management_mode,created_at,updated_at) VALUES(?,?,'node','ubuntu',1000,'/opt/node','SYSTEM','OBSERVE',?,?)`, environment, host, created, created); err != nil {
		t.Fatal(err)
	}
	for index := range 125 {
		id := fmt.Sprintf("20000000-0000-4000-8000-%012d", index)
		input := fmt.Sprintf(`{"installation":{"node":{"environmentId":%q}},"secrets":{"token":"never-return-input"}}`, environment)
		if _, err = tx.Exec(`INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_INSTALL',?,?,?,'SUCCEEDED','TEST',?,'page-test',?,?)`, id, actor, host, id, hashToken(id), input, created, created); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []struct {
		path          string
		total, legacy int
	}{
		{"/software-instances?hostId=" + host, 1005, 1000},
		{"/plugin-installations?targetId=" + host, 205, 200},
		{"/node-environments/" + environment + "/jobs?", 125, 100},
	} {
		var legacy struct{ Data []any }
		if err = json.Unmarshal(b.call("GET", endpoint.path, nil, 200).Body.Bytes(), &legacy); err != nil || len(legacy.Data) != endpoint.legacy {
			t.Fatalf("legacy response %s: length=%d error=%v", endpoint.path, len(legacy.Data), err)
		}
		seen := map[string]bool{}
		for page := 1; len(seen) < endpoint.total; page++ {
			data := dataOf(t, b.call("GET", fmt.Sprintf("%s&page=%d&pageSize=100", endpoint.path, page), nil, 200))
			if int(data["total"].(float64)) != endpoint.total {
				t.Fatalf("incorrect total for %s", endpoint.path)
			}
			items := data["items"].([]any)
			if len(items) == 0 {
				t.Fatal("records beyond the former limit were omitted")
			}
			for _, item := range items {
				id := item.(map[string]any)["id"].(string)
				if seen[id] {
					t.Fatalf("equal timestamps duplicated %s", id)
				}
				seen[id] = true
			}
		}
		b.call("GET", endpoint.path+"&pageSize=101", nil, 400)
		if items := dataOf(t, b.call("GET", endpoint.path+"&page=99999&pageSize=10", nil, 200))["items"].([]any); len(items) != 0 {
			t.Fatal("out-of-range page returned unrelated records")
		}
	}
	if total := dataOf(t, b.call("GET", "/software-instances?hostId="+uuid.NewString()+"&page=1", nil, 200))["total"].(float64); total != 0 {
		t.Fatal("host filter was applied after pagination")
	}
}
