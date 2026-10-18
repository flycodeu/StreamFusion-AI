package controller

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/plugin"
	"golang.org/x/crypto/ssh"
)

func TestSchemaTwoUpgradePreservesPendingLocks(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	controllerID, adminID, jobID, targetID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, query := range []string{schema, schemaV2, "PRAGMA user_version=2"} {
		if _, e = db.Exec(query); e != nil {
			t.Fatal(e)
		}
	}
	queries := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO schema_migrations VALUES(1,?,'old',?),(2,?,'old',?)", []any{hashToken(schema), Now(), hashToken(schemaV2), Now()}},
		{"INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'existing',?)", []any{controllerID, Now()}},
		{"INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,'admin',?,?)", []any{adminID, []byte("unchanged"), Now()}},
		{"INSERT INTO targets(id,name,install_root,created_at) VALUES(?,'existing','/opt/existing-target',?)", []any{targetID, Now()}},
		{"INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'PUBLISH',?,'existing-key','hash','UNKNOWN','HEALTH','{}','old-trace',?,?)", []any{jobID, adminID, targetID, Now(), Now()}},
		{"INSERT INTO operation_locks VALUES(?,?)", []any{"target:" + targetID, jobID}},
	}
	for _, q := range queries {
		if _, e = db.Exec(q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	var lock, trace string
	if e = s.DB.QueryRow("SELECT l.job_id,j.trace_id FROM operation_locks l JOIN jobs j ON j.id=l.job_id").Scan(&lock, &trace); e != nil || lock != jobID || trace != "old-trace" {
		t.Fatal(lock, trace, e)
	}
	rows, e := s.DB.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	if rows.Next() {
		t.Fatal("invalid foreign keys after migration")
	}
	rows.Close()
	backups, e := filepath.Glob(filepath.Join(dir, "before-schema-3-*.tar.gz"))
	if e != nil || len(backups) != 1 {
		t.Fatal(backups, e)
	}
	key := filepath.Join(t.TempDir(), "key")
	if e = os.WriteFile(key, s.Key, 0600); e != nil {
		t.Fatal(e)
	}
	s.Close()
	dest := filepath.Join(t.TempDir(), "restored")
	if e = Restore(backups[0], key, dest); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dest)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.controllerID() != controllerID {
		t.Fatal("restored identity changed")
	}
	if e = s.DB.QueryRow("SELECT job_id FROM operation_locks").Scan(&lock); e != nil || lock != jobID {
		t.Fatal("restored lock lost", e)
	}
}

