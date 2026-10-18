package deploy

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRedisProbeAuthenticationAndPing(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			go func() {
				defer server.Close()
				_ = server.SetDeadline(time.Now().Add(time.Second))
				reader := bufio.NewReader(server)
				for _, expected := range []string{"*3\r\n$4\r\nAUTH\r\n$5\r\nalice\r\n$11\r\nsecret-test\r\n", "*2\r\n$6\r\nSELECT\r\n$1\r\n2\r\n", "*1\r\n$4\r\nPING\r\n"} {
					data := make([]byte, len(expected))
					if _, err := io.ReadFull(reader, data); err != nil || string(data) != expected {
						return
					}
					if reject {
						_, _ = io.WriteString(server, "-WRONGPASS secret-test\r\n")
						return
					}
					_, _ = io.WriteString(server, "+OK\r\n")
				}
			}()
			err := probeRedis(client, Binding{CredentialID: uuid.NewString(), Username: "alice", Database: "2"}, "secret-test")
			if reject {
				if err == nil || err.Error() != "AUTH_REJECTED" {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func mysqlGreeting(plugin string) []byte {
	p := append([]byte{10}, []byte("8.0.41\x00")...)
	base := make([]byte, 31)
	copy(base[4:], "12345678")
	binary.LittleEndian.PutUint16(base[13:], 0x8209)
	base[15] = 45
	binary.LittleEndian.PutUint16(base[18:], 0x0008)
	base[20] = 21
	p = append(p, base...)
	p = append(p, []byte("abcdefghijkl\x00")...)
	return append(p, append([]byte(plugin), 0)...)
}

func TestMySQLProbeReadOnlyAuthentication(t *testing.T) {
	for _, mode := range []string{"native", "fast", "tls-required", "rejected", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(2 * time.Second))
			done := make(chan error, 1)
			go func() {
				defer server.Close()
				_ = server.SetDeadline(time.Now().Add(2 * time.Second))
				plugin := "mysql_native_password"
				if mode == "fast" || mode == "tls-required" {
					plugin = "caching_sha2_password"
				}
				if mode == "unsupported" {
					plugin = "mysql_clear_password"
				}
				if err := mysqlWrite(server, mysqlGreeting(plugin), 0); err != nil {
					done <- err
					return
				}
				if mode == "unsupported" {
					done <- nil
					return
				}
				p, seq, err := mysqlRead(server)
				if err != nil || seq != 1 || bytes.Contains(p, []byte("secret-test")) {
					done <- fmt.Errorf("bad client authentication")
					return
				}
				if mode == "rejected" {
					_ = mysqlWrite(server, []byte{0xff, 1, 0}, 2)
					done <- nil
					return
				}
				if mode == "tls-required" {
					_ = mysqlWrite(server, []byte{1, 4}, 2)
					done <- nil
					return
				}
				okSeq := byte(2)
				if mode == "fast" {
					_ = mysqlWrite(server, []byte{1, 3}, 2)
					okSeq = 3
				}
				if err := mysqlWrite(server, []byte{0, 0, 0, 2, 0, 0, 0}, okSeq); err != nil {
					done <- err
					return
				}
				ping, seq, err := mysqlRead(server)
				if err != nil || seq != 0 || !bytes.Equal(ping, []byte{0x0e}) {
					done <- fmt.Errorf("expected only COM_PING")
					return
				}
				done <- mysqlWrite(server, []byte{0, 0, 0, 2, 0, 0, 0}, 1)
			}()
			err := probeMySQL(client, Binding{Username: "alice", Database: "db"}, "secret-test")
			expected := map[string]string{"tls-required": "AUTH_TLS_REQUIRED", "rejected": "AUTH_REJECTED", "unsupported": "AUTH_PLUGIN_UNSUPPORTED"}[mode]
			if expected == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != expected {
				t.Fatal("wrong failure", err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunBindingProbeSeparatesNetworkAndAuthentication(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		data := make([]byte, 128)
		_, _ = conn.Read(data)
		_, _ = io.WriteString(conn, "-WRONGPASS private-server-detail\r\n")
	}()
	b := Binding{ID: uuid.NewString(), Kind: "REDIS", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, CredentialID: uuid.NewString(), CredentialRevision: "1"}
	raw, _ := json.Marshal(bindingProbeInput{b, "secret-test"})
	var out bytes.Buffer
	if err := RunBindingProbe(bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	var result BindingProbe
	if err := StrictJSON(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.ApplicationNetwork.Reachable || result.Authentication.Authenticated || result.Authentication.ErrorCode != "AUTH_REJECTED" {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "secret-test") || strings.Contains(out.String(), "private-server-detail") {
		t.Fatal("secret leaked")
	}
	if err := RunBindingProbe(strings.NewReader(strings.Repeat("x", 17<<10)), io.Discard); err == nil {
		t.Fatal("unbounded input accepted")
	}
}

func TestMySQLProbeRejectsMalformedHandshake(t *testing.T) {
	for _, payload := range [][]byte{{0xff}, {10, 0}, mysqlGreeting("mysql_clear_password")} {
		client, server := net.Pipe()
		_ = client.SetDeadline(time.Now().Add(time.Second))
		go func(p []byte) { defer server.Close(); _ = mysqlWrite(server, p, 0) }(payload)
		if err := probeMySQL(client, Binding{Username: "user", Database: "db"}, "secret"); err == nil {
			t.Fatal("invalid handshake accepted")
		}
		client.Close()
	}
}
