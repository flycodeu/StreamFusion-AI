package com.streamfusion.platform.media.preview;

import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

/** Deployment budgets, not a claim of tested server throughput. */
@ConfigurationProperties(prefix = "platform.preview", ignoreUnknownFields = false)
public record PreviewProperties(
        @DefaultValue("36") int maxSessions, @DefaultValue("2") int maxTranscodes) {
    public PreviewProperties {
        if (maxSessions < 1 || maxSessions > 64 || maxTranscodes < 0 || maxTranscodes > 8)
            throw new IllegalArgumentException("Invalid preview capacity limits");
    }
}
