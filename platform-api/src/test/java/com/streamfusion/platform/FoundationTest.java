package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import ch.qos.logback.classic.LoggerContext;
import ch.qos.logback.classic.PatternLayout;
import ch.qos.logback.classic.spi.LoggingEvent;
import ch.qos.logback.classic.spi.ThrowableProxy;
import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.common.logging.LogRedactor;
import com.streamfusion.platform.common.logging.SafeExceptionConverter;
import com.streamfusion.platform.common.logging.SafeMessageConverter;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.boot.test.web.client.TestRestTemplate;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Import;
import org.springframework.http.HttpEntity;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpMethod;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@SpringBootTest(
        classes = PlatformApplication.class,
        webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
@ActiveProfiles("test")
@Import(FoundationTest.TestEndpoints.class)
class FoundationTest {
    private final TestRestTemplate client;

    @Autowired
    FoundationTest(TestRestTemplate client) {
        this.client = client;
    }

    @RestController
    static class TestEndpoints {
        @GetMapping("/test-only/failure")
        String failure() {
            throw new IllegalStateException("password=dummy-secret");
        }

        @GetMapping("/test-only/validation")
        int validation(@RequestParam int count) {
            return count;
        }
    }

    @ParameterizedTest
    @CsvSource({
        "/test-only/failure, 500, INTERNAL_ERROR",
        "/missing, 404, NOT_FOUND",
        "/test-only/validation?count=dummy-secret, 400, VALIDATION_ERROR"
    })
    void errorsPreserveStatusAndTraceWithoutExposingInput(String path, int status, String code) {
        var headers = new HttpHeaders();
        String trace = "a".repeat(32);
        headers.set("X-Trace-Id", trace);
        var response =
                client.exchange(path, HttpMethod.GET, new HttpEntity<>(headers), JsonNode.class);
        assertThat(response.getStatusCode().value()).isEqualTo(status);
        assertThat(response.getHeaders().getFirst("X-Trace-Id")).isEqualTo(trace);
        var body = response.getBody();
        assertThat(body).isNotNull();
        assertThat(body.get("traceId").asText()).isEqualTo(trace);
        assertThat(body.get("code").asText()).isEqualTo(code);
        assertThat(body.toString()).doesNotContain("dummy-secret", "IllegalStateException");
        assertThat(Instant.parse(body.get("timestamp").asText())).isNotNull();
    }

    @Test
    void rejectsInvalidTraceAndPreservesMethodNotAllowed() {
        var headers = new HttpHeaders();
        headers.set("X-Trace-Id", "invalid");
        var response =
                client.exchange(
                        "/test-only/validation",
                        HttpMethod.POST,
                        new HttpEntity<>(headers),
                        JsonNode.class);
        assertThat(response.getStatusCode().value()).isEqualTo(405);
        assertThat(response.getBody().get("code").asText()).isEqualTo("METHOD_NOT_ALLOWED");
        assertThat(response.getHeaders().getFirst("X-Trace-Id")).matches("[a-f0-9]{32}");
        assertThat(response.getHeaders().getAllow()).contains(HttpMethod.GET);
    }

    @Test
    void publishesOpenApi() {
        var response = client.getForEntity("/v3/api-docs", JsonNode.class);
        assertThat(response.getStatusCode().value()).isEqualTo(200);
        assertThat(response.getBody().get("openapi").asText()).startsWith("3.");
    }

    @Configuration(proxyBeanMethods = false)
    @EnableConfigurationProperties(PlatformProperties.class)
    static class PropertiesOnly {}

    @Test
    void failsOnInvalidOrUnknownConfiguration() {
        for (String property :
                new String[] {
                    "streamfusion.shutdown-grace=0s",
                    "streamfusion.shutdown-grace=121s",
                    "streamfusion.unknown=true"
                }) {
            new ApplicationContextRunner()
                    .withUserConfiguration(PropertiesOnly.class)
                    .withPropertyValues(property)
                    .run(context -> assertThat(context).hasFailed());
        }
    }

    @Test
    void validatesDirectoryWithoutCreatingIt(@TempDir Path temp) throws Exception {
        Path target = temp.resolve("not-created");
        new PlatformProperties(target, Duration.ofSeconds(15));
        assertThat(target).doesNotExist();
        Path file = Files.createFile(temp.resolve("file"));
        assertThatThrownBy(
                        () -> new PlatformProperties(file.resolve("child"), Duration.ofSeconds(15)))
                .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void redactsCredentialsAndBoundsLogMessages() {
        String safe =
                LogRedactor.redact(
                        "password=dummy-secret rtsp://alice:private@camera/stream Bearer dummy-token");
        assertThat(safe).doesNotContain("dummy-secret", "alice:private", "dummy-token");
        assertThat(LogRedactor.redact("x".repeat(20000))).hasSize(16384);
    }

    @Test
    void logbackEncodesMessageAndExceptionWithoutRawFallback() {
        var context = new LoggerContext();
        try {
            var layout = new PatternLayout();
            layout.setContext(context);
            layout.getInstanceConverterMap().put("safeMessage", SafeMessageConverter::new);
            layout.getInstanceConverterMap().put("safeException", SafeExceptionConverter::new);
            layout.setPattern("%safeMessage%n%safeException");
            layout.start();
            var event = new LoggingEvent();
            event.setMessage("password=dummy-message");
            event.setThrowableProxy(
                    new ThrowableProxy(new IllegalStateException("token=dummy-exception")));
            String output = layout.doLayout(event);
            assertThat(output).contains("[REDACTED]", "IllegalStateException");
            assertThat(output).doesNotContain("dummy-message", "dummy-exception");
            layout.stop();
        } finally {
            context.stop();
        }
    }
}
