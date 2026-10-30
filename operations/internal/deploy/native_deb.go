package deploy

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const debLoader = "/lib64/ld-linux-x86-64.so.2"

func (x *executor) prepareDebProgram(ctx context.Context, req Request, root string, parts map[string]string) error {
	d := req.Installation.Definition
	if d.Native == nil || d.Native.BuildRecipe != "DEB" || serviceinstall.ValidateDefinition(d) != nil || d.Key != "redis" && d.Key != "nginx" {
		return errors.New("NATIVE_DEB_RECIPE_UNSUPPORTED")
	}
	selected := debProgramPayloads(d)
	for _, part := range d.Native.Parts {
		payloads := selected[part.Name]
		if len(payloads) == 0 {
			continue
		}
		archive, exists := parts[part.Name]
		if !exists {
			return errors.New("NATIVE_DEB_PART_MISSING")
		}
		if err := extractDebPayload(ctx, archive, root, payloads); err != nil {
			return err
		}
	}
	if d.Key == "nginx" {
		out, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/ldd", filepath.Join(root, "bin/nginx"))
		if err != nil || strings.Contains(string(out), "not found") {
			return errors.New("NATIVE_DEB_RUNTIME_LIBRARY_MISSING")
		}
		return nil
	}
	resolved, err := filepath.EvalSymlinks(debLoader)
	if err != nil || secureAncestors(resolved) != nil {
		return errors.New("NATIVE_DEB_LOADER_UNTRUSTED")
	}
	loaderInfo, err := os.Stat(resolved)
	if err != nil || !loaderInfo.Mode().IsRegular() || loaderInfo.Mode()&0111 == 0 {
		return errors.New("NATIVE_DEB_LOADER_UNTRUSTED")
	}
	libs := filepath.Join(root, "runtime/lib")
	for _, name := range []string{"redis-server", "redis-cli"} {
		binary := filepath.Join(root, "runtime/redis", name)
		out, err := softwareCommand(ctx, x.softwareRunner, debLoader, "--library-path", libs, "--list", binary)
		if err != nil || strings.Contains(string(out), "not found") {
			return errors.New("NATIVE_DEB_RUNTIME_LIBRARY_MISSING")
		}
		wrapper := redisDebWrapper(root, name)
		path := filepath.Join(root, "bin", name)
		if err = serviceFile(path, []byte(wrapper), 0, false); err != nil {
			return err
		}
		if err = os.Chmod(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

func redisDebWrapper(root, name string) string {
	return "#!/bin/sh\nexec " + posixSingleQuote(debLoader) + " --argv0 " + posixSingleQuote(name) + " --library-path " + posixSingleQuote(filepath.Join(root, "runtime/lib")) + " " + posixSingleQuote(filepath.Join(root, "runtime/redis", name)) + " \"$@\"\n"
}

// Package maintainer scripts, system config, units, links and documentation are
// never extracted or executed. Only these fixed regular payloads are consumed.
func debProgramPayloads(d serviceinstall.Definition) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, part := range d.Native.Parts {
		switch {
		case strings.HasPrefix(part.Name, "redis-tools_"):
			out[part.Name] = map[string]string{"usr/bin/redis-check-rdb": "runtime/redis/redis-server", "usr/bin/redis-cli": "runtime/redis/redis-cli"}
		case strings.HasPrefix(part.Name, "liblzf1_"):
			out[part.Name] = map[string]string{"usr/lib/x86_64-linux-gnu/liblzf.so.1.5": "runtime/lib/liblzf.so.1"}
		case strings.HasPrefix(part.Name, "libjemalloc2_"):
			out[part.Name] = map[string]string{"usr/lib/x86_64-linux-gnu/libjemalloc.so.2": "runtime/lib/libjemalloc.so.2"}
		case strings.HasPrefix(part.Name, "nginx_"):
			out[part.Name] = map[string]string{"usr/sbin/nginx": "bin/nginx"}
		}
	}
	return out
}

func extractDebPayload(ctx context.Context, archive, root string, wanted map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resolved, err := filepath.EvalSymlinks("/usr/bin/dpkg-deb")
	if err != nil || secureAncestors(resolved) != nil {
		return errors.New("NATIVE_DEB_READER_UNTRUSTED")
	}
	cmd := exec.CommandContext(ctx, resolved, "--fsys-tarfile", archive)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C"}
	boundPackageCommand(cmd)
	cmd.Stderr = &cappedBuffer{limit: 4096}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return errors.New("NATIVE_DEB_READER_FAILED")
	}
	err = extractDebPayloadTar(ctx, stdout, root, wanted)
	if err != nil {
		_ = cmd.Cancel()
	}
	stdout.Close()
	waitErr := cmd.Wait()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return errors.New("NATIVE_DEB_READER_FAILED")
	}
	return nil
}

func extractDebPayloadTar(ctx context.Context, input io.Reader, root string, wanted map[string]string) error {
	// Debian package payloads are bounded independently of the outer 2GiB
	// material; these fixed executables are each far smaller than 128MiB.
	limited := &io.LimitedReader{R: input, N: (512 << 20) + 1}
	reader := tar.NewReader(limited)
	seen := map[string]bool{}
	found := map[string]bool{}
	var expanded int64
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("NATIVE_DEB_PAYLOAD_CORRUPT")
		}
		if (header.Name == "." || header.Name == "./") && header.Typeflag == tar.TypeDir {
			continue
		}
		name, err := nativeArchiveName(header.Name, 0)
		if err != nil {
			return err
		}
		expanded += header.Size
		if count > 10000 || header.Size < 0 || header.Size > 128<<20 || expanded > 512<<20 || seen[name] {
			return errors.New("NATIVE_DEB_PAYLOAD_LIMIT")
		}
		seen[name] = true
		destination, selected := wanted[name]
		if !selected {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || header.Size < 1 {
			return errors.New("NATIVE_DEB_PAYLOAD_NOT_REGULAR")
		}
		if clean, err := nativeArchiveName(destination, 0); err != nil || clean != destination {
			return errors.New("NATIVE_DEB_DESTINATION_INVALID")
		}
		output := filepath.Join(root, filepath.FromSlash(destination))
		if err = ensureExtractionParent(root, filepath.Dir(output)); err != nil {
			return err
		}
		file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, reader)
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		found[name] = true
	}
	// Drain stdout so dpkg-deb can exit, while retaining the payload bound.
	if _, err := io.Copy(io.Discard, limited); err != nil || limited.N < 1 {
		return errors.New("NATIVE_DEB_PAYLOAD_LIMIT")
	}
	if len(found) != len(wanted) {
		return errors.New("NATIVE_DEB_PAYLOAD_MISSING")
	}
	return nil
}
