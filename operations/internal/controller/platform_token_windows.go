//go:build windows

package controller

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A token is private only when both it and its containing directory exclude
// principals other than this process, SYSTEM and local Administrators.
func validatePlatformTokenAccess(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("PLATFORM_TOKEN_ACL_INVALID")
	}
	trusted := map[string]bool{
		user.User.Sid.String(): true,
		"S-1-5-18":             true, // SYSTEM
		"S-1-5-32-544":         true, // Administrators
	}
	for _, candidate := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(candidate)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("PLATFORM_TOKEN_ACL_INVALID")
		}
		sd, err := windows.GetNamedSecurityInfo(candidate, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return errors.New("PLATFORM_TOKEN_ACL_INVALID")
		}
		acl, _, err := sd.DACL()
		if err != nil || acl == nil {
			return errors.New("PLATFORM_TOKEN_ACL_INVALID")
		}
		for index := uint16(0); index < acl.AceCount; index++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, uint32(index), &ace); err != nil {
				return errors.New("PLATFORM_TOKEN_ACL_INVALID")
			}
			if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
				continue
			}
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !trusted[(*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()] {
				return errors.New("PLATFORM_TOKEN_ACL_INVALID")
			}
		}
	}
	return nil
}
