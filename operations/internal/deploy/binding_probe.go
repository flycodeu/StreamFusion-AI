package deploy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ProbeLayer struct {
	Layer         string `json:"layer"`
	Executed      bool   `json:"executed"`
	Reachable     bool   `json:"reachable"`
	Authenticated bool   `json:"authenticated"`
	ErrorCode     string `json:"errorCode,omitempty"`
}

type BindingProbe struct {
	Reachable          bool        `json:"reachable"` // Legacy target-host TCP result.
	TargetHost         ProbeLayer  `json:"targetHost"`
	ApplicationNetwork *ProbeLayer `json:"applicationNetwork"`
	Authentication     *ProbeLayer `json:"authentication"`
	ReleaseID          string      `json:"releaseId,omitempty"`
	CredentialRevision string      `json:"credentialRevision,omitempty"`
	ObservedAt         string      `json:"observedAt"`
}

type bindingProbeInput struct {
	Binding  Binding `json:"binding"`
	Password string  `json:"password"`
}

// RunBindingProbe is a single bounded, read-only protocol probe. It has no
// filesystem, executor-owner, Docker or shell authority. Password travels only
// through stdin; returned errors are fixed codes, never remote error messages.
func RunBindingProbe(input io.Reader, output io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(input, 16<<10+1))
	if err != nil || len(raw) > 16<<10 {
		return errors.New("PROBE_INPUT_INVALID")
	}
	var in bindingProbeInput
	if StrictJSON(raw, &in) != nil || in.Binding.Validate() != nil || len(in.Password) > 1024 {
		return errors.New("PROBE_INPUT_INVALID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(in.Binding.Host, strconv.Itoa(in.Binding.Port)))
	network := ProbeLayer{Layer: "APPLICATION_NETWORK_TCP", Executed: true, Reachable: err == nil}
	auth := ProbeLayer{Layer: "AUTHENTICATION", Executed: false}
	if err != nil {
		network.ErrorCode = "TCP_UNREACHABLE"
	} else {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
		auth.Executed, auth.Reachable = true, true
		if in.Binding.Kind == "REDIS" {
			err = probeRedis(conn, in.Binding, in.Password)
		} else {
			err = probeMySQL(conn, in.Binding, in.Password)
		}
		auth.Authenticated = err == nil
		if err != nil {
			auth.ErrorCode = err.Error()
		}
	}
	return json.NewEncoder(output).Encode(BindingProbe{ApplicationNetwork: &network, Authentication: &auth, CredentialRevision: in.Binding.CredentialRevision, ObservedAt: stamp()})
}

func probeRedis(conn net.Conn, b Binding, password string) error {
	reader := bufio.NewReaderSize(conn, 4096)
	command := func(parts ...string) error {
		var request strings.Builder
		fmt.Fprintf(&request, "*%d\r\n", len(parts))
		for _, value := range parts {
			fmt.Fprintf(&request, "$%d\r\n%s\r\n", len(value), value)
		}
		if _, err := io.WriteString(conn, request.String()); err != nil {
			return errors.New("AUTH_PROTOCOL_IO")
		}
		line, err := reader.ReadSlice('\n')
		if err != nil || len(line) < 3 || len(line) > 4096 || !bytes.HasSuffix(line, []byte("\r\n")) {
			return errors.New("AUTH_PROTOCOL_INVALID")
		}
		if line[0] == '-' {
			return errors.New("AUTH_REJECTED")
		}
		if line[0] != '+' || (string(line) != "+OK\r\n" && string(line) != "+PONG\r\n") {
			return errors.New("AUTH_PROTOCOL_INVALID")
		}
		return nil
	}
	if b.CredentialID != "" {
		parts := []string{"AUTH"}
		if b.Username != "" {
			parts = append(parts, b.Username)
		}
		if err := command(append(parts, password)...); err != nil {
			return err
		}
	}
	if b.Database != "" {
		db, err := strconv.Atoi(b.Database)
		if err != nil || db < 0 || db > 255 {
			return errors.New("REDIS_DATABASE_UNSUPPORTED")
		}
		if err := command("SELECT", strconv.Itoa(db)); err != nil {
			return err
		}
	}
	return command("PING")
}

func mysqlRead(conn net.Conn) ([]byte, byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, 0, errors.New("AUTH_PROTOCOL_IO")
	}
	n := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if n < 1 || n > 32<<10 {
		return nil, 0, errors.New("AUTH_PROTOCOL_INVALID")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, 0, errors.New("AUTH_PROTOCOL_IO")
	}
	return payload, header[3], nil
}

