package serviceinstall

import (
	"errors"
	"maps"
	"net"
	"slices"
	"strconv"
)

func WebPortKey(d Definition) string {
	if d.Web == nil {
		return ""
	}
	if d.Web.PortKey == "" {
		return "main"
	}
	return d.Web.PortKey
}

func webPortResource(d Definition, r ResolvedResource) bool {
	port, err := strconv.Atoi(r.Value)
	ip := net.ParseIP(r.BindAddress)
	return WebPortKey(d) != "" && r.Key == WebPortKey(d) && r.Type == "PORT" && r.Protocol == "TCP" && !r.Release && err == nil && port > 0 && port <= 65535 && ip != nil && ip.To4() != nil && !ip.IsMulticast()
}

// ConfirmedWebPort selects the last confirmed single Web entry. Other resource
// keys still identify addresses, including repeated labels for multiple sites.
func ConfirmedWebPort(d Definition, resources []ResolvedResource) (ResolvedResource, bool) {
	for i := len(resources) - 1; i >= 0; i-- {
		if webPortResource(d, resources[i]) && sha.MatchString(resources[i].IdentityHash) {
			return resources[i], true
		}
	}
	return ResolvedResource{}, false
}

func ProjectWebConfig(d Definition, c Config, resources []ResolvedResource) Config {
	// Published configuration operations also manage non-Web and auxiliary ports.
	// Project confirmed claims into a copy, never into the frozen installation.
	c.Ports = maps.Clone(c.Ports)
	for _, resource := range resources {
		if !DeclaredInstallationPort(d, resource.Key, resource.Protocol) || !confirmedPortResource(resource) {
			continue
		}
		port, _ := strconv.Atoi(resource.Value)
		if resource.Key == "main" {
			c.HostPort, c.BindAddress = port, resource.BindAddress
		} else {
			if c.Ports == nil {
				c.Ports = map[string]int{}
			}
			c.Ports[resource.Key] = port
		}
	}
	r, ok := ConfirmedWebPort(d, resources)
	if !ok {
		return c
	}
	c.BindAddress = r.BindAddress
	port, _ := strconv.Atoi(r.Value)
	if WebPortKey(d) == "main" {
		c.HostPort = port
	} else {
		c.Ports = maps.Clone(c.Ports)
		if c.Ports == nil {
			c.Ports = map[string]int{}
		}
		c.Ports[WebPortKey(d)] = port
	}
	return c
}

// OverlayWebPortClaims overrides only the installation's single Web listener;
// recomputing Config.BindAddress would accidentally move its other listeners.
func OverlayWebPortClaims(d Definition, base, confirmed []ResolvedResource) []ResolvedResource {
	active, ok := ConfirmedWebPort(d, confirmed)
	out := slices.Clone(base)
	configured := map[string]int{}
	for index, resource := range confirmed {
		if DeclaredInstallationPort(d, resource.Key, resource.Protocol) && confirmedPortResource(resource) {
			configured[resource.Key+"/"+resource.Protocol] = index
		}
	}
	out = slices.DeleteFunc(out, func(r ResolvedResource) bool {
		_, exists := configured[r.Key+"/"+r.Protocol]
		return r.Type == "PORT" && exists
	})
	confirmed = slices.DeleteFunc(slices.Clone(confirmed), func(r ResolvedResource) bool {
		index, exists := configured[r.Key+"/"+r.Protocol]
		return r.Type == "PORT" && exists && r != confirmed[index]
	})
	if !ok {
		return append(out, confirmed...)
	}
	out = slices.DeleteFunc(out, func(r ResolvedResource) bool {
		return r.Key == WebPortKey(d) && r.Type == "PORT" && r.Protocol == "TCP"
	})
	for _, r := range confirmed {
		if r.Key != WebPortKey(d) || r.Type != "PORT" || r.Protocol != "TCP" {
			out = append(out, r)
		}
	}
	return append(out, active)
}

func confirmedPortResource(r ResolvedResource) bool {
	port, err := strconv.Atoi(r.Value)
	ip := net.ParseIP(r.BindAddress)
	return r.Type == "PORT" && !r.Release && (r.Protocol == "TCP" || r.Protocol == "UDP") && err == nil && port > 0 && port <= 65535 && ip != nil && ip.To4() != nil && !ip.IsMulticast() && sha.MatchString(r.IdentityHash)
}

// Repeated labels such as Nginx site listeners keep address semantics. Only a
// port explicitly declared by the installation may replace its own old claim.
func DeclaredInstallationPort(d Definition, key, protocol string) bool {
	if key == "main" && protocol == "TCP" && d.Port > 0 {
		return true
	}
	return slices.ContainsFunc(d.Ports, func(p PortDescriptor) bool { return p.Key == key && p.Protocol == protocol })
}

func ManagementReplacesInstallationPorts(m ManagementRequest) bool {
	op, err := m.Definition.ManagementOperation(m.OperationKey)
	return err == nil && op.Rebind && op.Key == "configure" && !op.ReadOnly && slices.ContainsFunc(op.Resources, func(r ResourceDefinition) bool {
		return r.Type == "PORT" && !r.Release && DeclaredInstallationPort(m.Definition, r.Key, r.Protocol)
	})
}

// Only a mutating operation's own explicit Web listener declaration opts into
// replacement. Diagnostics and shared definition resources cannot release it.
func ManagementReplacesWebPort(m ManagementRequest) bool {
	op, err := m.Definition.ManagementOperation(m.OperationKey)
	if err != nil || op.ReadOnly || WebPortKey(m.Definition) == "" {
		return false
	}
	return slices.ContainsFunc(op.Resources, func(r ResourceDefinition) bool {
		return r.Key == WebPortKey(m.Definition) && r.Type == "PORT" && r.Protocol == "TCP" && !r.Release
	})
}

func MergeManagementResourceClaims(m ManagementRequest, prior, changes []ResolvedResource) ([]ResolvedResource, error) {
	op, err := m.Definition.ManagementOperation(m.OperationKey)
	if err != nil {
		return nil, err
	}
	if op.ReadOnly {
		return slices.Clone(prior), nil
	}
	hasWebChange := slices.ContainsFunc(changes, func(r ResolvedResource) bool { return webPortResource(m.Definition, r) })
	if hasWebChange && !ManagementReplacesWebPort(m) {
		return nil, errors.New("MANAGEMENT_WEB_ENTRY_UNDECLARED")
	}
	if hasWebChange {
		prior = slices.DeleteFunc(slices.Clone(prior), func(r ResolvedResource) bool {
			return r.Key == WebPortKey(m.Definition) && r.Type == "PORT" && r.Protocol == "TCP"
		})
	}
	if ManagementReplacesInstallationPorts(m) {
		prior = slices.DeleteFunc(slices.Clone(prior), func(previous ResolvedResource) bool {
			return slices.ContainsFunc(changes, func(next ResolvedResource) bool {
				return previous.Type == "PORT" && next.Type == "PORT" && previous.Key == next.Key && previous.Protocol == next.Protocol && !next.Release && DeclaredInstallationPort(m.Definition, next.Key, next.Protocol) && slices.ContainsFunc(op.Resources, func(declared ResourceDefinition) bool {
					return declared.Type == "PORT" && declared.Key == next.Key && declared.Protocol == next.Protocol && !declared.Release
				})
			})
		})
	}
	return MergeResourceClaims(prior, changes)
}
