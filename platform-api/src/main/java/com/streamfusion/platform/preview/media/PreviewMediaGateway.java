package com.streamfusion.platform.preview.media;

import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import com.streamfusion.platform.preview.PreviewModels;
import java.net.URI;

/** Preview control plane. Media remains in ZLM; no frames pass through the platform. */
public interface PreviewMediaGateway {
    record Stream(ActiveZlm runtime, String app, String id, String proxyKey) {
        @Override
        public String toString() {
            return "Stream[app=" + app + ",id=" + id + "]";
        }
    }

    record Transcode(String id, String key) {}

    Stream open(ActiveZlm runtime, String app, String id, URI source);

    Transcode transcode(Stream source, String outputId);

    PreviewModels.Stats stats(Stream source, String outputId);

    PreviewModels.Answer answer(Stream source, String outputId, String sdp);

    void close(Stream source, Transcode output);
}
