package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type firewallRecovery struct {
	After     FirewallSnapshot `json:"after"`
	Before    FirewallSnapshot `json:"before"`
	AfterHash string           `json:"afterHash"`
	ExpiresAt string           `json:"expiresAt"`
	Committed bool             `json:"committed"`
}

func (x *executor) executeFirewall(req Request, out io.Writer) error {
	if req.Action == "FIREWALL_OBSERVE" {
		return json.NewEncoder(out).Encode(x.firewallSnapshot())
	}
	if req.Firewall == nil || req.Firewall.Validate() != nil || req.Software != nil || req.Installation != nil || req.Plugin != nil || req.Candidate != nil || req.Baseline != nil || req.Binding != nil || req.Reconfiguration != nil {
		return errors.New("FIREWALL_INPUT_INVALID")
	}
	if req.Action == "FIREWALL_CHECK" {
		s, e := x.firewallCheck(*req.Firewall)
		code := ""
		if e != nil {
			code = e.Error()
		}
		return json.NewEncoder(out).Encode(struct {
			Snapshot FirewallSnapshot `json:"snapshot"`
			Code     string           `json:"code"`
		}{s, code})
	}
	frozen := req
	frozen.Action, frozen.InputHash, frozen.ResultHash = "FIREWALL_CHANGE", "", ""
	if !ID.MatchString(req.JobID) || Digest(frozen) != req.InputHash {
		return errors.New("FIREWALL_INPUT_HASH_INVALID")
	}
	lock, e := targetLock(x.root)
	if e != nil {
		return e
	}
	defer lock.Close()
	dir := filepath.Join(x.root, "jobs", req.JobID)
	intentFile := filepath.Join(dir, "intent.json")
	resultFile := filepath.Join(dir, "result.json")
	var intent Request
	if e = readJSON(intentFile, &intent); e == nil {
		if intent.InputHash != req.InputHash || intent.Firewall == nil {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		var result Result
		if readJSON(resultFile, &result) != nil {
			result = softwareResult(intent)
			result.Phase = "FIREWALL_UNCONFIRMED"
		}
		if req.Action == "ACK" {
			if result.Status != "SUCCEEDED" && result.Status != "FAILED" || req.ResultHash != Digest(result) {
				return errors.New("ACK_RESULT_MISMATCH")
			}
			if e = atomicJSON(filepath.Join(dir, "ack.json"), map[string]string{"ResultHash": req.ResultHash}); e != nil {
				return e
			}
			var pending Request
			if readJSON(filepath.Join(x.root, "pending.json"), &pending) == nil && pending.JobID == req.JobID && pending.InputHash == req.InputHash {
				if e = os.Remove(filepath.Join(x.root, "pending.json")); e != nil {
					return e
				}
			}
			if req.Firewall.Operation == "DELETE" && result.Status == "SUCCEEDED" || req.Firewall.Operation == "ADD" && result.Phase == "FIREWALL_NOT_APPLIED" {
				_ = os.Remove(filepath.Join(x.root, "firewall", "rules", req.Firewall.Rule.ID+".json"))
			}
			result.Ack = true
			return json.NewEncoder(out).Encode(result)
		}
		if result.Status == "UNKNOWN" {
			if req.Action == "FIREWALL_CONFIRM" {
				result = x.confirmFirewall(intent, dir)
			} else if req.Action == "STATUS" {
				result = x.reconcileFirewall(intent, dir)
			}
		}
		return json.NewEncoder(out).Encode(result)
	} else if !os.IsNotExist(e) {
		return errors.New("FIREWALL_INTENT_DAMAGED")
	}
	if req.Action != "FIREWALL_CHANGE" {
		return errors.New("FIREWALL_NOT_STARTED")
	}
	if _, e = os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(e) {
		return errors.New("TARGET_PENDING")
	}
	deadline, e := time.Parse(time.RFC3339, req.DeadlineAt)
	if e != nil || time.Now().After(deadline) {
		return errors.New("DEADLINE_EXCEEDED")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e = atomicJSON(filepath.Join(x.root, "pending.json"), req); e != nil {
		return e
	}
	if e = atomicJSON(intentFile, req); e != nil {
		return e
	}
	result := softwareResult(req)
	result.Phase = "FIREWALL_PREFLIGHT"
	s, err := x.firewallCheck(*req.Firewall)
	if err == nil {
		result, err = x.applyFirewall(req, dir, s)
	}
	if err != nil {
		result.Status, result.Phase, result.ErrorCode = "FAILED", "FIREWALL_NOT_APPLIED", err.Error()
		var marker firewallRecovery
		if readJSON(filepath.Join(dir, "firewall-recovery.json"), &marker) == nil {
			marker.Committed = true
			if atomicJSON(filepath.Join(dir, "firewall-recovery.json"), marker) != nil {
				return errors.New("FIREWALL_RECOVERY_RECORD_UNCONFIRMED")
			}
			x.disarmFirewallRecovery(req.JobID)
		}
	}
	if e = atomicJSON(resultFile, result); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}

func (x *executor) applyFirewall(req Request, dir string, before FirewallSnapshot) (Result, error) {
	result := softwareResult(req)
	result.Phase = "FIREWALL_UNCONFIRMED"
	c := req.Firewall
	global := c.Operation == "ENABLE" || c.Operation == "DISABLE"
	if global {
		if c.Operation == "ENABLE" && (c.Remote || before.ManagementSource != "") && !firewallManagementAllowed(before) {
			return result, errors.New("SSH_ALLOW_RULE_REQUIRED")
		}
		if c.Operation == "ENABLE" && (c.Remote || before.ManagementSource != "") && firewallSSHBlocked(before) {
			return result, errors.New("SSH_RULE_CONFLICT")
		}
		marker := firewallRecovery{Before: FirewallSnapshot{Status: before.Status, Hash: before.Hash, RulesHash: before.RulesHash}, ExpiresAt: time.Now().UTC().Add(120 * time.Second).Format(time.RFC3339)}
		if e := atomicJSON(filepath.Join(dir, "firewall-recovery.json"), marker); e != nil {
			return result, e
		}
		if e := x.armFirewallRecovery(req, marker.ExpiresAt); e != nil {
			return result, e
		}
	} else {
		if c.Operation == "ADD" && firewallRulePresent(before, req.ControllerID, c.Rule) {
			return result, errors.New("FIREWALL_RULE_ALREADY_PRESENT")
		}
		if e := os.MkdirAll(filepath.Join(x.root, "firewall", "rules"), 0700); e != nil {
			return result, e
		}
		if c.Operation == "ADD" {
			if e := atomicJSON(filepath.Join(x.root, "firewall", "rules", c.Rule.ID+".json"), c.Rule); e != nil {
				return result, e
			}
		}
	}
	if e := atomicJSON(filepath.Join(dir, "firewall-command.json"), req.Firewall); e != nil {
		return result, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	args := []string{"--force", strings.ToLower(c.Operation)}
	if !global {
		args = firewallArguments(c.Rule, firewallTag(req.ControllerID, c.Rule.ID), c.Operation == "DELETE")
	}
	_, commandErr := softwareCommand(ctx, x.softwareRunner, "/usr/sbin/ufw", args...)
	after := x.firewallSnapshot()
	if commandErr != nil || !FirewallEffectMatches(*c, after, req.ControllerID) {
		return result, nil
	}
	if global {
		var marker firewallRecovery
		if readJSON(filepath.Join(dir, "firewall-recovery.json"), &marker) != nil {
			return result, nil
		}
		marker.AfterHash = after.Hash
		marker.After = after
		if atomicJSON(filepath.Join(dir, "firewall-recovery.json"), marker) != nil {
			return result, nil
		}
		result.FirewallConfirmBy = marker.ExpiresAt
		result.Phase = "FIREWALL_AWAITING_CONFIRM"
		result.Firewall = &after
		return result, nil
	}
	result.Status, result.Phase, result.Firewall = "SUCCEEDED", "FIREWALL_APPLIED", &after
	return result, nil
}

func FirewallEffectMatches(c FirewallChange, s FirewallSnapshot, controller string) bool {
	if s.Code != "" {
		return false
	}
	switch c.Operation {
	case "ENABLE":
		return s.Status == "ACTIVE"
	case "DISABLE":
		return s.Status == "INACTIVE"
	case "ADD":
		return firewallRulePresent(s, controller, c.Rule)
	case "DELETE":
		return !strings.Contains(s.AddedRules, firewallTag(controller, c.Rule.ID))
	}
	return false
}
func firewallManagementAllowed(s FirewallSnapshot) bool {
	peer, e := netipAddress(s.ManagementSource)
	if e != nil {
		return false
	}
	for _, r := range s.ManagedRules {
		prefix, e := parseFirewallPrefix(r.Source)
		if e == nil && r.Action == "allow" && r.Protocol == "tcp" && r.Start <= s.ManagementPort && r.End >= s.ManagementPort && prefix.Contains(peer) {
			return true
		}
	}
	return false
}
func (x *executor) confirmFirewall(req Request, dir string) Result {
	result := softwareResult(req)
	result.Phase = "FIREWALL_UNCONFIRMED"
	var marker firewallRecovery
	if readJSON(filepath.Join(dir, "firewall-recovery.json"), &marker) != nil {
		return result
	}
	expires, e := time.Parse(time.RFC3339, marker.ExpiresAt)
	if e != nil || !time.Now().Before(expires) {
		return x.reconcileFirewall(req, dir)
	}
	after := x.firewallSnapshot()
	if after.Hash != marker.AfterHash || !FirewallEffectMatches(*req.Firewall, after, req.ControllerID) {
		return result
	}
	// This action arrives over a new authenticated connection. Commit on disk
	// before stopping the target's independent rollback timer.
	marker.After = after
	marker.Committed = true
	if atomicJSON(filepath.Join(dir, "firewall-recovery.json"), marker) != nil {
		return result
	}
	result.Status, result.Phase, result.Firewall = "SUCCEEDED", "FIREWALL_APPLIED", &after
	if atomicJSON(filepath.Join(dir, "result.json"), result) != nil {
		return softwareResult(req)
	}
	x.disarmFirewallRecovery(req.JobID)
	return result
}
func (x *executor) reconcileFirewall(req Request, dir string) Result {
	result := softwareResult(req)
	result.Phase = "FIREWALL_UNCONFIRMED"
	var marker firewallRecovery
	if readJSON(filepath.Join(dir, "firewall-recovery.json"), &marker) == nil {
		if marker.Committed {
			return x.confirmedFirewallResult(req, dir)
		}
		expires, e := time.Parse(time.RFC3339, marker.ExpiresAt)
		if e == nil && time.Now().After(expires) {
			return x.rollbackFirewall(req, dir, marker)
		}
		if e == nil {
			return pendingFirewallResult(req, marker, x.firewallSnapshot())
		}
		return result
	}
	var command FirewallChange
	if readJSON(filepath.Join(dir, "firewall-command.json"), &command) != nil {
		result.Status, result.Phase, result.ErrorCode = "FAILED", "FIREWALL_NOT_APPLIED", "FIREWALL_PREPARATION_INTERRUPTED"
		_ = atomicJSON(filepath.Join(dir, "result.json"), result)
		return result
	}
	s := x.firewallSnapshot()
	if FirewallEffectMatches(*req.Firewall, s, req.ControllerID) {
		result.Status, result.Phase, result.Firewall = "SUCCEEDED", "FIREWALL_APPLIED", &s
		_ = atomicJSON(filepath.Join(dir, "result.json"), result)
	}
	return result
}

func pendingFirewallResult(req Request, marker firewallRecovery, actual FirewallSnapshot) Result {
	r := softwareResult(req)
	r.Phase = "FIREWALL_UNCONFIRMED"
	if marker.AfterHash != "" && actual.Hash == marker.AfterHash && req.Firewall != nil && (req.Firewall.Operation == "ENABLE" || req.Firewall.Operation == "DISABLE") && FirewallEffectMatches(*req.Firewall, actual, req.ControllerID) {
		r.Phase, r.FirewallConfirmBy, r.Firewall = "FIREWALL_AWAITING_CONFIRM", marker.ExpiresAt, &actual
	}
	return r
}
func (x *executor) confirmedFirewallResult(req Request, dir string) Result {
	r := softwareResult(req)
	r.Phase = "FIREWALL_UNCONFIRMED"
	var marker firewallRecovery
	if readJSON(filepath.Join(dir, "firewall-recovery.json"), &marker) == nil && marker.Committed && FirewallEffectMatches(*req.Firewall, marker.After, req.ControllerID) {
		r.Status, r.Phase, r.Firewall = "SUCCEEDED", "FIREWALL_APPLIED", &marker.After
		if atomicJSON(filepath.Join(dir, "result.json"), r) != nil {
			return softwareResult(req)
		}
	}
	return r
}
