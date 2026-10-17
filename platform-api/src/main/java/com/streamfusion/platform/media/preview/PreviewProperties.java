package com.streamfusion.platform.media.preview;

import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

/** Deployment budgets, not a claim of tested server throughput. */
@ConfigurationProperties(prefix = "platform.preview", ignoreUnknownFields = false)
public record PreviewProperties(
        @DefaultValue("64") int maxSessions, @DefaultValue("64") int maxTranscodes) {
    public PreviewProperties {
        if (maxSessions < 1 || maxSessions > 64 || maxTranscodes < 0 || maxTranscodes > 64)
            throw new IllegalArgumentException("Invalid preview capacity limits");
    }
}
