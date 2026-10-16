package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;

import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.boot.context.properties.bind.Bindable;
import org.springframework.boot.context.properties.bind.Binder;
import org.springframework.boot.context.properties.source.MapConfigurationPropertySource;

class PreviewPropertiesTest {
    @Test
    void defaultsAreConservativeAndIndependent() {
        var properties = bind(Map.of());
        assertThat(properties.maxSessions()).isEqualTo(36);
        assertThat(properties.maxTranscodes()).isEqualTo(2);
    }

    @Test
    void allowsExplicitDisableAndTheSupportedBoundaries() {
        assertThat(
                        bind(
                                Map.of(
                                        "platform.preview.max-sessions",
                                        "1",
                                        "platform.preview.max-transcodes",
                                        "0")))
                .isEqualTo(new PreviewProperties(1, 0));
        assertThat(
                        bind(
                                Map.of(
                                        "platform.preview.max-sessions",
                                        "64",
                                        "platform.preview.max-transcodes",
                                        "8")))
                .isEqualTo(new PreviewProperties(64, 8));
    }

    @Test
    void rejectsUnboundedAndNegativeBudgets() {
        for (int sessions : new int[] {-1, 0, 65, Integer.MAX_VALUE})
            assertThatThrownBy(() -> new PreviewProperties(sessions, 2))
                    .isInstanceOf(IllegalArgumentException.class);
        for (int transcodes : new int[] {-1, 9, Integer.MAX_VALUE})
            assertThatThrownBy(() -> new PreviewProperties(36, transcodes))
                    .isInstanceOf(IllegalArgumentException.class);
    }

    private PreviewProperties bind(Map<String, String> values) {
        return new Binder(new MapConfigurationPropertySource(values))
                .bindOrCreate("platform.preview", Bindable.of(PreviewProperties.class));
    }
}
