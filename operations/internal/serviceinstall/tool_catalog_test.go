package serviceinstall

import (
	"encoding/json"
	"testing"
)

func TestAdditionalToolsExposeOnlySupportedChannels(t *testing.T) {
	for _, d := range additionalDefinitions() {
		c := Config{Name: d.Name, RuntimeMode: "NATIVE", MemoryMiB: 512}
		if c.Validate(d) != nil || d.Native == nil || len(d.Native.Parts) != 1 || NativeBinaryRelative(d) == "" {
			t.Fatal(d.Key)
		}
		c.RuntimeMode = "DOCKER"
		if d.Key == "python" {
			if err := c.Validate(d); err != nil {
				t.Fatal(err)
			}
		} else if c.Validate(d) == nil {
			t.Fatal("invented Docker channel", d.Key)
		}
		if UsesJavaBridge(d) {
			t.Fatal("new tool entered JAR bridge", d.Key)
		}
	}
	if _, err := Find("minio", "historical"); err == nil {
		t.Fatal("unavailable MinIO installation advertised")
	}
	for _, d := range install03Definitions() {
		before, _ := json.Marshal(d)
		after, err := Find(d.Key, d.Version)
		encoded, _ := json.Marshal(after)
		if err != nil || string(before) != string(encoded) {
			t.Fatal("frozen install03 definition changed", d.Key)
		}
	}
}
