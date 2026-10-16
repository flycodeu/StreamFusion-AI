package com.streamfusion.platform.camera.status;

public record CameraProbeResult(
        Outcome outcome,
        String reasonCode,
        int statusCode,
        long durationMs,
        CameraMediaEvidence evidence) {
    public enum Outcome {
        ACCESSIBLE,
        FAILED,
        UNSUPPORTED,
        NOT_EXECUTED,
        CANCELLED
    }
}
