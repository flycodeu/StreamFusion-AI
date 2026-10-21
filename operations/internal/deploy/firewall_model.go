package deploy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Revision 2 requires restart-persistent recovery and exact inbound evidence.
const FirewallCapabilityVersion = 2

type HostPort struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Process  string `json:"process,omitempty"`
}
type FirewallRule struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Source   string `json:"source"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}
type FirewallChange struct {
	Operation      string       `json:"operation"`
	Rule           FirewallRule `json:"rule"`
	ExpectedHash   string       `json:"expectedHash"`
	Remote         bool         `json:"remote"`
	ManagementPort int          `json:"managementPort"`
}
type FirewallSnapshot struct {
	IncomingPolicy   string         `json:"incomingPolicy"`
	OutgoingPolicy   string         `json:"outgoingPolicy"`
	IPv6             bool           `json:"ipv6"`
	Status           string         `json:"status"`
	Code             string         `json:"code,omitempty"`
	ObservedAt       string         `json:"observedAt"`
	Hash             string         `json:"hash"`
	RulesHash        string         `json:"rulesHash"`
	RawRules         string         `json:"rawRules"`
	AddedRules       string         `json:"addedRules"`
	Ports            []HostPort     `json:"ports"`
	DockerPorts      string         `json:"dockerPorts"`
	DockerCode       string         `json:"dockerCode,omitempty"`
	ManagedRules     []FirewallRule `json:"managedRules"`
	ProtectedPorts   []int          `json:"protectedPorts"`
	ManagementSource string         `json:"managementSource,omitempty"`
	ManagementPort   int            `json:"managementPort,omitempty"`
}

func (c FirewallChange) Validate() error {
	if !Hex.MatchString(c.ExpectedHash) || c.ManagementPort < 1 || c.ManagementPort > 65535 {
		return errors.New("FIREWALL_INPUT_INVALID")
	}
	if c.Operation == "ENABLE" || c.Operation == "DISABLE" {
		return nil
	}
	r := c.Rule
	if !slices.Contains([]string{"ADD", "DELETE"}, c.Operation) || !ID.MatchString(r.ID) || !slices.Contains([]string{"allow", "deny"}, r.Action) || !slices.Contains([]string{"tcp", "udp"}, r.Protocol) || r.Start < 1 || r.End < r.Start || r.End > 65535 || r.End-r.Start >= 100 {
		return errors.New("FIREWALL_RULE_INVALID")
	}
	prefix, err := netip.ParsePrefix(r.Source)
	if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() || prefix.Addr().IsMulticast() {
		return errors.New("FIREWALL_SOURCE_INVALID")
	}
	return nil
}
func firewallTag(controller, rule string) string { return "sfops-" + controller[:8] + "-" + rule }
func firewallArguments(r FirewallRule, tag string, remove bool) []string {
	args := []string{"--force"}
	if remove {
		args = append(args, "delete")
	} else if r.Action == "deny" {
		args = append(args, "prepend")
	}
	port := strconv.Itoa(r.Start)
	if r.End != r.Start {
		port += ":" + strconv.Itoa(r.End)
	}
	return append(args, r.Action, "in", "proto", r.Protocol, "from", r.Source, "to", "any", "port", port, "comment", tag)
}

var portProcess = regexp.MustCompile(`\(\("([a-zA-Z0-9_.-]+)"`)

func ParseHostPorts(raw string) ([]HostPort, error) {
	if len(raw) > 64<<10 {
		return nil, errors.New("PORT_SNAPSHOT_TOO_LARGE")
	}
	rows := []HostPort{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || (f[0] != "tcp" && f[0] != "udp") {
			continue
		}
		host, p, err := net.SplitHostPort(f[4])
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			continue
		}
		proc := ""
		if m := portProcess.FindStringSubmatch(line); len(m) > 1 {
			proc = m[1]
		}
		rows = append(rows, HostPort{f[0], host, n, proc})
		if len(rows) >= 256 {
			return nil, errors.New("PORT_SNAPSHOT_TOO_LARGE")
		}
	}
	return rows, nil
}
func ObserveHostPorts(ctx context.Context, run SoftwareCommandRunner) ([]HostPort, string, string, error) {
	out, err := softwareCommand(ctx, run, "/usr/bin/ss", "-H", "-lntup")
	if err != nil {
		return nil, "", "", errors.New("PORT_OBSERVATION_DENIED")
	}
	ports, err := ParseHostPorts(string(out))
	if err != nil {
		return nil, "", "", err
	}
	docker, derr := softwareCommand(ctx, run, "/usr/bin/docker", "container", "ls", "--format", `{{.Names}}\t{{.Ports}}`)
	code := ""
	if derr != nil {
		code = "DOCKER_PORTS_UNAVAILABLE"
		docker = nil
	}
	return ports, string(docker), code, nil
}

func validateFirewallEffect(c FirewallChange, s FirewallSnapshot) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if s.Code != "" || s.Hash != c.ExpectedHash {
		return errors.New("FIREWALL_SNAPSHOT_CHANGED")
	}
	if c.Operation == "ENABLE" && (c.Remote || s.ManagementSource != "") && (s.ManagementSource == "" || s.ManagementPort == 0) {
		return errors.New("SSH_PROTECTION_UNCONFIRMED")
	}
	if c.Operation == "ADD" && c.Rule.Action == "deny" || c.Operation == "DELETE" && c.Rule.Action == "allow" {
		for _, p := range append(s.ProtectedPorts, c.ManagementPort) {
			if c.Rule.Protocol == "tcp" && p >= c.Rule.Start && p <= c.Rule.End {
				return errors.New("MANAGEMENT_PORT_PROTECTED")
			}
		}
	}
	if (c.Operation == "ADD" || c.Operation == "DELETE") && strings.Contains(c.Rule.Source, ":") && !s.IPv6 {
		return errors.New("UFW_IPV6_DISABLED")
	}
	if c.Operation == "DELETE" && !slices.Contains(s.ManagedRules, c.Rule) {
		return errors.New("FIREWALL_RULE_NOT_OWNED")
	}
	if c.Operation == "ADD" && firewallTupleConflict(c.Rule, s.AddedRules) {
		return errors.New("FIREWALL_RULE_ALREADY_EXISTS")
	}
	// Docker publications can bypass INPUT/UFW. Refuse a deceptive rule change.
	if c.Operation == "ADD" && s.DockerCode != "" && s.DockerCode != "DOCKER_NOT_INSTALLED" {
		return errors.New("DOCKER_PORT_OWNERSHIP_UNCONFIRMED")
	}
	if c.Operation == "ADD" {
		published := regexp.MustCompile(`:([0-9]+)(?:-([0-9]+))?->[0-9]+(?:-[0-9]+)?/(tcp|udp)`)
		for _, m := range published.FindAllStringSubmatch(s.DockerPorts, -1) {
			if m[3] != c.Rule.Protocol {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			end := start
			if m[2] != "" {
				end, _ = strconv.Atoi(m[2])
			}
			if start <= c.Rule.End && end >= c.Rule.Start {
				return errors.New("DOCKER_PORT_UFW_UNSUPPORTED")
			}
		}
	}

	return nil
}

func firewallTupleConflict(wanted FirewallRule, raw string) bool {
	for _, action := range []string{"allow", "deny", "reject", "limit"} {
		r := wanted
		r.Action = action
		if firewallTupleExists(r, raw) {
			return true
		}
	}
	return false
}

// Avoid updating the comment/ownership of an existing administrator rule. UFW
// may treat adding an identical tuple with a new comment as an update.
func firewallTupleExists(wanted FirewallRule, raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.SplitN(line, " comment ", 2)[0]
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != "ufw" {
			continue
		}
		action := ""
		source := "any"
		proto := "any"
		port := ""
		for i, v := range f {
			if v == "allow" || v == "deny" || v == "reject" || v == "limit" {
				action = v
				if i+1 < len(f) && f[i+1] != "in" && f[i+1] != "out" && f[i+1] != "from" {
					port = f[i+1]
				}
			}
			if i+1 < len(f) {
				switch v {
				case "from":
					source = f[i+1]
				case "proto":
					proto = f[i+1]
				case "port":
					port = f[i+1]
				}
			}
		}
		if action != wanted.Action {
			continue
		}
		if bits := strings.Split(port, "/"); len(bits) == 2 {
			port, proto = bits[0], bits[1]
		}
		pair := strings.Split(port, ":")
		start, _ := strconv.Atoi(pair[0])
		end := start
		if len(pair) == 2 {
			end, _ = strconv.Atoi(pair[1])
		}
		if start != wanted.Start || end != wanted.End || (proto != "any" && proto != wanted.Protocol) {
			continue
		}
		if source == "any" && (wanted.Source == "0.0.0.0/0" || wanted.Source == "::/0") {
			return true
		}
		if ip, e := netip.ParseAddr(source); e == nil {
			source = netip.PrefixFrom(ip, ip.BitLen()).String()
		}
		if source == wanted.Source {
			return true
		}
	}
	return false
}

func parseFirewallPrefix(value string) (netip.Prefix, error) { return netip.ParsePrefix(value) }
func netipAddress(value string) (netip.Addr, error) {
	p, e := netip.ParsePrefix(value)
	return p.Addr(), e
}
