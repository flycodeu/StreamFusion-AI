package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

type sshTarget struct {
	Kind         string `json:"kind"`
	Address      string `json:"address"`
	Port         int    `json:"sshPort"`
	User         string `json:"sshUser"`
	Fingerprint  string `json:"hostFingerprint"`
	CredentialID string `json:"credentialId"`
	Revision     string `json:"credentialRevision"`
}

func (s *Store) secret(id, revision, kind string) ([]byte, error) {
	var material string
	e := s.DB.QueryRow("SELECT material FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND kind=? AND revoked_at IS NULL", id, revision, kind).Scan(&material)
	if e != nil {
		return nil, errors.New("CREDENTIAL_UNAVAILABLE")
	}
	rev, e := strconv.Atoi(revision)
	if e != nil {
		return nil, e
	}
	if material != id+"-"+revision+".enc" {
		return nil, errors.New("CREDENTIAL_PATH_INVALID")
	}
	data, e := os.ReadFile(s.credentialFile(material))
	if e != nil {
		return nil, e
	}
	return unseal(s.Key, id, rev, data)
}
func (s *Store) sshExecute(ctx context.Context, t target, input io.Reader, out, errOut io.Writer) error {
	client, closeClient, e := s.connectSSH(ctx, t)
	if e != nil {
		return e
	}
	defer closeClient()
	diagnostic := &limitedOutput{limit: 1024}
	if e = runSSHCommand(client, executorSSHCommand(t), input, out, io.MultiWriter(errOut, diagnostic)); e != nil {
		if ctx.Err() != nil {
			return classifyExecutorInvocation(ctx.Err(), "")
		}
		return classifyExecutorInvocation(e, diagnostic.String())
	}
	return nil
}

func executorSSHCommand(t target) string {
	command := "'" + t.InstallRoot + "/executor' target-exec"
	if t.User == "root" {
		return command
	}
	return "/usr/bin/sudo -n " + command
}

func (s *Store) connectSSH(ctx context.Context, t target) (*ssh.Client, func(), error) {
	var kind string
	if e := s.DB.QueryRow("SELECT kind FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND revoked_at IS NULL", t.CredentialID, t.Revision).Scan(&kind); e != nil {
		return nil, nil, errors.New("CREDENTIAL_UNAVAILABLE")
	}
	key, e := s.secret(t.CredentialID, t.Revision, kind)
	if e != nil {
		return nil, nil, e
	}
	var auth ssh.AuthMethod
	switch kind {
	case "SSH_KEY":
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, nil, errors.New("SSH_KEY_INVALID_OR_ENCRYPTED")
		}
		auth = ssh.PublicKeys(signer)
	case "PASSWORD":
		auth = ssh.Password(string(key))
	default:
		return nil, nil, errors.New("SSH_CREDENTIAL_KIND_INVALID")
	}
	config := &ssh.ClientConfig{User: t.User, Auth: []ssh.AuthMethod{auth}, Timeout: 5 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != t.Fingerprint {
			return errors.New("SSH_HOST_KEY_CHANGED")
		}
		return nil
	}}
	conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(t.Address, strconv.Itoa(t.Port)))
	if e != nil {
		return nil, nil, e
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	clientConn, chans, reqs, e := ssh.NewClientConn(conn, t.Address, config)
	if e != nil {
		stop()
		conn.Close()
		return nil, nil, e
	}
	client := ssh.NewClient(clientConn, chans, reqs)
	return client, func() { stop(); client.Close(); conn.Close() }, nil
}

func runSSHCommand(client *ssh.Client, command string, input io.Reader, out, errOut io.Writer) error {
	session, e := client.NewSession()
	if e != nil {
		return e
	}
	defer session.Close()
	session.Stdin = input
	session.Stdout = out
	session.Stderr = errOut
	if e = session.Run(command); e != nil {
		return fmt.Errorf("SSH_EXECUTOR_FAILED: %w", e)
	}
	return nil
}

// Discovery deliberately stops before user authentication. A returned public-key
// fingerprint is only a candidate; every authenticated connection pins it later.
func discoverSSHKey(ctx context.Context, address string, port int) (string, error) {
	conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
	if e != nil {
		return "", e
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	var fingerprint string
	_, _, _, e = ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: "host-key-discovery", Timeout: 5 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(key)
		return errors.New("SSH_KEY_DISCOVERY_COMPLETE")
	}})
	if fingerprint == "" {
		return "", e
	}
	return fingerprint, nil
}
