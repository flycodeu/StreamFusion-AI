package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.status.CameraProbeResult.Outcome;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.RawVideo;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.preview.source.PreviewLocatorService;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import java.net.URI;
import java.nio.file.Path;
import java.time.Instant;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.test.util.ReflectionTestUtils;

class CameraMediaProbeTest {
    @TempDir Path directory;
    private final MediaRuntimeService runtimes = mock(MediaRuntimeService.class);
    private final PreviewLocatorService locator = mock(PreviewLocatorService.class);
    private final PreviewMediaGateway media = mock(PreviewMediaGateway.class);
    private CameraMediaPullRegistry registry;
    private CameraMediaProbe probe;
    private MediaRuntimeService.ActiveZlm runtime;
    private final CameraProfileEntity profile = new CameraProfileEntity();

    @BeforeEach
    void start() {
        profile.setId(1L);
        profile.setSourceId(2L);
        runtime =
                new MediaRuntimeService.ActiveZlm(
                        URI.create("http://127.0.0.1:8088"),
                        "secret",
                        directory.resolve("server"),
                        directory.resolve("config"),
                        "test");
        when(runtimes.activeZlm()).thenReturn(Optional.of(runtime));
        when(locator.resolve(any(), any(), any()))
                .thenReturn(
                        new PreviewLocatorService.Resolved(URI.create("rtsp://127.0.0.1/test")));
        when(media.openObservation(any(), anyString(), anyString(), any(), any()))
                .thenAnswer(
                        invocation ->
                                new Stream(
                                        invocation.getArgument(0),
                                        invocation.getArgument(1),
                                        invocation.getArgument(2),
                                        "__defaultVhost__/"
                                                + invocation.getArgument(1)
                                                + "/"
                                                + invocation.getArgument(2)));
        registry = new CameraMediaPullRegistry(directory, new ObjectMapper());
        probe = new CameraMediaProbe(runtimes, locator, media, registry);
    }

    @AfterEach
    void stop() {
        probe.close();
        registry.close();
    }

    @Test
    void actualRawFramesAcrossFiveSecondsProduceReceivingEvidenceAndCleanup() throws Exception {
        var frames = new AtomicLong(10);
        when(media.observeRawVideo(any(), any()))
                .thenAnswer(ignored -> sample(frames.getAndAdd(30)));
        var cancellation = new RtspDescribeProbe.Cancellation();
        var result = execute(cancellation);
        assertThat(result.outcome()).isEqualTo(Outcome.ACCESSIBLE);
        assertThat(result.reasonCode()).isEqualTo("MEDIA_RECEIVING");
        assertThat(result.durationMs()).isBetween(5000L, 12000L);
        assertThat(result.evidence().sampleCount()).isGreaterThanOrEqualTo(3);
        assertThat(result.evidence().lastFrames()).isGreaterThan(result.evidence().firstFrames());
        verify(media, never()).transcode(any(), anyString(), any());
        var released = new CompletableFuture<Void>();
        cancellation.afterResourcesStopped(() -> released.complete(null));
        released.get(5, TimeUnit.SECONDS);
        verify(media).close(any(), isNull(), any());
    }

    @Test
    void validButFrozenCounterIsMediaFailureNotControlOffline() {
        when(media.observeRawVideo(any(), any())).thenAnswer(ignored -> sample(0));
        var result = execute(new RtspDescribeProbe.Cancellation());
        assertThat(result.outcome()).isEqualTo(Outcome.FAILED);
        assertThat(result.reasonCode()).isEqualTo("MEDIA_NO_FRAMES");
        assertThat(result.evidence().lastFrames()).isZero();
        assertThat(result.evidence().sampleCount()).isGreaterThanOrEqualTo(3);
    }

    @Test
    void resetOrIdentityChangeCannotBecomeCameraFailure() {
        when(media.observeRawVideo(any(), any())).thenReturn(sample(10), sample(1));
        var result = execute(new RtspDescribeProbe.Cancellation());
        assertThat(result.outcome()).isEqualTo(Outcome.NOT_EXECUTED);
        assertThat(result.reasonCode()).isEqualTo("MEDIA_COUNTER_RESET");
        assertThat(result.evidence()).isNull();
    }

