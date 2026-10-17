package com.streamfusion.platform.media.preview;

import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.preview.media.PreviewPullRegistry;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Component;

/** Reconciles only persisted exact resource tuples; it never restores viewer leases. */
@Component
public class PreviewRecoveryCoordinator {
    private static final Logger LOG = LoggerFactory.getLogger(PreviewRecoveryCoordinator.class);
    private static final int MAX_ATTEMPTS = 6;
    private final PreviewPullRegistry pulls;
    private final PreviewMediaGateway media;
    private final MediaRuntimeService runtimes;
    private final Map<PreviewPullRegistry.Entry, Integer> attempts = new HashMap<>();
    private long nextRetryRequest;

    public PreviewRecoveryCoordinator(
            PreviewPullRegistry pulls, PreviewMediaGateway media, MediaRuntimeService runtimes) {
        this.pulls = pulls;
        this.media = media;
        this.runtimes = runtimes;
    }

    void requireRecovered() {
        try {
            pulls.requireRecovered();
        } catch (PreviewFailure pending) {
            synchronized (attempts) {
                if (System.nanoTime() >= nextRetryRequest) {
                    attempts.clear();
                    nextRetryRequest = System.nanoTime() + TimeUnit.SECONDS.toNanos(30);
                }
            }
            throw pending;
        }
    }

    void reconcile(BooleanSupplier stopping) {
        var pending = pulls.pending();
        synchronized (attempts) {
            attempts.keySet().retainAll(pending);
        }
        var deadline = PreviewDeadline.after(Duration.ofSeconds(3));
        int processed = 0;
        for (var entry : pending) {
            if (stopping.getAsBoolean() || processed >= 16 || deadline.remaining().toMillis() < 100)
                break;
            if (Instant.now().toEpochMilli() < entry.notBefore()) continue;
            synchronized (attempts) {
                int count = attempts.getOrDefault(entry, 0);
                if (count >= MAX_ATTEMPTS) continue;
                attempts.put(entry, count + 1);
            }
            processed++;
            try {
                var runtime =
                        runtimes.activeZlm()
                                .filter(entry::matches)
                                .or(() -> runtimes.recoveryZlm(entry.api(), entry.config()))
                                .orElseThrow(() -> new PreviewFailure("ZLM_NOT_READY"));
                var stream =
                        new Stream(
                                runtime,
                                entry.app(),
                                entry.id(),
                                "__defaultVhost__/" + entry.app() + "/" + entry.id());
                media.closeRecovered(stream, deadline);
                pulls.exited(stream);
                synchronized (attempts) {
                    attempts.remove(entry);
                }
                LOG.info(
                        "Preview orphan stopped traceId={} app={} streamId={}",
                        "preview-recovery",
                        entry.app(),
                        entry.id());
            } catch (RuntimeException ex) {
                LOG.warn(
                        "Preview orphan cleanup pending traceId={} app={} streamId={} reasonCode={}",
                        "preview-recovery",
                        entry.app(),
                        entry.id(),
                        ex instanceof PreviewFailure known
                                ? known.reason()
                                : "DEPENDENCY_UNAVAILABLE");
            }
        }
    }
}
