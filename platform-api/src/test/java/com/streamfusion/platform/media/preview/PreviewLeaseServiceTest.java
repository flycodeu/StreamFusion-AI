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
        service =
                new PreviewLeaseService(
                        authorization, channels, profiles, locators, media, runtimes, clock);
    }

    @AfterEach
    void close() {
        service.close();
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
        verify(media, timeout(1000)).close(any(), isNull());
    }

    @Test
    void expirationAndRevokedScopesForceMediaCleanup() {
        service.create(input("MAIN"));
        clock.instant = clock.instant.plusSeconds(91);
        service.reap();
        verify(media).close(any(), isNull());
        service.create(input("SUB"));
        when(authorization.stillAllowed(any(), anyLong())).thenReturn(false);
        service.reap();
        verify(media, times(2)).close(any(), isNull());
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
        verify(media, times(2)).close(any(), any());
        service.delete(created.leaseId());
        verify(media, times(2)).close(any(), any());
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
        verify(media, timeout(1000)).close(any(), isNull());
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
                        actualAuthorization, channels, profiles, locators, media, runtimes, clock);
        var created = service.create(input("MAIN"));
        when(current.principal()).thenReturn(new SessionPrincipalDto(2L, 0L, clock.instant()));
        when(cameraAccess.readActor()).thenThrow(BusinessException.error(ErrorCode.FORBIDDEN));
        assertThatThrownBy(() -> service.delete(created.leaseId())).hasMessage("LEASE_NOT_FOUND");
        assertThatThrownBy(() -> service.stats(created.leaseId())).hasMessage("LEASE_NOT_FOUND");
        verify(media, never()).close(any(), any());
        when(current.principal()).thenReturn(principal);
        doReturn(new CameraAccessService.Actor(1L, false)).when(cameraAccess).readActor();
        service.delete(created.leaseId());
        verify(media).close(any(), isNull());
    }

    @Test
    void sameOwnerWithRevokedPageStillSchedulesCleanup() {
        var created = service.create(input("MAIN"));
        var denied = BusinessException.error(ErrorCode.FORBIDDEN);
        doThrow(denied).when(authorization).request(any(), eq(10L));
        assertThatThrownBy(() -> service.stats(created.leaseId())).isSameAs(denied);
        verify(media, timeout(1000)).close(any(), isNull());
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
        verify(media, times(8)).close(any(), any());
        service.delete(created.leaseId());
        verify(media, times(8)).close(any(), any());
    }

    @Test
    void failedSignalClosesTheLease() {
        var created = service.create(input("MAIN"));
        when(media.answer(any(), any(), any(), any()))
                .thenThrow(new PreviewFailure("ZLM_UNAVAILABLE"));
        assertThatThrownBy(
                        () -> service.answer(created.leaseId(), new PreviewModels.Offer("v=0\r\n")))
                .hasMessage("ZLM_UNAVAILABLE");
        verify(media, timeout(1000)).close(any(), isNull());
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
