package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
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
import java.time.Instant;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.Timeout;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.junit.jupiter.api.io.CleanupMode;
import org.junit.jupiter.api.io.TempDir;

/** Opt-in RTSP load fixture, not a WebRTC/browser or deployment-capacity acceptance test. */
@EnabledIfEnvironmentVariable(named = "SF_PREVIEW_CAPACITY_TEST", matches = "true")
class ZlmPreviewCapacityTest {
    private static final int FPS = 15;
    private final ObjectMapper json = new ObjectMapper();
    private final HttpClient http =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(1)).build();
    private final HashSet<Integer> ports = new HashSet<>();
    private final Map<String, Object> evidence = new LinkedHashMap<>();
    private final List<Process> ownedProcesses = new ArrayList<>();

    @TempDir(cleanup = CleanupMode.ON_SUCCESS)
    Path directory;

    @Test
    @Timeout(value = 12, unit = TimeUnit.MINUTES)
    void measuresSixteenThirtySixSixtyFourAndSharedSixtyFourWithConfirmedCleanup()
            throws Exception {
        Path zlm = executable("SF_PREVIEW_ZLM_BIN");
        Path ffmpeg = executable("SF_PREVIEW_FFMPEG_BIN");
        String destination = required("SF_PREVIEW_CAPACITY_EVIDENCE_DIR");
        int seconds =
                Integer.parseInt(System.getenv().getOrDefault("SF_PREVIEW_CAPACITY_SECONDS", "10"));
        assertThat(seconds).isBetween(10, 60);
        var runs = new ArrayList<Map<String, Object>>();
        evidence.put("status", "FAIL");
        evidence.put("startedAt", Instant.now().toString());
        evidence.put(
                "scope",
                "isolated-loopback-RTSP; no WebRTC, browser, camera, NIC or production SLA");
        evidence.put(
                "video", "generated H264 1920x1080, 15fps, target CBR 2Mbps, no audio/transcode");
        evidence.put("zlmThreadsPerProcess", 2);
        evidence.put("availableProcessors", Runtime.getRuntime().availableProcessors());
        evidence.put("sampleSeconds", seconds);
        evidence.put("processRss", "not measured");
        evidence.put("runs", runs);
        Server media = null, peer = null;
        Process publisher = null;
        boolean bodySucceeded = false;
        var registry = new PreviewTranscodeRegistry(directory.resolve("registry"), json);
        var gateway =
                new ZlmPreviewGateway(
                        json,
                        mock(MediaRuntimeService.class),
                        registry,
                        new PreviewProcessInspector());
        try {
            media = startServer(zlm, "media");
            peer = startServer(zlm, "peer");
            evidence.put(
                    "zlmCommit",
                    api(media, "version", Map.of()).path("data").path("commitHash").asText());
            publisher = publisher(ffmpeg, peer);
            Server source = peer;
            await(() -> sourceMedia(source).path("tracks").isArray(), Duration.ofSeconds(12));
            for (int viewers : List.of(16, 36, 64))
                run(gateway, media, peer, publisher, viewers, false, seconds, runs);
            run(gateway, media, peer, publisher, 64, true, seconds, runs);
            assertThat(registry.keys()).isEmpty();
            bodySucceeded = true;
        } finally {
            // An interrupted test still stops only its own three processes, with finite waits.
            boolean interrupted = Thread.interrupted();
            try {
                stopAll(ownedProcesses.reversed().toArray(Process[]::new));
                evidence.put("status", bodySucceeded ? "PASS" : "FAIL");
            } finally {
                registry.close();
                boolean stopped = ownedProcesses.stream().noneMatch(Process::isAlive);
                evidence.put("ownedProcessesStopped", stopped);
                if (!stopped) evidence.put("status", "FAIL");
                evidence.put("finishedAt", Instant.now().toString());
                Files.createDirectories(Path.of(destination));
                Files.writeString(
                        Path.of(destination).resolve("capacity-result.json"),
                        json.writerWithDefaultPrettyPrinter().writeValueAsString(evidence));
                if (interrupted) Thread.currentThread().interrupt();
            }
        }
    }

    private void run(
            ZlmPreviewGateway gateway,
            Server media,
            Server peer,
            Process publisher,
            int viewers,
            boolean shared,
            int seconds,
            List<Map<String, Object>> runs)
            throws Exception {
        String app = "sf_preview_" + random().substring(0, 12);
        var roots = new ArrayList<Stream>();
        var outputs = new ArrayList<Stream>();
        var sinks = new ArrayList<Stream>();
        var setupTimes = new ArrayList<Double>();
        var result = new LinkedHashMap<String, Object>();
        result.put("viewers", viewers);
        result.put("shared", shared);
        result.put("status", "FAIL");
        runs.add(result);
        var setupDeadline = PreviewDeadline.after(Duration.ofSeconds(90));
        long setupStarted = System.nanoTime();
        Throwable primary = null;
        System.out.printf("PreviewCapacity start viewers=%d shared=%s%n", viewers, shared);
        try {
            for (int index = 0; index < viewers; index++) {
                long started = System.nanoTime();
                if (!shared || roots.isEmpty()) {
                    var root = owned(media, app);
                    roots.add(root); // Register the exact tuple before a possibly uncertain open.
                    gateway.open(
                            media.runtime(),
                            app,
                            root.id(),
                            uri(peer, "capacity_source", "input"),
                            setupDeadline);
                }
                var root = roots.get(shared ? 0 : index);
                var output = owned(media, app);
                outputs.add(output);
                gateway.openViewer(root, app, output.id(), setupDeadline);
                var sink = owned(peer, app);
                sinks.add(sink);
                addReceiver(peer, sink, uri(media, app, output.id()), setupDeadline);
                await(
                        () -> video(media(peer, app).get(sink.id())).path("frames").asLong() >= 2,
                        setupDeadline.remaining());
                setupTimes.add((System.nanoTime() - started) / 1_000_000.0);
            }
            result.put("setupTotalMs", (System.nanoTime() - setupStarted) / 1_000_000.0);
            setupTimes.sort(Double::compareTo);
            result.put(
                    "perViewerSetupToTwoFramesP95Ms",
                    setupTimes.get((int) Math.ceil(viewers * .95) - 1));
            result.put("remoteRoots", roots.size());
            result.put("localViewerProxies", outputs.size());
            assertThat(sourceMedia(peer).path("totalReaderCount").asInt(-1))
                    .isEqualTo(roots.size());
            var firstInput = sample(media, app);
            var firstOutput = sample(peer, app);
            assertTopology(firstInput, roots, outputs, shared);
            assertThat(firstOutput.values()).hasSize(viewers);
            Long mediaCpu = cpu(media.process()),
                    peerCpu = cpu(peer.process()),
                    encoderCpu = cpu(publisher);
            long windowStarted = System.nanoTime();
            long windowEnd = windowStarted + Duration.ofSeconds(seconds).toNanos();
            while (System.nanoTime() < windowEnd) {
                assertThat(alive(media) && alive(peer) && publisher.isAlive()).isTrue();
                Thread.sleep(100);
            }
            double elapsed = (System.nanoTime() - windowStarted) / 1_000_000_000.0;
            result.put("mediaCpuEquivalentCores", cpuCores(media.process(), mediaCpu, elapsed));
            result.put("peerCpuEquivalentCores", cpuCores(peer.process(), peerCpu, elapsed));
            result.put("encoderCpuEquivalentCores", cpuCores(publisher, encoderCpu, elapsed));
            var lastInput = sample(media, app);
            var lastOutput = sample(peer, app);
            assertTopology(lastInput, roots, outputs, shared);
            result.put("inputMbps", throughput(firstInput, lastInput, roots));
            result.put("outputMbps", throughput(firstOutput, lastOutput, sinks));
            result.put("minimumReceivedFps", minimumFps(firstOutput, lastOutput, sinks));
            result.put(
                    "sampleElapsedSeconds", (lastOutput.at() - firstOutput.at()) / 1_000_000_000.0);
            assertThat((double) result.get("minimumReceivedFps")).isGreaterThanOrEqualTo(FPS * .8);
            assertThat((double) result.get("inputMbps")).isPositive();
            assertThat((double) result.get("outputMbps")).isPositive();
            result.put("status", "PASS");
        } catch (Exception | AssertionError failure) {
            primary = failure;
            result.put("failureType", failure.getClass().getSimpleName());
            throw failure;
        } finally {
            try {
                var cleanup = PreviewDeadline.after(Duration.ofSeconds(30));
                // Stop remote readers before local outputs, and local outputs before roots.
                for (var group : List.of(sinks, outputs, roots))
                    for (var stream : group) closeConfirmed(gateway, stream, cleanup);
                assertThat(media(media, app)).isEmpty();
                assertThat(media(peer, app)).isEmpty();
                assertThat(sourceMedia(peer).path("totalReaderCount").asInt(-1)).isZero();
                result.put("ownedMediaGone", true);
            } catch (Exception | AssertionError cleanupFailure) {
                result.put("status", "FAIL");
                result.put("ownedMediaGone", false);
                result.put("cleanupFailureType", cleanupFailure.getClass().getSimpleName());
                if (primary != null) primary.addSuppressed(cleanupFailure);
                else throw cleanupFailure;
            }
        }
        System.out.printf(
                "PreviewCapacity PASS viewers=%d shared=%s minFps=%.2f inputMbps=%.2f outputMbps=%.2f%n",
                viewers,
                shared,
                result.get("minimumReceivedFps"),
                result.get("inputMbps"),
                result.get("outputMbps"));
    }

    private static void closeConfirmed(
            ZlmPreviewGateway gateway, Stream stream, PreviewDeadline deadline) throws Exception {
        await(
                () -> {
                    try {
                        gateway.close(stream, null, deadline);
                        return true;
                    } catch (PreviewFailure pending) {
                        if (!"CLEANUP_PENDING".equals(pending.reason())) throw pending;
                        return false;
                    }
                },
                deadline.remaining());
    }

    private static void assertTopology(
            Sample sample, List<Stream> roots, List<Stream> outputs, boolean shared) {
        assertThat(sample.values()).hasSize(roots.size() + outputs.size());
        for (var root : roots)
            assertThat(sample.values().get(root.id()).readers())
                    .isEqualTo(shared ? outputs.size() : 1);
        for (var output : outputs)
            assertThat(sample.values().get(output.id()).readers()).isEqualTo(1);
    }

    private Sample sample(Server server, String app) throws Exception {
        var values = new LinkedHashMap<String, Counters>();
        for (var entry : media(server, app).entrySet()) {
            var item = entry.getValue();
            var video = video(item);
            assertThat(video.path("codec_id_name").asText()).isEqualTo("H264");
            assertThat(video.path("width").asInt()).isEqualTo(1920);
            assertThat(video.path("height").asInt()).isEqualTo(1080);
            assertThat(video.path("frames").isIntegralNumber()).isTrue();
            assertThat(item.path("totalBytes").isIntegralNumber()).isTrue();
            assertThat(item.path("createStamp").asLong()).isPositive();
            values.put(
                    entry.getKey(),
                    new Counters(
                            video.path("frames").longValue(),
                            item.path("totalBytes").longValue(),
                            item.path("createStamp").longValue(),
                            item.path("totalReaderCount").asInt(-1)));
        }
        return new Sample(System.nanoTime(), values);
    }

    private static double throughput(Sample first, Sample last, List<Stream> selected) {
        long bytes = 0;
        for (var stream : selected) {
            var before = first.values().get(stream.id());
            var after = last.values().get(stream.id());
            assertThat(after).isNotNull();
            assertThat(after.created()).isEqualTo(before.created());
            assertThat(after.bytes()).isGreaterThanOrEqualTo(before.bytes());
            bytes += after.bytes() - before.bytes();
        }
        return bytes * 8_000.0 / (last.at() - first.at());
    }

    private static double minimumFps(Sample first, Sample last, List<Stream> selected) {
        double minimum = Double.POSITIVE_INFINITY;
        for (var stream : selected) {
            var before = first.values().get(stream.id());
            var after = last.values().get(stream.id());
            assertThat(after.created()).isEqualTo(before.created());
            minimum =
                    Math.min(
                            minimum,
                            (after.frames() - before.frames())
                                    * 1_000_000_000.0
                                    / (last.at() - first.at()));
        }
        return minimum;
    }

    private Map<String, JsonNode> media(Server server, String app) throws Exception {
        var data =
                api(
                                server,
                                "getMediaList",
                                Map.of("schema", "rtsp", "vhost", "__defaultVhost__", "app", app))
                        .path("data");
        var values = new LinkedHashMap<String, JsonNode>();
        if (data.isMissingNode() || data.isNull()) return values;
        assertThat(data.isArray()).isTrue();
        for (var item : data) {
            assertThat(item.path("schema").asText()).isEqualTo("rtsp");
            assertThat(item.path("app").asText()).isEqualTo(app);
            assertThat(values.put(item.path("stream").asText(), item)).isNull();
        }
        return values;
    }

    private JsonNode sourceMedia(Server peer) throws Exception {
        return media(peer, "capacity_source").getOrDefault("input", json.createObjectNode());
    }

    private static JsonNode video(JsonNode media) {
        if (media != null)
            for (var track : media.path("tracks"))
                if (track.path("codec_type").asInt(-1) == 0) return track;
        return com.fasterxml.jackson.databind.node.MissingNode.getInstance();
    }

    private void addReceiver(Server server, Stream sink, URI source, PreviewDeadline deadline)
            throws Exception {
        deadline.check();
        var parameters = new LinkedHashMap<String, String>();
        parameters.put("vhost", "__defaultVhost__");
        parameters.put("app", sink.app());
        parameters.put("stream", sink.id());
        parameters.put("url", source.toASCIIString());
        parameters.put("rtp_type", "0");
        parameters.put("retry_count", "0");
        parameters.put("timeout_sec", "5");
        // Terminal receiver has no reader. Disable idle close only in this owned test process.
        parameters.put("auto_close", "0");
        for (var option :
                List.of(
                        "enable_hls",
                        "enable_mp4",
                        "enable_rtmp",
                        "enable_ts",
                        "enable_fmp4",
                        "enable_audio")) parameters.put(option, "0");
        parameters.put("enable_rtsp", "1");
        assertThat(api(server, "addStreamProxy", parameters).path("data").path("key").asText())
                .isEqualTo(sink.proxyKey());
        deadline.check();
    }

    private Server startServer(Path binary, String name) throws Exception {
        int apiPort = unusedPort(), rtsp = unusedPort(), rtmp = unusedPort();
        Path config = directory.resolve(name + ".ini");
        String secret = random();
        Files.writeString(
                config,
                """
                [api]
                apiDebug=0
                secret=%s
                [general]
                listen_ip=127.0.0.1
                mediaServerId=capacity-%s
                streamNoneReaderDelayMS=20000
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
                port=0
                tcpPort=0
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
                enable_rtmp=0
                enable_audio=0
                """
                        .formatted(secret, name, apiPort, rtsp, rtmp));
        var runtime =
                new ActiveZlm(
                        URI.create("http://127.0.0.1:" + apiPort),
                        secret,
                        binary,
                        config,
                        "capacity-" + name);
        var process =
                new ProcessBuilder(
                                binary.toString(),
                                "-c",
                                config.toString(),
                                "-t",
                                "2",
                                "--affinity",
                                "0",
                                "-l",
                                "3",
                                "--log-dir",
                                directory.resolve(name + "-logs").toString())
                        .directory(directory.toFile())
                        .redirectErrorStream(true)
                        .redirectOutput(directory.resolve(name + ".log").toFile())
                        .start();
        var server = new Server(runtime, rtsp, process);
        ownedProcesses.add(process); // Also retained if startup is interrupted before returning.
        await(() -> available(server), Duration.ofSeconds(12));
        return server;
    }

    private Process publisher(Path binary, Server peer) throws Exception {
        var process =
                new ProcessBuilder(
                                binary.toString(),
                                "-hide_banner",
                                "-loglevel",
                                "warning",
                                "-nostdin",
                                "-re",
                                "-f",
                                "lavfi",
                                "-i",
                                "testsrc2=size=1920x1080:rate=15",
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
                                "15",
                                "-b:v",
                                "2M",
                                "-minrate",
                                "2M",
                                "-maxrate",
                                "2M",
                                "-bufsize",
                                "4M",
                                "-x264-params",
                                "nal-hrd=cbr:force-cfr=1",
                                "-f",
                                "rtsp",
                                "-rtsp_transport",
                                "tcp",
                                uri(peer, "capacity_source", "input").toString())
                        .redirectErrorStream(true)
                        .redirectOutput(directory.resolve("publisher.log").toFile())
                        .start();
        ownedProcesses.add(process);
        return process;
    }

    private JsonNode api(Server server, String action, Map<String, String> parameters)
            throws Exception {
        var values = new LinkedHashMap<>(parameters);
        values.put("secret", server.runtime().secret());
        String body =
                values.entrySet().stream()
                        .map(
                                entry ->
                                        URLEncoder.encode(entry.getKey(), StandardCharsets.UTF_8)
                                                + "="
                                                + URLEncoder.encode(
                                                        entry.getValue(), StandardCharsets.UTF_8))
                        .collect(java.util.stream.Collectors.joining("&"));
        var request =
                HttpRequest.newBuilder(server.runtime().apiBase().resolve("/index/api/" + action))
                        .timeout(Duration.ofSeconds(5))
                        .header("Content-Type", "application/x-www-form-urlencoded")
                        .POST(HttpRequest.BodyPublishers.ofString(body))
                        .build();
        var response = http.send(request, HttpResponse.BodyHandlers.ofString());
        assertThat(response.statusCode()).isEqualTo(200);
        var value = json.readTree(response.body());
        // Do not print vendor response bodies: they can include internal URLs or the API secret.
        assertThat(value.path("code").asInt(-1)).as("isolated ZLM action %s", action).isZero();
        return value;
    }

    private boolean available(Server server) {
        if (!server.process().isAlive()) return false;
        try {
            return api(server, "version", Map.of()).path("code").asInt(-1) == 0;
        } catch (Exception unavailable) {
            return false;
        }
    }

    private int unusedPort() throws Exception {
        for (int attempt = 0; attempt < 32; attempt++)
            try (var socket = new ServerSocket(0, 0, InetAddress.getLoopbackAddress())) {
                if (ports.add(socket.getLocalPort())) return socket.getLocalPort();
            }
        throw new IllegalStateException("No distinct local test port");
    }

    private static Stream owned(Server server, String app) {
        String id = random();
        return new Stream(server.runtime(), app, id, "__defaultVhost__/" + app + "/" + id);
    }

    private static URI uri(Server server, String app, String id) {
        return URI.create("rtsp://127.0.0.1:" + server.rtsp() + "/" + app + "/" + id);
    }

    private static String random() {
        return UUID.randomUUID().toString().replace("-", "");
    }

    private static boolean alive(Server server) {
        return server != null && alive(server.process());
    }

    private static boolean alive(Process process) {
        return process != null && process.isAlive();
    }

    private static Long cpu(Process process) {
        return process.info().totalCpuDuration().map(Duration::toNanos).orElse(null);
    }

    private static Double cpuCores(Process process, Long before, double seconds) {
        Long after = cpu(process);
        return before == null || after == null
                ? null
                : (after - before) / 1_000_000_000.0 / seconds;
    }

    private static Path executable(String name) {
        Path path = Path.of(required(name)).toAbsolutePath();
        assertThat(Files.isRegularFile(path)).as("explicit test executable %s", name).isTrue();
        return path;
    }

    private static String required(String name) {
        String value = System.getenv(name);
        assertThat(value).as("explicit test prerequisite %s", name).isNotBlank();
        return value;
    }

    private static void stop(Process process) throws Exception {
        if (!alive(process)) return;
        process.destroy();
        if (!process.waitFor(3, TimeUnit.SECONDS)) {
            process.destroyForcibly();
            assertThat(process.waitFor(3, TimeUnit.SECONDS)).isTrue();
        }
    }

    private static void stopAll(Process... processes) throws Exception {
        Exception failure = null;
        for (var process : processes) {
            try {
                stop(process);
            } catch (Exception | AssertionError stopped) {
                if (failure == null)
                    failure = new IllegalStateException("Owned process cleanup failed");
                failure.addSuppressed(stopped);
            }
        }
        if (failure != null) throw failure;
    }

    private static void await(Condition condition, Duration timeout) throws Exception {
        long deadline = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < deadline) {
            if (condition.ready()) return;
            Thread.sleep(100);
        }
        assertThat(condition.ready()).as("bounded capacity condition").isTrue();
    }

    @FunctionalInterface
    private interface Condition {
        boolean ready() throws Exception;
    }

    private record Server(ActiveZlm runtime, int rtsp, Process process) {}

    private record Counters(long frames, long bytes, long created, int readers) {}

    private record Sample(long at, Map<String, Counters> values) {}
}
