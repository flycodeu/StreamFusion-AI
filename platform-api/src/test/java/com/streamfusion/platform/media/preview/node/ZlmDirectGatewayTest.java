package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ZlmDirectGatewayTest {
    @TempDir Path directory;

    @Test
    void privateHttpZlmUsesServerSideSecretAndConfirmsExactCleanup() throws Exception {
        Path secret = directory.resolve("zlm.secret");
        Files.writeString(secret, "existing-short-secret", StandardCharsets.UTF_8);
        var streamOnline = new AtomicBoolean();
        var authorized = new AtomicBoolean(true);
        var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/index/api/",
                exchange -> {
                    String action =
                            exchange.getRequestURI().getPath().substring("/index/api/".length());
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    boolean hasSecret =
                            "webrtc".equals(action)
                                    ? exchange.getRequestURI()
                                            .getRawQuery()
                                            .contains("secret=existing-short-secret")
                                    : body.contains("secret=existing-short-secret");
                    if (!hasSecret) authorized.set(false);
                    String response =
                            switch (action) {
                                case "getServerConfig" ->
                                        "{\"code\":0,\"data\":[{\"rtsp.directProxy\":\"0\",\"general.streamNoneReaderDelayMS\":30000,\"rtc.port\":8000,\"rtsp.port\":8554,\"rtc.externIP\":\"127.0.0.1\",\"api.apiDebug\":\"0\",\"http.allow_ip_range\":\"127.0.0.1\"}]}";
                                case "version" ->
                                        "{\"code\":0,\"data\":{\"commitHash\":\"de04a56\",\"buildTime\":\"2026-10-10T21:29:04\"}}";
                                case "addStreamProxy" -> {
                                    streamOnline.set(true);
                                    yield "{\"code\":0,\"data\":{\"key\":\"__defaultVhost__/sf_preview_123456789abc/1234567890abcdef1234567890abcdef\"}}";
                                }
                                case "getMediaList" ->
                                        "{\"code\":0,\"data\":[{\"tracks\":[{\"codec_type\":0,\"ready\":true,\"codec_id_name\":\"H264\",\"width\":1920,\"height\":1080}]}]}";
                                case "isMediaOnline" ->
                                        "{\"code\":0,\"online\":" + streamOnline.get() + "}";
                                case "webrtc" ->
                                        "{\"code\":0,\"type\":\"answer\",\"sdp\":\"v=0\\r\\n\"}";
                                case "delStreamProxy", "close_streams" -> {
                                    streamOnline.set(false);
                                    yield "{\"code\":0}";
                                }
                                default -> "{\"code\":-1}";
                            };
                    byte[] bytes = response.getBytes(StandardCharsets.UTF_8);
                    exchange.getResponseHeaders().add("Content-Type", "application/json");
                    exchange.sendResponseHeaders(200, bytes.length);
                    try (var output = exchange.getResponseBody()) {
                        output.write(bytes);
                    }
                });
        server.start();
        try {
            var connection =
                    new ZlmDirectConnection(
                            "zlm-1234567890abcdef",
                            URI.create("http://127.0.0.1:" + server.getAddress().getPort()),
                            secret,
                            "127.0.0.1");
            var gateway = new ZlmDirectGateway(new ObjectMapper());
            var deadline = PreviewDeadline.after(Duration.ofSeconds(5));
            gateway.requireReady(connection, deadline);
            assertThat(gateway.version(connection, deadline))
                    .isEqualTo("de04a56 (2026-10-10T21:29:04)");
            var stream =
                    new ZlmDirectGateway.Stream(
                            "sf_preview_123456789abc", "1234567890abcdef1234567890abcdef");
            gateway.open(connection, stream, URI.create("rtsp://127.0.0.1/live"), deadline);
            assertThat(gateway.exists(connection, stream, deadline)).isTrue();
            assertThat(gateway.stats(connection, stream, deadline).videoCodec()).isEqualTo("H264");
            assertThat(gateway.answer(connection, stream, "v=0\r\n", deadline).type())
                    .isEqualTo("answer");
            gateway.close(connection, stream, deadline);
            assertThat(gateway.exists(connection, stream, deadline)).isFalse();
            assertThat(authorized.get()).isTrue();
        } finally {
            server.stop(0);
        }
    }
}
