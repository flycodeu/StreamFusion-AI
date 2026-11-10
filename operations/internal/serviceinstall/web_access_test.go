package serviceinstall

import "testing"

func TestWebAccessRequiresDeclaredTCPPortAndPublicTextParameter(t *testing.T) {
	d := Definition{Kind: "SERVICE", Port: 8080, DefaultBindAddress: "127.0.0.1", Parameters: []ParameterDefinition{{Key: "PUBLIC_URL", Label: "对外访问地址", Type: "TEXT"}}, Web: &WebAccessDefinition{Scheme: "http", PortKey: "main", Path: "/label/", URLParameter: "PUBLIC_URL"}}
	if err := d.ValidateManagement(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"//outside.test", "/\\outside.test", "/%2foutside.test", "/%0a", "/%20", "/app?token=value", "/app#secret"} {
		d.Web.Path = path
		if d.ValidateManagement() == nil {
			t.Errorf("unsafe path accepted: %q", path)
		}
	}
	d.Web.Path = "/"
	d.Parameters[0].Type = "CREDENTIAL"
	if d.ValidateManagement() == nil {
		t.Fatal("credential role accepted as a browser address")
	}
	d.Parameters[0].Type = "TEXT"
	d.Ports = []PortDescriptor{{Key: "main", Protocol: "UDP"}}
	if d.ValidateManagement() == nil {
		t.Fatal("UDP accepted as HTTP transport")
	}
	d.Ports = nil
	d.DefaultBindAddress = "0.0.0.0"
	if err := d.ValidateManagement(); err != nil {
		t.Fatal("Web service wildcard default rejected", err)
	}
	for _, address := range []string{"::1", "224.0.0.1", "host.example", "127.0.0.1\n"} {
		d.DefaultBindAddress = address
		if d.ValidateManagement() == nil {
			t.Errorf("unsupported default binding accepted: %q", address)
		}
	}
}

func TestWebAccessURLValidationAppliesToInstallationAndDefaults(t *testing.T) {
	d := Definition{Kind: "SERVICE", Port: 8080, Parameters: []ParameterDefinition{{Key: "PUBLIC_URL", Label: "地址", Type: "TEXT"}}, Web: &WebAccessDefinition{Scheme: "http", URLParameter: "PUBLIC_URL"}}
	for _, address := range []string{"https://label.example/app/", "http://192.0.2.4:8080", "https://[2001:db8::1]:8443/label"} {
		if !ValidWebURL(address) || (Config{Parameters: map[string]string{"PUBLIC_URL": address}}).validateWebURL(d) != nil {
			t.Fatalf("valid address rejected: %q", address)
		}
	}
	for _, address := range []string{"javascript:alert(1)", "//host/path", "http://user:secret@host/", "https://host/?token=secret", "http://host/#secret", "http://host/?", "http://host/#", " http://host", "http://host/%0a", "http://host/has%20space", "http://host:0", "http://host:65536", "http://host:"} {
		if ValidWebURL(address) || (Config{Parameters: map[string]string{"PUBLIC_URL": address}}).validateWebURL(d) == nil {
			t.Errorf("unsafe address accepted: %q", address)
		}
	}
	d.Parameters[0].Default = "https://host/?token=secret"
	if d.ValidateManagement() == nil {
		t.Fatal("unsafe default browser address accepted")
	}
}
