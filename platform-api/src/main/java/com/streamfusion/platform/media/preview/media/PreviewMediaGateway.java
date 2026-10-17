package com.streamfusion.platform.media.preview.media;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewModels;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.time.Instant;

/** Preview control plane. Media remains in ZLM; no frames pass through the platform. */
public interface PreviewMediaGateway {
    record Stream(ActiveZlm runtime, String app, String id, String proxyKey) {
        @Override
        public String toString() {
            return "Stream[app=" + app + ",id=" + id + "]";
        }
    }

    record Transcode(String id, String key) {}

    record RawVideo(String instanceId, String trackIdentity, long frames, Instant observedAt) {}

    default RawVideo observeRawVideo(Stream source, PreviewDeadline deadline) {
        throw new com.streamfusion.platform.media.preview.PreviewFailure(
                "MEDIA_EVIDENCE_INSUFFICIENT");
    }

    Stream open(ActiveZlm runtime, String app, String id, URI source);

    /** Isolated viewer output from an owned local upstream; never accepts a caller URL. */
    Stream openViewer(Stream upstream, String app, String id, PreviewDeadline deadline);

    /** Exact tuple presence, distinct from video tracks not yet being ready. */
    boolean exists(Stream stream, PreviewDeadline deadline);

    Stream openObservation(
            ActiveZlm runtime, String app, String id, URI source, PreviewDeadline deadline);

    default Stream open(
            ActiveZlm runtime, String app, String id, URI source, PreviewDeadline deadline) {
        deadline.check();
        return open(runtime, app, id, source);
    }

    Transcode transcode(Stream source, String outputId);

    /** Fingerprint of the currently selected encoder and output command; never exposes secrets. */
    String transcodeConfiguration(Stream source, PreviewDeadline deadline);

    Transcode transcode(
            Stream source, String outputId, String configuration, PreviewDeadline deadline);

    default Transcode transcode(Stream source, String outputId, PreviewDeadline deadline) {
        deadline.check();
        return transcode(source, outputId);
    }

    PreviewModels.Stats stats(Stream source, String outputId);

    default PreviewModels.Stats stats(Stream source, String outputId, PreviewDeadline deadline) {
        deadline.check();
        return stats(source, outputId);
    }

    PreviewModels.Answer answer(Stream source, String outputId, String sdp);

    default PreviewModels.Answer answer(
            Stream source, String outputId, String sdp, PreviewDeadline deadline) {
        deadline.check();
        return answer(source, outputId, sdp);
    }

    void close(Stream source, Transcode output);

    /** Reconcile a recorded tuple after API restart, including any recorded transcode. */
    default void closeRecovered(Stream source, PreviewDeadline deadline) {
        close(source, null, deadline);
    }

    default void close(Stream source, Transcode output, PreviewDeadline deadline) {
        deadline.check();
        close(source, output);
    }
}
