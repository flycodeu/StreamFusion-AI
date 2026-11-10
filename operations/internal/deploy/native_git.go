package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

var gitRuntimePackages = []string{"libc6", "libcurl3t64-gnutls", "libexpat1", "libpcre2-8-0", "zlib1g", "perl", "liberror-perl", "openssh-client", "ca-certificates"}

func gitDebWrapper(root string) string {
	vendor := filepath.Join(root, "runtime/git/usr")
	return deployTemplate("native/git-wrapper.sh.tmpl",
		posixSingleQuote(filepath.Join(vendor, "lib/git-core")),
		posixSingleQuote(filepath.Join(vendor, "share/git-core/templates")),
		posixSingleQuote(filepath.Join(vendor, "share/perl5")),
		posixSingleQuote(filepath.Join(vendor, "bin/git")))
}

func (x *executor) prepareGitProgram(ctx context.Context, req Request, root string, parts map[string]string) error {
	d := req.Installation.Definition
	if serviceinstall.ValidateDefinition(d) != nil || d.Key != "git" || d.Native == nil || d.Native.BuildRecipe != "GIT_DEB" || len(d.Native.Parts) != 1 {
		return errors.New("NATIVE_GIT_RECIPE_INVALID")
	}
	part := d.Native.Parts[0]
	archive := parts[part.Name]
	if archive == "" {
		return errors.New("NATIVE_DEB_PART_MISSING")
	}
	vendor := filepath.Join(root, "runtime/git")
	// Decode the trusted package as data through the existing bounded extractor.
	// Maintainer scripts are not read, and no package/system paths are installed.
	if err := extractNativePart(ctx, part, archive, vendor); err != nil {
		return err
	}
	if err := filepath.WalkDir(vendor, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if info.Mode()&0111 != 0 {
			mode = 0755
		}
		return os.Chmod(name, mode)
	}); err != nil {
		return err
	}
	for _, relative := range []string{"usr/bin/git", "usr/lib/git-core/git-remote-http"} {
		out, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/ldd", filepath.Join(vendor, relative))
		if err != nil || strings.Contains(string(out), "not found") {
			return errors.New("NATIVE_DEB_RUNTIME_LIBRARY_MISSING")
		}
	}
	binary := filepath.Join(root, "bin/git")
	if err := serviceFile(binary, []byte(gitDebWrapper(root)), 0, false); err != nil {
		return err
	}
	return os.Chmod(binary, 0755)
}
