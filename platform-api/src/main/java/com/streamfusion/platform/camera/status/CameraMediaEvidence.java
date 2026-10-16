package com.streamfusion.platform.camera.status;

import java.time.Instant;

/** Observed RTP receipt, not decoded image health or an exact device offline time. */
public record CameraMediaEvidence(
        Instant windowStartedAt,
        Instant windowEndedAt,
        long firstFrames,
        long lastFrames,
        int sampleCount,
        long maxGapMs) {}
