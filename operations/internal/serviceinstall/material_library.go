package serviceinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaterialLibrary contains bytes for existing recipes. The controller may
// resolve published templates; a manifest never supplies executable definitions.
type MaterialLibrary struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Materials     []LibraryMaterialFile `json:"materials"`
}

type LibraryMaterialFile struct {
	RecipeKey      string `json:"recipeKey"`
	RecipeVersion  string `json:"recipeVersion"`
	RecipeRevision string `json:"recipeRevision"`
	RuntimeMode    string `json:"runtimeMode"`
	Name           string `json:"name"`
	File           string `json:"file"`
	SHA256         string `json:"sha256"`
	ByteSize       int64  `json:"byteSize"`
}

var ErrMaterialLibraryTemplateUnavailable = errors.New("MATERIAL_LIBRARY_TEMPLATE_UNAVAILABLE")

func (m LibraryMaterialFile) Definition(resolvers ...func(string, string) (Definition, error)) (Definition, error) {
	if !sha.MatchString(m.SHA256) || m.ByteSize < 1 || m.ByteSize > ArchiveLimit || m.RuntimeMode != "NATIVE" && m.RuntimeMode != "DOCKER" {
		return Definition{}, errors.New("MATERIAL_LIBRARY_RECIPE_INVALID")
	}
	if !LibraryFileName(m.File) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 160 || strings.ContainsAny(m.Name, "\r\n\x00") {
		return Definition{}, errors.New("MATERIAL_LIBRARY_FILE_INVALID")
	}
	resolve := Find
	if len(resolvers) > 0 && resolvers[0] != nil {
		resolve = resolvers[0]
	}
	d, err := resolve(m.RecipeKey, m.RecipeVersion)
	d = BareDefinition(d)
	if len(resolvers) > 0 && resolvers[0] != nil && strings.HasPrefix(m.RecipeKey, "custom-") && (errors.Is(err, ErrMaterialLibraryTemplateUnavailable) || err == nil && d.Key == m.RecipeKey && (d.Version != m.RecipeVersion || d.Revision != m.RecipeRevision)) {
		return Definition{}, ErrMaterialLibraryTemplateUnavailable
	}
	if err != nil || d.Key != m.RecipeKey || d.Version != m.RecipeVersion || d.Revision != m.RecipeRevision || !slices.Contains(d.RuntimeModes, m.RuntimeMode) || d.Commands != nil && !slices.Contains(d.SourceModes, "ARCHIVE") {
		return Definition{}, errors.New("MATERIAL_LIBRARY_RECIPE_INVALID")
	}
	return d, nil
}

func LibraryFileName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 240 && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\:\r\n\x00")
}

// CopyLibraryMaterial snapshots an untrusted sidecar file before validation.
// The checked source cannot be a link, change identity, or exceed its declared
// size; the private output is never an install script and is not executed.
func CopyLibraryMaterial(ctx context.Context, root, output string, m LibraryMaterialFile, resolvers ...func(string, string) (Definition, error)) error {
	if _, err := m.Definition(resolvers...); err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	before, err := r.Lstat(m.File)
	if err != nil || !before.Mode().IsRegular() || before.Size() != m.ByteSize {
		return errors.New("MATERIAL_LIBRARY_SOURCE_INVALID")
	}
	in, err := r.Open(m.File)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return errors.New("MATERIAL_LIBRARY_SOURCE_CHANGED")
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		out.Close()
		if !complete {
			_ = os.Remove(output)
		}
	}()
	digest := sha256.New()
	reader := &materialContextReader{ctx: ctx, reader: io.LimitReader(in, m.ByteSize+1)}
	size, err := io.Copy(io.MultiWriter(out, digest), reader)
	if err != nil {
		return err
	}
	if size != m.ByteSize || hex.EncodeToString(digest.Sum(nil)) != m.SHA256 {
		return errors.New("MATERIAL_LIBRARY_HASH_OR_SIZE_MISMATCH")
	}
	if err = ctx.Err(); err == nil {
		err = out.Sync()
	}
	if err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

type materialContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *materialContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
