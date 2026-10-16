package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.stream.Collectors;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

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
    @TempDir Path directory;
    private PreviewTranscodeRegistry registry;
    private PreviewProcessInspector inspector;
    private MediaRuntimeService runtimes;
    private String ffmpegBinary = "";
    private String ffmpegCommand = "%s -i %s -c:v libx264 -f flv %s";
    private final Map<String, Map<String, String>> jobs =
            new java.util.concurrent.ConcurrentHashMap<>();
    private boolean retainJobs;
    private boolean retainMedia;
    private boolean failAddTranscode;
    private long configDelayMillis;
    private long cleanupDelayMillis;
    private java.util.concurrent.ExecutorService handlers;
    private final java.util.Set<String> closed = java.util.concurrent.ConcurrentHashMap.newKeySet();

    @BeforeEach
    void start() throws Exception {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        handlers = java.util.concurrent.Executors.newFixedThreadPool(4);
        server.setExecutor(handlers);
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
                    if ("delStreamProxy".equals(api) && cleanupDelayMillis > 0) {
                        try {
                            Thread.sleep(cleanupDelayMillis);
                        } catch (InterruptedException interrupted) {
                            Thread.currentThread().interrupt();
                        }
                    }
                    if ("getServerConfig".equals(api) && configDelayMillis > 0) {
                        try {
                            Thread.sleep(configDelayMillis);
                        } catch (InterruptedException interrupted) {
                            Thread.currentThread().interrupt();
                        }
                    }
                    if ("close_streams".equals(api) && !retainMedia)
                        closed.add(params.get("stream"));
                    if ("addFFmpegSource".equals(api) && !failAddTranscode) {
                        String key = key(params.get("dst_url"));
                        jobs.put(
                                key,
                                Map.of(
                                        "key",
                                        key,
                                        "src_url",
                                        params.get("src_url"),
                                        "dst_url",
                                        params.get("dst_url")));
                    }
                    if ("delFFmpegSource".equals(api) && !retainJobs)
                        jobs.remove(params.get("key"));
                    Object response =
                            switch (api) {
                                case "getServerConfig" ->
                                        Map.of(
                                                "code",
                                                0,
                                                "data",
                                                List.of(
                                                        Map.of(
                                                                "rtsp.directProxy",
                                                                directProxy,
                                                                "ffmpeg.bin",
                                                                ffmpegBinary,
                                                                "ffmpeg.cmd",
                                                                ffmpegCommand,
                                                                "rtsp.port",
                                                                "8554",
                                                                "rtmp.port",
                                                                "1935")));
                                case "listFFmpegSource" ->
                                        Map.of("code", 0, "data", List.copyOf(jobs.values()));
                                case "addFFmpegSource" ->
                                        failAddTranscode
                                                ? Map.of("code", -1)
                                                : Map.of(
                                                        "code",
                                                        0,
                                                        "data",
                                                        Map.of("key", key(params.get("dst_url"))));
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
                                                closed.contains(params.get("stream"))
                                                        ? List.of()
                                                        : List.of(
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
        registry = new PreviewTranscodeRegistry(directory, json);
        inspector = mock(PreviewProcessInspector.class);
        when(inspector.exited(any(), anyString(), anyString(), anyString(), any()))
                .thenReturn(true);
        runtimes = mock(MediaRuntimeService.class);
        gateway = new ZlmPreviewGateway(json, runtimes, registry, inspector);
    }

    @AfterEach
    void stop() {
        server.stop(0);
        handlers.shutdownNow();
        registry.close();
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
        assertThat(open.params).doesNotContainKey("media_timeout_ms");
        assertThat(open.params.get("secret")).isEqualTo("api-secret");
        assertThat(open.params.get("url")).isEqualTo("rtsp://user:pass@10.0.0.1/live");
    }

    @Test
    void observationPullKeepsTheFiveSecondWindowAliveWithoutChangingPreviewDefaults() {
        gateway.openObservation(
                runtime,
                APP,
                ID,
                URI.create("rtsp://10.0.0.1/live"),
                PreviewDeadline.after(Duration.ofSeconds(18)));
        var observation =
                requests.stream()
                        .filter(request -> request.api.equals("addStreamProxy"))
                        .findFirst()
                        .orElseThrow();
        assertThat(observation.params)
                .containsEntry("media_timeout_ms", "10000")
                .containsEntry("retry_count", "0")
                .containsEntry("timeout_sec", "8");
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

    @Test
    void transcodeStartsOnlyAfterReservationAndReleasesAfterConfirmedExit() throws Exception {
        configureFfmpeg();
        var output = gateway.transcode(stream(), ID + "h264");
        assertThat(registry.keys()).containsExactly(output.key());
        assertThat(jobs).containsKey(output.key());
        gateway.close(stream(), output);
        assertThat(registry.keys()).isEmpty();
        assertThat(jobs).isEmpty();
    }

    @Test
    void successfulDeleteDoesNotReleaseWhileTheOwnedProcessStillRuns() throws Exception {
        configureFfmpeg();
        var output = gateway.transcode(stream(), ID + "h264");
        when(inspector.exited(any(), anyString(), anyString(), anyString(), any()))
                .thenReturn(false);
        assertThatThrownBy(() -> gateway.close(stream(), output)).hasMessage("CLEANUP_PENDING");
        assertThat(registry.keys()).containsExactly(output.key());
        when(inspector.exited(any(), anyString(), anyString(), anyString(), any()))
                .thenReturn(true);
        gateway.close(stream(), output);
        assertThat(registry.keys()).isEmpty();
    }

    @Test
    void zlmRegistryAndMediaMustAlsoDisappearBeforeRelease() throws Exception {
        configureFfmpeg();
        var output = gateway.transcode(stream(), ID + "h264");
        retainJobs = true;
        assertThatThrownBy(() -> gateway.close(stream(), output)).hasMessage("CLEANUP_PENDING");
        assertThat(registry.keys()).hasSize(1);
        retainJobs = false;
        retainMedia = true;
        closed.clear();
        assertThatThrownBy(() -> gateway.close(stream(), output)).hasMessage("CLEANUP_PENDING");
        assertThat(registry.keys()).hasSize(1);
    }

    @Test
    void transcodesHaveNoCountQuotaAndDoNotCountForeignJobs() throws Exception {
        configureFfmpeg();
        String foreignKey = "f".repeat(32);
        jobs.put(
                foreignKey,
                Map.of(
                        "key",
                        foreignKey,
                        "src_url",
                        "rtsp://127.0.0.1/foreign/input",
                        "dst_url",
                        "rtmp://127.0.0.1/foreign/output"));
        for (int index = 1; index <= 3; index++) {
            String id = "%032x".formatted(index);
            var source =
                    new PreviewMediaGateway.Stream(
                            runtime, APP, id, "__defaultVhost__/" + APP + "/" + id);
            gateway.transcode(source, id + "h264");
        }
        assertThat(registry.keys()).hasSize(3);
        assertThat(jobs).hasSize(4).containsKey(foreignKey);
        assertThat(requests.stream().filter(r -> r.api.equals("addFFmpegSource"))).hasSize(3);
    }

    @Test
    void duplicateResourceCannotStartAnotherTranscoder() throws Exception {
        configureFfmpeg();
        gateway.transcode(stream(), ID + "h264");
        assertThatThrownBy(() -> gateway.transcode(stream(), ID + "h264"))
                .hasMessage("TRANSCODE_RECOVERY_REQUIRED");
        assertThat(requests.stream().filter(r -> r.api.equals("addFFmpegSource"))).hasSize(1);
    }

    @Test
    void slowOldCleanupUsesAShortBudgetAndDoesNotBlockANewTranscoder() throws Exception {
        configureFfmpeg();
        registerOldResource(1);
        cleanupDelayMillis = 3000;
        long started = System.nanoTime();
        var output =
                gateway.transcode(
                        stream(), ID + "h264", PreviewDeadline.after(Duration.ofSeconds(12)));
        assertThat(Duration.ofNanos(System.nanoTime() - started)).isLessThan(Duration.ofSeconds(2));
        assertThat(registry.keys()).hasSize(2).contains(output.key());
        assertThat(requests.stream().filter(r -> r.api.equals("addFFmpegSource"))).hasSize(1);
    }

    @Test
    void oldCleanupIsSkippedWhenItWouldConsumeTheNewStartBudget() throws Exception {
        configureFfmpeg();
        registerOldResource(1);
        gateway.transcode(stream(), ID + "h264", PreviewDeadline.after(Duration.ofSeconds(9)));
        assertThat(requests)
                .extracting(Request::api)
                .doesNotContain("delFFmpegSource", "delStreamProxy", "close_streams");
        assertThat(registry.keys()).hasSize(2);
    }

    @Test
    void successiveNewStartsRecoverOnlyOneOldRecordAndRotateFailures() throws Exception {
        configureFfmpeg();
        String first = registerOldResource(1);
        String second = registerOldResource(2);
        retainMedia = true;
        for (int index = 3; index <= 5; index++) {
            String id = "%032x".formatted(index);
            var source =
                    new PreviewMediaGateway.Stream(
                            runtime, APP, id, "__defaultVhost__/" + APP + "/" + id);
            gateway.transcode(source, id + "h264");
        }
        assertThat(requests.stream().filter(r -> r.api.equals("delStreamProxy")))
                .extracting(r -> r.params.get("key"))
                .containsExactly(
                        "__defaultVhost__/" + APP + "/" + first,
                        "__defaultVhost__/" + APP + "/" + second,
                        "__defaultVhost__/" + APP + "/" + first);
        assertThat(registry.keys()).hasSize(5);
    }

    @Test
    void unknownStartRetainsTheExactHandleAndReservation() throws Exception {
        configureFfmpeg();
        failAddTranscode = true;
        var failure =
                catchThrowableOfType(
                        () -> gateway.transcode(stream(), ID + "h264"), PreviewFailure.class);
        assertThat(failure.pendingTranscode()).isNotNull();
        assertThat(registry.keys()).containsExactly(failure.pendingTranscode().key());
        assertThatThrownBy(() -> gateway.close(stream(), failure.pendingTranscode()))
                .hasMessage("CLEANUP_PENDING");
        assertThat(registry.keys()).hasSize(1);
    }

    @Test
    void aForeignJobWithACorruptJournalKeyMustNotBeDeleted() throws Exception {
        configureFfmpeg();
        var output = gateway.transcode(stream(), ID + "h264");
        jobs.put(
                output.key(),
                Map.of(
                        "key",
                        output.key(),
                        "src_url",
                        "rtsp://127.0.0.1:8554/foreign/stream",
                        "dst_url",
                        "rtmp://127.0.0.1:1935/foreign/stream"));
        requests.clear();
        assertThatThrownBy(() -> gateway.close(stream(), output))
                .hasMessage("CLEANUP_DESTINATION_REJECTED");
        assertThat(requests)
                .extracting(Request::api)
                .doesNotContain("delFFmpegSource", "delStreamProxy", "close_streams");
        assertThat(registry.keys()).hasSize(1);
    }

    @Test
    void unsafeSpacePathAndZlmRejectedQuotedTemplatesNeverStartFfmpeg() throws Exception {
        configureFfmpeg();
        ffmpegBinary = directory.resolve("tools with spaces/ffmpeg.exe").toString();
        assertThatThrownBy(() -> gateway.transcode(stream(), ID + "h264"))
                .hasMessage("TRANSCODE_NOT_CONFIGURED");
        ffmpegCommand = "\"%s\" -i %s -c:v libx264 %s";
        assertThatThrownBy(() -> gateway.transcode(stream(), ID + "h264"))
                .hasMessage("TRANSCODE_NOT_CONFIGURED");
        assertThat(requests).extracting(Request::api).doesNotContain("addFFmpegSource");
        assertThat(registry.keys()).isEmpty();
    }

    @Test
    void aSharedDeadlineStopsTheChainBeforeAnyMutation() {
        configDelayMillis = 200;
        long started = System.nanoTime();
        assertThatThrownBy(
                        () ->
                                gateway.open(
                                        runtime,
                                        APP,
                                        ID,
                                        URI.create("rtsp://127.0.0.1/live"),
                                        PreviewDeadline.after(Duration.ofMillis(40))))
                .hasMessage("PREVIEW_TIMEOUT");
        assertThat(Duration.ofNanos(System.nanoTime() - started)).isLessThan(Duration.ofSeconds(2));
        assertThat(requests).extracting(Request::api).doesNotContain("addStreamProxy");
    }

    @Test
    void cancelledDeadlinePreventsNetworkCalls() {
        var deadline = PreviewDeadline.after(Duration.ofSeconds(1));
        deadline.cancel();
        assertThatThrownBy(
                        () ->
                                gateway.open(
                                        runtime,
                                        APP,
                                        ID,
                                        URI.create("rtsp://127.0.0.1/live"),
                                        deadline))
                .hasMessage("CANCELLED");
        assertThat(requests).isEmpty();
    }

    private void configureFfmpeg() throws Exception {
        Path executable = Files.createFile(directory.resolve("ffmpeg.exe"));
        ffmpegBinary = executable.toString();
        when(runtimes.activeFfmpeg())
                .thenReturn(Optional.of(new MediaRuntimeService.ActiveFfmpeg(executable, "test")));
    }

    private String registerOldResource(int index) {
        String id = "%032x".formatted(index);
        var source =
                new PreviewMediaGateway.Stream(
                        runtime, APP, id, "__defaultVhost__/" + APP + "/" + id);
        var output =
                new PreviewMediaGateway.Transcode(
                        id + "h264", key("rtmp://127.0.0.1:1935/" + APP + "/" + id + "h264"));
        registry.register(source, output);
        registry.started(output.key());
        registry.releasing(output.key());
        return id;
    }

    private static String key(String destination) {
        try {
            return java.util.HexFormat.of()
                    .formatHex(
                            java.security.MessageDigest.getInstance("MD5")
                                    .digest(destination.getBytes(StandardCharsets.UTF_8)));
        } catch (java.security.NoSuchAlgorithmException unavailable) {
            throw new IllegalStateException(unavailable);
        }
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