    @Test
    void changedInstanceIsNotReceivingEvidence() {
        when(media.observeRawVideo(any(), any()))
                .thenReturn(
                        sample(10), new RawVideo("instance2", "h264:1920:1080", 50, Instant.now()));
        var result = execute(new RtspDescribeProbe.Cancellation());
        assertThat(result.outcome()).isEqualTo(Outcome.NOT_EXECUTED);
        assertThat(result.reasonCode()).isEqualTo("MEDIA_INSTANCE_CHANGED");
        assertThat(result.evidence()).isNull();
    }

    @Test
    void cancellationAfterLateSuccessfulOpenStillCleansOwnedPullBeforeReleasing() {
        ((java.util.concurrent.ScheduledExecutorService)
                        ReflectionTestUtils.getField(probe, "cleanup"))
                .shutdownNow();
        var cancellation = new RtspDescribeProbe.Cancellation();
        when(media.openObservation(any(), anyString(), anyString(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            cancellation.cancel();
                            return new Stream(
                                    runtime,
                                    invocation.getArgument(1),
                                    invocation.getArgument(2),
                                    "unused");
                        });
        var result = execute(cancellation);
        assertThat(result.outcome()).isEqualTo(Outcome.CANCELLED);
        verify(media, never()).observeRawVideo(any(), any());
        var released = new CompletableFuture<Void>();
        cancellation.afterResourcesStopped(() -> released.complete(null));
        assertThat(released).isNotDone();
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).close(any(), isNull(), any());
        ReflectionTestUtils.invokeMethod(probe, "recover");
        assertThat(released).isNotDone();
        assertThat(execute(new RtspDescribeProbe.Cancellation()).reasonCode())
                .isEqualTo("MEDIA_RECOVERY_REQUIRED");
        doNothing().when(media).close(any(), isNull(), any());
        ReflectionTestUtils.setField(probe, "nextRecovery", 0L);
        ReflectionTestUtils.invokeMethod(probe, "recover");
        assertThat(released).isCompleted();
    }

    @Test
    void revokedBackgroundEligibilityDuringSamplingCannotWriteMediaFailure() {
        var authorized = new java.util.concurrent.atomic.AtomicBoolean(true);
        when(media.observeRawVideo(any(), any()))
                .thenAnswer(
                        ignored -> {
                            authorized.set(false);
                            return sample(10);
                        });
        var result =
                probe.execute(
                        1,
                        "trace1",
                        profile,
                        () -> {
                            if (!authorized.get())
                                throw new com.streamfusion.platform.camera.access.adapter
                                        .CameraAdapterException("ACTOR_REVOKED");
                        },
                        System.nanoTime() + TimeUnit.SECONDS.toNanos(12),
                        new RtspDescribeProbe.Cancellation());
        assertThat(result.outcome()).isEqualTo(Outcome.NOT_EXECUTED);
        assertThat(result.reasonCode()).isEqualTo("ACTOR_REVOKED");
        assertThat(result.evidence()).isNull();
    }

    @Test
    void missingRuntimeAndBusySlotNeverOpenUpstream() {
        when(runtimes.activeZlm()).thenReturn(Optional.empty());
        assertThat(execute(new RtspDescribeProbe.Cancellation()).reasonCode())
                .isEqualTo("MEDIA_RUNTIME_UNAVAILABLE");
        when(runtimes.activeZlm()).thenReturn(Optional.of(runtime));
        var stream =
                new Stream(
                        runtime,
                        "sf_preview_0123456789ab",
                        "0123456789abcdef0123456789abcdef",
                        "unused");
        registry.reserve(stream, 3, "trace3", new CompletableFuture<>());
        assertThat(execute(new RtspDescribeProbe.Cancellation()).reasonCode())
                .isEqualTo("MEDIA_BUSY");
        verifyNoInteractions(locator, media);
    }

