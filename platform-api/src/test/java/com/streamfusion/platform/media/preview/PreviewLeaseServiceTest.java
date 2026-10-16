package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewPullRegistry;
import com.streamfusion.platform.media.preview.media.PreviewTranscodeRegistry;
import com.streamfusion.platform.media.preview.source.PreviewLocatorService;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.service.UserService;
import java.net.URI;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class PreviewLeaseServiceTest {
    final PreviewAuthorization authorization = mock(PreviewAuthorization.class);
    final CameraChannelMapper channels = mock(CameraChannelMapper.class);
    final CameraProfileMapper profiles = mock(CameraProfileMapper.class);
    final PreviewLocatorService locators = mock(PreviewLocatorService.class);
    final PreviewMediaGateway media = mock(PreviewMediaGateway.class);
    final MediaRuntimeService runtimes = mock(MediaRuntimeService.class);
    final PreviewTranscodeRegistry transcodes = mock(PreviewTranscodeRegistry.class);
    final PreviewPullRegistry pulls = mock(PreviewPullRegistry.class);
    final MutableClock clock = new MutableClock();
    PreviewLeaseService service;
    CameraProfileEntity main;
    CameraProfileEntity sub;
    SessionPrincipalDto principal;

    @BeforeEach
    void setup() {
        principal = new SessionPrincipalDto(1L, 0L, clock.instant());
        var channel = new CameraChannelEntity();
        channel.setId(10L);
        channel.setLifecycle("ENABLED");
        when(channels.selectById(10L)).thenReturn(channel);
        main = profile(20, "MAIN");
        sub = profile(21, "SUB");
        when(profiles.selectList(any())).thenReturn(List.of(main, sub));
        when(profiles.selectById(20L)).thenReturn(main);
        when(profiles.selectById(21L)).thenReturn(sub);
        when(authorization.principal(10L)).thenReturn(principal);
        when(authorization.stillAllowed(any(), eq(10L))).thenReturn(true);
        var runtime =
                new MediaRuntimeService.ActiveZlm(
                        URI.create("http://127.0.0.1:8088"),
                        "secret",
                        Path.of("D:/runtime/MediaServer.exe"),
                        Path.of("D:/runtime/config.ini"),
                        "1");
        when(runtimes.activeZlm()).thenReturn(Optional.of(runtime));
        when(locators.connectionKey(any())).thenReturn("connection-1");
        when(locators.resolve(any(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            ((Runnable) invocation.getArgument(1)).run();
                            return new PreviewLocatorService.Resolved(
                                    URI.create("rtsp://10.0.0.1/live"));
                        });
        when(media.open(any(), anyString(), anyString(), any(), any()))
                .thenAnswer(
                        invocation ->
                                new PreviewMediaGateway.Stream(
                                        invocation.getArgument(0),
                                        invocation.getArgument(1),
                                        invocation.getArgument(2),
                                        "__defaultVhost__/"
                                                + invocation.getArgument(1)
                                                + "/"
                                                + invocation.getArgument(2)));
        when(media.stats(any(), anyString(), any())).thenReturn(stats("H264"));
        when(media.exists(any(), any())).thenReturn(true);
        // Preserve the simple close fixture while exercising the service's shared cleanup budget.
        doAnswer(
                        call -> {
                            ((PreviewDeadline) call.getArgument(2)).check();
                            media.close(call.getArgument(0), call.getArgument(1));
                            return null;
                        })
                .when(media)
                .close(any(), nullable(PreviewMediaGateway.Transcode.class), any());
        when(media.openViewer(any(), anyString(), anyString(), any()))
                .thenAnswer(
                        invocation -> {
                            PreviewMediaGateway.Stream upstream = invocation.getArgument(0);
                            String app = invocation.getArgument(1), id = invocation.getArgument(2);
                            return new PreviewMediaGateway.Stream(
                                    upstream.runtime(),
                                    app,
                                    id,
                                    "__defaultVhost__/" + app + "/" + id);
                        });
        service =
                new PreviewLeaseService(
                        authorization,
                        channels,
                        profiles,
                        locators,
                        media,
                        runtimes,
                        clock,
                        new PreviewProperties(36, 2),
                        transcodes,
                        pulls);
    }

    @AfterEach
    void close() {
        service.close();
    }

    @Test
    void twoOwnersShareOneRemotePullButKeepIndependentOutputsAndBudgets() {
        var first = service.create(input("MAIN"));
        when(authorization.principal(10L))
                .thenReturn(new SessionPrincipalDto(2L, 0L, clock.instant()));
        var second = service.create(input("MAIN"));
        assertThat(second.leaseId()).isNotEqualTo(first.leaseId());
        assertThat(service.capacity().occupiedSessions()).isEqualTo(2);
        verify(locators).resolve(any(), any(), any());
        var root = org.mockito.ArgumentCaptor.forClass(PreviewMediaGateway.Stream.class);
        verify(media, times(2)).openViewer(root.capture(), anyString(), anyString(), any());
        assertThat(root.getAllValues().get(0)).isSameAs(root.getAllValues().get(1));
        verify(media).open(any(), any(), any(), any(), any());

        service.delete(first.leaseId());
        verify(media)
                .close(argThat(s -> s.id().equals(first.leaseId().replace("-", ""))), isNull());
        verify(media, never()).close(same(root.getValue()), any());
        assertThat(service.renew(second.leaseId()).state()).isEqualTo("READY");
        assertThat(service.capacity().occupiedSessions()).isEqualTo(1);
        service.delete(second.leaseId());
        verify(media).close(same(root.getValue()), isNull());
        assertThat(service.capacity().occupiedSessions()).isZero();
    }

    @Test
    void restartBlocksNewPullsUntilExactOldResourcesAreReconciled(
            @org.junit.jupiter.api.io.TempDir Path directory) {
        service.close();
        var runtime = runtimes.activeZlm().orElseThrow();
        String oldApp = "sf_preview_abcdef012345", oldId = "a".repeat(32);
        var old =
                new PreviewMediaGateway.Stream(
                        runtime, oldApp, oldId, "__defaultVhost__/" + oldApp + "/" + oldId);
        var json = new com.fasterxml.jackson.databind.ObjectMapper();
        try (var prior = new PreviewPullRegistry(directory, json)) {
            prior.register(old, true);
            prior.started(old);
        }
        try (var restarted = new PreviewPullRegistry(directory, json)) {
            service =
                    new PreviewLeaseService(
                            authorization,
                            channels,
                            profiles,
                            locators,
                            media,
                            runtimes,
                            clock,
                            new PreviewProperties(36, 2),
                            transcodes,
                            restarted);
            assertThat(service.create(input("MAIN")).reasonCode())
                    .isEqualTo("PREVIEW_RECOVERY_REQUIRED");
            verify(media, never()).open(any(), any(), any(), any(), any());
            doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).closeRecovered(any(), any());
            service.reconcile();
            assertThat(restarted.pending()).hasSize(1);
            doNothing().when(media).closeRecovered(any(), any());
            service.reconcile();
            assertThat(restarted.pending()).isEmpty();
            verify(media, times(2)).closeRecovered(eq(old), any());
            var fresh = service.create(input("MAIN"));
            assertThat(fresh.state()).isEqualTo("READY");
            assertThatThrownBy(() -> service.renew("00000000-0000-0000-0000-000000000000"))
                    .isInstanceOf(BusinessException.class)
                    .extracting(e -> ((BusinessException) e).code())
                    .isEqualTo(ErrorCode.NOT_FOUND);
            // Reconciliation never enumerates media or closes new-process tuples.
            service.reconcile();
            verify(media, times(2)).closeRecovered(any(), any());
            service.delete(fresh.leaseId());
            service.close();
        }
    }

    @Test
    void concurrentViewersJoinThePendingPullWithoutOpeningAnotherRemoteConnection()
            throws Exception {
        var opening = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        when(media.open(any(), anyString(), anyString(), any(), any()))
                .thenAnswer(
                        call -> {
                            opening.countDown();
                            await(proceed);
                            String app = call.getArgument(1), id = call.getArgument(2);
                            return new PreviewMediaGateway.Stream(
                                    call.getArgument(0),
                                    app,
                                    id,
                                    "__defaultVhost__/" + app + "/" + id);
                        });
        var first = CompletableFuture.supplyAsync(() -> service.create(input("MAIN")));
        await(opening);
        var second = CompletableFuture.supplyAsync(() -> service.create(input("MAIN")));
        try {
            verify(authorization, timeout(1000).atLeast(2)).principal(10L);
            org.awaitility.Awaitility.await()
                    .atMost(java.time.Duration.ofSeconds(2))
                    .untilAsserted(
                            () -> assertThat(service.capacity().occupiedSessions()).isEqualTo(2));
            verify(media).open(any(), any(), any(), any(), any());
            verify(media, never()).openViewer(any(), any(), any(), any());
        } finally {
            proceed.countDown();
        }
        assertThat(first.get(2, TimeUnit.SECONDS).state()).isEqualTo("READY");
        assertThat(second.get(2, TimeUnit.SECONDS).state()).isEqualTo("READY");
        verify(media).open(any(), any(), any(), any(), any());
        verify(media, times(2)).openViewer(any(), any(), any(), any());
    }

    @Test
    void revokingOneOwnerClosesOnlyThatViewer() {
        var first = service.create(input("MAIN"));
        var other = new SessionPrincipalDto(2L, 0L, clock.instant());
        when(authorization.principal(10L)).thenReturn(other);
        var second = service.create(input("MAIN"));
        when(authorization.stillAllowed(any(), eq(10L)))
                .thenAnswer(call -> ((SessionPrincipalDto) call.getArgument(0)).getUserId() == 2L);
        service.reap();
        verify(media)
                .close(argThat(s -> s.id().equals(first.leaseId().replace("-", ""))), isNull());
        verify(media, times(1)).close(any(), any());
        assertThat(service.renew(second.leaseId()).state()).isEqualTo("READY");
    }

    @Test
    void failedLastUpstreamCleanupRetainsSlotAndBlocksReattachmentUntilConfirmed() {
        var first = service.create(input("MAIN"));
        String viewer = first.leaseId().replace("-", "");
        doAnswer(
                        call -> {
                            PreviewMediaGateway.Stream stream = call.getArgument(0);
                            if (!stream.id().equals(viewer))
                                throw new PreviewFailure("CLEANUP_PENDING");
                            return null;
                        })
                .when(media)
                .close(any(), any());
        assertThatThrownBy(() -> service.delete(first.leaseId())).hasMessage("CLEANUP_PENDING");
        assertThat(service.capacity().closingSessions()).isEqualTo(1);
        assertThat(service.create(input("MAIN")).reasonCode()).isEqualTo("PREVIEW_PENDING");
        verify(media).open(any(), any(), any(), any(), any());
        clock.instant = clock.instant.plusSeconds(61);
        doNothing().when(media).close(any(), any());
        service.delete(first.leaseId());
        // Only the root is retried; the already confirmed viewer is not closed again.
        verify(media).close(argThat(s -> s.id().equals(viewer)), isNull());
        assertThat(service.create(input("MAIN")).state()).isEqualTo("READY");
        verify(media, times(2)).open(any(), any(), any(), any(), any());
    }

    @Test
    void changedConfigurationGetsANewPullAndInvalidatesTheOldLease() {
        var first = service.create(input("MAIN"));
        when(locators.connectionKey(any())).thenReturn("connection-2");
        var second = service.create(input("MAIN"));
        verify(media, times(2)).open(any(), any(), any(), any(), any());
        assertThatThrownBy(() -> service.renew(first.leaseId()))
                .hasMessage("CONFIGURATION_CHANGED");
        assertThat(service.renew(second.leaseId()).state()).isEqualTo("READY");
    }

    @Test
    void changedConfigurationDuringRootOpenNeverStartsTheViewer() {
        when(locators.connectionKey(any())).thenReturn("connection-1", "connection-2");
        assertThat(service.create(input("MAIN")).reasonCode()).isEqualTo("CONFIGURATION_CHANGED");
        verify(media, never()).openViewer(any(), any(), any(), any());
        verify(media, timeout(1000)).close(any(), isNull());
    }

    @Test
    void failedViewerDoesNotDestroyAnAlreadyWorkingSharedPull() {
        var first = service.create(input("MAIN"));
        when(media.openViewer(any(), any(), any(), any()))
                .thenThrow(new PreviewFailure("ZLM_UNAVAILABLE"));
        assertThat(service.create(input("MAIN")).state()).isEqualTo("UNAVAILABLE");
        verify(media, timeout(1000))
                .close(argThat(s -> !s.id().equals(first.leaseId().replace("-", ""))), isNull());
        verify(media).open(any(), any(), any(), any(), any());
        assertThat(service.renew(first.leaseId()).state()).isEqualTo("READY");
    }

    @Test
    void missingGenerationCannotBeRenewedOrReusedAndCanBeRebuiltAfterCleanup() {
        var first = service.create(input("MAIN"));
        when(media.exists(any(), any())).thenReturn(false);
        assertThat(service.create(input("MAIN")).reasonCode()).isEqualTo("MEDIA_NOT_READY");
        assertThatThrownBy(() -> service.renew(first.leaseId()))
                .isInstanceOf(RuntimeException.class);
        verify(media).open(any(), any(), any(), any(), any());
        service.reap();
        org.awaitility.Awaitility.await()
                .atMost(java.time.Duration.ofSeconds(2))
                .untilAsserted(() -> assertThat(service.capacity().occupiedSessions()).isZero());
        when(media.exists(any(), any())).thenReturn(true);
        assertThat(service.create(input("MAIN")).state()).isEqualTo("READY");
        verify(media, times(2)).open(any(), any(), any(), any(), any());
    }

    @Test
    void joiningOwnersDoNotInheritTheCreatorsAuthorizationFailure() throws Exception {
        var resolving = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        doAnswer(
                        call -> {
                            resolving.countDown();
                            await(proceed);
                            throw BusinessException.error(ErrorCode.FORBIDDEN);
                        })
                .when(locators)
                .resolve(any(), any(), any());
        var first = CompletableFuture.supplyAsync(() -> service.create(input("MAIN")));
        await(resolving);
        var joined = new CountDownLatch(1);
        when(locators.connectionKey(any()))
                .thenAnswer(
                        call -> {
                            joined.countDown();
                            return "connection-1";
                        });
        var joiningThread = new AtomicReference<Thread>();
        var second =
                CompletableFuture.supplyAsync(
                        () -> {
                            joiningThread.set(Thread.currentThread());
                            return service.create(input("MAIN"));
                        });
        await(joined);
        try {
            org.awaitility.Awaitility.await()
                    .atMost(java.time.Duration.ofSeconds(2))
                    .until(() -> joiningThread.get().getState() == Thread.State.TIMED_WAITING);
        } finally {
            proceed.countDown();
        }
        assertThatThrownBy(() -> first.get(2, TimeUnit.SECONDS))
                .hasCauseInstanceOf(BusinessException.class);
        assertThat(second.get(2, TimeUnit.SECONDS).reasonCode()).isEqualTo("PREVIEW_UNAVAILABLE");
        verify(media, never()).open(any(), any(), any(), any(), any());
    }

    @Test
    void lastReleaseAndNewJoinCannotRaceIntoAClosingUpstream() throws Exception {
        var first = service.create(input("MAIN"));
        String viewerId = first.leaseId().replace("-", "");
        var rootClosing = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        doAnswer(
                        call -> {
                            PreviewMediaGateway.Stream stream = call.getArgument(0);
                            if (!stream.id().equals(viewerId)) {
                                rootClosing.countDown();
                                await(proceed);
                            }
                            return null;
                        })
                .when(media)
                .close(any(), any());
        var closing = CompletableFuture.runAsync(() -> service.delete(first.leaseId()));
        await(rootClosing);
        try {
            assertThat(service.create(input("MAIN")).reasonCode()).isEqualTo("PREVIEW_PENDING");
            verify(media).open(any(), any(), any(), any(), any());
            assertThat(service.capacity().closingSessions()).isGreaterThanOrEqualTo(1);
        } finally {
            proceed.countDown();
        }
        closing.get(2, TimeUnit.SECONDS);
        assertThat(service.create(input("MAIN")).state()).isEqualTo("READY");
        verify(media, times(2)).open(any(), any(), any(), any(), any());
    }

    @Test
    void browsersWithDifferentCodecSupportShareOnlyTheRemotePull() {
        when(media.stats(any(), anyString(), any()))
                .thenAnswer(
                        call ->
                                stats(
                                        ((String) call.getArgument(1)).endsWith("h264")
                                                ? "H264"
                                                : "H265"));
        when(media.transcode(any(), any(), any()))
                .thenAnswer(call -> new PreviewMediaGateway.Transcode(call.getArgument(1), "key"));
        var nativeRequest =
                new PreviewModels.CreateLease(
                        UUID.randomUUID().toString(), "10", null, "MAIN", List.of("H264", "H265"));
        var nativeViewer = service.create(nativeRequest);
        var convertedViewer = service.create(input("MAIN"));
        assertThat(nativeViewer.videoCodec()).isEqualTo("H265");
        assertThat(nativeViewer.transcoded()).isFalse();
        assertThat(convertedViewer.videoCodec()).isEqualTo("H264");
        assertThat(convertedViewer.transcoded()).isTrue();
        verify(media).open(any(), any(), any(), any(), any());
        verify(media).transcode(any(), any(), any());
        service.delete(nativeViewer.leaseId());
        assertThat(service.renew(convertedViewer.leaseId()).state()).isEqualTo("READY");
    }

    @Test
    void viewerAndLastRootCleanupShareOneFiniteBudget() {
        var created = service.create(input("MAIN"));
        service.delete(created.leaseId());
        var deadlines = org.mockito.ArgumentCaptor.forClass(PreviewDeadline.class);
        verify(media, times(2))
                .close(any(), nullable(PreviewMediaGateway.Transcode.class), deadlines.capture());
        assertThat(deadlines.getAllValues().get(0)).isSameAs(deadlines.getAllValues().get(1));
        assertThat(deadlines.getValue().remaining())
                .isLessThanOrEqualTo(java.time.Duration.ofSeconds(12));
    }

    @Test
    void globalBudgetCountsAllUsersButDoesNotDoubleCountIdempotentReplay() {
        useLimits(1, 2);
        var request = input("MAIN");
        var first = service.create(request);
        assertThat(service.create(request).leaseId()).isEqualTo(first.leaseId());
        when(authorization.principal(10L))
                .thenReturn(new SessionPrincipalDto(2L, 0L, clock.instant()));
        var failure =
                catchThrowableOfType(() -> service.create(input("MAIN")), BusinessException.class);
        assertThat(failure.code()).isEqualTo(ErrorCode.RATE_LIMITED);
        assertThat(failure.diagnosticReason()).isEqualTo("PREVIEW_CAPACITY_EXCEEDED");
        assertThat(service.capacity().occupiedSessions()).isEqualTo(1);
        verify(media, times(1)).open(any(), any(), any(), any(), any());
        service.delete(first.leaseId());
        assertThat(service.capacity().occupiedSessions()).isZero();
        assertThat(service.create(input("MAIN")).state()).isEqualTo("READY");
    }

    @Test
    void rapidCreateAndReleaseDoesNotConfuseReceiptHistoryWithOccupiedSessions() {
        useLimits(1, 2);
        var startedAt = clock.instant();
        for (int attempt = 0; attempt < 129; attempt++) {
            var created = service.create(input("MAIN"));
            assertThat(created.state()).isEqualTo("READY");
            assertThat(service.capacity().occupiedSessions()).isEqualTo(1);
            service.delete(created.leaseId());
            assertThat(service.capacity().occupiedSessions()).isZero();
        }
        // No receipt expired: the old 128-entry coupling would reject the 129th request.
        assertThat(clock.instant()).isEqualTo(startedAt);
        verify(media, times(129)).open(any(), any(), any(), any(), any());
        verify(media, times(258)).close(any(), isNull());
    }

    @Test
    void creationReservesTheGlobalSlotBeforeContactingMedia() throws Exception {
        useLimits(1, 2);
        var started = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        when(media.open(any(), any(), any(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            started.countDown();
                            await(proceed);
                            return new PreviewMediaGateway.Stream(
                                    invocation.getArgument(0),
                                    invocation.getArgument(1),
                                    invocation.getArgument(2),
                                    "proxy");
                        });
        var result = CompletableFuture.supplyAsync(() -> service.create(input("MAIN")));
        try {
            await(started);
            assertThat(service.capacity().occupiedSessions()).isEqualTo(1);
            assertThat(service.capacity().closingSessions()).isZero();
            assertThatThrownBy(() -> service.create(input("MAIN")))
                    .isInstanceOf(BusinessException.class);
            verify(media, times(1)).open(any(), any(), any(), any(), any());
        } finally {
            proceed.countDown();
        }
        assertThat(result.get(2, TimeUnit.SECONDS).state()).isEqualTo("READY");
    }

    @Test
    void closingSlotIsNotReleasedUntilCleanupHasBeenConfirmed() {
        useLimits(1, 2);
        var created = service.create(input("MAIN"));
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).close(any(), any());
        assertThatThrownBy(() -> service.delete(created.leaseId())).hasMessage("CLEANUP_PENDING");
        assertThat(service.capacity().occupiedSessions()).isEqualTo(1);
        assertThat(service.capacity().closingSessions()).isEqualTo(1);
        assertThatThrownBy(() -> service.create(input("SUB")))
                .isInstanceOf(BusinessException.class);
        clock.instant = clock.instant.plusSeconds(61);
        doNothing().when(media).close(any(), any());
        service.delete(created.leaseId());
        assertThat(service.capacity().occupiedSessions()).isZero();
    }

    @Test
    void capacityReportsUnknownTranscodesInsteadOfInventingAnEmptyJournal() {
        when(transcodes.occupied()).thenReturn(2);
        var first = service.capacity();
        assertThat(first.maxSessions()).isEqualTo(36);
        assertThat(first.maxTranscodes()).isEqualTo(2);
        assertThat(first.occupiedTranscodes()).isEqualTo(2);
        assertThat(first.transcodesAvailable()).isTrue();
        assertThat(first.observedAt()).isEqualTo(clock.instant());
        when(transcodes.occupied()).thenThrow(new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED"));
        var unavailable = service.capacity();
        assertThat(unavailable.occupiedTranscodes()).isNull();
        assertThat(unavailable.transcodesAvailable()).isFalse();
        verify(authorization, times(2)).capacity();
        assertThat(service.create(input("MAIN")).state()).isEqualTo("READY");
    }

    @Test
    void deniedCapacityReadDoesNotReadGlobalUsage() {
        doThrow(BusinessException.error(ErrorCode.FORBIDDEN)).when(authorization).capacity();
        assertThatThrownBy(service::capacity).isInstanceOf(BusinessException.class);
        verifyNoInteractions(transcodes);
    }

    private void useLimits(int sessions, int transcodeLimit) {
        service.close();
        service =
                new PreviewLeaseService(
                        authorization,
                        channels,
                        profiles,
                        locators,
                        media,
                        runtimes,
                        clock,
                        new PreviewProperties(sessions, transcodeLimit),
                        transcodes,
                        pulls);
    }

    @Test
    void managementSelectsSubAndCenterSelectsMainWithoutChangingProfiles() {
        assertThat(service.create(input("SUB")).streamProfileId()).isEqualTo("21");
        assertThat(service.create(input("MAIN")).streamProfileId()).isEqualTo("20");
        verify(locators).resolve(eq(sub), any(), any());
        verify(locators).resolve(eq(main), any(), any());
    }

    @Test
    void sameRequestReturnsSameLeaseAndChangedInputConflicts() {
        var request = input("MAIN");
        var first = service.create(request);
        assertThat(service.create(request).leaseId()).isEqualTo(first.leaseId());
        verify(media, times(1)).open(any(), any(), any(), any(), any());
        var changed =
                new PreviewModels.CreateLease(
                        request.clientRequestId(), "10", null, "SUB", List.of("H264"));
        assertThatThrownBy(() -> service.create(changed))
                .isInstanceOf(BusinessException.class)
                .extracting(e -> ((BusinessException) e).code())
                .isEqualTo(ErrorCode.CONFLICT);
    }

    @Test
    void zlmFailureNeverReturnsReadyOrInventsPlaybackUrl() {
        when(media.open(any(), any(), any(), any(), any()))
                .thenThrow(new PreviewFailure("ZLM_UNAVAILABLE"));
        var result = service.create(input("MAIN"));
        assertThat(result.state()).isEqualTo("UNAVAILABLE");
        assertThat(result.reasonCode()).isEqualTo("ZLM_UNAVAILABLE");
        assertThat(result.leaseId()).isNull();
        verify(media, timeout(1000)).close(any(), isNull());
    }

    @Test
    void actualH265ControlsCompatibilityEvenWhenCatalogSaysH264() {
        when(media.stats(any(), any(), any())).thenReturn(stats("H265"), stats("H264"));
        when(media.transcode(any(), anyString(), any()))
                .thenAnswer(
                        invocation ->
                                new PreviewMediaGateway.Transcode(
                                        invocation.getArgument(1),
                                        "0123456789abcdef0123456789abcdef"));
        var result = service.create(input("MAIN"));
        assertThat(result.state()).isEqualTo("READY");
        assertThat(result.transcoded()).isTrue();
        assertThat(result.videoCodec()).isEqualTo("H264");
        verify(media).transcode(any(), matches("[a-f0-9]{32}h264"), any());
    }

    @Test
    void h265NativeBrowserDoesNotTranscode() {
        when(media.stats(any(), any(), any())).thenReturn(stats("H265"));
        var request =
                new PreviewModels.CreateLease(
                        UUID.randomUUID().toString(), "10", null, "MAIN", List.of("H264", "H265"));
        var result = service.create(request);
        assertThat(result.videoCodec()).isEqualTo("H265");
        assertThat(result.transcoded()).isFalse();
        verify(media, never()).transcode(any(), any(), any());
    }

    @Test
    void missingTranscoderIsAnHonestFailure() {
        when(media.stats(any(), any(), any())).thenReturn(stats("H265"));
        when(media.transcode(any(), any(), any()))
                .thenThrow(new PreviewFailure("TRANSCODE_NOT_CONFIGURED"));
        var result = service.create(input("MAIN"));
        assertThat(result.state()).isEqualTo("UNAVAILABLE");
        assertThat(result.reasonCode()).isEqualTo("TRANSCODE_NOT_CONFIGURED");
        verify(media, timeout(1000).times(2)).close(any(), isNull());
    }

    @Test
    void expirationAndRevokedScopesForceMediaCleanup() {
        service.create(input("MAIN"));
        clock.instant = clock.instant.plusSeconds(91);
        service.reap();
        verify(media, times(2)).close(any(), isNull());
        service.create(input("SUB"));
        when(authorization.stillAllowed(any(), anyLong())).thenReturn(false);
        service.reap();
        verify(media, times(4)).close(any(), isNull());
    }

    @Test
    void otherOwnerCannotStopOrAccessLease() {
        var created = service.create(input("MAIN"));
        doThrow(new PreviewFailure("LEASE_NOT_FOUND")).when(authorization).request(any(), eq(10L));
        assertThatThrownBy(() -> service.delete(created.leaseId()))
                .isInstanceOf(PreviewFailure.class);
        verify(media, never()).close(any(), any());
    }

    @Test
    void signalCanOnlyBeCreatedOnceAndNoSdpIsLogged() {
        var created = service.create(input("MAIN"));
        when(media.answer(any(), any(), any(), any()))
                .thenReturn(new PreviewModels.Answer("v=0\r\n", "answer"));
        assertThat(service.answer(created.leaseId(), new PreviewModels.Offer("v=0\r\n")).type())
                .isEqualTo("answer");
        assertThatThrownBy(
                        () -> service.answer(created.leaseId(), new PreviewModels.Offer("v=0\r\n")))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("SIGNAL_ALREADY_CREATED");
    }

    @Test
    void failedCleanupIsRetainedAndRetriedInsteadOfReportedAsReleased() {
        var created = service.create(input("MAIN"));
        doThrow(new PreviewFailure("ZLM_UNAVAILABLE")).doNothing().when(media).close(any(), any());
        assertThatThrownBy(() -> service.delete(created.leaseId()))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("CLEANUP_PENDING");
        assertThatThrownBy(() -> service.delete(created.leaseId()))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("CLEANUP_PENDING");
        verify(media, times(1)).close(any(), any());
        clock.instant = clock.instant.plusSeconds(20);
        service.delete(created.leaseId());
        verify(media, times(3)).close(any(), any());
        service.delete(created.leaseId());
        verify(media, times(3)).close(any(), any());
    }

    @Test
    void disabledChannelsNeverResolveOrContactZlm() {
        var disabled = new CameraChannelEntity();
        disabled.setId(10L);
        disabled.setLifecycle("DISABLED");
        when(channels.selectById(10L)).thenReturn(disabled);
        assertThat(service.create(input("MAIN")).reasonCode()).isEqualTo("CAMERA_DISABLED");
        verifyNoInteractions(locators, media);
    }

    @Test
    void requestIdCaseDoesNotChangeIdempotentPayload() {
        var original = input("MAIN");
        var result = service.create(original);
        var replay =
                new PreviewModels.CreateLease(
                        original.clientRequestId().toUpperCase(java.util.Locale.ROOT),
                        "10",
                        null,
                        "MAIN",
                        original.videoCodecs());
        assertThat(service.create(replay).leaseId()).isEqualTo(result.leaseId());
        verify(media, times(1)).open(any(), any(), any(), any(), any());
    }

    @Test
    void idempotentReplayPreservesOriginalBusinessFailure() {
        var request = input("MAIN");
        var denied = BusinessException.error(ErrorCode.FORBIDDEN);
        doThrow(denied).when(authorization).request(any(), eq(10L));
        assertThatThrownBy(() -> service.create(request)).isSameAs(denied);
        assertThatThrownBy(() -> service.create(request)).isSameAs(denied);
        verifyNoInteractions(media);
    }

    @Test
    void openingTracksAndTranscodingShareOneBudget() {
        var deadline = new AtomicReference<PreviewDeadline>();
        doAnswer(
                        invocation -> {
                            deadline.set(invocation.getArgument(2));
                            return new PreviewLocatorService.Resolved(
                                    URI.create("rtsp://10.0.0.1/live"));
                        })
                .when(locators)
                .resolve(any(), any(), any());
        when(media.stats(any(), any(), any())).thenReturn(stats("H265"), stats("H264"));
        when(media.transcode(any(), any(), any()))
                .thenReturn(new PreviewMediaGateway.Transcode("out", "key"));
        assertThat(service.create(input("MAIN")).state()).isEqualTo("READY");
        verify(media).open(any(), any(), any(), any(), same(deadline.get()));
        verify(media, times(2)).stats(any(), any(), same(deadline.get()));
        verify(media).transcode(any(), any(), same(deadline.get()));
        assertThat(deadline.get().remaining()).isLessThan(java.time.Duration.ofSeconds(18));
    }

    @Test
    void revokedLeaseDoesNotWaitForOpenLockAndCleansLateSuccess() throws Exception {
        var started = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        var late = new AtomicReference<PreviewMediaGateway.Stream>();
        when(media.open(any(), any(), any(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            started.countDown();
                            await(proceed);
                            var stream =
                                    new PreviewMediaGateway.Stream(
                                            invocation.getArgument(0),
                                            invocation.getArgument(1),
                                            invocation.getArgument(2),
                                            "late-proxy-key");
                            late.set(stream);
                            return stream;
                        });
        var result = CompletableFuture.supplyAsync(() -> service.create(input("MAIN")));
        try {
            await(started);
            when(authorization.stillAllowed(any(), anyLong())).thenReturn(false);
            CompletableFuture.runAsync(service::reap).get(1, TimeUnit.SECONDS);
            verify(media, never()).close(any(), any());
        } finally {
            proceed.countDown();
        }
        assertThat(result.get(2, TimeUnit.SECONDS).reasonCode()).isEqualTo("CANCELLED");
        verify(media, timeout(1000)).close(same(late.get()), isNull());
        verify(media, never()).stats(any(), any(), any());
    }

    @Test
    void revokedLeaseRetainsLateTranscodeForCleanup() throws Exception {
        var started = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        var output = new PreviewMediaGateway.Transcode("h264", "late-process-key");
        when(media.stats(any(), any(), any())).thenReturn(stats("H265"));
        when(media.transcode(any(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            started.countDown();
                            await(proceed);
                            return output;
                        });
        var result = CompletableFuture.supplyAsync(() -> service.create(input("MAIN")));
        try {
            await(started);
            when(authorization.stillAllowed(any(), anyLong())).thenReturn(false);
            CompletableFuture.runAsync(service::reap).get(1, TimeUnit.SECONDS);
            verify(media, never()).close(any(), any());
        } finally {
            proceed.countDown();
        }
        assertThat(result.get(2, TimeUnit.SECONDS).state()).isEqualTo("UNAVAILABLE");
        verify(media, timeout(1000)).close(any(), same(output));
    }

    @Test
    void unknownTranscodeOutcomeRetainsItsCleanupHandle() {
        var output = new PreviewMediaGateway.Transcode("h264", "pending-process-key");
        when(media.stats(any(), any(), any())).thenReturn(stats("H265"));
        when(media.transcode(any(), any(), any()))
                .thenThrow(new PreviewFailure("PREVIEW_TIMEOUT", output));
        assertThat(service.create(input("MAIN")).reasonCode()).isEqualTo("PREVIEW_TIMEOUT");
        verify(media, timeout(1000)).close(any(), same(output));
    }

    @Test
    void deleteCancelsBlockedSignalWithoutWaitingForNetworkLock() throws Exception {
        var created = service.create(input("MAIN"));
        var started = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        var deadline = new AtomicReference<PreviewDeadline>();
        when(media.answer(any(), any(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            deadline.set(invocation.getArgument(3));
                            started.countDown();
                            await(proceed);
                            return new PreviewModels.Answer("v=0\r\n", "answer");
                        });
        var answer =
                CompletableFuture.supplyAsync(
                        () ->
                                service.answer(
                                        created.leaseId(), new PreviewModels.Offer("v=0\r\n")));
        try {
            await(started);
            CompletableFuture.runAsync(
                            () ->
                                    assertThatThrownBy(() -> service.delete(created.leaseId()))
                                            .isInstanceOf(PreviewFailure.class)
                                            .hasMessage("CLEANUP_PENDING"))
                    .get(1, TimeUnit.SECONDS);
            assertThatThrownBy(() -> deadline.get().check()).hasMessage("CANCELLED");
            verify(media, never()).close(any(), any());
        } finally {
            proceed.countDown();
        }
        assertThatThrownBy(() -> answer.get(2, TimeUnit.SECONDS))
                .hasCauseInstanceOf(PreviewFailure.class);
        verify(media, timeout(1000).times(2)).close(any(), isNull());
        service.delete(created.leaseId());
    }

    @Test
    void anotherOwnerCannotRetryCleanupOfAClosingLease() {
        var created = service.create(input("MAIN"));
        doThrow(new PreviewFailure("ZLM_UNAVAILABLE")).when(media).close(any(), any());
        assertThatThrownBy(() -> service.delete(created.leaseId())).hasMessage("CLEANUP_PENDING");
        doThrow(new PreviewFailure("LEASE_NOT_FOUND")).when(authorization).request(any(), eq(10L));
        assertThatThrownBy(() -> service.delete(created.leaseId())).hasMessage("LEASE_NOT_FOUND");
        verify(media, times(1)).close(any(), any());
    }

    @Test
    void nonOwnerWithoutCameraPageCannotCloseAnotherUsersStream() {
        service.close();
        var cameraAccess = mock(CameraAccessService.class);
        var current = mock(CurrentUserService.class);
        var actualAuthorization =
                new PreviewAuthorization(
                        cameraAccess, current, mock(AccessService.class), mock(UserService.class));
        when(current.principal()).thenReturn(principal);
        when(cameraAccess.readActor()).thenReturn(new CameraAccessService.Actor(1L, false));
        service =
                new PreviewLeaseService(
                        actualAuthorization,
                        channels,
                        profiles,
                        locators,
                        media,
                        runtimes,
                        clock,
                        new PreviewProperties(36, 2),
                        transcodes,
                        pulls);
        var created = service.create(input("MAIN"));
        when(current.principal()).thenReturn(new SessionPrincipalDto(2L, 0L, clock.instant()));
        when(cameraAccess.readActor()).thenThrow(BusinessException.error(ErrorCode.FORBIDDEN));
        assertThatThrownBy(() -> service.delete(created.leaseId())).hasMessage("LEASE_NOT_FOUND");
        assertThatThrownBy(() -> service.stats(created.leaseId())).hasMessage("LEASE_NOT_FOUND");
        verify(media, never()).close(any(), any());
        when(current.principal()).thenReturn(principal);
        doReturn(new CameraAccessService.Actor(1L, false)).when(cameraAccess).readActor();
        service.delete(created.leaseId());
        verify(media, times(2)).close(any(), isNull());
    }

    @Test
    void sameOwnerWithRevokedPageStillSchedulesCleanup() {
        var created = service.create(input("MAIN"));
        var denied = BusinessException.error(ErrorCode.FORBIDDEN);
        doThrow(denied).when(authorization).request(any(), eq(10L));
        assertThatThrownBy(() -> service.stats(created.leaseId())).isSameAs(denied);
        verify(media, timeout(1000).times(2)).close(any(), isNull());
    }

    @Test
    void manualDeleteCanRecoverAfterAutomaticCleanupBudgetIsExhausted() {
        var created = service.create(input("MAIN"));
        doThrow(new PreviewFailure("ZLM_UNAVAILABLE")).when(media).close(any(), any());
        assertThatThrownBy(() -> service.delete(created.leaseId())).hasMessage("CLEANUP_PENDING");
        for (int retry = 0; retry < 5; retry++) {
            clock.instant = clock.instant.plusSeconds(61);
            service.reap();
        }
        verify(media, times(6)).close(any(), any());
        clock.instant = clock.instant.plusSeconds(61);
        service.reap();
        service.reap();
        verify(media, times(6)).close(any(), any());
        assertThatThrownBy(() -> service.delete(created.leaseId())).hasMessage("CLEANUP_PENDING");
        verify(media, times(7)).close(any(), any());
        clock.instant = clock.instant.plusSeconds(61);
        service.reap();
        verify(media, times(7)).close(any(), any());
        doNothing().when(media).close(any(), any());
        service.delete(created.leaseId());
        verify(media, times(9)).close(any(), any());
        service.delete(created.leaseId());
        verify(media, times(9)).close(any(), any());
    }

    @Test
    void failedSignalClosesTheLease() {
        var created = service.create(input("MAIN"));
        when(media.answer(any(), any(), any(), any()))
                .thenThrow(new PreviewFailure("ZLM_UNAVAILABLE"));
        assertThatThrownBy(
                        () -> service.answer(created.leaseId(), new PreviewModels.Offer("v=0\r\n")))
                .hasMessage("ZLM_UNAVAILABLE");
        verify(media, timeout(1000).times(2)).close(any(), isNull());
        assertThatThrownBy(() -> service.renew(created.leaseId()))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void stoppedServiceRejectsNewLeasesWithoutOpeningMedia() {
        service.close();
        assertThatThrownBy(() -> service.create(input("MAIN"))).hasMessage("PREVIEW_UNAVAILABLE");
        verifyNoInteractions(media);
    }

    @Test
    void failedCreateReturnsItsResultWhileCleanupRunsSeparately() throws Exception {
        var cleanupStarted = new CountDownLatch(1);
        var finishCleanup = new CountDownLatch(1);
        when(media.open(any(), any(), any(), any(), any()))
                .thenThrow(new PreviewFailure("PREVIEW_TIMEOUT"));
        doAnswer(
                        invocation -> {
                            cleanupStarted.countDown();
                            await(finishCleanup);
                            return null;
                        })
                .when(media)
                .close(any(), any());
        var request = input("MAIN");
        try {
            var result =
                    CompletableFuture.supplyAsync(() -> service.create(request))
                            .get(1, TimeUnit.SECONDS);
            assertThat(result.reasonCode()).isEqualTo("PREVIEW_TIMEOUT");
            await(cleanupStarted);
            assertThat(service.create(request).reasonCode()).isEqualTo("PREVIEW_TIMEOUT");
        } finally {
            finishCleanup.countDown();
        }
    }

    @Test
    void failedSignalDoesNotAddCleanupTimeoutToItsResponseBudget() throws Exception {
        var created = service.create(input("MAIN"));
        var cleanupStarted = new CountDownLatch(1);
        var finishCleanup = new CountDownLatch(1);
        when(media.answer(any(), any(), any(), any()))
                .thenThrow(new PreviewFailure("PREVIEW_TIMEOUT"));
        doAnswer(
                        invocation -> {
                            cleanupStarted.countDown();
                            await(finishCleanup);
                            return null;
                        })
                .when(media)
                .close(any(), any());
        try {
            CompletableFuture.runAsync(
                            () ->
                                    assertThatThrownBy(
                                                    () ->
                                                            service.answer(
                                                                    created.leaseId(),
                                                                    new PreviewModels.Offer(
                                                                            "v=0\r\n")))
                                            .hasMessage("PREVIEW_TIMEOUT"))
                    .get(1, TimeUnit.SECONDS);
            await(cleanupStarted);
            assertThatThrownBy(() -> service.renew(created.leaseId()))
                    .isInstanceOf(BusinessException.class);
        } finally {
            finishCleanup.countDown();
        }
    }

    private static void await(CountDownLatch latch) {
        try {
            assertThat(latch.await(3, TimeUnit.SECONDS)).isTrue();
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new AssertionError(ex);
        }
    }

    private PreviewModels.CreateLease input(String usage) {
        return new PreviewModels.CreateLease(
                UUID.randomUUID().toString(), "10", null, usage, List.of("H264"));
    }

    private static CameraProfileEntity profile(long id, String usage) {
        var value = new CameraProfileEntity();
        value.setId(id);
        value.setChannelId(10L);
        value.setSourceId(30L);
        value.setUsageHint(usage);
        value.setUsageOrigin("DEVICE_REPORTED");
        value.setEnabled(true);
        value.setVideoCodec("H264");
        return value;
    }

    private static PreviewModels.Stats stats(String codec) {
        return new PreviewModels.Stats("READY", codec, 1920, 1080, 25.0, 1000.0, 0, Instant.now());
    }

    static final class MutableClock extends Clock {
        Instant instant = Instant.parse("2026-10-11T00:00:00Z");

        @Override
        public ZoneId getZone() {
            return ZoneOffset.UTC;
        }

        @Override
        public Clock withZone(ZoneId zone) {
            return this;
        }

        @Override
        public Instant instant() {
            return instant;
        }
    }
}
