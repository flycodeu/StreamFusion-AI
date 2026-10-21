package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func firewallRecoveryUnit(job string) string { return "sfops-fw-" + job }
func firewallRecoveryUnits(root, job, expiresAt string) (string, string, error) {
	expires, e := time.Parse(time.RFC3339, expiresAt)
	if e != nil || !ID.MatchString(job) || !ValidRoot(root) {
		return "", "", errors.New("FIREWALL_RECOVERY_RECORD_INVALID")
	}
	unit := firewallRecoveryUnit(job)
	service := fmt.Sprintf("[Unit]\nDescription=Ops firewall recovery\nStartLimitIntervalSec=60s\nStartLimitBurst=3\n[Service]\nType=oneshot\nRestart=on-failure\nRestartSec=10s\nExecStart=%s/executor target-firewall-recover --root %s --job %s\n", root, root, job)
	// The absolute persistent deadline survives target reboots. A relative-only
	// timer would incorrectly grant a fresh 120 seconds on every activation.
	timer := "[Unit]\nDescription=Ops firewall recovery timer\n[Timer]\nOnCalendar=" + expires.UTC().Format("2006-01-02 15:04:05 UTC") + "\nPersistent=true\nOnActiveSec=120s\nAccuracySec=1s\nUnit=" + unit + ".service\n[Install]\nWantedBy=timers.target\n"
	return service, timer, nil
}

