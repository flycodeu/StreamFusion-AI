package controller

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func javaRuntimeFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	var layer bytes.Buffer
	w := tar.NewWriter(&layer)
	if err := w.WriteHeader(&tar.Header{Name: "runtime", Typeflag: tar.TypeReg, Mode: 0444, Size: 4}); err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("java"))
	w.Close()
	layerHash := sha256.Sum256(layer.Bytes())
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"diff_ids": []string{"sha256:" + hex.EncodeToString(layerHash[:])}}, "config": map[string]any{"Env": []string{"PATH=/opt/java/openjdk/bin:/usr/bin:/bin"}}})
	configHash := sha256.Sum256(config)
	name := hex.EncodeToString(configHash[:]) + ".json"
	manifest, _ := json.Marshal([]map[string]any{{"Config": name, "RepoTags": nil, "Layers": []string{"runtime/layer.tar"}}})
	var archive bytes.Buffer
	w = tar.NewWriter(&archive)
	for name, data := range map[string][]byte{name: config, "manifest.json": manifest, "runtime/layer.tar": layer.Bytes()} {
		if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	w.Close()
	return archive.Bytes(), "sha256:" + hex.EncodeToString(configHash[:])
}

func executableJarFixture(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	entry, err := w.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	entry.Write([]byte("Manifest-Version: 1.0\r\nMain-Class: demo.Main\r\n\r\n"))
	entry, err = w.Create("demo/Main.class")
	if err != nil {
		t.Fatal(err)
	}
	entry.Write([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 65})
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func readyJavaRuntime(t *testing.T, s *Store, b *browser, tid, version string) string {
	t.Helper()
	p := dataOf(t, b.call("POST", "/targets/"+tid+"/plugin-install-plans", map[string]string{"pluginKey": "java-runtime", "version": version, "recipeRevision": "java-runtime-v1"}, 200))
	j := dataOf(t, b.call("POST", "/plugin-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": uuid.NewString()}, 202))
	jobID := j["jobId"].(string)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", jobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processPlugin(context.Background(), jobID, raw)
	iid := j["pluginInstallationId"].(string)
	i, err := s.readPlugin(iid)
	if err != nil || i.Status != "READY" {
		t.Fatal(i, err)
	}
	return iid
}

func putJarInput(t *testing.T, s *Store, b *browser, job string, data []byte) string {
	t.Helper()
	r := httptest.NewRequest("PUT", "http://127.0.0.1:19090"+api+"/jobs/"+job+"/input", bytes.NewReader(data))
	r.Header.Set("Origin", "http://127.0.0.1:19090")
	r.Header.Set("X-CSRF-Token", b.csrf)
	r.AddCookie(b.cookie)
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	b.call("POST", "/jobs/"+job+"/input/complete", nil, 202)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestJarImportFreezesEnvironmentAndProtectsReferences(t *testing.T) {
	s, b := setup(t)
	b.login()
	runtimeArchive, image := javaRuntimeFixture(t)
	tid := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		if req.Action == "JAVA_RUNTIME_EXPORT" {
			return runtimeArchive, true
		}
		return nil, false
	})
	iid := readyJavaRuntime(t, s, b, tid, "21-jre")
	if _, err := s.DB.Exec("UPDATE plugin_installations SET image_id=? WHERE id=?", image, iid); err != nil {
		t.Fatal(err)
	}
	jar := executableJarFixture(t)
	sha := sha256.Sum256(jar)
	in := map[string]any{"sourceKind": "JAR", "pluginInstallationId": iid, "name": "backend.jar", "archiveSha256": hex.EncodeToString(sha[:]), "byteSize": len(jar), "idempotencyKey": "jar-source-one"}
	job := dataOf(t, b.call("POST", "/artifact-imports", in, 202))["jobId"].(string)
	if again := dataOf(t, b.call("POST", "/artifact-imports", in, 200))["jobId"]; again != job {
		t.Fatal("duplicate imported again")
	}
	installation, err := s.readPlugin(iid)
	if err != nil || installation.ReferenceCount != 1 {
		t.Fatal(installation, err)
	}
	b.call("POST", "/plugin-installations/"+iid+"/remove", map[string]string{"idempotencyKey": "remove-pending-import"}, 409)
	raw := putJarInput(t, s, b, job, jar)
	b.h.processImport(job, raw)
	var status, result string
	if err := s.DB.QueryRow("SELECT status,result_json FROM jobs WHERE id=?", job).Scan(&status, &result); err != nil || status != "SUCCEEDED" {
		t.Fatal(status, result, err)
	}
	var packaged struct {
		ArtifactID         string                    `json:"artifactId"`
		RuntimeEnvironment deploy.RuntimeEnvironment `json:"runtimeEnvironment"`
	}
	if err := json.Unmarshal([]byte(result), &packaged); err != nil {
		t.Fatal(err)
	}
	if packaged.RuntimeEnvironment.InstallationID != iid || packaged.RuntimeEnvironment.ImageID != image || packaged.RuntimeEnvironment.PluginVersion != "21-jre" {
		t.Fatal(packaged)
	}
	var environment, archiveSHA, artifactImage string
	var size int64
	if err := s.DB.QueryRow("SELECT runtime_environment_json,archive_sha256,byte_size,image_id FROM artifacts WHERE id=?", packaged.ArtifactID).Scan(&environment, &archiveSHA, &size, &artifactImage); err != nil {
		t.Fatal(err)
	}
	if actual, err := deploy.ValidateArchive(s.artifactPath(packaged.ArtifactID), archiveSHA, size); err != nil || actual != artifactImage {
		t.Fatal(actual, err)
	}
	installation, err = s.readPlugin(iid)
	if err != nil || installation.ReferenceCount != 1 {
		t.Fatal("artifact did not hold environment reference", installation, err)
	}
	b.call("POST", "/plugin-installations/"+iid+"/remove", map[string]string{"idempotencyKey": "remove-frozen-artifact"}, 409)
	config := deploy.Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/actuator/health", HealthSeconds: 10, DataCompatibility: "v1"}
	instanceID := dataOf(t, b.call("POST", "/instances", map[string]any{"name": "jar-service", "targetId": tid, "config": config}, 200))["id"].(string)
	plan := dataOf(t, b.call("POST", "/instances/"+instanceID+"/plans", map[string]string{"kind": "PUBLISH", "artifactId": packaged.ArtifactID}, 200))
	candidate := plan["candidate"].(map[string]any)
	if candidate["runtimeEnvironment"].(map[string]any)["installationId"] != iid {
		t.Fatal("release lost frozen runtime", candidate)
	}
	if _, err := os.Stat(s.uploadPath(job)); !os.IsNotExist(err) {
		t.Fatal("completed input not removed", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(s.Dir, "packages", "*.partial"))
	if len(leftovers) != 0 {
		t.Fatal("runtime temporary transfer leaked", leftovers)
	}
}