    @Test
    void deadlineAndMissingEvidenceRemainNotExecuted() {
        when(media.observeRawVideo(any(), any()))
                .thenThrow(new PreviewFailure("MEDIA_EVIDENCE_INSUFFICIENT"));
        var result =
                probe.execute(
                        1,
                        "trace1",
                        profile,
                        () -> {},
                        System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(150),
                        new RtspDescribeProbe.Cancellation());
        assertThat(result.outcome()).isEqualTo(Outcome.NOT_EXECUTED);
        assertThat(result.reasonCode()).isEqualTo("MEDIA_TIMEOUT");
        assertThat(result.evidence()).isNull();
    }

    @Test
    void cancelledAndAuthorizationFailureBeforeLaunchLeaveNoResource() {
        var cancellation = new RtspDescribeProbe.Cancellation();
        cancellation.cancel();
        assertThat(execute(cancellation).outcome()).isEqualTo(Outcome.CANCELLED);
        when(locator.resolve(any(), any(), any())).thenThrow(new PreviewFailure("NETWORK_BLOCKED"));
        var result = execute(new RtspDescribeProbe.Cancellation());
        assertThat(result.reasonCode()).isEqualTo("NETWORK_BLOCKED");
        verifyNoInteractions(media);
        var stream =
                new Stream(
                        runtime,
                        "sf_preview_0123456789ab",
                        "0123456789abcdef0123456789abcdef",
                        "unused");
        registry.reserve(stream, 8, "trace8", new CompletableFuture<>());
    }

