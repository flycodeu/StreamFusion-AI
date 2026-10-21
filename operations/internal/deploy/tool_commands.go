package deploy

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/streamfusion-ai/operations/internal/plugin"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func nativeNeedsAccount(d serviceinstall.Definition) bool {
	return d.Kind != "TOOL" || d.Category == "PROGRAMMING" || d.Key == "python" || d.Key == "ffmpeg"
}

func installedToolCommand(key string) (string, []string, error) {
	switch key {
	case "JAVA":
		return "java", []string{"-version"}, nil
	case "MAVEN":
		return "mvn", []string{"-version"}, nil
	case "PYTHON":
		// Fail instead of claiming a usable interpreter with missing extensions.
		return "python3", []string{"-I", "-c", "import sys,ssl,sqlite3,bz2,lzma,ctypes,venv,ensurepip; print(sys.version); print('SSL/sqlite/venv/ensurepip OK')"}, nil
	case "FFMPEG":
		return "ffmpeg", []string{"-hide_banner", "-encoders"}, nil
	case "CODEX":
		return "codex", []string{"--version"}, nil
	case "CLAUDE":
		return "claude", []string{"--version"}, nil
	}
	return "", nil, errors.New("TOOL_RECIPE_UNSUPPORTED")
}

func nativeToolCommand(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner, args ...string) ([]byte, error) {
	o := s.Ownership
	out, err := executeNativeTool(ctx, s, run, o.BinaryPath, args...)
	if err != nil {
		return nil, err
	}
	if s.SoftwareKey == "FFMPEG" {
		if !strings.Contains(string(out), "libx264") {
			return nil, errors.New("FFMPEG_H264_ENCODER_UNAVAILABLE")
		}
		version, err := executeNativeTool(ctx, s, run, o.BinaryPath, "-version")
		if err != nil {
			return nil, err
		}
		probe, err := executeNativeTool(ctx, s, run, path.Join(path.Dir(o.BinaryPath), "ffprobe"), "-version")
		if err != nil || !strings.Contains(string(probe), "ffprobe version") {
			return nil, errors.New("FFPROBE_UNAVAILABLE")
		}
		return version, nil
	}
	return out, nil
}

func executeNativeTool(ctx context.Context, s SoftwareRequest, run SoftwareCommandRunner, binary string, args ...string) ([]byte, error) {
	if s.SoftwareKey == "CODEX" || s.SoftwareKey == "CLAUDE" {
		// No inherited credentials, project hooks, root HOME or auto-updater.
		home := path.Join(path.Dir(path.Dir(s.Ownership.BinaryPath)), "data")
		probe := []string{"-u", nativeUserName(s.ID), "--", "/usr/bin/env", "-i", "--chdir=" + home, "PATH=/usr/bin:/bin", "HOME=" + home, "CODEX_HOME=" + home, "CLAUDE_CONFIG_DIR=" + home, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "LC_ALL=C", binary}
		return softwareCommand(ctx, run, "/usr/sbin/runuser", append(probe, args...)...)
	}
	if s.SoftwareKey == "PYTHON" || s.SoftwareKey == "FFMPEG" {
		probe := []string{"-u", nativeUserName(s.ID), "--", "/usr/bin/env", "-i", "--chdir=/", "PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C", binary}
		return softwareCommand(ctx, run, "/usr/sbin/runuser", append(probe, args...)...)
	}
	return softwareCommand(ctx, run, binary, args...)
}

func installedToolVersionMatches(d serviceinstall.Definition, version string) bool {
	if serviceinstall.UsesJavaBridge(d) {
		p, err := toolPluginDefinition(d)
		return err == nil && plugin.VersionMatches(p, version)
	}
	return strings.Contains(version, d.Version)
}
