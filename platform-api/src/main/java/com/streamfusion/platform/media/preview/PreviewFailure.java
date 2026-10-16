package com.streamfusion.platform.media.preview;

/** Safe machine-readable reasons. No upstream exception message is forwarded to a client. */
public final class PreviewFailure extends RuntimeException {
    private final String reason;
    private final com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Transcode
            pendingTranscode;

    public PreviewFailure(String reason) {
        this(reason, null);
    }

    public PreviewFailure(
            String reason,
            com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Transcode
                    pendingTranscode) {
        super(reason, null, false, false);
        this.reason = reason;
        this.pendingTranscode = pendingTranscode;
    }

    public String reason() {
        return reason;
    }

    public com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Transcode
            pendingTranscode() {
        return pendingTranscode;
    }
}
