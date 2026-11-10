package deploy

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"os"
	"path/filepath"
	"time"
)

const CommandRecipeVersion = 9

func RequiredCommandRecipeVersion(d serviceinstall.Definition) int {
	if d.Management != nil {
		read, readErr := d.ManagementOperation("read-config")
		configure, configureErr := d.ManagementOperation("configure")
		if readErr == nil && read.ReadOnly && configureErr == nil && configure.Rebind {
			switch d.CatalogKey {
			case "jenkins", "postgresql", "gitlab", "prometheus", "nacos":
				// These maintained adapters read the actual state supplied to
				// readonly steps by v9. Earlier annotation templates retain v8.
				return 9
			}
		}
		if len(d.Management.ScriptPrelude) > 16384 {
			return 9
		}
		for _, operation := range d.Management.Operations {
			if operation.Rebind && serviceinstall.IsBuiltinCommand(d) {
				return 9
			}
			if serviceinstall.ManagementReplacesInstallationPorts(serviceinstall.ManagementRequest{Definition: d, OperationKey: operation.Key}) {
				for _, resource := range operation.Resources {
					if resource.Type == "PORT" && !resource.Release && serviceinstall.DeclaredInstallationPort(d, resource.Key, resource.Protocol) && (resource.Key != serviceinstall.WebPortKey(d) || resource.Protocol != "TCP") {
						return 9
					}
				}
			}
		}
		if d.Management.ScriptPrelude != "" {
			return 8
		}
		for _, operation := range d.Management.Operations {
			if operation.Rebind || len(operation.RollbackSteps) != 0 {
				return 8
			}
		}
	}
	if d.DefaultBindAddress == "0.0.0.0" {
		return 7
	}
	if d.Management != nil {
		for _, operation := range d.Management.Operations {
			if serviceinstall.ManagementReplacesWebPort(serviceinstall.ManagementRequest{Definition: d, OperationKey: operation.Key}) {
				return 7
			}
		}
	}
	for _, parameter := range d.Parameters {
		if parameter.Key == "PYTHON_BINARY" && parameter.Type == "PATH" {
			return 6
		}
	}
	if d.Web != nil || d.DefaultBindAddress != "" {
		return 5
	}
	if len(d.Parameters) != 0 || d.Management != nil || d.Commands != nil && !serviceinstall.IsBuiltinCommand(d) && len(d.Ports) != 0 {
		return 4
	}
	return 3
}

type commandTrust struct {
	ControllerID string `json:"controllerId"`
	TargetID     string `json:"targetId"`
	PublicKey    string `json:"publicKey"`
}

// CommandRequestDigest freezes commands, permissions, account, materials and
// deadline. Transport-only status/ack fields cannot change the authorized plan.
func CommandRequestDigest(req Request) string {
	req.CommandSignature, req.InputHash, req.ResultHash, req.Secrets = "", "", "", nil
	req.ResumeOffset = 0
	if req.SoftwareOperation != "" {
		req.Action = req.SoftwareOperation
	}
	return Digest(req)
}

func commandSigningMessage(req Request) []byte {
	return []byte("streamfusion-ops/command-plan/v1\n" + CommandRequestDigest(req))
}

func SignCommandRequest(req *Request, key ed25519.PrivateKey) error {
	d := commandDefinition(*req)
	if len(key) != ed25519.PrivateKeySize || len(req.Secrets) != 0 && d == nil {
		return errors.New("COMMAND_SIGNING_INPUT_INVALID")
	}
	if req.Management != nil && len(req.Secrets) != 0 {
		if err := validateManagementSecrets(*req, req.Secrets); err != nil {
			return err
		}
	} else if i := commandInstallation(*req); i != nil && !serviceinstall.IsBuiltinCommand(i.Definition) && len(req.Secrets) != 0 {
		if err := validateCustomParameterSecrets(*req, req.Secrets); err != nil {
			return err
		}
	}
	if (req.Action == "SOFTWARE_INSTALL_CHECK" || req.Action == "NODE_ENVIRONMENT_CHECK" || req.Action == "SOFTWARE_OPERATION_CHECK") && req.DeadlineAt == "" {
		// Leave room below the target's five-minute ceiling for clock skew and
		// RFC3339 second precision during read-only checks.
		req.DeadlineAt = time.Now().UTC().Add(4 * time.Minute).Format(time.RFC3339)
	}
	req.CommandSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, commandSigningMessage(*req)))
	return nil
}

