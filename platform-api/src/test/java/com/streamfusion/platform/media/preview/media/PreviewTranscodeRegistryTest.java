package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.concurrent.Executors;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class PreviewTranscodeRegistryTest {
    @TempDir Path directory;
    private final ObjectMapper json = new ObjectMapper();
    private final ActiveZlm runtime =
            new ActiveZlm(
                    URI.create("http://127.0.0.1:8088"),
                    "never-persist-this-secret",
                    Path.of("/zlm/MediaServer"),
                    Path.of("/zlm/config.ini"),
                    "test");

    @Test
    void concurrentStartsCannotExceedTheTranscodeBudget() throws Exception {
        var registry = new PreviewTranscodeRegistry(directory, json, 2);
        try (var workers = Executors.newFixedThreadPool(8)) {
            var work = new ArrayList<java.util.concurrent.Future<Boolean>>();
            for (int index = 0; index < 32; index++) {
                int target = index;
                work.add(
                        workers.submit(
                                () -> {
                                    try {
                                        registry.register(stream(target), output(target));
                                        return true;
                                    } catch (PreviewFailure full) {
                                        assertThat(full.reason())
                                                .isEqualTo("TRANSCODE_CAPACITY_EXCEEDED");
                                        return false;
                                    }
                                }));
            }
            int accepted = 0;
            for (var result : work) if (result.get()) accepted++;
            assertThat(accepted).isEqualTo(2);
            assertThat(registry.occupied()).isEqualTo(2);
        } finally {
            registry.close();
        }
    }

    @Test
    void disabledTranscodingNeverRegistersANewResource() {
        var registry = new PreviewTranscodeRegistry(directory, json, 0);
        try {
            assertThatThrownBy(() -> registry.register(stream(1), output(1)))
                    .hasMessage("TRANSCODE_DISABLED");
            assertThat(registry.occupied()).isZero();
        } finally {
            registry.close();
        }
    }

    @Test
    void restartAndLoweredBudgetKeepOldReservationsUntilConfirmedExit() {
        var first = new PreviewTranscodeRegistry(directory, json, 2);
        first.register(stream(1), output(1));
        first.register(stream(2), output(2));
        first.close();
        var restarted = new PreviewTranscodeRegistry(directory, json, 1);
        try {
            assertThat(restarted.occupied()).isEqualTo(2);
            restarted.releasing(output(1).key());
            assertThat(restarted.occupied()).isEqualTo(2);
            assertThatThrownBy(() -> restarted.register(stream(3), output(3)))
                    .hasMessage("TRANSCODE_CAPACITY_EXCEEDED");
            restarted.exited(output(1).key());
            assertThatThrownBy(() -> restarted.register(stream(3), output(3)))
                    .hasMessage("TRANSCODE_CAPACITY_EXCEEDED");
            restarted.exited(output(2).key());
            restarted.register(stream(3), output(3));
            assertThat(restarted.occupied()).isEqualTo(1);
        } finally {
            restarted.close();
        }
    }

    @Test
    void concurrentDistinctResourcesAreRegisteredWithinAnExplicitBudget() throws Exception {
        var registry = new PreviewTranscodeRegistry(directory, json);
        try (var workers = Executors.newFixedThreadPool(8)) {
            var work = new ArrayList<java.util.concurrent.Future<?>>();
            for (int index = 0; index < 64; index++) {
                int target = index;
                work.add(workers.submit(() -> registry.register(stream(target), output(target))));
            }
            for (var result : work) result.get();
            assertThat(registry.keys()).hasSize(64);
            assertThatThrownBy(() -> registry.register(stream(64), output(64)))
                    .hasMessage("TRANSCODE_CAPACITY_EXCEEDED");
            assertThat(Files.readString(journal()))
                    .doesNotContain("never-persist-this-secret", "password", "rtsp://");
        } finally {
            registry.close();
        }
        var restarted = new PreviewTranscodeRegistry(directory, json);
        try {
            assertThat(restarted.keys()).hasSize(64);
            assertThat(restarted.recoverable(runtime)).hasSize(64);
        } finally {
            restarted.close();
        }
    }

    @Test
    void concurrentDuplicateRegistrationHasOnlyOneOwner() throws Exception {
        var registry = new PreviewTranscodeRegistry(directory, json);
        try (var workers = Executors.newFixedThreadPool(8)) {
            var work = new ArrayList<java.util.concurrent.Future<Boolean>>();
            for (int index = 0; index < 16; index++)
                work.add(
                        workers.submit(
                                () -> {
                                    try {
                                        registry.register(stream(1), output(1));
                                        return true;
                                    } catch (PreviewFailure duplicate) {
                                        assertThat(duplicate.reason())
                                                .isEqualTo("TRANSCODE_RECOVERY_REQUIRED");
                                        return false;
                                    }
                                }));
            int registered = 0;
            for (var result : work) if (result.get()) registered++;
            assertThat(registered).isEqualTo(1);
            assertThat(registry.keys()).containsExactly(output(1).key());
        } finally {
            registry.close();
        }
    }

    @Test
    void restartRetainsUnknownStartWithoutBlockingUnrelatedResources() {
        var first = new PreviewTranscodeRegistry(directory, json);
        first.register(stream(1), output(1));
        assertThat(first.canConfirmExit(output(1).key())).isFalse();
        first.close();
        var restarted = new PreviewTranscodeRegistry(directory, json);
        try {
            assertThat(restarted.recoverable(runtime)).hasSize(1);
            restarted.register(stream(2), output(2));
            assertThat(restarted.keys())
                    .containsExactlyInAnyOrder(output(1).key(), output(2).key());
            assertThat(restarted.canConfirmExit(output(1).key())).isFalse();
            restarted.started(output(1).key());
            assertThat(restarted.canConfirmExit(output(1).key())).isTrue();
            restarted.exited(output(1).key());
            assertThat(restarted.keys()).containsExactly(output(2).key());
        } finally {
            restarted.close();
        }
    }

    @Test
    void acknowledgedStartAndCleanupKeepTheRecordUntilConfirmedExit() {
        var registry = new PreviewTranscodeRegistry(directory, json);
        try {
            registry.register(stream(1), output(1));
            registry.started(output(1).key());
            assertThat(registry.recoverable(runtime)).isEmpty();
            registry.releasing(output(1).key());
            assertThat(registry.recoverable(runtime)).hasSize(1);
            registry.register(stream(2), output(2));
            assertThat(registry.keys()).containsExactlyInAnyOrder(output(1).key(), output(2).key());
            registry.exited(output(1).key());
            assertThat(registry.keys()).containsExactly(output(2).key());
        } finally {
            registry.close();
        }
    }

    @Test
    void legacyJournalKeepsTheSameFileNameAndEntryShape() throws Exception {
        Files.writeString(
                journal(),
                """
                [{"api":"http://127.0.0.1:8088","config":%s,
                "app":"sf_preview_0123456789ab","input":"00000000000000000000000000000001",
                "output":"00000000000000000000000000000001h264",
                "key":"00000000000000000000000000000001","notBefore":0}]
                """
                        .formatted(
                                json.writeValueAsString(
                                        runtime.configPath()
                                                .toAbsolutePath()
                                                .normalize()
                                                .toString())));
        var registry = new PreviewTranscodeRegistry(directory, json);
        try {
            assertThat(registry.recoverable(runtime)).hasSize(1);
            assertThat(registry.canConfirmExit(output(1).key())).isTrue();
            registry.register(stream(2), output(2));
            var values = json.readTree(Files.readString(journal()));
            assertThat(values.size()).isEqualTo(2);
            var fields = new ArrayList<String>();
            values.get(0).fieldNames().forEachRemaining(fields::add);
            assertThat(fields)
                    .containsExactlyInAnyOrder(
                            "api", "config", "app", "input", "output", "key", "notBefore");
        } finally {
            registry.close();
        }
    }

    @Test
    void anotherProcessCannotResetTheSameJournal() {
        var first = new PreviewTranscodeRegistry(directory, json);
        var second = new PreviewTranscodeRegistry(directory, json);
        try {
            first.register(stream(1), output(1));
            assertThatThrownBy(() -> second.register(stream(2), output(2)))
                    .hasMessage("TRANSCODE_RECOVERY_REQUIRED");
            assertThat(first.keys()).hasSize(1);
        } finally {
            first.close();
            second.close();
        }
    }

    @Test
    void corruptJournalFailsClosedInsteadOfOverwritingEvidence() throws Exception {
        Files.writeString(journal(), "corrupt");
        var registry = new PreviewTranscodeRegistry(directory, json);
        try {
            assertThatThrownBy(() -> registry.register(stream(1), output(1)))
                    .hasMessage("TRANSCODE_RECOVERY_REQUIRED");
            assertThat(Files.readString(journal())).isEqualTo("corrupt");
        } finally {
            registry.close();
        }
    }

    @Test
    void oversizedJournalIsRejectedAsFileSafetyNotATranscodeCount() throws Exception {
        String oversized = " ".repeat(4 * 1024 * 1024 + 1);
        Files.writeString(journal(), oversized);
        var registry = new PreviewTranscodeRegistry(directory, json);
        try {
            assertThatThrownBy(registry::keys).hasMessage("TRANSCODE_RECOVERY_REQUIRED");
            assertThat(Files.size(journal())).isEqualTo(oversized.length());
        } finally {
            registry.close();
        }
    }

    @Test
    void fullJournalRejectsNewRegistrationWithoutDamagingExistingRecords() throws Exception {
        var entries = new ArrayList<PreviewTranscodeRegistry.Entry>();
        for (int index = 0; index < 1024; index++) entries.add(entry(index, "/".repeat(4095)));
        int targetBytes = 4 * 1024 * 1024 - 1;
        int excess = json.writeValueAsBytes(entries).length - targetBytes;
        assertThat(excess).isPositive();
        for (int index = 0; excess > 0; index++) {
            int trimmed = Math.min(excess, 4094);
            entries.set(index, entry(index, "/".repeat(4095 - trimmed)));
            excess -= trimmed;
        }
        byte[] before = json.writeValueAsBytes(entries);
        assertThat(before).hasSize(targetBytes);
        Files.write(journal(), before);
        var registry = new PreviewTranscodeRegistry(directory, json, 2048);
        try {
            assertThat(registry.keys()).hasSize(1024);
            assertThatThrownBy(() -> registry.register(stream(2048), output(2048)))
                    .hasMessage("TRANSCODE_RECOVERY_REQUIRED");
            assertThat(registry.keys()).hasSize(1024);
            assertThat(Files.readAllBytes(journal())).isEqualTo(before);
            registry.exited(output(1023).key());
            registry.register(stream(2048), output(2048));
            assertThat(registry.keys()).hasSize(1024).contains(output(2048).key());
        } finally {
            registry.close();
        }
    }

    @Test
    void lateCleanupCannotWriteAfterTheFileLockHasBeenReleased() throws Exception {
        var first = new PreviewTranscodeRegistry(directory, json);
        first.register(stream(1), output(1));
        first.close();
        String before = Files.readString(journal());
        var restarted = new PreviewTranscodeRegistry(directory, json);
        try {
            assertThat(restarted.keys()).hasSize(1);
            assertThatThrownBy(() -> first.exited(output(1).key()))
                    .hasMessage("TRANSCODE_RECOVERY_REQUIRED");
            assertThatThrownBy(() -> first.register(stream(2), output(2)))
                    .hasMessage("TRANSCODE_RECOVERY_REQUIRED");
            assertThat(Files.readString(journal())).isEqualTo(before);
        } finally {
            restarted.close();
        }
    }

    @Test
    void recoverySelectsOneEligibleRecordAndRotatesWithoutWritingTheJournal() throws Exception {
        var registry = new PreviewTranscodeRegistry(directory, json, 8);
        try {
            for (int index = 1; index <= 4; index++)
                registry.register(stream(index), output(index));
            for (int index = 1; index <= 3; index++) registry.started(output(index).key());
            registry.releasing(output(1).key());
            registry.releasing(output(2).key());
            registry.releasing(output(4).key());
            byte[] before = Files.readAllBytes(journal());
            assertThat(registry.nextRecoverable(runtime).orElseThrow().key())
                    .isEqualTo(output(1).key());
            assertThat(registry.nextRecoverable(runtime).orElseThrow().key())
                    .isEqualTo(output(2).key());
            assertThat(registry.nextRecoverable(runtime).orElseThrow().key())
                    .isEqualTo(output(1).key());
            assertThat(registry.keys()).hasSize(4);
            assertThat(Files.readAllBytes(journal())).isEqualTo(before);
        } finally {
            registry.close();
        }
    }

    private Path journal() {
        return directory.resolve("preview-transcodes.json");
    }

    private PreviewTranscodeRegistry.Entry entry(int index, String config) {
        return new PreviewTranscodeRegistry.Entry(
                runtime.apiBase().toString(),
                config,
                "sf_preview_0123456789ab",
                "%032x".formatted(index),
                output(index).id(),
                output(index).key(),
                0);
    }

    private PreviewMediaGateway.Stream stream(int index) {
        String id = "%032x".formatted(index);
        return new PreviewMediaGateway.Stream(
                runtime,
                "sf_preview_0123456789ab",
                id,
                "__defaultVhost__/sf_preview_0123456789ab/" + id);
    }

    private PreviewMediaGateway.Transcode output(int index) {
        return new PreviewMediaGateway.Transcode(
                "%032xh264".formatted(index), "%032x".formatted(index));
    }
}
