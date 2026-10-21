package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func firewallFixture() (FirewallChange, FirewallSnapshot) {
	r := FirewallRule{ID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", Action: "allow", Protocol: "tcp", Source: "192.0.2.0/24", Start: 8080, End: 8080}
	c := FirewallChange{Operation: "ADD", Rule: r, ExpectedHash: strings.Repeat("a", 64), Remote: true, ManagementPort: 22}
	s := FirewallSnapshot{Status: "ACTIVE", Hash: c.ExpectedHash, RulesHash: strings.Repeat("b", 64), ProtectedPorts: []int{22, 19090}, ManagementPort: 22, ManagementSource: "192.0.2.4/32", Ports: []HostPort{}, ManagedRules: []FirewallRule{}}
	return c, s
}
func TestFirewallValidationAndManagementProtection(t *testing.T) {
	c, s := firewallFixture()
	if validateFirewallEffect(c, s) != nil {
		t.Fatal("valid rule rejected")
	}
	for _, alter := range []func(*FirewallChange){func(c *FirewallChange) { c.Rule.Start = 0 }, func(c *FirewallChange) { c.Rule.End = 65536 }, func(c *FirewallChange) { c.Rule.End = c.Rule.Start + 100 }, func(c *FirewallChange) { c.Rule.Source = "192.0.2.4/24" }, func(c *FirewallChange) { c.Rule.Source = "any; ufw disable" }, func(c *FirewallChange) { c.Rule.Protocol = "tcp;id" }, func(c *FirewallChange) { c.Rule.ID = "../../root" }, func(c *FirewallChange) { c.ExpectedHash = "old" }} {
		bad := c
		alter(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid firewall input accepted", bad)
		}
	}
	for _, port := range []int{22, 19090} {
		bad := c
		bad.Rule.Start, bad.Rule.End, bad.Rule.Action = port, port, "deny"
		if validateFirewallEffect(bad, s) == nil {
			t.Fatal("management port deny accepted", port)
		}
	}
	for _, published := range []string{"web\t0.0.0.0:8080->80/tcp", "web\t[::]:8070-8090->80-100/tcp"} {
		bad := s
		bad.DockerPorts = published
		if validateFirewallEffect(c, bad) == nil {
			t.Fatal("Docker port promised UFW control")
		}
	}
	for _, protocol := range []string{"tcp", "udp"} {
		other := "udp"
		if protocol == "udp" {
			other = "tcp"
		}
		rule, snapshot := c, s
		rule.Rule.Protocol = protocol
		snapshot.DockerPorts = "media\t0.0.0.0:8080->80/" + other
		if e := validateFirewallEffect(rule, snapshot); e != nil {
			t.Fatal("different Docker transport blocked independent rule", protocol, e)
		}
		snapshot.DockerPorts = "media\t0.0.0.0:8080->80/" + protocol
		if validateFirewallEffect(rule, snapshot) == nil {
			t.Fatal("same transport publication accepted", protocol)
		}
	}
	bad := s
	bad.DockerCode = "DOCKER_PORTS_UNAVAILABLE"
	if validateFirewallEffect(c, bad) == nil {
		t.Fatal("unconfirmed Docker ownership accepted")
	}
	bad.DockerCode = "DOCKER_NOT_INSTALLED"
	if validateFirewallEffect(c, bad) != nil {
		t.Fatal("native-only host requires Docker")
	}
	bad = s
	bad.Hash = strings.Repeat("b", 64)
	if validateFirewallEffect(c, bad) == nil {
		t.Fatal("stale rules accepted")
	}
	c.Operation = "DELETE"
	if validateFirewallEffect(c, s) == nil {
		t.Fatal("external rule deletion allowed")
	}
	s.ManagedRules = []FirewallRule{c.Rule}
	if validateFirewallEffect(c, s) != nil {
		t.Fatal("owned delete rejected")
	}
	c.Rule.Start, c.Rule.End = 22, 22
	s.ManagedRules = []FirewallRule{c.Rule}
	if validateFirewallEffect(c, s) == nil {
		t.Fatal("management allow deletion accepted")
	}
}
func TestFirewallRulesPreserveExternalOwnershipAndRequireExactEvidence(t *testing.T) {
	c, s := firewallFixture()
	controller := "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"
	for _, line := range []string{"ufw allow from 192.0.2.0/24 to any port 8080 proto tcp comment 'manual'", "ufw allow in proto tcp from 192.0.2.0/24 to any port 8080"} {
		if !firewallTupleExists(c.Rule, line) {
			t.Fatal("existing tuple missed", line)
		}
		s.AddedRules = line
		if validateFirewallEffect(c, s) == nil {
			t.Fatal("external comment could be overwritten")
		}
	}
	for _, action := range []string{"deny", "reject", "limit"} {
		s.AddedRules = "ufw " + action + " from 192.0.2.0/24 to any port 8080 proto tcp comment 'manual'"
		if validateFirewallEffect(c, s) == nil {
			t.Fatal("existing action could be overwritten", action)
		}
	}
	r := c.Rule
	r.Source = "0.0.0.0/0"
	if !firewallTupleExists(r, "ufw allow 8080/tcp") {
		t.Fatal("short UFW form missed")
	}
	s.AddedRules = "ufw allow from 192.0.2.0/24 to any port 8081 proto tcp comment '" + firewallTag(controller, r.ID) + "'"
	if FirewallEffectMatches(c, s, controller) {
		t.Fatal("tag alone certified wrong port")
	}
	s.AddedRules = "ufw allow from 192.0.2.0/24 to any port 8080 proto tcp comment '" + firewallTag(controller, r.ID) + "'"
	if !FirewallEffectMatches(c, s, controller) {
		t.Fatal("correct rule receipt rejected")
	}
	c.Operation = "ENABLE"
	if FirewallEffectMatches(c, FirewallSnapshot{Status: "INACTIVE"}, controller) {
		t.Fatal("inactive certified enable")
	}
	if firewallManagementAllowed(s) {
		t.Fatal("non-SSH rule certified management access")
	}
	s.ManagedRules = []FirewallRule{{Action: "allow", Protocol: "tcp", Source: "192.0.2.0/24", Start: 22, End: 22}}
	if !firewallManagementAllowed(s) {
		t.Fatal("source-specific SSH guard missed")
	}
}

func TestFirewallIPv6AndBackendStateGuards(t *testing.T) {
	c, s := firewallFixture()
	c.Rule.Source = "2001:db8::/64"
	if e := validateFirewallEffect(c, s); e == nil || e.Error() != "UFW_IPV6_DISABLED" {
		t.Fatal("disabled IPv6 accepted", e)
	}
	s.IPv6 = true
	if e := validateFirewallEffect(c, s); e != nil {
		t.Fatal(e)
	}
	normal := "Id=firewalld.service\nLoadState=not-found\nActiveState=inactive\n\nId=nftables.service\nLoadState=loaded\nActiveState=inactive\n"
	for _, tc := range []struct{ output, code string }{
		{normal, ""},
		{strings.Replace(normal, "LoadState=not-found", "LoadState=masked", 1), ""},
		{strings.Replace(normal, "ActiveState=inactive", "ActiveState=active", 1), "FIREWALL_BACKEND_CONFLICT"},
		{strings.Replace(normal, "ActiveState=inactive", "ActiveState=activating", 1), "FIREWALL_BACKEND_CONFLICT"},
		{strings.Replace(normal, "ActiveState=inactive", "ActiveState=failed", 1), "FIREWALL_BACKEND_UNCONFIRMED"},
		{"", "FIREWALL_BACKEND_UNCONFIRMED"},
	} {
		e := checkFirewallBackend(context.Background(), func(_ context.Context, command string, args ...string) ([]byte, error) {
			if command != "/usr/bin/systemctl" || len(args) != 5 || args[0] != "show" {
				t.Fatal("unbounded backend command", command, args)
			}
			// Missing systemd units can return nonzero with complete properties.
			return []byte(tc.output), errors.New("unit absent")
		})
		if tc.code == "" && e != nil || tc.code != "" && (e == nil || e.Error() != tc.code) {
			t.Fatal(tc.code, e)
		}
	}
}
func TestHostPortParsingKeepsFamiliesProtocolsAndDoesNotExposeCommands(t *testing.T) {
	ports, e := ParseHostPorts("tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:((\"sshd\",pid=123,fd=3))\nudp UNCONN 0 0 [::]:8554 [::]:*\ntcp LISTEN 0 128 127.0.0.1:65536 *:*\n")
	if e != nil || len(ports) != 2 || ports[0].Process != "sshd" || ports[1].Protocol != "udp" || ports[1].Address != "::" {
		t.Fatal(ports, e)
	}
	if _, e = ParseHostPorts(strings.Repeat("x", (64<<10)+1)); e == nil {
		t.Fatal("unbounded port output")
	}
}

func TestFirewallRecoveryNeverOverwritesChangedPolicy(t *testing.T) {
	c, s := firewallFixture()
	c.Operation = "ENABLE"
	before := s
	before.Status = "INACTIVE"
	m := firewallRecovery{Before: before, AfterHash: strings.Repeat("c", 64)}
	s.Hash = m.AfterHash
	if action, e := firewallRecoveryOperation(&c, m, s); e != nil || action != "disable" {
		t.Fatal(action, e)
	}
	m.Before = s
	s.Status = "INACTIVE"
	s.Hash = strings.Repeat("d", 64)
	m.AfterHash = s.Hash
	if action, e := firewallRecoveryOperation(&c, m, s); e != nil || action != "enable" {
		t.Fatal(action, e)
	}
	for _, alter := range []func(*firewallRecovery, *FirewallSnapshot){func(m *firewallRecovery, s *FirewallSnapshot) { m.Committed = true }, func(m *firewallRecovery, s *FirewallSnapshot) { m.Before.Status = "invalid" }, func(m *firewallRecovery, s *FirewallSnapshot) { s.RulesHash = "external-change" }, func(m *firewallRecovery, s *FirewallSnapshot) { s.Hash = "unconfirmed-change" }, func(m *firewallRecovery, s *FirewallSnapshot) { s.Code = "UFW_STATUS_UNAVAILABLE" }} {
		marker, actual := m, s
		alter(&marker, &actual)
		if _, e := firewallRecoveryOperation(&c, marker, actual); e == nil {
			t.Fatal("unsafe recovery allowed", marker, actual)
		}
	}
}

func TestFirewallRecoveryPreservesReceiptForPendingAck(t *testing.T) {
	c, _ := firewallFixture()
	c.Operation = "ENABLE"
	req := Request{ControllerID: "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb", TargetID: c.Rule.ID, JobID: c.Rule.ID, InputHash: strings.Repeat("c", 64), Firewall: &c}
	x := executor{root: t.TempDir()}
	commands := []string{}
	x.softwareRun = func(_ time.Duration, command string, args ...string) ([]byte, error) {
		commands = append(commands, command)
		return nil, nil
	}
	dir := t.TempDir()
	for _, status := range []string{"FAILED", "SUCCEEDED"} {
		final := softwareResult(req)
		final.Status, final.Phase = status, "FIREWALL_ROLLED_BACK"
		if status == "SUCCEEDED" {
			final.Phase = "FIREWALL_APPLIED"
		}
		raw, e := json.Marshal(final)
		if e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		for range 2 {
			actual := x.rollbackFirewall(req, dir, firewallRecovery{})
			if Digest(actual) != Digest(final) {
				t.Fatal("late timer replaced terminal receipt before ACK")
			}
		}
	}
	if len(commands) != 0 {
		t.Fatal("terminal recovery ran commands", commands)
	}
}

func TestFirewallOwnershipRejectsOutboundAndShadowedSSH(t *testing.T) {
	c, s := firewallFixture()
	controller := "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"
	tag := firewallTag(controller, c.Rule.ID)
	for _, rule := range []string{
		"ufw allow out from 192.0.2.0/24 to any port 8080 proto tcp",
		"ufw route allow from 192.0.2.0/24 to any port 8080 proto tcp",
		"ufw allow from 192.0.2.0/24 to 192.0.2.8 port 8080 proto tcp",
		"ufw allow in on eth0 from 192.0.2.0/24 to any port 8080 proto tcp",
	} {
		s.AddedRules = rule + " comment '" + tag + "'"
		if FirewallEffectMatches(c, s, controller) {
			t.Fatal("comment certified wrong incoming rule", rule)
		}
	}
	for _, tc := range []struct {
		line    string
		blocked bool
	}{
		{"ufw deny 22/tcp", true},
		{"ufw reject from 192.0.2.0/24 to any port 22 proto tcp", true},
		{"ufw limit 22/tcp", true},
		{"ufw deny from 192.0.2.0/24", true},
		{"ufw deny OpenSSH", true},
		{"ufw deny", true},
		{"ufw deny 22/udp", false},
		{"ufw deny out 22/tcp", false},
		{"ufw deny from 198.51.100.0/24 to any port 22 proto tcp", false},
		{"ufw deny 1000:2000/tcp", false},
	} {
		s.AddedRules = tc.line
		if firewallSSHBlocked(s) != tc.blocked {
			t.Fatal("SSH rule conflict misclassified", tc)
		}
	}
}

func TestFirewallStatusRestoresPendingConfirmationAfterLostReply(t *testing.T) {
	c, s := firewallFixture()
	c.Operation = "ENABLE"
	req := Request{ControllerID: c.Rule.ID, Firewall: &c}
	m := firewallRecovery{AfterHash: s.Hash, ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
	r := pendingFirewallResult(req, m, s)
	if r.Phase != "FIREWALL_AWAITING_CONFIRM" || r.Status != "UNKNOWN" || r.FirewallConfirmBy != m.ExpiresAt || r.Firewall == nil {
		t.Fatal("could not recover original confirmation", r)
	}
	s.Hash = strings.Repeat("d", 64)
	if r := pendingFirewallResult(req, m, s); r.Phase == "FIREWALL_AWAITING_CONFIRM" {
		t.Fatal("changed rule set offered stale confirmation")
	}
}

func TestFirewallRecoveryTimerPersistsOriginalDeadlineAcrossReboot(t *testing.T) {
	job := "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
	service, timer, e := firewallRecoveryUnits("/opt/port-test", job, "2026-10-21T20:12:00+08:00")
	if e != nil || !strings.Contains(timer, "OnCalendar=2026-10-21 12:12:00 UTC\nPersistent=true\n") || !strings.Contains(timer, "OnActiveSec=120s") || !strings.Contains(service, "target-firewall-recover --root /opt/port-test --job "+job) {
		t.Fatal("recovery lost its durable original deadline", e, timer)
	}
	for _, bad := range []string{"", "now; ufw disable", "2026-10-21"} {
		if _, _, e := firewallRecoveryUnits("/opt/port-test", job, bad); e == nil {
			t.Fatal("invalid recovery deadline accepted")
		}
	}
}
