package com.streamfusion.platform.auth.config;

import com.baomidou.mybatisplus.core.incrementer.DefaultIdentifierGenerator;
import com.baomidou.mybatisplus.core.incrementer.IdentifierGenerator;
import java.time.Clock;
import java.util.Map;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;
import org.springframework.core.env.Profiles;
import org.springframework.security.crypto.password.DelegatingPasswordEncoder;
import org.springframework.security.crypto.password.PasswordEncoder;
import org.springframework.security.crypto.password.Pbkdf2PasswordEncoder;

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(AuthProperties.class)
public class AuthConfiguration {
    @Bean
    Clock identityClock() {
        return Clock.systemUTC();
    }

    @Bean
    IdentifierGenerator identifierGenerator(AuthProperties properties) {
        return new DefaultIdentifierGenerator(properties.workerId(), properties.datacenterId());
    }

    @Bean
    PasswordEncoder passwordEncoder(AuthProperties properties, Environment environment) {
        if (environment.acceptsProfiles(Profiles.of("prod")) && !properties.secureCookie()) {
            throw new IllegalArgumentException("Production session cookies require HTTPS");
        }
        var encoder = new Pbkdf2PasswordEncoder("", 16, properties.hashIterations(), 256);
        encoder.setAlgorithm(Pbkdf2PasswordEncoder.SecretKeyFactoryAlgorithm.PBKDF2WithHmacSHA256);
        return new DelegatingPasswordEncoder(
                "pbkdf2-sha256-v1", Map.of("pbkdf2-sha256-v1", encoder));
    }
}