func mysqlWrite(conn net.Conn, payload []byte, sequence byte) error {
	header := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), sequence}
	if _, err := conn.Write(append(header, payload...)); err != nil {
		return errors.New("AUTH_PROTOCOL_IO")
	}
	return nil
}

func mysqlScramble(plugin, password string, salt []byte) ([]byte, error) {
	if password == "" {
		return nil, nil
	}
	var stage1, stage3 []byte
	switch plugin {
	case "mysql_native_password":
		a := sha1.Sum([]byte(password))
		b := sha1.Sum(a[:])
		c := sha1.Sum(append(append([]byte{}, salt...), b[:]...))
		stage1, stage3 = a[:], c[:]
	case "caching_sha2_password":
		a := sha256.Sum256([]byte(password))
		b := sha256.Sum256(a[:])
		c := sha256.Sum256(append(append([]byte{}, b[:]...), salt...))
		stage1, stage3 = a[:], c[:]
	default:
		return nil, errors.New("AUTH_PLUGIN_UNSUPPORTED")
	}
	result := make([]byte, len(stage1))
	for i := range result {
		result[i] = stage1[i] ^ stage3[i]
	}
	return result, nil
}

func probeMySQL(conn net.Conn, b Binding, password string) error {
	greeting, sequence, err := mysqlRead(conn)
	if err != nil {
		return err
	}
	if sequence != 0 || len(greeting) < 2 || greeting[0] != 10 {
		return errors.New("AUTH_PROTOCOL_INVALID")
	}
	versionEnd := bytes.IndexByte(greeting[1:], 0)
	if versionEnd < 0 {
		return errors.New("AUTH_PROTOCOL_INVALID")
	}
	pos := versionEnd + 2
	if len(greeting) < pos+31 {
		return errors.New("AUTH_PROTOCOL_INVALID")
	}
	salt := append([]byte{}, greeting[pos+4:pos+12]...)
	capabilities := uint32(binary.LittleEndian.Uint16(greeting[pos+13:pos+15])) | uint32(binary.LittleEndian.Uint16(greeting[pos+18:pos+20]))<<16
	authLength := int(greeting[pos+20])
	pos += 31
	if capabilities&0x8000 == 0 || authLength < 20 || len(greeting) < pos+13 {
		return errors.New("AUTH_PROTOCOL_UNSUPPORTED")
	}
	salt = append(salt, greeting[pos:pos+12]...)
	pos += 13
	plugin := "mysql_native_password"
	if capabilities&0x80000 != 0 && len(greeting) > pos {
		plugin = strings.TrimRight(string(greeting[pos:]), "\x00")
	}
	scramble, err := mysqlScramble(plugin, password, salt)
	if err != nil {
		return err
	}
	flags := uint32(0x00000001 | 0x00000200 | 0x00008000 | 0x00080000 | 0x00000008)
	if capabilities&0x200 == 0 || capabilities&0x80000 == 0 {
		return errors.New("AUTH_PROTOCOL_UNSUPPORTED")
	}
	payload := make([]byte, 32)
	binary.LittleEndian.PutUint32(payload, flags)
	binary.LittleEndian.PutUint32(payload[4:], 32<<10)
	payload[8] = 45
	payload = append(payload, append([]byte(b.Username), 0)...)
	payload = append(payload, byte(len(scramble)))
	payload = append(payload, scramble...)
	payload = append(payload, append([]byte(b.Database), 0)...)
	payload = append(payload, append([]byte(plugin), 0)...)
	if err := mysqlWrite(conn, payload, 1); err != nil {
		return err
	}
	expectedSequence := byte(2)
	for i := 0; i < 4; i++ {
		response, seq, err := mysqlRead(conn)
		if err != nil {
			return err
		}
		if seq != expectedSequence {
			return errors.New("AUTH_PROTOCOL_INVALID")
		}
		expectedSequence = seq + 1
		switch response[0] {
		case 0:
			if err := mysqlWrite(conn, []byte{0x0e}, 0); err != nil {
				return err
			} // COM_PING, no business SQL.
			pong, pongSeq, err := mysqlRead(conn)
			if err != nil || pongSeq != 1 || pong[0] != 0 {
				return errors.New("AUTH_PING_FAILED")
			}
			return nil
		case 0xff:
			return errors.New("AUTH_REJECTED")
		case 0xfe:
			end := bytes.IndexByte(response[1:], 0)
			if end < 0 || len(response) < end+3 {
				return errors.New("AUTH_PROTOCOL_INVALID")
			}
			plugin = string(response[1 : end+1])
			scramble, err := mysqlScramble(plugin, password, bytes.TrimRight(response[end+2:], "\x00"))
			if err != nil {
				return err
			}
			if err := mysqlWrite(conn, scramble, seq+1); err != nil {
				return err
			}
			expectedSequence++
		case 1:
			if len(response) != 2 {
				return errors.New("AUTH_PROTOCOL_INVALID")
			}
			if response[1] == 4 {
				return errors.New("AUTH_TLS_REQUIRED")
			} // Never send cleartext or trust an unpinned RSA key.
			if response[1] != 3 {
				return errors.New("AUTH_PROTOCOL_UNSUPPORTED")
			}
		default:
			return errors.New("AUTH_PROTOCOL_INVALID")
		}
	}
	return errors.New("AUTH_PROTOCOL_LIMIT")
}

