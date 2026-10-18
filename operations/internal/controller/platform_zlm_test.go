package controller

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

func TestPlatformZLMCatalogAndAuthenticatedHandoff(t *testing.T) {
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
	const apiSecret = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	secretCredential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "ZLM API", "kind": "TOKEN", "secret": apiSecret}, 201))["id"].(string)
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	sshCredential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "isolated target", "kind": "SSH_KEY", "secret": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))}, 201))["id"].(string)
	clientSigner, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	sshConfig := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
			return nil, io.EOF
		}
		return nil, nil
	}}
	sshConfig.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var healthy atomic.Bool
	requests := make(chan deploy.Request, 2)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, channels, globalRequests, handshakeErr := ssh.NewServerConn(conn, sshConfig)
				if handshakeErr != nil {
					return
				}
				go ssh.DiscardRequests(globalRequests)
				for channel := range channels {
					ch, channelRequests, channelErr := channel.Accept()
					if channelErr != nil {
						return
					}
					for request := range channelRequests {
						var command struct{ Command string }
						if request.Type != "exec" || ssh.Unmarshal(request.Payload, &command) != nil || command.Command != "/usr/bin/sudo -n '/opt/zlm-node-a/executor' target-exec" {
							request.Reply(false, nil)
							return
						}
						request.Reply(true, nil)
						var targetRequest deploy.Request
						if json.NewDecoder(ch).Decode(&targetRequest) != nil {
							return
						}
						requests <- targetRequest
						result := deploy.Result{Health: "UNAVAILABLE", ReleaseID: targetRequest.Candidate.ID, ConfigHash: deploy.Digest(targetRequest.Candidate.Config)}
						if healthy.Load() {
							result.Health = "APPLICATION_READY"
						}
						_ = json.NewEncoder(ch).Encode(result)
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						ch.Close()
					}
				}
			}()
		}
	}()
	targetID := dataOf(t, b.call("POST", "/targets", map[string]any{
		"name": "ZLM host", "kind": "SSH", "address": "127.0.0.1", "sshPort": listener.Addr().(*net.TCPAddr).Port,
		"sshUser": "ops", "hostFingerprint": ssh.FingerprintSHA256(hostSigner.PublicKey()),
		"credentialId": sshCredential, "credentialRevision": "1", "installRoot": "/opt/zlm-node-a",
	}, 201))["id"].(string)
	config := deploy.Config{
		Environment: "zlm", BindAddress: "10.75.216.216", HostPort: 18080,
		ContainerPort: 8080, MemoryMiB: 4096, HealthPath: "/index/api/getServerConfig",
		HealthSeconds: 30, DataCompatibility: deploy.ZLMRecipe,
		ZLM: &deploy.ZLMConfig{
			APIPort: 18080, RTCPort: 18000, MediaAddress: "10.75.216.216",
			PlatformSourceIPs: []string{"10.75.216.25"}, SecretCredentialID: secretCredential,
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
	got = platformCall(t, b.h, "POST", "/platform/zlm-instances/"+instanceID+"/handoff", token, map[string]string{"releaseId": releaseID})
	if got.Code != http.StatusConflict || strings.Contains(got.Body.String(), apiSecret) {
		t.Fatal("unhealthy target returned a handoff or credential", got.Code, got.Body.String())
	}
	assertInspect := func() {
		t.Helper()
		select {
		case request := <-requests:
			if request.Action != "INSPECT" || request.Candidate == nil || request.Candidate.ID != releaseID || request.Secrets[secretCredential+":1"] != apiSecret {
				t.Fatal("handoff did not inspect the selected release with its protected credential")
			}
		case <-time.After(time.Second):
			t.Fatal("target inspection was not invoked")
		}
	}
	assertInspect()
	healthy.Store(true)
	got = platformCall(t, b.h, "POST", "/platform/zlm-instances/"+instanceID+"/handoff", token, map[string]string{"releaseId": releaseID})
	if got.Code != http.StatusOK {
		t.Fatal("healthy release handoff failed", got.Code, got.Body.String())
	}
	assertInspect()
	var handoff struct {
		Data struct {
			InstanceID string              `json:"instanceId"`
			ReleaseID  string              `json:"releaseId"`
			Connection platformZLMInstance `json:"connection"`
			APISecret  string              `json:"apiSecret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	if handoff.Data.InstanceID != instanceID || handoff.Data.ReleaseID != releaseID || !reflect.DeepEqual(handoff.Data.Connection, envelope.Data.Items[0]) || handoff.Data.APISecret != apiSecret {
		t.Fatalf("handoff differs from the selected published catalog entry: %+v", handoff.Data.Connection)
	}
}
