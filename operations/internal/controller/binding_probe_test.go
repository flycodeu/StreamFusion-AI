package controller

import (
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestBindingProbeUsesFrozenReleaseAndRejectsOldExecutor(t *testing.T) {
	s, b := setup(t)
	b.login()
	var mode atomic.Int32
	requests := make(chan deploy.Request, 8)
	tid := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		if req.Action != "BINDING_PROBE" {
			return nil, false
		}
		requests <- req
		if req.Candidate == nil || mode.Load() == 1 {
			return []byte(`{"reachable":true}`), true
		}
		if mode.Load() == 2 {
			s.DB.Exec("UPDATE instances SET current_release_id=NULL WHERE id=?", req.Candidate.InstanceID)
		}
		out := deploy.BindingProbe{Reachable: true, ReleaseID: req.Candidate.ID, CredentialRevision: req.Binding.CredentialRevision, ObservedAt: Now(), ApplicationNetwork: &deploy.ProbeLayer{Layer: "APPLICATION_NETWORK_TCP", Executed: true, Reachable: true}, Authentication: &deploy.ProbeLayer{Layer: "AUTHENTICATION", Executed: true, Reachable: true, Authenticated: false, ErrorCode: "AUTH_REJECTED"}}
		raw, _ := json.Marshal(out)
		return raw, true
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "dependency-password", "kind": "PASSWORD", "secret": "private-dependency-password"}, 201))
	bound := dataOf(t, b.call("POST", "/bindings", deploy.Binding{Kind: "REDIS", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Database: "0", CredentialID: credential["id"].(string), CredentialRevision: "1"}, 201))
	bindingID := bound["id"].(string)
	binding, err := s.readBinding(bindingID)
	if err != nil {
		t.Fatal(err)
	}
	config := deploy.Config{Environment: "test", BindAddress: "127.0.0.1", HostPort: 18080, ContainerPort: 8080, MemoryMiB: 512, HealthPath: "/actuator/health", HealthSeconds: 10, DataCompatibility: "v1", BindingIDs: []string{bindingID}}
	instanceID := dataOf(t, b.call("POST", "/instances", map[string]any{"name": "dependency-consumer", "targetId": tid, "config": config}, 200))["id"].(string)
	artifactID, releaseID := uuid.NewString(), uuid.NewString()
	release := deploy.Release{ID: releaseID, InstanceID: instanceID, ArtifactID: artifactID, ArchiveSHA256: strings.Repeat("a", 64), ByteSize: 1, ImageID: "sha256:" + strings.Repeat("b", 64), Config: config, Bindings: []deploy.Binding{binding}}
	if _, err = s.DB.Exec("INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at) VALUES(?,'a',?,1,?,?)", artifactID, release.ArchiveSHA256, release.ImageID, Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO releases VALUES(?,?,?,?,?)", releaseID, instanceID, artifactID, jsonText(release), Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE instances SET current_release_id=? WHERE id=?", releaseID, instanceID); err != nil {
		t.Fatal(err)
	}
	path := "/bindings/" + bindingID + "/probes"
	input := map[string]string{"targetId": tid, "instanceId": instanceID, "releaseId": releaseID}
	response := b.call("POST", path, input, 200)
	result := dataOf(t, response)
	if strings.Contains(response.Body.String(), "private-dependency-password") || result["releaseId"] != releaseID || result["authentication"].(map[string]any)["authenticated"] != false {
		t.Fatal(result)
	}
	req := <-requests
	if req.Candidate == nil || req.Candidate.ID != releaseID || req.Secrets[bindingID] != "private-dependency-password" || req.Binding.CredentialRevision != "1" {
		t.Fatal("not frozen release/credential", req.Action)
	}
	// The old host-only request still works and must not receive application credentials.
	b.call("POST", path, map[string]string{"targetId": tid}, 200)
	req = <-requests
	if req.Candidate != nil || len(req.Secrets) != 0 {
		t.Fatal("host probe received application secrets")
	}
	b.call("POST", path, map[string]string{"targetId": tid, "releaseId": releaseID}, 400)
	mode.Store(1)
	if out := b.call("POST", path, input, 409); !strings.Contains(out.Body.String(), "TARGET_PROBE_UPGRADE_REQUIRED") {
		t.Fatal(out.Body.String())
	}
	<-requests
	mode.Store(2)
	if out := b.call("POST", path, input, 409); !strings.Contains(out.Body.String(), "PROBE_RELEASE_CHANGED") {
		t.Fatal(out.Body.String())
	}
	<-requests
	s.DB.Exec("UPDATE instances SET current_release_id=? WHERE id=?", releaseID, instanceID)
	mode.Store(0)
	jobID := uuid.NewString()
	if _, err = s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,target_ack_pending,trace_id,created_at,updated_at) VALUES(?,?,'PUBLISH',?,?,?,'SUCCEEDED','DONE','{}',1,'probe',?,?)", jobID, b.currentAdmin(), tid, jobID, hashToken(jobID), Now(), Now()); err != nil {
		t.Fatal(err)
	}
	b.call("POST", path, input, 409)
	select {
	case <-requests:
		t.Fatal("probe crossed pending ACK")
	default:
	}
}
