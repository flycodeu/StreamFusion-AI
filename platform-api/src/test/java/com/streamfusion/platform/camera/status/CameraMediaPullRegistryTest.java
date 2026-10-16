package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.concurrent.CompletableFuture;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class CameraMediaPullRegistryTest {
    @TempDir Path directory;
    private final ObjectMapper json = new ObjectMapper();

    @Test
    void durableSingleSlotIsOnlyReleasedAfterConfirmedExit() throws Exception {
        var stream = stream();
        var stopped = new CompletableFuture<Void>();
        try (var registry = new CameraMediaPullRegistry(directory, json)) {
            registry.reserve(stream, 7, "trace_7", stopped);
            assertThat(registry.hasPending()).isFalse();
            assertThatThrownBy(
                            () -> registry.reserve(stream, 8, "trace_8", new CompletableFuture<>()))
                    .isInstanceOf(PreviewFailure.class)
                    .hasMessage("MEDIA_BUSY");
            assertThat(registry.recoverable(stream.runtime())).isEmpty();
            registry.started();
            registry.releasing();
            assertThat(registry.hasPending()).isTrue();
            assertThat(registry.recoverable(stream.runtime()))
                    .isPresent()
                    .get()
                    .extracting(CameraMediaPullRegistry.Entry::checkId)
                    .isEqualTo(7L);
            assertThat(stopped).isNotDone();
            assertThat(Files.readString(directory.resolve("camera-media-pull.json")))
                    .contains("trace_7")
                    .doesNotContain("secret");
            registry.exited(stream);
            assertThat(registry.hasPending()).isFalse();
            assertThat(stopped).isCompleted();
            registry.reserve(stream, 8, "trace_8", new CompletableFuture<>());
            registry.notLaunched();
        }
    }

    @Test
    void restartRetainsUnknownLaunchAndMismatchedRuntimeCannotCleanIt() {
        var stream = stream();
        try (var first = new CameraMediaPullRegistry(directory, json)) {
            first.reserve(stream, 9, "trace9", new CompletableFuture<>());
            first.releasing();
            assertThat(first.recoverable(stream.runtime())).isEmpty();
        }
        try (var restarted = new CameraMediaPullRegistry(directory, json)) {
            assertThatThrownBy(
                            () ->
                                    restarted.reserve(
                                            stream, 10, "trace10", new CompletableFuture<>()))
                    .hasMessage("MEDIA_RECOVERY_REQUIRED");
            assertThat(restarted.recoverable(stream.runtime())).isEmpty();
        }
    }

    @Test
    void restartRecoversAcknowledgedPullButNotAnotherRuntime() {
        var stream = stream();
        try (var first = new CameraMediaPullRegistry(directory, json)) {
            first.reserve(stream, 9, "trace9", new CompletableFuture<>());
            first.started();
        }
        try (var restarted = new CameraMediaPullRegistry(directory, json)) {
            assertThat(
                            restarted.recoverable(
                                    new ActiveZlm(
                                            URI.create("http://127.0.0.1:1"),
                                            "secret",
                                            directory.resolve("server"),
                                            directory.resolve("config"),
                                            "test")))
                    .isEmpty();
            assertThat(restarted.recoverable(stream.runtime())).isPresent();
            restarted.exited(stream);
            restarted.reserve(stream, 10, "trace10", new CompletableFuture<>());
        }
    }

    @Test
    void secondProcessAndCorruptJournalFailClosed() throws Exception {
        var stream = stream();
        try (var first = new CameraMediaPullRegistry(directory, json);
                var second = new CameraMediaPullRegistry(directory, json)) {
            first.reserve(stream, 1, "trace", new CompletableFuture<>());
            assertThatThrownBy(() -> second.reserve(stream, 2, "trace", new CompletableFuture<>()))
                    .hasMessage("MEDIA_RECOVERY_REQUIRED");
            first.notLaunched();
        }
        Files.writeString(directory.resolve("camera-media-pull.json"), "invalid");
        try (var registry = new CameraMediaPullRegistry(directory, json)) {
            assertThatThrownBy(
                            () -> registry.reserve(stream, 1, "trace", new CompletableFuture<>()))
                    .hasMessage("MEDIA_RECOVERY_REQUIRED");
        }
    }

    @Test
    void exitCallbackCanReserveNextRuntimeWithoutOldExitErasingItsOwnership() {
        var first = stream();
        var secondRuntime =
                new ActiveZlm(
                        URI.create("http://127.0.0.1:8099"),
                        "replacement-secret",
                        directory.resolve("serverB"),
                        directory.resolve("configB"),
                        "testB");
        var second =
                new Stream(
                        secondRuntime, first.app(), "abcdef0123456789abcdef0123456789", "unused");
        var firstStopped = new CompletableFuture<Void>();
        var secondStopped = new CompletableFuture<Void>();
        try (var registry = new CameraMediaPullRegistry(directory, json)) {
            registry.reserve(first, 1, "trace1", firstStopped);
            registry.started();
            registry.releasing();
            firstStopped.thenRun(() -> registry.reserve(second, 2, "trace2", secondStopped));
            registry.exited(first);
            assertThat(registry.ownedRuntime()).isPresent().get().isSameAs(secondRuntime);
            assertThat(secondStopped).isNotDone();
            registry.started();
            registry.releasing();
            registry.exited(second);
            assertThat(secondStopped).isCompleted();
            assertThat(registry.ownedRuntime()).isEmpty();
        }
    }

    private Stream stream() {
        String app = "sf_preview_0123456789ab", id = "0123456789abcdef0123456789abcdef";
        var runtime =
                new ActiveZlm(
                        URI.create("http://127.0.0.1:8088"),
                        "secret",
                        directory.resolve("server"),
                        directory.resolve("config"),
                        "test");
        return new Stream(runtime, app, id, "__defaultVhost__/" + app + "/" + id);
    }
}
