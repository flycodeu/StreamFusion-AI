package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.spy;
import static org.mockito.Mockito.when;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.status.CameraMediaProbe;
import com.streamfusion.platform.camera.status.CameraMediaPullRegistry;
import com.streamfusion.platform.camera.status.CameraProbeResult;
import com.streamfusion.platform.camera.status.RtspDescribeProbe;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.source.PreviewLocatorService;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveFfmpeg;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.BooleanSupplier;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.junit.jupiter.api.io.TempDir;

/**
 * Explicit opt-in: isolated local ZLM and generated video, never business services or real cameras.
 */
@EnabledIfEnvironmentVariable(named = "SF_PREVIEW_REAL_MEDIA_TEST", matches = "true")
class ZlmPreviewRealMediaTest {
    private final ObjectMapper json = new ObjectMapper();
    private final HttpClient http =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(1)).build();
    private final Map<String, Object> evidence = new LinkedHashMap<>();
    private final String secret = UUID.randomUUID().toString();

    @TempDir(cleanup = org.junit.jupiter.api.io.CleanupMode.ON_SUCCESS)
    Path directory;

    private ActiveZlm runtime;

    @Test
    void actualMediaProbeObservesReceivingPausedSourceRecoveryAndOwnedCleanup() throws Exception {
        Path zlm = Path.of(required("SF_PREVIEW_ZLM_BIN")).toAbsolutePath();
        Path ffmpeg = Path.of(required("SF_PREVIEW_FFMPEG_BIN")).toAbsolutePath();
        assertThat(Files.isRegularFile(zlm) && Files.isRegularFile(ffmpeg)).isTrue();
        var ports = new HashSet<Integer>();
        int apiPort = unusedPort(ports),
                rtsp = unusedPort(ports),
                rtmp = unusedPort(ports),
                rtc = unusedPort(ports);
        Path config = directory.resolve("media-config.ini");
        Files.writeString(config, config(ffmpeg, apiPort, rtsp, rtmp, rtc));
        runtime =
                new ActiveZlm(
                        URI.create("http://127.0.0.1:" + apiPort),
                        secret,
                        zlm,
                        config,
                        "isolated-media");
        var runtimes = mock(MediaRuntimeService.class);
        when(runtimes.activeZlm()).thenReturn(Optional.of(runtime));
        var transcodes = new PreviewTranscodeRegistry(directory.resolve("transcodes"), json);
        var gateway =
                spy(
                        new ZlmPreviewGateway(
                                json, runtimes, transcodes, new PreviewProcessInspector()));
        var locator = mock(PreviewLocatorService.class);
        var profile = new CameraProfileEntity();
        profile.setId(901L);
        profile.setSourceId(900L);
        String sourceApp =
                "sf_media_" + UUID.randomUUID().toString().replace("-", "").substring(0, 12);
        when(locator.resolve(
                        org.mockito.ArgumentMatchers.eq(profile),
                        org.mockito.ArgumentMatchers.any(),
                        org.mockito.ArgumentMatchers.any()))
                .thenReturn(
                        new PreviewLocatorService.Resolved(
                                URI.create(
                                        "rtsp://127.0.0.1:" + rtsp + "/" + sourceApp + "/source")));
        Process server = null, publisher = null;
        Thread writer = null;
        var publishing = new AtomicBoolean(true);
        var stopWriter = new AtomicBoolean(false);
        var pauseOnSample = new AtomicBoolean(false);
        Object sourceLock = new Object();
        var registryDir = directory.resolve("media-registry");
        var registry =
                new CameraMediaPullRegistry(
                        new PlatformProperties(registryDir, Duration.ofSeconds(15)), json);
        CameraMediaProbe probe = null;
        try {
            server =
                    new ProcessBuilder(
                                    zlm.toString(),
                                    "-c",
                                    config.toString(),
                                    "-t",
                                    "2",
                                    "--affinity",
                                    "0",
                                    "-l",
                                    "3",
                                    "--log-dir",
                                    directory.resolve("media-logs").toString())
                            .directory(directory.toFile())
                            .redirectErrorStream(true)
                            .redirectOutput(directory.resolve("media-zlm.log").toFile())
                            .start();
            Process ownedServer = server;
            await(() -> ownedServer.isAlive() && available(), Duration.ofSeconds(12));
            publisher =
                    new ProcessBuilder(
                                    ffmpeg.toString(),
                                    "-hide_banner",
                                    "-loglevel",
                                    "warning",
                                    "-f",
                                    "rawvideo",
                                    "-pixel_format",
                                    "yuv420p",
                                    "-video_size",
                                    "320x180",
                                    "-framerate",
                                    "8",
                                    "-i",
                                    "pipe:0",
                                    "-an",
                                    "-c:v",
                                    "libx264",
                                    "-preset",
                                    "ultrafast",
                                    "-tune",
                                    "zerolatency",
                                    "-threads",
                                    "1",
                                    "-g",
                                    "8",
                                    "-f",
                                    "rtsp",
                                    "-rtsp_transport",
                                    "tcp",
                                    "rtsp://127.0.0.1:" + rtsp + "/" + sourceApp + "/source")
                            .redirectErrorStream(true)
                            .redirectOutput(directory.resolve("pipe-publisher.log").toFile())
                            .start();
            Process ownedPublisher = publisher;
            byte[] frame = new byte[320 * 180 * 3 / 2];
            java.util.Arrays.fill(frame, 0, 320 * 180, (byte) 80);
            java.util.Arrays.fill(frame, 320 * 180, frame.length, (byte) 128);
            writer =
                    new Thread(
                            () -> {
                                try (var input = ownedPublisher.getOutputStream()) {
                                    while (!stopWriter.get() && ownedPublisher.isAlive()) {
                                        synchronized (sourceLock) {
                                            if (publishing.get()) {
                                                input.write(frame);
                                                input.flush();
                                            }
                                        }
                                        Thread.sleep(125);
                                    }
                                } catch (java.io.IOException | InterruptedException stopped) {
                                    Thread.currentThread().interrupt();
                                }
                            },
                            "isolated-media-publisher");
            writer.setDaemon(true);
            writer.start();
            await(() -> frames(sourceApp, "source") >= 4, Duration.ofSeconds(10));
            doAnswer(
                            invocation -> {
                                Object observed = invocation.callRealMethod();
                                if (pauseOnSample.compareAndSet(true, false)) {
                                    synchronized (sourceLock) {
                                        publishing.set(false);
                                    }
                                    Thread.sleep(1000);
                                    return invocation.callRealMethod();
                                }
                                return observed;
                            })
                    .when(gateway)
                    .observeRawVideo(
                            org.mockito.ArgumentMatchers.any(), org.mockito.ArgumentMatchers.any());
            probe = new CameraMediaProbe(runtimes, locator, gateway, registry);
            for (int attempt = 0; attempt < 3; attempt++) {
                if (attempt == 1) pauseOnSample.set(true);
                else publishing.set(true);
                var result =
                        probe.execute(
                                1001L + attempt,
                                "media-real-" + attempt,
                                profile,
                                () -> {},
                                System.nanoTime() + TimeUnit.SECONDS.toNanos(18),
                                new RtspDescribeProbe.Cancellation());
                evidence.put(
                        "attempt" + attempt,
                        Map.of("outcome", result.outcome().name(), "reason", result.reasonCode()));
                assertThat(result.outcome())
                        .as("attempt %s, reason %s", attempt, result.reasonCode())
                        .isEqualTo(
                                attempt == 1
                                        ? CameraProbeResult.Outcome.FAILED
                                        : CameraProbeResult.Outcome.ACCESSIBLE);
                assertThat(result.reasonCode())
                        .isEqualTo(attempt == 1 ? "MEDIA_NO_FRAMES" : "MEDIA_RECEIVING");
                assertThat(result.evidence()).isNotNull();
                assertThat(result.evidence().sampleCount()).isGreaterThanOrEqualTo(3);
                if (attempt == 1)
                    assertThat(result.evidence().lastFrames())
                            .isEqualTo(result.evidence().firstFrames());
                else
                    assertThat(result.evidence().lastFrames())
                            .isGreaterThan(result.evidence().firstFrames());
                await(
                        () -> {
                            try {
                                return "[]"
                                        .equals(
                                                Files.readString(
                                                        registryDir.resolve(
                                                                "camera-media-pull.json")));
                            } catch (java.io.IOException unavailable) {
                                return false;
                            }
                        },
                        Duration.ofSeconds(10));
                assertThat(api("getMediaList", Map.of()).path("data").findValuesAsText("app"))
                        .allMatch(sourceApp::equals);
                evidence.put(
                        attempt == 0
                                ? "receiving"
                                : attempt == 1 ? "pausedSource" : "recoveredSource",
                        Map.of(
                                "outcome",
                                result.outcome().name(),
                                "reason",
                                result.reasonCode(),
                                "firstFrames",
                                result.evidence().firstFrames(),
                                "lastFrames",
                                result.evidence().lastFrames(),
                                "sampleCount",
                                result.evidence().sampleCount(),
                                "ownedPullGone",
                                true,
                                "registryEmpty",
                                true));
            }
            assertThat(transcodes.keys()).isEmpty();
            evidence.put("transcoderNotUsed", true);
            evidence.put("status", "PASS");
        } finally {
            if (probe != null) probe.close();
            registry.close();
            transcodes.close();
            stopWriter.set(true);
            if (writer != null) {
                writer.interrupt();
                writer.join(1500);
            }
            stop(publisher);
            stop(server);
            evidence.put("isolatedZlmStopped", server == null || !server.isAlive());
            evidence.put("publisherStopped", publisher == null || !publisher.isAlive());
            String destination = System.getenv("SF_PREVIEW_REAL_EVIDENCE_DIR");
            if (destination != null) {
                Files.createDirectories(Path.of(destination));
                Files.writeString(
                        Path.of(destination).resolve("camera-media-real-result.json"),
                        json.writerWithDefaultPrettyPrinter().writeValueAsString(evidence));
            }
        }
    }

    @Test
    void generatedH264AndH265UseRealGatewayRegistryAndConfirmedProcessCleanup() throws Exception {
        Path zlm = Path.of(required("SF_PREVIEW_ZLM_BIN")).toAbsolutePath();
        Path installedFfmpeg = Path.of(required("SF_PREVIEW_FFMPEG_BIN")).toAbsolutePath();
        assertThat(Files.isRegularFile(zlm) && Files.isRegularFile(installedFfmpeg)).isTrue();
        // This ZLM version requires an unquoted first %s placeholder. The isolated copy has no
        // spaces.
        Path ffmpeg =
                directory.resolve(
                        System.getProperty("os.name").startsWith("Windows")
                                ? "ffmpeg.exe"
                                : "ffmpeg");
        try {
            Files.createLink(ffmpeg, installedFfmpeg);
        } catch (java.io.IOException unsupported) {
            Files.copy(installedFfmpeg, ffmpeg);
        }
        assertThat(ffmpeg.toString().chars().noneMatch(Character::isWhitespace)).isTrue();
        var ports = new HashSet<Integer>();
        int api = unusedPort(ports),
                rtsp = unusedPort(ports),
                rtmp = unusedPort(ports),
                rtc = unusedPort(ports);
        Path config = directory.resolve("config.ini");
        Files.writeString(config, config(ffmpeg, api, rtsp, rtmp, rtc));
        runtime =
                new ActiveZlm(
                        URI.create("http://127.0.0.1:" + api), secret, zlm, config, "isolated");
        var service = mock(MediaRuntimeService.class);
        when(service.activeFfmpeg()).thenReturn(Optional.of(new ActiveFfmpeg(ffmpeg, "isolated")));
        var registry = new PreviewTranscodeRegistry(directory.resolve("registry"), json);
        var inspector = new PreviewProcessInspector();
        var gateway = new ZlmPreviewGateway(json, service, registry, inspector);
        Process server = null;
        try {
            server =
                    new ProcessBuilder(
                                    zlm.toString(),
                                    "-c",
                                    config.toString(),
                                    "-t",
                                    "2",
                                    "--affinity",
                                    "0",
                                    "-l",
                                    "3",
                                    "--log-dir",
                                    directory.resolve("logs").toString())
                            .directory(directory.toFile())
                            .redirectErrorStream(true)
                            .redirectOutput(directory.resolve("zlm.log").toFile())
                            .start();
            Process ownedServer = server;
            await(() -> ownedServer.isAlive() && available(), Duration.ofSeconds(12));
            evidence.put("zlmStarted", true);
            JsonNode version = api("version", Map.of()).path("data");
            evidence.put("zlmCommit", version.path("commitHash").asText());
            evidence.put("zlmBuildTime", version.path("buildTime").asText());
            evidence.put(
                    "zlmContinuePushMs",
                    api("getServerConfig", Map.of())
                            .path("data")
                            .get(0)
                            .path("protocol.continue_push_ms")
                            .asInt());
            for (String codec : List.of("H264", "H265")) {
                String app =
                        "sf_preview_"
                                + UUID.randomUUID().toString().replace("-", "").substring(0, 12);
                String input = UUID.randomUUID().toString().replace("-", "");
                String sourceApp = "sf_stability_" + app.substring("sf_preview_".length());
                Process publisher = null;
                PreviewMediaGateway.Stream stream = null;
                PreviewMediaGateway.Transcode transcode = null;
                var result = new LinkedHashMap<String, Object>();
                boolean bodyFailed = false;
                try {
                    ZlmPreviewGateway activeGateway = gateway;
                    publisher = publisher(ffmpeg, rtsp, sourceApp, codec);
                    await(() -> frames(sourceApp, "source") > 0, Duration.ofSeconds(10));
                    stream =
                            gateway.open(
                                    runtime,
                                    app,
                                    input,
                                    URI.create(
                                            "rtsp://127.0.0.1:"
                                                    + rtsp
                                                    + "/"
                                                    + sourceApp
                                                    + "/source"),
                                    PreviewDeadline.after(Duration.ofSeconds(18)));
                    PreviewMediaGateway.Stream owned = stream;
                    await(
                            () -> codec.equals(activeGateway.stats(owned, input).videoCodec()),
                            Duration.ofSeconds(5));
                    long before = frames(app, input);
                    await(() -> frames(app, input) > before + 3, Duration.ofSeconds(5));
                    result.put("sourceCodec", codec);
                    result.put("sourceFramesIncreasing", true);
                    if ("H265".equals(codec)) {
                        transcode =
                                gateway.transcode(
                                        stream,
                                        input + "h264",
                                        PreviewDeadline.after(Duration.ofSeconds(18)));
                        assertThat(registry.keys()).containsExactly(transcode.key());
                        assertThat(
                                        inspector.exited(
                                                runtime,
                                                app,
                                                input,
                                                transcode.id(),
                                                PreviewDeadline.after(Duration.ofSeconds(4))))
                                .isFalse();
                        await(
                                () ->
                                        "H264"
                                                .equals(
                                                        activeGateway
                                                                .stats(owned, input + "h264")
                                                                .videoCodec()),
                                Duration.ofSeconds(5));
                        long convertedBefore = frames(app, input + "h264");
                        await(
                                () -> frames(app, input + "h264") > convertedBefore + 3,
                                Duration.ofSeconds(5));
                        result.put("outputCodec", "H264");
                        result.put("outputFramesIncreasing", true);
                        result.put("processObserved", true);
                        result.put("resourceRegistered", true);
                        // A coordinator restart retains ownership; only the recorded tuple is
                        // reconciled.
                        registry.close();
                        registry =
                                new PreviewTranscodeRegistry(directory.resolve("registry"), json);
                        assertThat(registry.keys()).containsExactly(transcode.key());
                        assertThat(registry.recoverable(runtime)).hasSize(1);
                        gateway = new ZlmPreviewGateway(json, service, registry, inspector);
                        result.put("restartRetainsRegistration", true);
                    }
                } catch (PreviewFailure failure) {
                    bodyFailed = true;
                    result.put("failureReason", failure.reason());
                    if (failure.pendingTranscode() != null) transcode = failure.pendingTranscode();
                    throw failure;
                } catch (Exception | AssertionError failure) {
                    bodyFailed = true;
                    result.put("primaryFailure", failure.getClass().getSimpleName());
                    throw failure;
                } finally {
                    PreviewTranscodeRegistry activeRegistry = registry;
                    try {
                        if (stream != null) {
                            PreviewMediaGateway.Stream owned = stream;
                            PreviewMediaGateway.Transcode output = transcode;
                            ZlmPreviewGateway owner = gateway;
                            await(
                                    () -> {
                                        try {
                                            owner.close(owned, output);
                                            return true;
                                        } catch (PreviewFailure pending) {
                                            result.put("cleanupPendingObserved", true);
                                            result.put("pendingReason", pending.reason());
                                            if (output != null) {
                                                assertThat(activeRegistry.keys())
                                                        .contains(output.key());
                                                result.put(
                                                        "pendingRegistryCount",
                                                        activeRegistry.keys().size());
                                            }
                                            try {
                                                var remaining =
                                                        api(
                                                                        "getMediaList",
                                                                        Map.of(
                                                                                "vhost",
                                                                                "__defaultVhost__",
                                                                                "app",
                                                                                app))
                                                                .path("data");
                                                var labels = new java.util.ArrayList<String>();
                                                for (var media : remaining) {
                                                    String id = media.path("stream").asText();
                                                    String schema = media.path("schema").asText();
                                                    labels.add(
                                                            (schema.matches("[a-z0-9_]{1,24}")
                                                                            ? schema
                                                                            : "unknown")
                                                                    + ":"
                                                                    + (input.equals(id)
                                                                            ? "input"
                                                                            : (input + "h264")
                                                                                            .equals(
                                                                                                    id)
                                                                                    ? "output"
                                                                                    : "other"));
                                                }
                                                result.put("pendingMediaKinds", labels);
                                            } catch (Exception unavailable) {
                                                result.put("pendingMediaQueryUnavailable", true);
                                            }
                                            return false;
                                        }
                                    },
                                    // ZLM keeps RTMP media registered for 15s after a non-graceful
                                    // FFmpeg exit. The resource must remain registered until that
                                    // expires.
                                    Duration.ofSeconds(30));
                            assertThat(
                                            api(
                                                            "getMediaList",
                                                            Map.of(
                                                                    "vhost",
                                                                    "__defaultVhost__",
                                                                    "app",
                                                                    app))
                                                    .path("data")
                                                    .size())
                                    .isZero();
                            if (transcode != null) {
                                assertThat(registry.keys()).isEmpty();
                                assertThat(
                                                inspector.exited(
                                                        runtime,
                                                        app,
                                                        input,
                                                        transcode.id(),
                                                        PreviewDeadline.after(
                                                                Duration.ofSeconds(4))))
                                        .isTrue();
                            }
                            result.put("ownedMediaGone", true);
                            result.put("ownedTranscoderGone", true);
                            result.put("registryEmpty", registry.keys().isEmpty());
                        }
                    } catch (Exception | AssertionError cleanup) {
                        result.put("cleanupFailed", true);
                        if (!bodyFailed) throw cleanup;
                    } finally {
                        try {
                            api(
                                    "close_streams",
                                    Map.of(
                                            "vhost",
                                            "__defaultVhost__",
                                            "app",
                                            sourceApp,
                                            "stream",
                                            "source",
                                            "force",
                                            "1"));
                        } catch (Exception | AssertionError cleanup) {
                            result.put("publisherMediaCleanupFailed", true);
                        } finally {
                            stop(publisher);
                        }
                        evidence.put(codec, result);
                    }
                }
            }
            evidence.put("status", "PASS");
        } finally {
            registry.close();
            stopOwnedChildren(server, ffmpeg);
            stop(server);
            evidence.put("isolatedZlmStopped", server == null || !server.isAlive());
            String destination = System.getenv("SF_PREVIEW_REAL_EVIDENCE_DIR");
            if (destination != null) {
                Path output = Path.of(destination);
                Files.createDirectories(output);
                Files.writeString(
                        output.resolve("real-media-result.json"),
                        json.writerWithDefaultPrettyPrinter().writeValueAsString(evidence));
            }
        }
    }

    private Process publisher(Path ffmpeg, int port, String app, String codec) throws Exception {
        var arguments =
                new java.util.ArrayList<>(
                        List.of(
                                ffmpeg.toString(),
                                "-hide_banner",
                                "-loglevel",
                                "warning",
                                "-nostdin",
                                "-re",
                                "-f",
                                "lavfi",
                                "-i",
                                "testsrc2=size=320x180:rate=8",
                                "-an"));
        if ("H265".equals(codec))
            arguments.addAll(
                    List.of(
                            "-c:v",
                            "libx265",
                            "-preset",
                            "ultrafast",
                            "-x265-params",
                            "pools=1:frame-threads=1:log-level=error:repeat-headers=1:keyint=8"));
        else
            arguments.addAll(
                    List.of(
                            "-c:v",
                            "libx264",
                            "-preset",
                            "ultrafast",
                            "-tune",
                            "zerolatency",
                            "-threads",
                            "1",
                            "-g",
                            "8"));
        arguments.addAll(
                List.of(
                        "-b:v",
                        "180k",
                        "-f",
                        "rtsp",
                        "-rtsp_transport",
                        "tcp",
                        "rtsp://127.0.0.1:" + port + "/" + app + "/source"));
        return new ProcessBuilder(arguments)
                .redirectErrorStream(true)
                .redirectOutput(directory.resolve(codec + "-publisher.log").toFile())
                .start();
    }

    private String config(Path ffmpeg, int api, int rtsp, int rtmp, int rtc) {
        return """
                [api]
                apiDebug=0
                secret=%s
                [general]
                mediaServerId=preview-stability-isolated
                listen_ip=127.0.0.1
                [hook]
                enable=0
                [http]
                port=%d
                sslport=0
                allow_ip_range=127.0.0.1
                [rtsp]
                port=%d
                sslport=0
                directProxy=0
                [rtmp]
                port=%d
                sslport=0
                [rtc]
                port=%d
                tcpPort=%d
                externIP=127.0.0.1
                [shell]
                port=0
                [srt]
                port=0
                [rtp_proxy]
                port=0
                [protocol]
                enable_hls=0
                enable_mp4=0
                enable_rtsp=1
                enable_rtmp=1
                enable_audio=0
                [ffmpeg]
                bin=%s
                cmd=%%s -nostdin -rtsp_transport tcp -i %%s -an -c:v libx264 -preset ultrafast -tune zerolatency -threads 1 -r 8 -g 8 -b:v 180k -f flv %%s
                log=%s
                restart_sec=0
                """
                .formatted(
                        secret,
                        api,
                        rtsp,
                        rtmp,
                        rtc,
                        rtc,
                        ffmpeg.toString().replace('\\', '/'),
                        directory.resolve("ffmpeg.log").toString().replace('\\', '/'));
    }

    private boolean available() {
        try {
            return api("version", Map.of()).path("code").asInt(-1) == 0;
        } catch (Exception unavailable) {
            return false;
        }
    }

    private long frames(String app, String stream) {
        try {
            var data =
                    api(
                                    "getMediaList",
                                    Map.of(
                                            "schema",
                                            "rtsp",
                                            "vhost",
                                            "__defaultVhost__",
                                            "app",
                                            app,
                                            "stream",
                                            stream))
                            .path("data");
            if (!data.isArray() || data.isEmpty()) return 0;
            for (var track : data.get(0).path("tracks"))
                if (track.path("codec_type").asInt(-1) == 0) return track.path("frames").asLong();
            return 0;
        } catch (Exception unavailable) {
            return 0;
        }
    }

    private JsonNode api(String name, Map<String, String> values) throws Exception {
        var parameters = new LinkedHashMap<>(values);
        parameters.put("secret", secret);
        String body =
                parameters.entrySet().stream()
                        .map(
                                e ->
                                        URLEncoder.encode(e.getKey(), StandardCharsets.UTF_8)
                                                + "="
                                                + URLEncoder.encode(
                                                        e.getValue(), StandardCharsets.UTF_8))
                        .collect(java.util.stream.Collectors.joining("&"));
        var request =
                HttpRequest.newBuilder(runtime.apiBase().resolve("/index/api/" + name))
                        .timeout(Duration.ofSeconds(2))
                        .header("Content-Type", "application/x-www-form-urlencoded")
                        .POST(HttpRequest.BodyPublishers.ofString(body))
                        .build();
        var response = http.send(request, HttpResponse.BodyHandlers.ofString());
        assertThat(response.statusCode()).isEqualTo(200);
        JsonNode value = json.readTree(response.body());
        if (value.path("code").asInt(-1) != 0) {
            String message =
                    value.path("msg")
                            .asText()
                            .replace(secret, "<redacted>")
                            .replaceAll("(?:rtsp|rtmp|https?)://[^\\s]+", "<local-test-url>");
            throw new IllegalStateException(
                    "Isolated ZLM "
                            + name
                            + " code="
                            + value.path("code").asInt()
                            + " message="
                            + message.substring(0, Math.min(240, message.length())));
        }
        return value;
    }

    private static int unusedPort(HashSet<Integer> chosen) throws Exception {
        while (true)
            try (var socket = new ServerSocket(0, 0, InetAddress.getLoopbackAddress())) {
                int port = socket.getLocalPort();
                if (chosen.add(port)) return port;
            }
    }

    private static void await(BooleanSupplier condition, Duration registry) throws Exception {
        long deadline = System.nanoTime() + registry.toNanos();
        while (System.nanoTime() < deadline) {
            if (condition.getAsBoolean()) return;
            Thread.sleep(100);
        }
        assertThat(condition.getAsBoolean()).as("bounded real-media condition").isTrue();
    }

    private static void stop(Process owned) throws Exception {
        if (owned == null) return;
        owned.destroy();
        if (!owned.waitFor(3, java.util.concurrent.TimeUnit.SECONDS)) {
            owned.destroyForcibly();
            assertThat(owned.waitFor(3, java.util.concurrent.TimeUnit.SECONDS)).isTrue();
        }
    }

    private static void stopOwnedChildren(Process server, Path executable) throws Exception {
        if (server == null || !server.isAlive()) return;
        try (var children = server.descendants()) {
            for (var child : children.limit(16).toList()) {
                if (child.info().command().map(Path::of).filter(executable::equals).isEmpty())
                    continue;
                child.destroy();
                try {
                    child.onExit().get(3, java.util.concurrent.TimeUnit.SECONDS);
                } catch (java.util.concurrent.TimeoutException timeout) {
                    child.destroyForcibly();
                    child.onExit().get(3, java.util.concurrent.TimeUnit.SECONDS);
                }
            }
        }
    }

    private static String required(String name) {
        String value = System.getenv(name);
        assertThat(value).as("explicit local test prerequisite " + name).isNotBlank();
        return value;
    }
}
