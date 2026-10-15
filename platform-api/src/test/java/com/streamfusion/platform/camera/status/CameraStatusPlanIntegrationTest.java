package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.service.*;
import com.streamfusion.platform.support.CameraTestSupport;
import com.streamfusion.platform.user.service.UserService;
import java.time.*;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.context.ApplicationContext;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.transaction.PlatformTransactionManager;

/** Real HTTP/SQL/auth with a controlled worker; not real camera or thousand-stream acceptance. */
class CameraStatusPlanIntegrationTest extends CameraTestSupport {
    @Autowired ApplicationContext context;
    @MockitoBean CameraStatusWorker worker;
    final Map<Long, CameraStatusWorker.Request> submitted = new HashMap<>();
    final Map<Long, CompletableFuture<RtspDescribeProbe.Result>> futures = new HashMap<>();
    MutableClock clock;
    CameraStatusService background;
    CameraStatusPlanService scheduler;

    @BeforeEach
    void prepare() {
        submitted.clear();
        futures.clear();
        clock = new MutableClock();
        background =
                new CameraStatusService(
                        bean(CameraStatusCheckMapper.class),
                        bean(CameraStatusSnapshotMapper.class),
                        bean(CameraChannelMapper.class),
                        bean(CameraProfileMapper.class),
                        bean(CameraAccessService.class),
                        bean(CameraCryptoService.class),
                        bean(CameraRtspTargetService.class),
                        worker,
                        bean(CameraStatusProperties.class),
                        bean(CurrentUserService.class),
                        bean(UserService.class),
                        bean(AccessService.class),
                        bean(AccessMapper.class),
                        mock(ObjectProvider.class),
                        bean(PlatformTransactionManager.class),
                        clock,
                        bean(CameraStatusPlanMapper.class),
                        bean(com.streamfusion.platform.diagnostics.DiagnosticLog.class));
        scheduler =
                new CameraStatusPlanService(
                        bean(CameraStatusPlanMapper.class),
                        bean(CameraStatusPlanTargetMapper.class),
                        bean(CameraChannelMapper.class),
                        bean(CameraProfileMapper.class),
                        bean(CameraAccessService.class),
                        bean(AccessMapper.class),
                        bean(CameraCryptoService.class),
                        bean(CameraRtspTargetService.class),
                        background,
                        bean(CameraStatusProperties.class),
                        bean(PlatformTransactionManager.class),
                        clock,
                        bean(com.streamfusion.platform.audit.service.AuditService.class));
        when(worker.trySubmit(any()))
                .thenAnswer(
                        call -> {
                            CameraStatusWorker.Request request = call.getArgument(0);
                            submitted.put(request.checkId(), request);
                            var future = new CompletableFuture<RtspDescribeProbe.Result>();
                            futures.put(request.checkId(), future);
                            return new CameraStatusWorker.Submission(true, null, future);
                        });
    }

