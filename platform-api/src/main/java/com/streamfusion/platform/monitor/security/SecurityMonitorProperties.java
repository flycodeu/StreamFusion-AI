package com.streamfusion.platform.monitor.security;

import java.time.Duration;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties(prefix = "platform.security-monitor", ignoreUnknownFields = false)
public record SecurityMonitorProperties(
        @DefaultValue("true") boolean enabled,
        @DefaultValue("5m") Duration window,
        @DefaultValue("5m") Duration cooldown,
        @DefaultValue("8") int loginThreshold,
        @DefaultValue("10") int deniedThreshold,
        @DefaultValue("12") int scanThreshold,
        @DefaultValue("5") int csrfThreshold,
        @DefaultValue("12") int mixedThreshold) {
    public SecurityMonitorProperties {
        if (window == null
                || cooldown == null
                || window.compareTo(Duration.ofMinutes(1)) < 0
                || window.compareTo(Duration.ofMinutes(30)) > 0
                || cooldown.compareTo(window) < 0
                || cooldown.compareTo(Duration.ofHours(1)) > 0)
            throw new IllegalArgumentException("Invalid security monitor window/cooldown");
        for (int threshold :
                new int[] {
                    loginThreshold, deniedThreshold, scanThreshold, csrfThreshold, mixedThreshold
                }) {
            if (threshold < 3 || threshold > 100)
                throw new IllegalArgumentException("Security monitor thresholds must be 3..100");
        }
    }
}
