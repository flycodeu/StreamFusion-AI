package serviceinstall

import "testing"

func TestWebWildcardBindingKeepsNonWebAndFrozenContracts(t *testing.T) {
	var nginx, mysql Definition
	for _, d := range BuiltinCommandCatalog() {
		if d.Key == "nginx" && d.Version == "1.30.6" {
			nginx = d
		}
		if d.Key == "mysql" && d.Version == "8.4.11" {
			mysql = d
		}
	}
	config := Config{Name: "web", BindAddress: "0.0.0.0", HostPort: 18080, MemoryMiB: 512}
	if nginx.Web == nil || nginx.DefaultBindAddress != "0.0.0.0" || config.Validate(nginx) != nil || ValidateDefinition(nginx) != nil {
		t.Fatal("current Nginx cannot bind its published Web default")
	}
	for _, address := range []string{"224.0.0.1", "not-an-ip", "::", "127.0.0.1\n"} {
		config.BindAddress = address
		if config.Validate(nginx) == nil {
			t.Fatal("invalid Web binding accepted", address)
		}
	}
	config.BindAddress, config.CredentialID, config.CredentialRevision = "0.0.0.0", testCredential, "1"
	if mysql.Web != nil || config.Validate(mysql) == nil {
		t.Fatal("database binding boundary changed")
	}
	old, err := Find("nginx", "1.30.6")
	config.BindAddress, config.CredentialID, config.CredentialRevision = "127.0.0.1", "", ""
	if err != nil || old.Web != nil || old.DefaultBindAddress != "" || config.Validate(old) != nil || ValidateDefinition(old) != nil {
		t.Fatal("frozen catalog contract was rewritten", err)
	}
}
