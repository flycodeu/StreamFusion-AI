package serviceinstall

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strings"
)

// ValidateArchive accepts one Docker-save image (plain or gzip-compressed image
// layers), without extracting files or invoking Docker. A pure OCI index without
// the Docker manifest is intentionally unsupported. Registry and archive SHA differ:
// saved images often have no RepoDigest, so the trusted config image ID and each
// rootfs diffID are checked against actual bytes before Docker can consume them.
func ValidateArchive(filename, expectedSHA string, size int64, d Definition) (string, error) {
	if err := ValidateDefinition(d); err != nil {
		return "", err
	}
	return validateArchive(filename, expectedSHA, size, d)
}

func validateArchive(filename, expectedSHA string, size int64, d Definition) (string, error) {
	if !sha.MatchString(expectedSHA) || size < 1 || size > ArchiveLimit {
		return "", errors.New("SERVICE_ARCHIVE_LIMIT_OR_HASH")
	}
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return "", errors.New("SERVICE_ARCHIVE_SIZE_MISMATCH")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != expectedSHA {
		return "", errors.New("SERVICE_ARCHIVE_HASH_MISMATCH")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return "", err
	}
	reader := tar.NewReader(f)
	hashes := map[string]string{}
	sizes := map[string]int64{}
	seen := map[string]bool{}
	var manifestBytes []byte
	var total int64
	for count := 0; ; count++ {
		hdr, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return "", errors.New("SERVICE_ARCHIVE_CORRUPT")
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if count >= 20000 || name == "" || len(name) > 512 || path.Clean(name) != name || path.IsAbs(name) || strings.ContainsAny(name, "\\\r\n\x00") || name == ".." || strings.HasPrefix(name, "../") || seen[name] || hdr.Size < 0 {
			return "", errors.New("SERVICE_ARCHIVE_UNSAFE_ENTRY")
		}
		seen[name] = true
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return "", errors.New("SERVICE_ARCHIVE_LINK_OR_SPECIAL")
		}
		total += hdr.Size
		if hdr.Size > size || total > size {
			return "", errors.New("SERVICE_ARCHIVE_TOO_LARGE")
		}
		sum := sha256.New()
		if name == "manifest.json" {
			if hdr.Size > 2<<20 {
				return "", errors.New("SERVICE_ARCHIVE_METADATA_LIMIT")
			}
			manifestBytes, err = io.ReadAll(io.TeeReader(reader, sum))
		} else {
			_, err = io.Copy(sum, reader)
		}
		if err != nil {
			return "", err
		}
		hashes[name], sizes[name] = hex.EncodeToString(sum.Sum(nil)), hdr.Size
	}
	var manifest []struct {
		Config   string   `json:"Config"`
		Layers   []string `json:"Layers"`
		RepoTags []string `json:"RepoTags"`
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil || len(manifest) != 1 || len(manifest[0].Layers) < 1 || len(manifest[0].Layers) > 256 {
		return "", errors.New("SERVICE_ARCHIVE_SINGLE_DOCKER_IMAGE_REQUIRED")
	}
	m := manifest[0]
	if len(m.RepoTags) > 1 {
		return "", errors.New("SERVICE_ARCHIVE_TAG_UNTRUSTED")
	}
	for _, tag := range m.RepoTags {
		normalize := func(value string) string {
			return strings.TrimPrefix(strings.TrimPrefix(value, "docker.io/"), "library/")
		}
		image := normalize(d.Image)
		colon := strings.LastIndex(image, ":")
		if colon < 1 || normalize(tag) != image && normalize(tag) != image[:colon]+":"+d.Version {
			return "", errors.New("SERVICE_ARCHIVE_TAG_UNTRUSTED")
		}
	}
	if sizes[m.Config] < 1 || sizes[m.Config] > 4<<20 || "sha256:"+hashes[m.Config] != d.ImageID {
		return "", errors.New("SERVICE_ARCHIVE_IMAGE_MISMATCH")
	}
	layers := map[string]bool{}
	for _, layer := range m.Layers {
		if hashes[layer] == "" || layer == m.Config || layer == "manifest.json" {
			return "", errors.New("SERVICE_ARCHIVE_LAYER_MISSING")
		}
		layers[layer] = true
	}
	if _, err = f.Seek(0, 0); err != nil {
		return "", err
	}
	reader = tar.NewReader(f)
	layerHashes := map[string]string{}
	var expanded int64
	var config struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		RootFS       struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	for {
		hdr, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return "", errors.New("SERVICE_ARCHIVE_CORRUPT")
		}
		if hdr.Name == m.Config {
			data, readErr := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
			if readErr != nil || len(data) > 4<<20 || json.Unmarshal(data, &config) != nil {
				return "", errors.New("SERVICE_ARCHIVE_CONFIG_INVALID")
			}
		}
		if layers[hdr.Name] {
			var count int64
			layerHashes[hdr.Name], count, err = layerDigest(reader, hdr.Size, (8<<30)-expanded)
			if err != nil {
				return "", err
			}
			expanded += count
		}
	}
	if config.OS != "linux" || config.Architecture != "amd64" || len(config.RootFS.DiffIDs) != len(m.Layers) {
		return "", errors.New("SERVICE_ARCHIVE_PLATFORM_OR_LAYERS")
	}
	for i, layer := range m.Layers {
		if "sha256:"+layerHashes[layer] != config.RootFS.DiffIDs[i] {
			return "", errors.New("SERVICE_ARCHIVE_LAYER_HASH_MISMATCH")
		}
	}
	return d.ImageID, nil
}

