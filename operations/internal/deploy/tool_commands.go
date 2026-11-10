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
	case "JAVA", "MAVEN", "PYTHON", "FFMPEG", "CODEX", "CLAUDE", "GIT":
		var inputs []string
		if key == "PYTHON" {
			inputs = []string{deployScript("verify/python-extensions.py")}
		}
		command := deployCommand("tool-"+strings.ToLower(key), inputs...)
		return command[0], command[1:], nil
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
		versionArgs := deployCommand("tool-ffmpeg-version")
		version, err := executeNativeTool(ctx, s, run, o.BinaryPath, versionArgs...)
		if err != nil {
			return nil, err
		}
		probe, err := executeNativeTool(ctx, s, run, path.Join(path.Dir(o.BinaryPath), "ffprobe"), versionArgs...)
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
		probe := deployCommand("tool-sandbox-ai", nativeUserName(s.ID), "--chdir="+home, "HOME="+home, "CODEX_HOME="+home, "CLAUDE_CONFIG_DIR="+home, binary)
		return softwareCommand(ctx, run, probe[0], append(probe[1:], args...)...)
	}
	if s.SoftwareKey == "PYTHON" || s.SoftwareKey == "FFMPEG" {
		probe := deployCommand("tool-sandbox-standard", nativeUserName(s.ID), binary)
		return softwareCommand(ctx, run, probe[0], append(probe[1:], args...)...)
	}
	return softwareCommand(ctx, run, binary, args...)
}

func installedToolVersionMatches(d serviceinstall.Definition, version string) bool {
	if d.Key == "git" {
		return strings.TrimSpace(version) == "git version "+strings.SplitN(d.Version, "-", 2)[0]
	}
	if d.Key == "java" && d.Version == "8" {
		return strings.Contains(version, `openjdk version "1.8.0_504"`) && strings.Contains(version, "build 1.8.0_504-b01") && strings.Contains(version, "Temurin")
	}
	if serviceinstall.UsesJavaBridge(d) {
		p, err := toolPluginDefinition(d)
		return err == nil && plugin.VersionMatches(p, version)
	}
	return strings.Contains(version, d.Version)
}
