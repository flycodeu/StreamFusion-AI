package controller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestYtlabelPython312RecipesKeepUpstreamSourceAndSeparateRequirements(t *testing.T) {
	original := deploy.Digest(ytlabelDefinitions())
	definitions := ytlabelPython312Definitions()
	if len(definitions) != 2 {
		t.Fatal("expected online and offline compatibility recipes")
	}
	for _, d := range definitions {
		if err := d.ValidateCommands(); err != nil {
			t.Fatal(d.Key, err)
		}
		if !strings.HasPrefix(d.Key, "custom-ytlabel-py312-native-") || d.CatalogKey != "ytlabel" || d.Version != "4.0-python312" || d.Parameters[0].Default != "/usr/bin/python3" {
			t.Fatalf("wrong compatibility identity: %+v", d)
		}
		for _, step := range d.Commands.Steps {
			if strings.Contains(step.Script, "\r\n") {
				t.Fatal("compatibility recipe contains CRLF")
			}
			command := exec.Command(additionalTestBash(t), "-n")
			command.Stdin = strings.NewReader(step.Script)
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s %s: %v: %s", d.Key, step.Name, err, out)
			}
		}
		prepare, material := d.Commands.Steps[0].Script, d.Commands.Steps[1].Script
		if !strings.Contains(prepare, "sys.version_info[:2] != (3, 12)") {
			t.Fatal("compatibility recipe does not require Python 3.12")
		}
		for _, required := range []string{ytlabelSourceURL, ytlabelSourceSHA256, "opencv-python==4.10.0.84", "psutil==5.9.8", "requirements.upstream.txt", "requirements.effective.txt", `readable_material=("$root/runtime/source" "$root/config/requirements.upstream.txt" "$root/config/requirements.effective.txt")`, `--no-index --find-links "$root/runtime/wheelhouse"`, "--only-binary=:all:", "runuser -u"} {
			if !strings.Contains(material, required) {
				t.Fatalf("missing explicit material boundary: %s", required)
			}
		}
		if strings.Contains(material, `-r "$root/runtime/source/requirements.txt"`) || strings.Count(material, `-r "$root/config/requirements.effective.txt"`) != 2 {
			t.Fatal("online/offline pip does not use the effective requirements")
		}
	}
	if original != deploy.Digest(ytlabelDefinitions()) {
		t.Fatal("compatibility recipe changed the original recipe")
	}
}

func TestYtlabelPython312RequirementsPreserveUpstreamAndRejectUnexpectedPins(t *testing.T) {
	python := ytlabelTestPython(t)
	for _, test := range []struct {
		name, upstream string
		valid          bool
	}{
		{"exact upstream", "# source requirements\nFlask==3.1.2\nopencv-python==4.5.4.60\npsutil==5.9.1\nnumpy==1.26.4\n", true},
		{"upstream changed", "opencv-python==4.6.0\npsutil==5.9.1\n", false},
		{"duplicate pin", "opencv-python==4.5.4.60\npsutil==5.9.1\npsutil==5.9.1\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "requirements.txt")
			if err := os.WriteFile(source, []byte(test.upstream), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(python, "-X", "utf8", "-I", "-", source, root)
			command.Stdin = strings.NewReader(ytlabelSource("py312-requirements.py"))
			out, err := command.CombinedOutput()
			if !test.valid {
				if err == nil || !strings.Contains(string(out), "上游依赖定义已变化") {
					t.Fatalf("unexpected upstream pins accepted: %v: %s", err, out)
				}
				if _, err := os.Stat(filepath.Join(root, "requirements.effective.txt")); !os.IsNotExist(err) {
					t.Fatal("failed validation wrote effective requirements")
				}
				return
			}
			if err != nil {
				t.Fatalf("requirements generation failed: %v: %s", err, out)
			}
			for _, file := range []string{source, filepath.Join(root, "requirements.upstream.txt")} {
				data, err := os.ReadFile(file)
				if err != nil || string(data) != test.upstream {
					t.Fatal("upstream requirements changed", err)
				}
			}
			actual, err := os.ReadFile(filepath.Join(root, "requirements.effective.txt"))
			wanted := strings.NewReplacer("opencv-python==4.5.4.60", "opencv-python==4.10.0.84", "psutil==5.9.1", "psutil==5.9.8").Replace(test.upstream)
			if err != nil || string(actual) != wanted || !strings.Contains(string(out), "opencv-python==4.5.4.60 → opencv-python==4.10.0.84") || !strings.Contains(string(out), "psutil==5.9.1 → psutil==5.9.8") {
				t.Fatalf("dependency adjustment was not exact and visible: %v: %s\n%s", err, actual, out)
			}
		})
	}
}
