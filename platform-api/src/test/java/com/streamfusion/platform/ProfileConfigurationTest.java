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
import org.springframework.core.env.StandardEnvironment;

class ProfileConfigurationTest {
    @TempDir Path tempDirectory;

    private ApplicationContextRunner configuration() {
        return new ApplicationContextRunner()
                .withInitializer(
                        context -> {
                            var sources = context.getEnvironment().getPropertySources();
                            sources.remove(
                                    StandardEnvironment.SYSTEM_ENVIRONMENT_PROPERTY_SOURCE_NAME);
                            sources.remove(
                                    StandardEnvironment.SYSTEM_PROPERTIES_PROPERTY_SOURCE_NAME);
                            new ConfigDataApplicationContextInitializer().initialize(context);
                        })
                .withPropertyValues("spring.config.location=classpath:/");
    }

    @ParameterizedTest
    @CsvSource({
        "dev,8080,DEBUG",
        "local,18765,DEBUG",
        "test,8080,INFO",
        "prod,8080,INFO",
        "it,0,WARN"
    })
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
    void defaultsToTestWithLoopbackServicesAndInjectedPasswords() {
        configuration()
                .withPropertyValues("DB_PASSWORD=db-fixture", "REDIS_PASSWORD=redis-fixture")
                .run(
                        context -> {
                            assertThat(context).hasNotFailed();
                            var env = context.getEnvironment();
                            assertThat(env.getDefaultProfiles()).containsExactly("test");
                            assertThat(env.getProperty("server.port")).isEqualTo("8080");
                            assertThat(env.getProperty("spring.datasource.url"))
                                    .startsWith("jdbc:mysql://127.0.0.1:3306/streamfusion?");
                            assertThat(env.getProperty("spring.datasource.username")).isEmpty();
                            assertThat(env.getProperty("spring.datasource.password"))
                                    .isEqualTo("db-fixture");
                            assertThat(env.getProperty("spring.data.redis.host"))
                                    .isEqualTo("127.0.0.1");
                            assertThat(env.getProperty("spring.data.redis.port")).isEqualTo("6379");
                            assertThat(env.getProperty("spring.data.redis.database"))
                                    .isEqualTo("10");
                            assertThat(env.getProperty("spring.data.redis.password"))
                                    .isEqualTo("redis-fixture");
                        });
    }

    @Test
    void runtimeEndpointOverridesDoNotAffectIntegrationTests() {
        var runner =
                configuration()
                        .withPropertyValues(
                                "DB_URL=jdbc:mysql://db.example.test:3310/other",
                                "DB_USERNAME=override",
                                "REDIS_HOST=redis.example.test",
                                "REDIS_PORT=6380",
                                "REDIS_DATABASE=8",
                                "REDIS_PASSWORD=redis-fixture");
        runner.withPropertyValues("spring.profiles.active=test")
                .run(
                        context -> {
                            assertThat(context).hasNotFailed();
                            var env = context.getEnvironment();
                            assertThat(env.getProperty("spring.datasource.url"))
                                    .isEqualTo("jdbc:mysql://db.example.test:3310/other");
                            assertThat(env.getProperty("spring.datasource.username"))
                                    .isEqualTo("override");
                            assertThat(env.getProperty("spring.data.redis.host"))
                                    .isEqualTo("redis.example.test");
                            assertThat(env.getProperty("spring.data.redis.port")).isEqualTo("6380");
                            assertThat(env.getProperty("spring.data.redis.database"))
                                    .isEqualTo("8");
                        });
        runner.withPropertyValues("spring.profiles.active=it")
                .run(
                        context -> {
                            assertThat(context).hasNotFailed();
                            var env = context.getEnvironment();
                            assertThat(env.getProperty("spring.datasource.url"))
                                    .startsWith("jdbc:h2:mem:");
                            assertThat(env.getProperty("spring.data.redis.host"))
                                    .isEqualTo("127.0.0.1");
                            assertThat(env.getProperty("spring.data.redis.password")).isEmpty();
                        });
    }

    @ParameterizedTest
    @CsvSource({"local-dev,dev,127.0.0.1,DEBUG", "local-test,test,db.example.test,INFO"})
    void selectsOnlyTheRequestedPersonalConfiguration(
            String profile, String baseProfile, String host, String level) throws IOException {
        Path configDir = Files.createDirectory(tempDirectory.resolve("config"));
        writePersonalConfig(configDir, "local-dev", "127.0.0.1");
        writePersonalConfig(configDir, "local-test", "db.example.test");

        configuration()
                .withPropertyValues(
                        "spring.config.additional-location=" + configDir.toUri(),
                        "spring.profiles.active=" + profile)
                .run(
                        context -> {
                            assertThat(context).hasNotFailed();
                            var env = context.getEnvironment();
                            assertThat(env.getActiveProfiles())
                                    .containsExactlyInAnyOrder(profile, baseProfile);
                            assertThat(env.getProperty("logging.level.com.streamfusion"))
                                    .isEqualTo(level);
                            assertThat(env.getProperty("spring.datasource.url"))
                                    .isEqualTo("jdbc:mysql://" + host + ":3306/personal");
                            assertThat(env.getProperty("spring.datasource.username"))
                                    .isEqualTo(profile);
                            assertThat(env.getProperty("spring.data.redis.host")).isEqualTo(host);
                        });
    }

    private static void writePersonalConfig(Path directory, String profile, String host)
            throws IOException {
        Files.writeString(
                directory.resolve("application-" + profile + ".yml"),
                """
                spring:
                  datasource:
                    url: jdbc:mysql://%s:3306/personal
                    username: %s
                  data:
                    redis:
                      host: %s
                """
                        .formatted(host, profile, host));
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
