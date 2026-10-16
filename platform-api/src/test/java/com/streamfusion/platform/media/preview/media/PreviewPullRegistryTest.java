package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.nio.file.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class PreviewPullRegistryTest {
    @TempDir Path directory;
    private final ObjectMapper json = new ObjectMapper();
    private final ActiveZlm runtime =
            new ActiveZlm(
                    URI.create("http://127.0.0.1:8088"),
                    "never-persist-secret",
                    Path.of("MediaServer.exe"),
                    Path.of("config.ini"),
                    "1");

    @Test
    void restartInvalidatesOwnershipAndKeepsExactTuplesUntilConfirmedExit() throws Exception {
        var root = stream("0".repeat(32));
        var viewer = stream("1".repeat(32));
        try (var registry = new PreviewPullRegistry(directory, json)) {
            registry.register(root, false);
            registry.started(root);
            registry.register(viewer, true);
            registry.started(viewer);
            registry.requireRecovered();
            assertThat(registry.pending()).isEmpty();
        }
        assertThat(Files.readString(directory.resolve("preview-pulls.json")))
                .doesNotContain("never-persist-secret", "rtsp://");
        try (var restarted = new PreviewPullRegistry(directory, json)) {
            assertThatThrownBy(restarted::requireRecovered).hasMessage("PREVIEW_RECOVERY_REQUIRED");
            assertThat(restarted.pending())
                    .extracting(PreviewPullRegistry.Entry::id)
                    .containsExactly(viewer.id(), root.id());
            var other =
                    new ActiveZlm(
                            URI.create("http://127.0.0.1:8089"),
                            "other",
                            runtime.executablePath(),
                            runtime.configPath(),
                            "1");
            assertThat(restarted.pending()).allMatch(e -> e.matches(runtime) && !e.matches(other));
            restarted.exited(viewer);
            assertThatThrownBy(restarted::requireRecovered).hasMessage("PREVIEW_RECOVERY_REQUIRED");
            restarted.exited(root);
            restarted.requireRecovered();
        }
    }

    @Test
    void unknownCreationOutcomeRetainsItsLateResultFenceAcrossRestart() {
        var stream = stream("2".repeat(32));
        try (var registry = new PreviewPullRegistry(directory, json)) {
            registry.register(stream, false);
        }
        try (var restarted = new PreviewPullRegistry(directory, json)) {
            assertThat(restarted.pending()).hasSize(1);
            assertThatThrownBy(() -> restarted.exited(stream))
                    .hasMessage("PREVIEW_RECOVERY_REQUIRED");
            assertThat(restarted.pending()).hasSize(1);
        }
    }

    @Test
    void concurrentOwnersOfTheSameWorkDirectoryFailClosed() {
        try (var first = new PreviewPullRegistry(directory, json);
                var second = new PreviewPullRegistry(directory, json)) {
            first.requireRecovered();
            assertThatThrownBy(second::requireRecovered).hasMessage("PREVIEW_RECOVERY_REQUIRED");
        }
    }

    @Test
    void malformedJournalIsPreservedAndNeverTreatedAsEmpty() throws Exception {
        // Deliberately corrupt isolated fixture; production recovery must preserve it.
        Path invalid = directory.resolve("preview-pulls.json");
        Files.writeString(invalid, "[{\"app\":\"foreign\"}]");
        try (var registry = new PreviewPullRegistry(directory, json)) {
            assertThatThrownBy(registry::requireRecovered).hasMessage("PREVIEW_RECOVERY_REQUIRED");
            assertThat(Files.readString(invalid)).isEqualTo("[{\"app\":\"foreign\"}]");
        }
    }

    private PreviewMediaGateway.Stream stream(String id) {
        String app = "sf_preview_0123456789ab";
        return new PreviewMediaGateway.Stream(
                runtime, app, id, "__defaultVhost__/" + app + "/" + id);
    }
}
