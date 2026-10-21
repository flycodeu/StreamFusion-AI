package deploy

import (
	"errors"
	"regexp"
	"strings"
)

// SoftwareRequest freezes a concrete lifecycle object. Locator is never a shell
// fragment: it is an explicit service, full container ID, or supported tool key.
type SoftwareRequest struct {
	Ownership       *SoftwareOwnership `json:"ownership,omitempty"`
	ID              string             `json:"id"`
	Version         int64              `json:"version"`
	Kind            string             `json:"kind"`
	Locator         string             `json:"locator"`
	IdentityHash    string             `json:"identityHash"`
	SoftwareKey     string             `json:"softwareKey"`
	ExpectedVersion string             `json:"expectedVersion,omitempty"`
}

type SoftwareOwnership struct {
	RuntimeMode  string `json:"runtimeMode,omitempty"`
	BinaryPath   string `json:"binaryPath,omitempty"`
	BinaryHash   string `json:"binaryHash,omitempty"`
	ControllerID string `json:"controllerId"`
	TargetID     string `json:"targetId"`
	SoftwareID   string `json:"softwareId"`
	JobID        string `json:"jobId"`
	ConfigHash   string `json:"configHash"`
	ImageID      string `json:"imageId"`
}

type SoftwareObservation struct {
	SoftwareMetadata
	Kind         string `json:"kind"`
	Locator      string `json:"locator"`
	IdentityHash string `json:"identityHash"`
	State        string `json:"state"`
	Version      string `json:"version"`
	ObservedAt   string `json:"observedAt"`
	Log          string `json:"log,omitempty"`
	// Truncated denotes a bounded recent tail, not a complete historical log.
	Truncated bool `json:"truncated"`
}

var softwareUnitPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,95}\.service$`)
var softwareContainerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var nativeBinaryPathPattern = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

var protectedSoftwareUnits = map[string]bool{
	"systemd": true, "dbus": true, "ssh": true, "sshd": true,
	"docker": true, "containerd": true, "cron": true, "crond": true,
	"networking": true, "networkmanager": true, "polkit": true,
	"getty": true, "serial-getty": true, "rescue": true, "emergency": true,
	"streamfusion-ops": true, "udev": true, "auditd": true,
}

func (s SoftwareRequest) Validate(observation bool) error {
	if o := s.Ownership; o != nil {
		if o.SoftwareID != s.ID || !ID.MatchString(o.SoftwareID) || !ID.MatchString(o.ControllerID) || !ID.MatchString(o.TargetID) || !ID.MatchString(o.JobID) || !Hex.MatchString(o.ConfigHash) {
			return errors.New("SOFTWARE_OWNERSHIP_INVALID")
		}
		if o.RuntimeMode == "NATIVE" {
			if (s.Kind != "SYSTEMD" && s.Kind != "TOOL") || !Hex.MatchString(o.BinaryHash) || !validNativeBinaryPath(o.BinaryPath) {
				return errors.New("SOFTWARE_NATIVE_OWNERSHIP_INVALID")
			}
		} else if (s.Kind != "DOCKER" && s.Kind != "TOOL") || !strings.HasPrefix(o.ImageID, "sha256:") || !Hex.MatchString(strings.TrimPrefix(o.ImageID, "sha256:")) {
			return errors.New("SOFTWARE_OWNERSHIP_INVALID")
		}
	}
	switch s.Kind {
	case "SYSTEMD":
		name := strings.ToLower(strings.TrimSuffix(s.Locator, ".service"))
		if !softwareUnitPattern.MatchString(s.Locator) || protectedSoftwareUnits[name] || strings.HasPrefix(name, "systemd-") || strings.HasPrefix(name, "streamfusion-ops") || strings.HasPrefix(name, "sfops-") && !(s.Ownership != nil && s.Ownership.RuntimeMode == "NATIVE" && s.Locator == "sfops-service-"+s.ID+".service") {
			return errors.New("SOFTWARE_UNIT_FORBIDDEN")
		}
	case "DOCKER":
		if !Hex.MatchString(s.Locator) && (!observation || !softwareContainerPattern.MatchString(s.Locator)) {
			return errors.New("SOFTWARE_CONTAINER_ID_REQUIRED")
		}
	case "TOOL":
		if s.Ownership != nil {
			if s.Ownership.RuntimeMode == "NATIVE" && s.Locator != s.Ownership.BinaryPath || s.Ownership.RuntimeMode != "NATIVE" && s.Locator != "image:"+s.Ownership.ImageID {
				return errors.New("SOFTWARE_TOOL_IDENTITY_INVALID")
			}
		} else if s.Locator != "java" && s.Locator != "python" && s.Locator != "ffmpeg" {
			return errors.New("SOFTWARE_TOOL_UNSUPPORTED")
		}
	default:
		return errors.New("SOFTWARE_KIND_INVALID")
	}
	if !observation && (!ID.MatchString(s.ID) || s.Version < 1 || !Hex.MatchString(s.IdentityHash) || s.Kind == "TOOL" && s.Ownership == nil) {
		return errors.New("SOFTWARE_IDENTITY_INVALID")
	}
	if len(s.SoftwareKey) > 64 || strings.ContainsAny(s.SoftwareKey, "\r\n") || len(s.ExpectedVersion) > 128 || strings.ContainsAny(s.ExpectedVersion, "\r\n") {
		return errors.New("SOFTWARE_METADATA_INVALID")
	}
	return nil
}

func softwareMutation(action string) bool {
	return action == "SOFTWARE_CONFIGURE" || action == "SOFTWARE_INSTALL" || action == "SOFTWARE_ADOPT" || action == "SOFTWARE_START" || action == "SOFTWARE_STOP" || action == "SOFTWARE_UNMANAGE"
}

func validNativeBinaryPath(value string) bool {
	if !nativeBinaryPathPattern.MatchString(value) || strings.Contains(value, "..") || len(value) > 512 {
		return false
	}
	for _, prefix := range []string{"/opt/", "/srv/", "/var/lib/", "/data/", "/mnt/"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
