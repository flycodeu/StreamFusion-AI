package com.streamfusion.platform.media.preview.media;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewModels;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.io.ByteArrayOutputStream;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.Flow;
import java.util.concurrent.TimeUnit;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Component;

@Component
public class ZlmPreviewGateway implements PreviewMediaGateway {
    private static final Logger LOG = LoggerFactory.getLogger(ZlmPreviewGateway.class);
    private static final String VHOST = "__defaultVhost__";
    private static final Duration MIN_START_REMAINING = Duration.ofSeconds(10);
    private static final Duration RECOVERY_BUDGET = Duration.ofMillis(500);
    private final HttpClient http =
            HttpClient.newBuilder()
                    .connectTimeout(Duration.ofSeconds(3))
                    .followRedirects(HttpClient.Redirect.NEVER)
                    .build();
    private final ObjectMapper json;
    private final MediaRuntimeService runtimes;
    private final PreviewTranscodeRegistry registry;
    private final PreviewProcessInspector processes;
    private final java.util.concurrent.ConcurrentMap<String, Long> uncertainOpens =
            new java.util.concurrent.ConcurrentHashMap<>();

    public ZlmPreviewGateway(
            ObjectMapper json,
            MediaRuntimeService runtimes,
            PreviewTranscodeRegistry registry,
            PreviewProcessInspector processes) {
        this.json = json;
        this.runtimes = runtimes;
        this.registry = registry;
        this.processes = processes;
    }

    @Override
    public Stream open(ActiveZlm runtime, String app, String id, URI source) {
        return open(runtime, app, id, source, PreviewDeadline.after(Duration.ofSeconds(18)));
    }

    @Override
    public Stream open(
            ActiveZlm runtime, String app, String id, URI source, PreviewDeadline deadline) {
        proxyConfig(runtime, deadline, false);
        return addProxy(runtime, app, id, source, deadline, false);
    }

    @Override
    public Stream openObservation(
            ActiveZlm runtime, String app, String id, URI source, PreviewDeadline deadline) {
        if (!app.matches("sf_preview_[a-f0-9]{12}") || !id.matches("[a-f0-9]{32}"))
            throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        proxyConfig(runtime, deadline, true);
        return addProxy(runtime, app, id, source, deadline, true);
    }

    @Override
    public Stream openViewer(Stream upstream, String app, String id, PreviewDeadline deadline) {
        if (!upstream.app().equals(app)
                || !app.matches("sf_preview_[a-f0-9]{12}")
                || !upstream.id().matches("[a-f0-9]{32}")
                || !upstream.proxyKey().equals(VHOST + "/" + app + "/" + upstream.id())
                || !id.matches("[a-f0-9]{32}")
                || upstream.id().equals(id))
            throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        String host = upstream.runtime().apiBase().getHost();
        if (!List.of("127.0.0.1", "localhost", "::1", "[::1]").contains(host))
            throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        var config = proxyConfig(upstream.runtime(), deadline, false);
        int port = config.path("rtsp.port").asInt(0);
        if (port < 1 || port > 65535) throw new PreviewFailure("ZLM_RTC_NOT_CONFIGURED");
        if (host.contains(":") && !host.startsWith("[")) host = "[" + host + "]";
        return addProxy(
                upstream.runtime(),
                app,
                id,
                URI.create("rtsp://" + host + ":" + port + "/" + app + "/" + upstream.id()),
                deadline,
                false);
    }

    private JsonNode proxyConfig(ActiveZlm runtime, PreviewDeadline deadline, boolean observation) {
        JsonNode configs = call(runtime, "getServerConfig", Map.of(), deadline).path("data");
        if (!configs.isArray()
                || configs.isEmpty()
                || !"0".equals(configs.get(0).path("rtsp.directProxy").asText())
                || !observation
                        && configs.get(0).path("general.streamNoneReaderDelayMS").asLong(0) < 20000)
            throw new PreviewFailure("ZLM_RTC_NOT_CONFIGURED");
        return configs.get(0);
    }

