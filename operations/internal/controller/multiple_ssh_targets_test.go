package controller

import (
	"fmt"
	"testing"
)

func TestMultipleSSHTargetsRemainBounded(t *testing.T) {
	_, b := setup(t)
	b.login()
	credential := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "test SSH", "kind": "SSH_KEY", "secret": "test-only-key"}, 201))
	credentialID := credential["id"].(string)
	newTarget := func(n int, expected int) {
		t.Helper()
		b.call("POST", "/targets", map[string]any{
			"name": "ssh-" + fmt.Sprint(n), "kind": "SSH", "address": fmt.Sprintf("192.0.2.%d", n), "sshPort": 22,
			"sshUser": "ops", "hostFingerprint": "SHA256:test", "credentialId": credentialID,
			"credentialRevision": "1", "installRoot": "/opt/preview",
		}, expected)
	}
	newTarget(1, 201)
	newTarget(2, 201)
	b.call("POST", "/targets", map[string]any{"name": "duplicate", "kind": "SSH", "address": "192.0.2.2", "sshPort": 22, "sshUser": "ops", "hostFingerprint": "SHA256:test", "credentialId": credentialID, "credentialRevision": "1", "installRoot": "/opt/preview"}, 409)
	for n := 3; n <= 16; n++ {
		newTarget(n, 201)
	}
	newTarget(17, 409)
	b.call("POST", "/targets", map[string]any{"name": "local", "installRoot": "/opt/local-target"}, 201)
	b.call("POST", "/targets", map[string]any{"name": "local-2", "installRoot": "/opt/local-target-2"}, 409)
}
