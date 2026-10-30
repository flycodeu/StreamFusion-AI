package controller

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"golang.org/x/crypto/ssh"
)

type hostSSHFixture struct {
	Port            int
	Fingerprint     string
	CredentialID    string
	Authentications atomic.Int32
	Commands        atomic.Int32
	Key             atomic.Pointer[ssh.Signer]
	Responses       atomic.Pointer[func(string) *hostSSHResponse]
}

type hostSSHResponse struct {
	Stdout, Stderr string
	Exit           uint32
	Input          func([]byte)
}

func hostSSHServer(t *testing.T, s *Store, b *browser, executorAvailable, dockerAvailable bool, extensions ...func(string) ([]byte, bool)) *hostSSHFixture {
	t.Helper()
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	pk, _ := x509.MarshalPKCS8PrivateKey(private)
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "SSH test key", "kind": "SSH_KEY", "secret": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))}, 201))
	signer, _ := ssh.NewSignerFromKey(private)
	_, hostKey, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostKey)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	f := &hostSSHFixture{Port: listener.Addr().(*net.TCPAddr).Port, Fingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey()), CredentialID: credential["id"].(string)}
	f.Key.Store(&hostSigner)
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					f.Authentications.Add(1)
					if !bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
						return nil, io.EOF
					}
					return nil, nil
				}, PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
					f.Authentications.Add(1)
					if string(password) != "test-ssh-password" {
						return nil, io.EOF
					}
					return nil, nil
				}}
				config.AddHostKey(*f.Key.Load())
				_, channels, requests, e := ssh.NewServerConn(conn, config)
				if e != nil {
					return
				}
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					ch, requests, e := channel.Accept()
					if e != nil {
						return
					}
					for request := range requests {
						var payload struct{ Command string }
						if request.Type != "exec" || ssh.Unmarshal(request.Payload, &payload) != nil {
							request.Reply(false, nil)
							continue
						}
						f.Commands.Add(1)
						if handler := f.Responses.Load(); handler != nil {
							if response := (*handler)(payload.Command); response != nil {
								request.Reply(true, nil)
								input, _ := io.ReadAll(io.LimitReader(ch, 2<<20))
								if response.Input != nil {
									response.Input(input)
								}
								io.WriteString(ch, response.Stdout)
								io.WriteString(ch.Stderr(), response.Stderr)
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{response.Exit}))
								ch.Close()
								continue
							}
						}
						handled := false
						for _, extension := range extensions {
							if data, ok := extension(payload.Command); ok {
								request.Reply(true, nil)
								// Both shell probes and target-exec consume stdin before
								// exit. An early fixture close races SSH's stdin copier.
								_, _ = io.Copy(io.Discard, io.LimitReader(ch, 1<<20))
								ch.Write(data)
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								ch.Close()
								handled = true
								break
							}
						}
						if handled {
							continue
						}
						if payload.Command == hostReadOnlyCommand {
							request.Reply(true, nil)
							io.WriteString(ch, "SFOPS_HOST_V1\nLinux\nx86_64\nDOCKER_MISSING\n")
							ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							ch.Close()
							continue
						}
						if payload.Command == executorFileProbeCommand("/opt/streamfusion-ops-target") {
							request.Reply(true, nil)
							state := "MISSING"
							if executorAvailable {
								state = "PRESENT"
							}
							io.WriteString(ch, "SFOPS_EXECUTOR_V1\n"+state+"\n")
							ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							ch.Close()
							continue
						}
						if !executorAvailable || payload.Command != "/usr/bin/sudo -n '/opt/streamfusion-ops-target/executor' target-exec" {
							request.Reply(false, nil)
							ch.Close()
							continue
						}
						request.Reply(true, nil)
						var envelope deploy.Request
						if json.NewDecoder(ch).Decode(&envelope) != nil {
							ch.Close()
							continue
						}
						_, _ = io.Copy(io.Discard, io.LimitReader(ch, 1<<20))
						json.NewEncoder(ch).Encode(map[string]any{"owner": deploy.Owner{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: envelope.ControllerID, TargetID: envelope.TargetID}, "dockerAvailable": dockerAvailable, "softwareLifecycleVersion": 1, "platform": "linux/amd64"})
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						ch.Close()
					}
				}
			}()
		}
	}()
	return f
}

