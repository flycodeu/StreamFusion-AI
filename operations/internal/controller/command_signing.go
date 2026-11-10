package controller

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// Domain-separated from credential encryption; only the public key leaves Ops.
func (s *Store) commandSigningKey() ed25519.PrivateKey {
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte("streamfusion-ops/command-signing/v1"))
	return ed25519.NewKeyFromSeed(mac.Sum(nil))
}
func (s *Store) commandPublicKey() string {
	return base64.StdEncoding.EncodeToString(s.commandSigningKey().Public().(ed25519.PublicKey))
}
func commandRequest(req deploy.Request) bool {
	return req.Management != nil || req.Installation != nil && req.Installation.Definition.Commands != nil || req.Uninstallation != nil && req.Uninstallation.Installation.Definition.Commands != nil || req.NodeEnvironment != nil && req.NodeEnvironment.Definition != nil && req.NodeEnvironment.Definition.Commands != nil
}
