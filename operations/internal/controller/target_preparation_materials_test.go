package controller

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestPreparationMaterialCachePinsExactPairAndRejectsMissingOrDamagedFiles(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	primary := targetPreparationFixture(t)
	original, err := readTargetPackage(primary)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := s.cachePreparationMaterial(original)
	if err != nil || cached.Binary.Path == original.Binary.Path {
		t.Fatal("original materials were not preserved", err)
	}
	before, _ := os.ReadFile(cached.Binary.Path)
	current := append([]byte(nil), before...)
	current[len(current)-1]++
	helper, _ := os.ReadFile(original.Script.Path)
	if err = os.WriteFile(original.Binary.Path, current, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(primary, "SHA256SUMS"), []byte(fmt.Sprintf("%x  streamfusion-ops\n%x  install-target.sh\n", sha256.Sum256(current), sha256.Sum256(helper))), 0644); err != nil {
		t.Fatal(err)
	}
	read, err := s.frozenPreparationMaterial(primary, original.Binary.Hash, original.Script.Hash)
	if err != nil || read.Binary.Path != cached.Binary.Path {
		t.Fatal("new primary release replaced the frozen pair", err)
	}
	if again, err := s.cachePreparationMaterial(original); err != nil || again.Binary.Path != cached.Binary.Path {
		t.Fatal("repeated cache changed the pair", err)
	}
	missing := &Store{Dir: t.TempDir()}
	if _, err = missing.frozenPreparationMaterial(primary, original.Binary.Hash, original.Script.Hash); err == nil || err.Error() != "PREPARATION_RECOVERY_MATERIAL_MISSING" {
		t.Fatal("missing frozen bytes were replaced by the current release", err)
	}
	if err = os.WriteFile(cached.Script.Path, []byte("tampered helper"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.frozenPreparationMaterial(primary, original.Binary.Hash, original.Script.Hash); err == nil || err.Error() != "PREPARATION_RECOVERY_MATERIAL_DAMAGED" {
		t.Fatal("tampered cache accepted", err)
	}
	if _, err = s.cachePreparationMaterial(original); err == nil {
		t.Fatal("tampered cache silently overwritten")
	}
}

func TestPreparationMaterialCacheRejectsChangedSourceBeforePublishing(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	material, err := readTargetPackage(targetPreparationFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(material.Script.Path, []byte("changed"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cachePreparationMaterial(material); err == nil {
		t.Fatal("changed source was cached")
	}
	entries, err := os.ReadDir(filepath.Join(s.Dir, "preparation-materials"))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed cache left a published or partial pair", err)
	}
}

func TestPreparationRecoveryUsesFrozenPairAfterControllerReleaseChanges(t *testing.T) {
	s, b := setup(t)
	b.login()
	oldDirectory := targetPreparationFixture(t)
	old, err := readTargetPackage(oldDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cachePreparationMaterial(old); err != nil {
		t.Fatal(err)
	}
	b.h.targetPackageDir = targetPreparationFixture(t)
	data, _ := os.ReadFile(filepath.Join(b.h.targetPackageDir, "streamfusion-ops"))
	data[len(data)-1]++
	helper, _ := os.ReadFile(filepath.Join(b.h.targetPackageDir, "install-target.sh"))
	os.WriteFile(filepath.Join(b.h.targetPackageDir, "streamfusion-ops"), data, 0755)
	os.WriteFile(filepath.Join(b.h.targetPackageDir, "SHA256SUMS"), []byte(fmt.Sprintf("%x  streamfusion-ops\n%x  install-target.sh\n", sha256.Sum256(data), sha256.Sum256(helper))), 0644)
	f := hostSSHServer(t, s, b, true, false)
	host := f.create(t, b, "recover-cached-release")
	target, _ := s.readTarget(host["id"].(string))
	snapshot := preparationSnapshot{Host: target, BinarySHA: strings.Repeat("a", 64), ScriptSHA: strings.Repeat("b", 64), RecoveryBinarySHA: old.Binary.Hash, RecoveryScriptSHA: old.Script.Hash}
	job, _, err := b.h.acceptPreparation(preparationActor(t, b), uuid.NewString(), "cached-recovery", snapshot, "cached-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.h.finishPreparation(job, "UNKNOWN", "PREPARATION_RESULT_UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&before); err != nil {
		t.Fatal(err)
	}
	expectedBinary, err := os.ReadFile(old.Binary.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"new-primary", "missing-primary"} {
		if scenario == "missing-primary" {
			if err = os.Remove(filepath.Join(b.h.targetPackageDir, "streamfusion-ops")); err != nil {
				t.Fatal(err)
			}
		}
		preparation := dataOf(t, b.call("GET", "/hosts/"+target.ID+"/preparation", nil, 200))
		pack := preparation["package"].(map[string]any)
		if pack["available"] != true || pack["sha256"] != old.Binary.Hash || pack["byteSize"] != float64(old.Binary.Size) || pack["downloadUrl"] == "" {
			t.Fatal("recovery metadata does not describe the frozen package", scenario, pack)
		}
		packageReady := false
		for _, step := range preparation["steps"].([]any) {
			item := step.(map[string]any)
			if item["key"] == "package" {
				packageReady = item["status"] == "AVAILABLE" && item["code"] == "TARGET_PACKAGE_READY"
			}
		}
		if !packageReady {
			t.Fatal("cached package metadata disagrees with the installation step", scenario)
		}
	}
	download := b.call("GET", "/hosts/"+target.ID+"/preparation/download?expectedVersion="+target.Version, nil, 200)
	if !bytes.Equal(targetArchiveEntry(t, download.Body.Bytes(), "streamfusion-ops"), expectedBinary) {
		t.Fatal("offline recovery download did not preserve the frozen pair")
	}
	var uploaded atomic.Pointer[[]byte]
	handler := func(command string) *hostSSHResponse {
		switch {
		case strings.Contains(command, "&& cat >"):
			return &hostSSHResponse{Input: func(input []byte) { copyOfInput := append([]byte(nil), input...); uploaded.Store(&copyOfInput) }}
		case strings.Contains(command, "SFOPS_SETUP_ENTERED"):
			if !strings.Contains(command, "/usr/bin/sudo -n -- ") {
				t.Error("ordinary SSH account bypassed sudo")
			}
			return &hostSSHResponse{Stdout: "SFOPS_SETUP_READY\n"}
		case strings.HasPrefix(command, "sha256sum -- "):
			return &hostSSHResponse{Stdout: old.Binary.Hash + "  " + target.InstallRoot + "/executor\n"}
		}
		return nil
	}
	f.Responses.Store(&handler)
	result := dataOf(t, b.call("POST", "/hosts/"+target.ID+"/preparation/repair", preparationRepairInput{JobID: job, ExpectedVersion: target.Version, Confirmed: true}, 200))
	if result["id"] != job || result["status"] != "SUCCEEDED" || uploaded.Load() == nil {
		t.Fatal("original job was not recovered", result)
	}
	if !bytes.Equal(targetArchiveEntry(t, *uploaded.Load(), "streamfusion-ops"), expectedBinary) {
		t.Fatal("uploaded current release in place of frozen bytes")
	}
	var after string
	if err = s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&after); err != nil || after != before {
		t.Fatal("frozen original or recovery identity changed", err)
	}
}
