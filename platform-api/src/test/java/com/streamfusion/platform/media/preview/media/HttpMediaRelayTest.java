package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.*;

import com.streamfusion.platform.media.preview.PreviewFailure;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;

class HttpMediaRelayTest {
    private final HttpClient http =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(2)).build();

    @Test
    void closingOwnerInterruptsAnActiveFlvResponse() throws Exception {
        var release = new java.util.concurrent.CountDownLatch(1);
        var fixture = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 8);
        fixture.createContext(
                "/live.flv",
                exchange -> {
                    exchange.sendResponseHeaders(200, 0);
                    exchange.getResponseBody()
                            .write("FLV-data".getBytes(StandardCharsets.US_ASCII));
                    exchange.getResponseBody().flush();
                    try {
                        release.await(3, java.util.concurrent.TimeUnit.SECONDS);
                    } catch (InterruptedException interrupted) {
                        Thread.currentThread().interrupt();
                    } finally {
                        exchange.close();
                    }
                });
        fixture.start();
        try (var relay = new HttpMediaRelay()) {
            URI uri =
                    relay.open(
                            "active",
                            URI.create(
                                    "http://127.0.0.1:"
                                            + fixture.getAddress().getPort()
                                            + "/live.flv"));
            var response =
                    http.send(
                            HttpRequest.newBuilder(uri)
                                    .timeout(Duration.ofSeconds(3))
                                    .GET()
                                    .build(),
                            HttpResponse.BodyHandlers.ofInputStream());
            var executor = java.util.concurrent.Executors.newSingleThreadExecutor();
            try {
                var reading =
                        executor.submit(
                                () -> {
                                    try (var body = response.body()) {
                                        body.readAllBytes();
                                    } catch (java.io.IOException closed) {
                                        /* expected abrupt close */
                                    }
                                });
                relay.close("active");
                reading.get(3, java.util.concurrent.TimeUnit.SECONDS);
                assertThat(relay.sessionCount()).isZero();
            } finally {
                executor.shutdownNow();
            }
        } finally {
            release.countDown();
            fixture.stop(0);
        }
    }

    @Test
    void hlsRewritesSignedRelativeUrlsAndKeepsPinnedHostAndCredentials() throws Exception {
        var host = new AtomicReference<String>();
        var auth = new AtomicReference<String>();
        var fixture = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 8);
        fixture.createContext(
                "/",
                e -> {
                    host.set(e.getRequestHeaders().getFirst("Host"));
                    auth.set(e.getRequestHeaders().getFirst("Authorization"));
                    String path = e.getRequestURI().getRawPath();
                    String body =
                            path.endsWith("master.m3u8")
                                    ? "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100\nchild.m3u8?sig=secret\n"
                                    : path.endsWith("child.m3u8")
                                            ? "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nsegment.ts?sig=secret\n#EXT-X-ENDLIST\n"
                                            : "sample-ts-payload";
                    byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
                    e.sendResponseHeaders(200, bytes.length);
                    e.getResponseBody().write(bytes);
                    e.close();
                });
        fixture.start();
        try (var relay = new HttpMediaRelay()) {
            int port = fixture.getAddress().getPort();
            URI uri =
                    relay.open(
                            "owner",
                            URI.create(
                                    "http://user:p%2Bass@127.0.0.1:"
                                            + port
                                            + "/master.m3u8#sf-origin=camera.invalid"));
            String master = get(uri).body();
            assertThat(master).doesNotContain("secret", "camera.invalid", "user:");
            String child =
                    get(URI.create(
                                    master.lines()
                                            .filter(l -> l.startsWith("http"))
                                            .findFirst()
                                            .orElseThrow()))
                            .body();
            String segment =
                    get(URI.create(
                                    child.lines()
                                            .filter(l -> l.startsWith("http"))
                                            .findFirst()
                                            .orElseThrow()))
                            .body();
            assertThat(segment).isEqualTo("sample-ts-payload");
            assertThat(host.get()).isEqualTo("camera.invalid:" + port);
            assertThat(auth.get()).isEqualTo("Basic dXNlcjpwK2Fzcw==");
            relay.close("owner");
            assertThat(relay.sessionCount()).isZero();
            assertThat(get(uri).statusCode()).isEqualTo(502);
        } finally {
            fixture.stop(0);
        }
    }

    @Test
    void unsupportedPlaylistsFailWithActionableReasons() throws Exception {
        try (var relay = new HttpMediaRelay()) {
            var origin = URI.create("http://127.0.0.1:80/live/index.m3u8");
            relay.open("test", origin);
            var session = new HttpMediaRelay.Session("token", origin, origin);
            for (String value :
                    new String[] {
                        "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"",
                        "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"",
                        "#EXTM3U\nhttp://elsewhere.invalid/a.ts",
                        "not-hls"
                    })
                assertThatThrownBy(() -> relay.rewrite(session, origin, value))
                        .isInstanceOf(PreviewFailure.class);
            assertThatThrownBy(
                            () ->
                                    HttpMediaRelay.requireOrigin(
                                            origin, URI.create("http://127.0.0.2/live/a.ts")))
                    .isInstanceOf(PreviewFailure.class);
        }
    }

    @Test
    void redirectsAreNotFollowedAndChunkedFlvStreamsClose() throws Exception {
        var fixture = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 8);
        fixture.createContext(
                "/redirect.flv",
                e -> {
                    e.getResponseHeaders().set("Location", "http://127.0.0.2/private");
                    e.sendResponseHeaders(302, -1);
                    e.close();
                });
        fixture.createContext(
                "/good.flv",
                e -> {
                    e.sendResponseHeaders(200, 0);
                    e.getResponseBody()
                            .write("FLV-test-stream".getBytes(StandardCharsets.US_ASCII));
                    e.close();
                });
        fixture.start();
        try (var relay = new HttpMediaRelay()) {
            String base = "http://127.0.0.1:" + fixture.getAddress().getPort();
            assertThat(get(relay.open("bad", URI.create(base + "/redirect.flv"))).statusCode())
                    .isEqualTo(502);
            assertThat(relay.failure("bad")).isEqualTo("HTTP_MEDIA_REDIRECT_REJECTED");
            assertThat(get(relay.open("good", URI.create(base + "/good.flv"))).body())
                    .isEqualTo("FLV-test-stream");
            relay.close("good");
            relay.close("bad");
            assertThat(relay.sessionCount()).isZero();
        } finally {
            fixture.stop(0);
        }
    }

    private HttpResponse<String> get(URI uri) throws Exception {
        return http.send(
                HttpRequest.newBuilder(uri).timeout(Duration.ofSeconds(5)).GET().build(),
                HttpResponse.BodyHandlers.ofString());
    }
}
