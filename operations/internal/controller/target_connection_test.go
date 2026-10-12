package controller

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Exercises the real SSH handshake/client against an isolated protocol server;
// it does not stand in for OpenSSH/sudo acceptance on a second Linux host.
func TestSSHFixedCommandFingerprintAndCancellation(t *testing.T) {
	s, b := setup(t)
	b.login()
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "test ssh", "kind": "SSH_KEY", "secret": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))}, 201))
	clientSigner, _ := ssh.NewSignerFromKey(clientKey)
	_, hostKey, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostKey)
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
			return nil, io.EOF
		}
		return nil, nil
	}}
	serverConfig.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var commands atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					ch, requests, err := channel.Accept()
					if err != nil {
						return
					}
					for req := range requests {
						if req.Type != "exec" {
							req.Reply(false, nil)
							continue
						}
						var payload struct{ Command string }
						if ssh.Unmarshal(req.Payload, &payload) != nil || payload.Command != "/usr/bin/sudo -n '/opt/ssh-test/executor' target-exec" {
							req.Reply(false, nil)
							ch.Close()
							return
						}
						commands.Add(1)
						req.Reply(true, nil)
						input, _ := io.ReadAll(ch)
						if string(input) == "wait" {
							<-requests
							ch.Close()
							return
						}
						ch.Write([]byte(`{"platform":"linux/amd64"}`))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						ch.Close()
					}
				}
			}()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	target := target{InstallRoot: "/opt/ssh-test", sshTarget: sshTarget{Kind: "SSH", Address: "127.0.0.1", Port: port, User: "test", Fingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey()), CredentialID: credential["id"].(string), Revision: "1"}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var output, stderr bytes.Buffer
	if err = s.sshExecute(ctx, target, strings.NewReader("probe"), &output, &stderr); err != nil || !strings.Contains(output.String(), "linux/amd64") {
		t.Fatal(err, output.String())
	}
	target.Fingerprint = "SHA256:" + strings.Repeat("A", 43)
	if s.sshExecute(ctx, target, strings.NewReader("probe"), io.Discard, io.Discard) == nil {
		t.Fatal("changed fingerprint accepted")
	}
	if commands.Load() != 1 {
		t.Fatal("command executed after fingerprint rejection")
	}
	target.Fingerprint = ssh.FingerprintSHA256(hostSigner.PublicKey())
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer shortCancel()
	start := time.Now()
	if s.sshExecute(shortCtx, target, strings.NewReader("wait"), io.Discard, io.Discard) == nil || time.Since(start) > time.Second {
		t.Fatal("SSH cancellation not bounded")
	}
}
