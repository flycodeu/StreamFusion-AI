package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.status.CameraProbeResult.Outcome;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.RawVideo;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.preview.source.PreviewLocatorService;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import jakarta.annotation.PreDestroy;
import java.time.Duration;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.stereotype.Component;

/** A bounded, owned RTP receipt sample. No decoding, transcoding or permanent camera pulls. */
@Component
public final class CameraMediaProbe implements AutoCloseable {
    private static final Logger LOG = LoggerFactory.getLogger(CameraMediaProbe.class);
    private static final long WINDOW_NANOS = TimeUnit.SECONDS.toNanos(5);
    private final MediaRuntimeService runtimes;
    private final PreviewLocatorService locator;
    private final PreviewMediaGateway media;
    private final CameraMediaPullRegistry registry;
    private final ScheduledExecutorService cleanup;
    private volatile boolean closed;
    private volatile PreviewDeadline activeDeadline;
    private final java.util.concurrent.atomic.AtomicBoolean recoveryRequested =
            new java.util.concurrent.atomic.AtomicBoolean();
    private final java.util.concurrent.atomic.AtomicLong lastRecoveryRequest =
            new java.util.concurrent.atomic.AtomicLong();
    private int recoveryAttempts;
    private long nextRecovery;
    private String cleanupFailure;

    public CameraMediaProbe(
            MediaRuntimeService runtimes,
            PreviewLocatorService locator,
            PreviewMediaGateway media,
            CameraMediaPullRegistry registry) {
        this.runtimes = runtimes;
        this.locator = locator;
        this.media = media;
        this.registry = registry;
        cleanup =
                Executors.newSingleThreadScheduledExecutor(
                        task -> {
                            var thread = new Thread(task, "camera-media-cleanup");
                            thread.setDaemon(true);
                            return thread;
                        });
        cleanup.scheduleWithFixedDelay(this::recover, 0, 2, TimeUnit.SECONDS);
    }