    private Stream addProxy(
            ActiveZlm runtime,
            String app,
            String id,
            URI source,
            PreviewDeadline deadline,
            boolean observation) {
        var params = tuple(app, id);
        params.put("url", source.toASCIIString());
        params.put("retry_count", "0");
        params.put("force", "0");
        params.put("timeout_sec", "8");
        if (observation) params.put("media_timeout_ms", "10000");
        params.put("rtp_type", "0");
        params.put("enable_rtsp", "1");
        params.put("enable_rtmp", "0");
        params.put("enable_hls", "0");
        params.put("enable_mp4", "0");
        params.put("enable_ts", "0");
        params.put("enable_fmp4", "0");
        params.put("enable_audio", "0");
        // The lease manager closes explicitly; this is also a crash-safety fallback.
        // Its verified grace period exceeds the whole 18s create budget, including local attach.
        params.put("auto_close", "1");
        String identity = runtime.apiBase() + "/" + app + "/" + id;
        if (uncertainOpens.size() >= 256) throw new PreviewFailure("PREVIEW_BUSY");
        uncertainOpens.put(identity, System.nanoTime() + TimeUnit.SECONDS.toNanos(30));
        JsonNode result = call(runtime, "addStreamProxy", params, deadline);
        String key = result.path("data").path("key").asText();
        if (!key.equals(VHOST + "/" + app + "/" + id))
            throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        uncertainOpens.remove(identity);
        return new Stream(runtime, app, id, key);
    }

    @Override
    public Transcode transcode(Stream source, String outputId) {
        return transcode(source, outputId, PreviewDeadline.after(Duration.ofSeconds(18)));
    }

    @Override
    public Transcode transcode(Stream source, String outputId, PreviewDeadline deadline) {
        if (!source.app().matches("sf_preview_[a-f0-9]{12}")
                || !source.id().matches("[a-f0-9]{32}")
                || !(source.id() + "h264").equals(outputId))
            throw new PreviewFailure("TRANSCODE_NOT_CONFIGURED");
        JsonNode configs =
                call(source.runtime(), "getServerConfig", Map.of(), deadline).path("data");
        if (!configs.isArray() || configs.isEmpty())
            throw new PreviewFailure("TRANSCODE_NOT_CONFIGURED");
        JsonNode config = configs.get(0);
        String command = config.path("ffmpeg.cmd").asText();
        String binary = config.path("ffmpeg.bin").asText();
        var selected = runtimes.activeFfmpeg();
        if (selected.isEmpty()
                || binary.isBlank()
                || !command.startsWith("%s ")
                || binary.chars().anyMatch(Character::isWhitespace)
                || !command.matches("(?s).*-(?:c:v|vcodec)\\s+libx264(?:\\s.*|$)"))
            throw new PreviewFailure("TRANSCODE_NOT_CONFIGURED");
        try {
            Path configured = Path.of(binary);
            if (!configured.isAbsolute())
                configured = source.runtime().executablePath().getParent().resolve(configured);
            if (!Files.isSameFile(configured, selected.get().executablePath()))
                throw new PreviewFailure("TRANSCODE_VERSION_MISMATCH");
        } catch (java.io.IOException | java.nio.file.InvalidPathException ex) {
            throw new PreviewFailure("TRANSCODE_NOT_CONFIGURED");
        }
        int rtsp = config.path("rtsp.port").asInt(0), rtmp = config.path("rtmp.port").asInt(0);
        if (rtsp < 1 || rtsp > 65535 || rtmp < 1 || rtmp > 65535)
            throw new PreviewFailure("TRANSCODE_NOT_CONFIGURED");
        String host = source.runtime().apiBase().getHost();
        if (host.contains(":") && !host.startsWith("[")) host = "[" + host + "]";
        // FFmpeg only receives a local random stream URL. Upstream credentials never enter its
        // command.
        String destination = "rtmp://" + host + ":" + rtmp + "/" + source.app() + "/" + outputId;
        Transcode reference;
        try {
            reference =
                    new Transcode(
                            outputId,
                            java.util.HexFormat.of()
                                    .formatHex(
                                            java.security.MessageDigest.getInstance("MD5")
                                                    .digest(
                                                            destination.getBytes(
                                                                    StandardCharsets.UTF_8))));
        } catch (java.security.NoSuchAlgorithmException ex) {
            throw new IllegalStateException("MD5 unavailable");
        }
        recover(source.runtime(), deadline);
        if (!processes.exited(source.runtime(), source.app(), source.id(), outputId, deadline))
            throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        registry.register(source, reference);
        try {
            var result =
                    call(
                            source.runtime(),
                            "addFFmpegSource",
                            Map.of(
                                    "src_url",
                                    "rtsp://"
                                            + host
                                            + ":"
                                            + rtsp
                                            + "/"
                                            + source.app()
                                            + "/"
                                            + source.id(),
                                    "dst_url",
                                    destination,
                                    "timeout_ms",
                                    "8000",
                                    "enable_hls",
                                    "0",
                                    "enable_mp4",
                                    "0"),
                            deadline);
            String key = result.path("data").path("key").asText();
            if (!key.equalsIgnoreCase(reference.key()))
                throw new PreviewFailure("ZLM_INVALID_RESPONSE");
            registry.started(reference.key());
            return reference;
        } catch (PreviewFailure failure) {
            throw new PreviewFailure(failure.reason(), reference);
        }
    }

