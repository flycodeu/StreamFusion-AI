package com.streamfusion.platform.media.preview.node;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewModels;
import com.streamfusion.platform.media.preview.PreviewPlaybackPlanner;
import java.io.ByteArrayOutputStream;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.LinkOption;
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

/** Bounded HTTP control of a dedicated private-network ZLM; camera bytes never cross Platform. */
@Component
public final class ZlmDirectGateway {
    private static final String VHOST = "__defaultVhost__";
    private static final int MAX_OBSERVED_THREADS = 256;
    private final HttpClient http =
            HttpClient.newBuilder()
                    .connectTimeout(Duration.ofSeconds(3))
                    .followRedirects(HttpClient.Redirect.NEVER)
                    .build();
    private final ObjectMapper json;

    public ZlmDirectGateway(ObjectMapper json) {
        this.json = json;
    }

    public record Stream(String app, String id) {
        public Stream {
            if (app == null
                    || !app.matches("sf_preview_[a-f0-9]{12}")
                    || id == null
                    || !id.matches("[a-f0-9]{32}"))
                throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        }

        String proxyKey() {
            return VHOST + "/" + app + "/" + id;
        }
    }

    public void requireReady(ZlmDirectConnection connection, PreviewDeadline deadline) {
        JsonNode configs = call(connection, "getServerConfig", Map.of(), deadline).path("data");
        if (!configs.isArray() || configs.isEmpty())
            throw new PreviewFailure("NODE_MEDIA_CONFIGURATION_INVALID");
        JsonNode config = configs.get(0);
        if (!"0".equals(config.path("rtsp.directProxy").asText())
                || config.path("general.streamNoneReaderDelayMS").asLong(0) < 20000
                || config.path("rtc.port").asInt(0) < 1
                || !connection.mediaAddress().equals(config.path("rtc.externIP").asText())
                || !"0".equals(config.path("api.apiDebug").asText())
                || config.path("http.allow_ip_range").asText().isBlank()
                || config.path("http.allow_ip_range").asText().contains("0.0.0.0")
                || config.path("http.allow_ip_range").asText().contains("*"))
            throw new PreviewFailure("NODE_MEDIA_CONFIGURATION_INVALID");
    }

    public String version(ZlmDirectConnection connection, PreviewDeadline deadline) {
        JsonNode value = call(connection, "version", Map.of(), deadline).path("data");
        String version = value.path("version").asText("");
        if (version.isBlank()) {
            String commit = value.path("commitHash").asText("");
            String build = value.path("buildTime").asText("");
            version = commit + (build.isBlank() ? "" : " (" + build + ")");
        }
        if (version.isBlank()
                || version.length() > 256
                || version.codePoints().anyMatch(Character::isISOControl))
            throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        return version;
    }

    /** ZLM epoll thread load is an observation, not host CPU or scheduling capacity. */
    public record NetworkThreadLoad(
            int networkThreadCount,
            double averageNetworkThreadLoadPercent,
            double peakNetworkThreadLoadPercent,
            Instant observedAt) {}

    public NetworkThreadLoad networkThreadLoad(
            ZlmDirectConnection connection, PreviewDeadline deadline) {
        JsonNode threads = call(connection, "getThreadsLoad", Map.of(), deadline).path("data");
        if (!threads.isArray() || threads.isEmpty() || threads.size() > MAX_OBSERVED_THREADS)
            throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        double total = 0;
        double peak = 0;
        for (JsonNode thread : threads) {
            JsonNode value = thread.path("load");
            if (!value.isNumber()) throw new PreviewFailure("ZLM_INVALID_RESPONSE");
            double load = value.asDouble();
            if (!Double.isFinite(load) || load < 0 || load > 100)
                throw new PreviewFailure("ZLM_INVALID_RESPONSE");
            total += load;
            peak = Math.max(peak, load);
        }
        // Floating-point summation can put the mean a fraction above an identical peak.
        return new NetworkThreadLoad(
                threads.size(), Math.min(peak, total / threads.size()), peak, Instant.now());
    }

