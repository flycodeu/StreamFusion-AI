//go:build linux

package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// This opt-in check runs only version commands from real pinned release bytes.
// It creates no account/unit, starts no service/listener, and invokes no package
// manager. The test owns and removes only its unique /opt temporary directory.
func TestNativeOfficialDebAndJDKVersionOnlyLinux(t *testing.T) {
	library := os.Getenv("SF_OPS_TEST_MATERIAL_LIBRARY")
	if library == "" {
		t.Skip("set SF_OPS_TEST_MATERIAL_LIBRARY to an official packaged sidecar")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root-owned /opt for the real protected-path checks")
	}
	manifestFile := filepath.Join(library, "library.json")
	info, err := os.Lstat(manifestFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		t.Fatal("invalid explicit library manifest", err)
	}
	raw, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	var manifest serviceinstall.MaterialLibrary
	if err = StrictJSON(raw, &manifest); err != nil || manifest.SchemaVersion != 1 {
		t.Fatal("invalid library schema", err)
	}
	selected := map[string]serviceinstall.LibraryMaterialFile{}
	for _, entry := range manifest.Materials {
		if entry.RuntimeMode == "NATIVE" && (entry.RecipeKey == "redis" && entry.RecipeVersion == "7.0.15-1ubuntu0.24.04.5" || entry.RecipeKey == "nginx" && entry.RecipeVersion == "1.24.0-2ubuntu7.19" || entry.RecipeKey == "java" && entry.RecipeVersion == "8") {
			if _, duplicate := selected[entry.RecipeKey]; duplicate {
				t.Fatal("duplicate official fixture recipe", entry.RecipeKey)
			}
			selected[entry.RecipeKey] = entry
		}
	}
	if len(selected) != 3 {
		t.Fatal("official sidecar must contain the pinned Redis, Nginx and JDK8 recipes")
	}
	keys := []string{"redis", "nginx", "java"}
	if os.Getenv("SF_OPS_TEST_SKIP_JDK") == "1" {
		keys = []string{"redis", "nginx"}
		t.Log("JDK runtime check explicitly omitted; no JDK execution evidence")
	}
	const prefix = "sfops-deb-verify-"
	temp, err := os.MkdirTemp("/opt", prefix)
	if err != nil {
		t.Fatal(err)
	}
	clean := filepath.Clean(temp)
	if filepath.Dir(clean) != "/opt" || !strings.HasPrefix(filepath.Base(clean), prefix) || len(filepath.Base(clean)) <= len(prefix) {
		t.Fatal("refusing unexpected cleanup target", clean)
	}
	t.Cleanup(func() {
		if filepath.Dir(clean) != "/opt" || !strings.HasPrefix(filepath.Base(clean), prefix) {
			t.Error("refusing changed cleanup target", clean)
			return
		}
		if err := os.RemoveAll(clean); err != nil {
			t.Error("test temporary directory cleanup failed", err)
		}
		if _, err := os.Lstat(clean); !os.IsNotExist(err) {
			t.Error("test temporary directory still exists", clean, err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			entry := selected[key]
			d, err := entry.Definition()
			if err != nil {
				t.Fatal(err)
			}
			work := filepath.Join(clean, key)
			root := filepath.Join(work, "program")
			job := filepath.Join(work, "material")
			if err = os.MkdirAll(job, 0700); err != nil {
				t.Fatal(err)
			}
			for _, directory := range []string{root, filepath.Join(root, "bin"), filepath.Join(root, "runtime")} {
				if err = os.Mkdir(directory, 0750); err != nil {
					t.Fatal(err)
				}
			}
			// Snapshot then validate the exact outer bundle. receiveNativeMaterial
			// repeats the real validation and splits only its original named parts.
			snapshot := filepath.Join(work, "source")
			if err = serviceinstall.CopyLibraryMaterial(ctx, library, snapshot, entry); err != nil {
				t.Fatal(err)
			}
			if _, err = serviceinstall.ValidateMaterial(snapshot, entry.SHA256, entry.ByteSize, d, "NATIVE"); err != nil {
				t.Fatal(err)
			}
			input, err := os.Open(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			x := &executor{installationInput: input}
			req := Request{Installation: &serviceinstall.Request{Definition: d, Config: serviceinstall.Config{RuntimeMode: "NATIVE"}, Source: serviceinstall.Source{Type: "ARCHIVE", SHA256: entry.SHA256, ByteSize: entry.ByteSize}}}
			parts, err := x.receiveNativeMaterial(req, job)
			if err != nil {
				t.Fatal(err)
			}
			if key == "java" {
				part := d.Native.Parts[0]
				err = extractNativePart(ctx, part, parts[part.Name], filepath.Join(root, "runtime"))
			} else {
				err = x.prepareDebProgram(ctx, req, root, parts)
			}
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(root, serviceinstall.NativeBinaryRelative(d))
			argument := "--version"
			if key == "nginx" {
				argument = "-v"
			} else if key == "java" {
				argument = "-version"
			}
			output, err := softwareCommand(ctx, x.softwareRunner, binary, argument)
			if err != nil {
				t.Fatalf("version command failed: %v: %s", err, output)
			}
			version := strings.TrimSpace(string(output))
			if key == "java" {
				if !installedToolVersionMatches(d, version) {
					t.Fatal("unexpected pinned JDK8 version", version)
				}
			} else if !nativeServiceVersionMatches(d, version) {
				t.Fatal("unexpected package binary version", version)
			}
			t.Logf("%s: %s", key, version)
			if key == "redis" {
				output, err = softwareCommand(ctx, x.softwareRunner, filepath.Join(root, "bin/redis-cli"), "--version")
				if err != nil || !strings.Contains(string(output), "redis-cli 7.0.15") {
					t.Fatalf("redis-cli version/loader failed: %v: %s", err, output)
				}
				t.Logf("redis-cli: %s", strings.TrimSpace(string(output)))
			}
		})
	}
}
