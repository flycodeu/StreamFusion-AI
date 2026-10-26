package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.execution.PreviewRuntimeProvider;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.*;
import java.net.http.*;
import java.nio.file.*;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.junit.jupiter.api.io.TempDir;

/** Explicit opt-in, synthetic H264 and owned isolated processes, never a business ZLM. */
@EnabledIfEnvironmentVariable(named = "SF_CAMERA_PROTOCOL_REAL_TEST", matches = "true")
class CameraProtocolsRealMediaTest {
    @TempDir(cleanup = org.junit.jupiter.api.io.CleanupMode.ON_SUCCESS)
    Path directory;

    @Test
    void syntheticCameraInputsConvergeThroughZlmAndDecodeFrames() throws Exception {
        Path zlm = Path.of(System.getenv("SF_PREVIEW_ZLM_BIN")),
                ffmpeg = Path.of(System.getenv("SF_PREVIEW_FFMPEG_BIN"));
        int api = free(), rtsp = free(), rtmp = free(), rtc = free();
        String secret = UUID.randomUUID().toString();
        Path config = directory.resolve("zlm.ini");
        String command =
                Files.readAllLines(Path.of("config/zlm-preview.ini.example")).stream()
                        .filter(l -> l.startsWith("cmd="))
                        .findFirst()
                        .orElseThrow();
        Files.writeString(
                config,
                "[api]\nsecret="
                        + secret
                        + "\napiDebug=0\n[general]\nlisten_ip=127.0.0.1\nstreamNoneReaderDelayMS=60000\n[hook]\nenable=0\n[http]\nport="
                        + api
                        + "\nsslport=0\nrootPath="
                        + directory.toString().replace('\\', '/')
                        + "\n[rtsp]\nport="
                        + rtsp
                        + "\nsslport=0\ndirectProxy=0\n[rtmp]\nport="
                        + rtmp
                        + "\nsslport=0\n[rtc]\nport="
                        + rtc
                        + "\ntcpPort="
                        + rtc
                        + "\nexternIP=127.0.0.1\n[shell]\nport=0\n[srt]\nport=0\n[rtp_proxy]\nport=0\n[protocol]\npaced_sender_ms=20\nenable_hls=1\nenable_mp4=0\nenable_rtsp=1\nenable_rtmp=1\nenable_audio=0\n[hls]\nsegDur=1\nsegNum=3\n[ffmpeg]\nbin="
                        + ffmpeg.toString().replace('\\', '/')
                        + "\n"
                        + command
                        + "\nrestart_sec=0\n");
        var runtime =
                new ActiveZlm(
                        URI.create("http://127.0.0.1:" + api),
                        secret,
                        zlm,
                        config,
                        "isolated-protocol-test");
        Files.writeString(
                config,
                Files.readString(config)
                        .replace(
                                "[protocol]\n",
                                "[protocol]\nhls_save_path="
                                        + directory.toString().replace('\\', '/')
                                        + "\n"));
        var provider = mock(PreviewRuntimeProvider.class);
        when(provider.activeZlm()).thenReturn(Optional.of(runtime));
        Path evidence =
                Path.of(
                                System.getenv()
                                        .getOrDefault(
                                                "SF_PREVIEW_REAL_EVIDENCE_DIR",
                                                ".run/camera-protocol-real"))
                        .toAbsolutePath();
        Files.createDirectories(evidence);
        var server =
                process(
                        evidence.resolve("zlm.log"),
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
                        evidence.resolve("zlm-logs").toString());
        Process publisher = null, passivePublisher = null;
        var results = new LinkedHashMap<String, Object>();
        var json = new ObjectMapper();
        var registry = new PreviewTranscodeRegistry(directory.resolve("registry"), json);
        try (var relay = new HttpMediaRelay()) {
            var gateway =
                    new ZlmPreviewGateway(
                            json, provider, registry, new PreviewProcessInspector(), relay);
            await(
                    () -> {
                        try {
                            return HttpClient.newHttpClient()
                                            .send(
                                                    HttpRequest.newBuilder(
                                                                    URI.create(
                                                                            "http://127.0.0.1:"
                                                                                    + api
                                                                                    + "/index/api/getServerConfig?secret="
                                                                                    + secret))
                                                            .timeout(Duration.ofSeconds(1))
                                                            .GET()
                                                            .build(),
                                                    HttpResponse.BodyHandlers.ofString())
                                            .statusCode()
                                    == 200;
                        } catch (Exception e) {
                            return false;
                        }
                    },
                    12);
            publisher =
                    process(
                            evidence.resolve("publisher.log"),
                            ffmpeg.toString(),
                            "-hide_banner",
                            "-loglevel",
                            "warning",
                            "-re",
                            "-f",
                            "lavfi",
                            "-i",
                            "testsrc2=size=320x180:rate=10",
                            "-an",
                            "-c:v",
                            "libx264",
                            "-preset",
                            "ultrafast",
                            "-tune",
                            "zerolatency",
                            "-g",
                            "10",
                            "-f",
                            "flv",
                            "rtmp://127.0.0.1:" + rtmp + "/fixture/camera");
            await(() -> Files.exists(directory.resolve("fixture/camera/hls.m3u8")), 15);
            var sources = new LinkedHashMap<String, URI>();
            sources.put("RTSP", URI.create("rtsp://127.0.0.1:" + rtsp + "/fixture/camera"));
            sources.put("RTMP", URI.create("rtmp://127.0.0.1:" + rtmp + "/fixture/camera"));
            sources.put("FLV", URI.create("http://127.0.0.1:" + api + "/fixture/camera.live.flv"));
            sources.put("HLS", URI.create("http://127.0.0.1:" + api + "/fixture/camera/hls.m3u8"));
            String passiveId = UUID.randomUUID().toString().replace("-", "");
            passivePublisher =
                    process(
                            evidence.resolve("crtsp-publisher.log"),
                            ffmpeg.toString(),
                            "-hide_banner",
                            "-loglevel",
                            "error",
                            "-rtsp_transport",
                            "tcp",
                            "-i",
                            "rtsp://127.0.0.1:" + rtsp + "/fixture/camera",
                            "-an",
                            "-c:v",
                            "copy",
                            "-rtsp_transport",
                            "tcp",
                            "-f",
                            "rtsp",
                            "rtsp://127.0.0.1:" + rtsp + "/sf_ingress/" + passiveId);
            sources.put(
                    "CRTSP", URI.create("rtsp://127.0.0.1:" + rtsp + "/sf_ingress/" + passiveId));
            int rtpPort = free();
            String gbApp = "sf_gb_0123456789ab",
                    gbId = UUID.randomUUID().toString().replace("-", "");
            api(
                    runtime,
                    "openRtpServer",
                    Map.of(
                            "app",
                            gbApp,
                            "stream_id",
                            gbId,
                            "port",
                            Integer.toString(rtpPort),
                            "tcp_mode",
                            "0",
                            "ssrc",
                            "1234567890"));
            var receivers = api(runtime, "listRtpServer", Map.of()).path("data");
            assertThat(receivers.toString()).contains(gbApp, gbId);
            api(
                    runtime,
                    "startSendRtp",
                    Map.of(
                            "vhost",
                            "__defaultVhost__",
                            "app",
                            "fixture",
                            "stream",
                            "camera",
                            "ssrc",
                            "1234567890",
                            "dst_url",
                            "127.0.0.1",
                            "dst_port",
                            Integer.toString(rtpPort),
                            "is_udp",
                            "1",
                            "pt",
                            "96",
                            "use_ps",
                            "1"));
            sources.put(
                    "GB28181_PS_RTP",
                    URI.create("rtsp://127.0.0.1:" + rtsp + "/" + gbApp + "/" + gbId));
            for (var source : sources.entrySet()) {
                String
                        app =
                                "sf_preview_"
                                        + UUID.randomUUID()
                                                .toString()
                                                .replace("-", "")
                                                .substring(0, 12),
                        id = UUID.randomUUID().toString().replace("-", "");
                var stream =
                        gateway.open(
                                runtime,
                                app,
                                id,
                                source.getValue(),
                                PreviewDeadline.after(Duration.ofSeconds(18)));
                try {
                    var decoder =
                            process(
                                    evidence.resolve(source.getKey() + "-decode.log"),
                                    ffmpeg.toString(),
                                    "-hide_banner",
                                    "-loglevel",
                                    "error",
                                    "-rtsp_transport",
                                    "tcp",
                                    "-i",
                                    "rtsp://127.0.0.1:" + rtsp + "/" + app + "/" + id,
                                    "-frames:v",
                                    "3",
                                    "-f",
                                    "null",
                                    "-");
                    try {
                        assertThat(decoder.waitFor(12, TimeUnit.SECONDS))
                                .as(source.getKey() + " decoded frames")
                                .isTrue();
                        assertThat(decoder.exitValue()).isZero();
                    } finally {
                        stop(decoder);
                    }
                    results.put(
                            source.getKey(),
                            Map.of("decodedFrames", 3, "zlmOutput", "RTSP", "passed", true));
                } finally {
                    await(
                            () -> {
                                try {
                                    gateway.close(
                                            stream,
                                            null,
                                            PreviewDeadline.after(Duration.ofSeconds(4)));
                                    return true;
                                } catch (RuntimeException pending) {
                                    return false;
                                }
                            },
                            35);
                }
            }
            assertThat(relay.sessionCount()).isZero();
            api(
                    runtime,
                    "stopSendRtp",
                    Map.of(
                            "vhost",
                            "__defaultVhost__",
                            "app",
                            "fixture",
                            "stream",
                            "camera",
                            "ssrc",
                            "1234567890"));
            api(runtime, "closeRtpServer", Map.of("app", gbApp, "stream_id", gbId));
            assertThat(api(runtime, "listRtpServer", Map.of()).path("data").toString())
                    .doesNotContain(gbId);
            results.put("relaySessionsAfterClose", relay.sessionCount());
        } finally {
            registry.close();
            stop(passivePublisher);
            stop(publisher);
            stop(server);
            results.put("isolatedZlmExited", !server.isAlive());
            Files.writeString(
                    evidence.resolve("result.json"),
                    json.writerWithDefaultPrettyPrinter().writeValueAsString(results));
        }
    }

