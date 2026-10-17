package com.streamfusion.platform.media.preview.execution;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewModels;
import java.net.URI;
import java.util.List;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;

/**
 * Internal execution boundary. The platform owns authorization, viewer deadlines and cleanup
 * retries. An executor owns media resources until close confirms their release. This is an
 * in-process contract, not a remote control protocol.
 */
public interface PreviewExecutor {
    record SourceKey(Long profileId, String connectionKey) {}

    record Playback(String codec, boolean transcoded, String sourceCodec) {
        public Playback(String codec, boolean transcoded) {
            this(codec, transcoded, transcoded ? "H265" : codec);
        }
    }

    void requireRecovered();

    /** Select the execution environment without starting media work. */
    void prepare(String viewerId);

    default void prepare(
            String viewerId, SourceKey source, List<String> codecs, PreviewDeadline deadline) {
        prepare(viewerId);
    }

    default PreviewAdmissionProtection.Observation protection() {
        return null;
    }

    default void requireAdmission() {}

    default void allowTranscoding(String viewerId, boolean allowed) {}

    default int sessionLimit(int localLimit) {
        return localLimit;
    }

    /** Persisted responsibility without a current browser lease still occupies capacity. */
    default int retainedViewers(java.util.Set<String> activeLeaseIds) {
        return 0;
    }

    default int transcodeLimit(int localLimit) {
        return localLimit;
    }

    /** Resolve credentials only when creating a source; joining viewers reuse the existing pull. */
    void attachSource(
            String viewerId,
            SourceKey source,
            Supplier<URI> resolver,
            Runnable checkpoint,
            PreviewDeadline deadline);

    Playback start(
            String viewerId,
            List<String> videoCodecs,
            Runnable checkpoint,
            PreviewDeadline deadline);

    void confirmReady(String viewerId, PreviewDeadline deadline);

    /** Extends execution-side ownership only after the platform rechecks viewer authorization. */
    default void renew(String viewerId, PreviewDeadline deadline) {
        deadline.check();
    }

    void requireCurrent(String viewerId);

    /** Mark a confirmed missing source as closing and return all affected viewer identities. */
    List<String> missingSourceViewers(String viewerId, PreviewDeadline deadline);

    PreviewModels.Answer answer(String viewerId, String sdp, PreviewDeadline deadline);

    PreviewModels.Stats stats(String viewerId, PreviewDeadline deadline);

    /**
     * Idempotent; retain precise resource ownership when cleanup fails or its outcome is unknown.
     */
    void close(String viewerId, PreviewDeadline deadline);

    int occupiedTranscodes();

    void reconcile(BooleanSupplier stopping);
}
