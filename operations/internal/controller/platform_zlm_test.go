package controller

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestPlatformZLMCatalogAndReleaseFence(t *testing.T) {
	s, b := setup(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	path := filepath.Join(t.TempDir(), "platform-token")
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	protectPlatformTokenPath(t, path)
	if got := platformCall(t, b.h, "GET", "/platform/zlm-instances", token, nil); got.Code != http.StatusNotFound {
		t.Fatal("disabled service route exposed", got.Code)
	}
	if err := b.h.ConfigurePlatformTokenFile(path); err != nil {
		t.Fatal(err)
	}
	if got := platformCall(t, b.h, "GET", "/platform/zlm-instances", "", nil); got.Code != http.StatusUnauthorized {
		t.Fatal("anonymous request accepted", got.Code)
	}
	b.login()
	targetID := dataOf(t, b.call("POST", "/targets", map[string]any{"name": "ZLM host", "installRoot": "/opt/zlm-node-a"}, 201))["id"].(string)
	config := deploy.Config{
		Environment: "zlm", BindAddress: "10.75.216.216", HostPort: 18080,
		ContainerPort: 8080, MemoryMiB: 4096, HealthPath: "/index/api/getServerConfig",
		HealthSeconds: 30, DataCompatibility: deploy.ZLMRecipe,
		ZLM: &deploy.ZLMConfig{
			APIPort: 18080, RTCPort: 18000, MediaAddress: "10.75.216.216",
			PlatformSourceIPs: []string{"10.75.216.25"}, SecretCredentialID: uuid.NewString(),
			SecretRevision: "1", CPUs: 4, MaxSources: 64, MaxViewers: 64,
		},
	}
	instanceID := dataOf(t, b.call("POST", "/instances", map[string]any{"name": "独立 ZLM", "targetId": targetID, "config": config}, 200))["id"].(string)
	artifactID, releaseID := uuid.NewString(), uuid.NewString()
	release := deploy.Release{ID: releaseID, InstanceID: instanceID, ArtifactID: artifactID, ImageID: "sha256:abc", Config: config}
	if _, err := s.DB.Exec("INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at,recipe) VALUES(?,?,?,?,?,?,?)", artifactID, "ZLM", hashToken("archive"), 1, release.ImageID, Now(), deploy.ZLMRecipe); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO releases VALUES(?,?,?,?,?)", releaseID, instanceID, artifactID, jsonText(release), Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE instances SET current_release_id=? WHERE id=?", releaseID, instanceID); err != nil {
		t.Fatal(err)
	}
	got := platformCall(t, b.h, "GET", "/platform/zlm-instances", token, nil)
	if got.Code != http.StatusOK {
		t.Fatal(got.Code, got.Body.String())
	}
	var envelope struct {
		Data struct {
			Items []platformZLMInstance `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].APIURL != "http://10.75.216.216:18080" || envelope.Data.Items[0].MaxTranscodes != 0 {
		t.Fatalf("wrong ZLM catalog: %+v", envelope.Data.Items)
	}
	if strings.Contains(got.Body.String(), "apiSecret") || strings.Contains(got.Body.String(), config.ZLM.SecretCredentialID) {
		t.Fatal("catalog exposed protected credential")
	}
	if got = platformCall(t, b.h, "POST", "/platform/zlm-instances/"+instanceID+"/handoff", token, map[string]string{"releaseId": uuid.NewString()}); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "RELEASE_CHANGED") {
		t.Fatal("stale release was not fenced", got.Code, got.Body.String())
	}
	if got = platformCall(t, b.h, "POST", "/platform/zlm-instances/"+instanceID+"/handoff", token, map[string]string{"releaseId": releaseID, "unexpected": "value"}); got.Code != http.StatusBadRequest {
		t.Fatal("extra request field accepted", got.Code)
	}
}
