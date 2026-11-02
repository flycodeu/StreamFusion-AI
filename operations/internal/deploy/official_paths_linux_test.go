package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// No system files are created: check missing unit names against the actual
// distro directories, including Ubuntu's standard merged-/usr alias.
func TestOfficialStandardSystemdDirectories(t *testing.T) {
	if _, err := os.Stat("/usr/lib/systemd/system"); os.IsNotExist(err) {
		t.Skip("systemd directory unavailable")
	}
	name := "sfops-path-check-" + uuid.NewString()
	spec := serviceinstall.OfficialSpec{Paths: serviceinstall.OfficialPaths{Binary: "/usr/bin/" + name, Unit: name + ".service"}}
	if err := officialInstallAbsent(spec, map[string]string{}); err != nil {
		t.Fatal(officialExistingMessage(err))
	}
	if info, err := os.Lstat("/lib"); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if secureAncestors("/lib/systemd/system") == nil {
			t.Fatal("global symlink protection was relaxed")
		}
	}
}

func TestOfficialPathChecksPreserveExistingAndRejectWritableParents(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "missing-program")
	spec := serviceinstall.OfficialSpec{Paths: serviceinstall.OfficialPaths{Binary: path}}
	err := officialInstallAbsent(spec, nil)
	if err == nil || err.Error() != "OFFICIAL_PATH_NOT_PROTECTED" || !strings.Contains(officialExistingMessage(err), path) {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	err = officialInstallAbsent(spec, nil)
	if err == nil || err.Error() != "OFFICIAL_EXISTING_CONFIGURATION_OR_DATA" {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "existing" {
		t.Fatal("existing file changed")
	}
	link := filepath.Join(directory, "alias")
	if err := os.Symlink("/usr/lib", link); err != nil {
		t.Fatal(err)
	}
	if protectedOfficialParent(filepath.Join(link, "systemd/system/missing.service")) == nil {
		t.Fatal("arbitrary alias accepted")
	}
}