    public CameraProbeResult execute(
            long checkId,
            String traceId,
            CameraProfileEntity profile,
            Runnable authorization,
            long deadlineNanos,
            RtspDescribeProbe.Cancellation cancellation) {
        long started = System.nanoTime();
        long remaining = deadlineNanos - started;
        if (closed) return result(Outcome.NOT_EXECUTED, "PROBE_CLOSED", started, null);
        if (remaining <= 0) return result(Outcome.NOT_EXECUTED, "MEDIA_TIMEOUT", started, null);
        var deadline =
                PreviewDeadline.after(
                        Duration.ofNanos(Math.min(remaining, TimeUnit.SECONDS.toNanos(18))));
        var stopped = new CompletableFuture<Void>();
        if (!cancellation.claimResources(stopped, deadline::cancel))
            return result(
                    cancellation.isCancelled() ? Outcome.CANCELLED : Outcome.NOT_EXECUTED,
                    cancellation.isCancelled() ? "CANCELLED" : "CANCELLATION_IN_USE",
                    started,
                    null);
        boolean reserved = false;
        boolean launched = false;
        try {
            Runnable checkActive =
                    () -> {
                        deadline.check();
                        if (closed || cancellation.isCancelled())
                            throw new PreviewFailure("CANCELLED");
                    };
            Runnable active =
                    () -> {
                        checkActive.run();
                        authorization.run();
                    };
            active.run();
            var runtime =
                    runtimes.activeZlm()
                            .orElseThrow(() -> new PreviewFailure("MEDIA_RUNTIME_UNAVAILABLE"));
            String id = UUID.randomUUID().toString().replace("-", "");
            String app = "sf_preview_" + id.substring(0, 12);
            var stream = new Stream(runtime, app, id, "__defaultVhost__/" + app + "/" + id);
            requestRecovery();
            registry.reserve(stream, checkId, traceId, stopped);
            reserved = true;
            activeDeadline = deadline;
            var source = locator.resolve(profile, active, deadline);
            active.run();
            launched = true;
            media.openObservation(runtime, app, id, source.uri(), deadline);
            registry.started();
            active.run();
            RawVideo first = null;
            while (first == null) {
                active.run();
                try {
                    first = media.observeRawVideo(stream, deadline);
                    if (first == null) pause(Duration.ofMillis(500), checkActive);
                } catch (PreviewFailure missing) {
                    if (!"MEDIA_EVIDENCE_INSUFFICIENT".equals(missing.reason())) throw missing;
                    pause(Duration.ofMillis(500), checkActive);
                }
            }
            long windowStart = System.nanoTime();
            long lastSample = windowStart;
            long lastProgress = windowStart;
            long maxGap = 0;
            int samples = 1;
            RawVideo last = first;
            do {
                pause(Duration.ofMillis(2500), checkActive);
                active.run();
                RawVideo next = media.observeRawVideo(stream, deadline);
                active.run();
                if (next == null) throw new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT");
                if (!first.instanceId().equals(next.instanceId())
                        || !first.trackIdentity().equals(next.trackIdentity()))
                    throw new PreviewFailure("MEDIA_INSTANCE_CHANGED");
                if (next.frames() < last.frames()) throw new PreviewFailure("MEDIA_COUNTER_RESET");
                long now = System.nanoTime();
                maxGap = Math.max(maxGap, now - lastProgress);
                if (next.frames() > last.frames()) lastProgress = now;
                lastSample = now;
                last = next;
                samples++;
            } while (lastSample - windowStart < WINDOW_NANOS || samples < 3);
            var evidence =
                    new CameraMediaEvidence(
                            first.observedAt(),
                            last.observedAt(),
                            first.frames(),
                            last.frames(),
                            samples,
                            TimeUnit.NANOSECONDS.toMillis(maxGap));
            boolean receiving = last.frames() > first.frames();
            return result(
                    receiving ? Outcome.ACCESSIBLE : Outcome.FAILED,
                    receiving ? "MEDIA_RECEIVING" : "MEDIA_NO_FRAMES",
                    started,
                    evidence);
        } catch (CameraAdapterException failure) {
            return result(
                    Outcome.NOT_EXECUTED,
                    failure.reasonCode() != null && failure.reasonCode().startsWith("STREAM_URI_")
                            ? reason(failure.reasonCode())
                            : RtspDescribeProbe.authorizationReason(failure),
                    started,
                    null);
        } catch (PreviewFailure failure) {
            boolean cancelled = cancellation.isCancelled() || "CANCELLED".equals(failure.reason());
            return result(
                    cancelled ? Outcome.CANCELLED : Outcome.NOT_EXECUTED,
                    cancelled ? "CANCELLED" : reason(failure.reason()),
                    started,
                    null);
        } catch (RuntimeException failure) {
            return result(Outcome.NOT_EXECUTED, "MEDIA_RUNTIME_UNAVAILABLE", started, null);
        } finally {
            if (reserved) {
                activeDeadline = null;
                if (launched) registry.releasing();
                else registry.notLaunched();
            } else stopped.complete(null);
        }
    }

    private static void pause(Duration duration, Runnable active) {
        long end = System.nanoTime() + duration.toNanos();
        while (System.nanoTime() - end < 0) {
            active.run();
            java.util.concurrent.locks.LockSupport.parkNanos(
                    Math.min(TimeUnit.MILLISECONDS.toNanos(100), end - System.nanoTime()));
        }
        active.run();
    }

