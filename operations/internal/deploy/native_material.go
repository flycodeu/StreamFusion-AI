package deploy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func nativeBinaryRelative(d serviceinstall.Definition) string {
	return serviceinstall.NativeBinaryRelative(d)
}

func existingServiceParent(value string) string {
	for {
		if _, err := os.Lstat(value); err == nil {
			return value
		}
		parent := filepath.Dir(value)
		if parent == value {
			return value
		}
		value = parent
	}
}

func fileSHA(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (x *executor) receiveNativeMaterial(req Request, dir string) (map[string]string, error) {
	i := req.Installation
	if x.installationInput == nil || i.Source.ByteSize < 1 || !Hex.MatchString(i.Source.SHA256) {
		return nil, errors.New("NATIVE_MATERIAL_REQUIRED")
	}
	archive := filepath.Join(dir, "native-material")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = io.CopyN(f, x.installationInput, i.Source.ByteSize)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, errors.New("NATIVE_MATERIAL_TRANSFER_FAILED")
	}
	if _, err = serviceinstall.ValidateMaterial(archive, i.Source.SHA256, i.Source.ByteSize, i.Definition, "NATIVE"); err != nil {
		return nil, err
	}
	parts := map[string]string{}
	if len(i.Definition.Native.Parts) == 1 {
		parts[i.Definition.Native.Parts[0].Name] = archive
		return parts, nil
	}
	input, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		out := filepath.Join(dir, header.Name)
		file, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(file, reader)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		parts[header.Name] = out
	}
	return parts, nil
}

// extractNativePart only consumes already hash-verified vendor bytes. All links
// are deferred until regular files exist, and every link target stays in root.
func extractNativePart(ctx context.Context, part serviceinstall.NativePart, archive, destination string) error {
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	archiveInfo, err := f.Stat()
	if err != nil || archiveInfo.Size() <= 0 {
		return errors.New("NATIVE_ARCHIVE_EMPTY")
	}
	expansionLimit := min(int64(8<<30), archiveInfo.Size()*200)
	var input io.Reader = f
	var gz *gzip.Reader
	var cmd *exec.Cmd
	if part.Format == "TAR_GZIP" {
		gz, err = gzip.NewReader(f)
		if err != nil {
			return errors.New("NATIVE_ARCHIVE_GZIP_INVALID")
		}
		defer gz.Close()
		input = gz
	} else if part.Format == "TAR_XZ" {
		cmd = exec.CommandContext(ctx, "/usr/bin/xz", "--decompress", "--stdout", "--single-stream", archive)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		defer out.Close()
		cmd.Stderr = &cappedBuffer{limit: 4096}
		if err = cmd.Start(); err != nil {
			return err
		}
		input = out
	} else {
		return errors.New("NATIVE_ARCHIVE_FORMAT_INVALID")
	}
	reader := tar.NewReader(io.LimitReader(input, 8<<30))
	var expanded int64
	type link struct {
		name, target string
		hard         bool
	}
	links := []link{}
	seen := map[string]bool{}
	fail := func(err error) error {
		if cmd != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		return err
	}
	for count := 0; ; count++ {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return fail(errors.New("NATIVE_ARCHIVE_CORRUPT"))
		}
		name, err := nativeArchiveName(header.Name, part.StripComponents)
		if err != nil {
			return fail(err)
		}
		if name == "" {
			continue
		}
		if count > 50000 || seen[name] || header.Size < 0 {
			return fail(errors.New("NATIVE_ARCHIVE_ENTRY_LIMIT"))
		}
		seen[name] = true
		expanded += header.Size
		if expanded > expansionLimit {
			return fail(errors.New("NATIVE_ARCHIVE_EXPANSION_LIMIT"))
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if err = ensureExtractionParent(destination, filepath.Dir(target)); err != nil {
			return fail(err)
		}
		mode := os.FileMode(header.Mode) & 0755
		mode &^= 0022
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0700); err != nil {
				return fail(err)
			}
		case tar.TypeReg, tar.TypeRegA:
			file, openErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0400)
			if openErr != nil {
				return fail(openErr)
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return fail(copyErr)
			}
			if closeErr != nil {
				return fail(closeErr)
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(header.Linkname) || strings.ContainsAny(header.Linkname, "\\\x00\r\n") {
				return fail(errors.New("NATIVE_ARCHIVE_LINK_FORBIDDEN"))
			}
			resolved := path.Clean(path.Join(path.Dir(name), header.Linkname))
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				return fail(errors.New("NATIVE_ARCHIVE_LINK_FORBIDDEN"))
			}
			links = append(links, link{name: name, target: header.Linkname})
		case tar.TypeLink:
			linked, err := nativeArchiveName(header.Linkname, part.StripComponents)
			if err != nil || linked == "" {
				return fail(errors.New("NATIVE_ARCHIVE_LINK_FORBIDDEN"))
			}
			links = append(links, link{name: name, target: linked, hard: true})
		default:
			return fail(errors.New("NATIVE_ARCHIVE_SPECIAL_FORBIDDEN"))
		}
	}
	// Drain the decoder to check its checksum and bounded trailing padding.
	if n, drainErr := io.Copy(io.Discard, io.LimitReader(input, (1<<20)+1)); drainErr != nil || n > 1<<20 {
		err = errors.New("NATIVE_ARCHIVE_TRAILING_LIMIT")
		return fail(err)
	}
	if cmd != nil {
		if err = cmd.Wait(); err != nil {
			return errors.New("NATIVE_ARCHIVE_XZ_INVALID")
		}
	}
	for _, item := range links {
		name := filepath.Join(destination, filepath.FromSlash(item.name))
		if err = ensureExtractionParent(destination, filepath.Dir(name)); err != nil {
			return err
		}
		if item.hard {
			target := filepath.Join(destination, filepath.FromSlash(item.target))
			if err = ensureExtractionParent(destination, filepath.Dir(target)); err != nil {
				return err
			}
			info, statErr := os.Lstat(target)
			if statErr != nil || !info.Mode().IsRegular() {
				return errors.New("NATIVE_ARCHIVE_LINK_FORBIDDEN")
			}
			err = os.Link(target, name)
		} else {
			err = os.Symlink(filepath.FromSlash(item.target), name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func nativeArchiveName(value string, strip int) (string, error) {
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") || strings.Contains(value, ":") {
		return "", errors.New("NATIVE_ARCHIVE_PATH_FORBIDDEN")
	}
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimSuffix(value, "/")
	if path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return "", errors.New("NATIVE_ARCHIVE_PATH_FORBIDDEN")
	}
	components := strings.Split(value, "/")
	if len(components) <= strip {
		return "", nil
	}
	return strings.Join(components[strip:], "/"), nil
}
func ensureExtractionParent(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("NATIVE_ARCHIVE_PATH_FORBIDDEN")
	}
	current := root
	for _, component := range strings.Split(relative, string(os.PathSeparator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		if info, err := os.Lstat(current); os.IsNotExist(err) {
			if err = os.Mkdir(current, 0700); err != nil {
				return err
			}
		} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("NATIVE_ARCHIVE_PARENT_FORBIDDEN")
		}
	}
	return nil
}

func nativeDeadline(req Request) time.Duration {
	limit := 30 * time.Minute
	if t, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil {
		limit = min(limit, time.Until(t))
	}
	return limit
}
