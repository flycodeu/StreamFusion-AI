package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.mockito.Mockito.when;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.runtime.adapter.WindowsProcessMetadata;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

class PreviewProcessInspectorTest {
    private static final String APP = "sf_preview_123456abcdef";
    private static final String INPUT = "a".repeat(32);
    private static final String OUTPUT = INPUT + "h264";
    private final WindowsProcessMetadata metadata = mock(WindowsProcessMetadata.class);

    @Test
    void confirmsNoMatchingProcessAndClosesInventory() {
        AtomicBoolean closed = new AtomicBoolean();
        var inspector =
                new PreviewProcessInspector(
                        () -> Stream.<ProcessHandle>empty().onClose(() -> closed.set(true)),
                        metadata,
                        false);
        assertThat(exited(inspector)).isTrue();
        assertThat(closed).isTrue();
    }

    @Test
    void exactOutputAndInputEitherRetainTheSlotRegardlessOfPort() {
        for (String url :
                new String[] {
                    "rtmp://127.0.0.1:1936/" + APP + "/" + OUTPUT,
                    "rtsp://127.0.0.1:8555/" + APP + "/" + OUTPUT,
                    "rtsp://127.0.0.1:8555/" + APP + "/" + INPUT,
                    "rtmp://[::1]:1936/" + APP + "/" + OUTPUT,
                    "rtmp://localhost:1936/" + APP + "/" + OUTPUT
                }) {
            assertThat(exited(inspector(false, ffmpeg("ffmpeg", "-i", url)))).as(url).isFalse();
        }
    }

    @Test
    void unrelatedTuplesAndRemoteFfmpegJobsDoNotMatch() {
        var inspector =
                inspector(
                        false,
                        ffmpeg("ffmpeg", "rtmp://127.0.0.1:1935/" + APP + "/" + OUTPUT + "extra"),
                        ffmpeg("ffmpeg", "rtsp://127.0.0.1:8554/other/" + INPUT),
                        ffmpeg("ffmpeg", "rtmp://192.0.2.1:1935/" + APP + "/" + OUTPUT));
        assertThat(exited(inspector)).isTrue();
    }

    @Test
    void onlyFfmpegExecutablesAreExamined() {
        assertThat(
                        exited(
                                inspector(
                                        false,
                                        ffmpeg("other", "rtmp://127.0.0.1/" + APP + "/" + OUTPUT))))
                .isTrue();
        verifyNoInteractions(metadata);
    }

    @Test
    void anyVisibleFfmpegWithoutReadableArgumentsRetainsTheSlot() {
        var process = ffmpeg("ffmpeg", "unrelated");
        when(process.info().arguments()).thenReturn(Optional.empty());
        assertThat(exited(inspector(false, process))).isFalse();
        verifyNoInteractions(metadata);
    }

    @Test
    void windowsFallbackReceivesOnlyObservedPidPathAndRemainingBudget() {
        var process = ffmpeg("ffmpeg.exe", "unrelated");
        when(process.pid()).thenReturn(51L);
        when(process.info().arguments()).thenReturn(Optional.empty());
        when(metadata.arguments(eq(51L), eq(Path.of("ffmpeg.exe")), any()))
                .thenReturn(Optional.of(new String[] {"rtmp://127.0.0.1/" + APP + "/" + OUTPUT}));
        assertThat(exited(inspector(true, process))).isFalse();
        verify(metadata)
                .arguments(
                        eq(51L),
                        eq(Path.of("ffmpeg.exe")),
                        org.mockito.ArgumentMatchers.argThat(
                                timeout ->
                                        timeout.isPositive()
                                                && timeout.compareTo(Duration.ofSeconds(2)) <= 0));
    }

    @Test
    void failedWindowsFallbackNeverConfirmsExit() {
        var process = ffmpeg("ffmpeg.exe", "unrelated");
        when(process.info().arguments()).thenReturn(Optional.empty());
        when(metadata.arguments(any(Long.class), any(), any())).thenReturn(Optional.empty());
        assertThat(exited(inspector(true, process))).isFalse();
    }

