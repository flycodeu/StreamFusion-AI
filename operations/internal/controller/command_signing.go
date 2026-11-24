package controller

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"

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
	return req.Action == deploy.JenkinsUnlockReadAction ||
		req.Action == deploy.MavenConfigReadAction || req.Action == deploy.MavenConfigWriteAction ||
		strings.HasPrefix(req.Action, "APP_CONFIG_") || req.ProjectDeployment != nil ||
		req.Management != nil ||
		req.Installation != nil && (req.Installation.Definition.Commands != nil || req.Installation.PrepareHostTerminal) ||
		req.Uninstallation != nil && req.Uninstallation.Installation.Definition.Commands != nil ||
		req.NodeEnvironment != nil && req.NodeEnvironment.Definition != nil && req.NodeEnvironment.Definition.Commands != nil
}
