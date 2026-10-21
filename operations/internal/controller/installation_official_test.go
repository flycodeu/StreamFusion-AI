package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Cancellation is injected before dispatch. These tests exercise the real
// durable material state machine without depending on vendor network access.
func officialMaterialCall(t *testing.T, b *browser, input any, canceled bool, want int) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest("POST", "http://127.0.0.1:19090"+api+"/installation-materials/official", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:19090")
	r.Header.Set("X-CSRF-Token", b.csrf)
	r.AddCookie(b.cookie)
	if canceled {
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		r = r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(installDeadlineRecorder{ResponseRecorder: w}, r)
	if w.Code != want {
		t.Fatalf("official material got %d, want %d: %s", w.Code, want, w.Body.String())
	}
	return w
}

func TestOfficialMaterialCancellationReplayAndRecovery(t *testing.T) {
	s, b := setup(t)
	b.login()
	d, _ := serviceinstall.Find("nginx", "1.30.6")
	in := map[string]string{"recipeKey": d.Key, "recipeVersion": d.Version, "recipeRevision": d.Revision, "idempotencyKey": "official-stable-request"}
	w := officialMaterialCall(t, b, in, true, 409)
	if !strings.Contains(w.Body.String(), "OFFICIAL_DOWNLOAD_FAILED") {
		t.Fatal(w.Body.String())
	}
	var id, status string
	if err := s.DB.QueryRow("SELECT id,status FROM installation_materials WHERE idempotency_key=?", in["idempotencyKey"]).Scan(&id, &status); err != nil || status != "FAILED" {
		t.Fatal(id, status, err)
	}
	if _, err := os.Stat(s.installationMaterialPath(id, true)); !os.IsNotExist(err) {
		t.Fatal("canceled download left partial file", err)
	}
	s.DB.Exec("UPDATE installation_materials SET status='DOWNLOADING' WHERE id=?", id)
	replay := dataOf(t, officialMaterialCall(t, b, in, false, 200))
	if replay["id"] != id || replay["status"] != "DOWNLOADING" {
		t.Fatal(replay)
	}
	changed := map[string]string{"recipeKey": "redis", "recipeVersion": "7.4.11", "recipeRevision": "redis-docker-v1", "idempotencyKey": in["idempotencyKey"]}
	if !strings.Contains(officialMaterialCall(t, b, changed, false, 409).Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal("different vendor accepted under same operation key")
	}
	b.call("DELETE", "/installation-materials/"+id, nil, 409)
	s.recoverInstallationMaterials()
	m, err := s.readInstallationMaterial(id)
	if err != nil || m.Status != "FAILED" || m.ErrorCode != "UPLOAD_INTERRUPTED" {
		t.Fatal(m, err)
	}
	// Simulate an uncertain filesystem publish. Invalid bytes must be rejected
	// without overwriting them or fetching a replacement under the same record.
	file := s.installationMaterialPath(id, false)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("invalid published vendor archive"), 0600); err != nil {
		t.Fatal(err)
	}
	officialMaterialCall(t, b, in, false, 409)
	contents, _ := os.ReadFile(file)
	if string(contents) != "invalid published vendor archive" {
		t.Fatal("uncertain published file was overwritten")
	}
	b.call("DELETE", "/installation-materials/"+id, nil, 200)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("explicit deletion did not remove failed owned material", err)
	}
}
