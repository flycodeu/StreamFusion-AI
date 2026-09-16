package com.streamfusion.platform.common.config;

import org.springframework.boot.autoconfigure.web.ServerProperties;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;
import org.springframework.core.env.Profiles;

@Configuration
@EnableConfigurationProperties(PlatformProperties.class)
public class FoundationConfiguration {
    public FoundationConfiguration(ServerProperties server, Environment environment) {
        int port = server.getPort() == null ? 8080 : server.getPort();
        boolean test = environment.acceptsProfiles(Profiles.of("test"));
        if (port < (test ? 0 : 1) || port > 65535) {
            throw new IllegalArgumentException("server.port must be 1..65535 (0 allowed in test)");
        }
    }
}
