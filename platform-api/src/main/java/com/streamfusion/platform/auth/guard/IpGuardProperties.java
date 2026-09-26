package com.streamfusion.platform.auth.guard;

import java.time.Duration;
import java.util.List;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties(prefix = "platform.ip-guard", ignoreUnknownFields = false)
public record IpGuardProperties(
        @DefaultValue("true") boolean enabled,
        @DefaultValue("20") int failureThreshold,
        @DefaultValue("10m") Duration failureWindow,
        @DefaultValue("120") int resourceLimit,
        @DefaultValue("1m") Duration resourceWindow,
        @DefaultValue("streamfusion:guard") String redisNamespace,
        @DefaultValue List<String> trustedProxies) {
    public IpGuardProperties {
        if (failureThreshold < 5
                || failureThreshold > 1000
                || resourceLimit < failureThreshold
                || resourceLimit > 10000
                || failureWindow == null
                || failureWindow.toSeconds() < 60
                || failureWindow.compareTo(Duration.ofHours(24)) > 0
                || resourceWindow == null
                || resourceWindow.toSeconds() < 1
                || resourceWindow.compareTo(Duration.ofMinutes(60)) > 0
                || redisNamespace == null
                || !redisNamespace.matches("[A-Za-z0-9:,_-]{1,128}")
                || trustedProxies == null
                || trustedProxies.size() > 32)
            throw new IllegalArgumentException("Invalid IP guard configuration");
        trustedProxies = trustedProxies.stream().map(IpAddresses::canonical).distinct().toList();
    }
}
