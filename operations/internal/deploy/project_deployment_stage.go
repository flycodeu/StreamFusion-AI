package deploy

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func projectSecureDir(dir string, mode os.FileMode) error {
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() {
			return errors.New("PROJECT_RUNTIME_PATH_UNSAFE")
		}
		return secureAncestors(dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return errors.New("PROJECT_RUNTIME_PATH_UNSAFE")
	}
	if err := projectSecureDir(parent, 0755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, mode); err != nil {
		return err
	}
	return os.Chmod(dir, mode)
}
func projectAtomicFile(filename string, data []byte, mode os.FileMode) error {
	if err := secureAncestors(filepath.Dir(filename)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(filename), ".sfops-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if info, statErr := os.Lstat(filename); statErr == nil {
		if !info.Mode().IsRegular() || secureAncestors(filename) != nil {
			return errors.New("PROJECT_RUNTIME_PATH_UNSAFE")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err = os.Rename(f.Name(), filename); err != nil {
		return err
	}
	return projectSyncDir(filepath.Dir(filename))
}
func projectReadSmall(filename string, max int64) ([]byte, error) {
	if err := secureNearestAncestor(filepath.Dir(filename)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, errors.New("PROJECT_FILE_INVALID")
	}
	if err := secureAncestors(filename); err != nil {
		return nil, err
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(data)) > max {
		return nil, errors.New("PROJECT_FILE_INVALID")
	}
	return data, err
}
func projectSyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func projectReceiveArtifact(src io.Reader, dst string, p ProjectDeployment) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(src, p.ArtifactSize+1))
	if err != nil || n != p.ArtifactSize || hex.EncodeToString(h.Sum(nil)) != p.ArtifactSHA256 {
		return errors.New("PROJECT_ARTIFACT_SHA_MISMATCH")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

func extractProjectWebsite(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	g, err := gzip.NewReader(f)
	if err != nil {
		return errors.New("PROJECT_WEB_ARCHIVE_INVALID")
	}
	defer g.Close()
	g.Multistream(false)
	tr := tar.NewReader(g)
	seen := map[string]bool{}
	total := int64(0)
	for count := 0; ; count++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count >= 16000 {
			return errors.New("PROJECT_WEB_ARCHIVE_INVALID")
		}
		name := strings.TrimPrefix(strings.TrimSuffix(h.Name, "/"), "./")
		if name == "." || name == "" || name == "dist" {
			if h.Typeflag != tar.TypeDir {
				return errors.New("PROJECT_WEB_ARCHIVE_INVALID")
			}
			continue
		}
		name = strings.TrimPrefix(name, "dist/")
		if !projectSafePath("/"+name) || len(name) > 250 || seen[name] {
			return errors.New("PROJECT_WEB_ARCHIVE_PATH_INVALID")
		}
		seen[name] = true
		full := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err = projectWebsiteDir(full, dest); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > 128<<20 || total+h.Size > 512<<20 {
				return errors.New("PROJECT_WEB_ARCHIVE_TOO_LARGE")
			}
			total += h.Size
			if err = projectWebsiteDir(filepath.Dir(full), dest); err != nil {
				return err
			}
			out, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
			if err != nil {
				return err
			}
			n, copyErr := io.Copy(out, tr)
			modeErr := out.Chmod(0644)
			syncErr := out.Sync()
			closeErr := out.Close()
			if copyErr != nil || modeErr != nil || syncErr != nil || closeErr != nil || n != h.Size {
				return errors.New("PROJECT_WEB_ARCHIVE_INVALID")
			}
		default:
			return errors.New("PROJECT_WEB_ARCHIVE_FILE_TYPE_INVALID")
		}
	}
	info, err := os.Stat(filepath.Join(dest, "index.html"))
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("PROJECT_WEB_INDEX_REQUIRED")
	}
	return nil
}

// Only touches the private staging tree with file types restricted above.
func projectWebsiteDir(dir, dest string) error {
	if dir == dest {
		return nil
	}
	if err := projectWebsiteDir(filepath.Dir(dir), dest); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
		return err
	}
	return os.Chmod(dir, 0755)
}

func projectEnvironment(values map[string]string) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	escape := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "\\$", "`", "\\`")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, escape.Replace(values[k]))
	}
	return []byte(b.String())
}

func projectWriteJava(p ProjectDeployment, dir, artifact string, secrets map[string]string) error {
	if err := ValidateJavaJar(artifact, p.ArtifactSHA256, p.ArtifactSize); err != nil {
		return err
	}
	if err := os.Rename(artifact, filepath.Join(dir, "app.jar")); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(dir, "app.jar"), 0644); err != nil {
		return err
	}
	conf := filepath.Join(dir, "config")
	if err := projectSecureDir(conf, 0755); err != nil {
		return err
	}
	values := map[string]string{}
	secretKeys := make([]string, 0, len(secrets))
	for key := range secrets {
		secretKeys = append(secretKeys, key)
	}
	sort.Strings(secretKeys)
	for _, v := range p.Variables {
		value := v.Value
		if v.CredentialRef != "" {
			value = secrets[v.Name]
		}
		values[v.Name] = value
	}
	for _, f := range p.Files {
		refs := map[string]string{}
		for _, ref := range ExtractAppConfigCredentialRefs(f.Content) {
			key := ProjectSecretVar(ref)
			values[key] = secrets[key]
			refs[AppConfigCredSecretPrefix+ref] = "${" + key + "}"
		}
		body, err := ExpandAppConfigCredentials(f.Content, refs)
		if err != nil {
			return err
		}
		if err = projectAtomicFile(filepath.Join(conf, f.Filename), []byte(body), 0644); err != nil {
			return err
		}
	}
	if err := projectAtomicFile(filepath.Join(dir, "runtime.env"), projectEnvironment(values), 0600); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(dir, ".log-redaction.json"), secretKeys)
}
