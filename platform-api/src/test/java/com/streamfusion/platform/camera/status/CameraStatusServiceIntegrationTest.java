package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.support.CameraTestSupport;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.temporal.ChronoUnit;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.context.ApplicationContext;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.session.MapSession;
import org.springframework.session.MapSessionRepository;
import org.springframework.session.Session;
import org.springframework.session.SessionRepository;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.transaction.PlatformTransactionManager;

/** Real HTTP/auth, MyBatis, crypto and SQL; no real device or Redis/provider acceptance claimed. */
class CameraStatusServiceIntegrationTest extends CameraTestSupport {
    @MockitoBean private CameraStatusWorker worker;
    @Autowired private ApplicationContext context;
    @Autowired private CameraStatusCheckMapper checks;
    @Autowired private CameraStatusSnapshotMapper snapshots;
    @Autowired private CameraChannelMapper channels;
    @Autowired private CameraProfileMapper profiles;
    @Autowired private CameraRtspTargetService targets;
    @Autowired private CameraCryptoService crypto;
    private final Map<Long, CompletableFuture<RtspDescribeProbe.Result>> completions =
            new ConcurrentHashMap<>();
    private final Map<Long, CameraStatusWorker.Request> submissions = new ConcurrentHashMap<>();
    private MapSessionRepository sessions;
    private ObjectProvider<SessionRepository<? extends Session>> repositoryProvider;
    private MutableClock clock;
    private CameraStatusService background;

    @BeforeEach
    @SuppressWarnings("unchecked")
    void prepareBackgroundBoundary() {
        completions.clear();
        submissions.clear();
        sessions = new MapSessionRepository(new ConcurrentHashMap<>());
        repositoryProvider = mock(ObjectProvider.class);
        doReturn(sessions).when(repositoryProvider).getIfAvailable();
        clock = new MutableClock();
        background =
                new CameraStatusService(
                        checks,
                        snapshots,
                        channels,
                        profiles,
                        bean(CameraAccessService.class),
                        crypto,
                        targets,
                        worker,
                        bean(CameraStatusProperties.class),
                        bean(CurrentUserService.class),
                        bean(UserService.class),
                        bean(AccessService.class),
                        bean(AccessMapper.class),
                        repositoryProvider,
                        bean(PlatformTransactionManager.class),
                        clock,
                        bean(CameraStatusPlanMapper.class),
                        bean(com.streamfusion.platform.diagnostics.DiagnosticLog.class));
        when(worker.trySubmit(any()))
                .thenAnswer(
                        call -> {
                            CameraStatusWorker.Request request = call.getArgument(0);
                            submissions.put(request.checkId(), request);
                            var completion = new CompletableFuture<RtspDescribeProbe.Result>();
                            completions.put(request.checkId(), completion);
                            return new CameraStatusWorker.Submission(true, null, completion);
                        });
    }

    @AfterEach
    void releaseMockWork() {
        completions
                .values()
                .forEach(
                        future ->
                                future.complete(
                                        new RtspDescribeProbe.Result(
                                                RtspDescribeProbe.Outcome.NOT_EXECUTED,
                                                "WORKER_STOPPED",
                                                0,
                                                0)));
    }

    @Test
    void statusGetHasNeverObservationAndDoesNotCreateOrSubmitWork() throws Exception {
        var f = fixture();
        var first = read("/cameras/" + f.cameraId + "/status", f.auth);
        var next = read("/cameras/" + f.cameraId + "/status", f.auth);
        assertThat(first.path("profiles")).hasSize(2);
        assertThat(first.path("profiles").get(0).path("freshness").asText()).isEqualTo("NEVER");
        assertThat(first.path("profiles").get(0).path("observation").isNull()).isTrue();
        assertThat(next.path("profiles")).isEqualTo(first.path("profiles"));
        assertThat(countChecks()).isZero();
        verifyNoInteractions(worker);
    }

