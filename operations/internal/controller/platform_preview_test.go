package controller

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func platformCall(t *testing.T, h *HTTP, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:19090"+api+path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("Cache-Control") != "no-store" || len(w.Header().Get("X-Trace-Id")) != 32 {
		t.Fatal("service response must be non-cacheable and traceable")
	}
	return w
}

func TestPlatformCatalogAuthenticationAndVersionFence(t *testing.T) {
	s, b := setup(t)
	const serviceToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	path := filepath.Join(t.TempDir(), "platform-token")
	if err := os.WriteFile(path, []byte(serviceToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	protectPlatformTokenPath(t, path)
	if got := platformCall(t, b.h, "GET", "/platform/preview-instances", serviceToken, nil); got.Code != 404 {
		t.Fatal("disabled service route exposed", got.Code)
	}
	if err := b.h.ConfigurePlatformTokenFile(path); err != nil {
		t.Fatal(err)
	}
	if got := platformCall(t, b.h, "GET", "/platform/preview-instances", "", nil); got.Code != 401 {
		t.Fatal("anonymous request accepted", got.Code)
	}
	b.login()
	if got := platformCall(t, b.h, "GET", "/platform/preview-instances", "", nil); got.Code != 401 {
		t.Fatal("Ops browser session accepted", got.Code)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:19090"+api+"/platform/preview-instances", nil)
	r.Header.Set("Authorization", "Bearer "+serviceToken)
	r.Header.Set("Origin", "http://127.0.0.1:19090")
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("browser origin accepted", w.Code)
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:19090"+api+"/platform/preview-instances", nil)
	r.Header.Set("Authorization", "Bearer "+serviceToken)
	r.Header.Set("Cookie", "sf_ops_session=browser")
	w = httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("browser cookie accepted", w.Code)
	}
	targetID := dataOf(t, b.call("POST", "/hosts", map[string]any{"kind": "LOCAL", "name": "host-a", "installRoot": "/opt/preview-a"}, 201))["id"].(string)
	config := deploy.Config{Environment: "preview", BindAddress: "127.0.0.1", HostPort: 9443, ContainerPort: 9443, MemoryMiB: 1024, HealthPath: "/preview-control/v1/snapshot", HealthSeconds: 10, DataCompatibility: "preview-ownership-v1", Preview: &deploy.PreviewConfig{NodeID: "preview-a", ControlHost: "127.0.0.1", MediaAddress: "127.0.0.1", AllowedTargets: []string{"127.0.0.1:554"}, CPUs: 2, MaxSources: 64, MaxViewers: 64, MaxTranscodes: 64, MaxStarting: 2, IngressKbps: 100000, EgressKbps: 100000, SourceUpperKbps: 10000, OutputUpperKbps: 10000}}
	instanceID := dataOf(t, b.call("POST", "/instances", map[string]any{"name": "ZLM A", "targetId": targetID, "config": config}, 200))["id"].(string)
	artifactID, releaseID := uuid.NewString(), uuid.NewString()
	release := deploy.Release{ID: releaseID, InstanceID: instanceID, ArtifactID: artifactID, ImageID: "sha256:abc", Config: config}
	if _, err := s.DB.Exec("INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?,?,?,?,?,?)", artifactID, "preview", hashToken("archive"), 1, release.ImageID, Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO releases VALUES(?,?,?,?,?)", releaseID, instanceID, artifactID, jsonText(release), Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE instances SET current_release_id=? WHERE id=?", releaseID, instanceID); err != nil {
		t.Fatal(err)
	}
	javaConfig := deploy.Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/actuator/health", HealthSeconds: 10, DataCompatibility: "v1"}
	javaInstanceID := dataOf(t, b.call("POST", "/instances", map[string]any{"name": "Java service", "targetId": targetID, "config": javaConfig}, 200))["id"].(string)
	javaArtifactID, javaReleaseID := uuid.NewString(), uuid.NewString()
	javaRelease := deploy.Release{ID: javaReleaseID, InstanceID: javaInstanceID, ArtifactID: javaArtifactID, ImageID: "sha256:def", Config: javaConfig}
	if _, err := s.DB.Exec("INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?,?,?,?,?,?)", javaArtifactID, "java", hashToken("java"), 1, javaRelease.ImageID, Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO releases VALUES(?,?,?,?,?)", javaReleaseID, javaInstanceID, javaArtifactID, jsonText(javaRelease), Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE instances SET current_release_id=? WHERE id=?", javaReleaseID, javaInstanceID); err != nil {
		t.Fatal(err)
	}
	got := platformCall(t, b.h, "GET", "/platform/preview-instances", serviceToken, nil)
	if got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	var envelope struct {
		Data struct {
			Items []platformPreviewInstance `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].ControllerID != s.controllerID() || envelope.Data.Items[0].TargetID != targetID || envelope.Data.Items[0].InstanceID != instanceID || envelope.Data.Items[0].ReleaseID != releaseID || envelope.Data.Items[0].MaxTranscodes != 64 {
		t.Fatalf("wrong catalog: %+v", envelope.Data.Items)
	}
	if strings.Contains(got.Body.String(), "machineToken") || strings.Contains(got.Body.String(), "certificateSha256") {
		t.Fatal("catalog exposed handoff secret or certificate material")
	}
	if got = platformCall(t, b.h, "POST", "/platform/preview-instances/"+instanceID+"/handoff", serviceToken, map[string]string{"releaseId": uuid.NewString()}); got.Code != 409 || !strings.Contains(got.Body.String(), "RELEASE_CHANGED") {
		t.Fatal("stale release was not fenced", got.Code, got.Body.String())
	}
	if got = platformCall(t, b.h, "POST", "/platform/preview-instances/"+instanceID+"/handoff", serviceToken, map[string]string{"releaseId": releaseID, "unexpected": "value"}); got.Code != 400 {
		t.Fatal("extra request field accepted", got.Code)
	}
	for _, state := range []struct {
		status string
		ack    int
	}{{"QUEUED", 0}, {"RUNNING", 0}, {"UNKNOWN", 0}, {"SUCCEEDED", 1}, {"FAILED", 1}} {
		jobID := uuid.NewString()
		if _, err := s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,instance_id,idempotency_key,request_hash,status,phase,input_json,target_ack_pending,trace_id,created_at,updated_at) SELECT ?,id,'PUBLISH',?,?,?,? ,?,'TEST','{}',?,'test',?,? FROM admins LIMIT 1", jobID, targetID, instanceID, jobID, hashToken(jobID), state.status, state.ack, Now(), Now()); err != nil {
			t.Fatal(err)
		}
		got = platformCall(t, b.h, "POST", "/platform/preview-instances/"+instanceID+"/handoff", serviceToken, map[string]string{"releaseId": releaseID})
		if got.Code != 409 || !strings.Contains(got.Body.String(), "HANDOFF_JOB_PENDING") {
			t.Fatal("pending job not fenced", state, got.Code, got.Body.String())
		}
		if _, err := s.DB.Exec("DELETE FROM jobs WHERE id=?", jobID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlatformTokenFileValidation(t *testing.T) {
	_, b := setup(t)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	protectPlatformTokenPath(t, path)
	if err := b.h.ConfigurePlatformTokenFile(path); err == nil {
		t.Fatal("weak token accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.h.ConfigurePlatformTokenFile(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if err := b.h.ConfigurePlatformTokenFile(path); err == nil {
			t.Fatal("public service token file accepted")
		}
		openDirectory := filepath.Join(t.TempDir(), "public")
		if err := os.Mkdir(openDirectory, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(openDirectory, 0755); err != nil {
			t.Fatal(err)
		}
		openPath := filepath.Join(openDirectory, "token")
		if err := os.WriteFile(openPath, []byte(strings.Repeat("b", 64)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := b.h.ConfigurePlatformTokenFile(openPath); err == nil {
			t.Fatal("public parent directory accepted")
		}
	}
	link := filepath.Join(t.TempDir(), "linked-token")
	if err := os.Symlink(path, link); err == nil {
		if err := b.h.ConfigurePlatformTokenFile(link); err == nil {
			t.Fatal("service token symlink accepted")
		}
	}
	if got := platformCall(t, b.h, "GET", "/platform/preview-instances", strings.Repeat("a", 64), nil); got.Code != 200 {
		t.Fatal(got.Code)
	}
}