    public void open(
            ZlmDirectConnection connection, Stream stream, URI source, PreviewDeadline deadline) {
        if (source == null
                || source.getScheme() == null
                || !List.of("rtsp", "rtsps")
                        .contains(source.getScheme().toLowerCase(java.util.Locale.ROOT)))
            throw new PreviewFailure("SOURCE_UNSUPPORTED");
        var params = tuple(stream);
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
        String key =
                call(connection, "addStreamProxy", params, deadline)
                        .path("data")
                        .path("key")
                        .asText();
        if (!stream.proxyKey().equals(key)) throw new PreviewFailure("ZLM_INVALID_RESPONSE");
    }

    public void openViewer(
            ZlmDirectConnection connection,
            Stream source,
            Stream viewer,
            PreviewDeadline deadline) {
        if (!source.app().equals(viewer.app()) || source.id().equals(viewer.id()))
            throw new PreviewFailure("CLEANUP_DESTINATION_REJECTED");
        JsonNode configs = call(connection, "getServerConfig", Map.of(), deadline).path("data");
        int rtspPort =
                configs.isArray() && !configs.isEmpty()
                        ? configs.get(0).path("rtsp.port").asInt(0)
                        : 0;
        if (rtspPort < 1 || rtspPort > 65535) throw new PreviewFailure("ZLM_RTC_NOT_CONFIGURED");
        open(
                connection,
                viewer,
                URI.create("rtsp://127.0.0.1:" + rtspPort + "/" + source.app() + "/" + source.id()),
                deadline);
    }

    public PreviewModels.Stats stats(
            ZlmDirectConnection connection, Stream stream, PreviewDeadline deadline) {
        var params = tuple(stream);
        params.put("schema", "rtsp");
        JsonNode values = call(connection, "getMediaList", params, deadline).path("data");
        if (!values.isArray() || values.isEmpty()) return unavailable();
        JsonNode media = values.get(0);
        for (JsonNode track : media.path("tracks")) {
            if (track.path("codec_type").asInt(-1) != 0 || !track.path("ready").asBoolean(false))
                continue;
            return new PreviewModels.Stats(
                    "READY",
                    PreviewPlaybackPlanner.normalize(track.path("codec_id_name").asText()),
                    integer(track, "width"),
                    integer(track, "height"),
                    decimal(track, "fps"),
                    media.has("bytesSpeed") ? media.path("bytesSpeed").asDouble() * 8 / 1000 : null,
                    integer(media, "totalReaderCount"),
                    Instant.now());
        }
        return unavailable();
    }

    public boolean exists(ZlmDirectConnection connection, Stream stream, PreviewDeadline deadline) {
        var params = tuple(stream);
        params.put("schema", "rtsp");
        JsonNode online = call(connection, "isMediaOnline", params, deadline).path("online");
        if (!online.isBoolean()) throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        return online.booleanValue();
    }

    public PreviewModels.Answer answer(
            ZlmDirectConnection connection, Stream stream, String sdp, PreviewDeadline deadline) {
        var query = tuple(stream);
        query.put("type", "play");
        query.put("secret", secret(connection));
        JsonNode value = request(connection, "webrtc", query, sdp, "application/sdp", deadline);
        if (!"answer".equals(value.path("type").asText())
                || !value.path("sdp").asText().startsWith("v=0")
                || value.path("sdp").asText().length() > 65536)
            throw new PreviewFailure("ZLM_INVALID_RESPONSE");
        return new PreviewModels.Answer(value.path("sdp").asText(), "answer");
    }

    /** The exact tuple is deterministic and was recorded before open. Absence must be observed. */
    public void close(ZlmDirectConnection connection, Stream stream, PreviewDeadline deadline) {
        try {
            call(connection, "delStreamProxy", Map.of("key", stream.proxyKey()), deadline);
        } catch (RuntimeException ignored) {
            // Exact absence is checked below.
        }
        try {
            var params = tuple(stream);
            params.put("force", "1");
            call(connection, "close_streams", params, deadline);
        } catch (RuntimeException ignored) {
            // Exact absence is checked below.
        }
        // A missing proxy can make deletion return a nonzero ZLM code. Exact absence is the
        // authoritative cleanup result; an unknown open is guarded by the caller's journal.
        if (exists(connection, stream, deadline)) throw new PreviewFailure("CLEANUP_PENDING");
    }