    @Test
    void exitedDuringMetadataLookupDoesNotKeepSlot() {
        var process = ffmpeg("ffmpeg", "unrelated");
        when(process.info().arguments()).thenReturn(Optional.empty());
        when(process.isAlive()).thenReturn(true, false);
        assertThat(exited(inspector(false, process))).isTrue();
    }

    @Test
    void emptyArgumentsAndOversizedArgumentsAreIncompleteEvidence() {
        assertThat(exited(inspector(false, ffmpeg("ffmpeg")))).isFalse();
        assertThat(exited(inspector(false, ffmpeg("ffmpeg", "x".repeat(65537))))).isFalse();
        assertThat(exited(inspector(false, ffmpeg("ffmpeg", new String[4097])))).isFalse();
    }

    @Test
    void remoteRuntimeAndInvalidTuplesAreRejectedBeforeEnumerating() {
        var inspector =
                new PreviewProcessInspector(
                        () -> {
                            throw new AssertionError("must not enumerate");
                        },
                        metadata,
                        false);
        assertThat(
                        inspector.exited(
                                runtime("http://192.0.2.1:8088"), APP, INPUT, OUTPUT, deadline()))
                .isFalse();
        assertThat(
                        inspector.exited(
                                runtime("http://127.0.0.1:8088"),
                                "other",
                                INPUT,
                                OUTPUT,
                                deadline()))
                .isFalse();
        assertThat(
                        inspector.exited(
                                runtime("http://127.0.0.1:8088"), APP, INPUT, "other", deadline()))
                .isFalse();
    }

    @Test
    void processLimitOrEnumerationFailureNeverConfirmsExit() {
        var process = ffmpeg("other", "unrelated");
        var overLimit =
                new PreviewProcessInspector(
                        () -> Stream.generate(() -> process).limit(2001), metadata, false);
        assertThat(exited(overLimit)).isFalse();
        var failed =
                new PreviewProcessInspector(
                        () -> {
                            throw new SecurityException("not permitted");
                        },
                        metadata,
                        false);
        assertThat(exited(failed)).isFalse();
    }

    @Test
    void expiredOrCancelledDeadlineNeverConfirmsExit() {
        var cancelled = deadline();
        cancelled.cancel();
        assertThat(
                        inspector(false)
                                .exited(
                                        runtime("http://127.0.0.1:8088"),
                                        APP,
                                        INPUT,
                                        OUTPUT,
                                        cancelled))
                .isFalse();
    }

    @Test
    void systemProcessesWithNoVisibleExecutableAreOutsideCurrentUserObservation() {
        var process = ffmpeg("other", "unrelated");
        when(process.info().command()).thenReturn(Optional.empty());
        assertThat(exited(inspector(false, process))).isTrue();
    }

    private PreviewProcessInspector inspector(boolean windows, ProcessHandle... values) {
        return new PreviewProcessInspector(() -> Stream.of(values), metadata, windows);
    }

    private static ProcessHandle ffmpeg(String executable, String... arguments) {
        var process = mock(ProcessHandle.class);
        var info = mock(ProcessHandle.Info.class);
        when(process.isAlive()).thenReturn(true);
        when(process.info()).thenReturn(info);
        when(info.command()).thenReturn(Optional.of(executable));
        when(info.arguments()).thenReturn(Optional.of(arguments));
        return process;
    }

    private static boolean exited(PreviewProcessInspector inspector) {
        return inspector.exited(runtime("http://127.0.0.1:8088"), APP, INPUT, OUTPUT, deadline());
    }

    private static PreviewDeadline deadline() {
        return PreviewDeadline.after(Duration.ofSeconds(3));
    }

    private static ActiveZlm runtime(String api) {
        return new ActiveZlm(
                URI.create(api),
                "not-a-real-secret",
                Path.of("MediaServer"),
                Path.of("config.ini"),
                "v");
    }
}
