package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.mock;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ZlmMediaObservationTest {
    private static final String APP = "sf_preview_0123456789ab";
    private static final String ID = "0123456789abcdef0123456789abcdef";
    @TempDir Path directory;
    private final ObjectMapper json = new ObjectMapper();
    private HttpServer server;
    private ZlmPreviewGateway gateway;
    private PreviewMediaGateway.Stream stream;
    private Map<String, Object> track;
    private long repulls;
    private boolean duplicateVideo;
    private boolean wrongTuple;
    private PreviewTranscodeRegistry registry;

    @BeforeEach
    void start() throws Exception {
        track =
                new LinkedHashMap<>(
                        Map.of(
                                "codec_type",
                                0,
                                "ready",
                                true,
                                "codec_id",
                                0,
                                "codec_id_name",
                                "H264",
                                "width",
                                1280,
                                "height",
                                720,
                                "frames",
                                450L));
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/index/api/",
                exchange -> {
                    exchange.getRequestBody().readAllBytes();
                    Map<String, Object> tuple =
                            Map.of(
                                    "vhost",
                                    "__defaultVhost__",
                                    "app",
                                    APP,
                                    "stream",
                                    wrongTuple ? "other" : ID);
                    Object data =
                            exchange.getRequestURI().getPath().endsWith("getProxyInfo")
                                    ? Map.of(
                                            "status",
                                            0,
                                            "rePullCount",
                                            repulls,
                                            "src",
                                            tuple,
                                            "tracks",
                                            duplicateVideo ? List.of(track, track) : List.of(track))
                                    : List.of(
                                            Map.of(
                                                    "schema",
                                                    "rtsp",
                                                    "app",
                                                    APP,
                                                    "stream",
                                                    ID,
                                                    "vhost",
                                                    "__defaultVhost__",
                                                    "createStamp",
                                                    1720000000L));
                    byte[] body = json.writeValueAsBytes(Map.of("code", 0, "data", data));
                    exchange.sendResponseHeaders(200, body.length);
                    exchange.getResponseBody().write(body);
                    exchange.close();
                });
        server.start();
        var runtime =
                new MediaRuntimeService.ActiveZlm(
                        URI.create("http://127.0.0.1:" + server.getAddress().getPort()),
                        "api-secret",
                        directory.resolve("server"),
                        directory.resolve("config"),
                        "test");
        stream =
                new PreviewMediaGateway.Stream(
                        runtime, APP, ID, "__defaultVhost__/" + APP + "/" + ID);
        registry = new PreviewTranscodeRegistry(directory, json);
        gateway =
                new ZlmPreviewGateway(
                        json,
                        mock(MediaRuntimeService.class),
                        registry,
                        mock(PreviewProcessInspector.class));
    }

    @AfterEach
    void stop() {
        server.stop(0);
        registry.close();
    }

    @Test
    void rawObservationRequiresAnOwnedNonRepullingInstanceAndCumulativeFrames() {
        var value = gateway.observeRawVideo(stream, deadline());
        assertThat(value.frames()).isEqualTo(450);
        assertThat(value.instanceId()).contains(ID).contains("1720000000");
        assertThat(value.trackIdentity()).contains("H264").contains("1280x720");
        assertThat(value.toString()).doesNotContain("api-secret");
    }

    @Test
    void absentFramesOrMultipleTracksAreEvidenceInsufficient() {
        track.remove("frames");
        assertThatThrownBy(() -> gateway.observeRawVideo(stream, deadline()))
                .hasMessage("MEDIA_EVIDENCE_INSUFFICIENT");
        track.put("frames", 1L);
        duplicateVideo = true;
        assertThatThrownBy(() -> gateway.observeRawVideo(stream, deadline()))
                .hasMessage("MEDIA_EVIDENCE_INSUFFICIENT");
    }

    @Test
    void repulledOrForeignTupleIsNotCameraOffline() {
        repulls = 1;
        assertThatThrownBy(() -> gateway.observeRawVideo(stream, deadline()))
                .hasMessage("MEDIA_EVIDENCE_INSUFFICIENT");
        repulls = 0;
        wrongTuple = true;
        assertThatThrownBy(() -> gateway.observeRawVideo(stream, deadline()))
                .hasMessage("MEDIA_EVIDENCE_INSUFFICIENT");
    }

    @Test
    void counterMustBeNonNegativeAndNumeric() {
        track.put("frames", "450");
        assertThatThrownBy(() -> gateway.observeRawVideo(stream, deadline()))
                .hasMessage("MEDIA_EVIDENCE_INSUFFICIENT");
        track.put("frames", -1);
        assertThatThrownBy(() -> gateway.observeRawVideo(stream, deadline()))
                .hasMessage("MEDIA_EVIDENCE_INSUFFICIENT");
    }

    private PreviewDeadline deadline() {
        return PreviewDeadline.after(Duration.ofSeconds(2));
    }
}
