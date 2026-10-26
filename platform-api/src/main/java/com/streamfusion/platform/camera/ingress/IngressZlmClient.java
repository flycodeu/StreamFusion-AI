package com.streamfusion.platform.camera.ingress;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.io.ByteArrayOutputStream;
import java.net.*;
import java.net.http.*;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import org.springframework.stereotype.Component;

/** Only the deployment-selected loopback ZLM; callers cannot supply an API endpoint or key. */
@Component
public final class IngressZlmClient {
    private final ObjectMapper json;
    private final HttpClient http =
            HttpClient.newBuilder()
                    .connectTimeout(Duration.ofSeconds(2))
                    .followRedirects(HttpClient.Redirect.NEVER)
                    .build();

    public IngressZlmClient(ObjectMapper json) {
        this.json = json;
    }

    JsonNode call(
            ActiveZlm runtime, String api, Map<String, String> values, PreviewDeadline deadline) {
        if (!Set.of("127.0.0.1", "localhost", "::1", "[::1]").contains(runtime.apiBase().getHost()))
            throw new PreviewFailure("INGRESS_REQUIRES_LOCAL_ZLM");
        var params = new LinkedHashMap<>(values);
        params.put("secret", runtime.secret());
        String form =
                params.entrySet().stream()
                        .map(
                                e ->
                                        URLEncoder.encode(e.getKey(), StandardCharsets.UTF_8)
                                                + "="
                                                + URLEncoder.encode(
                                                        e.getValue(), StandardCharsets.UTF_8))
                        .collect(java.util.stream.Collectors.joining("&"));
        var request =
                HttpRequest.newBuilder(runtime.apiBase().resolve("/index/api/" + api))
                        .timeout(
                                deadline.remaining().compareTo(Duration.ofSeconds(3)) < 0
                                        ? deadline.remaining()
                                        : Duration.ofSeconds(3))
                        .header("Content-Type", "application/x-www-form-urlencoded")
                        .POST(HttpRequest.BodyPublishers.ofString(form))
                        .build();
        var future = http.sendAsync(request, ignored -> new Limited());
        try {
            var response =
                    future.get(
                            Math.min(deadline.remaining().toNanos(), TimeUnit.SECONDS.toNanos(3)),
                            TimeUnit.NANOSECONDS);
            if (response.statusCode() != 200) throw new PreviewFailure("INGRESS_ZLM_UNAVAILABLE");
            var result = json.readTree(response.body());
            if (result == null || result.path("code").asInt(-1) != 0)
                throw new PreviewFailure("INGRESS_ZLM_REJECTED");
            return result;
        } catch (InterruptedException failure) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (ExecutionException | TimeoutException | java.io.IOException failure) {
            throw new PreviewFailure("INGRESS_ZLM_UNAVAILABLE");
        } finally {
            future.cancel(true);
        }
    }

    JsonNode configuration(ActiveZlm runtime, PreviewDeadline deadline) {
        var data = call(runtime, "getServerConfig", Map.of(), deadline).path("data");
        if (!data.isArray() || data.isEmpty()) throw new PreviewFailure("INGRESS_ZLM_UNAVAILABLE");
        return data.get(0);
    }

    void close(ActiveZlm runtime, GbRtpJournal.Entry entry, PreviewDeadline deadline) {
        if (!entry.valid() || !entry.matches(runtime))
            throw new PreviewFailure("GB_RECOVERY_REQUIRED");
        var tuple =
                Map.of("vhost", "__defaultVhost__", "app", entry.app(), "stream_id", entry.id());
        call(runtime, "closeRtpServer", tuple, deadline);
        call(
                runtime,
                "close_streams",
                Map.of(
                        "vhost",
                        "__defaultVhost__",
                        "app",
                        entry.app(),
                        "stream",
                        entry.id(),
                        "force",
                        "1"),
                deadline);
        var receivers = call(runtime, "listRtpServer", Map.of(), deadline).path("data");
        if (!receivers.isArray() && !receivers.isNull() && !receivers.isMissingNode())
            throw new PreviewFailure("GB_RECOVERY_REQUIRED");
        if (receivers.isArray())
            for (var receiver : receivers)
                if (entry.id().equals(receiver.path("stream_id").asText()))
                    throw new PreviewFailure("GB_RECOVERY_REQUIRED");
    }

    private static final class Limited implements HttpResponse.BodySubscriber<byte[]> {
        private final CompletableFuture<byte[]> result = new CompletableFuture<>();
        private final ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        private Flow.Subscription subscription;

        public CompletionStage<byte[]> getBody() {
            return result;
        }

        public void onSubscribe(Flow.Subscription value) {
            subscription = value;
            value.request(1);
        }

        public void onNext(List<ByteBuffer> chunks) {
            for (var chunk : chunks) {
                if (bytes.size() + chunk.remaining() > 1048576) {
                    subscription.cancel();
                    result.completeExceptionally(new PreviewFailure("INGRESS_ZLM_UNAVAILABLE"));
                    return;
                }
                byte[] copy = new byte[chunk.remaining()];
                chunk.get(copy);
                bytes.writeBytes(copy);
            }
            subscription.request(1);
        }

        public void onError(Throwable failure) {
            result.completeExceptionally(failure);
        }

        public void onComplete() {
            result.complete(bytes.toByteArray());
        }
    }
}