// A real SSH transport with a controlled executor reply exercises the HTTP,
// store, scheduler and ACK paths. It does not claim real Docker acceptance.
func pluginSSHFixture(t *testing.T, s *Store, b *browser, supportsPlugins bool, extensions ...func(deploy.Request) ([]byte, bool)) string {
	t.Helper()
	_, clientKey, _ := ed25519.GenerateKey(rand.Reader)
	pk, e := x509.MarshalPKCS8PrivateKey(clientKey)
	if e != nil {
		t.Fatal(e)
	}
	cred := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "plugin test SSH", "kind": "SSH_KEY", "secret": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))}, 201))
	clientSigner, _ := ssh.NewSignerFromKey(clientKey)
	_, hostKey, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostKey)
	conf := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(clientSigner.PublicKey().Marshal()) {
			return nil, fmt.Errorf("key rejected")
		}
		return nil, nil
	}}
	conf.AddHostKey(hostSigner)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	results := map[string]deploy.Result{}
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				_, channels, requests, e := ssh.NewServerConn(conn, conf)
				if e != nil {
					return
				}
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					ch, reqs, e := channel.Accept()
					if e != nil {
						return
					}
					for q := range reqs {
						var cmd struct{ Command string }
						ssh.Unmarshal(q.Payload, &cmd)
						if q.Type != "exec" || cmd.Command != "/usr/bin/sudo -n '/opt/plugin-test/executor' target-exec" {
							q.Reply(false, nil)
							continue
						}
						q.Reply(true, nil)
						var req deploy.Request
						if json.NewDecoder(bufio.NewReader(ch)).Decode(&req) != nil {
							ch.Close()
							continue
						}
						handled := false
						for _, extension := range extensions {
							if raw, ok := extension(req); ok {
								ch.Write(raw)
								handled = true
								break
							}
						}
						if handled {
							ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							ch.Close()
							continue
						}
						mu.Lock()
						var out any
						switch req.Action {
						case "PROBE":
							catalog := ""
							if supportsPlugins {
								catalog = deploy.Digest(plugin.All())
							}
							jarRecipe := ""
							if supportsPlugins {
								jarRecipe = "java-jar-v1"
							}
							out = map[string]string{"dockerVersion": "27.5.1", "composeVersion": "2.32.4", "platform": "linux/amd64", "pendingJobId": "", "pluginCatalogHash": catalog, "javaJarRecipe": jarRecipe}
						case "PLUGIN_INSTALL", "PLUGIN_REMOVE":
							p := req.Plugin
							res := deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "SUCCEEDED", Phase: "RECORDED", ImageID: "sha256:" + strings.Repeat("a", 64), PluginKey: p.Key, PluginVersion: p.Version, PluginKind: p.Kind, PluginImageDigest: p.Digest, Platform: p.Platform, Health: "PLUGIN_READY", ObservedVersion: "tested version", ObservedAt: Now()}
							if req.Action == "PLUGIN_REMOVE" {
								res.Health, res.Phase = "PLUGIN_REMOVED", "REMOVED"
							}
							results[req.JobID] = res
							out = res
						case "STATUS":
							out = results[req.JobID]
						case "ACK":
							res := results[req.JobID]
							if deploy.Digest(res) == req.ResultHash {
								res.Ack = true
							}
							out = res
						case "PLUGIN_VERIFY":
							out = deploy.Result{Status: "SUCCEEDED", Health: "PLUGIN_READY", PluginImageDigest: req.Plugin.Digest, ImageID: "sha256:" + strings.Repeat("a", 64), Platform: req.Plugin.Platform, ObservedVersion: "tested version"}
						default:
							out = map[string]string{}
						}
						mu.Unlock()
						json.NewEncoder(ch).Encode(out)
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						ch.Close()
					}
				}
			}()
		}
	}()
	id := dataOf(t, b.call("POST", "/targets", map[string]any{"name": "plugin Linux", "installRoot": "/opt/plugin-test", "kind": "SSH", "address": "127.0.0.1", "sshPort": listener.Addr().(*net.TCPAddr).Port, "sshUser": "test", "hostFingerprint": ssh.FingerprintSHA256(hostSigner.PublicKey()), "credentialId": cred["id"], "credentialRevision": "1"}, 201))["id"].(string)
	return id
}

