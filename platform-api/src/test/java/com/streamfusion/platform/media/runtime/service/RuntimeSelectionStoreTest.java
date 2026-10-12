package com.streamfusion.platform.media.runtime.service;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Selection;
import java.nio.file.Files;
import org.junit.jupiter.api.Test;

class RuntimeSelectionStoreTest {
    @Test
    void updatesOnlyWithCurrentRevisionAndKeepsSelectionsPerOs() throws Exception {
        var directory = Files.createTempDirectory("media-selection");
        try {
            var store = new RuntimeSelectionStore(directory, new ObjectMapper());
            assertThat(store.load().revision()).isZero();
            var updated =
                    store.update(
                            "WINDOWS",
                            new Selection(
                                    "zlm-0123456789abcdef01234567",
                                    "ffmpeg-0123456789abcdef01234567"),
                            "0");
            assertThat(updated.revision()).isEqualTo(1);
            assertThat(store.load().selection("WINDOWS").zlmId()).startsWith("zlm-");
            assertThat(store.load().selection("LINUX").zlmId()).isNull();
            assertThatThrownBy(() -> store.update("WINDOWS", new Selection(null, null), "0"))
                    .hasMessageContaining("数据版本已变化");
        } finally {
            try (var files = Files.walk(directory)) {
                files.sorted(java.util.Comparator.reverseOrder())
                        .forEach(
                                path -> {
                                    try {
                                        Files.deleteIfExists(path);
                                    } catch (java.io.IOException ignored) {
                                    }
                                });
            }
        }
    }
}