func CommandPublicKeyFingerprint(encoded string) string {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return ""
	}
	hash := sha256.Sum256(key)
	return hex.EncodeToString(hash[:])
}

// TrustCommandController is intentionally a separate root-only command. The
// restricted sudo target-exec entry point must never enroll its own signer.
func TrustCommandController(root, controllerID, targetID, publicKey string) error {
	if CommandPublicKeyFingerprint(publicKey) == "" {
		return errors.New("COMMAND_PUBLIC_KEY_INVALID")
	}
	if err := VerifyTarget(root, controllerID, targetID); err != nil {
		return err
	}
	lock, err := targetLock(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	file := filepath.Join(root, "command-trust.json")
	wanted := commandTrust{controllerID, targetID, publicKey}
	var existing commandTrust
	if err := readJSON(file, &existing); err == nil {
		if secureAncestors(file) != nil || existing != wanted {
			return errors.New("COMMAND_TRUST_CONFLICT")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return errors.New("COMMAND_TRUST_DAMAGED")
	}
	return atomicJSON(file, wanted)
}

func verifyCommandSignature(req Request, encodedKey string) error {
	key, keyErr := base64.StdEncoding.DecodeString(encodedKey)
	sig, sigErr := base64.StdEncoding.DecodeString(req.CommandSignature)
	if keyErr != nil || len(key) != ed25519.PublicKeySize || sigErr != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, commandSigningMessage(req), sig) {
		return errors.New("COMMAND_SIGNATURE_INVALID")
	}
	if req.InputHash != "" && req.InputHash != CommandRequestDigest(req) {
		return errors.New("INPUT_HASH_MISMATCH")
	}
	return nil
}

func (x *executor) verifyCommandRequest(req Request) error {
	file := filepath.Join(x.root, "command-trust.json")
	var trust commandTrust
	if secureAncestors(file) != nil || readJSON(file, &trust) != nil || trust.ControllerID != req.ControllerID || trust.TargetID != req.TargetID {
		return errors.New("COMMAND_TRUST_REQUIRED")
	}
	if req.Management != nil {
		if err := validateManagementSecrets(req, x.secrets); err != nil {
			return err
		}
	} else if i := commandInstallation(req); i != nil && !serviceinstall.IsBuiltinCommand(i.Definition) {
		if err := validateCustomParameterSecrets(req, x.secrets); err != nil {
			return err
		}
	}
	if req.Action == "SOFTWARE_INSTALL_CHECK" || req.Action == "NODE_ENVIRONMENT_CHECK" || req.Action == "SOFTWARE_OPERATION_CHECK" {
		if err := verifyCommandCheckDeadline(req.DeadlineAt, time.Now()); err != nil {
			return err
		}
	}
	return verifyCommandSignature(req, trust.PublicKey)
}

func verifyCommandCheckDeadline(value string, now time.Time) error {
	deadline, err := time.Parse(time.RFC3339, value)
	if err != nil || now.After(deadline) || deadline.Sub(now) > 5*time.Minute {
		return errors.New("DEADLINE_EXCEEDED")
	}
	return nil
}

func validateCustomParameterSecrets(req Request, values map[string]string) error {
	i := commandInstallation(req)
	if req.Action == "SOFTWARE_INSTALL" || req.Action == "SOFTWARE_INSTALL_CHECK" || req.Action == "SOFTWARE_UNINSTALL" {
		return serviceinstall.ValidateSecrets(i.Definition, i.Config, values)
	}
	return serviceinstall.ValidateCredentialSubset(i.Config.CredentialReferences(), values)
}

func validateManagementSecrets(req Request, values map[string]string) error {
	if req.Action == "SOFTWARE_OPERATION" || req.Action == "SOFTWARE_OPERATION_CHECK" {
		return serviceinstall.ValidateCredentialValues(req.Management.CredentialReferences(), values)
	}
	return serviceinstall.ValidateCredentialSubset(req.Management.CredentialReferences(), values)
}

func (x *executor) commandTrustFingerprint() string {
	file := filepath.Join(x.root, "command-trust.json")
	var trust commandTrust
	if secureAncestors(file) != nil || readJSON(file, &trust) != nil || trust.ControllerID != x.owner.ControllerID || trust.TargetID != x.owner.TargetID {
		return ""
	}
	return CommandPublicKeyFingerprint(trust.PublicKey)
}
