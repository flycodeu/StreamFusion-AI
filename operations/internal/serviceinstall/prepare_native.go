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
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 6 || r.URL.Scheme != "https" {
			return errors.New("NATIVE_SOURCE_REDIRECT_FORBIDDEN")
		}
		return nil
	}}
	var total int64
	for _, part := range d.Native.Parts {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, part.URL, nil)
		if err != nil {
			return err
		}
		request.Header.Set("User-Agent", "StreamFusion-Ops/0.2")
		response, err := client.Do(request)
		if err != nil {
			return errors.New("NATIVE_SOURCE_UNREACHABLE")
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return fmt.Errorf("NATIVE_SOURCE_HTTP_%d", response.StatusCode)
		}
		file, err := os.OpenFile(filepath.Join(temp, part.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			response.Body.Close()
			return err
		}
		sum := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(file, sum), io.LimitReader(response.Body, ArchiveLimit+1))
		response.Body.Close()
		syncErr := file.Sync()
		closeErr := file.Close()
		total += size
		if copyErr != nil || syncErr != nil || closeErr != nil || total > ArchiveLimit || size > ArchiveLimit || part.ByteSize > 0 && size != part.ByteSize || hex.EncodeToString(sum.Sum(nil)) != part.SHA256 {
			return errors.New("NATIVE_SOURCE_HASH_OR_SIZE_MISMATCH")
		}
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
