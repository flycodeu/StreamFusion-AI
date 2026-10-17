package com.streamfusion.platform.media.preview.node;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import com.streamfusion.platform.media.preview.control.PreviewControlTls;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.Path;
import java.time.Duration;

/** Control traffic only. No redirects, finite deadlines, pinned TLS and a bounded response body. */
public final class PreviewNodeClient implements AutoCloseable {
    private final URI base;
    private final Path tokenFile;
    private final ObjectMapper json;
    private final HttpClient http;

    public PreviewNodeClient(
            URI base, String certificateSha256, Path tokenFile, ObjectMapper json) {
        if (base == null
                || !"https".equals(base.getScheme())
                || base.getHost() == null
                || base.getUserInfo() != null
                || base.getQuery() != null
                || base.getFragment() != null
                || !base.getRawPath().isEmpty())
            throw new IllegalArgumentException("Node URL must be an HTTPS origin");
        this.base = base;
        this.tokenFile = tokenFile;
        this.json = json;
        http =
                HttpClient.newBuilder()
                        .sslContext(PreviewControlTls.client(certificateSha256))
                        .connectTimeout(Duration.ofSeconds(3))
                        .followRedirects(HttpClient.Redirect.NEVER)
                        .build();
    }

    public <T> T get(String path, Class<T> type, PreviewDeadline deadline) {
        return send(path, null, type, deadline);
    }

    public PreviewControl.Receipt apply(PreviewControl.Command command, PreviewDeadline deadline) {
        return send("operations", command, PreviewControl.Receipt.class, deadline);
    }

    public com.streamfusion.platform.media.preview.control.PreviewMaintenance.State maintain(
            com.streamfusion.platform.media.preview.control.PreviewMaintenance.Command command,
            PreviewDeadline deadline) {
        return send(
                "maintenance",
                command,
                com.streamfusion.platform.media.preview.control.PreviewMaintenance.State.class,
                deadline);
    }

    private <T> T send(String path, Object body, Class<T> type, PreviewDeadline deadline) {
        if (!path.matches("[a-z0-9/-]{1,100}"))
            throw new IllegalArgumentException("Invalid control path");
        try {
            var request =
                    HttpRequest.newBuilder(base.resolve("/preview-control/v1/" + path))
                            .timeout(deadline.remaining())
                            .header(
                                    "Authorization",
                                    "Bearer " + PreviewControlTls.secret(tokenFile))
                            .header("Accept", "application/json");
            if (body == null) request.GET();
            else
                request.header("Content-Type", "application/json")
                        .POST(HttpRequest.BodyPublishers.ofByteArray(json.writeValueAsBytes(body)));
            // The subscriber refuses oversized chunks before buffering the whole response.
            var response = http.send(request.build(), limitedBody());
            deadline.check();
            if (response.statusCode() != 200) {
                var reason = json.readTree(response.body()).path("reasonCode").asText();
                throw new PreviewFailure(
                        reason.matches("[A-Z][A-Z0-9_]{0,79}") ? reason : "NODE_UNAVAILABLE");
            }
            return json.readValue(response.body(), type);
        } catch (PreviewFailure known) {
            throw known;
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (Exception ex) {
            throw new PreviewFailure("NODE_UNREACHABLE");
        }
    }

    private static HttpResponse.BodyHandler<byte[]> limitedBody() {
        return info ->
                new HttpResponse.BodySubscriber<>() {
                    private final java.io.ByteArrayOutputStream bytes =
                            new java.io.ByteArrayOutputStream();
                    private final java.util.concurrent.CompletableFuture<byte[]> result =
                            new java.util.concurrent.CompletableFuture<>();
                    private java.util.concurrent.Flow.Subscription subscription;

                    public java.util.concurrent.CompletionStage<byte[]> getBody() {
                        return result;
                    }

                    public void onSubscribe(java.util.concurrent.Flow.Subscription value) {
                        subscription = value;
                        value.request(1);
                    }

                    public void onNext(java.util.List<java.nio.ByteBuffer> values) {
                        for (var value : values) {
                            if (bytes.size() + value.remaining() > PreviewControl.MAX_BODY) {
                                subscription.cancel();
                                result.completeExceptionally(
                                        new IllegalStateException("Control response too large"));
                                return;
                            }
                            byte[] chunk = new byte[value.remaining()];
                            value.get(chunk);
                            bytes.writeBytes(chunk);
                        }
                        subscription.request(1);
                    }

                    public void onError(Throwable failure) {
                        result.completeExceptionally(failure);
                    }

                    public void onComplete() {
                        result.complete(bytes.toByteArray());
                    }
                };
    }

    @Override
    public void close() {
        http.shutdownNow();
    }
}
