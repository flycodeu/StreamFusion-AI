package com.streamfusion.platform.preview.media;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import com.streamfusion.platform.preview.PreviewFailure;
import com.streamfusion.platform.preview.PreviewModels;
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
import org.springframework.stereotype.Component;

@Component
public class ZlmPreviewGateway implements PreviewMediaGateway {
    private static final String VHOST = "__defaultVhost__";
    private final HttpClient http =
            HttpClient.newBuilder()
                    .connectTimeout(Duration.ofSeconds(3))
                    .followRedirects(HttpClient.Redirect.NEVER)
                    .build();
    private final ObjectMapper json;
    private final MediaRuntimeService runtimes;

    public ZlmPreviewGateway(ObjectMapper json, MediaRuntimeService runtimes) {
        this.json = json;
        this.runtimes = runtimes;
    }

    @Override
    public Stream open(ActiveZlm runtime, String app, String id, URI source) {
        JsonNode configs = call(runtime, "getServerConfig", Map.of()).path("data");
        if (!configs.isArray()
                || configs.isEmpty()
                || !"0".equals(configs.get(0).path("rtsp.directProxy").asText()))
            throw new PreviewFailure("ZLM_RTC_NOT_CONFIGURED");
        var params = tuple(app, id);
        params.put("url", source.toASCIIString());
        params.put("retry_count", "0");
        params.put("force", "0");
        params.put("timeout_sec", "8");
        params.put("rtp_type", "0");
        params.put("enable_rtsp", "1");
        params.put("enable_rtmp", "0");
        params.put("enable_hls", "0");
        params.put("enable_mp4", "0");
        params.put("enable_ts", "0");
        params.put("enable_fmp4", "0");
        params.put("enable_audio", "0");
        params.put("auto_close", "1");
        JsonNode result = call(runtime, "addStreamProxy", params);
        String key = result.path("data").path("key").asText();
        if (!key.equals(VHOST + "/" + app + "/" + id))
            throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        return new Stream(runtime, app, id, key);
    }

    @Override
    public Transcode transcode(Stream source, String outputId) {
        JsonNode configs = call(source.runtime(), "getServerConfig", Map.of()).path("data");
        if (!configs.isArray() || configs.isEmpty())
            throw new PreviewFailure("TRANSCODE_NOT_CONFIGURED");
        JsonNode config = configs.get(0);
        String command = config.path("ffmpeg.cmd").asText();
        String binary = config.path("ffmpeg.bin").asText();
        var selected = runtimes.activeFfmpeg();
        if (selected.isEmpty()
                || binary.isBlank()
                || !command.startsWith("%s ")
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
                                    "0"));
            String key = result.path("data").path("key").asText();
            if (!key.equalsIgnoreCase(reference.key()))
                throw new PreviewFailure("ZLM_INVALID_RESPONSE");
            return reference;
        } catch (PreviewFailure failure) {
            throw new PreviewFailure(failure.reason(), reference);
        }
    }

    @Override
    public PreviewModels.Stats stats(Stream source, String outputId) {
        var params = tuple(source.app(), outputId);
        params.put("schema", "rtsp");
        var values = call(source.runtime(), "getMediaList", params).path("data");
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
        var params = tuple(source.app(), outputId);
        params.put("type", "play");
        var result = request(source.runtime(), "webrtc", params, sdp, "application/sdp", true);
        if (!result.path("sdp").isTextual() || !"answer".equals(result.path("type").asText()))
            throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        return new PreviewModels.Answer(result.path("sdp").asText(), "answer");
    }

    @Override
    public void close(Stream source, Transcode output) {
        if (!source.app().matches("sf_preview_[a-f0-9]{12}")
                || !source.id().matches("[a-f0-9]{32}")
                || !source.proxyKey().equals(VHOST + "/" + source.app() + "/" + source.id())
                || output != null
                        && (!output.id().equals(source.id() + "h264")
                                || !output.key().matches("[a-fA-F0-9]{32}")))
            throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        RuntimeException failure = null;
        try {
            if (output != null)
                call(source.runtime(), "delFFmpegSource", Map.of("key", output.key()));
        } catch (RuntimeException ex) {
            failure = ex;
        }
        try {
            call(source.runtime(), "delStreamProxy", Map.of("key", source.proxyKey()));
        } catch (RuntimeException ex) {
            if (failure == null) failure = ex;
        }
        try {
            var params = tuple(source.app(), source.id());
            params.put("force", "1");
            call(source.runtime(), "close_streams", params);
        } catch (RuntimeException ex) {
            if (failure == null) failure = ex;
        }
        if (output != null) {
            try {
                var params = tuple(source.app(), output.id());
                params.put("force", "1");
                call(source.runtime(), "close_streams", params);
            } catch (RuntimeException ex) {
                if (failure == null) failure = ex;
            }
        }
        if (failure != null) throw new PreviewFailure("CLEANUP_PENDING");
    }

    private static LinkedHashMap<String, String> tuple(String app, String stream) {
        return new LinkedHashMap<>(Map.of("vhost", VHOST, "app", app, "stream", stream));
    }

    private JsonNode call(ActiveZlm runtime, String api, Map<String, String> values) {
        var params = new LinkedHashMap<>(values);
        params.put("secret", runtime.secret());
        return request(
                runtime, api, Map.of(), form(params), "application/x-www-form-urlencoded", false);
    }

    private JsonNode request(
            ActiveZlm runtime,
            String api,
            Map<String, String> query,
            String body,
            String contentType,
            boolean signal) {
        URI endpoint =
                runtime.apiBase()
                        .resolve("/index/api/" + api + (query.isEmpty() ? "" : "?" + form(query)));
        HttpRequest request =
                HttpRequest.newBuilder(endpoint)
                        .timeout(Duration.ofSeconds(12))
                        .header("Content-Type", contentType)
                        .POST(HttpRequest.BodyPublishers.ofString(body, StandardCharsets.UTF_8))
                        .build();
        var future = http.sendAsync(request, ignored -> new LimitedBody());
        try {
            var response = future.get(13, TimeUnit.SECONDS);
            if (response.statusCode() != 200) throw new PreviewFailure("ZLM_UNAVAILABLE");
            JsonNode value = json.readTree(response.body());
            if (value == null || !value.has("code") || value.path("code").asInt(-1) != 0)
                throw new PreviewFailure(signal ? "SIGNAL_REJECTED" : "ZLM_REQUEST_FAILED");
            return value;
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (java.util.concurrent.ExecutionException
                | java.util.concurrent.TimeoutException
                | java.io.IOException ex) {
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