    @Test
    void postIs202AndOnlyAcceptsProfileAndClientRequestId() throws Exception {
        var f = fixture();
        var input = Map.of("streamProfileId", f.profileId, "clientRequestId", requestId());
        var receipt = write(post(path(f)), f.auth, input, 202);
        long id = receipt.path("checkId").asLong();
        assertThat(receipt.path("executionState").asText()).isEqualTo("QUEUED");
        assertThat(read(path(f) + "/" + id, f.auth)).isEqualTo(receipt);
        assertThat(countChecks()).isOne();
        assertThat(submissions.get(id).timeout()).isEqualTo(Duration.ofSeconds(10));
        assertThat(submissions.get(id).target().uri().getRawUserInfo()).isNull();
        assertThat(receipt.toString()).doesNotContain("/private-main", "rtsp://", "\"sessionId\"");
        assertThat(receipt.toString()).doesNotContain(":\"" + f.auth.session().getId() + "\"");
        write(
                post(path(f)),
                f.auth,
                Map.of(
                        "streamProfileId",
                        f.profileId,
                        "clientRequestId",
                        requestId(),
                        "timeout",
                        1000),
                400);
        write(post(path(f)), f.auth, Map.of("streamProfileId", f.profileId), 400);
        assertThat(countChecks()).isOne();
    }

    @Test
    void sameActorKeyReplaysMatchingInputAndConflictingInputIs409() throws Exception {
        var f = fixture();
        String key = requestId();
        var first = create(f, key);
        var replay =
                write(
                        post(path(f)),
                        f.auth,
                        Map.of("streamProfileId", f.profileId, "clientRequestId", key),
                        202);
        assertThat(replay).isEqualTo(first);
        String other =
                read("/cameras/" + f.cameraId, f.auth)
                        .path("profiles")
                        .get(1)
                        .path("streamProfileId")
                        .asText();
        write(post(path(f)), f.auth, Map.of("streamProfileId", other, "clientRequestId", key), 409);
        verify(worker, times(1)).trySubmit(any());
        assertThat(countChecks()).isOne();
    }

    @Test
    void staleExistingKeyReplaysEvenWhenNewChecksAreBlockedButMissingOldKeyIsRejected()
            throws Exception {
        var f = fixture();
        var created = create(f, requestId());
        long id = created.path("checkId").asLong();
        String oldKey = (System.currentTimeMillis() - 600_000) + "-" + UUID.randomUUID();
        jdbc.update("UPDATE camera_status_check SET client_request_id=? WHERE id=?", oldKey, id);
        jdbc.update("UPDATE camera_source SET enabled=FALSE WHERE id=?", Long.valueOf(f.sourceId));
        var replay = create(f, oldKey);
        assertThat(replay.path("checkId")).isEqualTo(created.path("checkId"));
        assertThat(
                        read("/cameras/" + f.cameraId + "/status", f.auth)
                                .path("profiles")
                                .get(0)
                                .path("canCheck")
                                .asBoolean())
                .isFalse();
        verify(worker, times(1)).trySubmit(any());
        jdbc.update("DELETE FROM camera_status_check WHERE id=?", id);
        write(
                post(path(f)),
                f.auth,
                Map.of("streamProfileId", f.profileId, "clientRequestId", oldKey),
                409);
        assertThat(countChecks()).isZero();
    }

    @Test
    void queuedCheckCanBeRecoveredOnlyFromOriginalSessionAndCrossSessionIsReadOnly()
            throws Exception {
        var f = fixture();
        String key = requestId();
        var receipt = create(f, key);
        long id = receipt.path("checkId").asLong();
        Csrf otherSession = login("CameraAdmin", "AdminPass1!");
        assertThat(otherSession.session().getId()).isNotEqualTo(f.auth.session().getId());
        var replay =
                write(
                        post(path(f)),
                        otherSession,
                        Map.of("streamProfileId", f.profileId, "clientRequestId", key),
                        202);
        assertThat(replay).isEqualTo(receipt);
        verify(worker, times(1)).trySubmit(any());
        background.dispatch(id, otherSession.session().getId());
        verify(worker, times(1)).trySubmit(any());
        background.dispatch(id, f.auth.session().getId());
        verify(worker, times(2)).trySubmit(any());
        assertThat(countChecks()).isOne();
    }

