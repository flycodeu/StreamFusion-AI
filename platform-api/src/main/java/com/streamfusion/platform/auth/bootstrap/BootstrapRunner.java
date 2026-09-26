package com.streamfusion.platform.auth.bootstrap;

import com.streamfusion.platform.auth.service.BootstrapService;
import java.io.Console;
import java.util.Arrays;
import lombok.RequiredArgsConstructor;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.boot.autoconfigure.condition.ConditionalOnNotWebApplication;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.core.env.Environment;
import org.springframework.core.env.Profiles;
import org.springframework.stereotype.Component;

@Component
@ConditionalOnNotWebApplication
@ConditionalOnProperty(name = "platform.bootstrap.enabled", havingValue = "true")
@RequiredArgsConstructor
public class BootstrapRunner implements ApplicationRunner {
    private final BootstrapService bootstrap;
    private final Environment environment;

    @Override
    public void run(ApplicationArguments arguments) {
        Long departmentId = environment.getProperty("platform.bootstrap.department-id", Long.class);
        if (environment.getProperty("platform.bootstrap.default-admin", Boolean.class, false)) {
            String initialPassword = environment.getProperty("SF_BOOTSTRAP_PASSWORD");
            if ((initialPassword == null || initialPassword.isBlank())
                    && environment.acceptsProfiles(Profiles.of("dev", "local"))
                    && !environment.acceptsProfiles(Profiles.of("prod"))) {
                initialPassword = environment.getProperty("platform.auth.initial-password");
            }
            if (initialPassword == null || initialPassword.isBlank()) {
                throw new IllegalStateException(
                        "SF_BOOTSTRAP_PASSWORD is required for default administrator bootstrap");
            }
            bootstrap.initializeDefaultAdmin(
                    environment.getRequiredProperty("platform.bootstrap.username"),
                    environment.getProperty("platform.bootstrap.nickname"),
                    initialPassword,
                    departmentId);
            return;
        }
        Console console = System.console();
        if (console == null)
            throw new IllegalStateException("Bootstrap requires an interactive console");
        String username = console.readLine("Account: ");
        String nickname = console.readLine("Nickname (optional): ");
        char[] first = console.readPassword("Password: ");
        char[] second = console.readPassword("Confirm password: ");
        try {
            if (first == null || second == null || !Arrays.equals(first, second)) {
                throw new IllegalArgumentException("Password confirmation does not match");
            }
            long id = bootstrap.initialize(username, nickname, new String(first), departmentId);
            console.printf("Super administrator created. ID: %s%n", id);
        } finally {
            if (first != null) Arrays.fill(first, '\0');
            if (second != null) Arrays.fill(second, '\0');
        }
    }
}
