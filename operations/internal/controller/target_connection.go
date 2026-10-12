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
	key, e := s.secret(t.CredentialID, t.Revision, "SSH_KEY")
	if e != nil {
		return e
	}
	signer, e := ssh.ParsePrivateKey(key)
	if e != nil {
		return errors.New("SSH_KEY_INVALID_OR_ENCRYPTED")
	}
	config := &ssh.ClientConfig{User: t.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, Timeout: 5 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != t.Fingerprint {
			return errors.New("SSH_HOST_KEY_CHANGED")
		}
		return nil
	}}
	conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(t.Address, strconv.Itoa(t.Port)))
	if e != nil {
		return e
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	clientConn, chans, reqs, e := ssh.NewClientConn(conn, t.Address, config)
	if e != nil {
		return e
	}
	client := ssh.NewClient(clientConn, chans, reqs)
	defer client.Close()
	session, e := client.NewSession()
	if e != nil {
		return e
	}
	defer session.Close()
	session.Stdin = input
	session.Stdout = out
	session.Stderr = errOut
	// installRoot is a strict absolute-path allowlist without shell metacharacters.
	if e = session.Run("/usr/bin/sudo -n '" + t.InstallRoot + "/executor' target-exec"); e != nil {
		return fmt.Errorf("SSH_EXECUTOR_FAILED: %w", e)
	}
	return nil
}
