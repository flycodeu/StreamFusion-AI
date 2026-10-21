package deploy

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// Only the canonical subset emitted for Ops rules can prove ownership. A
// matching comment on an outbound, routed or destination-specific rule cannot
// certify the inbound rule that the user requested.
func exactFirewallRule(line, controller string, wanted FirewallRule) bool {
	body, comment, ok := strings.Cut(line, " comment ")
	if !ok || strings.Trim(strings.TrimSpace(comment), "\"'") != firewallTag(controller, wanted.ID) {
		return false
	}
	f := strings.Fields(body)
	if len(f) < 3 || f[0] != "ufw" || f[1] != wanted.Action {
		return false
	}
	proto, source, destination, port := "any", "any", "any", ""
	for i := 2; i < len(f); i++ {
		switch f[i] {
		case "in":
		case "proto", "from", "to", "port":
			if i+1 == len(f) {
				return false
			}
			key := f[i]
			i++
			switch key {
			case "proto":
				proto = f[i]
			case "from":
				source = f[i]
			case "to":
				destination = f[i]
			case "port":
				port = f[i]
			}
		default:
			if i != 2 || port != "" || f[i][0] < '0' || f[i][0] > '9' {
				return false
			}
			port = f[i]
		}
	}
	if p, protocol, ok := strings.Cut(port, "/"); ok {
		port, proto = p, protocol
	}
	if destination != "any" || proto != wanted.Protocol {
		return false
	}
	first, last, _ := strings.Cut(port, ":")
	if last == "" {
		last = first
	}
	start, e1 := strconv.Atoi(first)
	end, e2 := strconv.Atoi(last)
	if e1 != nil || e2 != nil || start != wanted.Start || end != wanted.End {
		return false
	}
	if source == "any" {
		return wanted.Source == "0.0.0.0/0" || wanted.Source == "::/0"
	}
	if ip, e := netip.ParseAddr(source); e == nil {
		source = netip.PrefixFrom(ip, ip.BitLen()).String()
	}
	return source == wanted.Source
}

// Before global enable, a saved allow alone is insufficient: an earlier deny
// may shadow it. Unknown deny expressions fail closed rather than being
// interpreted as proof that the new SSH connection will be reachable.
func firewallSSHBlocked(s FirewallSnapshot) bool {
	peer, e := netipAddress(s.ManagementSource)
	if e != nil {
		return true
	}
	for _, line := range strings.Split(s.AddedRules, "\n") {
		f := strings.Fields(strings.SplitN(line, " comment ", 2)[0])
		if len(f) < 2 || f[0] != "ufw" || (f[1] != "deny" && f[1] != "reject" && f[1] != "limit") {
			continue
		}
		if len(f) < 3 {
			return true
		}
		if slices.Contains(f, "out") || slices.Contains(f, "udp") || strings.HasSuffix(f[2], "/udp") {
			continue
		}
		source, port := "any", ""
		for i := 2; i < len(f); i++ {
			if i+1 < len(f) {
				if f[i] == "from" {
					source = f[i+1]
				}
				if f[i] == "port" {
					port = f[i+1]
				}
			}
		}
		if source != "any" {
			if ip, e := netip.ParseAddr(source); e == nil {
				source = netip.PrefixFrom(ip, ip.BitLen()).String()
			}
			prefix, e := netip.ParsePrefix(source)
			if e == nil && !prefix.Contains(peer) {
				continue
			}
		}
		if port == "" && len(f) >= 3 {
			port = strings.TrimSuffix(f[2], "/tcp")
		}
		first, last, _ := strings.Cut(port, ":")
		if last == "" {
			last = first
		}
		start, e1 := strconv.Atoi(first)
		end, e2 := strconv.Atoi(last)
		if e1 == nil && e2 == nil && (s.ManagementPort < start || s.ManagementPort > end) {
			continue
		}
		return true
	}
	return false
}