    @Override
    public PreviewModels.Stats stats(Stream source, String outputId) {
        return stats(source, outputId, PreviewDeadline.after(Duration.ofSeconds(10)));
    }

    @Override
    public RawVideo observeRawVideo(Stream source, PreviewDeadline deadline) {
        var proxy =
                call(source.runtime(), "getProxyInfo", Map.of("key", source.proxyKey()), deadline)
                        .path("data");
        var media =
                call(source.runtime(), "getMediaList", tuple(source.app(), source.id()), deadline)
                        .path("data");
        if (!proxy.isObject()
                || !proxy.path("status").isIntegralNumber()
                || proxy.path("status").asInt(-1) != 0
                || !proxy.path("rePullCount").isIntegralNumber()
                || proxy.path("rePullCount").asLong(-1) != 0
                || !media.isArray()) throw new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT");
        var src = proxy.path("src");
        if (!source.app().equals(src.path("app").asText())
                || !source.id().equals(src.path("stream").asText())
                || !VHOST.equals(src.path("vhost").asText()))
            throw new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT");
        JsonNode rtsp = null;
        for (var item : media) {
            if (!"rtsp".equals(item.path("schema").asText())) continue;
            if (rtsp != null
                    || !source.app().equals(item.path("app").asText())
                    || !source.id().equals(item.path("stream").asText())
                    || !VHOST.equals(item.path("vhost").asText()))
                throw new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT");
            rtsp = item;
        }
        if (rtsp == null
                || !rtsp.path("createStamp").isIntegralNumber()
                || !rtsp.path("createStamp").canConvertToLong()
                || rtsp.path("createStamp").asLong(0) <= 0)
            throw new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT");
        JsonNode video = null;
        for (var track : proxy.path("tracks")) {
            if (track.path("codec_type").asInt(-1) != 0) continue;
            if (video != null) throw new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT");
            video = track;
        }
        if (video == null
                || !video.path("ready").asBoolean(false)
                || !video.path("frames").isIntegralNumber()
                || !video.path("frames").canConvertToLong()
                || video.path("frames").asLong(-1) < 0
                || !video.path("codec_id").isIntegralNumber()
                || video.path("codec_id_name").asText().isBlank()
                || video.path("width").asInt(0) < 1
                || video.path("height").asInt(0) < 1)
            throw new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT");
        String identity =
                video.path("codec_id").asInt()
                        + ":"
                        + video.path("codec_id_name").asText()
                        + ":"
                        + video.path("width").asInt()
                        + "x"
                        + video.path("height").asInt()
                        + (video.path("index").isIntegralNumber()
                                ? ":" + video.path("index").asLong()
                                : "");
        return new RawVideo(
                source.proxyKey() + ":" + rtsp.path("createStamp").asLong(),
                identity,
                video.path("frames").asLong(),
                Instant.now());
    }

    @Override
    public PreviewModels.Stats stats(Stream source, String outputId, PreviewDeadline deadline) {
        var params = tuple(source.app(), outputId);
        params.put("schema", "rtsp");
        var values = call(source.runtime(), "getMediaList", params, deadline).path("data");
        if (!values.isArray() || values.isEmpty())
            return new PreviewModels.Stats(
                    "UNAVAILABLE", null, null, null, null, null, null, Instant.now());
        JsonNode media = values.get(0), video = null;
        for (var track : media.path("tracks")) {
            if (track.path("codec_type").asInt(-1) == 0 && track.path("ready").asBoolean(false)) {
                video = track;
                break;
            }
        }
        if (video == null)
            return new PreviewModels.Stats(
                    "UNAVAILABLE", null, null, null, null, null, null, Instant.now());
        return new PreviewModels.Stats(
                "READY",
                normalizeCodec(video.path("codec_id_name").asText()),
                integer(video, "width"),
                integer(video, "height"),
                decimal(video, "fps"),
                media.has("bytesSpeed") ? media.path("bytesSpeed").asDouble() * 8 / 1000 : null,
                integer(media, "totalReaderCount"),
                Instant.now());
    }

