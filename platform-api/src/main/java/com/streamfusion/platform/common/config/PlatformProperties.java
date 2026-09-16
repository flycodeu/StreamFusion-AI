package com.streamfusion.platform.common.config;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties(prefix = "streamfusion", ignoreUnknownFields = false)
public record PlatformProperties(
        @DefaultValue(".run") Path workDir, @DefaultValue("15s") Duration shutdownGrace) {
    public PlatformProperties {
        validateDuration("shutdown-grace", shutdownGrace);
        workDir = workDir.toAbsolutePath().normalize();
        Path ancestor = workDir;
        while (ancestor != null && !Files.exists(ancestor)) {
            ancestor = ancestor.getParent();
        }
        if (ancestor == null || !Files.isDirectory(ancestor) || !Files.isWritable(ancestor)) {
            throw new IllegalArgumentException(
                    "streamfusion.work-dir must have a writable directory ancestor");
        }
    }

    private static void validateDuration(String field, Duration duration) {
        if (duration == null
                || duration.compareTo(Duration.ofSeconds(1)) < 0
                || duration.compareTo(Duration.ofSeconds(120)) > 0) {
            throw new IllegalArgumentException(
                    "streamfusion." + field + " must be between 1s and 120s");
        }
    }
}
