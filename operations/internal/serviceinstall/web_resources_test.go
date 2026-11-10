package serviceinstall

import (
	"reflect"
	"testing"
)

func webClaim(key, port, bind string) ResolvedResource {
	return ResolvedResource{Key: key, Type: "PORT", Value: port, Protocol: "TCP", BindAddress: bind, IdentityHash: Digest([]string{key, port, bind})}
}

func TestWebEntryReplacementPreservesOtherSitesAndReadonlyClaims(t *testing.T) {
	old, next := webClaim("main", "9924", "127.0.0.1"), webClaim("main", "19924", "0.0.0.0")
	sites := []ResolvedResource{webClaim("listener", "8081", "127.0.0.1"), webClaim("listener", "8082", "127.0.0.1")}
	m := ManagementRequest{OperationKey: "network", Definition: Definition{Web: &WebAccessDefinition{PortKey: "main"}, Management: &ManagementDefinition{Operations: []ManagementOperation{{Key: "network", Resources: []ResourceDefinition{{Key: "main", Type: "PORT", Protocol: "TCP"}}}}}}}
	prior := append([]ResolvedResource{old}, sites...)
	merged, err := MergeManagementResourceClaims(m, prior, []ResolvedResource{next})
	if err != nil || !reflect.DeepEqual(merged, append(sites, next)) || prior[0] != old {
		t.Fatal("Web replacement lost another site or mutated prior claims", merged, err)
	}
	base := []ResolvedResource{old, webClaim("admin", "9000", "127.0.0.1")}
	overlaid := OverlayWebPortClaims(m.Definition, base, []ResolvedResource{old, next})
	if !reflect.DeepEqual(overlaid, []ResolvedResource{base[1], next}) {
		t.Fatal("old installation entry returned or another listener moved", overlaid)
	}
	config := Config{BindAddress: "127.0.0.1", HostPort: 9924}
	projected := ProjectWebConfig(m.Definition, config, []ResolvedResource{old, next})
	if projected.HostPort != 19924 || projected.BindAddress != "0.0.0.0" || config.HostPort != 9924 {
		t.Fatal("projection changed the frozen config or selected the old entry")
	}
	invalid := next
	invalid.IdentityHash = ""
	if selected, ok := ConfirmedWebPort(m.Definition, []ResolvedResource{old, invalid}); !ok || selected != old {
		t.Fatal("unconfirmed listener replaced a confirmed entry")
	}
	m.Definition.Management.Operations[0].ReadOnly = true
	if ManagementReplacesWebPort(m) {
		t.Fatal("diagnostics opted into replacement")
	}
	if got, err := MergeManagementResourceClaims(m, prior, []ResolvedResource{next}); err != nil || !reflect.DeepEqual(got, prior) {
		t.Fatal("diagnostics changed listener claims", err)
	}
	m.Definition.Management.Operations[0].ReadOnly = false
	m.Definition.Management.Operations[0].Resources = nil
	if _, err := MergeManagementResourceClaims(m, prior, []ResolvedResource{next}); err == nil || err.Error() != "MANAGEMENT_WEB_ENTRY_UNDECLARED" {
		t.Fatal("an operation without its own declaration replaced the entry", err)
	}
	m.Definition.Web = nil
	if got, err := MergeManagementResourceClaims(m, prior, []ResolvedResource{next}); err != nil || len(got) != len(prior)+1 {
		t.Fatal("ordinary repeated resource labels lost address semantics", got, err)
	}
}

func TestWebEntryProjectionCopiesNamedPortMap(t *testing.T) {
	d := Definition{Web: &WebAccessDefinition{PortKey: "web"}}
	original := Config{BindAddress: "127.0.0.1", Ports: map[string]int{"web": 8080, "admin": 9000}}
	projected := ProjectWebConfig(d, original, []ResolvedResource{webClaim("web", "8088", "0.0.0.0")})
	if projected.Ports["web"] != 8088 || projected.Ports["admin"] != 9000 || original.Ports["web"] != 8080 {
		t.Fatal("named Web projection mutated frozen ports")
	}
}

func TestConfigurationReplacesDeclaredDatabasePortsAndPreservesOtherClaims(t *testing.T) {
	d := Definition{Port: 3306, Ports: []PortDescriptor{{Key: "admin", Protocol: "TCP"}}, Management: &ManagementDefinition{Operations: []ManagementOperation{{Key: "configure", Rebind: true, Resources: []ResourceDefinition{{Key: "main", Type: "PORT", Protocol: "TCP"}}}}}}
	m := ManagementRequest{Definition: d, OperationKey: "configure"}
	old, next, other := webClaim("main", "3306", "127.0.0.1"), webClaim("main", "13306", "0.0.0.0"), webClaim("listener", "8081", "127.0.0.1")
	merged, err := MergeManagementResourceClaims(m, []ResolvedResource{old, other}, []ResolvedResource{next})
	if err != nil || !reflect.DeepEqual(merged, []ResolvedResource{other, next}) {
		t.Fatal("database claim was duplicated or another claim lost", merged, err)
	}
	overlaid := OverlayWebPortClaims(d, []ResolvedResource{old, other}, []ResolvedResource{old, next})
	if !reflect.DeepEqual(overlaid, []ResolvedResource{other, next}) {
		t.Fatal("old installation port reappeared", overlaid)
	}
	original := Config{HostPort: 3306, BindAddress: "127.0.0.1", Ports: map[string]int{"admin": 33060}}
	projected := ProjectWebConfig(d, original, []ResolvedResource{next})
	if projected.HostPort != 13306 || projected.BindAddress != "0.0.0.0" || original.HostPort != 3306 || projected.Ports["admin"] != 33060 {
		t.Fatal("database projection changed unrelated or frozen configuration")
	}
	m.Definition.Management.Operations[0].Rebind = false
	merged, err = MergeManagementResourceClaims(m, []ResolvedResource{old, other}, []ResolvedResource{next})
	if err != nil || len(merged) != 3 {
		t.Fatal("ordinary command unexpectedly released old claim", merged, err)
	}
	m.Definition.Management.Operations[0].Rebind = true
	m.Definition.Management.Operations[0].ReadOnly = true
	merged, err = MergeManagementResourceClaims(m, []ResolvedResource{old, other}, []ResolvedResource{next})
	if err != nil || !reflect.DeepEqual(merged, []ResolvedResource{old, other}) {
		t.Fatal("read-only operation changed claims", err)
	}
}