func (x *executor) armFirewallRecovery(req Request, expiresAt string) error {
	unit := firewallRecoveryUnit(req.JobID)
	service, timer, e := firewallRecoveryUnits(x.root, req.JobID, expiresAt)
	if e != nil {
		return e
	}
	for name, body := range map[string]string{unit + ".service": service, unit + ".timer": timer} {
		if e := serviceFile(filepath.Join("/etc/systemd/system", name), []byte(body), 0, false); e != nil {
			return errors.New("FIREWALL_RECOVERY_PREPARATION_FAILED")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, e := x.softwareRunner(ctx, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return errors.New("FIREWALL_RECOVERY_UNAVAILABLE")
	}
	if _, e := x.softwareRunner(ctx, "/usr/bin/systemctl", "enable", "--now", unit+".timer"); e != nil {
		return errors.New("FIREWALL_RECOVERY_UNAVAILABLE")
	}
	if _, e := x.softwareRunner(ctx, "/usr/bin/systemctl", "is-active", "--quiet", unit+".timer"); e != nil {
		return errors.New("FIREWALL_RECOVERY_UNAVAILABLE")
	}
	return nil
}
func (x *executor) disarmFirewallRecovery(job string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unit := firewallRecoveryUnit(job)
	_, _ = x.softwareRunner(ctx, "/usr/bin/systemctl", "disable", "--now", unit+".timer")
	_, _ = x.softwareRunner(ctx, "/usr/bin/systemctl", "clean", "--what=state", unit+".timer")
	// Unit paths are derived exclusively from a validated UUID.
	_ = os.Remove(filepath.Join("/etc/systemd/system", unit+".timer"))
	_ = os.Remove(filepath.Join("/etc/systemd/system", unit+".service"))
	_, _ = x.softwareRunner(ctx, "/usr/bin/systemctl", "daemon-reload")
}
func (x *executor) rollbackFirewall(req Request, dir string, m firewallRecovery) Result {
	// A timer already queued by systemd may run after STATUS completed recovery.
	// Preserve the exact durable receipt so its hash still matches a pending ACK.
	var final Result
	if readJSON(filepath.Join(dir, "result.json"), &final) == nil {
		sameJob := final.ControllerID == req.ControllerID && final.TargetID == req.TargetID && final.JobID == req.JobID && final.InputHash == req.InputHash
		finished := final.Status == "FAILED" && final.Phase == "FIREWALL_ROLLED_BACK" || final.Status == "SUCCEEDED" && final.Phase == "FIREWALL_APPLIED"
		if sameJob && finished {
			return final
		}
	}
	r := softwareResult(req)
	r.Phase = "FIREWALL_RECOVERY_REQUIRED"
	s := x.firewallSnapshot()
	action, e := firewallRecoveryOperation(req.Firewall, m, s)
	if e != nil {
		return r
	}
	if action != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if _, e := x.softwareRunner(ctx, "/usr/sbin/ufw", "--force", action); e != nil {
			return r
		}
		s = x.firewallSnapshot()
	}
	if s.Code != "" || s.Hash != m.Before.Hash {
		return r
	}
	r.Status, r.Phase, r.ErrorCode, r.Firewall = "FAILED", "FIREWALL_ROLLED_BACK", "FIREWALL_CONFIRMATION_TIMEOUT", &s
	if atomicJSON(filepath.Join(dir, "result.json"), r) != nil {
		return softwareResult(req)
	}
	x.disarmFirewallRecovery(req.JobID)
	return r
}

// Keep recovery decisions testable without invoking a host firewall. A change
// by another administrator invalidates recovery instead of overwriting it.
func firewallRecoveryOperation(change *FirewallChange, m firewallRecovery, s FirewallSnapshot) (string, error) {
	if change == nil || (change.Operation != "ENABLE" && change.Operation != "DISABLE") || m.Committed || (m.Before.Status != "ACTIVE" && m.Before.Status != "INACTIVE") || !Hex.MatchString(m.Before.Hash) || !Hex.MatchString(m.Before.RulesHash) {
		return "", errors.New("FIREWALL_RECOVERY_RECORD_INVALID")
	}
	if s.Code != "" || s.RulesHash != m.Before.RulesHash {
		return "", errors.New("FIREWALL_RECOVERY_CONFLICT")
	}
	if s.Status == m.Before.Status {
		if s.Hash != m.Before.Hash {
			return "", errors.New("FIREWALL_RECOVERY_CONFLICT")
		}
		return "", nil
	}
	if (s.Status != "ACTIVE" && s.Status != "INACTIVE") || m.AfterHash != "" && s.Hash != m.AfterHash {
		return "", errors.New("FIREWALL_RECOVERY_CONFLICT")
	}
	if m.Before.Status == "ACTIVE" {
		return "enable", nil
	}
	return "disable", nil
}

// This is only invoked by the root-owned target timer, never through sudoers.
func RecoverTargetFirewall(root, job string) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || !ValidRoot(root) || !ID.MatchString(job) {
		return errors.New("FIREWALL_RECOVERY_IDENTITY_INVALID")
	}
	bin, e := os.Executable()
	if e != nil || bin != filepath.Join(root, "executor") || secureAncestors(bin) != nil {
		return errors.New("EXECUTOR_INSTALL_REQUIRED")
	}
	x := executor{root: root}
	if readJSON(filepath.Join(root, "owner.json"), &x.owner) != nil {
		return errors.New("TARGET_OWNER_INVALID")
	}
	lock, e := targetLock(root)
	if e != nil {
		return e
	}
	defer lock.Close()
	dir := filepath.Join(root, "jobs", job)
	var req Request
	var marker firewallRecovery
	if readJSON(filepath.Join(dir, "intent.json"), &req) != nil || req.JobID != job || req.ControllerID != x.owner.ControllerID || req.TargetID != x.owner.TargetID || req.Firewall == nil || readJSON(filepath.Join(dir, "firewall-recovery.json"), &marker) != nil {
		return errors.New("FIREWALL_RECOVERY_RECORD_INVALID")
	}
	frozen := req
	frozen.InputHash, frozen.ResultHash = "", ""
	if req.Action != "FIREWALL_CHANGE" || req.Firewall.Validate() != nil || Digest(frozen) != req.InputHash {
		return errors.New("FIREWALL_RECOVERY_RECORD_INVALID")
	}
	if marker.Committed {
		x.disarmFirewallRecovery(job)
		return nil
	}
	deadline, e := time.Parse(time.RFC3339, marker.ExpiresAt)
	if e != nil || time.Now().Before(deadline) {
		return errors.New("FIREWALL_RECOVERY_NOT_DUE")
	}
	if result := x.rollbackFirewall(req, dir, marker); result.Status != "FAILED" && result.Status != "SUCCEEDED" {
		return errors.New("FIREWALL_RECOVERY_CONFLICT")
	}
	x.disarmFirewallRecovery(job)
	return nil
}
