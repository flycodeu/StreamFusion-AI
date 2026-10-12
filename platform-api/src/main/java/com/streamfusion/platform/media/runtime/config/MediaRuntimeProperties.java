package com.streamfusion.platform.media.runtime.config;

import java.time.Duration;
import java.util.List;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties(prefix = "platform.media-runtime", ignoreUnknownFields = false)
public record MediaRuntimeProperties(
        @DefaultValue("15s") Duration cacheTtl,
        @DefaultValue("2s") Duration probeTimeout,
        @DefaultValue("12s") Duration collectionTimeout,
        @DefaultValue("3") int searchDepth,
        @DefaultValue("2000") int maxEntries,
        @DefaultValue("16") int maxCandidates,
        @DefaultValue OsPaths windows,
        @DefaultValue OsPaths linux) {
    public MediaRuntimeProperties {
        if (cacheTtl == null
                || cacheTtl.toSeconds() < 5
                || cacheTtl.toSeconds() > 300
                || probeTimeout == null
                || probeTimeout.toMillis() < 100
                || probeTimeout.toMillis() > 3000
                || collectionTimeout == null
                || collectionTimeout.toSeconds() < 2
                || collectionTimeout.toSeconds() > 30
                || searchDepth < 0
                || searchDepth > 5
                || maxEntries < 100
                || maxEntries > 10000
                || maxCandidates < 1
                || maxCandidates > 32) {
            throw new IllegalArgumentException("Invalid media runtime detection limits");
        }
        windows = windows == null ? new OsPaths(null, null, null) : windows;
        linux = linux == null ? new OsPaths(null, null, null) : linux;
    }

    /**
     * Paths are administrator supplied deployment configuration, never commands from HTTP input.
     */
    public record OsPaths(
            List<String> searchRoots, List<String> executables, List<String> zlmConfigs) {
        public OsPaths {
            searchRoots = safePaths(searchRoots);
            executables = safePaths(executables);
            zlmConfigs = safePaths(zlmConfigs);
        }

        private static List<String> safePaths(List<String> paths) {
            if (paths == null) return List.of();
            if (paths.size() > 16
                    || paths.stream()
                            .anyMatch(
                                    path ->
                                            path == null
                                                    || path.length() > 4096
                                                    || !(path.startsWith("/")
                                                            || path.matches(
                                                                    "[A-Za-z]:[/\\\\].*")))) {
                throw new IllegalArgumentException(
                        "Media runtime paths must be absolute and bounded");
            }
            return paths.stream().distinct().toList();
        }
    }
}
