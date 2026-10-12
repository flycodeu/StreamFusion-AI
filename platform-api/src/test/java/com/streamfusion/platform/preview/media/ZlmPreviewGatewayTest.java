package com.streamfusion.platform.preview.media;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.mock;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.streamfusion.platform.preview.PreviewFailure;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.stream.Collectors;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class ZlmPreviewGatewayTest {
    private static final String APP = "sf_preview_0123456789ab";
    private static final String ID = "0123456789abcdef0123456789abcdef";
    private final ObjectMapper json = new ObjectMapper();
    private final List<Request> requests = new CopyOnWriteArrayList<>();
    private HttpServer server;
    private MediaRuntimeService.ActiveZlm runtime;
    private ZlmPreviewGateway gateway;
    private boolean failDelete;
    private String directProxy = "0";

    @BeforeEach
    void start() throws Exception {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/index/api/",
                exchange -> {
                    String api =
                            exchange.getRequestURI().getPath().substring("/index/api/".length());
                    String body =
                            new String(
                                    exchange.getRequestBody().readAllBytes(),
                                    StandardCharsets.UTF_8);
                    var params = "webrtc".equals(api) ? Map.<String, String>of() : form(body);
                    requests.add(
                            new Request(api, exchange.getRequestURI().getRawQuery(), body, params));
                    Object response =
                            switch (api) {
                                case "getServerConfig" ->
                                        Map.of(
                                                "code",
                                                0,
                                                "data",
                                                List.of(Map.of("rtsp.directProxy", directProxy)));
                                case "addStreamProxy" ->
                                        Map.of(
                                                "code",
                                                0,
                                                "data",
                                                Map.of(
                                                        "key",
                                                        "__defaultVhost__/"
                                                                + params.get("app")
                                                                + "/"
                                                                + params.get("stream")));
                                case "getMediaList" ->
                                        Map.of(
                                                "code",
                                                0,
                                                "data",
                                                List.of(
                                                        Map.of(
                                                                "bytesSpeed",
                                                                1000,
                                                                "totalReaderCount",
                                                                2,
                                                                "tracks",
                                                                List.of(
                                                                        Map.of(
                                                                                "codec_type",
                                                                                0,
                                                                                "ready",
                                                                                true,
                                                                                "codec_id_name",
                                                                                "H265",
                                                                                "width",
                                                                                1920,
                                                                                "height",
                                                                                1080,
                                                                                "fps",
                                                                                25)))));
                                case "webrtc" ->
                                        Map.of("code", 0, "type", "answer", "sdp", "v=0\r\n");
                                case "delStreamProxy" ->
                                        failDelete
                                                ? Map.of(
                                                        "code",
                                                        -1,
                                                        "msg",
                                                        "upstream secret should stay private")
                                                : Map.of("code", 0);
                                default -> Map.of("code", 0);
                            };
                    byte[] bytes = json.writeValueAsBytes(response);
                    exchange.getResponseHeaders().set("Content-Type", "application/json");
                    exchange.sendResponseHeaders(200, bytes.length);
                    exchange.getResponseBody().write(bytes);
                    exchange.close();
                });
        server.start();
        runtime =
                new MediaRuntimeService.ActiveZlm(
                        URI.create("http://127.0.0.1:" + server.getAddress().getPort()),
                        "api-secret",
                        Path.of("D:/ZLM/MediaServer.exe"),
                        Path.of("D:/ZLM/config.ini"),
                        "1");
        gateway = new ZlmPreviewGateway(json, mock(MediaRuntimeService.class));
    }

    @AfterEach
    void stop() {
        server.stop(0);
    }

    @Test
    void proxyUsesFiniteRetryTimeoutAndCredentialsOnlyInPostBody() {
        gateway.open(runtime, APP, ID, URI.create("rtsp://user:pass@10.0.0.1/live"));
        var open =
                requests.stream()
                        .filter(r -> r.api.equals("addStreamProxy"))
                        .findFirst()
                        .orElseThrow();
        assertThat(open.query).isNull();
        assertThat(open.params)
                .containsEntry("force", "0")
                .containsEntry("retry_count", "0")
                .containsEntry("timeout_sec", "8")
                .containsEntry("rtp_type", "0")
                .containsEntry("auto_close", "1");
        assertThat(open.params.get("secret")).isEqualTo("api-secret");
        assertThat(open.params.get("url")).isEqualTo("rtsp://user:pass@10.0.0.1/live");
    }

    @Test
    void directProxyMustBeDisabledBeforeRtcPreview() {
        directProxy = "1";
        assertThatThrownBy(() -> gateway.open(runtime, APP, ID, URI.create("rtsp://10.0.0.1/live")))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("ZLM_RTC_NOT_CONFIGURED");
        assertThat(requests).extracting(Request::api).doesNotContain("addStreamProxy");
    }

    @Test
    void statsContainOnlySafeActualTrackAndMediaRtpRate() {
        var value = gateway.stats(stream(), ID);
        assertThat(value.videoCodec()).isEqualTo("H265");
        assertThat(value.mediaKbps()).isEqualTo(8.0);
        assertThat(value.readers()).isEqualTo(2);
        assertThat(value.width()).isEqualTo(1920);
    }

    @Test
    void deletionFailureStillClosesOnlyOwnedTuple() {
        failDelete = true;
        assertThatThrownBy(() -> gateway.close(stream(), null)).hasMessage("CLEANUP_PENDING");
        assertThat(requests).extracting(Request::api).contains("delStreamProxy", "close_streams");
        var close =
                requests.stream()
                        .filter(r -> r.api.equals("close_streams"))
                        .findFirst()
                        .orElseThrow();
        assertThat(close.params)
                .containsEntry("app", APP)
                .containsEntry("stream", ID)
                .containsEntry("force", "1");
    }

    @Test
    void foreignNamespaceCannotBeDeleted() {
        var foreign =
                new PreviewMediaGateway.Stream(
                        runtime, "foreign", ID, "__defaultVhost__/foreign/" + ID);
        assertThatThrownBy(() -> gateway.close(foreign, null))
                .hasMessage("CLEANUP_DESTINATION_REJECTED");
        assertThat(requests).isEmpty();
    }

    @Test
    void negotiationReturnsSdpAndNeverSecretOrUpstreamLocator() {
        var answer = gateway.answer(stream(), ID, "v=0\r\n");
        assertThat(answer.type()).isEqualTo("answer");
        assertThat(answer.sdp()).isEqualTo("v=0\r\n");
        var signal = requests.getFirst();
        assertThat(signal.query).contains("type=play").doesNotContain("secret", "10.0.0.1");
        assertThat(signal.body).isEqualTo("v=0\r\n");
    }

    @Test
    void codecsHaveStableNames() {
        assertThat(ZlmPreviewGateway.normalizeCodec("H.264")).isEqualTo("H264");
        assertThat(ZlmPreviewGateway.normalizeCodec("hevc")).isEqualTo("H265");
    }

    private PreviewMediaGateway.Stream stream() {
        return new PreviewMediaGateway.Stream(
                runtime, APP, ID, "__defaultVhost__/" + APP + "/" + ID);
    }

    private static Map<String, String> form(String value) {
        return java.util.Arrays.stream(value.split("&"))
                .map(item -> item.split("=", 2))
                .collect(
                        Collectors.toMap(
                                item -> URLDecoder.decode(item[0], StandardCharsets.UTF_8),
                                item ->
                                        URLDecoder.decode(
                                                item.length == 1 ? "" : item[1],
                                                StandardCharsets.UTF_8)));
    }

    private record Request(String api, String query, String body, Map<String, String> params) {}
}
