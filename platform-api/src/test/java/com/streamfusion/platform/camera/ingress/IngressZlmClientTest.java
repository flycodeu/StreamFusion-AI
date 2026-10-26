package com.streamfusion.platform.camera.ingress;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;

class IngressZlmClientTest {
    @Test
    void emptyListMayOmitDataButAnExistingExactReceiverNeverCountsAsReleased() throws Exception {
        var list = new AtomicReference<>("{\"code\":0}");
        var fixture = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 8);
        fixture.createContext(
                "/index/api/",
                exchange -> {
                    String result =
                            exchange.getRequestURI().getPath().endsWith("listRtpServer")
                                    ? list.get()
                                    : "{\"code\":0}";
                    byte[] bytes = result.getBytes(StandardCharsets.UTF_8);
                    exchange.sendResponseHeaders(200, bytes.length);
                    exchange.getResponseBody().write(bytes);
                    exchange.close();
                });
        fixture.start();
        try {
            var runtime =
                    new ActiveZlm(
                            URI.create("http://127.0.0.1:" + fixture.getAddress().getPort()),
                            "secret",
                            Path.of("zlm"),
                            Path.of("config.ini"),
                            "test");
            var entry =
                    GbRtpJournal.Entry.of(
                            runtime, "sf_gb_0123456789ab", "0123456789abcdef0123456789abcdef");
            var client = new IngressZlmClient(new ObjectMapper());
            client.close(runtime, entry, PreviewDeadline.after(Duration.ofSeconds(5)));
            list.set(
                    "{\"code\":0,\"data\":[{\"app\":\"rtp\",\"stream_id\":\"0123456789abcdef0123456789abcdef\"}]}");
            assertThatThrownBy(
                            () ->
                                    client.close(
                                            runtime,
                                            entry,
                                            PreviewDeadline.after(Duration.ofSeconds(5))))
                    .hasMessage("GB_RECOVERY_REQUIRED");
            list.set("{\"code\":0,\"data\":{}}");
            assertThatThrownBy(
                            () ->
                                    client.close(
                                            runtime,
                                            entry,
                                            PreviewDeadline.after(Duration.ofSeconds(5))))
                    .hasMessage("GB_RECOVERY_REQUIRED");
        } finally {
            fixture.stop(0);
        }
    }
}
