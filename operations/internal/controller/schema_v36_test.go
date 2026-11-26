package controller

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSchema36PreservesBindingsAndReleaseHistory(t *testing.T) {
	s, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]string{"name": "Schema fixture", "kind": "LOCAL"}, 201))["id"].(string)
	draft := projectDraft28("deployment-schema-fixture")
	draft.Environments[0].Deployments[0].HostID = host
	p := applicationResponse(t, b.call("POST", "/application-projects", draft, 200))
	id := p.Environments[0].Deployments[0].ID
	var before string
	if err := s.DB.QueryRow("SELECT config_json FROM application_deployments WHERE id=?", id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Restore the original v25/v35 constraint while leaving all referencing tables
	// intact. The test then upgrades through Open, using that preexisting state.
	oldDDLStart := strings.Index(schemaV25, "CREATE TABLE application_deployments(")
	oldDDL := strings.SplitN(schemaV25[oldDDLStart:], "\n", 2)[0]
	first := strings.Index(schemaV36, "CREATE TABLE application_deployments_v36(")
	last := strings.Index(schemaV36, "INSERT INTO application_deployments_v36 ")
	if first == -1 || last == -1 {
		t.Fatal("cannot stage old table definition")
	}
	rewind := schemaV36[:first] + strings.Replace(oldDDL, "application_deployments(", "application_deployments_v36(", 1) + "\n" + schemaV36[last:]
	rewind = strings.Replace(rewind, "CREATE UNIQUE INDEX application_deployments_module_host ON application_deployments(environment_id,module_id,coalesce(host_id,''));", "", 1)
	rewind = strings.Replace(rewind, "PRAGMA user_version=36;", "DELETE FROM schema_migrations WHERE version=36; PRAGMA user_version=35; PRAGMA foreign_keys=ON;", 1)
	if _, err := s.DB.Exec("PRAGMA foreign_keys=OFF;" + rewind); err != nil {
		t.Fatal(err)
	}

	current, previous := uuid.NewString(), uuid.NewString()
	qaRequest, planToken := uuid.NewString(), uuid.NewString()
	for _, release := range []string{previous, current} {
		if _, err := s.DB.Exec(`INSERT INTO project_deployment_jobs
			(id,deployment_id,target_id,actor_id,action,status,phase,host_name,subject_name,build_number,
			commit_id,release_id,baseline_id,snapshot_json,trace_id,created_at,updated_at)
			SELECT ?,?,?,id,'DEPLOY','SUCCEEDED','VERIFIED','host','service',1,'commit',?,'','{}','trace',?,? FROM admins LIMIT 1`,
			release, id, host, release, Now(), Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO project_deployment_running VALUES(?,?,?)", []any{id, current, previous}},
		{"INSERT INTO project_deployment_plans SELECT ?,id,?,'{}',?,? FROM admins LIMIT 1", []any{planToken, id, Now(), current}},
		{"INSERT INTO application_qa_requests(deployment_id,request_id,monitor_version,queue_id) VALUES(?,?,1,42)", []any{id, qaRequest}},
	} {
		if _, err := s.DB.Exec(step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`INSERT INTO application_deployments
		SELECT ?,project_id,environment_id,module_id,NULL,NULL,config_json,5 FROM application_deployments WHERE id=?`, uuid.NewString(), id); err == nil {
		t.Fatal("historical schema allowed a second module binding")
	}

	dir := s.Dir
	s.Close()
	next, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	var after, gotCurrent, gotPrevious, gotPlan string
	if err = next.DB.QueryRow(`SELECT d.config_json,r.current_id,r.previous_id,p.job_id
		FROM project_deployment_running r JOIN application_deployments d ON d.id=r.deployment_id
		JOIN project_deployment_plans p ON p.deployment_id=d.id WHERE d.id=?`,
		id).Scan(&after, &gotCurrent, &gotPrevious, &gotPlan); err != nil || before != after ||
		gotCurrent != current || gotPrevious != previous || gotPlan != current {
		t.Fatal("deployment snapshot, release pointers or confirmation was changed", err)
	}
	var queueID, foreignKeys, invalid, version int
	if err := next.DB.QueryRow("SELECT queue_id FROM application_qa_requests WHERE request_id=?", qaRequest).Scan(&queueID); err != nil {
		t.Fatal(err)
	}
	for query, target := range map[string]*int{
		"PRAGMA foreign_keys":                           &foreignKeys,
		"SELECT count(*) FROM pragma_foreign_key_check": &invalid,
		"PRAGMA user_version":                           &version,
	} {
		if err := next.DB.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if queueID != 42 || foreignKeys != 1 || invalid != 0 || version != 36 {
		t.Fatalf("migration integrity mismatch: QA=%d FK=%d invalid=%d schema=%d", queueID, foreignKeys, invalid, version)
	}
	for _, statement := range []string{
		"DELETE FROM application_deployments WHERE id=?",
		"UPDATE application_deployments SET host_id=NULL WHERE id=?",
		"UPDATE targets SET address='192.0.2.12' WHERE id=(SELECT host_id FROM application_deployments WHERE id=?)",
	} {
		if _, err := next.DB.Exec(statement, id); err == nil || !strings.Contains(err.Error(), "PROJECT_DEPLOYMENT_IN_USE") {
			t.Fatal("existing release was not protected", err)
		}
	}
	if backup, err := filepath.Glob(filepath.Join(dir, "before-schema-36-*.tar.gz")); err != nil || len(backup) != 1 {
		t.Fatal("migration backup missing", backup, err)
	}
}
