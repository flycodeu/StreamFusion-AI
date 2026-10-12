package com.streamfusion.platform.media.runtime.adapter;

import com.streamfusion.platform.media.runtime.config.MediaRuntimeProperties;
import java.nio.file.Path;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** OS differences live here; discovery and probing share a single bounded pipeline. */
public sealed interface MediaHostAdapter
        permits MediaHostAdapter.Windows, MediaHostAdapter.Linux, MediaHostAdapter.Unsupported {
    String os();

    String kind(Path executable);

    String pathSeparator();

    MediaRuntimeProperties.OsPaths paths(MediaRuntimeProperties properties);

    List<Path> defaultRoots(Map<String, String> environment);

    static MediaHostAdapter forOs(String name) {
        String normalized = name.toLowerCase(Locale.ROOT);
        if (normalized.startsWith("windows")) return new Windows();
        if (normalized.equals("linux")) return new Linux();
        return new Unsupported();
    }

    final class Windows implements MediaHostAdapter {
        public String os() {
            return "WINDOWS";
        }

        public String pathSeparator() {
            return ";";
        }

        public MediaRuntimeProperties.OsPaths paths(MediaRuntimeProperties p) {
            return p.windows();
        }

        public String kind(Path executable) {
            String name = executable.getFileName().toString().toLowerCase(Locale.ROOT);
            return name.equals("ffmpeg.exe")
                    ? "FFMPEG"
                    : name.equals("mediaserver.exe") ? "ZLM" : null;
        }

        public List<Path> defaultRoots(Map<String, String> environment) {
            String base = environment.getOrDefault("ProgramFiles", "C:\\Program Files");
            String x86 = environment.getOrDefault("ProgramFiles(x86)", "C:\\Program Files (x86)");
            return List.of(
                    Path.of(base, "ZLMediaKit"),
                    Path.of(base, "ffmpeg"),
                    Path.of(x86, "ZLMediaKit"),
                    Path.of(x86, "ffmpeg"));
        }
    }

    final class Linux implements MediaHostAdapter {
        public String os() {
            return "LINUX";
        }

        public String pathSeparator() {
            return ":";
        }

        public MediaRuntimeProperties.OsPaths paths(MediaRuntimeProperties p) {
            return p.linux();
        }

        public String kind(Path executable) {
            String name = executable.getFileName().toString();
            return name.equals("ffmpeg") ? "FFMPEG" : name.equals("MediaServer") ? "ZLM" : null;
        }

        public List<Path> defaultRoots(Map<String, String> environment) {
            return List.of(
                    Path.of("/usr/bin"),
                    Path.of("/usr/local/bin"),
                    Path.of("/opt/zlmediakit"),
                    Path.of("/opt/ffmpeg"));
        }
    }

    final class Unsupported implements MediaHostAdapter {
        public String os() {
            return "UNSUPPORTED";
        }

        public String pathSeparator() {
            return ":";
        }

        public MediaRuntimeProperties.OsPaths paths(MediaRuntimeProperties p) {
            return new MediaRuntimeProperties.OsPaths(null, null, null);
        }

        public String kind(Path executable) {
            return null;
        }

        public List<Path> defaultRoots(Map<String, String> environment) {
            return List.of();
        }
    }
}