func (f *hostSSHFixture) connection() map[string]any {
	return map[string]any{"address": "127.0.0.1", "sshPort": f.Port, "sshUser": "ops", "credentialId": f.CredentialID}
}
func (f *hostSSHFixture) create(t *testing.T, b *browser, name string) map[string]any {
	t.Helper()
	in := f.connection()
	d := dataOf(t, b.call("POST", "/hosts/fingerprint", in, 200))
	in["name"] = name
	in["kind"] = "SSH"
	in["fingerprintToken"] = d["fingerprintToken"]
	return dataOf(t, b.call("POST", "/hosts", in, 201))
}

func TestHostFingerprintConfirmationAndCapabilities(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	discovery := dataOf(t, b.call("POST", "/hosts/fingerprint", f.connection(), 200))
	if discovery["hostFingerprint"] != f.Fingerprint || f.Authentications.Load() != 0 {
		t.Fatal("discovery used credential authentication or returned wrong key")
	}
	in := f.connection()
	in["name"] = "host-a"
	in["kind"] = "SSH"
	b.call("POST", "/hosts", in, 409)
	in["hostFingerprint"] = f.Fingerprint
	b.call("POST", "/hosts", in, 400)
	delete(in, "hostFingerprint")
	in["fingerprintToken"] = discovery["fingerprintToken"]
	in["sshUser"] = "other"
	b.call("POST", "/hosts", in, 409)
	in["sshUser"] = "ops"
	host := dataOf(t, b.call("POST", "/hosts", in, 201))
	id := host["id"].(string)
	if host["credentialRevision"] != "1" || host["version"] != "1" {
		t.Fatal(host)
	}
	b.call("POST", "/hosts", in, 409)
	checked := dataOf(t, b.call("POST", "/hosts/"+id+"/checks", nil, 200))
	caps := checked["capabilities"].(map[string]any)
	for _, key := range []string{"reachability", "readOnly"} {
		if caps[key].(map[string]any)["status"] != "AVAILABLE" {
			t.Fatal(caps)
		}
	}
	if caps["docker"].(map[string]any)["code"] != "DOCKER_NOT_INSTALLED" || caps["mutation"].(map[string]any)["status"] != "UNAVAILABLE" {
		t.Fatal(caps)
	}
	if caps["expiresAt"] == "" || checked["observedAt"] == "" {
		t.Fatal("missing freshness")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, caps["expiresAt"].(string))
	if err != nil || time.Until(expiresAt) < 29*time.Minute {
		t.Fatal("host capability window is too short", caps["expiresAt"], err)
	}
	_, newKey, _ := ed25519.GenerateKey(rand.Reader)
	newSigner, _ := ssh.NewSignerFromKey(newKey)
	f.Key.Store(&newSigner)
	checked = dataOf(t, b.call("POST", "/hosts/"+id+"/checks", nil, 200))
	caps = checked["capabilities"].(map[string]any)
	if caps["reachability"].(map[string]any)["code"] != "SSH_HOST_KEY_CHANGED" {
		t.Fatal(caps)
	}
	if caps["readOnly"].(map[string]any)["status"] != "UNKNOWN" {
		t.Fatal("stale read-only capability survived failed handshake")
	}
	if f.Commands.Load() != 3 {
		t.Fatal("commands ran after key change", f.Commands.Load())
	}
	b.call("GET", "/targets", nil, 404)
	b.call("GET", "/targets/"+id, nil, 404)
	b.call("POST", "/targets/"+id+"/probes", nil, 404)
}

func TestHostMutationCapabilityIndependentFromDocker(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, true, false)
	host := f.create(t, b, "native-host")
	checked := dataOf(t, b.call("POST", "/hosts/"+host["id"].(string)+"/checks", nil, 200))
	caps := checked["capabilities"].(map[string]any)
	if caps["mutation"].(map[string]any)["status"] != "AVAILABLE" || caps["docker"].(map[string]any)["status"] != "UNAVAILABLE" {
		t.Fatal(caps)
	}
}

