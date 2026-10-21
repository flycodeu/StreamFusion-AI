package serviceinstall

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
)

func ValidateMaterial(file, expectedSHA string, size int64, d Definition, mode string) (string, error) {
	if mode == "" || mode == "DOCKER" {
		return ValidateArchive(file, expectedSHA, size, d)
	}
	if mode != "NATIVE" || d.Native == nil || ValidateDefinition(d) != nil || !sha.MatchString(expectedSHA) || size < 1 || size > ArchiveLimit {
		return "", errors.New("NATIVE_MATERIAL_INVALID")
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return "", errors.New("NATIVE_MATERIAL_SIZE_MISMATCH")
	}
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return "", err
	}
	if hex.EncodeToString(sum.Sum(nil)) != expectedSHA {
		return "", errors.New("NATIVE_MATERIAL_HASH_MISMATCH")
	}
	if len(d.Native.Parts) == 1 {
		p := d.Native.Parts[0]
		if p.SHA256 != expectedSHA || p.ByteSize > 0 && p.ByteSize != size {
			return "", errors.New("NATIVE_VENDOR_MATERIAL_MISMATCH")
		}
		return "native:" + expectedSHA, nil
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	wanted := map[string]NativePart{}
	for _, p := range d.Native.Parts {
		wanted[p.Name] = p
	}
	seen := map[string]bool{}
	reader := tar.NewReader(f)
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", errors.New("NATIVE_BUNDLE_CORRUPT")
		}
		p, ok := wanted[h.Name]
		if !ok || seen[h.Name] || path.Base(h.Name) != h.Name || h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA || h.Size < 1 || h.Size > ArchiveLimit || p.ByteSize > 0 && p.ByteSize != h.Size {
			return "", errors.New("NATIVE_BUNDLE_UNEXPECTED_PART")
		}
		sum := sha256.New()
		if _, err = io.Copy(sum, reader); err != nil {
			return "", err
		}
		if hex.EncodeToString(sum.Sum(nil)) != p.SHA256 {
			return "", errors.New("NATIVE_BUNDLE_PART_HASH_MISMATCH")
		}
		seen[h.Name] = true
	}
	if len(seen) != len(wanted) {
		return "", errors.New("NATIVE_BUNDLE_INCOMPLETE")
	}
	return "native:" + expectedSHA, nil
}
