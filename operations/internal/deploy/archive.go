package deploy

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// ValidateArchive reads without extracting or invoking Docker. Only one
// uncompressed Docker-save image in the sfops/ namespace is accepted.
func ValidateArchive(filename, expected string, size int64) (string, error) {
	return ValidateRecipeArchive(filename, expected, size, "java-compose-v1")
}

func ValidateRecipeArchive(filename, expected string, size int64, recipe string) (string, error) {
	return validateImageArchive(filename, expected, size, recipe, false)
}

func ValidateRuntimeArchive(filename, expected string, size int64) (string, error) {
	return validateImageArchive(filename, expected, size, "java-compose-v1", true)
}

func validateImageArchive(filename, expected string, size int64, recipe string, runtimeBase bool) (string, error) {
	if recipe != "java-compose-v1" && recipe != PreviewRecipe && recipe != ZLMRecipe {
		return "", errors.New("RECIPE_UNSUPPORTED")
	}
	if !Hex.MatchString(expected) || size < 1 || size > ArchiveLimitFor(recipe) {
		return "", errors.New("ARCHIVE_LIMIT_OR_HASH")
	}
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.Size() != size {
		return "", errors.New("ARCHIVE_SIZE_MISMATCH")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return "", errors.New("ARCHIVE_HASH_MISMATCH")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return "", err
	}
	tr := tar.NewReader(f)
	hashes := map[string]string{}
	small := map[string][]byte{}
	var total, metadataSize int64
	for count := 0; ; count++ {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", errors.New("ARCHIVE_CORRUPT")
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if count >= 10000 || name == "" || path.Clean(name) != name || path.IsAbs(name) || strings.Contains(name, "\\") || name == ".." || strings.HasPrefix(name, "../") || hdr.Size < 0 {
			return "", errors.New("ARCHIVE_UNSAFE_ENTRY")
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return "", errors.New("ARCHIVE_LINK_OR_SPECIAL_FILE")
		}
		if _, ok := hashes[name]; ok {
			return "", errors.New("ARCHIVE_DUPLICATE_ENTRY")
		}
		total += hdr.Size
		if total > size || hdr.Size > ArchiveLimitFor(recipe) {
			return "", errors.New("ARCHIVE_TOO_LARGE")
		}
		sum := sha256.New()
		if hdr.Size <= 4<<20 && (strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "blobs/sha256/")) {
			metadataSize += hdr.Size
			if metadataSize > 8<<20 {
				return "", errors.New("ARCHIVE_METADATA_TOO_LARGE")
			}
			data, e := io.ReadAll(io.TeeReader(tr, sum))
			if e != nil {
				return "", e
			}
			small[name] = data
		} else {
			if _, e = io.Copy(sum, tr); e != nil {
				return "", e
			}
		}
		hashes[name] = hex.EncodeToString(sum.Sum(nil))
	}
	var manifest []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}
	tagPattern := `^sfops/[a-z0-9][a-z0-9._/-]*:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`
	if recipe == PreviewRecipe {
		tagPattern = `^streamfusion/preview-node:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`
	} else if recipe == ZLMRecipe {
		tagPattern = `^streamfusion/zlm-direct:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`
	}
	if json.Unmarshal(small["manifest.json"], &manifest) != nil || len(manifest) != 1 || len(manifest[0].Layers) < 1 {
		return "", errors.New("ARCHIVE_SINGLE_MANAGED_IMAGE_REQUIRED")
	}
	if !runtimeBase && (len(manifest[0].RepoTags) != 1 || !regexp.MustCompile(tagPattern).MatchString(manifest[0].RepoTags[0])) {
		return "", errors.New("ARCHIVE_SINGLE_MANAGED_IMAGE_REQUIRED")
	}
	m := manifest[0]
	configData, ok := small[m.Config]
	if !ok {
		return "", errors.New("ARCHIVE_CONFIG_MISSING")
	}
	var config struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		RootFS       struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if json.Unmarshal(configData, &config) != nil || config.OS != "linux" || config.Architecture != "amd64" || len(config.RootFS.DiffIDs) != len(m.Layers) {
		return "", errors.New("ARCHIVE_PLATFORM_OR_LAYERS")
	}
	if recipe == PreviewRecipe && config.Config.Labels["com.streamfusion.preview.maintenance"] != "1" {
		return "", errors.New("PREVIEW_MAINTENANCE_IMAGE_REQUIRED")
	}
	if recipe == ZLMRecipe && config.Config.Labels["com.streamfusion.zlm.direct"] != "1" {
		return "", errors.New("ZLM_DIRECT_IMAGE_REQUIRED")
	}
	for i, layer := range m.Layers {
		if hashes[layer] == "" || "sha256:"+hashes[layer] != config.RootFS.DiffIDs[i] {
			return "", errors.New("ARCHIVE_LAYER_MISMATCH")
		}
	}
	if _, err = f.Seek(0, 0); err != nil {
		return "", err
	}
	tr = tar.NewReader(f)
	layers := make(map[string]bool, len(m.Layers))
	for _, layer := range m.Layers {
		layers[layer] = true
	}
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", errors.New("ARCHIVE_CORRUPT")
		}
		if layers[hdr.Name] {
			if e = validateLayer(tr, hdr.Size); e != nil {
				return "", e
			}
		}
	}
	return "sha256:" + hashes[m.Config], nil
}

// Docker consumes these nested archives. A matching diffID alone also accepts
// arbitrary bytes, so verify the uncompressed tar structure before image load.
func validateLayer(input io.Reader, size int64) error {
	if size < 1024 || size%512 != 0 {
		return errors.New("ARCHIVE_LAYER_INVALID")
	}
	r := tar.NewReader(input)
	for count := 0; ; count++ {
		hdr, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil || count >= 1000000 || hdr.Size < 0 || hdr.Size > size {
			return errors.New("ARCHIVE_LAYER_INVALID")
		}
		if _, err = io.Copy(io.Discard, r); err != nil {
			return errors.New("ARCHIVE_LAYER_INVALID")
		}
	}
}

func archiveTag(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := tar.NewReader(f)
	for {
		hdr, err := r.Next()
		if err != nil {
			return "", errors.New("ARCHIVE_MANIFEST_MISSING")
		}
		if hdr.Name == "manifest.json" {
			var manifest []struct{ RepoTags []string }
			b, err := io.ReadAll(io.LimitReader(r, 4<<20))
			if err != nil || json.Unmarshal(b, &manifest) != nil || len(manifest) != 1 || len(manifest[0].RepoTags) != 1 {
				return "", errors.New("ARCHIVE_MANIFEST_INVALID")
			}
			return manifest[0].RepoTags[0], nil
		}
	}
}