func TestHostCRUDCredentialRotationMaintenanceAndReferences(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	host := f.create(t, b, "host-a")
	id := host["id"].(string)
	in := f.connection()
	in["name"] = "renamed"
	in["kind"] = "SSH"
	in["expectedVersion"] = "1"
	updated := dataOf(t, b.call("PATCH", "/hosts/"+id, in, 200))
	if updated["version"] != "2" || updated["name"] != "renamed" {
		t.Fatal(updated)
	}
	b.call("PATCH", "/hosts/"+id, in, 409)
	dataOf(t, b.call("PATCH", "/hosts/"+id+"/maintenance", map[string]any{"maintenance": true, "expectedVersion": "2"}, 200))
	b.call("POST", "/hosts/"+id+"/plugin-install-plans", map[string]string{"pluginKey": "java-runtime", "version": "21-jre", "recipeRevision": "java-runtime-v1"}, 409)
	b.call("POST", "/hosts/"+id+"/checks", nil, 200)
	b.call("DELETE", "/hosts/"+id+"?expectedVersion=2", nil, 409)
	b.call("DELETE", "/hosts/"+id+"?expectedVersion=3", nil, 200)
	local := dataOf(t, b.call("POST", "/hosts", map[string]any{"name": "local", "kind": "LOCAL"}, 201))
	localID := local["id"].(string)
	b.call("POST", "/hosts", map[string]any{"name": "local 2", "kind": "LOCAL"}, 409)
	_, e := s.DB.Exec("INSERT INTO instances(id,name,target_id,draft_json,created_at) VALUES(?,?,?,'{}',?)", uuid.NewString(), "reference", localID, Now())
	if e != nil {
		t.Fatal(e)
	}
	b.call("DELETE", "/hosts/"+localID+"?expectedVersion=1", nil, 409)
}

func TestHostChallengeExpiryCredentialRevisionAndSSHLimit(t *testing.T) {
	s, b := setup(t)
	b.login()
	f := hostSSHServer(t, s, b, false, false)
	d := dataOf(t, b.call("POST", "/hosts/fingerprint", f.connection(), 200))
	in := f.connection()
	in["name"] = "expiry"
	in["kind"] = "SSH"
	in["fingerprintToken"] = d["fingerprintToken"]
	s.DB.Exec("UPDATE host_fingerprint_challenges SET expires_at=?", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano))
	b.call("POST", "/hosts", in, 409)
	d = dataOf(t, b.call("POST", "/hosts/fingerprint", f.connection(), 200))
	in["fingerprintToken"] = d["fingerprintToken"]
	b.call("POST", "/credentials/"+f.CredentialID+"/revisions", map[string]string{"name": "rotated", "kind": "SSH_KEY", "secret": "test-key-material", "expectedRevision": "1"}, 201)
	b.call("POST", "/hosts", in, 409)
	for n := 0; n < 16; n++ {
		_, e := s.DB.Exec("INSERT INTO targets(id,name,install_root,kind,address,ssh_port,created_at) VALUES(?,?,'/opt/test','SSH',?,22,?)", uuid.NewString(), fmt.Sprint("seed-", n), fmt.Sprint("192.0.2.", n+1), Now())
		if e != nil {
			t.Fatal(e)
		}
	}
	d = dataOf(t, b.call("POST", "/hosts/fingerprint", f.connection(), 200))
	in["fingerprintToken"] = d["fingerprintToken"]
	b.call("POST", "/hosts", in, 409)
}

func TestHostAddressValidationAndReadOnlyOutput(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "2001:db8::1", "node-a.internal"} {
		if _, ok := normalizeHostAddress(address); !ok {
			t.Fatal(address)
		}
	}
	for _, address := range []string{"0.0.0.0", "::", "host;touch /tmp/x", "http://server", "-option", "server:22"} {
		if _, ok := normalizeHostAddress(address); ok {
			t.Fatal(address)
		}
	}
	facts := unknownHostCapabilities()
	applyHostReadOnly(&facts, strings.Repeat("bad", 4096))
	if facts.ReadOnly.Status != "UNAVAILABLE" {
		t.Fatal(facts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, e := discoverSSHKey(ctx, "192.0.2.1", 22)
	if e == nil {
		t.Fatal("unreachable discovery succeeded")
	}
}
