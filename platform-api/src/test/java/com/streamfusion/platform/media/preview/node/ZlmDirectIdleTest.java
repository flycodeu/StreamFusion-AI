package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ZlmDirectIdleTest {
    @TempDir Path directory;

    @Test
    void synchronizationRequiresValidEmptyPhysicalMediaObservation() throws Exception {
        var response = new AtomicReference<>("{\"code\":0,\"data\":[]}");
        var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/index/api/getMediaList",
                request -> {
                    String body =
                            java.net.URLDecoder.decode(
                                    new String(
                                            request.getRequestBody().readAllBytes(),
                                            StandardCharsets.UTF_8),
                                    StandardCharsets.UTF_8);
                    byte[] data =
                            ("secret=private-secret".equals(body)
                                            ? response.get()
                                            : "{\"code\":-100}")
                                    .getBytes(StandardCharsets.UTF_8);
                    request.sendResponseHeaders(200, data.length);
                    try (var out = request.getResponseBody()) {
                        out.write(data);
                    }
                });
        server.start();
        try {
            Path secret = directory.resolve("zlm.secret");
            OpsPreviewCatalog.writeProtectedSecret(secret, "private-secret");
            var connection =
                    new ZlmDirectConnection(
                            "zlm-1234567890abcdef",
                            "A",
                            URI.create("http://127.0.0.1:" + server.getAddress().getPort()),
                            secret,
                            "127.0.0.1");
            var gateway = new ZlmDirectGateway(new ObjectMapper());
            gateway.requireIdle(connection, PreviewDeadline.after(Duration.ofSeconds(2)));
            for (var value :
                    new String[] {"private-secret", "private-secret\n", "private-secret\r\n"}) {
                Files.writeString(secret, value);
                gateway.requireIdle(connection, PreviewDeadline.after(Duration.ofSeconds(2)));
            }
            response.set("{\"code\":0}");
            gateway.requireIdle(connection, PreviewDeadline.after(Duration.ofSeconds(2)));
            response.set("{\"code\":0,\"data\":[{\"schema\":\"rtsp\"}]}");
            assertThatThrownBy(
                            () ->
                                    gateway.requireIdle(
                                            connection,
                                            PreviewDeadline.after(Duration.ofSeconds(2))))
                    .hasMessage("NODE_RESOURCES_ACTIVE");
            for (var invalid :
                    new String[] {
                        "{\"code\":0,\"data\":null}",
                        "{\"code\":0,\"data\":{}}",
                        "{\"code\":0,\"unknown\":true}",
                        "{\"code\":\"0\"}"
                    }) {
                response.set(invalid);
                assertThatThrownBy(
                                () ->
                                        gateway.requireIdle(
                                                connection,
                                                PreviewDeadline.after(Duration.ofSeconds(2))))
                        .hasMessage("ZLM_INVALID_RESPONSE");
            }
            response.set("{\"code\":0}");
            for (var invalid :
                    new String[] {
                        " private-secret",
                        "private-secret ",
                        "private-secret\n\n",
                        "private-secret\r"
                    }) {
                Files.writeString(secret, invalid);
                assertThatThrownBy(
                                () ->
                                        gateway.requireIdle(
                                                connection,
                                                PreviewDeadline.after(Duration.ofSeconds(2))))
                        .hasMessage("NODE_CREDENTIAL_UNAVAILABLE");
            }
        } finally {
            server.stop(0);
        }
    }
}
