package com.streamfusion.platform.server.config;

import java.time.Duration;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties("platform.server-monitor")
public record ServerMonitorProperties(
        @DefaultValue("30s") Duration cacheTtl,
        @DefaultValue("500ms") Duration probeTimeout,
        @DefaultValue("6s") Duration collectionTimeout,
        @DefaultValue("127.0.0.1") String agentHost,
        @DefaultValue("8100") int agentPort,
        @DefaultValue("127.0.0.1") String runtimeHost,
        @DefaultValue("8101") int runtimePort,
        @DefaultValue("127.0.0.1") String webHost,
        @DefaultValue("8090") int webPort,
        @DefaultValue("true") boolean gpuEnabled) {
    public ServerMonitorProperties {
        if (cacheTtl == null
                || cacheTtl.compareTo(Duration.ofSeconds(5)) < 0
                || cacheTtl.compareTo(Duration.ofMinutes(5)) > 0
                || probeTimeout == null
                || probeTimeout.toMillis() < 100
                || probeTimeout.toMillis() > 2000
                || collectionTimeout == null
                || collectionTimeout.toSeconds() < 2
                || collectionTimeout.toSeconds() > 10)
            throw new IllegalArgumentException("Invalid monitoring time limits");
        if (agentPort < 0
                || agentPort > 65535
                || runtimePort < 0
                || runtimePort > 65535
                || webPort < 0
                || webPort > 65535) throw new IllegalArgumentException("Invalid monitoring port");
        if (!validHost(agentHost) || !validHost(runtimeHost) || !validHost(webHost))
            throw new IllegalArgumentException(
                    "Monitoring hosts must be explicit hostnames or IP addresses");
    }

    private static boolean validHost(String host) {
        return host != null
                && (host.isEmpty() || (host.length() <= 253 && host.matches("[A-Za-z0-9.:-]+")));
    }
}
