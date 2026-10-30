package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// checkMaterialLibrary validates transfer bytes without opening controller state,
// executing packages, or claiming the importer's pinned vendor-content check.
// Its successful output is a bounded, newline-safe list for the shell installer.
func checkMaterialLibrary(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("material-library-check", flag.ContinueOnError)
	dir := flags.String("material-library", "", "只读检查发行材料库；不打开或创建状态目录")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*dir) == "" {
		return errors.New("MATERIAL_LIBRARY_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	names, err := checkedMaterialLibrary(ctx, *dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err = fmt.Fprintln(output, name); err != nil {
			return err
		}
	}
	return nil
}

func checkedMaterialLibrary(ctx context.Context, dir string) ([]string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, statErr := os.Lstat(current)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("MATERIAL_LIBRARY_DIRECTORY_INVALID")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	manifest, err := readLibraryFile(ctx, root, "library.json", 0, "", true)
	var library serviceinstall.MaterialLibrary
	if err != nil {
		return nil, err
	}
	if uniqueLibraryJSON(manifest) != nil || deploy.StrictJSON(manifest, &library) != nil || library.SchemaVersion != 1 || library.Materials == nil || len(library.Materials) > 100 {
		return nil, errors.New("MATERIAL_LIBRARY_MANIFEST_INVALID")
	}
	names := []string{"library.json"}
	seen := map[string]bool{"library.json": true}
	var total int64
	for _, material := range library.Materials {
		if _, err = material.Definition(); err != nil {
			return nil, err
		}
		if !safeLibraryInstallName(material.File) || seen[strings.ToLower(material.File)] {
			return nil, errors.New("MATERIAL_LIBRARY_FILE_INVALID")
		}
		seen[strings.ToLower(material.File)] = true
		total += material.ByteSize
		if total > 16<<30 {
			return nil, errors.New("INSTALL_MATERIAL_QUOTA")
		}
		if _, err = readLibraryFile(ctx, root, material.File, material.ByteSize, material.SHA256, false); err != nil {
			return nil, err
		}
		names = append(names, material.File)
	}
	return names, nil
}

func safeLibraryInstallName(name string) bool {
	if !serviceinstall.LibraryFileName(name) || strings.TrimRight(name, " .") != name || strings.ContainsAny(name, `<>"|?*`) {
		return false
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func readLibraryFile(ctx context.Context, root *os.Root, name string, size int64, expected string, manifest bool) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || manifest && before.Size() > 1<<20 || !manifest && before.Size() != size {
		return nil, errors.New("MATERIAL_LIBRARY_SOURCE_INVALID")
	}
	in, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("MATERIAL_LIBRARY_SOURCE_CHANGED")
	}
	if manifest {
		size = before.Size()
	}
	digest := sha256.New()
	var raw bytes.Buffer
	var sink io.Writer = digest
	if manifest {
		sink = io.MultiWriter(digest, &raw)
	}
	reader := &libraryCheckReader{ctx: ctx, reader: io.LimitReader(in, size+1)}
	count, err := io.Copy(sink, reader)
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("MATERIAL_LIBRARY_SOURCE_CHANGED")
	}
	if count != size || !manifest && hex.EncodeToString(digest.Sum(nil)) != expected {
		return nil, errors.New("MATERIAL_LIBRARY_HASH_OR_SIZE_MISMATCH")
	}
	return raw.Bytes(), ctx.Err()
}

type libraryCheckReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *libraryCheckReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func uniqueLibraryJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var read func(int) error
	read = func(depth int) error {
		if depth > 5 {
			return errors.New("JSON_DEPTH")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if token != json.Delim('{') && token != json.Delim('[') {
			return nil
		}
		object := token == json.Delim('{')
		seen := map[string]bool{}
		allowed := map[string]bool{"schemaVersion": true, "materials": true}
		if depth == 2 {
			allowed = map[string]bool{"recipeKey": true, "recipeVersion": true, "recipeRevision": true, "runtimeMode": true, "name": true, "file": true, "sha256": true, "byteSize": true}
		}
		for d.More() {
			if object {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] || !allowed[name] || depth != 0 && depth != 2 {
					return errors.New("JSON_DUPLICATE_KEY")
				}
				seen[name] = true
			}
			if err := read(depth + 1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	return read(0)
}