    @Test
    void automaticCleanupIsBoundedAndLaterRequestCanTriggerOneRetry() {
        ((java.util.concurrent.ScheduledExecutorService)
                        ReflectionTestUtils.getField(probe, "cleanup"))
                .shutdownNow();
        var stream =
                new Stream(
                        runtime,
                        "sf_preview_0123456789ab",
                        "0123456789abcdef0123456789abcdef",
                        "unused");
        registry.reserve(stream, 3, "trace3", new CompletableFuture<>());
        registry.started();
        registry.releasing();
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).close(any(), isNull(), any());
        for (int i = 0; i < 8; i++) {
            ReflectionTestUtils.setField(probe, "nextRecovery", 0L);
            ReflectionTestUtils.invokeMethod(probe, "recover");
        }
        verify(media, times(6)).close(any(), isNull(), any());
        assertThat(registry.recoverable(runtime)).isPresent();
        ReflectionTestUtils.invokeMethod(probe, "requestRecovery");
        ReflectionTestUtils.invokeMethod(probe, "recover");
        verify(media, times(7)).close(any(), isNull(), any());
    }

    @Test
    void mediaRetryCanKickCleanupBeforeRetainedWorkerGateWithoutStartingAnotherPull()
            throws Exception {
        ((java.util.concurrent.ScheduledExecutorService)
                        ReflectionTestUtils.getField(probe, "cleanup"))
                .shutdownNow();
        when(media.observeRawVideo(any(), any()))
                .thenThrow(new PreviewFailure("MEDIA_COUNTER_RESET"));
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).close(any(), isNull(), any());
        try (var worker =
                new CameraStatusWorker(
                        mock(RtspDescribeProbe.class),
                        probe,
                        new CameraStatusWorker.Limits(1, 2, java.time.Duration.ZERO))) {
            var first =
                    worker.trySubmit(
                            CameraStatusWorker.Request.media(
                                    1,
                                    2,
                                    1,
                                    "trace1",
                                    profile,
                                    java.time.Duration.ofSeconds(18),
                                    () -> {},
                                    () -> {}));
            assertThat(first.completion().get(2, TimeUnit.SECONDS).reasonCode())
                    .isEqualTo("MEDIA_COUNTER_RESET");
            for (int i = 0; i < 6; i++) {
                ReflectionTestUtils.setField(probe, "nextRecovery", 0L);
                ReflectionTestUtils.invokeMethod(probe, "recover");
            }
            verify(media, times(6)).close(any(), isNull(), any());
            ((AtomicLong) ReflectionTestUtils.getField(probe, "lastRecoveryRequest")).set(0L);
            var retry =
                    worker.trySubmit(
                            CameraStatusWorker.Request.media(
                                    2,
                                    2,
                                    1,
                                    "trace2",
                                    profile,
                                    java.time.Duration.ofSeconds(18),
                                    () -> {},
                                    () -> {}));
            assertThat(retry.completion().get(2, TimeUnit.SECONDS).reasonCode())
                    .isEqualTo("PROFILE_BUSY");
            verify(media, times(1)).openObservation(any(), anyString(), anyString(), any(), any());
            doNothing().when(media).close(any(), isNull(), any());
            ReflectionTestUtils.invokeMethod(probe, "recover");
            verify(media, times(7)).close(any(), isNull(), any());
            var after =
                    worker.trySubmit(
                            CameraStatusWorker.Request.media(
                                    3,
                                    2,
                                    1,
                                    "trace3",
                                    profile,
                                    java.time.Duration.ofSeconds(18),
                                    () -> {},
                                    () -> {}));
            assertThat(after.completion().get(2, TimeUnit.SECONDS).reasonCode())
                    .isEqualTo("MEDIA_COUNTER_RESET");
            verify(media, times(2)).openObservation(any(), anyString(), anyString(), any(), any());
        }
    }

    @Test
    void runtimeSwitchStillCleansOriginalOwnedPullAndKeepsSlotUntilExit() {
        ((java.util.concurrent.ScheduledExecutorService)
                        ReflectionTestUtils.getField(probe, "cleanup"))
                .shutdownNow();
        when(media.observeRawVideo(any(), any()))
                .thenThrow(new PreviewFailure("MEDIA_COUNTER_RESET"));
        var cancellation = new RtspDescribeProbe.Cancellation();
        assertThat(execute(cancellation).reasonCode()).isEqualTo("MEDIA_COUNTER_RESET");
        var released = new CompletableFuture<Void>();
        cancellation.afterResourcesStopped(() -> released.complete(null));
        var replacement =
                new MediaRuntimeService.ActiveZlm(
                        URI.create("http://127.0.0.1:8099"),
                        "different-secret",
                        directory.resolve("serverB"),
                        directory.resolve("configB"),
                        "testB");
        when(runtimes.activeZlm()).thenReturn(Optional.of(replacement));
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).close(any(), isNull(), any());
        ReflectionTestUtils.invokeMethod(probe, "recover");
        assertThat(released).isNotDone();
        assertThat(registry.ownedRuntime()).isPresent().get().isSameAs(runtime);
        assertThat(execute(new RtspDescribeProbe.Cancellation()).reasonCode())
                .isEqualTo("MEDIA_RECOVERY_REQUIRED");
        verify(media, times(1)).openObservation(any(), anyString(), anyString(), any(), any());
        doNothing().when(media).close(any(), isNull(), any());
        ReflectionTestUtils.setField(probe, "nextRecovery", 0L);
        ReflectionTestUtils.invokeMethod(probe, "recover");
        assertThat(released).isCompleted();
        var cleaned = org.mockito.ArgumentCaptor.forClass(Stream.class);
        verify(media, times(2)).close(cleaned.capture(), isNull(), any());
        assertThat(cleaned.getAllValues())
                .allSatisfy(stream -> assertThat(stream.runtime()).isSameAs(runtime));
        assertThat(registry.ownedRuntime()).isEmpty();
        assertThat(execute(new RtspDescribeProbe.Cancellation()).reasonCode())
                .isEqualTo("MEDIA_COUNTER_RESET");
        verify(media).openObservation(same(replacement), anyString(), anyString(), any(), any());
    }

    private CameraProbeResult execute(RtspDescribeProbe.Cancellation cancellation) {
        return probe.execute(
                1,
                "trace1",
                profile,
                () -> {},
                System.nanoTime() + TimeUnit.SECONDS.toNanos(12),
                cancellation);
    }

    private RawVideo sample(long frames) {
        return new RawVideo("instance1", "h264:1920:1080", frames, Instant.now());
    }
}
