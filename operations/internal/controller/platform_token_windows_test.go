//go:build windows

package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func setPlatformTestACL(t *testing.T, path string, extra string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := "D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + user.User.Sid.String() + ")" + extra
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func protectPlatformTokenPath(t *testing.T, path string) {
	t.Helper()
	setPlatformTestACL(t, filepath.Dir(path), "")
	setPlatformTestACL(t, path, "")
}

func TestPlatformTokenRejectsBroadWindowsACL(t *testing.T) {
	_, b := setup(t)
	path := filepath.Join(t.TempDir(), "platform-token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	protectPlatformTokenPath(t, path)
	if err := b.h.ConfigurePlatformTokenFile(path); err != nil {
		t.Fatal(err)
	}
	setPlatformTestACL(t, path, "(A;;GR;;;WD)")
	if err := b.h.ConfigurePlatformTokenFile(path); err == nil {
		t.Fatal("Everyone-readable token accepted")
	}
	setPlatformTestACL(t, path, "")
	setPlatformTestACL(t, filepath.Dir(path), "(A;;GW;;;BU)")
	if err := b.h.ConfigurePlatformTokenFile(path); err == nil {
		t.Fatal("Users-writable token directory accepted")
	}
}
