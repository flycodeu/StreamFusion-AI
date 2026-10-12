package com.streamfusion.platform.preview;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import org.junit.jupiter.api.Test;

class PreviewContractTest {
    @Test
    void usagePreservesExplicitAdapterClassification() {
        var profile = new CameraProfileEntity();
        profile.setUsageHint("SUB");
        profile.setUsageOrigin("DEVICE_REPORTED");
        profile.setSourceLabel("MediaProfile_Channel2_MainStream");

        assertThat(PreviewCatalogService.usage(profile)).isEqualTo("SUB");
    }

    @Test
    void nameRulesOnlyApplyWhenUsageIsUnknown() {
        var profile = new CameraProfileEntity();
        profile.setUsageHint("UNKNOWN");
        profile.setUsageOrigin("UNKNOWN");
        profile.setSourceLabel("MediaProfile_Channel2_MainStream");

        assertThat(PreviewCatalogService.usage(profile)).isEqualTo("MAIN");
    }

    @Test
    void offerAndAnswerDoNotExposeSdpInToString() {
        var offer = new PreviewModels.Offer("v=0 secret-transport");
        var answer = new PreviewModels.Answer("v=0 secret-answer", "answer");
        assertThat(offer.toString()).doesNotContain("secret");
        assertThat(answer.toString()).doesNotContain("secret");
    }
}
