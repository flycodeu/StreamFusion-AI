package com.streamfusion.platform.media.runtime.service;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.runtime.config.MediaRuntimeProperties;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.http.HttpClient;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class RuntimeProbeTest {
    @TempDir Path directory;

    private RuntimeProbe probe() {
        var properties =
                new MediaRuntimeProperties(
                        true,
                        Duration.ofSeconds(15),
                        Duration.ofMillis(500),
                        Duration.ofSeconds(2),
                        2,
                        100,
                        4,
                        null,
                        null);
        return new RuntimeProbe(properties, new ObjectMapper(), HttpClient.newHttpClient());
    }

    @Test
    void parsesVersionOnlyFromFfmpegOutput() {
        assertThat(RuntimeProbe.parseFfmpegVersion("ffmpeg version 7.1-full_build Copyright\n"))
                .isEqualTo("7.1-full_build");
        assertThat(RuntimeProbe.parseFfmpegVersion("MediaServer version 1.0")).isNull();
        assertThat(RuntimeProbe.parseFfmpegVersion("ffmpeg version bad@value")).isNull();
    }

    @Test
    void parsesSectionsAndRtcAliasesWithoutExposingSecret() throws Exception {
        Path file = directory.resolve("config.ini");
        Files.writeString(
                file,
                "[http]\nport=8088\n[api]\nsecret=hidden\n[rtc]\nport=18000\ntcpPort=18001\n");
        var value = probe().readZlm(file);
        assertThat(value.apiBase().toString()).isEqualTo("http://127.0.0.1:8088");
        assertThat(value.secret()).isEqualTo("hidden");
        assertThat(value.ports()).extracting("port").contains(8088, 18000, 18001);
        assertThat(value.toString()).doesNotContain("hidden");
    }

    @Test
    void boundsConfigReadsAndValidatesPortNumbers() throws Exception {
        Path file = directory.resolve("config.ini");
        Files.writeString(file, "x".repeat(262145));
        assertThatThrownBy(() -> probe().readZlm(file)).isInstanceOf(java.io.IOException.class);
        Files.writeString(file, "[http]\nport=65536\n");
        assertThatThrownBy(() -> probe().readZlm(file))
                .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void authenticatesAndConfirmsProcessBeforeTreatingZlmAsReady() throws Exception {
        AtomicReference<String> response = new AtomicReference<>("{\"code\":-100}");
        AtomicReference<String> query = new AtomicReference<>();
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/index/api/version",
                exchange -> {
                    query.set(exchange.getRequestURI().getRawQuery());
                    byte[] bytes = response.get().getBytes(StandardCharsets.UTF_8);
                    exchange.sendResponseHeaders(200, bytes.length);
                    try (var output = exchange.getResponseBody()) {
                        output.write(bytes);
                    }
                });
        server.start();
        try {
            Path binary = directory.resolve("MediaServer");
            Files.writeString(binary, "must never run");
            Path config = directory.resolve("config.ini");
            Files.writeString(
                    config,
                    "[http]\nport="
                            + server.getAddress().getPort()
                            + "\n[api]\nsecret=a&b+c\n[rtsp]\nport=0\n[rtmp]\nport=0\n[rtc]\nport=0\ntcpPort=0\n");
            var found = new RuntimeDiscovery.Discovered("ZLM", binary, config, List.of(99L), true);
            var failed = probe().inspect(found, Instant.now(), deadline());
            assertThat(failed.candidate().status()).isEqualTo("PROBE_FAILED");
            assertThat(failed.candidate().diagnostic().code()).isEqualTo("API_AUTH_FAILED");
            assertThat(query.get()).contains("secret=a%26b%2Bc");

            response.set(
                    "{\"code\":0,\"data\":{\"commitHash\":\"abc123\",\"buildTime\":\"today\"}}");
            assertThat(probe().inspect(found, Instant.now(), deadline()).candidate().status())
                    .isEqualTo("READY");
            var unknown =
                    new RuntimeDiscovery.Discovered("ZLM", binary, config, List.of(99L), false);
            assertThat(probe().inspect(unknown, Instant.now(), deadline()).candidate().status())
                    .isEqualTo("API_AVAILABLE");
            response.set("x".repeat(40000));
            assertThat(probe().inspect(found, Instant.now(), deadline()).candidate().status())
                    .isEqualTo("PROBE_FAILED");
        } finally {
            server.stop(0);
        }
    }

    @Test
    void candidateIdSeparatesBinaryAndConfigurationIdentity() {
        assertThat(
                        RuntimeProbe.candidateId(
                                "ZLM", Path.of("/a/MediaServer"), Path.of("/a/config.ini")))
                .isNotEqualTo(
                        RuntimeProbe.candidateId(
                                "ZLM", Path.of("/a/MediaServer"), Path.of("/b/config.ini")));
    }

    private static long deadline() {
        return System.nanoTime() + TimeUnit.SECONDS.toNanos(2);
    }
}
