package controller

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestJobHistoryPagesAllRowsWithEqualTimestampsAndFilters(t *testing.T) {
	s, b := setup(t)
	b.call("GET", "/jobs", nil, 401)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "开发主机", "kind": "LOCAL"}, 201))["id"].(string)
	created := "2026-11-09T12:00:00Z"
	for index := range 127 {
		kind, state := "SOFTWARE_INSTALL", "SUCCEEDED"
		if index%2 == 0 {
			kind, state = "SOFTWARE_START", "UNKNOWN"
		}
		input := fmt.Sprintf(`{"installation":{"softwareId":"software-%03d","config":{"name":"测试软件 %03d"}},"secrets":{"password":"never-return-this-password"}}`, index, index)
		id := fmt.Sprintf("11111111-1111-4111-8111-%012d", index)
		if _, err := s.DB.Exec(`INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,?,?,?,? ,?,'TEST',?,'history',?,?)`,
			id, b.currentAdmin(), kind, host, id, hashToken(id), state, input, created, created); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for page := range 7 {
		reply := b.call("GET", "/jobs?limit=20&before="+url.QueryEscape(cursor), nil, 200)
		if strings.Contains(reply.Body.String(), "never-return-this-password") || strings.Contains(reply.Body.String(), "input_json") {
			t.Fatal("private request data leaked")
		}
		data := dataOf(t, reply)
		items := data["items"].([]any)
		want := 20
		if page == 6 {
			want = 7
		}
		if len(items) != want {
			t.Fatalf("page %d length=%d want=%d", page, len(items), want)
		}
		for _, raw := range items {
			job := raw.(map[string]any)
			id := job["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate across cursor boundary: %s", id)
			}
			seen[id] = true
			if job["subjectName"] == "" || job["softwareInstanceId"] == "" {
				t.Fatal("missing safe subject projection")
			}
		}
		cursor = data["nextCursor"].(string)
		if (page < 6) != (cursor != "") {
			t.Fatalf("wrong continuation on page %d: %q", page, cursor)
		}
	}
	if len(seen) != 127 {
		t.Fatal("history beyond the old latest-100 cap was lost")
	}
	filtered := dataOf(t, b.call("GET", "/jobs?limit=100&kind=SOFTWARE_START&status=UNKNOWN&hostId="+host+"&query="+url.QueryEscape("开发主机"), nil, 200))["items"].([]any)
	if len(filtered) != 64 {
		t.Fatalf("filters applied after pagination: %d", len(filtered))
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=x", "before=bad", "before=" + url.QueryEscape(created+"|"), "status=OTHER", "kind=bad-kind", "query=" + url.QueryEscape("bad\nquery")} {
		b.call("GET", "/jobs?"+query, nil, 400)
	}
	if items := dataOf(t, b.call("GET", "/jobs?query="+url.QueryEscape("%"), nil, 200))["items"].([]any); len(items) != 0 {
		t.Fatal("search wildcards were interpreted as matching every job")
	}
	detail := b.call("GET", "/jobs/11111111-1111-4111-8111-000000000126", nil, 200)
	if strings.Contains(detail.Body.String(), "never-return-this-password") || dataOf(t, detail)["subjectName"] != "测试软件 126" {
		t.Fatal("single job did not use the same safe projection")
	}
}

func TestJobHistoryProjectsCustomOperationsAndSurvivesDamagedInput(t *testing.T) {
	s, b := setup(t)
	b.login()
	for _, row := range []struct{ id, input string }{
		{"custom-history", `{"software":{"id":"custom-software"},"management":{"definition":{"name":"自定义构建服务"},"operationKey":"reindex"},"secrets":{"token":"private-operation-token"}}`},
		{"damaged-history", `{not-json`},
	} {
		if _, err := s.DB.Exec(`INSERT INTO jobs(id,actor_id,kind,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_START',?,?,'SUCCEEDED','TEST',?,'history',?,?)`,
			row.id, b.currentAdmin(), row.id, hashToken(row.id), row.input, Now(), Now()); err != nil {
			t.Fatal(err)
		}
	}
	response := b.call("GET", "/jobs?query="+url.QueryEscape("自定义构建"), nil, 200)
	data := dataOf(t, response)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatal("custom operation name not searchable")
	}
	item := items[0].(map[string]any)
	if item["softwareInstanceId"] != "custom-software" || item["operationKey"] != "reindex" || strings.Contains(response.Body.String(), "private-operation-token") {
		t.Fatal("unsafe custom projection")
	}
	if len(dataOf(t, b.call("GET", "/jobs", nil, 200))["items"].([]any)) != 2 {
		t.Fatal("a damaged request hid unrelated history")
	}
}