func TestPluginCenterPlansJobsAndReferenceProtection(t *testing.T) {
	s, b := setup(t)
	b.login()
	tid := pluginSSHFixture(t, s, b, true)
	b.call("GET", "/plugins", nil, 200)
	b.call("GET", "/plugins/java-runtime/versions", nil, 200)
	b.call("GET", "/plugins/unknown/versions", nil, 404)
	b.call("POST", "/targets/"+tid+"/plugin-install-plans", map[string]string{"pluginKey": "java-runtime", "version": "21-jre", "recipeRevision": "java-runtime-v1", "image": "bad"}, 400)
	create := func(key, version, revision string) string {
		return dataOf(t, b.call("POST", "/targets/"+tid+"/plugin-install-plans", map[string]string{"pluginKey": key, "version": version, "recipeRevision": revision}, 200))["id"].(string)
	}
	var installationID, jobID string
	for n, version := range []string{"21-jre", "17-jre"} {
		pid := create("java-runtime", version, "java-runtime-v1")
		key := fmt.Sprintf("install-key-%d", n)
		accepted := dataOf(t, b.call("POST", "/plugin-install-plans/"+pid+"/submit", map[string]string{"idempotencyKey": key}, 202))
		jobID = accepted["jobId"].(string)
		installationID = accepted["pluginInstallationId"].(string)
		again := dataOf(t, b.call("POST", "/plugin-install-plans/"+pid+"/submit", map[string]string{"idempotencyKey": key}, 200))
		if again["jobId"] != jobID {
			t.Fatal("duplicate submit created another job")
		}
		b.call("POST", "/plugin-install-plans/"+pid+"/submit", map[string]string{"idempotencyKey": "different-key"}, 409)
		var raw string
		if e := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", jobID).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		b.h.processPlugin(context.Background(), jobID, raw)
		i, e := s.readPlugin(installationID)
		if e != nil || i.Status != "READY" || i.ImageID == "" {
			t.Fatal(i, e)
		}
		var locks, pending int
		s.DB.QueryRow("SELECT count(*) FROM operation_locks").Scan(&locks)
		s.DB.QueryRow("SELECT target_ack_pending FROM jobs WHERE id=?", jobID).Scan(&pending)
		if locks != 0 || pending != 0 {
			t.Fatal("ACK did not release lock", locks, pending)
		}
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM plugin_installations WHERE status='READY'").Scan(&count)
	if count != 2 {
		t.Fatal("Java versions overwritten")
	}
	b.call("POST", "/plugin-installations/"+installationID+"/verify", nil, 200)
	if _, e := s.DB.Exec("UPDATE plugin_installations SET reference_count=1 WHERE id=?", installationID); e != nil {
		t.Fatal(e)
	}
	b.call("POST", "/plugin-installations/"+installationID+"/remove", map[string]string{"idempotencyKey": "remove-key"}, 409)
	s.DB.Exec("UPDATE plugin_installations SET reference_count=0 WHERE id=?", installationID)
	accepted := dataOf(t, b.call("POST", "/plugin-installations/"+installationID+"/remove", map[string]string{"idempotencyKey": "remove-key"}, 202))
	jobID = accepted["jobId"].(string)
	var raw string
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", jobID).Scan(&raw)
	b.h.processPlugin(context.Background(), jobID, raw)
	b.call("GET", "/plugin-installations/"+installationID, nil, 404)
	b.call("POST", "/plugin-installations/"+installationID+"/remove", map[string]string{"idempotencyKey": "remove-key"}, 200)
	b.call("GET", "/plugin-installations?targetId="+tid, nil, 200)
	pid := create("maven-jdk-build", "3.9.11-jdk-21", "maven-jdk-build-v1")
	s.DB.Exec("UPDATE plugin_install_plans SET expires_at=?,snapshot_json=json_set(snapshot_json,'$.expiresAt',?) WHERE id=?", "2000-01-01T00:00:00Z", "2000-01-01T00:00:00Z", pid)
	b.call("POST", "/plugin-install-plans/"+pid+"/submit", map[string]string{"idempotencyKey": "expired-key"}, 409)
	pid = create("maven-jdk-build", "3.9.11-jdk-21", "maven-jdk-build-v1")
	s.DB.Exec("UPDATE targets SET name='changed' WHERE id=?", tid)
	b.call("POST", "/plugin-install-plans/"+pid+"/submit", map[string]string{"idempotencyKey": "changed-key"}, 409)
}

func TestPluginDockerNotReadyAndHashValidation(t *testing.T) {
	s, b := setup(t)
	b.login()
	tid := dataOf(t, b.call("POST", "/targets", map[string]any{"name": "uninstalled", "installRoot": "/opt/not-installed"}, 201))["id"].(string)
	w := b.call("POST", "/targets/"+tid+"/plugin-install-plans", map[string]string{"pluginKey": "java-runtime", "version": "21-jre", "recipeRevision": "java-runtime-v1"}, 409)
	var response Envelope
	json.Unmarshal(w.Body.Bytes(), &response)
	if response.Code != "DOCKER_NOT_READY" {
		t.Fatal(response.Code)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 0 {
		t.Fatal("job created without Docker")
	}
}

func TestPluginCancellationAndRestartReconciliation(t *testing.T) {
	s, b := setup(t)
	b.login()
	tid := pluginSSHFixture(t, s, b, true)
	accept := func(key string) (string, string) {
		p := dataOf(t, b.call("POST", "/targets/"+tid+"/plugin-install-plans", map[string]string{"pluginKey": "java-runtime", "version": "21-jre", "recipeRevision": "java-runtime-v1"}, 200))
		j := dataOf(t, b.call("POST", "/plugin-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": key}, 202))
		return j["jobId"].(string), j["pluginInstallationId"].(string)
	}
	jobID, iid := accept("cancel-key")
	b.call("POST", "/jobs/"+jobID+"/cancel", nil, 200)
	var raw, status string
	s.DB.QueryRow("SELECT input_json,status FROM jobs WHERE id=?", jobID).Scan(&raw, &status)
	if status != "FAILED" {
		t.Fatal("queued work not cancelled")
	}
	b.h.processPlugin(context.Background(), jobID, raw)
	i, e := s.readPlugin(iid)
	if e != nil || i.Status != "FAILED" {
		t.Fatal("cancelled job was executed", i, e)
	}
	jobID, iid = accept("restart-key")
	s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", jobID).Scan(&raw)
	var req deploy.Request
	if e = deploy.StrictJSON([]byte(raw), &req); e != nil {
		t.Fatal(e)
	}
	tgt, e := s.readTarget(tid)
	if e != nil {
		t.Fatal(e)
	}
	var actual deploy.Result
	// The target executed but the reply was not committed before controller exit.
	if e = s.callTarget(context.Background(), tgt, req, "", &actual); e != nil {
		t.Fatal(e)
	}
	bad := actual
	bad.PluginImageDigest = "sha256:" + strings.Repeat("b", 64)
	if e = b.h.commitPluginResult(jobID, req, bad); e == nil {
		t.Fatal("mismatched digest committed")
	}
	s.DB.Exec("UPDATE jobs SET status='RUNNING' WHERE id=?", jobID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.h.RunOperations(ctx)
	i, e = s.readPlugin(iid)
	if e != nil || i.Status != "UNKNOWN" {
		t.Fatal("restart did not preserve unknown", i, e)
	}
	b.call("POST", "/jobs/"+jobID+"/cancel", nil, 409)
	b.call("POST", "/plugin-installations/"+iid+"/verify", nil, 409)
	b.call("POST", "/jobs/"+jobID+"/reconcile", nil, 200)
	i, e = s.readPlugin(iid)
	if e != nil || i.Status != "READY" {
		t.Fatal("unknown result not reconciled", i, e)
	}
	var locks int
	s.DB.QueryRow("SELECT count(*) FROM operation_locks").Scan(&locks)
	if locks != 0 {
		t.Fatal("reconciliation left target locked")
	}
}

func TestPluginOldExecutorBlockedBeforeSubmission(t *testing.T) {
	s, b := setup(t)
	b.login()
	tid := pluginSSHFixture(t, s, b, false)
	w := b.call("POST", "/targets/"+tid+"/plugin-install-plans", map[string]string{"pluginKey": "java-runtime", "version": "21-jre", "recipeRevision": "java-runtime-v1"}, 409)
	var response Envelope
	json.Unmarshal(w.Body.Bytes(), &response)
	if response.Code != "PLUGIN_EXECUTOR_UPGRADE_REQUIRED" {
		t.Fatal(response.Code)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 0 {
		t.Fatal("old executor accepted plugin side effects")
	}
}
