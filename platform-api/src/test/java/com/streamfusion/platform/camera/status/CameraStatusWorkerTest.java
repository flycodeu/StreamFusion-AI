package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.net.URI;
import java.net.UnknownHostException;
import java.time.Duration;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;

class CameraStatusWorkerTest {
    @Test
    void platformBatchUsesOneWorkerSlotAndOneSourceGateWithIndependentCancellation()
            throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var platform = mock(CameraPlatformProbe.class);
        var claims = new AtomicInteger();
        var requests =
                java.util.stream.LongStream.rangeClosed(1, 100)
                        .mapToObj(
                                id -> {
                                    var profile =
                                            new com.streamfusion.platform.camera.pojo.entity
                                                    .CameraProfileEntity();
                                    profile.setId(id);
                                    profile.setSourceId(1L);
                                    return CameraStatusWorker.Request.upstream(
                                            id,
                                            1,
                                            id,
                                            "batch_trace",
                                            profile,
                                            Duration.ofSeconds(5),
                                            claims::incrementAndGet,
                                            () -> {});
                                })
                        .toList();
        when(platform.executeBatch(anyList()))
                .thenAnswer(
                        call -> {
                            java.util.List<CameraPlatformProbe.Target> targets =
                                    call.getArgument(0);
                            entered.countDown();
                            assertThat(release.await(3, TimeUnit.SECONDS)).isTrue();
                            return targets.stream().map(t -> accessible()).toList();
                        });
        try (var worker =
                new CameraStatusWorker(
                        mock(RtspDescribeProbe.class),
                        mock(CameraMediaProbe.class),
                        platform,
                        new CameraStatusWorker.Limits(2, 1, Duration.ZERO))) {
            var batch = worker.trySubmitBatch(requests);
            try {
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                assertThat(batch).hasSize(100).allMatch(CameraStatusWorker.Submission::accepted);
                assertThat(claims).hasValue(100);
                assertThat(
                                worker.trySubmit(request(101, 1, 101))
                                        .completion()
                                        .get(2, TimeUnit.SECONDS)
                                        .reasonCode())
                        .isEqualTo("SOURCE_BUSY");
                assertThat(worker.cancel(50)).isTrue();
            } finally {
                release.countDown();
            }
            for (int index = 0; index < batch.size(); index++) {
                var result = batch.get(index).completion().get(2, TimeUnit.SECONDS);
                assertThat(result.outcome())
                        .isEqualTo(
                                index == 49
                                        ? CameraProbeResult.Outcome.CANCELLED
                                        : CameraProbeResult.Outcome.ACCESSIBLE);
            }
            verify(platform).executeBatch(anyList());
        }
    }

    @Test
    void failedBatchClaimDoesNotDiscardHealthyMembers() throws Exception {
        var platform = mock(CameraPlatformProbe.class);
        var first = new com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity();
        first.setId(1L);
        first.setSourceId(1L);
        var second = new com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity();
        second.setId(2L);
        second.setSourceId(1L);
        when(platform.executeBatch(anyList()))
                .thenAnswer(
                        call -> {
                            java.util.List<CameraPlatformProbe.Target> active = call.getArgument(0);
                            assertThat(active).hasSize(1);
                            assertThat(active.getFirst().profile().getId()).isEqualTo(2L);
                            return java.util.List.of(accessible());
                        });
        try (var worker =
                new CameraStatusWorker(
                        mock(RtspDescribeProbe.class),
                        mock(CameraMediaProbe.class),
                        platform,
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            var batch =
                    worker.trySubmitBatch(
                            java.util.List.of(
                                    CameraStatusWorker.Request.upstream(
                                            1,
                                            1,
                                            1,
                                            "revoked",
                                            first,
                                            Duration.ofSeconds(3),
                                            () -> {
                                                throw new CameraAdapterException("ACTOR_REVOKED");
                                            },
                                            () -> {}),
                                    CameraStatusWorker.Request.upstream(
                                            2,
                                            1,
                                            2,
                                            "allowed",
                                            second,
                                            Duration.ofSeconds(3),
                                            () -> {},
                                            () -> {})));
            assertThat(batch.getFirst().completion().get(2, TimeUnit.SECONDS).outcome())
                    .isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
            assertThat(batch.getLast().completion().get(2, TimeUnit.SECONDS).outcome())
                    .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
        }
    }

    @Test
    void mediaUsesTheSharedSourceGateUntilOwnedPullCleanupCompletes() throws Exception {
        var control = mock(RtspDescribeProbe.class);
        var media = mock(CameraMediaProbe.class);
        var stopped = new java.util.concurrent.CompletableFuture<Void>();
        var profile = new com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity();
        profile.setId(1L);
        profile.setSourceId(2L);
        when(media.execute(anyLong(), anyString(), any(), any(), anyLong(), any()))
                .thenAnswer(
                        invocation -> {
                            RtspDescribeProbe.Cancellation cancellation = invocation.getArgument(5);
                            assertThat(cancellation.claimResources(stopped, () -> {})).isTrue();
                            return new CameraProbeResult(
                                    CameraProbeResult.Outcome.NOT_EXECUTED,
                                    "MEDIA_EVIDENCE_INSUFFICIENT",
                                    0,
                                    0,
                                    null);
                        });
        try (var worker =
                new CameraStatusWorker(
                        control, media, new CameraStatusWorker.Limits(2, 2, Duration.ZERO))) {
            var result =
                    worker.trySubmit(
                                    CameraStatusWorker.Request.media(
                                            1,
                                            2,
                                            1,
                                            "trace1",
                                            profile,
                                            Duration.ofSeconds(18),
                                            () -> {},
                                            () -> {}))
                            .completion()
                            .get(2, TimeUnit.SECONDS);
            assertThat(result.reasonCode()).isEqualTo("MEDIA_EVIDENCE_INSUFFICIENT");
            assertThat(
                            worker.trySubmit(request(2, 2, 3))
                                    .completion()
                                    .get(2, TimeUnit.SECONDS)
                                    .reasonCode())
                    .isEqualTo("SOURCE_BUSY");
            assertThat(
                            worker.trySubmit(request(3, 4, 1))
                                    .completion()
                                    .get(2, TimeUnit.SECONDS)
                                    .reasonCode())
                    .isEqualTo("PROFILE_BUSY");
            stopped.complete(null);
            when(control.execute(any(), anyLong(), any())).thenReturn(accessible());
            assertThat(
                            worker.trySubmit(request(4, 2, 1))
                                    .completion()
                                    .get(2, TimeUnit.SECONDS)
                                    .reasonCode())
                    .isEqualTo("RTSP_ACCESSIBLE");
            verify(media).execute(eq(1L), eq("trace1"), same(profile), any(), anyLong(), any());
        }
    }

    @Test
    void deferredResolutionSharesWorkerGateAndDeadlineAndCancellationPreventsDescribe()
            throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var probe = mock(RtspDescribeProbe.class);
        var base = request(1, 1, 1);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(2, 1, Duration.ZERO))) {
            try {
                var submitted =
                        worker.trySubmit(
                                new CameraStatusWorker.Request(
                                        1,
                                        1,
                                        1,
                                        base.traceId(),
                                        base.target(),
                                        base.timeout(),
                                        () -> {},
                                        (deadline, active) -> {
                                            assertThat(Thread.currentThread().getName())
                                                    .isEqualTo("camera-status");
                                            assertThat(deadline - System.nanoTime())
                                                    .isPositive()
                                                    .isLessThanOrEqualTo(base.timeout().toNanos());
                                            entered.countDown();
                                            try {
                                                assertThat(release.await(2, TimeUnit.SECONDS))
                                                        .isTrue();
                                            } catch (InterruptedException ex) {
                                                throw new IllegalStateException(ex);
                                            }
                                            active.run();
                                            return base.target();
                                        }));
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                assertThat(
                                worker.trySubmit(request(2, 1, 2))
                                        .completion()
                                        .get(2, TimeUnit.SECONDS)
                                        .reasonCode())
                        .isEqualTo("SOURCE_BUSY");
                worker.cancel(1);
                release.countDown();
                assertThat(submitted.completion().get(2, TimeUnit.SECONDS).outcome())
                        .isEqualTo(CameraProbeResult.Outcome.CANCELLED);
                verifyNoInteractions(probe);
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void resolutionFailureIsNotAnRtspFailureFact() throws Exception {
        var probe = mock(RtspDescribeProbe.class);
        var base = request(1, 1, 1);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            var submitted =
                    worker.trySubmit(
                            new CameraStatusWorker.Request(
                                    1,
                                    1,
                                    1,
                                    base.traceId(),
                                    base.target(),
                                    base.timeout(),
                                    () -> {},
                                    (deadline, active) -> {
                                        throw new CameraAdapterException(
                                                "STREAM_URI_AUTH_REJECTED");
                                    }));
            var result = submitted.completion().get(2, TimeUnit.SECONDS);
            assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
            assertThat(result.reasonCode()).isEqualTo("STREAM_URI_AUTH_REJECTED");
            verifyNoInteractions(probe);
        }
    }

    private static CameraStatusWorker.Request request(long check, long source, long profile) {
        return request(check, source, profile, Duration.ofSeconds(5));
    }

    private static CameraStatusWorker.Request request(
            long check, long source, long profile, Duration timeout) {
        return new CameraStatusWorker.Request(
                check,
                source,
                profile,
                "test-trace",
                new RtspDescribeProbe.Target(
                        URI.create("rtsp://10.0.0.1/main"), "test", "user", "secret"),
                timeout);
    }

    private static CameraProbeResult accessible() {
        return new CameraProbeResult(
                CameraProbeResult.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 200, 1, null);
    }

    private static RtspDescribeProbe blocking(
            CountDownLatch entered, CountDownLatch release, AtomicReference<String> threadName) {
        var probe = mock(RtspDescribeProbe.class);
        when(probe.execute(any(), anyLong(), any()))
                .thenAnswer(
                        invocation -> {
                            if (threadName != null)
                                threadName.set(Thread.currentThread().getName());
                            entered.countDown();
                            assertThat(release.await(5, TimeUnit.SECONDS)).isTrue();
                            return accessible();
                        });
        return probe;
    }

    @Test
    void queueAndAdmissionAreBoundedAndNeverRunOnSubmittingThread() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var thread = new AtomicReference<String>();
        var probe = blocking(entered, release, thread);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            try {
                var first = worker.trySubmit(request(1, 1, 1));
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                var queued = worker.trySubmit(request(2, 2, 2));
                var rejected = worker.trySubmit(request(3, 3, 3));
                assertThat(first.accepted()).isTrue();
                assertThat(queued.accepted()).isTrue();
                assertThat(rejected.accepted()).isFalse();
                assertThat(rejected.reasonCode()).isEqualTo("WORKER_BUSY");
                assertThat(rejected.completion().get().outcome())
                        .isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
                for (int i = 0; i < 1000; i++) {
                    var burst = worker.trySubmit(request(i + 4, i + 4, i + 4));
                    assertThat(burst.accepted()).isFalse();
                    assertThat(burst.reasonCode()).isEqualTo("WORKER_BUSY");
                }
                verify(probe, times(1)).execute(any(), anyLong(), any());
                assertThat(thread.get()).isNotEqualTo(Thread.currentThread().getName());
                release.countDown();
                assertThat(first.completion().get(2, TimeUnit.SECONDS).outcome())
                        .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
                queued.completion().get(2, TimeUnit.SECONDS);
                verify(probe, times(2)).execute(any(), anyLong(), any());
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void duplicateActiveCheckReusesCompletionWithoutFillingQueue() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var probe = blocking(entered, release, null);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            try {
                var input = request(1, 1, 1);
                var first = worker.trySubmit(input);
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                for (int i = 0; i < 100; i++)
                    assertThat(worker.trySubmit(input).completion()).isSameAs(first.completion());
                assertThat(worker.trySubmit(request(1, 2, 2)).reasonCode())
                        .isEqualTo("CHECK_ID_CONFLICT");
                assertThat(worker.trySubmit(request(2, 2, 2)).accepted()).isTrue();
                release.countDown();
                first.completion().get(2, TimeUnit.SECONDS);
                verify(probe, timeout(2000).times(2)).execute(any(), anyLong(), any());
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void lifecycleHookRunsOnWorkerAndReplayIgnoresCallbackIdentity() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var hookThread = new AtomicReference<String>();
        var hookCalls = new AtomicInteger();
        var probe = blocking(entered, release, null);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            try {
                var base = request(1, 1, 1);
                var firstRequest =
                        new CameraStatusWorker.Request(
                                base.checkId(),
                                base.sourceId(),
                                base.streamProfileId(),
                                base.traceId(),
                                base.target(),
                                base.timeout(),
                                () -> {
                                    hookThread.set(Thread.currentThread().getName());
                                    hookCalls.incrementAndGet();
                                });
                var first = worker.trySubmit(firstRequest);
                assertThat(entered.await(1, TimeUnit.SECONDS)).isTrue();
                var equivalentTarget =
                        new RtspDescribeProbe.Target(
                                base.target().uri(),
                                base.target().networkPolicyKey(),
                                base.target().username(),
                                base.target().password(),
                                () -> {});
                var duplicate =
                        worker.trySubmit(
                                new CameraStatusWorker.Request(
                                        base.checkId(),
                                        base.sourceId(),
                                        base.streamProfileId(),
                                        base.traceId(),
                                        equivalentTarget,
                                        base.timeout(),
                                        () -> {
                                            throw new AssertionError(
                                                    "Replay hook must not execute");
                                        }));
                assertThat(duplicate.completion()).isSameAs(first.completion());
                assertThat(hookCalls.get()).isEqualTo(1);
                assertThat(hookThread.get())
                        .startsWith("camera-status")
                        .isNotEqualTo(Thread.currentThread().getName());
                release.countDown();
                assertThat(first.completion().get(1, TimeUnit.SECONDS).outcome())
                        .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void lifecycleRevocationNeverInvokesProbeOrTurnsIntoDeviceFailure() throws Exception {
        var probe = mock(RtspDescribeProbe.class);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            var base = request(1, 1, 1);
            var input =
                    new CameraStatusWorker.Request(
                            base.checkId(),
                            base.sourceId(),
                            base.streamProfileId(),
                            base.traceId(),
                            base.target(),
                            base.timeout(),
                            () -> {
                                throw new CameraAdapterException("ACTOR_REVOKED");
                            });
            var result = worker.trySubmit(input).completion().get(1, TimeUnit.SECONDS);
            assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
            assertThat(result.reasonCode()).isEqualTo("ACTOR_REVOKED");
            verifyNoInteractions(probe);
        }
    }

    @Test
    void sameSourceIsBusyUntilOriginalExecutionActuallyReturns() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var probe = blocking(entered, release, null);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(2, 1, Duration.ZERO))) {
            try {
                var first = worker.trySubmit(request(1, 1, 1));
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                var other = worker.trySubmit(request(2, 1, 2));
                assertThat(other.completion().get(2, TimeUnit.SECONDS).reasonCode())
                        .isEqualTo("SOURCE_BUSY");
                assertThat(worker.cancel(1)).isTrue();
                assertThat(first.completion()).isNotDone();
                verify(probe, times(1)).execute(any(), anyLong(), any());
                release.countDown();
                assertThat(first.completion().get(2, TimeUnit.SECONDS).outcome())
                        .isEqualTo(CameraProbeResult.Outcome.CANCELLED);
                worker.trySubmit(request(3, 1, 2)).completion().get(2, TimeUnit.SECONDS);
                verify(probe, times(2)).execute(any(), anyLong(), any());
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void sameProfileCannotBypassGateByChangingSource() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var probe = blocking(entered, release, null);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(2, 2, Duration.ZERO))) {
            try {
                var first = worker.trySubmit(request(1, 1, 1));
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                var other = worker.trySubmit(request(2, 2, 1));
                assertThat(other.completion().get(2, TimeUnit.SECONDS).reasonCode())
                        .isEqualTo("PROFILE_BUSY");
                release.countDown();
                first.completion().get(2, TimeUnit.SECONDS);
                worker.trySubmit(request(3, 2, 2)).completion().get(2, TimeUnit.SECONDS);
                verify(probe, times(2)).execute(any(), anyLong(), any());
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void timedOutDnsRetainsProfileAndSourceGatesUntilResolverActuallyExits() throws Exception {
        retainedDnsTargetGates(false);
    }

    @Test
    void cancelledDnsRetainsProfileAndSourceGatesUntilResolverActuallyExits() throws Exception {
        retainedDnsTargetGates(true);
    }

    private static void retainedDnsTargetGates(boolean cancel) throws Exception {
        var rules = mock(CameraSourceRules.class);
        when(rules.host(any(), any())).thenAnswer(call -> call.getArgument(0));
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var calls = new AtomicInteger();
        var probe =
                new RtspDescribeProbe(
                        rules,
                        new RtspDescribeProbe.Limits(2, 2, 2, 8192, 16384, 2048, 8),
                        host -> {
                            if (calls.incrementAndGet() == 1) {
                                entered.countDown();
                                boolean interrupted = false;
                                while (release.getCount() != 0) {
                                    try {
                                        release.await();
                                    } catch (InterruptedException ex) {
                                        interrupted = true;
                                    }
                                }
                                if (interrupted) Thread.currentThread().interrupt();
                            }
                            // This fixture never returns a connection address or opens a Socket.
                            throw new UnknownHostException("fixture-unavailable");
                        });
        var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(2, 2, Duration.ZERO));
        try {
            var first = worker.trySubmit(request(1, 1, 1, Duration.ofMillis(cancel ? 1500 : 150)));
            assertThat(entered.await(1, TimeUnit.SECONDS)).isTrue();
            if (cancel) assertThat(worker.cancel(1)).isTrue();
            assertThat(first.completion().get(1, TimeUnit.SECONDS).reasonCode())
                    .isEqualTo(cancel ? "CANCELLED" : "RTSP_TIMEOUT");
            assertThat(
                            worker.trySubmit(request(2, 1, 1))
                                    .completion()
                                    .get(1, TimeUnit.SECONDS)
                                    .reasonCode())
                    .isEqualTo("PROFILE_BUSY");
            assertThat(
                            worker.trySubmit(request(3, 2, 1))
                                    .completion()
                                    .get(1, TimeUnit.SECONDS)
                                    .reasonCode())
                    .isEqualTo("PROFILE_BUSY");
            assertThat(
                            worker.trySubmit(request(4, 1, 2))
                                    .completion()
                                    .get(1, TimeUnit.SECONDS)
                                    .reasonCode())
                    .isEqualTo("SOURCE_BUSY");
            assertThat(calls.get()).isEqualTo(1);
            release.countDown();
            CameraProbeResult afterRelease = null;
            long until = System.nanoTime() + TimeUnit.SECONDS.toNanos(1);
            long check = 5;
            do {
                afterRelease =
                        worker.trySubmit(request(check++, 1, 1))
                                .completion()
                                .get(1, TimeUnit.SECONDS);
                if (!afterRelease.reasonCode().equals("PROFILE_BUSY")) break;
                new CountDownLatch(1).await(5, TimeUnit.MILLISECONDS);
            } while (System.nanoTime() - until < 0);
            assertThat(afterRelease.reasonCode()).isEqualTo("DNS_UNAVAILABLE");
            assertThat(calls.get()).isEqualTo(2);
        } finally {
            release.countDown();
            worker.close();
            probe.close();
            assertThat(worker.awaitStopped(Duration.ofSeconds(2))).isTrue();
            assertThat(probe.awaitStopped(Duration.ofSeconds(2))).isTrue();
        }
    }

    @Test
    void cancelledQueuedCheckDoesNotProbeAndFreesOnlyItsQueueSlot() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var probe = blocking(entered, release, null);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            try {
                var first = worker.trySubmit(request(1, 1, 1));
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                var queued = worker.trySubmit(request(2, 2, 2));
                assertThat(worker.cancel(2)).isTrue();
                assertThat(queued.completion().get().outcome())
                        .isEqualTo(CameraProbeResult.Outcome.CANCELLED);
                var replacement = worker.trySubmit(request(3, 3, 3));
                assertThat(replacement.accepted()).isTrue();
                release.countDown();
                first.completion().get(2, TimeUnit.SECONDS);
                replacement.completion().get(2, TimeUnit.SECONDS);
                verify(probe, times(2)).execute(any(), anyLong(), any());
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void queueWaitingConsumesTheOriginalDeadline() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var probe = blocking(entered, release, null);
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            try {
                var first = worker.trySubmit(request(1, 1, 1));
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                var queued = worker.trySubmit(request(2, 2, 2, Duration.ofMillis(1)));
                assertThat(queued.completion().isDone()).isFalse();
                var pause = new CountDownLatch(1);
                pause.await(20, TimeUnit.MILLISECONDS);
                release.countDown();
                first.completion().get(2, TimeUnit.SECONDS);
                assertThat(queued.completion().get(2, TimeUnit.SECONDS).reasonCode())
                        .isEqualTo("QUEUE_DEADLINE_EXCEEDED");
                verify(probe, times(1)).execute(any(), anyLong(), any());
            } finally {
                release.countDown();
            }
        }
    }

    @Test
    void startBudgetDoesNotWaitOrTurnCapacityPressureIntoDeviceFailure() throws Exception {
        var probe = mock(RtspDescribeProbe.class);
        when(probe.execute(any(), anyLong(), any())).thenReturn(accessible());
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ofSeconds(10)))) {
            worker.trySubmit(request(1, 1, 1)).completion().get(2, TimeUnit.SECONDS);
            var result = worker.trySubmit(request(2, 2, 2)).completion().get(2, TimeUnit.SECONDS);
            assertThat(result.reasonCode()).isEqualTo("START_BUDGET_BUSY");
            assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
            verify(probe, times(1)).execute(any(), anyLong(), any());
        }
    }

    @Test
    void probeExceptionIsLocalAndDoesNotExposeItsMessage() throws Exception {
        var probe = mock(RtspDescribeProbe.class);
        when(probe.execute(any(), anyLong(), any()))
                .thenThrow(new IllegalStateException("rtsp://user:secret@host/private"));
        try (var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO))) {
            var result = worker.trySubmit(request(1, 1, 1)).completion().get(2, TimeUnit.SECONDS);
            assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
            assertThat(result.reasonCode()).isEqualTo("PROBE_UNAVAILABLE");
            assertThat(result.toString()).doesNotContain("secret", "private");
        }
    }

    @Test
    void closingCompletesQueuedChecksAndRejectsFurtherSubmission() throws Exception {
        var probe = mock(RtspDescribeProbe.class);
        var entered = new CountDownLatch(1);
        var interrupted = new AtomicInteger();
        when(probe.execute(any(), anyLong(), any()))
                .thenAnswer(
                        invocation -> {
                            entered.countDown();
                            try {
                                new CountDownLatch(1).await(5, TimeUnit.SECONDS);
                            } catch (InterruptedException ex) {
                                interrupted.incrementAndGet();
                            }
                            return accessible();
                        });
        var worker =
                new CameraStatusWorker(
                        probe,
                        org.mockito.Mockito.mock(CameraMediaProbe.class),
                        new CameraStatusWorker.Limits(1, 1, Duration.ZERO));
        try {
            var running = worker.trySubmit(request(1, 1, 1));
            assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
            var queued = worker.trySubmit(request(2, 2, 2));
            worker.close();
            assertThat(queued.completion().get(2, TimeUnit.SECONDS).outcome())
                    .isEqualTo(CameraProbeResult.Outcome.CANCELLED);
            assertThat(running.completion().get(2, TimeUnit.SECONDS).outcome())
                    .isEqualTo(CameraProbeResult.Outcome.CANCELLED);
            assertThat(interrupted.get()).isEqualTo(1);
            assertThat(worker.trySubmit(request(3, 3, 3)).reasonCode()).isEqualTo("WORKER_STOPPED");
            assertThat(worker.awaitStopped(Duration.ofSeconds(2))).isTrue();
            verify(probe, times(1)).execute(any(), anyLong(), any());
        } finally {
            worker.close();
        }
    }

    @Test
    void closeAndSubmissionRaceCompletesAllReceipts() throws Exception {
        var probe = mock(RtspDescribeProbe.class);
        when(probe.execute(any(), anyLong(), any())).thenReturn(accessible());
        for (int i = 0; i < 100; i++) {
            var worker =
                    new CameraStatusWorker(
                            probe,
                            org.mockito.Mockito.mock(CameraMediaProbe.class),
                            new CameraStatusWorker.Limits(1, 1, Duration.ZERO));
            var start = new CountDownLatch(1);
            var receipt = new AtomicReference<CameraStatusWorker.Submission>();
            var submitting =
                    new Thread(
                            () -> {
                                try {
                                    start.await();
                                    receipt.set(worker.trySubmit(request(1, 1, 1)));
                                } catch (InterruptedException ex) {
                                    Thread.currentThread().interrupt();
                                }
                            });
            submitting.start();
            start.countDown();
            worker.close();
            submitting.join(2000);
            assertThat(submitting.isAlive()).isFalse();
            assertThat(receipt.get()).isNotNull();
            receipt.get().completion().get(2, TimeUnit.SECONDS);
            assertThat(worker.awaitStopped(Duration.ofSeconds(2))).isTrue();
            assertThat(worker.trySubmit(request(2, 2, 2)).reasonCode()).isEqualTo("WORKER_STOPPED");
        }
    }

    @Test
    void limitsAndRequestRejectUnsafeValuesWithoutExposingTarget() {
        assertThatThrownBy(() -> new CameraStatusWorker.Limits(0, 1, Duration.ZERO))
                .isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> new CameraStatusWorker.Limits(1, 129, Duration.ZERO))
                .isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> request(0, 1, 1)).isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> request(1, 1, 1, Duration.ZERO))
                .isInstanceOf(IllegalArgumentException.class);
        assertThat(request(1, 1, 1).toString()).doesNotContain("user", "secret", "/main");
    }
}
