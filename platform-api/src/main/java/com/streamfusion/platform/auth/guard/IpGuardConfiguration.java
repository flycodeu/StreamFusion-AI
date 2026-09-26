package com.streamfusion.platform.auth.guard;

import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(IpGuardProperties.class)
public class IpGuardConfiguration {
    public IpGuardConfiguration(IpGuardProperties properties, Environment environment) {
        if (properties.enabled()
                && !"none"
                        .equalsIgnoreCase(
                                environment.getProperty("server.forward-headers-strategy", "none")))
            throw new IllegalArgumentException(
                    "IP guard requires socket addresses; configure trusted proxies explicitly");
    }
}