    static String normalizeCodec(String codec) {
        return switch (codec.toUpperCase(java.util.Locale.ROOT).replace(".", "")) {
            case "H264", "AVC" -> "H264";
            case "H265", "HEVC" -> "H265";
            default -> codec.toUpperCase(java.util.Locale.ROOT);
        };
    }

    private static Integer integer(JsonNode value, String key) {
        return value.has(key) ? value.path(key).asInt() : null;
    }

    private static Double decimal(JsonNode value, String key) {
        return value.has(key) ? value.path(key).asDouble() : null;
    }

    @Override
    public PreviewModels.Answer answer(Stream source, String outputId, String sdp) {
        return answer(source, outputId, sdp, PreviewDeadline.after(Duration.ofSeconds(10)));
    }

    @Override
    public PreviewModels.Answer answer(
            Stream source, String outputId, String sdp, PreviewDeadline deadline) {
        var params = tuple(source.app(), outputId);
        params.put("type", "play");
        var result =
                request(source.runtime(), "webrtc", params, sdp, "application/sdp", true, deadline);
        if (!result.path("sdp").isTextual() || !"answer".equals(result.path("type").asText()))
            throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        return new PreviewModels.Answer(result.path("sdp").asText(), "answer");
    }

    @Override
    public void close(Stream source, Transcode output) {
        close(source, output, PreviewDeadline.after(Duration.ofSeconds(12)));
    }

    @Override
    public void closeRecovered(Stream source, PreviewDeadline deadline) {
        var outputs =
                registry.recoverable(source.runtime()).stream()
                        .filter(e -> e.app().equals(source.app()) && e.input().equals(source.id()))
                        .toList();
        if (outputs.isEmpty()) close(source, null, deadline);
        else
            for (var entry : outputs)
                close(source, new Transcode(entry.output(), entry.key()), deadline);
    }

    @Override
    public void close(Stream source, Transcode output, PreviewDeadline deadline) {
        if (!source.app().matches("sf_preview_[a-f0-9]{12}")
                || !source.id().matches("[a-f0-9]{32}")
                || !source.proxyKey().equals(VHOST + "/" + source.app() + "/" + source.id())
                || output != null
                        && (!output.id().equals(source.id() + "h264")
                                || !output.key().matches("[a-fA-F0-9]{32}")))
            throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        if (output != null) {
            registry.releasing(output.key());
            verifyTranscodeTarget(source, output, deadline);
        }
        RuntimeException failure = null;
        try {
            if (output != null)
                call(source.runtime(), "delFFmpegSource", Map.of("key", output.key()), deadline);
        } catch (RuntimeException ex) {
            cleanupFailed("delFFmpegSource", source, ex);
            failure = ex;
        }
        try {
            call(source.runtime(), "delStreamProxy", Map.of("key", source.proxyKey()), deadline);
        } catch (RuntimeException ex) {
            cleanupFailed("delStreamProxy", source, ex);
            if (failure == null) failure = ex;
        }
        try {
            var params = tuple(source.app(), source.id());
            params.put("force", "1");
            call(source.runtime(), "close_streams", params, deadline);
        } catch (RuntimeException ex) {
            cleanupFailed("closeSource", source, ex);
            if (failure == null) failure = ex;
        }
        if (output != null) {
            try {
                var params = tuple(source.app(), output.id());
                params.put("force", "1");
                call(source.runtime(), "close_streams", params, deadline);
            } catch (RuntimeException ex) {
                cleanupFailed("closeOutput", source, ex);
                if (failure == null) failure = ex;
            }
        }
        if (failure != null) throw new PreviewFailure("CLEANUP_PENDING");
        String identity = source.runtime().apiBase() + "/" + source.app() + "/" + source.id();
        Long notBefore = uncertainOpens.get(identity);
        if (notBefore != null && System.nanoTime() - notBefore < 0)
            throw new PreviewFailure("CLEANUP_PENDING");
        try {
            if (hasMedia(source, source.id(), deadline)
                    || output != null && hasMedia(source, output.id(), deadline))
                throw new PreviewFailure("CLEANUP_PENDING");
        } catch (RuntimeException pending) {
            throw new PreviewFailure("CLEANUP_PENDING");
        }
        uncertainOpens.remove(identity);
        if (output != null) {
            if (!registry.canConfirmExit(output.key())) throw new PreviewFailure("CLEANUP_PENDING");
            try {
                if (ffmpegKeys(source.runtime(), deadline).contains(output.key())
                        || !processes.exited(
                                source.runtime(), source.app(), source.id(), output.id(), deadline))
                    throw new PreviewFailure("CLEANUP_PENDING");
                registry.exited(output.key());
            } catch (RuntimeException pending) {
                throw new PreviewFailure("CLEANUP_PENDING");
            }
        }
    }

