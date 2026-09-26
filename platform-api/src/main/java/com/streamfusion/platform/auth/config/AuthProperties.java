package com.streamfusion.platform.auth.config;

import java.time.Duration;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties(prefix = "platform.auth", ignoreUnknownFields = false)
public record AuthProperties(
        @DefaultValue("8") int minPasswordLength,
        @DefaultValue("64") int maxPasswordLength,
        @DefaultValue("true") boolean requireUppercase,
        @DefaultValue("true") boolean requireLowercase,
        @DefaultValue("true") boolean requireDigit,
        @DefaultValue("true") boolean requireSymbol,
        @DefaultValue("600000") int hashIterations,
        @DefaultValue("30m") Duration idleTimeout,
        @DefaultValue("8h") Duration absoluteTimeout,
        @DefaultValue("true") boolean secureCookie,
        @DefaultValue("") String initialPassword,
        @DefaultValue("0") long workerId,
        @DefaultValue("0") long datacenterId) {
    public AuthProperties {
        if (minPasswordLength < 8
                || maxPasswordLength < minPasswordLength
                || maxPasswordLength > 128
                || hashIterations != 600000
                || idleTimeout == null
                || idleTimeout.compareTo(Duration.ofSeconds(1)) < 0
                || absoluteTimeout == null
                || absoluteTimeout.compareTo(idleTimeout) < 0
                || absoluteTimeout.compareTo(Duration.ofDays(1)) > 0
                || workerId < 0
                || workerId > 31
                || datacenterId < 0
                || datacenterId > 31) {
            throw new IllegalArgumentException("Invalid platform.auth configuration");
        }
    }

    @Override
    public String toString() {
        return "AuthProperties[credentials=REDACTED]";
    }
}