    private static com.fasterxml.jackson.databind.JsonNode api(
            ActiveZlm runtime, String method, Map<String, String> values) throws Exception {
        var params = new LinkedHashMap<>(values);
        params.put("secret", runtime.secret());
        String query =
                params.entrySet().stream()
                        .map(
                                e ->
                                        URLEncoder.encode(
                                                        e.getKey(),
                                                        java.nio.charset.StandardCharsets.UTF_8)
                                                + "="
                                                + URLEncoder.encode(
                                                        e.getValue(),
                                                        java.nio.charset.StandardCharsets.UTF_8))
                        .collect(java.util.stream.Collectors.joining("&"));
        var response =
                HttpClient.newHttpClient()
                        .send(
                                HttpRequest.newBuilder(
                                                runtime.apiBase().resolve("/index/api/" + method))
                                        .timeout(Duration.ofSeconds(5))
                                        .header("Content-Type", "application/x-www-form-urlencoded")
                                        .POST(HttpRequest.BodyPublishers.ofString(query))
                                        .build(),
                                HttpResponse.BodyHandlers.ofString());
        var json = new ObjectMapper().readTree(response.body());
        assertThat(json.path("code").asInt(-1)).as(method).isZero();
        return json;
    }

    private static Process process(Path log, String... command) throws Exception {
        return new ProcessBuilder(command)
                .redirectErrorStream(true)
                .redirectOutput(log.toFile())
                .start();
    }

    private static int free() throws Exception {
        try (var socket = new java.net.ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static void stop(Process process) throws Exception {
        if (process != null && process.isAlive()) {
            process.destroy();
            if (!process.waitFor(3, TimeUnit.SECONDS)) {
                process.destroyForcibly();
                process.waitFor(3, TimeUnit.SECONDS);
            }
        }
    }

    private static void await(java.util.function.BooleanSupplier predicate, int seconds)
            throws Exception {
        long end = System.nanoTime() + TimeUnit.SECONDS.toNanos(seconds);
        do {
            if (predicate.getAsBoolean()) return;
            Thread.sleep(100);
        } while (System.nanoTime() < end);
        throw new AssertionError("isolated media deadline exceeded");
    }
}
