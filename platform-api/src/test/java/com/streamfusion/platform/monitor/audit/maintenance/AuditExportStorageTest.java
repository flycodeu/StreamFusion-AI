package com.streamfusion.platform.monitor.audit.maintenance;

import static org.assertj.core.api.Assertions.*;

import com.streamfusion.platform.common.config.PlatformProperties;
import java.io.ByteArrayOutputStream;
import java.nio.file.Path;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class AuditExportStorageTest {
    @TempDir Path work;

    @Test
    void publishesOnlyCompleteFilesAndCsvQuotesFormulas() throws Exception {
        var storage = new AuditExportStorage(new PlatformProperties(work, Duration.ofSeconds(15)));
        try (var out = storage.create(1)) {
            out.write("complete".getBytes());
        }
        assertThat(storage.available(1)).isFalse();
        storage.publish(1);
        var result = new ByteArrayOutputStream();
        try (var file = storage.open(1)) {
            file.copyTo(result);
        }
        assertThat(result.toString()).isEqualTo("complete");
        storage.remove(1);
        assertThat(storage.available(1)).isFalse();
        assertThat(AuditExportStorage.csv("=1+1")).isEqualTo("\"'=1+1\"");
        assertThat(AuditExportStorage.csv("a\",\nb")).isEqualTo("\"a\"\",\nb\"");
    }

    @Test
    void limitsDownloadsAndAlwaysReleasesSlotsOnOpenFailure() throws Exception {
        var storage = new AuditExportStorage(new PlatformProperties(work, Duration.ofSeconds(15)));
        for (long id = 1; id <= 3; id++) {
            try (var out = storage.create(id)) {
                out.write(1);
            }
            storage.publish(id);
        }
        try (var first = storage.open(1);
                var second = storage.open(2)) {
            assertThatThrownBy(() -> storage.open(3))
                    .isInstanceOf(
                            com.streamfusion.platform.common.exception.BusinessException.class);
            assertThatThrownBy(() -> storage.remove(1)).isInstanceOf(java.io.IOException.class);
        }
        assertThatThrownBy(() -> storage.open(4)).isInstanceOf(java.io.IOException.class);
        try (var first = storage.open(1);
                var second = storage.open(2)) {
            assertThatThrownBy(() -> storage.open(1))
                    .isInstanceOf(
                            com.streamfusion.platform.common.exception.BusinessException.class);
        }
        storage.remove(1);
    }
}
