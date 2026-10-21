package deploy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func serviceRequestFixture(t *testing.T) (*executor, Request) {
	t.Helper()
	d, _ := serviceinstall.Find("nginx", "1.30.6")
	x := &executor{root: t.TempDir(), owner: Owner{ControllerID: uuid.NewString(), TargetID: uuid.NewString()}}
	req := Request{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: x.owner.ControllerID, TargetID: x.owner.TargetID, JobID: uuid.NewString(), TraceID: uuid.NewString(), Action: "SOFTWARE_INSTALL", SoftwareOperation: "SOFTWARE_INSTALL", DeadlineAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339), Installation: &serviceinstall.Request{SoftwareID: uuid.NewString(), BindingRevision: 1, Definition: d, Config: serviceinstall.Config{Name: "Nginx", BindAddress: "127.0.0.1", HostPort: 8088, MemoryMiB: 256}, Source: serviceinstall.Source{Type: "REGISTRY"}}}
	req.InputHash = Digest(req)
	return x, req
}

func TestServiceInstallInputFreezesOptionsAndRejectsConflictingObjects(t *testing.T) {
	_, req := serviceRequestFixture(t)
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	req.Installation.Config.HostPort++
	if err := validateSoftwareInput(req); err == nil || err.Error() != "INPUT_HASH_MISMATCH" {
		t.Fatalf("changed port did not invalidate frozen request: %v", err)
	}
	req.Installation.Config.HostPort--
	req.Software = &SoftwareRequest{}
	if validateSoftwareInput(req) == nil {
		t.Fatal("installation accepted a second lifecycle object")
	}
}

func TestServiceOwnedContainerDoesNotWeakenExternalProtection(t *testing.T) {
	x, req := serviceRequestFixture(t)
	id := strings.Repeat("a", 64)
	s := SoftwareRequest{ID: req.Installation.SoftwareID, Version: 1, Kind: "DOCKER", Locator: id, SoftwareKey: "NGINX", Ownership: x.installOwnership(req)}
	labels := serviceLabels(s.Ownership)
	if !serviceOwnershipMatches(s, "/"+serviceName(s.ID), req.Installation.Definition.ImageID, labels) {
		t.Fatal("exact installed object rejected")
	}
	for _, key := range []string{"controller", "target", "software", "job", "config"} {
		original := labels[serviceLabel+key]
		labels[serviceLabel+key] = "forged"
		if serviceOwnershipMatches(s, "/"+serviceName(s.ID), req.Installation.Definition.ImageID, labels) {
			t.Fatalf("forged %s accepted", key)
		}
		labels[serviceLabel+key] = original
	}
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
		return json.Marshal(map[string]any{"id": id, "name": "/" + serviceName(s.ID), "imageId": req.Installation.Definition.ImageID, "image": req.Installation.Definition.ImageID, "labels": labels, "state": "running"})
	}
	actual, err := ObserveSoftware(context.Background(), s, x.softwareRunner)
	if err != nil || actual.Locator != id || actual.State != "RUNNING" {
		t.Fatalf("owned observe failed: %+v %v", actual, err)
	}
	s.Ownership = nil
	if _, err = ObserveSoftware(context.Background(), s, x.softwareRunner); err == nil {
		t.Fatal("external adoption bypassed managed-container protection")
	}
}

func TestServicePublishedPortsCoversNATWildcardAndRanges(t *testing.T) {
	for _, test := range []struct {
		ports string
		port  int
		busy  bool
	}{
		{"0.0.0.0:13306->3306/tcp, [::]:13306->3306/tcp", 13306, true},
		{"127.0.0.1:8088->80/tcp", 8088, true},
		{"192.0.2.3:8088->80/tcp", 8088, false},
		{"0.0.0.0:8080-8090->80-90/tcp", 8088, true},
		{"0.0.0.0:8088->80/udp", 8088, false},
		{"3306/tcp", 3306, false},
	} {
		t.Run(test.ports, func(t *testing.T) {
			x := &executor{softwareRun: func(time.Duration, string, ...string) ([]byte, error) {
				return json.Marshal(map[string]string{"id": "abc123", "name": "existing-database", "ports": test.ports})
			}}
			owner, err := x.servicePublishedPort(context.Background(), "127.0.0.1", test.port)
			if err != nil || (owner != "") != test.busy {
				t.Fatalf("mapping got owner=%q err=%v", owner, err)
			}
		})
	}
	x := &executor{softwareRun: func(time.Duration, string, ...string) ([]byte, error) { return []byte("not valid metadata"), nil }}
	if _, err := x.servicePublishedPort(context.Background(), "127.0.0.1", 8333); err == nil {
		t.Fatal("unknown metadata accepted as free")
	}
}