    private static PreviewModels.Stats unavailable() {
        return new PreviewModels.Stats(
                "UNAVAILABLE", null, null, null, null, null, null, Instant.now());
    }

    private static Integer integer(JsonNode node, String key) {
        return node.has(key) ? node.path(key).asInt() : null;
    }

    private static Double decimal(JsonNode node, String key) {
        return node.has(key) ? node.path(key).asDouble() : null;
    }

    private static LinkedHashMap<String, String> tuple(Stream stream) {
        return new LinkedHashMap<>(
                Map.of("vhost", VHOST, "app", stream.app(), "stream", stream.id()));
    }

    private JsonNode call(
            ZlmDirectConnection connection,
            String action,
            Map<String, String> values,
            PreviewDeadline deadline) {
        var body = new LinkedHashMap<>(values);
        body.put("secret", secret(connection));
        return request(
                connection,
                action,
                Map.of(),
                form(body),
                "application/x-www-form-urlencoded",
                deadline);
    }

    private JsonNode request(
            ZlmDirectConnection connection,
            String action,
            Map<String, String> query,
            String body,
            String type,
            PreviewDeadline deadline) {
        URI uri =
                connection
                        .apiUri()
                        .resolve(
                                "/index/api/"
                                        + action
                                        + (query.isEmpty() ? "" : "?" + form(query)));
        Duration remaining = deadline.remaining();
        Duration timeout =
                remaining.compareTo(Duration.ofSeconds(12)) < 0
                        ? remaining
                        : Duration.ofSeconds(12);
        var request =
                HttpRequest.newBuilder(uri)
                        .timeout(timeout)
                        .header("Content-Type", type)
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
            if (value == null || !value.has("code") || value.path("code").asInt(-1) != 0)
                throw new PreviewFailure("ZLM_REQUEST_FAILED");
            return value;
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (java.util.concurrent.ExecutionException | java.io.IOException ex) {
            deadline.check();
            throw new PreviewFailure("ZLM_UNAVAILABLE");
        } finally {
            future.cancel(true);
        }
    }

    private static String form(Map<String, String> values) {
        return values.entrySet().stream()
                .map(
                        entry ->
                                URLEncoder.encode(entry.getKey(), StandardCharsets.UTF_8)
                                        + "="
                                        + URLEncoder.encode(
                                                entry.getValue(), StandardCharsets.UTF_8))
                .collect(java.util.stream.Collectors.joining("&"));
    }

    private static String secret(ZlmDirectConnection connection) {
        try {
            var path = connection.secretFile();
            if (!Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS) || Files.size(path) > 512)
                throw new IllegalArgumentException();
            String value = Files.readString(path, StandardCharsets.UTF_8);
            if (value.isEmpty()
                    || value.length() > 256
                    || value.charAt(0) == ' '
                    || value.charAt(value.length() - 1) == ' '
                    || value.chars().anyMatch(ch -> ch < 32 || ch > 126))
                throw new IllegalArgumentException();
            return value;
        } catch (Exception invalid) {
            throw new PreviewFailure("NODE_CREDENTIAL_UNAVAILABLE");
        }
    }

    private static final class LimitedBody implements HttpResponse.BodySubscriber<byte[]> {
        private final CompletableFuture<byte[]> result = new CompletableFuture<>();
        private final ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        private Flow.Subscription subscription;

        public CompletionStage<byte[]> getBody() {
            return result;
        }

        public void onSubscribe(Flow.Subscription value) {
            subscription = value;
            subscription.request(1);
        }

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

        public void onError(Throwable error) {
            result.completeExceptionally(error);
        }

        public void onComplete() {
            result.complete(bytes.toByteArray());
        }
    }
}
