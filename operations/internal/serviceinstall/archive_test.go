package serviceinstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	name string
	data []byte
	kind byte
}

func tarBytes(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := tar.NewWriter(&buffer)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: entry.name, Mode: 0600, Typeflag: kind, Size: int64(len(entry.data))}
		if kind == tar.TypeSymlink {
			h.Linkname, h.Size = "/etc/passwd", 0
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func rawHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func imageFixture(t *testing.T, kind string) ([]archiveEntry, Definition) {
	t.Helper()
	layer := tarBytes(t, []archiveEntry{{name: "usr/bin/service", data: []byte("pinned executable")}})
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + rawHash(layer)}}})
	if kind == "platform" {
		config = bytes.ReplaceAll(config, []byte("amd64"), []byte("arm64"))
	}
	d := Definition{Key: "fixture", Version: "1.0", Image: "docker.io/library/fixture:1.0", ImageID: "sha256:" + rawHash(config)}
	manifest := []map[string]any{{"Config": "config.json", "RepoTags": []string{"fixture:1.0"}, "Layers": []string{"layer.tar"}}}
	if kind == "multi" {
		manifest = append(manifest, manifest[0])
	}
	if kind == "tag" {
		manifest[0]["RepoTags"] = []string{"unrelated/production:latest"}
	}
	manifestData, _ := json.Marshal(manifest)
	entries := []archiveEntry{{name: "manifest.json", data: manifestData}, {name: "config.json", data: config}, {name: "layer.tar", data: layer}}
	switch kind {
	case "layer":
		entries[2].data = tarBytes(t, []archiveEntry{{name: "usr/bin/service", data: []byte("tampered executable")}})
	case "config":
		entries[1].data = append(config, ' ')
	case "duplicate":
		entries = append(entries, entries[2])
	case "traversal":
		entries = append(entries, archiveEntry{name: "../outside", data: []byte("x")})
	case "link":
		entries = append(entries, archiveEntry{name: "link", kind: tar.TypeSymlink})
	case "invalid-layer":
		entries[2].data = bytes.Repeat([]byte("x"), 1024)
		config, _ = json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"diff_ids": []string{"sha256:" + rawHash(entries[2].data)}}})
		entries[1].data, d.ImageID = config, "sha256:"+rawHash(config)
	case "gzip", "gzip-trailing", "gzip-corrupt":
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(layer); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		entries[2].data = compressed.Bytes()
		if kind == "gzip-trailing" {
			entries[2].data = append(entries[2].data, []byte("untrusted trailing member")...)
		}
		if kind == "gzip-corrupt" {
			entries[2].data[len(entries[2].data)-1] ^= 1
		}
	}
	return entries, d
}

func TestServiceArchiveValidatesBytesIdentityAndNestedLayers(t *testing.T) {
	for _, kind := range []string{"valid", "gzip", "gzip-trailing", "gzip-corrupt", "layer", "config", "multi", "platform", "duplicate", "traversal", "link", "invalid-layer", "tag"} {
		t.Run(kind, func(t *testing.T) {
			entries, d := imageFixture(t, kind)
			data := tarBytes(t, entries)
			file := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			id, err := validateArchive(file, rawHash(data), int64(len(data)), d)
			if kind == "valid" || kind == "gzip" {
				if err != nil || id != d.ImageID {
					t.Fatalf("valid image rejected: %q %v", id, err)
				}
			} else if err == nil {
				t.Fatal("malformed or mutated image accepted")
			}
		})
	}
}

func TestCompressedServiceLayerHasExpansionLimit(t *testing.T) {
	layer := tarBytes(t, []archiveEntry{{name: "large-empty-file", data: make([]byte, 2<<20)}})
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(layer)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := layerDigest(bytes.NewReader(compressed.Bytes()), int64(compressed.Len()), 8<<30); err == nil {
		t.Fatal("over-compressed layer exceeded ratio without rejection")
	}
}

func TestServiceArchiveRejectsWrongHashSizeAndUntrustedDefinition(t *testing.T) {
	entries, d := imageFixture(t, "valid")
	data := tarBytes(t, entries)
	file := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		hash string
		size int64
	}{{strings.Repeat("a", 64), int64(len(data))}, {rawHash(data), 1}, {rawHash(data), ArchiveLimit + 1}} {
		if _, err := validateArchive(file, test.hash, test.size, d); err == nil {
			t.Fatal("wrong hash/size accepted")
		}
	}
	if _, err := ValidateArchive(file, rawHash(data), int64(len(data)), d); err == nil {
		t.Fatal("untrusted recipe accepted")
	}
}
