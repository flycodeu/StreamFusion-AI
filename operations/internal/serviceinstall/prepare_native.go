package serviceinstall

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PrepareNativeMaterial fetches only the catalog's immutable official sources.
// It never executes a package, script or compiler, and publishes no partial out.
func PrepareNativeMaterial(ctx context.Context, d Definition, outputPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if ValidateDefinition(d) != nil || d.Native == nil {
		return errors.New("NATIVE_RECIPE_UNAVAILABLE")
	}
	if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
		return errors.New("NATIVE_OUTPUT_EXISTS")
	}
	parent := filepath.Dir(outputPath)
	temp, err := os.MkdirTemp(parent, ".native-material-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if _, err = DownloadNativeParts(ctx, d, temp, nil); err != nil {
		return err
	}
	prepared := filepath.Join(temp, "prepared")
	if len(d.Native.Parts) == 1 {
		prepared = filepath.Join(temp, d.Native.Parts[0].Name)
	} else {
		output, err := os.OpenFile(prepared, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		writer := tar.NewWriter(output)
		for _, part := range d.Native.Parts {
			source, err := os.Open(filepath.Join(temp, part.Name))
			if err != nil {
				output.Close()
				return err
			}
			info, err := source.Stat()
			if err == nil {
				err = writer.WriteHeader(&tar.Header{Name: part.Name, Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg})
			}
			if err == nil {
				_, err = io.Copy(writer, source)
			}
			source.Close()
			if err != nil {
				output.Close()
				return err
			}
		}
		if err = writer.Close(); err == nil {
			err = output.Sync()
		}
		closeErr := output.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	// A hard link publishes the completed file without overwriting an output
	// created by another caller between the initial check and final commit.
	if err = os.Link(prepared, outputPath); err != nil {
		return errors.New("NATIVE_MATERIAL_PUBLISH_FAILED")
	}
	return nil
}

// DownloadNativeParts keeps verified parts and interrupted prefixes in a job's
// private directory. Every reuse rechecks the complete file against the catalog.
// Continuing is explicit: one request per part, with no automatic retry loop.
func DownloadNativeParts(ctx context.Context, d Definition, directory string, progress func(string, int64, int64)) (map[string]string, error) {
	if ValidateDefinition(d) != nil || d.Native == nil {
		return nil, errors.New("NATIVE_RECIPE_UNAVAILABLE")
	}
	if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("NATIVE_DOWNLOAD_CACHE_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, ResponseHeaderTimeout: 45 * time.Second, TLSHandshakeTimeout: 15 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 6 || r.URL.Scheme != "https" {
			return errors.New("NATIVE_SOURCE_REDIRECT_FORBIDDEN")
		}
		return nil
	}}
	parts := make(map[string]string, len(d.Native.Parts))
	var total int64
	for _, part := range d.Native.Parts {
		file := filepath.Join(directory, part.Name)
		size, err := downloadNativePart(ctx, client, part, file, ArchiveLimit-total, progress)
		if err != nil {
			return nil, err
		}
		total += size
		parts[part.Name] = file
	}
	return parts, nil
}

func downloadNativePart(ctx context.Context, client *http.Client, part NativePart, path string, limit int64, progress func(string, int64, int64)) (int64, error) {
	mismatch := errors.New("NATIVE_SOURCE_HASH_OR_SIZE_MISMATCH")
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) || err == nil && !info.Mode().IsRegular() {
		return 0, errors.New("NATIVE_DOWNLOAD_CACHE_INVALID")
	}
	var offset int64
	if err == nil {
		offset = info.Size()
	}
	if limit < 1 || offset > limit || part.ByteSize > limit || part.ByteSize > 0 && offset > part.ByteSize {
		return 0, mismatch
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, io.LimitReader(file, limit+1)); err != nil {
		return 0, err
	}
	if offset > 0 && (part.ByteSize == 0 || offset == part.ByteSize) {
		if hex.EncodeToString(sum.Sum(nil)) == part.SHA256 {
			if progress != nil {
				progress(part.Name, offset, offset)
			}
			return offset, nil
		}
		if offset == part.ByteSize {
			return 0, mismatch
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, part.URL, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", "StreamFusion-Ops/0.2")
	request.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, errors.New("NATIVE_DOWNLOAD_INTERRUPTED")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		// Servers may ignore Range. Restart instead of appending duplicate bytes.
		if err = file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		offset = 0
		sum.Reset()
	case http.StatusPartialContent:
		if offset == 0 || !nativeContentRange(response.Header.Get("Content-Range"), offset, part.ByteSize, limit) {
			return 0, errors.New("NATIVE_SOURCE_RANGE_INVALID")
		}
	default:
		return 0, fmt.Errorf("NATIVE_SOURCE_HTTP_%d", response.StatusCode)
	}
	if response.ContentLength > limit-offset || part.ByteSize > 0 && response.ContentLength >= 0 && response.ContentLength != part.ByteSize-offset {
		return 0, mismatch
	}
	if progress != nil {
		progress(part.Name, offset, part.ByteSize)
	}
	reporter := &nativeDownloadProgress{name: part.Name, size: offset, total: part.ByteSize, report: progress}
	n, copyErr := io.Copy(io.MultiWriter(file, sum, reporter), io.LimitReader(response.Body, limit-offset+1))
	if err = file.Sync(); err != nil {
		return 0, err
	}
	size := offset + n
	if size > limit || part.ByteSize > 0 && size > part.ByteSize {
		return 0, mismatch
	}
	if copyErr != nil || part.ByteSize > 0 && size < part.ByteSize {
		return 0, errors.New("NATIVE_DOWNLOAD_INTERRUPTED")
	}
	if hex.EncodeToString(sum.Sum(nil)) != part.SHA256 {
		return 0, mismatch
	}
	if err = file.Close(); err != nil {
		return 0, err
	}
	if progress != nil {
		progress(part.Name, size, size)
	}
	return size, nil
}

func nativeContentRange(value string, offset, expected, limit int64) bool {
	value, ok := strings.CutPrefix(value, "bytes ")
	span, total, slash := strings.Cut(value, "/")
	start, end, dash := strings.Cut(span, "-")
	a, ea := strconv.ParseInt(start, 10, 64)
	b, eb := strconv.ParseInt(end, 10, 64)
	c, ec := strconv.ParseInt(total, 10, 64)
	return ok && slash && dash && ea == nil && eb == nil && ec == nil && a == offset && b >= a && b == c-1 && c <= limit && (expected == 0 || c == expected)
}

type nativeDownloadProgress struct {
	name        string
	size, total int64
	last        time.Time
	report      func(string, int64, int64)
}

func (p *nativeDownloadProgress) Write(data []byte) (int, error) {
	p.size += int64(len(data))
	if p.report != nil && time.Since(p.last) >= 2*time.Second {
		p.report(p.name, p.size, p.total)
		p.last = time.Now()
	}
	return len(data), nil
}
