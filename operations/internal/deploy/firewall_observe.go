package deploy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Find the actual SSH peer through our sshd ancestors, including sudo/runuser.
// A configured external port may be NATed and is never substituted for it.
func sshManagementConnection(ctx context.Context, run SoftwareCommandRunner) (string, int) {
	ancestors := map[int]bool{}
	pid := os.Getppid()
	for range 24 {
		if pid <= 1 {
			break
		}
		ancestors[pid] = true
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
		if err != nil {
			break
		}
		parent := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				parent, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
			}
		}
		if parent == pid {
			break
		}
		pid = parent
	}
	out, err := softwareCommand(ctx, run, "/usr/bin/ss", "-H", "-tnp")
	if err != nil {
		return "", 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || f[0] != "ESTAB" || !strings.Contains(line, "sshd") {
			continue
		}
		match := false
		for id := range ancestors {
			if strings.Contains(line, "pid="+strconv.Itoa(id)+",") {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		_, port, err := net.SplitHostPort(f[3])
		if err != nil {
			continue
		}
		ip, _, err := net.SplitHostPort(f[4])
		if err != nil {
			continue
		}
		address, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		address = address.Unmap()
		n, _ := strconv.Atoi(port)
		if n > 0 && n <= 65535 {
			return netip.PrefixFrom(address, address.BitLen()).String(), n
		}
	}
	return "", 0
}

func (x *executor) firewallSnapshot() FirewallSnapshot {
	s := FirewallSnapshot{Status: "UNAVAILABLE", ObservedAt: stamp(), Ports: []HostPort{}, ManagedRules: []FirewallRule{}, ProtectedPorts: []int{}}
	ctx, cancel := context.WithTimeout(context.Background(), 15e9)
	defer cancel()
	ports, docker, dockerCode, err := ObserveHostPorts(ctx, x.softwareRunner)
	if err == nil {
		s.Ports, s.DockerPorts, s.DockerCode = ports, docker, dockerCode
	}
	if _, e := os.Stat("/usr/bin/docker"); os.IsNotExist(e) {
		s.DockerCode = "DOCKER_NOT_INSTALLED"
	}
	for _, p := range ports {
		if strings.HasPrefix(p.Process, "sshd") || strings.HasPrefix(p.Process, "streamfusion") {
			s.ProtectedPorts = append(s.ProtectedPorts, p.Port)
		}
	}
	s.ManagementSource, s.ManagementPort = sshManagementConnection(ctx, x.softwareRunner)
	if s.ManagementPort > 0 {
		s.ProtectedPorts = append(s.ProtectedPorts, s.ManagementPort)
	}
	if secureAncestors("/usr/sbin/ufw") != nil {
		s.Code = "UFW_NOT_INSTALLED"
		return s
	}
	osRelease, e := os.ReadFile("/etc/os-release")
	if e != nil || !strings.Contains(string(osRelease), "ID=ubuntu\n") {
		s.Code = "UFW_PLATFORM_UNSUPPORTED"
		return s
	}
	if e := checkFirewallBackend(ctx, x.softwareRunner); e != nil {
		s.Code = e.Error()
		return s
	}
	out, e := softwareCommand(ctx, x.softwareRunner, "/usr/sbin/ufw", "status", "verbose")
	if e != nil {
		s.Code = "UFW_STATUS_UNAVAILABLE"
		return s
	}
	if strings.HasPrefix(string(out), "Status: active\n") {
		s.Status = "ACTIVE"
	} else if strings.TrimSpace(string(out)) == "Status: inactive" {
		s.Status = "INACTIVE"
	} else {
		s.Code = "UFW_STATUS_INVALID"
		return s
	}
	added, e := softwareCommand(ctx, x.softwareRunner, "/usr/sbin/ufw", "show", "added")
	if e != nil || len(out) > 16<<10 || len(added) > 16<<10 || len(docker) > 16<<10 {
		s.Code = "UFW_RULES_UNAVAILABLE"
		return s
	}
	s.RawRules, s.AddedRules = string(out), string(added)
	files := []string{}
	for _, file := range []string{"/etc/ufw/user.rules", "/etc/ufw/user6.rules", "/etc/default/ufw", "/etc/ufw/before.rules", "/etc/ufw/before6.rules", "/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		if secureAncestors(file) != nil {
			s.Code = "UFW_CONFIG_NOT_PROTECTED"
			return s
		}
		info, e := os.Stat(file)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 512<<10 {
			s.Code = "UFW_CONFIG_UNAVAILABLE"
			return s
		}
		h, e := fileSHA(file)
		if e != nil {
			s.Code = "UFW_CONFIG_UNAVAILABLE"
			return s
		}
		files = append(files, h)
	}
	defaultConfig, _ := os.ReadFile("/etc/default/ufw")
	for _, line := range strings.Split(string(defaultConfig), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			value = strings.Trim(value, `"'`)
			if key == "DEFAULT_INPUT_POLICY" {
				s.IncomingPolicy = value
			}
			if key == "DEFAULT_OUTPUT_POLICY" {
				s.OutgoingPolicy = value
			}
		}

		if strings.TrimSpace(line) == "IPV6=yes" || strings.TrimSpace(line) == `IPV6="yes"` {
			s.IPv6 = true
		}
	}
	s.RulesHash = Digest(files)
	s.Hash = Digest([]string{s.Status, s.RulesHash, s.AddedRules})
	entries, e := os.ReadDir(filepath.Join(x.root, "firewall", "rules"))
	if e != nil && !os.IsNotExist(e) {
		s.Code = "UFW_OWNERSHIP_UNAVAILABLE"
		return s
	}
	if len(entries) > 1000 {
		s.Code = "UFW_RULE_LIMIT"
		return s
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var r FirewallRule
		if readJSON(filepath.Join(x.root, "firewall", "rules", entry.Name()), &r) != nil || !ID.MatchString(r.ID) {
			s.Code = "UFW_OWNERSHIP_INVALID"
			return s
		}
		if firewallRulePresent(s, x.owner.ControllerID, r) {
			s.ManagedRules = append(s.ManagedRules, r)
			if len(s.ManagedRules) > 100 {
				s.Code = "UFW_RULE_LIMIT"
				return s
			}
		}
	}
	if err != nil {
		s.Code = "PORT_OBSERVATION_DENIED"
	}
	return s
}