func (x *executor) bindingProbe(req Request) (BindingProbe, error) {
	b := *req.Binding
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(b.Host, strconv.Itoa(b.Port)))
	result := BindingProbe{Reachable: err == nil, TargetHost: ProbeLayer{Layer: "TCP", Executed: true, Reachable: err == nil}, ObservedAt: stamp()}
	if conn != nil {
		conn.Close()
	}
	if err != nil {
		result.TargetHost.ErrorCode = "TCP_UNREACHABLE"
	}
	if req.Candidate == nil {
		return result, nil
	} // Old host-only callers retain their contract.
	if validateRelease(req.Candidate) != nil || req.Candidate.Config.Recipe() != "java-compose-v1" {
		return result, errors.New("PROBE_RELEASE_INVALID")
	}
	matched := false
	for _, binding := range req.Candidate.Bindings {
		if Digest(binding) == Digest(b) {
			matched = true
		}
	}
	if !matched {
		return result, errors.New("PROBE_BINDING_NOT_IN_RELEASE")
	}
	lock, err := x.handoffLock()
	if err != nil {
		return result, err
	}
	defer lock.Close()
	actual, err := x.inspected(*req.Candidate)
	if err != nil {
		return result, err
	}
	name := "sfops-probe-" + strings.ReplaceAll(b.ID, "-", "") + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	defer x.docker("rm", "-f", name)
	raw, _ := json.Marshal(bindingProbeInput{Binding: b, Password: x.secrets[b.ID]})
	args := []string{"--host", "unix://" + x.owner.DockerSocket, "run", "--rm", "-i", "--pull", "never", "--no-healthcheck", "--name", name, "--network", "container:" + actual.ContainerID, "--read-only", "--user", "10001:10001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--pids-limit", "32", "--memory", "64m", "--cpus", "0.25", "--mount", "type=bind,source=" + filepath.Join(x.root, "executor") + ",target=/sfops-probe,readonly", "--entrypoint", "/sfops-probe", actual.ImageID, "binding-probe"}
	if err := secureAncestors(x.owner.DockerBinary); err != nil {
		return result, err
	}
	runCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	cmd := exec.CommandContext(runCtx, x.owner.DockerBinary, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root", "DOCKER_CONFIG=" + filepath.Join(x.root, "docker-config")}
	cmd.Stdin = bytes.NewReader(raw)
	out, stderr := &cappedBuffer{limit: 16 << 10}, &cappedBuffer{limit: 1024}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		return result, errors.New("APPLICATION_PROBE_UNAVAILABLE")
	}
	var observed BindingProbe
	if StrictJSON(out.Bytes(), &observed) != nil || observed.ApplicationNetwork == nil || observed.Authentication == nil {
		return result, errors.New("APPLICATION_PROBE_INVALID")
	}
	result.ApplicationNetwork, result.Authentication = observed.ApplicationNetwork, observed.Authentication
	result.ReleaseID, result.CredentialRevision = req.Candidate.ID, b.CredentialRevision
	return result, nil
}
