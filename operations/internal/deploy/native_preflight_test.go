package deploy

import (
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestNativePlatformSupportedMultiDistro(t *testing.T) {
	jdk, err := serviceinstall.Find("java", "21")
	if err != nil {
		t.Fatal(err)
	}
	debRedis, err := serviceinstall.Find("redis", "7.0.15-1ubuntu0.24.04.5")
	if err != nil {
		t.Fatal(err)
	}

	distros := []struct {
		name      string
		osRelease string
		wantJDK   bool
		wantDEB   bool
	}{
		{"ubuntu-24.04", "ID=ubuntu\nVERSION_ID=\"24.04\"\n", true, true},
		{"ubuntu-22.04", "ID=ubuntu\nVERSION_ID=\"22.04\"\n", true, false},
		{"debian-12", "ID=debian\nVERSION_ID=\"12\"\n", true, false},
		{"rocky-9", "ID=\"rocky\"\nVERSION_ID=\"9.4\"\n", true, false},
		{"openeuler-24", "ID=\"openEuler\"\nVERSION_ID=\"24.03\"\n", true, false},
		{"empty", "", false, false},
	}

	for _, tc := range distros {
		t.Run(tc.name, func(t *testing.T) {
			if ok, _ := nativePlatformSupported("linux", "amd64", []byte(tc.osRelease), jdk); ok != tc.wantJDK {
				t.Fatalf("jdk support got %v, want %v", ok, tc.wantJDK)
			}
			if ok, _ := nativePlatformSupported("linux", "amd64", []byte(tc.osRelease), debRedis); ok != tc.wantDEB {
				t.Fatalf("deb support got %v, want %v", ok, tc.wantDEB)
			}
		})
	}

	if ok, _ := nativePlatformSupported("windows", "amd64", []byte("ID=ubuntu\nVERSION_ID=\"24.04\"\n"), jdk); ok {
		t.Fatal("non-linux OS must be rejected")
	}
	if ok, _ := nativePlatformSupported("linux", "arm64", []byte("ID=ubuntu\nVERSION_ID=\"24.04\"\n"), jdk); ok {
		t.Fatal("arm64 must be rejected until multi-arch materials exist")
	}
}