func TestInstallationProgressAndLogsAreReadOnlyAndBoundToIntent(t *testing.T) {
	x, req := serviceRequestFixture(t)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"intent.json": req, "progress.json": softwareResult(req)} {
		data, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	secret := "INSTALL_TEST_ONLY_SECRET"
	x.secrets = map[string]string{"fixture": secret}
	if err := os.WriteFile(filepath.Join(dir, "service-install.log"), []byte("output="+secret), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"PROGRESS", "SOFTWARE_INSTALL_LOGS"} {
		req.Action = action
		var out bytes.Buffer
		if err := x.executeSoftware(req, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), secret) {
			t.Fatal("log credential leaked")
		}
	}
	req.JobID = uuid.NewString()
	if err := x.executeSoftware(req, &bytes.Buffer{}); err == nil {
		t.Fatal("cross-job log request accepted")
	}
}

func TestServiceReconciliationBeforeCreateIsTerminalWithoutReplay(t *testing.T) {
	for _, phase := range []string{"SOFTWARE_PREFLIGHT", "SERVICE_IMAGE_PULL", "SERVICE_IMAGE_LOAD", "SERVICE_IMAGE_VERIFY", "SERVICE_CONFIGURE"} {
		t.Run(phase, func(t *testing.T) {
			x, req := serviceRequestFixture(t)
			dir := filepath.Join(x.root, "jobs", req.JobID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			progress := softwareResult(req)
			progress.Phase = phase
			data, _ := json.Marshal(progress)
			if err := os.WriteFile(filepath.Join(dir, "progress.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
				t.Fatal("pre-create recovery executed a command")
				return nil, nil
			}
			result := x.reconcileServiceInstallation(req, dir)
			if result.Status != "FAILED" || result.Phase != "SERVICE_NOT_CREATED" {
				t.Fatalf("pre-create interruption cannot terminate: %+v", result)
			}
			if err := os.WriteFile(filepath.Join(dir, "service-create.json"), []byte("damaged evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			if x.reconcileServiceInstallation(req, dir).Status != "UNKNOWN" {
				t.Fatal("damaged create boundary treated as absent")
			}
		})
	}
}

func TestInstallationLogTailFitsTransportWithUTF8AndEscapes(t *testing.T) {
	for _, payload := range []string{
		strings.Repeat("旧日志", 12000) + strings.Repeat("多字节视频", 3000) + "最新执行结果",
		strings.Repeat("\x00\x01\x02\t\n\"\\", 12000) + "latest-result",
	} {
		x, req := serviceRequestFixture(t)
		dir := filepath.Join(x.root, "jobs", req.JobID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		intent, _ := json.Marshal(req)
		if err := os.WriteFile(filepath.Join(dir, "intent.json"), intent, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "service-install.log"), []byte(payload), 0600); err != nil {
			t.Fatal(err)
		}
		req.Action = "SOFTWARE_INSTALL_LOGS"
		var out bytes.Buffer
		if err := x.readServiceInstallation(req, &out); err != nil {
			t.Fatal(err)
		}
		if out.Len() >= 128<<10 {
			t.Fatalf("escaped log exceeds transport: %d", out.Len())
		}
		var logs struct {
			Text    string `json:"text"`
			Limited bool   `json:"limited"`
			Source  string `json:"source"`
		}
		if err := json.Unmarshal(out.Bytes(), &logs); err != nil {
			t.Fatal(err)
		}
		if !logs.Limited || logs.Source != "TARGET_EXECUTION" || len(logs.Text) > 16<<10 || !utf8.ValidString(logs.Text) || !strings.HasSuffix(payload, logs.Text) {
			t.Fatal("tail is not bounded, valid UTF-8, or the actual recent output")
		}
	}
}

func TestInstallationBindingRevisionSurvivesReceiptRoundTrip(t *testing.T) {
	x, req := serviceRequestFixture(t)
	id := strings.Repeat("a", 64)
	s := SoftwareRequest{ID: req.Installation.SoftwareID, Version: 1, Kind: "DOCKER", Locator: id, IdentityHash: Digest([]string{"DOCKER", id, req.Installation.Definition.ImageID}), SoftwareKey: "NGINX", Ownership: x.installOwnership(req)}
	x.softwareRun = func(time.Duration, string, ...string) ([]byte, error) {
		return json.Marshal(map[string]any{"id": id, "name": "/" + serviceName(s.ID), "imageId": req.Installation.Definition.ImageID, "image": req.Installation.Definition.ImageID, "labels": serviceLabels(s.Ownership), "state": "running"})
	}
	if err := os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(softwareBinding{Software: s, JobID: req.JobID})
	if err := os.WriteFile(x.softwareFile(s.ID), data, 0600); err != nil {
		t.Fatal(err)
	}
	result := x.serviceInstallResult(req, s, "nginx/1.30.6", "READY", "SUCCEEDED")
	if err := x.verifyServiceInstallation(req, result); err != nil {
		t.Fatal(err)
	}
	if result.Software.VersionSource != "INSTALL_PROBE" || result.Software.Version != "nginx/1.30.6" {
		t.Fatal("actual installed program version lost")
	}
	// JSON restores a different ownership pointer; semantic comparison is
	// required for the first start/stop after installation.
	if err := x.boundSoftware(s); err != nil {
		t.Fatal(err)
	}
	s.Version = 2
	if x.boundSoftware(s) == nil {
		t.Fatal("configuration version substituted for binding revision")
	}
}

func TestSeaweedS3HealthUsesAuthenticatedProtocol(t *testing.T) {
	const secret = "fixture-s3-secret-123"
	sign := func(key []byte, value string) []byte {
		h := hmac.New(sha256.New, key)
		_, _ = h.Write([]byte(value))
		return h.Sum(nil)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		date := r.Header.Get("X-Amz-Date")
		if len(date) < 8 || !strings.Contains(r.Header.Get("Authorization"), "Credential=fixture-access/") {
			w.WriteHeader(403)
			return
		}
		payload := sha256.Sum256(nil)
		canonical := "GET\n/\n\nhost:" + r.Host + "\nx-amz-content-sha256:" + hex.EncodeToString(payload[:]) + "\nx-amz-date:" + date + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + hex.EncodeToString(payload[:])
		requestHash := sha256.Sum256([]byte(canonical))
		key := sign([]byte("AWS4"+secret), date[:8])
		key = sign(key, "us-east-1")
		key = sign(key, "s3")
		key = sign(key, "aws4_request")
		expected := hex.EncodeToString(sign(key, "AWS4-HMAC-SHA256\n"+date+"\n"+date[:8]+"/us-east-1/s3/aws4_request\n"+hex.EncodeToString(requestHash[:])))
		if !strings.HasSuffix(r.Header.Get("Authorization"), "Signature="+expected) {
			w.WriteHeader(403)
			return
		}
		_, _ = w.Write([]byte(`<ListAllMyBucketsResult><Buckets/></ListAllMyBucketsResult>`))
	}))
	defer server.Close()
	endpoint := strings.TrimPrefix(server.URL, "http://")
	if err := serviceS3Authentication(context.Background(), endpoint, "fixture-access", secret); err != nil {
		t.Fatal(err)
	}
	if serviceS3Authentication(context.Background(), endpoint, "fixture-access", "wrong-secret") == nil {
		t.Fatal("wrong credentials passed protocol health")
	}
	openServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<ListAllMyBucketsResult><Buckets/></ListAllMyBucketsResult>`))
	}))
	defer openServer.Close()
	if serviceS3Authentication(context.Background(), strings.TrimPrefix(openServer.URL, "http://"), "fixture-access", secret) == nil {
		t.Fatal("anonymous open S3 endpoint reported authenticated")
	}
}
