package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.node.OpsPreviewCatalog;
import com.streamfusion.platform.media.preview.node.ZlmDirectConnection;
import com.streamfusion.platform.media.preview.node.ZlmDirectGateway;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import java.net.DatagramSocket;
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
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.junit.jupiter.api.io.TempDir;

/** Explicit opt-in: isolated loopback ZLM, generated sine/video, no business service or device. */
@EnabledIfEnvironmentVariable(named = "SF_PREVIEW_AUDIO_REAL_MEDIA_TEST", matches = "true")
class ZlmPreviewAudioRealMediaTest {
    @TempDir(cleanup = org.junit.jupiter.api.io.CleanupMode.ON_SUCCESS)
    Path directory;

    private final ObjectMapper json = new ObjectMapper();
    private final String secret = UUID.randomUUID().toString();
    private final HttpClient http =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(1)).build();
    private URI apiBase;

    @Test
    void realPcmaAndOpusSurviveBothGatewaysWhileAacRemainsVideoOnly() throws Exception {
        Path zlm = Path.of(required("SF_PREVIEW_ZLM_BIN")).toAbsolutePath();
        Path ffmpeg = Path.of(required("SF_PREVIEW_FFMPEG_BIN")).toAbsolutePath();
        assertThat(Files.isRegularFile(zlm) && Files.isRegularFile(ffmpeg)).isTrue();
        var ports = new HashSet<Integer>();
        int apiPort = port(ports), rtsp = port(ports), rtc = port(ports);
        apiBase = URI.create("http://127.0.0.1:" + apiPort);
        Path config = directory.resolve("audio-test.ini"),
                secretFile = directory.resolve("zlm.secret");
        Files.writeString(
                config,
                """
                [api]
                apiDebug=0
                secret=%s
                [general]
                mediaServerId=preview-audio-isolated
                listen_ip=127.0.0.1
                streamNoneReaderDelayMS=30000
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
                port=0
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
                continue_push_ms=0
                enable_hls=0
                enable_mp4=0
                enable_rtsp=1
                enable_rtmp=0
                enable_audio=1
                """
                        .formatted(secret, apiPort, rtsp, rtc, rtc));
        OpsPreviewCatalog.writeProtectedSecret(secretFile, secret);
        var runtime =
                new MediaRuntimeService.ActiveZlm(apiBase, secret, zlm, config, "isolated-audio");
        var direct = new ZlmDirectGateway(json);
        var connection =
                new ZlmDirectConnection("zlm-1234567890abcdef", apiBase, secretFile, "127.0.0.1");
        var results = new ArrayList<Map<String, Object>>();
        Process server =
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
        var registry = new PreviewTranscodeRegistry(directory.resolve("transcodes"), json);
        try {
            await(() -> server.isAlive() && available(), Duration.ofSeconds(12));
            direct.requireIdle(connection, budget());
            var gateway =
                    new ZlmPreviewGateway(
                            json,
                            mock(MediaRuntimeService.class),
                            registry,
                            mock(PreviewProcessInspector.class));
            for (String codec : List.of("pcm_alaw", "pcm_mulaw", "libopus", "aac", "none")) {
                String suffix = UUID.randomUUID().toString().replace("-", "");
                String fixture = "audio_" + suffix.substring(0, 12);
                String app = "sf_preview_" + suffix.substring(0, 12);
                URI input = URI.create("rtsp://127.0.0.1:" + rtsp + "/" + fixture + "/source");
                var command =
                        new ArrayList<>(
                                List.of(
                                        ffmpeg.toString(),
                                        "-hide_banner",
                                        "-nostdin",
                                        "-loglevel",
                                        "warning",
                                        "-re",
                                        "-f",
                                        "lavfi",
                                        "-i",
                                        "testsrc2=size=160x90:rate=5"));
                if (!codec.equals("none"))
                    command.addAll(
                            List.of(
                                    "-re",
                                    "-f",
                                    "lavfi",
                                    "-i",
                                    "sine=frequency=440:sample_rate="
                                            + (codec.startsWith("pcm_") ? "8000" : "48000")));
                command.addAll(
                        List.of(
                                "-t",
                                "60",
                                "-c:v",
                                "libx264",
                                "-preset",
                                "ultrafast",
                                "-tune",
                                "zerolatency",
                                "-threads",
                                "1",
                                "-g",
                                "5"));
                if (codec.equals("none")) command.add("-an");
                else command.addAll(List.of("-c:a", codec, "-ac", "1"));
                command.addAll(List.of("-f", "rtsp", "-rtsp_transport", "tcp", input.toString()));
                Process publisher =
                        new ProcessBuilder(command)
                                .redirectErrorStream(true)
                                .redirectOutput(
                                        directory.resolve(codec + "-publisher.log").toFile())
                                .start();
                PreviewMediaGateway.Stream localSource = null, localViewer = null;
                var directSource =
                        new ZlmDirectGateway.Stream(
                                app, UUID.randomUUID().toString().replace("-", ""));
                var directViewer =
                        new ZlmDirectGateway.Stream(
                                app, UUID.randomUUID().toString().replace("-", ""));
                try {
                    await(
                            () ->
                                    codec.equals("none")
                                            ? readyVideo(fixture, "source")
                                            : readyAudio(fixture, "source"),
                            Duration.ofSeconds(12));
                    localSource = gateway.open(runtime, app, suffix, input, budget());
                    await(
                            () ->
                                    codec.equals("none")
                                            ? readyVideo(app, suffix)
                                            : readyAudio(app, suffix),
                            Duration.ofSeconds(10));
                    String viewerId = UUID.randomUUID().toString().replace("-", "");
                    localViewer = gateway.openViewer(localSource, app, viewerId, budget());
                    direct.open(connection, directSource, input, budget());
                    await(
                            () ->
                                    codec.equals("none")
                                            ? readyVideo(app, directSource.id())
                                            : readyAudio(app, directSource.id()),
                            Duration.ofSeconds(10));
                    direct.openViewer(connection, directSource, directViewer, budget());
                    boolean compatible = !List.of("aac", "none").contains(codec);
                    await(
                            () -> readyVideo(app, viewerId) && readyVideo(app, directViewer.id()),
                            Duration.ofSeconds(10));
                    assertThat(readyAudio(app, viewerId))
                            .as("local " + codec)
                            .isEqualTo(compatible);
                    assertThat(readyAudio(app, directViewer.id()))
                            .as("direct " + codec)
                            .isEqualTo(compatible);
                    var result = new LinkedHashMap<String, Object>();
                    result.put("sourceCodec", codec);
                    result.put("localViewerHasAudio", compatible);
                    result.put("directViewerHasAudio", compatible);
                    if (compatible) {
                        result.put(
                                "localDecodedPcmPeak",
                                decodePeak(ffmpeg, rtsp, app, viewerId, codec + "-local"));
                        result.put(
                                "directDecodedPcmPeak",
                                decodePeak(
                                        ffmpeg, rtsp, app, directViewer.id(), codec + "-direct"));
                    }
                    results.add(result);
                } finally {
                    try {
                        if (localViewer != null) gateway.close(localViewer, null, budget());
                        direct.close(connection, directViewer, budget());
                        direct.close(connection, directSource, budget());
                        if (localSource != null) gateway.close(localSource, null, budget());
                    } finally {
                        stop(publisher);
                        await(() -> absent(fixture, "source"), Duration.ofSeconds(5));
                    }
                }
                assertThat(absent(app, suffix)).isTrue();
                assertThat(absent(app, directSource.id())).isTrue();
                assertThat(absent(app, directViewer.id())).isTrue();
            }
            direct.requireIdle(connection, budget());
            String evidence = System.getenv("SF_PREVIEW_REAL_EVIDENCE_DIR");
            if (evidence != null) {
                Path target = Path.of(evidence);
                Files.createDirectories(target);
                Files.writeString(
                        target.resolve("audio-real-media.json"),
                        json.writerWithDefaultPrettyPrinter()
                                .writeValueAsString(
                                        Map.of(
                                                "zlmVersion",
                                                api("version", Map.of()).path("data"),
                                                "matrix",
                                                results,
                                                "cleanupConfirmed",
                                                true,
                                                "physicalIdleConfirmed",
                                                true,
                                                "browserListeningVerified",
                                                false,
                                                "webRtcSdpVerified",
                                                false,
                                                "transportValidated",
                                                "RTSP source through both gateways and actual PCM decoding")));
            }
        } finally {
            registry.close();
            stop(server);
        }
    }

    private int decodePeak(Path ffmpeg, int rtsp, String app, String id, String label)
            throws Exception {
        Path pcm = directory.resolve(label + ".pcm");
        Process decoder =
                new ProcessBuilder(
                                ffmpeg.toString(),
                                "-hide_banner",
                                "-nostdin",
                                "-loglevel",
                                "warning",
                                "-rtsp_transport",
                                "tcp",
                                "-analyzeduration",
                                "1000000",
                                "-i",
                                "rtsp://127.0.0.1:" + rtsp + "/" + app + "/" + id,
                                "-t",
                                "0.3",
                                "-map",
                                "0:a:0",
                                "-vn",
                                "-ac",
                                "1",
                                "-ar",
                                "8000",
                                "-f",
                                "s16le",
                                pcm.toString())
                        .redirectErrorStream(true)
                        .redirectOutput(directory.resolve(label + "-decode.log").toFile())
                        .start();
        try {
            assertThat(decoder.waitFor(10, TimeUnit.SECONDS))
                    .as(label + " decoder deadline")
                    .isTrue();
            assertThat(decoder.exitValue()).as(label + " decoder exit").isZero();
        } finally {
            stop(decoder);
        }
        byte[] samples = Files.readAllBytes(pcm);
        assertThat(samples.length).isBetween(1000, 64000);
        int peak = 0;
        for (int index = 0; index + 1 < samples.length; index += 2)
            peak =
                    Math.max(
                            peak,
                            Math.abs((short) ((samples[index] & 255) | samples[index + 1] << 8)));
        assertThat(peak).as(label + " actual sine audio").isGreaterThan(100);
        return peak;
    }

    private boolean readyAudio(String app, String id) {
        return hasTrack(app, id, 1);
    }

    private boolean absent(String app, String id) {
        try {
            JsonNode online =
                    api(
                                    "isMediaOnline",
                                    Map.of(
                                            "schema",
                                            "rtsp",
                                            "vhost",
                                            "__defaultVhost__",
                                            "app",
                                            app,
                                            "stream",
                                            id))
                            .path("online");
            return online.isBoolean() && !online.booleanValue();
        } catch (Exception unavailable) {
            return false;
        }
    }

    private boolean readyVideo(String app, String id) {
        return hasTrack(app, id, 0);
    }

    private boolean hasTrack(String app, String id, int type) {
        try {
            for (var media :
                    api("getMediaList", Map.of("schema", "rtsp", "app", app, "stream", id))
                            .path("data"))
                for (var track : media.path("tracks"))
                    if (track.path("codec_type").asInt(-1) == type
                            && track.path("ready").asBoolean()) return true;
        } catch (Exception unavailable) {
            return false;
        }
        return false;
    }

    private boolean available() {
        try {
            return api("version", Map.of()).path("code").asInt(-1) == 0;
        } catch (Exception unavailable) {
            return false;
        }
    }

    private JsonNode api(String name, Map<String, String> values) throws Exception {
        var params = new LinkedHashMap<>(values);
        params.put("secret", secret);
        String body =
                params.entrySet().stream()
                        .map(
                                entry ->
                                        URLEncoder.encode(entry.getKey(), StandardCharsets.UTF_8)
                                                + "="
                                                + URLEncoder.encode(
                                                        entry.getValue(), StandardCharsets.UTF_8))
                        .collect(java.util.stream.Collectors.joining("&"));
        var request =
                HttpRequest.newBuilder(apiBase.resolve("/index/api/" + name))
                        .timeout(Duration.ofSeconds(2))
                        .header("Content-Type", "application/x-www-form-urlencoded")
                        .POST(HttpRequest.BodyPublishers.ofString(body))
                        .build();
        var response = http.send(request, HttpResponse.BodyHandlers.ofString());
        assertThat(response.statusCode()).isEqualTo(200);
        var value = json.readTree(response.body());
        assertThat(value.path("code").asInt(-1)).as(name).isZero();
        return value;
    }

    private static PreviewDeadline budget() {
        return PreviewDeadline.after(Duration.ofSeconds(12));
    }

    private static String required(String name) {
        String value = System.getenv(name);
        if (value == null || value.isBlank())
            throw new IllegalStateException(name + " is required");
        return value;
    }

    private static void await(BooleanSupplier condition, Duration timeout) throws Exception {
        long end = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < end) {
            if (condition.getAsBoolean()) return;
            Thread.sleep(100);
        }
        throw new AssertionError("Isolated audio media observation deadline exceeded");
    }

    private static int port(HashSet<Integer> chosen) throws Exception {
        for (int attempt = 0; attempt < 100; attempt++)
            try (var udp = new DatagramSocket(0, InetAddress.getLoopbackAddress());
                    var tcp =
                            new ServerSocket(
                                    udp.getLocalPort(), 0, InetAddress.getLoopbackAddress())) {
                if (chosen.add(tcp.getLocalPort())) return tcp.getLocalPort();
            } catch (java.net.BindException unavailable) {
                /* select another free loopback port */
            }
        throw new IllegalStateException("No isolated test port available");
    }

    private static void stop(Process process) throws InterruptedException {
        if (process == null || !process.isAlive()) return;
        process.destroy();
        if (!process.waitFor(2, TimeUnit.SECONDS)) {
            process.destroyForcibly();
            assertThat(process.waitFor(2, TimeUnit.SECONDS)).isTrue();
        }
    }
}