    @AfterEach
    void release() {
        new ArrayList<>(futures.values())
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
    void fixedRepresentativeSlotsAreIdempotentAndContinueWithoutLoginSession() throws Exception {
        var auth = admin();
        var first = enabled(auth);
        var second = camera(auth, first.path("sourceId").asText(), "第二通道");
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                first.path("groupId").asLong(),
                second.path("cameraId").asLong());
        var body =
                input(
                        List.of(
                                "c" + first.path("cameraId").asText(),
                                "c" + second.path("cameraId").asText()),
                        60,
                        24);
        var plan = write(post("/camera-status/plans"), auth, body, 201);
        var replay = write(post("/camera-status/plans"), auth, body, 201);
        JsonNode stableReplay =
                ((com.fasterxml.jackson.databind.node.ObjectNode) replay).without("serverNow");
        JsonNode stablePlan =
                ((com.fasterxml.jackson.databind.node.ObjectNode) plan.deepCopy())
                        .without("serverNow");
        assertThat(stableReplay).isEqualTo(stablePlan);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='CAMERA_STATUS_PLAN_CREATE' AND target_id=?",
                                Integer.class,
                                plan.path("planId").asLong()))
                .isEqualTo(1);
        var changed = new HashMap<>(body);
        changed.put("intervalMinutes", 30);
        write(post("/camera-status/plans"), auth, changed, 409);
        assertThat(plan.path("targetCount").asInt()).isEqualTo(2);
        assertThat(read("/camera-status/plans", auth).get(0).path("nextAt").asText())
                .isEqualTo(plan.path("startAt").asText());
        var targets = bean(CameraStatusPlanTargetMapper.class).selectList(null);
        assertThat(targets).hasSize(2);
        assertThat(Duration.between(targets.get(0).getNextAt(), targets.get(1).getNextAt()).abs())
                .isEqualTo(Duration.ofMinutes(30));
        assertThat(read("/cameras/devices/page", auth).toString()).contains("statusSummary");
        verifyNoInteractions(worker);
        auth.session().invalidate();
        clock.value = Instant.parse(plan.path("startAt").asText());
        scheduler.tick();
        scheduler.tick();
        assertThat(submitted).hasSize(1);
        var request = submitted.values().iterator().next();
        request.beforeProbe().run();
        futures.get(request.checkId())
                .complete(
                        new RtspDescribeProbe.Result(
                                RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_DESCRIBE_OK", 200, 5));
        assertThat(bean(CameraStatusSnapshotMapper.class).selectList(null)).hasSize(1);
        assertThat(bean(CameraStatusCheckMapper.class).selectById(request.checkId()).getPlanId())
                .isEqualTo(plan.path("planId").asLong());
        var signedIn = login("CameraAdmin", "AdminPass1!");
        var summaries = read("/cameras/devices/page", signedIn).path("items");
        for (var item : summaries) {
            assertThat(item.path("statusSummary").path("state").asText())
                    .isEqualTo(
                            item.path("groupKey").asText().equals("c" + requestCamera(request))
                                    ? "ACCESSIBLE"
                                    : "NEVER");
        }
        jdbc.update(
                "UPDATE camera_status_snapshot SET valid_until=DATEADD('SECOND',-1,CURRENT_TIMESTAMP)");
        for (var item : read("/cameras/devices/page", signedIn).path("items")) {
            if (item.path("groupKey").asText().equals("c" + requestCamera(request)))
                assertThat(item.path("statusSummary").path("state").asText()).isEqualTo("STALE");
        }
        jdbc.update(
                "UPDATE camera_channel SET default_preview_profile_id=(SELECT id FROM camera_stream_profile WHERE channel_id=? AND usage_hint='SUB') WHERE id=?",
                requestCamera(request),
                requestCamera(request));
        for (var item : read("/cameras/devices/page", signedIn).path("items")) {
            assertThat(item.path("statusSummary").path("state").asText()).isEqualTo("NEVER");
        }
        assertThat(submitted).hasSize(1);
    }

    @Test
    void stopAndExpiryFenceLateResultsWithoutOfflineFacts() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        var plan = create(auth, camera, 60, 1);
        clock.value = Instant.parse(plan.path("startAt").asText());
        scheduler.tick();
        var request = submitted.values().iterator().next();
        request.beforeProbe().run();
        write(
                post("/camera-status/plans/" + plan.path("planId").asText() + "/stop"),
                auth,
                Map.of("version", "0"),
                200);
        futures.get(request.checkId())
                .complete(
                        new RtspDescribeProbe.Result(
                                RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_DESCRIBE_OK", 200, 5));
        assertThat(bean(CameraStatusSnapshotMapper.class).selectList(null)).isEmpty();
        assertThat(
                        bean(CameraStatusCheckMapper.class)
                                .selectById(request.checkId())
                                .getExecutionState())
                .isEqualTo("SKIPPED");
        var another = create(auth, camera, 60, 1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='CAMERA_STATUS_PLAN_STOP' AND target_id=?",
                                Integer.class,
                                plan.path("planId").asLong()))
                .isEqualTo(1);
        clock.value = Instant.parse(another.path("endAt").asText());
        scheduler.tick();
        assertThat(
                        bean(CameraStatusPlanMapper.class)
                                .selectById(another.path("planId").asLong())
                                .getState())
                .isEqualTo("EXPIRED");
        assertThat(submitted).hasSize(1);
    }

    @Test
    void elapsedPlanFencesResultEvenBeforeExpiryMaintenance() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        var plan = create(auth, camera, 60, 1);
        clock.value = Instant.parse(plan.path("startAt").asText());
        scheduler.tick();
        var request = submitted.values().iterator().next();
        request.beforeProbe().run();
        // Move the plan deadline inside this running check's execution window.
        jdbc.update(
                "UPDATE camera_status_plan SET end_at=? WHERE id=?",
                LocalDateTime.ofInstant(clock.value.plusSeconds(1), ZoneId.of("Asia/Shanghai")),
                plan.path("planId").asLong());
        clock.value = clock.value.plusSeconds(2);
        futures.get(request.checkId())
                .complete(
                        new RtspDescribeProbe.Result(
                                RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_DESCRIBE_OK", 200, 5));
        assertThat(bean(CameraStatusSnapshotMapper.class).selectList(null)).isEmpty();
        assertThat(
                        bean(CameraStatusCheckMapper.class)
                                .selectById(request.checkId())
                                .getReasonCode())
                .isEqualTo("PLAN_NO_LONGER_ACTIVE");
    }

    @Test
    void changedConnectionAndRevokedActorBlockTargetsBeforeNetworking() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        var plan = create(auth, camera, 60, 1);
        jdbc.update(
                "UPDATE camera_source_endpoint SET port=8554 WHERE source_id=?",
                camera.path("sourceId").asLong());
        clock.value = Instant.parse(plan.path("startAt").asText());
        scheduler.tick();
        assertThat(
                        bean(CameraStatusPlanTargetMapper.class)
                                .selectList(null)
                                .getFirst()
                                .getReasonCode())
                .isEqualTo("CONFIGURATION_CHANGED");
        var listed = read("/camera-status/plans", auth).get(0);
        assertThat(listed.path("blockedCount").asInt()).isEqualTo(1);
        assertThat(listed.path("nextAt").isNull()).isTrue();
        assertThat(listed.path("lastReasonCode").asText()).isEqualTo("CONFIGURATION_CHANGED");
        var another = create(auth, camera, 60, 1);
        jdbc.update("UPDATE sys_user SET status=2 WHERE username='CameraAdmin'");
        clock.value = Instant.parse(another.path("startAt").asText());
        scheduler.tick();
        assertThat(bean(CameraStatusPlanTargetMapper.class).selectList(null))
                .allMatch(target -> target.getBlocked());
        verifyNoInteractions(worker);
        assertThat(bean(CameraStatusSnapshotMapper.class).selectList(null)).isEmpty();
    }

    @Test
    void missedSlotsAreNotReplayedAndBusyExecutionNeverProducesFailureFact() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        var plan = create(auth, camera, 60, 24);
        clock.value = Instant.parse(plan.path("startAt").asText()).plusSeconds(120);
        scheduler.tick();
        verifyNoInteractions(worker);
        assertThat(
                        bean(CameraStatusPlanTargetMapper.class)
                                .selectList(null)
                                .getFirst()
                                .getReasonCode())
                .isEqualTo("MISSED_SLOT");
        clock.value = Instant.parse(plan.path("startAt").asText()).plusSeconds(3600);
        scheduler.tick();
        var request = submitted.values().iterator().next();
        futures.get(request.checkId())
                .complete(
                        new RtspDescribeProbe.Result(
                                RtspDescribeProbe.Outcome.NOT_EXECUTED, "WORKER_BUSY", 0, 0));
        assertThat(
                        bean(CameraStatusCheckMapper.class)
                                .selectById(request.checkId())
                                .getExecutionState())
                .isEqualTo("SKIPPED");
        assertThat(bean(CameraStatusSnapshotMapper.class).selectList(null)).isEmpty();
        var listed = read("/camera-status/plans", auth).get(0);
        assertThat(listed.path("lastReasonCode").asText()).isEqualTo("WORKER_BUSY");
        assertThat(listed.path("progress").path("skipped").asInt()).isOne();
        assertThat(listed.path("progress").path("failed").asInt()).isZero();
        assertThat(listed.path("serverNow").asText()).isNotBlank();
    }

    @Test
    void thousandTargetsAreSpreadAndOneTickCannotSubmitThemAll() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        long source = camera.path("sourceId").asLong(), group = camera.path("groupId").asLong();
        var crypto = bean(CameraCryptoService.class);
        var keys = new ArrayList<String>();
        for (int index = 0; index < 1000; index++) {
            long device = 7000 + index, channel = 10000 + index, profile = 20000 + index;
            keys.add("d" + device);
            jdbc.update(
                    "INSERT INTO camera_device(id,source_id,external_device_key,device_type) VALUES(?,?,?,'NVR')",
                    device,
                    source,
                    "recorder-" + index);
            jdbc.update(
                    "INSERT INTO camera_channel(id,source_id,device_id,external_channel_key,group_id,name,lifecycle,mapping_origin) VALUES(?,?,?,?,?,'批量通道','ENABLED','MANUAL')",
                    channel,
                    source,
                    device,
                    "bulk-" + index,
                    group);
            jdbc.update(
                    "INSERT INTO camera_stream_profile(id,source_id,channel_id,external_profile_key,label,usage_hint) VALUES(?,?,?,'main','主流','MAIN')",
                    profile,
                    source,
                    channel);
            var secret =
                    crypto.encrypt(
                            "camera_profile_locator/" + source + "/" + profile + "/RTSP",
                            Map.of("encodedPath", "/bulk/" + index, "rawQuery", ""),
                            16000);
            jdbc.update(
                    "INSERT INTO camera_profile_locator(profile_id,source_id,locator_kind,endpoint_purpose,rtsp_host_mode,rtsp_transport,rtsp_secret_ciphertext,rtsp_secret_nonce,rtsp_secret_tag,rtsp_encryption_key_id,identity_digest,created_at,updated_at) VALUES(?,?,'RTSP','RTSP','SOURCE','TCP',?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                    profile,
                    source,
                    secret.ciphertext(),
                    secret.nonce(),
                    secret.tag(),
                    secret.keyId(),
                    crypto.digest("testkey", "fixture-identity", Long.toString(profile)));
        }
        var plan = write(post("/camera-status/plans"), auth, input(keys, 60, 24), 201);
        var targets =
                bean(CameraStatusPlanTargetMapper.class).selectList(null).stream()
                        .sorted(Comparator.comparing(CameraStatusPlanTargetEntity::getNextAt))
                        .toList();
        assertThat(targets).hasSize(1000);
        assertThat(targets.stream().map(CameraStatusPlanTargetEntity::getNextAt).distinct())
                .hasSize(1000);
        assertThat(Duration.between(targets.getFirst().getNextAt(), targets.getLast().getNextAt()))
                .isGreaterThan(Duration.ofMinutes(59));
        clock.value = Instant.parse(plan.path("startAt").asText());
        scheduler.tick();
        assertThat(submitted).hasSize(1);
        write(post("/camera-status/plans"), auth, input(keys, 15, 24), 429);
    }

    @Test
    void devicePlanChecksOnlyOneStableEnabledChannelAndHonorsDefaultProfile() throws Exception {
        var auth = admin();
        var first = enabled(auth);
        var second = camera(auth, first.path("sourceId").asText(), "另一路");
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,device_type) VALUES(7000,?,'recorder','NVR')",
                first.path("sourceId").asLong());
        jdbc.update(
                "UPDATE camera_channel SET device_id=7000,group_id=?,lifecycle='ENABLED' WHERE source_id=?",
                first.path("groupId").asLong(),
                first.path("sourceId").asLong());
        long main = first.path("defaultPreviewProfileId").asLong();
        long sub =
                jdbc.queryForObject(
                        "SELECT id FROM camera_stream_profile WHERE channel_id=? AND usage_hint='SUB'",
                        Long.class,
                        first.path("cameraId").asLong());
        jdbc.update(
                "UPDATE camera_channel SET default_preview_profile_id=? WHERE id=?",
                sub,
                first.path("cameraId").asLong());
        var plan = write(post("/camera-status/plans"), auth, input(List.of("d7000"), 60, 24), 201);
        assertThat(plan.path("targetCount").asInt()).isEqualTo(1);
        var target = bean(CameraStatusPlanTargetMapper.class).selectList(null).getFirst();
        assertThat(target.getCameraId()).isEqualTo(first.path("cameraId").asLong());
        assertThat(target.getProfileId()).isEqualTo(sub);
        assertThat(target.getCameraId()).isNotEqualTo(second.path("cameraId").asLong());
        jdbc.update(
                "UPDATE camera_channel SET default_preview_profile_id=NULL WHERE id=?",
                first.path("cameraId").asLong());
        var fallback =
                write(post("/camera-status/plans"), auth, input(List.of("d7000"), 60, 24), 201);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT profile_id FROM camera_status_plan_target WHERE plan_id=?",
                                Long.class,
                                fallback.path("planId").asLong()))
                .isEqualTo(main);
        jdbc.update(
                "UPDATE camera_channel SET lifecycle='DISABLED' WHERE id=?",
                first.path("cameraId").asLong());
        var next = write(post("/camera-status/plans"), auth, input(List.of("d7000"), 60, 24), 201);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT camera_id FROM camera_status_plan_target WHERE plan_id=?",
                                Long.class,
                                next.path("planId").asLong()))
                .isEqualTo(second.path("cameraId").asLong());
        verifyNoInteractions(worker);
    }

    @Test
    void representativeProfileFallsBackToStableEnabledIdWithoutMainOrDefault() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        jdbc.update(
                "UPDATE camera_channel SET default_preview_profile_id=NULL WHERE id=?",
                camera.path("cameraId").asLong());
        jdbc.update(
                "UPDATE camera_stream_profile SET usage_hint='UNKNOWN' WHERE channel_id=?",
                camera.path("cameraId").asLong());
        var plan = create(auth, camera, 60, 24);
        assertThat(
                        bean(CameraStatusPlanTargetMapper.class)
                                .selectList(null)
                                .getFirst()
                                .getProfileId())
                .isEqualTo(
                        jdbc.queryForObject(
                                "SELECT MIN(id) FROM camera_stream_profile WHERE channel_id=? AND enabled=TRUE",
                                Long.class,
                                camera.path("cameraId").asLong()));
        assertThat(plan.path("targetCount").asInt()).isEqualTo(1);
    }

    @Test
    void representativeQueryLimitsDevicesRatherThanTheirChannelCount() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        long source = camera.path("sourceId").asLong();
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,device_type) VALUES(7000,?,'recorder','NVR')",
                source);
        jdbc.update(
                "INSERT INTO camera_channel(id,source_id,device_id,external_channel_key,group_id,name,lifecycle,mapping_origin) SELECT 10000+X,?,7000,CONCAT('bulk-',X),?,'批量通道','ENABLED','MANUAL' FROM SYSTEM_RANGE(0,2099)",
                source,
                camera.path("groupId").asLong());
        var rows =
                bean(CameraChannelMapper.class)
                        .statusDeviceRepresentatives(
                                List.of("d7000", "c" + camera.path("cameraId").asText()),
                                new CameraAccessService.Visibility(true, 1L, List.of()),
                                2001);
        assertThat(rows).hasSize(2);
        assertThat(rows.getFirst().getId()).isEqualTo(10000L);
        assertThat(rows.getLast().getId()).isEqualTo(camera.path("cameraId").asLong());
        jdbc.update(
                "UPDATE camera_channel SET device_id=7000 WHERE id=?",
                camera.path("cameraId").asLong());
        long user =
                jdbc.queryForObject(
                        "SELECT id FROM sys_user WHERE username='CameraAdmin'", Long.class);
        jdbc.update("INSERT INTO camera_user_scope(user_id) VALUES(?)", user);
        jdbc.update(
                "INSERT INTO camera_user_channel_grant(user_id,channel_id) VALUES(?,?)",
                user,
                camera.path("cameraId").asLong());
        var scoped =
                bean(CameraChannelMapper.class)
                        .statusDeviceRepresentatives(
                                List.of("d7000"),
                                new CameraAccessService.Visibility(false, user, List.of()),
                                2001);
        assertThat(scoped).hasSize(1);
        assertThat(scoped.getFirst().getId()).isEqualTo(camera.path("cameraId").asLong());
    }

    @Test
    void ordinaryUserCannotCreatePlanForHiddenTargetOrStopAnotherUsersPlan() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        var plan = create(auth, camera, 60, 1);
        String userId =
                write(post("/user"), auth, Map.of("username", "PlanUser"), 201).path("id").asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8922,'PATROL','巡检','ENABLED',0)");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8922)", Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8922,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        var ordinary = login("PlanUser", "Initial1!");
        assertThat(read("/camera-status/plans", ordinary)).isEmpty();
        assertThat(
                        write(
                                        post("/camera-status/plans/targets/preview"),
                                        ordinary,
                                        Map.of("scope", "ALL"),
                                        200)
                                .path("targets"))
                .isEmpty();
        write(
                post("/camera-status/plans/targets/preview"),
                ordinary,
                Map.of("scope", "PLAN", "planId", plan.path("planId").asText()),
                404);
        write(
                post("/camera-status/plans"),
                ordinary,
                input(List.of("c" + camera.path("cameraId").asText()), 60, 1),
                409);
        write(
                post("/camera-status/plans/" + plan.path("planId").asText() + "/stop"),
                ordinary,
                Map.of("version", "0"),
                404);
        write(
                put("/camera-scopes/users/" + userId),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupIds",
                        List.of(),
                        "cameraIds",
                        List.of(camera.path("cameraId").asText())),
                200);
        var own = create(ordinary, camera, 60, 1);
        assertThat(
                        write(
                                        post("/camera-status/plans/targets/preview"),
                                        ordinary,
                                        Map.of("scope", "ALL"),
                                        200)
                                .path("targets"))
                .hasSize(1);
        clock.value = Instant.parse(own.path("startAt").asText());
        scheduler.tick();
        var ownRequest =
                submitted.values().stream()
                        .filter(
                                request ->
                                        bean(CameraStatusCheckMapper.class)
                                                .selectById(request.checkId())
                                                .getPlanId()
                                                .equals(own.path("planId").asLong()))
                        .findFirst();
        // The other owner's target can have an earlier slot; the second tick claims ours.
        if (ownRequest.isEmpty()) {
            scheduler.tick();
            ownRequest =
                    submitted.values().stream()
                            .filter(
                                    request ->
                                            bean(CameraStatusCheckMapper.class)
                                                    .selectById(request.checkId())
                                                    .getPlanId()
                                                    .equals(own.path("planId").asLong()))
                            .findFirst();
        }
        var request = ownRequest.orElseThrow();
        request.beforeProbe().run();
        write(
                put("/camera-scopes/users/" + userId),
                auth,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of()),
                200);
        futures.get(request.checkId())
                .complete(
                        new RtspDescribeProbe.Result(
                                RtspDescribeProbe.Outcome.ACCESSIBLE, "RTSP_DESCRIBE_OK", 200, 5));
        assertThat(
                        bean(CameraStatusCheckMapper.class)
                                .selectById(request.checkId())
                                .getSnapshotApplied())
                .isFalse();
        assertThat(bean(CameraStatusSnapshotMapper.class).selectList(null)).isEmpty();
    }

    private JsonNode enabled(Csrf auth) throws Exception {
        var camera = camera(auth, source(auth).path("sourceId").asText(), "巡检通道");
        long group =
                write(
                                post("/camera-groups"),
                                auth,
                                Map.of("name", "巡检区域-" + camera.path("cameraId").asText()),
                                201)
                        .path("groupId")
                        .asLong();
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                group,
                camera.path("cameraId").asLong());
        return read("/cameras/" + camera.path("cameraId").asText(), auth);
    }

    @Test
    void previewBatchesAllAuthorizedDevicesAndDistinguishesConfigurationGaps() throws Exception {
        var auth = admin();
        var direct = enabled(auth);
        var missing = enabled(auth);
        var onvif = enabled(auth);
        var noProfile = enabled(auth);
        jdbc.update(
                "DELETE FROM camera_profile_locator WHERE source_id IN (?,?)",
                missing.path("sourceId").asLong(),
                onvif.path("sourceId").asLong());
        jdbc.update(
                "UPDATE camera_source SET adapter_type='ONVIF' WHERE id=?",
                onvif.path("sourceId").asLong());
        jdbc.update(
                "UPDATE camera_stream_profile SET enabled=FALSE WHERE channel_id=?",
                noProfile.path("cameraId").asLong());
        var preview =
                write(
                        post("/camera-status/plans/targets/preview"),
                        auth,
                        Map.of("scope", "ALL"),
                        200);
        assertThat(preview.path("limitExceeded").asBoolean()).isFalse();
        assertThat(preview.path("targets")).hasSize(4);
        assertThat(preview.toString()).contains("LOCATOR_NOT_CONFIGURED", "PROFILE_NOT_CONFIGURED");
        var page =
                write(
                        post("/camera-status/plans/targets/preview"),
                        auth,
                        Map.of(
                                "scope",
                                "PAGE",
                                "groupKeys",
                                List.of("c" + direct.path("cameraId").asText())),
                        200);
        assertThat(page.path("targets")).hasSize(1);
        assertThat(page.path("targets").get(0).path("reasonCode").isNull()).isTrue();
        assertThat(bean(CameraStatusPlanMapper.class).selectList(null)).isEmpty();
        assertThat(bean(CameraStatusCheckMapper.class).selectList(null)).isEmpty();
        verifyNoInteractions(worker);
    }

    @Test
    void previewReusesOwnedTaskScopeAndReportsUnavailableTargetsWithoutNetwork() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        var plan = create(auth, camera, 60, 1);
        var query = Map.of("scope", "PLAN", "planId", plan.path("planId").asText());
        var preview = write(post("/camera-status/plans/targets/preview"), auth, query, 200);
        assertThat(preview.path("targets").get(0).path("groupKey").asText())
                .isEqualTo("c" + camera.path("cameraId").asText());
        jdbc.update(
                "UPDATE camera_channel SET lifecycle='DISABLED' WHERE id=?",
                camera.path("cameraId").asLong());
        preview = write(post("/camera-status/plans/targets/preview"), auth, query, 200);
        assertThat(preview.path("targets").get(0).path("reasonCode").asText())
                .isEqualTo("TARGET_UNAVAILABLE");
        verifyNoInteractions(worker);
    }

    @Test
    void allScopePreviewNeverSilentlyTruncatesAtTwoThousandDevices() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        jdbc.update(
                "INSERT INTO camera_channel(id,source_id,external_channel_key,group_id,name,lifecycle,mapping_origin) SELECT 10000+X,?,CONCAT('bulk-',X),?,'模拟通道','ENABLED','MANUAL' FROM SYSTEM_RANGE(0,1999)",
                camera.path("sourceId").asLong(),
                camera.path("groupId").asLong());
        var preview =
                write(
                        post("/camera-status/plans/targets/preview"),
                        auth,
                        Map.of("scope", "ALL"),
                        200);
        assertThat(preview.path("limitExceeded").asBoolean()).isTrue();
        assertThat(preview.path("targets")).isEmpty();
        verifyNoInteractions(worker);
    }

    @Test
    void previewValidatesScopeAndEmptyPageWithoutExpandingToAllDevices() throws Exception {
        var auth = admin();
        enabled(auth);
        var preview =
                write(
                        post("/camera-status/plans/targets/preview"),
                        auth,
                        Map.of("scope", "PAGE", "groupKeys", List.of()),
                        200);
        assertThat(preview.path("targets")).isEmpty();
        write(
                post("/camera-status/plans/targets/preview"),
                auth,
                Map.of("scope", "ALL", "groupKeys", List.of()),
                400);
        write(
                post("/camera-status/plans/targets/preview"),
                auth,
                Map.of("scope", "PAGE", "groupKeys", List.of("invalid")),
                400);
        write(post("/camera-status/plans/targets/preview"), auth, Map.of("scope", "INVALID"), 400);
        verifyNoInteractions(worker);
    }

    private Map<String, Object> input(List<String> keys, int interval, int hours) {
        return Map.of(
                "clientRequestId",
                requestId(),
                "groupKeys",
                keys,
                "intervalMinutes",
                interval,
                "durationHours",
                hours);
    }

    private JsonNode create(Csrf auth, JsonNode camera, int interval, int hours) throws Exception {
        return write(
                post("/camera-status/plans"),
                auth,
                input(List.of("c" + camera.path("cameraId").asText()), interval, hours),
                201);
    }

    private <T> T bean(Class<T> type) {
        return context.getBean(type);
    }

    private long requestCamera(CameraStatusWorker.Request request) {
        return bean(CameraStatusCheckMapper.class).selectById(request.checkId()).getCameraId();
    }

    private static final class MutableClock extends Clock {
        Instant value = Instant.now();

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
            return value;
        }
    }
}
