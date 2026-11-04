package serviceinstall

import (
	"strings"
	"testing"
)

func TestNodeCatalogKeepsHistoricalRecipesAndPinsMaterials(t *testing.T) {
	if len(Catalog()) != 18 {
		t.Fatal("historical catalog changed")
	}
	for _, d := range append(NodeCatalog(), NodeEnvironmentRecipes()...) {
		if ValidateDefinition(d) != nil || d.Native == nil || len(d.Native.Parts) == 0 {
			t.Fatal(d.Key)
		}
		for _, part := range d.Native.Parts {
			if !sha.MatchString(part.SHA256) || part.ByteSize < 1 || !strings.HasPrefix(part.URL, "https://") {
				t.Fatal(part)
			}
		}
		copy := d
		copy.Revision += "-injected"
		if ValidateDefinition(copy) == nil {
			t.Fatal("untrusted Node recipe accepted")
		}
	}
}
func TestNodeContextRejectsRootAndBroadPaths(t *testing.T) {
	n := NodeContext{EnvironmentID: "11111111-1111-4111-8111-111111111111", EnvironmentVersion: "1", Account: "ubuntu", AccountUID: "1000", RootPath: "/home/ubuntu/.local/share/streamfusion-ops/node/11111111-1111-4111-8111-111111111111", NVMVersion: NodeNVMVersion, NVMHash: NodeNVMHash}
	if n.Validate() != nil {
		t.Fatal(n.Validate())
	}
	for _, root := range []string{"/", "/usr", "/home/ubuntu/.nvm", n.RootPath + "/../11111111-1111-4111-8111-111111111111"} {
		changed := n
		changed.RootPath = root
		if changed.Validate() == nil {
			t.Fatal(root)
		}
	}
	n.Account = "root"
	if n.Validate() == nil {
		t.Fatal("root accepted")
	}
}

func TestNodeManagerTrustRetainsFixedIdentity(t *testing.T) {
	if !TrustedNodeManager("0.40.8", "a64806abf6aab85c456f9ffa2a3be737e35c3a1e6ac99a44ea20c69bdc923467") {
		t.Fatal("historical manager recipe removed")
	}
	if TrustedNodeManager("0.40.8", strings.Repeat("0", 64)) || TrustedNodeManager("999.0.0", NodeNVMHash) {
		t.Fatal("untrusted manager accepted")
	}
}
