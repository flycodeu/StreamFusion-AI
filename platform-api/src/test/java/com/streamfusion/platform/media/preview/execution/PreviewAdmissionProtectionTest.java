package com.streamfusion.platform.media.preview.execution;

import static org.assertj.core.api.Assertions.*;

import java.time.Instant;
import java.util.Set;
import org.junit.jupiter.api.Test;

class PreviewAdmissionProtectionTest {
    @Test
    void hostRequiresThreeHighSamplesAndThreeLowSamplesToRecover() {
        var guard = new PreviewAdmissionProtection();
        guard.sampleHost(.96, .4, Instant.now());
        guard.sampleHost(.96, .4, Instant.now());
        assertThat(guard.reason()).isNull();
        guard.sampleHost(.96, .4, Instant.now());
        assertThat(guard.reason()).isEqualTo("NODE_HOST_OVERLOADED");
        guard.sampleHost(.8, .8, Instant.now());
        guard.sampleHost(.8, .8, Instant.now());
        assertThat(guard.reason()).isEqualTo("NODE_HOST_OVERLOADED");
        guard.sampleHost(.8, .8, Instant.now());
        assertThat(guard.reason()).isNull();
    }

    @Test
    void unknownObservationsNeverClearOverloadAndConfirmedReleaseRemovesBandwidthOccupancy() {
        var guard = new PreviewAdmissionProtection();
        for (int i = 0; i < 3; i++) guard.sampleBandwidth("source-a", 10000.0, 8192);
        guard.sampleBandwidth("source-a", null, 8192);
        guard.sampleBandwidth("source-b", 1.0, 8192);
        assertThat(guard.reason()).isEqualTo("NODE_BITRATE_EXCEEDED");
        guard.retain(Set.of("source-b"));
        assertThat(guard.reason()).isNull();
    }
}