    @Test
    void ordinaryVisibleUserCanCheckAndReadAnotherActorsReceiptButRevokedScopeIs404()
            throws Exception {
        var f = fixture();
        var adminReceipt = create(f, requestId());
        String userId =
                write(post("/user"), f.auth, Map.of("username", "StatusViewer"), 201)
                        .path("id")
                        .asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8901,'STATUS_VIEW','状态查看','ENABLED',0)");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8901)", Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8901,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        write(
                put("/camera-scopes/users/" + userId),
                f.auth,
                Map.of("version", "0", "groupIds", List.of(), "cameraIds", List.of(f.cameraId)),
                200);
        Csrf ordinary = login("StatusViewer", "Initial1!");
        read(path(f) + "/" + adminReceipt.path("checkId").asText(), ordinary);
        assertThat(
                        read("/cameras/" + f.cameraId + "/status", ordinary)
                                .path("profiles")
                                .get(0)
                                .path("latestCheck")
                                .path("checkId"))
                .isEqualTo(adminReceipt.path("checkId"));
        write(
                post(path(f)),
                ordinary,
                Map.of("streamProfileId", f.profileId, "clientRequestId", requestId()),
                202);
        write(
                put("/camera-scopes/users/" + userId),
                f.auth,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of()),
                200);
        mvc.perform(get("/cameras/" + f.cameraId + "/status").session(ordinary.session()))
                .andExpect(status().isNotFound());
        mvc.perform(
                        get(path(f) + "/" + adminReceipt.path("checkId").asText())
                                .session(ordinary.session()))
                .andExpect(status().isNotFound());
        write(
                post(path(f)),
                ordinary,
                Map.of("streamProfileId", f.profileId, "clientRequestId", requestId()),
                404);
    }

    @Test
    void successfulAndTimedOutProbesPersistFactsInsideExecutionWindowAndGetDoesNotRenewThem()
            throws Exception {
        var f = fixture();
        long firstId = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String claim = background.claim(firstId, f.auth.session().getId());
        background.complete(
                firstId,
                claim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 20));
        var before = snapshots.selectById(Long.valueOf(f.profileId));
        assertThat(before.getValue()).isEqualTo("ACCESSIBLE");
        var firstStatus = read("/cameras/" + f.cameraId + "/status", f.auth);
        var secondStatus = read("/cameras/" + f.cameraId + "/status", f.auth);
        assertThat(secondStatus.path("profiles")).isEqualTo(firstStatus.path("profiles"));
        assertThat(secondStatus.path("profiles").get(0).path("freshness").asText())
                .isEqualTo("CURRENT");
        assertThat(snapshots.selectById(Long.valueOf(f.profileId)).getObservedAt())
                .isEqualTo(before.getObservedAt());

        long nextId = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String nextClaim = background.claim(nextId, f.auth.session().getId());
        clock.advance(Duration.ofSeconds(10));
        background.complete(
                nextId,
                nextClaim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.FAILED, "RTSP_TIMEOUT", 10_000));
        assertThat(checks.selectById(nextId).getExecutionState()).isEqualTo("COMPLETED");
        assertThat(checks.selectById(nextId).getResultValue()).isEqualTo("FAILED");
        assertThat(snapshots.selectById(Long.valueOf(f.profileId)).getReasonCode())
                .isEqualTo("RTSP_TIMEOUT");
    }

    @Test
    void timelineRetainsFirstFailureAcrossSamplesAndResetsAfterRecoveryOrConfigurationChange()
            throws Exception {
        var f = fixture();
        var outcomes =
                List.of(
                        RtspDescribeProbe.Outcome.FAILED,
                        RtspDescribeProbe.Outcome.FAILED,
                        RtspDescribeProbe.Outcome.NOT_EXECUTED,
                        RtspDescribeProbe.Outcome.ACCESSIBLE,
                        RtspDescribeProbe.Outcome.FAILED);
        LocalDateTime firstFailure = null;
        LocalDateTime lastFailure = null;
        for (int index = 0; index < outcomes.size(); index++) {
            if (index == 4)
                jdbc.update(
                        "UPDATE camera_source_endpoint SET port=8554 WHERE source_id=? AND purpose='RTSP'",
                        Long.valueOf(f.sourceId));
            long id = create(f, requestId()).path("checkId").asLong();
            clock.set(Instant.now());
            String claim = background.claim(id, f.auth.session().getId());
            clock.advance(Duration.ofSeconds(1));
            var outcome = outcomes.get(index);
            background.complete(
                    id,
                    claim,
                    f.auth.session().getId(),
                    result(
                            outcome,
                            outcome == RtspDescribeProbe.Outcome.NOT_EXECUTED
                                    ? "WORKER_BUSY"
                                    : outcome == RtspDescribeProbe.Outcome.FAILED
                                            ? "RTSP_TIMEOUT"
                                            : "RTSP_ACCESSIBLE",
                            10));
            var snapshot = snapshots.selectById(Long.valueOf(f.profileId));
            if (index == 0) {
                firstFailure = snapshot.getStateSince();
                assertThat(firstFailure).isEqualTo(snapshot.getObservedAt());
                assertThat(snapshot.getLastAccessibleAt()).isNull();
            } else if (index == 1) {
                assertThat(snapshot.getStateSince()).isEqualTo(firstFailure);
                lastFailure = snapshot.getLastFailedAt();
            } else if (index == 2) {
                assertThat(snapshot.getStateSince()).isEqualTo(firstFailure);
                assertThat(snapshot.getLastFailedAt()).isEqualTo(lastFailure);
            } else if (index == 3) {
                assertThat(snapshot.getStateSince()).isEqualTo(snapshot.getObservedAt());
                assertThat(snapshot.getLastAccessibleAt()).isEqualTo(snapshot.getObservedAt());
                assertThat(snapshot.getLastFailedAt()).isEqualTo(lastFailure);
            } else {
                assertThat(snapshot.getStateSince()).isEqualTo(snapshot.getObservedAt());
                assertThat(snapshot.getLastFailedAt()).isEqualTo(snapshot.getObservedAt());
                assertThat(snapshot.getLastAccessibleAt()).isNull();
            }
        }
        var beforePruning =
                read("/cameras/" + f.cameraId + "/status", f.auth)
                        .path("profiles")
                        .get(0)
                        .path("timeline");
        jdbc.update(
                "DELETE FROM camera_status_check WHERE profile_id=?", Long.valueOf(f.profileId));
        assertThat(
                        read("/cameras/" + f.cameraId + "/status", f.auth)
                                .path("profiles")
                                .get(0)
                                .path("timeline"))
                .isEqualTo(beforePruning);
    }

    @Test
    void expiredExecutionOldClaimAndChangedConnectionCannotWriteFacts() throws Exception {
        var f = fixture();
        long id = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String claim = background.claim(id, f.auth.session().getId());
        background.complete(
                id,
                "old-claim",
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 10));
        assertThat(checks.selectById(id).getExecutionState()).isEqualTo("RUNNING");
        assertThat(snapshots.selectById(Long.valueOf(f.profileId))).isNull();
        jdbc.update(
                "UPDATE camera_source_endpoint SET port=8554 WHERE source_id=? AND purpose='RTSP'",
                Long.valueOf(f.sourceId));
        background.complete(
                id,
                claim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 20));
        assertThat(checks.selectById(id).getExecutionState()).isEqualTo("SKIPPED");
        assertThat(checks.selectById(id).getReasonCode()).isEqualTo("CONFIGURATION_CHANGED");
        assertThat(snapshots.selectById(Long.valueOf(f.profileId))).isNull();

        long expired = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String expiredClaim = background.claim(expired, f.auth.session().getId());
        clock.advance(Duration.ofSeconds(31));
        background.complete(
                expired,
                expiredClaim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.FAILED, "RTSP_TIMEOUT", 10_000));
        assertThat(checks.selectById(expired).getReasonCode()).isEqualTo("CHECK_EXPIRED");
        assertThat(checks.selectById(expired).getResultValue()).isNull();
        assertThat(snapshots.selectById(Long.valueOf(f.profileId))).isNull();
    }

    @Test
    void sessionStoreFailureAfterClaimEndsSkippedWithoutLosingReasonOrCreatingFacts()
            throws Exception {
        var f = fixture();
        long id = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String claim = background.claim(id, f.auth.session().getId());
        doThrow(new IllegalStateException("synthetic secret store failure"))
                .when(repositoryProvider)
                .getIfAvailable();
        assertThatThrownBy(() -> background.checkWork(id, claim, f.auth.session().getId()))
                .isInstanceOf(CameraAdapterException.class)
                .hasMessage("SESSION_STORE_UNAVAILABLE");
        background.complete(
                id,
                claim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.NOT_EXECUTED, "SESSION_STORE_UNAVAILABLE", 0));
        var completed = checks.selectById(id);
        assertThat(completed.getExecutionState()).isEqualTo("SKIPPED");
        assertThat(completed.getReasonCode()).isEqualTo("SESSION_STORE_UNAVAILABLE");
        assertThat(completed.getResultValue()).isNull();
        assertThat(completed.getSnapshotApplied()).isFalse();
        assertThat(snapshots.selectById(Long.valueOf(f.profileId))).isNull();
    }

    @Test
    void revokedActorAndDeletedProfilePreventSnapshotWriteWithoutNullDereference()
            throws Exception {
        var f = fixture();
        long id = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String claim = background.claim(id, f.auth.session().getId());
        jdbc.update(
                "UPDATE sys_user SET session_version=session_version+1 WHERE username='CameraAdmin'");
        background.complete(
                id,
                claim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 20));
        assertThat(checks.selectById(id).getExecutionState()).isEqualTo("CANCELLED");
        assertThat(checks.selectById(id).getReasonCode()).isEqualTo("ACTOR_REVOKED");
        jdbc.update(
                "UPDATE sys_user SET session_version=session_version-1 WHERE username='CameraAdmin'");

        long deleted = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String deletedClaim = background.claim(deleted, f.auth.session().getId());
        jdbc.update(
                "UPDATE camera_channel SET default_preview_profile_id=NULL WHERE id=?",
                Long.valueOf(f.cameraId));
        jdbc.update(
                "DELETE FROM camera_profile_locator WHERE profile_id=?", Long.valueOf(f.profileId));
        jdbc.update("DELETE FROM camera_stream_profile WHERE id=?", Long.valueOf(f.profileId));
        background.complete(
                deleted,
                deletedClaim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 20));
        assertThat(checks.selectById(deleted).getExecutionState()).isEqualTo("INTERRUPTED");
        assertThat(checks.selectById(deleted).getReasonCode()).isEqualTo("TARGET_DELETED");
        assertThat(snapshots.selectById(Long.valueOf(f.profileId))).isNull();
    }

    @Test
    void olderCheckCannotOverwriteNewerSnapshotAndReceiptPruningKeepsLatestFact() throws Exception {
        var f = fixture();
        long older = create(f, requestId()).path("checkId").asLong();
        long newer = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String olderClaim = background.claim(older, f.auth.session().getId());
        String newerClaim = background.claim(newer, f.auth.session().getId());
        background.complete(
                newer,
                newerClaim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 20));
        background.complete(
                older,
                olderClaim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.FAILED, "RTSP_TIMEOUT", 10));
        assertThat(checks.selectById(older).getSnapshotApplied()).isFalse();
        assertThat(snapshots.selectById(Long.valueOf(f.profileId)).getCheckId()).isEqualTo(newer);
        clock.advance(Duration.ofDays(8));
        background.maintenance();
        assertThat(countChecks()).isZero();
        assertThat(snapshots.selectById(Long.valueOf(f.profileId)).getValue())
                .isEqualTo("ACCESSIBLE");
    }

    @Test
    void restartInterruptsQueuedAndRunningChecksWithoutSubmittingAnyWork() throws Exception {
        var f = fixture();
        long queued = create(f, requestId()).path("checkId").asLong();
        long running = create(f, requestId()).path("checkId").asLong();
        clock.set(Instant.now());
        String claim = background.claim(running, f.auth.session().getId());
        clearInvocations(worker);
        background.recoverInterruptedOnStartup();
        assertThat(checks.selectById(queued).getExecutionState()).isEqualTo("INTERRUPTED");
        assertThat(checks.selectById(running).getReasonCode()).isEqualTo("SERVICE_RESTARTED");
        background.complete(
                running,
                claim,
                f.auth.session().getId(),
                result(RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", 20));
        assertThat(snapshots.selectById(Long.valueOf(f.profileId))).isNull();
        verifyNoInteractions(worker);
    }

    @Test
    void restartRecoversAcrossTheHundredRowBoundaryWithoutTouchingPostCutoffChecks()
            throws Exception {
        var f = fixture();
        var original = checks.selectById(create(f, requestId()).path("checkId").asLong());
        for (long id = 1; id <= 100; id++) {
            var extra = new CameraStatusCheckEntity();
            org.springframework.beans.BeanUtils.copyProperties(original, extra);
            extra.setId(id);
            extra.setClientRequestId(requestId());
            checks.insert(extra);
        }
        var future = new CameraStatusCheckEntity();
        org.springframework.beans.BeanUtils.copyProperties(original, future);
        future.setId(1001L);
        future.setClientRequestId(requestId());
        future.setRequestedAt(original.getRequestedAt().plusSeconds(60));
        future.setDeadlineAt(original.getDeadlineAt().plusSeconds(60));
        checks.insert(future);
        clock.set(
                original.getRequestedAt()
                        .atZone(ZoneId.of("Asia/Shanghai"))
                        .toInstant()
                        .plusSeconds(1));
        clearInvocations(worker);

        assertThat(checks.activeBefore(original.getRequestedAt().plusSeconds(1))).hasSize(100);
        background.recoverInterruptedOnStartup();

        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_status_check WHERE execution_state='INTERRUPTED'",
                                Integer.class))
                .isEqualTo(101);
        assertThat(checks.selectById(1001L).getExecutionState()).isEqualTo("QUEUED");
        assertThat(countChecks()).isEqualTo(102);
        verifyNoInteractions(worker);
    }

    private Fixture fixture() throws Exception {
        Csrf auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var camera = camera(auth, sourceId, "手动状态检查");
        String id = camera.path("cameraId").asText();
        String group =
                write(post("/camera-groups"), auth, Map.of("name", "状态验收组"), 201)
                        .path("groupId")
                        .asText();
        var preview =
                write(
                        post("/cameras/" + id + "/move-preview"),
                        auth,
                        Map.of("cameraVersion", "0", "targetGroupId", group),
                        200);
        write(
                put("/cameras/" + id),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupId",
                        group,
                        "lifecycle",
                        "ENABLED",
                        "confirmation",
                        preview.path("confirmation").asText()),
                200);
        var saved = new MapSession(auth.session().getId());
        saved.setAttribute(
                HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY,
                auth.session()
                        .getAttribute(
                                HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY));
        saved.setMaxInactiveInterval(Duration.ofHours(12));
        sessions.save(saved);
        return new Fixture(
                auth,
                id,
                camera.path("profiles").get(0).path("streamProfileId").asText(),
                sourceId);
    }

    private JsonNode create(Fixture f, String key) throws Exception {
        return write(
                post(path(f)),
                f.auth,
                Map.of("streamProfileId", f.profileId, "clientRequestId", key),
                202);
    }

    private static String path(Fixture f) {
        return "/cameras/" + f.cameraId + "/status-checks";
    }

    private int countChecks() {
        return jdbc.queryForObject("SELECT COUNT(*) FROM camera_status_check", Integer.class);
    }

    private <T> T bean(Class<T> type) {
        return context.getBean(type);
    }

    private static RtspDescribeProbe.Result result(
            RtspDescribeProbe.Outcome outcome, String reason, long ms) {
        return new RtspDescribeProbe.Result(outcome, reason, 0, ms);
    }

    private record Fixture(Csrf auth, String cameraId, String profileId, String sourceId) {}

    private static final class MutableClock extends Clock {
        private Instant now = Instant.now().truncatedTo(ChronoUnit.MILLIS);

        void set(Instant value) {
            now = value.truncatedTo(ChronoUnit.MILLIS);
        }

        void advance(Duration value) {
            now = now.plus(value);
        }

        @Override
        public ZoneId getZone() {
            return ZoneId.of("UTC");
        }

        @Override
        public Clock withZone(ZoneId zone) {
            return this;
        }

        @Override
        public Instant instant() {
            return now;
        }
    }
}