    private static void cleanupFailed(String action, Stream source, RuntimeException failure) {
        LOG.warn(
                "Preview media cleanup pending action={} app={} streamId={} reasonCode={}",
                action,
                source.app(),
                source.id(),
                failure instanceof PreviewFailure known
                        ? known.reason()
                        : "DEPENDENCY_UNAVAILABLE");
    }

    private boolean hasMedia(Stream source, String id, PreviewDeadline deadline) {
        JsonNode values =
                call(source.runtime(), "getMediaList", tuple(source.app(), id), deadline)
                        .path("data");
        if (values.isMissingNode() || values.isNull()) return false;
        if (!values.isArray()) throw new PreviewFailure("CLEANUP_PENDING");
        return !values.isEmpty();
    }

    @Override
    public boolean exists(Stream stream, PreviewDeadline deadline) {
        var params = tuple(stream.app(), stream.id());
        params.put("schema", "rtsp");
        // This deployed ZLM omits getMediaList.data for an empty result. Use its explicit
        // presence flag instead of treating missing JSON as either absence or corruption.
        var online = call(stream.runtime(), "isMediaOnline", params, deadline).path("online");
        if (!online.isBoolean()) throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        return online.booleanValue();
    }

    private void verifyTranscodeTarget(Stream source, Transcode output, PreviewDeadline deadline) {
        JsonNode jobs = call(source.runtime(), "listFFmpegSource", Map.of(), deadline).path("data");
        if (jobs.isNull() || jobs.isMissingNode()) return;
        if (!jobs.isArray()) throw new PreviewFailure("CLEANUP_PENDING");
        for (JsonNode job : jobs) {
            if (!output.key().equalsIgnoreCase(job.path("key").asText())) continue;
            if (!localTuple(job.path("dst_url").asText(), "rtmp", source.app(), output.id())
                    || !localTuple(job.path("src_url").asText(), "rtsp", source.app(), source.id()))
                throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        }
    }

    private static boolean localTuple(String value, String scheme, String app, String id) {
        try {
            URI uri = URI.create(value);
            String host = uri.getHost();
            return scheme.equalsIgnoreCase(uri.getScheme())
                    && ("127.0.0.1".equals(host)
                            || "localhost".equalsIgnoreCase(host)
                            || "::1".equals(host)
                            || "[::1]".equals(host))
                    && ("/" + app + "/" + id).equals(uri.getRawPath());
        } catch (IllegalArgumentException invalid) {
            return false;
        }
    }

    private void recover(ActiveZlm runtime, PreviewDeadline deadline) {
        Duration available = deadline.remaining().minus(MIN_START_REMAINING);
        if (available.isNegative() || available.isZero()) return;
        var pending = registry.nextRecoverable(runtime);
        if (pending.isEmpty()) return;
        var entry = pending.get();
        var stream =
                new Stream(
                        runtime,
                        entry.app(),
                        entry.input(),
                        VHOST + "/" + entry.app() + "/" + entry.input());
        try {
            close(
                    stream,
                    new Transcode(entry.output(), entry.key()),
                    deadline.within(
                            available.compareTo(RECOVERY_BUDGET) < 0
                                    ? available
                                    : RECOVERY_BUDGET));
        } catch (PreviewFailure stillPending) {
            deadline.check();
        }
    }

    private java.util.Set<String> ffmpegKeys(ActiveZlm runtime, PreviewDeadline deadline) {
        var result = call(runtime, "listFFmpegSource", Map.of(), deadline);
        JsonNode values = result.path("data");
        if (values.isMissingNode() || values.isNull()) return new java.util.HashSet<>();
        if (!values.isArray()) throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        var keys = new java.util.HashSet<String>();
        for (JsonNode value : values) {
            String key = value.path("key").asText();
            if (!key.matches("[a-fA-F0-9]{32}"))
                throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
            keys.add(key.toLowerCase(java.util.Locale.ROOT));
        }
        return keys;
    }

