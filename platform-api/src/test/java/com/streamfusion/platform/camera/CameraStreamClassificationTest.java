package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.camera.service.CameraStreamClassification;
import org.junit.jupiter.api.Test;

class CameraStreamClassificationTest {
    @Test
    void recognizesKnownNamesWithoutTreatingNamesAsIdentities() {
        assertThat(hint("mainStream")).isEqualTo("MAIN");
        assertThat(hint("MediaProfile_Channel2_MainStream")).isEqualTo("MAIN");
        assertThat(hint("subStream")).isEqualTo("SUB");
        assertThat(hint("MediaProfile_Channel1_SubStream1")).isEqualTo("SUB");
        assertThat(hint("thirdStream")).isEqualTo("THIRD");
        assertThat(hint("MediaProfile_Channel2_SubStream2")).isEqualTo("THIRD");
        assertThat(hint("MainStreamForAnotherView")).isEqualTo("UNKNOWN");
        assertThat(hint("Profile_1")).isEqualTo("UNKNOWN");
        assertThat(hint(null)).isEqualTo("UNKNOWN");
        assertThat(
                        CameraStreamClassification.classify("CUSTOM", "MANUAL", "mainStream")
                                .usageHint())
                .isEqualTo("CUSTOM");
        assertThat(
                        CameraStreamClassification.classify("SUB", "DEVICE_REPORTED", "mainStream")
                                .usageHint())
                .isEqualTo("SUB");
    }

    private String hint(String name) {
        return CameraStreamClassification.classify("UNKNOWN", "UNKNOWN", name).usageHint();
    }
}