// UFW must be the sole service managing these rules. Missing units are normal,
// but an unreadable or transitional service state cannot authorize mutation.
func checkFirewallBackend(ctx context.Context, run SoftwareCommandRunner) error {
	out, _ := softwareCommand(ctx, run, "/usr/bin/systemctl", "show", "--no-pager", "--property=Id,LoadState,ActiveState", "firewalld.service", "nftables.service")
	seen := map[string]bool{}
	for _, block := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				fields[key] = value
			}
		}
		id := fields["Id"]
		if id != "firewalld.service" && id != "nftables.service" || seen[id] {
			return errors.New("FIREWALL_BACKEND_UNCONFIRMED")
		}
		seen[id] = true
		if fields["ActiveState"] != "inactive" {
			if fields["ActiveState"] == "active" || fields["ActiveState"] == "activating" || fields["ActiveState"] == "reloading" || fields["ActiveState"] == "deactivating" {
				return errors.New("FIREWALL_BACKEND_CONFLICT")
			}
			return errors.New("FIREWALL_BACKEND_UNCONFIRMED")
		}
		if fields["LoadState"] != "loaded" && fields["LoadState"] != "not-found" && fields["LoadState"] != "masked" {
			return errors.New("FIREWALL_BACKEND_UNCONFIRMED")
		}
	}
	if len(seen) != 2 {
		return errors.New("FIREWALL_BACKEND_UNCONFIRMED")
	}
	return nil
}

func firewallRulePresent(s FirewallSnapshot, controller string, r FirewallRule) bool {
	for _, line := range strings.Split(s.AddedRules, "\n") {
		if exactFirewallRule(line, controller, r) {
			return true
		}
	}
	return false
}
func (x *executor) firewallCheck(c FirewallChange) (FirewallSnapshot, error) {
	s := x.firewallSnapshot()
	if err := validateFirewallEffect(c, s); err != nil {
		return s, err
	}
	if c.Operation == "ENABLE" && (c.Remote || s.ManagementSource != "") && !firewallManagementAllowed(s) {
		return s, errors.New("SSH_ALLOW_RULE_REQUIRED")
	}
	if c.Operation == "ENABLE" && (c.Remote || s.ManagementSource != "") && firewallSSHBlocked(s) {
		return s, errors.New("SSH_RULE_CONFLICT")
	}
	if c.Operation == "ENABLE" || c.Operation == "DISABLE" {
		if secureAncestors("/usr/bin/systemctl") != nil {
			return s, errors.New("FIREWALL_RECOVERY_UNAVAILABLE")
		}
	}
	return s, nil
}