    private void recover() {
        if (closed) return;
        long now = System.nanoTime();
        boolean requested = recoveryRequested.getAndSet(false);
        if (!requested && (recoveryAttempts >= 6 || now - nextRecovery < 0)) return;
        try {
            if (!registry.hasPending()) return;
            var runtime = registry.ownedRuntime().or(runtimes::activeZlm);
            if (runtime.isEmpty()) return;
            var pending = registry.recoverable(runtime.get());
            if (pending.isEmpty()) return;
            var entry = pending.get();
            var stream =
                    new Stream(
                            runtime.get(),
                            entry.app(),
                            entry.id(),
                            "__defaultVhost__/" + entry.app() + "/" + entry.id());
            recoveryAttempts++;
            nextRecovery =
                    now
                            + TimeUnit.SECONDS.toNanos(
                                    Math.min(30, 1L << Math.min(recoveryAttempts, 5)));
            try (var ignored = MDC.putCloseable("traceId", entry.traceId())) {
                try {
                    media.close(stream, null, PreviewDeadline.after(Duration.ofSeconds(3)));
                    registry.exited(stream);
                } catch (RuntimeException failure) {
                    String reason =
                            failure instanceof PreviewFailure known
                                    ? known.reason()
                                    : "MEDIA_RECOVERY_REQUIRED";
                    if (!reason.equals(cleanupFailure))
                        LOG.warn(
                                "Camera media pull cleanup pending checkId={} reasonCode={}",
                                entry.checkId(),
                                reason);
                    cleanupFailure = reason;
                    return;
                }
                LOG.info(
                        "Camera media pull stopped checkId={} app={} streamId={}",
                        entry.checkId(),
                        stream.app(),
                        stream.id());
            }
            cleanupFailure = null;
            recoveryAttempts = 0;
            nextRecovery = 0;
        } catch (RuntimeException pending) {
            recoveryAttempts++;
            nextRecovery =
                    now
                            + TimeUnit.SECONDS.toNanos(
                                    Math.min(30, 1L << Math.min(recoveryAttempts, 5)));
            String reason =
                    pending instanceof PreviewFailure known
                            ? known.reason()
                            : "MEDIA_RECOVERY_REQUIRED";
            if (!reason.equals(cleanupFailure)) {
                try (var ignored = MDC.putCloseable("traceId", "camera-media-recovery")) {
                    LOG.warn("Camera media pull cleanup pending reasonCode={}", reason);
                }
            }
            cleanupFailure = reason;
        }
    }

    void requestRecovery() {
        long now = System.nanoTime();
        long last = lastRecoveryRequest.get();
        if ((last == 0 || now - last >= TimeUnit.SECONDS.toNanos(30))
                && lastRecoveryRequest.compareAndSet(last, now)) recoveryRequested.set(true);
    }

    private static String reason(String value) {
        if (value != null
                && value.matches("(?:GB_|INGRESS_|PUSH_STREAM_|HTTP_MEDIA_|HLS_)[A-Z0-9_]{1,64}"))
            return value;
        return switch (value) {
            case "MEDIA_BUSY",
                    "MEDIA_RECOVERY_REQUIRED",
                    "MEDIA_EVIDENCE_INSUFFICIENT",
                    "MEDIA_INSTANCE_CHANGED",
                    "MEDIA_COUNTER_RESET",
                    "MEDIA_RUNTIME_UNAVAILABLE",
                    "NETWORK_BLOCKED",
                    "STREAM_URI_TIMEOUT",
                    "STREAM_URI_AUTH_REJECTED",
                    "STREAM_URI_UNAVAILABLE",
                    "STREAM_URI_INVALID",
                    "LOCATOR_NOT_CONFIGURED",
                    "LOCATOR_INVALID",
                    "SOURCE_UNSUPPORTED",
                    "SOURCE_PROTOCOL_NOT_SUPPORTED",
                    "DNS_UNAVAILABLE",
                    "DESTINATION_REJECTED" ->
                    value;
            case "PREVIEW_TIMEOUT" -> "MEDIA_TIMEOUT";
            default -> "MEDIA_RUNTIME_UNAVAILABLE";
        };
    }

    private static CameraProbeResult result(
            Outcome outcome, String reason, long started, CameraMediaEvidence evidence) {
        return new CameraProbeResult(
                outcome,
                reason,
                0,
                Math.max(0, TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started)),
                evidence);
    }

    @Override
    @PreDestroy
    public void close() {
        closed = true;
        var deadline = activeDeadline;
        if (deadline != null) deadline.cancel();
        cleanup.shutdownNow();
    }
}
