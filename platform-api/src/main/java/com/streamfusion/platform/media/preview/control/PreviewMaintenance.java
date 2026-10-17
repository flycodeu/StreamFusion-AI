package com.streamfusion.platform.media.preview.control;

/** Preview-only maintenance extension. A seal never expires into admission. */
public final class PreviewMaintenance {
    private PreviewMaintenance() {}

    public record Command(
            int protocolVersion,
            String nodeId,
            String bootId,
            String configVersion,
            String action,
            String permitId,
            String jobId,
            long deadline) {}

    public record State(
            int protocolVersion,
            String nodeId,
            String state,
            String permitId,
            String bootId,
            String configVersion,
            String jobId,
            long expiresAt) {}
}
