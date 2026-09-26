package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.springframework.boot.test.context.ConfigDataApplicationContextInitializer;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class ProfileConfigurationTest {
    @TempDir Path tempDirectory;

    private ApplicationContextRunner configuration() {
        return new ApplicationContextRunner()
                .withInitializer(new ConfigDataApplicationContextInitializer())
                .withPropertyValues("spring.config.location=classpath:/");
    }

    @ParameterizedTest
    @CsvSource({"dev,8080,DEBUG", "local,18765,DEBUG", "prod,8080,INFO", "test,0,WARN"})
    void loadsSelectedProfileWithoutLeakingLocalSettings(String profile, int port, String level)
            throws IOException {
        Path localConfig = tempDirectory.resolve("application-local.yml");
        Files.writeString(
                localConfig,
                """
                server:
                  address: 127.0.0.1
                  port: 18765
                logging:
                  level:
                    com.streamfusion: DEBUG
                local-only-marker: private-value
                """);

        configuration()
                .withPropertyValues(
                        "spring.profiles.active=" + profile,
                        "LOCAL_CONFIG_PATH=" + localConfig.toString().replace('\\', '/'))
                .run(
                        context -> {
                            assertThat(context).hasNotFailed();
                            var environment = context.getEnvironment();
                            assertThat(environment.getActiveProfiles()).containsExactly(profile);
                            assertThat(environment.getProperty("server.port", Integer.class))
                                    .isEqualTo(port);
                            assertThat(environment.getProperty("logging.level.com.streamfusion"))
                                    .isEqualTo(level);
                            assertThat(environment.containsProperty("local-only-marker"))
                                    .isEqualTo(profile.equals("local"));
                            assertThat(environment.getProperty("platform.auth.initial-password"))
                                    .isEqualTo(
                                            profile.equals("dev") || profile.equals("local")
                                                    ? "StreamFusion@123"
                                                    : "");
                            assertThat(
                                            environment.getProperty(
                                                    "management.endpoints.web.exposure.include"))
                                    .isEqualTo("health");
                        });
    }

    @Test
    void defaultsToDevelopment() {
        configuration()
                .run(
                        context -> {
                            assertThat(context).hasNotFailed();
                            assertThat(context.getEnvironment().getDefaultProfiles())
                                    .containsExactly("dev");
                            assertThat(context.getEnvironment().getProperty("server.port"))
                                    .isEqualTo("8080");
                        });
    }

    @Test
    void localProfileRequiresItsExternalFile() {
        configuration()
                .withPropertyValues(
                        "spring.profiles.active=local",
                        "LOCAL_CONFIG_PATH="
                                + tempDirectory
                                        .resolve("missing.yml")
                                        .toString()
                                        .replace('\\', '/'))
                .run(context -> assertThat(context).hasFailed());
    }
}
