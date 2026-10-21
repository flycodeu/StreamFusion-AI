package serviceinstall

// New tools use the existing install protocol. Only vendor-distributed modes
// are advertised; a source-only project is never presented as an official image.
func additionalDefinitions() []Definition {
	part := func(name, url, sha string, size int64, format string, strip int) NativePart {
		return NativePart{Name: name, URL: url, SHA256: sha, ByteSize: size, Format: format, StripComponents: strip}
	}
	buildTools := []string{"/usr/sbin/useradd", "/usr/sbin/runuser", "/usr/bin/make", "/usr/bin/cc", "/usr/bin/pkg-config", "/usr/bin/xz"}
	return []Definition{
		{Key: "ffmpeg",
			Name:         "FFmpeg",
			Kind:         "TOOL",
			Category:     "MEDIA_TOOLS",
			Version:      "9.0.2",
			Revision:     "ffmpeg-v1",
			Platform:     "linux/amd64",
			OfficialURL:  "https://ffmpeg.org/download.html",
			RuntimeModes: []string{"NATIVE"},
			SourceModes:  []string{"ONLINE", "ARCHIVE"},
			Native:       &NativeDefinition{BuildRecipe: "FFMPEG_SOURCE", RequiredTools: append(append([]string{}, buildTools...), "/usr/bin/nasm"), Parts: []NativePart{part("ffmpeg.tar.xz", "https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz", "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e", 12040788, "TAR_XZ", 1)}}},
		{Key: "python",
			Name:         "Python",
			Kind:         "TOOL",
			Category:     "RUNTIMES",
			Version:      "3.12.15",
			Revision:     "python-v1",
			Platform:     "linux/amd64",
			OfficialURL:  "https://www.python.org/downloads/release/python-31215/",
			RuntimeModes: []string{"NATIVE", "DOCKER"},
			SourceModes:  []string{"ONLINE", "ARCHIVE", "REGISTRY"},
			Image:        "docker.io/library/python:3.12-slim-bookworm",
			Digest:       "sha256:57d96f71de145a98e7ceca18ee4ade77bba4103a33a72dfaea026b23625c6bf0",
			ImageID:      "sha256:3c4d812a6efe003770aafbdba65f9c1960ba8234ab23a133934205a70bdcda77",
			Native:       &NativeDefinition{BuildRecipe: "PYTHON_SOURCE", RequiredTools: buildTools, Parts: []NativePart{part("python.tar.xz", "https://www.python.org/ftp/python/3.12.15/Python-3.12.15.tar.xz", "c2c4321961fab0fb999d66e0cecf521c2ab3994c7992873ea99e306c1094fd5a", 20829824, "TAR_XZ", 1)}}},
		{Key: "codex",
			Name:         "Codex CLI",
			Kind:         "TOOL",
			Category:     "PROGRAMMING",
			Version:      "0.163.0",
			Revision:     "codex-v1",
			Platform:     "linux/amd64",
			OfficialURL:  "https://developers.openai.com/codex/cli/",
			RuntimeModes: []string{"NATIVE"},
			SourceModes:  []string{"ONLINE", "ARCHIVE"},
			Native:       &NativeDefinition{BuildRecipe: "CODEX_BINARY", RequiredTools: []string{"/usr/sbin/useradd", "/usr/sbin/runuser"}, Parts: []NativePart{part("codex.tar.gz", "https://github.com/openai/codex/releases/download/rust-v0.163.0/codex-x86_64-unknown-linux-musl.tar.gz", "beb339e8ecd9ba057a6bb2dc81ad8ca474812edfbf0fc5056ca818797648dde4", 112589966, "TAR_GZIP", 0)}}},
		{Key: "claude",
			Name:         "Claude Code",
			Kind:         "TOOL",
			Category:     "PROGRAMMING",
			Version:      "2.1.304",
			Revision:     "claude-v1",
			Platform:     "linux/amd64",
			OfficialURL:  "https://code.claude.com/docs/en/setup",
			RuntimeModes: []string{"NATIVE"},
			SourceModes:  []string{"ONLINE", "ARCHIVE"},
			Native:       &NativeDefinition{BuildRecipe: "RAW_BINARY", RequiredTools: []string{"/usr/sbin/useradd", "/usr/sbin/runuser"}, Parts: []NativePart{part("claude", "https://downloads.claude.ai/claude-code-releases/2.1.304/linux-x64/claude", "ea0f135784631619a7a409d07322ecec964f9cc4fd377508ed1ba3f141f27912", 288730480, "RAW", 0)}}},
	}
}

func UsesJavaBridge(d Definition) bool { return d.Key == "java" || d.Key == "maven" }