func layerDigest(input io.Reader, size, budget int64) (string, int64, error) {
	if size < 1 || budget < 1024 {
		return "", 0, errors.New("SERVICE_ARCHIVE_EXPANSION_LIMIT")
	}
	buffer := bufio.NewReader(input)
	header, err := buffer.Peek(2)
	if err != nil {
		return "", 0, errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
	}
	var source io.Reader = buffer
	var compressed *gzip.Reader
	if header[0] == 0x1f && header[1] == 0x8b {
		compressed, err = gzip.NewReader(buffer)
		if err != nil {
			return "", 0, errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
		}
		defer compressed.Close()
		compressed.Multistream(false)
		source = compressed
		budget = min(budget, size*200)
	} else if size%512 != 0 {
		return "", 0, errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
	}
	limited := &io.LimitedReader{R: source, N: budget + 1}
	hash := sha256.New()
	tee := io.TeeReader(limited, hash)
	if err = validateLayer(tee, budget); err != nil {
		return "", 0, err
	}
	if _, err = io.Copy(io.Discard, tee); err != nil {
		return "", 0, errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
	}
	count := budget + 1 - limited.N
	if count > budget {
		return "", 0, errors.New("SERVICE_ARCHIVE_EXPANSION_LIMIT")
	}
	if count < 1024 || count%512 != 0 {
		return "", 0, errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
	}
	if compressed != nil {
		if _, err = buffer.ReadByte(); err != io.EOF {
			return "", 0, errors.New("SERVICE_ARCHIVE_LAYER_TRAILING_DATA")
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

func validateLayer(input io.Reader, size int64) error {
	if size < 1024 {
		return errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
	}
	r := tar.NewReader(input)
	for count := 0; ; count++ {
		hdr, err := r.Next()
		if err == io.EOF {
			return nil
		}
		// Symlinks inside a pinned image layer are legitimate. Docker, rather
		// than the Ops filesystem, will interpret these nested archive entries.
		if err != nil || count >= 1000000 || hdr.Size < 0 || hdr.Size > size {
			return errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
		}
		if _, err = io.Copy(io.Discard, r); err != nil {
			return errors.New("SERVICE_ARCHIVE_LAYER_INVALID")
		}
	}
}