    private static LinkedHashMap<String, String> tuple(String app, String stream) {
        return new LinkedHashMap<>(Map.of("vhost", VHOST, "app", app, "stream", stream));
    }

    private JsonNode call(
            ActiveZlm runtime, String api, Map<String, String> values, PreviewDeadline deadline) {
        var params = new LinkedHashMap<>(values);
        params.put("secret", runtime.secret());
        return request(
                runtime,
                api,
                Map.of(),
                form(params),
                "application/x-www-form-urlencoded",
                false,
                deadline);
    }

    private JsonNode request(
            ActiveZlm runtime,
            String api,
            Map<String, String> query,
            String body,
            String contentType,
            boolean signal,
            PreviewDeadline deadline) {
        URI endpoint =
                runtime.apiBase()
                        .resolve("/index/api/" + api + (query.isEmpty() ? "" : "?" + form(query)));
        Duration remaining = deadline.remaining();
        Duration timeout =
                remaining.compareTo(Duration.ofSeconds(12)) < 0
                        ? remaining
                        : Duration.ofSeconds(12);
        HttpRequest request =
                HttpRequest.newBuilder(endpoint)
                        .timeout(timeout)
                        .header("Content-Type", contentType)
                        .POST(HttpRequest.BodyPublishers.ofString(body, StandardCharsets.UTF_8))
                        .build();
        var future = http.sendAsync(request, ignored -> new LimitedBody());
        try {
            HttpResponse<byte[]> response;
            while (true) {
                long slice =
                        Math.min(
                                TimeUnit.MILLISECONDS.toNanos(100), deadline.remaining().toNanos());
                try {
                    response = future.get(slice, TimeUnit.NANOSECONDS);
                    break;
                } catch (java.util.concurrent.TimeoutException waiting) {
                    deadline.check();
                }
            }
            if (response.statusCode() != 200) throw new PreviewFailure("ZLM_UNAVAILABLE");
            JsonNode value = json.readTree(response.body());
            if (value == null || !value.has("code") || value.path("code").asInt(-1) != 0) {
                LOG.warn(
                        "Preview media request rejected action={} responseCode={}",
                        api,
                        value == null ? -1 : value.path("code").asInt(-1));
                throw new PreviewFailure(signal ? "SIGNAL_REJECTED" : "ZLM_REQUEST_FAILED");
            }
            return value;
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (java.util.concurrent.ExecutionException | java.io.IOException ex) {
            deadline.check();
            if (ex instanceof java.net.http.HttpTimeoutException
                    || ex.getCause() instanceof java.net.http.HttpTimeoutException)
                throw new PreviewFailure("PREVIEW_TIMEOUT");
            throw new PreviewFailure("ZLM_UNAVAILABLE");
        } finally {
            future.cancel(true);
        }
    }

    private static String form(Map<String, String> params) {
        return params.entrySet().stream()
                .map(
                        e ->
                                URLEncoder.encode(e.getKey(), StandardCharsets.UTF_8)
                                        + "="
                                        + URLEncoder.encode(e.getValue(), StandardCharsets.UTF_8))
                .collect(java.util.stream.Collectors.joining("&"));
    }

    /** Cancels transfer before accumulating an unbounded response, including non-JSON bodies. */
    private static final class LimitedBody implements HttpResponse.BodySubscriber<byte[]> {
        private final CompletableFuture<byte[]> result = new CompletableFuture<>();
        private final ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        private Flow.Subscription subscription;

        @Override
        public CompletionStage<byte[]> getBody() {
            return result;
        }

        @Override
        public void onSubscribe(Flow.Subscription subscription) {
            this.subscription = subscription;
            subscription.request(1);
        }

        @Override
        public void onNext(List<ByteBuffer> buffers) {
            for (var buffer : buffers) {
                if (bytes.size() + buffer.remaining() > 1_048_576) {
                    subscription.cancel();
                    result.completeExceptionally(new PreviewFailure("ZLM_RESPONSE_TOO_LARGE"));
                    return;
                }
                byte[] chunk = new byte[buffer.remaining()];
                buffer.get(chunk);
                bytes.writeBytes(chunk);
            }
            subscription.request(1);
        }

        @Override
        public void onError(Throwable throwable) {
            result.completeExceptionally(throwable);
        }

        @Override
        public void onComplete() {
            result.complete(bytes.toByteArray());
        }
    }
}