func TestJarImportCancellationWrongEnvironmentAndPackagingFailure(t *testing.T) {
	s, b := setup(t)
	b.login()
	tid := pluginSSHFixture(t, s, b, true)
	iid := readyJavaRuntime(t, s, b, tid, "21-jre")
	java17 := readyJavaRuntime(t, s, b, tid, "17-jre")
	jar := executableJarFixture(t)
	sha := sha256.Sum256(jar)
	input := func(id, key string) map[string]any {
		return map[string]any{"sourceKind": "JAR", "pluginInstallationId": id, "name": "jar", "archiveSha256": hex.EncodeToString(sha[:]), "byteSize": len(jar), "idempotencyKey": key}
	}
	b.call("POST", "/artifact-imports", input(java17, "wrong-java17"), 409)
	override := input(iid, "wrong-complete-image")
	override["sourceKind"] = "DOCKER_ARCHIVE"
	b.call("POST", "/artifact-imports", override, 400)
	override = input(iid, "forged-environment")
	override["runtimeEnvironment"] = map[string]string{"pluginVersion": "21-jre"}
	b.call("POST", "/artifact-imports", override, 400)
	job := dataOf(t, b.call("POST", "/artifact-imports", input(iid, "cancel-jar-input"), 202))["jobId"].(string)
	b.call("POST", "/jobs/"+job+"/cancel", nil, 200)
	i, err := s.readPlugin(iid)
	if err != nil || i.ReferenceCount != 0 {
		t.Fatal(i, err)
	}
	job = dataOf(t, b.call("POST", "/artifact-imports", input(iid, "failed-runtime-export"), 202))["jobId"].(string)
	raw := putJarInput(t, s, b, job, jar)
	b.h.processImport(job, raw)
	var status, code string
	var count int
	s.DB.QueryRow("SELECT status,error_code FROM jobs WHERE id=?", job).Scan(&status, &code)
	s.DB.QueryRow("SELECT count(*) FROM artifacts").Scan(&count)
	if status != "FAILED" || code == "" || count != 0 {
		t.Fatal("failed export registered an artifact", status, code, count)
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "packages", "*.runtime.partial"))
	if len(files) != 0 {
		t.Fatal(files)
	}
}

func TestSchemaFiveUpgradeKeepsArtifactsUnknownJobAndLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	os.Mkdir(dir, 0700)
	db, err := sql.Open("sqlite", filepath.Join(dir, "controller.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for index, source := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5} {
		if _, err = db.Exec(source); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,?,?)", index+1, hashToken(source), Version, Now()); err != nil {
			t.Fatal(err)
		}
	}
	controllerID, adminID, targetID, jobID, artifactID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,'old',?)", []any{controllerID, Now()}},
		{"INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,'admin',?,?)", []any{adminID, []byte("unchanged"), Now()}},
		{"INSERT INTO targets(id,name,install_root,created_at) VALUES(?,'old','/opt/old-target',?)", []any{targetID, Now()}},
		{"INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?,'legacy',?,1,'sha256:old',?)", []any{artifactID, hashToken("old"), Now()}},
		{"INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'PUBLISH',?,'old-key','hash','UNKNOWN','HEALTH','{}','trace',?,?)", []any{jobID, adminID, targetID, Now(), Now()}},
		{"INSERT INTO operation_locks VALUES(?,?)", []any{"target:" + targetID, jobID}},
	} {
		if _, err = db.Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec("PRAGMA user_version=5")
	db.Close()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	var image, status, lock string
	var environment sql.NullString
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if err = s.DB.QueryRow("SELECT image_id,runtime_environment_json FROM artifacts WHERE id=?", artifactID).Scan(&image, &environment); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow("SELECT j.status,l.job_id FROM operation_locks l JOIN jobs j ON j.id=l.job_id").Scan(&status, &lock); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || s.controllerID() != controllerID || image != "sha256:old" || environment.Valid || status != "UNKNOWN" || lock != jobID {
		t.Fatal(version, image, environment, status, lock)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "before-schema-6-*.tar.gz"))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	var checksum string
	if err = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=6").Scan(&checksum); err != nil || checksum != hashToken(schemaV6) {
		t.Fatal(checksum, err)
	}
	if !strings.Contains(backups[0], "before-schema-6-") {
		t.Fatal(backups)
	}
}
