package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.support.CameraTestSupport;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/** Isolated SQL/auth regression: detection priority never changes preview or fixed plan targets. */
class CameraStatusProfileSelectionIntegrationTest extends CameraTestSupport {
    @MockitoBean private CameraStatusWorker worker;
    @Autowired private CameraProfileMapper profiles;
    @Autowired private CameraChannelMapper channels;
    @Autowired private CameraRtspTargetService targets;
    @Autowired private CameraStatusSnapshotMapper snapshots;

    @Test
    void subStreamDrivesFilteringSummaryAndNewPlansWithoutChangingPreviewOrOldPlans()
            throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        long cameraId = camera.path("cameraId").asLong();
        long main = camera.path("profiles").get(0).path("streamProfileId").asLong();
        long sub = camera.path("profiles").get(1).path("streamProfileId").asLong();
        jdbc.update(
                "UPDATE camera_stream_profile SET bitrate_kbps=2048,width=640,height=360 WHERE id=?",
                main);
        jdbc.update(
                "UPDATE camera_stream_profile SET bitrate_kbps=512,width=1920,height=1080 WHERE id=?",
                sub);
        addFact(cameraId, main, "FAILED");
        addFact(cameraId, sub, "ACCESSIBLE");

        var page = read("/camera-status?layer=CONTROL", auth);
        assertThat(
                        page.path("items")
                                .get(0)
                                .path("profiles")
                                .get(0)
                                .path("streamProfileId")
                                .asLong())
                .isEqualTo(sub);
        assertThat(page.path("items").get(0).path("defaultPreviewProfileId").asLong())
                .isEqualTo(main);
        assertThat(page.path("summary").path("accessible").asInt()).isOne();
        assertThat(
                        read("/camera-status?layer=CONTROL&observation=ACCESSIBLE", auth)
                                .path("total")
                                .asInt())
                .isOne();
        assertThat(
                        read("/camera-status?layer=CONTROL&observation=FAILED", auth)
                                .path("total")
                                .asInt())
                .isZero();
        assertThat(
                        read("/cameras/devices/page", auth)
                                .path("items")
                                .get(0)
                                .path("statusSummary")
                                .path("state")
                                .asText())
                .isEqualTo("ACCESSIBLE");
        // A healthy preview stream must not hide a failure of the cheaper detection stream.
        jdbc.update(
                "UPDATE camera_status_snapshot SET observation_value=CASE profile_id WHEN ? THEN 'ACCESSIBLE' ELSE 'FAILED' END WHERE camera_id=?",
                main,
                cameraId);
        assertThat(
                        read("/camera-status?layer=CONTROL", auth)
                                .path("summary")
                                .path("failed")
                                .asInt())
                .isOne();
        assertThat(
                        read("/camera-status?layer=CONTROL&observation=ACCESSIBLE", auth)
                                .path("total")
                                .asInt())
                .isZero();
        for (var item : read("/cameras/" + cameraId + "/status", auth).path("profiles"))
            assertThat(item.path("control").path("observation").path("value").asText())
                    .isEqualTo(
                            item.path("streamProfileId").asLong() == main
                                    ? "ACCESSIBLE"
                                    : "FAILED");
        var originalPlan = plan(auth, cameraId);
        assertThat(planProfile(originalPlan)).isEqualTo(sub);

        jdbc.update("UPDATE camera_stream_profile SET bitrate_kbps=128 WHERE id=?", main);
        assertThat(planProfile(plan(auth, cameraId))).isEqualTo(sub);
        assertThat(planProfile(originalPlan)).isEqualTo(sub);
        assertThat(read("/cameras/" + cameraId, auth).path("defaultPreviewProfileId").asLong())
                .isEqualTo(main);
        verifyNoInteractions(worker);
    }

    @Test
    void subStreamRemainsDefaultWithMissingMetricsAndDisabledProfilesAreSkipped() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        long cameraId = camera.path("cameraId").asLong();
        long main = camera.path("profiles").get(0).path("streamProfileId").asLong();
        long sub = camera.path("profiles").get(1).path("streamProfileId").asLong();
        jdbc.update(
                "UPDATE camera_stream_profile SET bitrate_kbps=NULL,width=1920,height=1080 WHERE id=?",
                main);
        jdbc.update(
                "UPDATE camera_stream_profile SET bitrate_kbps=NULL,width=640,height=360 WHERE id=?",
                sub);
        assertThat(selected(cameraId)).isEqualTo(sub);
        jdbc.update("UPDATE camera_stream_profile SET bitrate_kbps=512 WHERE id=?", main);
        assertThat(selected(cameraId)).isEqualTo(sub);
        jdbc.update(
                "UPDATE camera_stream_profile SET bitrate_kbps=NULL WHERE channel_id=?", cameraId);
        jdbc.update("UPDATE camera_stream_profile SET width=NULL,height=NULL WHERE id=?", sub);
        assertThat(selected(cameraId)).isEqualTo(sub);
        jdbc.update(
                "UPDATE camera_stream_profile SET bitrate_kbps=NULL,width=NULL,height=NULL WHERE channel_id=?",
                cameraId);
        assertThat(selected(cameraId)).isEqualTo(sub);
        jdbc.update("UPDATE camera_stream_profile SET enabled=FALSE WHERE id=?", sub);
        assertThat(selected(cameraId)).isEqualTo(main);
        jdbc.update(
                "UPDATE camera_stream_profile SET enabled=TRUE,usage_hint='UNKNOWN' WHERE channel_id=?",
                cameraId);
        assertThat(selected(cameraId)).isEqualTo(Math.min(main, sub));
        verifyNoInteractions(worker);
    }

    @Test
    void mainIsPreferredToThirdWhenSubIsUnavailable() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        long id = camera.path("cameraId").asLong();
        long main = camera.path("profiles").get(0).path("streamProfileId").asLong();
        long sub = camera.path("profiles").get(1).path("streamProfileId").asLong();
        jdbc.update(
                "INSERT INTO camera_stream_profile(id,source_id,channel_id,external_profile_key,label,usage_hint,enabled,bitrate_kbps) VALUES(7002,?,?,'third','thirdStream','THIRD',TRUE,64)",
                camera.path("sourceId").asLong(),
                id);
        jdbc.update(
                "UPDATE camera_stream_profile SET label='subStream',bitrate_kbps=4096 WHERE id=?",
                sub);
        jdbc.update("UPDATE camera_stream_profile SET bitrate_kbps=128 WHERE id=?", main);
        assertThat(selected(id)).isEqualTo(sub);
        jdbc.update("UPDATE camera_stream_profile SET enabled=FALSE WHERE id=?", sub);
        assertThat(selected(id)).isEqualTo(main);
        jdbc.update("UPDATE camera_stream_profile SET enabled=FALSE WHERE id=?", main);
        assertThat(selected(id)).isEqualTo(7002L);
        verifyNoInteractions(worker);
    }

    @Test
    void explicitUsageIsNotOverriddenByLabelsWhileUnknownStreamsCanBeInferred() throws Exception {
        var auth = admin();
        var camera = enabled(auth);
        long id = camera.path("cameraId").asLong();
        long main = camera.path("profiles").get(0).path("streamProfileId").asLong();
        long sub = camera.path("profiles").get(1).path("streamProfileId").asLong();
        jdbc.update(
                "UPDATE camera_stream_profile SET label='sub-stream',usage_hint='MAIN',bitrate_kbps=64 WHERE id=?",
                main);
        jdbc.update(
                "UPDATE camera_stream_profile SET usage_hint='SUB',bitrate_kbps=4096 WHERE id=?",
                sub);
        assertThat(selected(id)).isEqualTo(sub);
        jdbc.update("UPDATE camera_stream_profile SET usage_hint='UNKNOWN' WHERE id=?", main);
        assertThat(selected(id)).isEqualTo(main);
        jdbc.update("UPDATE camera_stream_profile SET usage_hint='CUSTOM' WHERE id=?", main);
        assertThat(selected(id)).isEqualTo(sub);
    }

    private long selected(long cameraId) {
        long representative = profiles.statusRepresentatives(List.of(cameraId)).getFirst().getId();
        assertThat(
                        profiles.statusProfiles(List.of(cameraId), 100).stream()
                                .filter(p -> Boolean.TRUE.equals(p.getEnabled()))
                                .findFirst()
                                .orElseThrow()
                                .getId())
                .isEqualTo(representative);
        return representative;
    }

    private JsonNode enabled(Csrf auth) throws Exception {
        var camera = camera(auth, source(auth).path("sourceId").asText(), "低负载相机");
        long group =
                write(post("/camera-groups"), auth, Map.of("name", "检测组"), 201)
                        .path("groupId")
                        .asLong();
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                group,
                camera.path("cameraId").asLong());
        return camera;
    }

    private JsonNode plan(Csrf auth, long cameraId) throws Exception {
        return write(
                post("/camera-status/plans"),
                auth,
                Map.of(
                        "clientRequestId",
                        requestId(),
                        "groupKeys",
                        List.of("c" + cameraId),
                        "intervalMinutes",
                        60,
                        "durationHours",
                        24),
                201);
    }

    private long planProfile(JsonNode plan) {
        return jdbc.queryForObject(
                "SELECT profile_id FROM camera_status_plan_target WHERE plan_id=?",
                Long.class,
                plan.path("planId").asLong());
    }

    private void addFact(long cameraId, long profileId, String value) {
        var inspection =
                targets.inspect(
                        channels.selectById(cameraId),
                        profiles.selectById(profileId),
                        "testkey",
                        () -> {});
        var now = LocalDateTime.now(ZoneId.of("Asia/Shanghai")).withNano(0);
        var fact = new CameraStatusSnapshotEntity();
        fact.setProfileId(profileId);
        fact.setCameraId(cameraId);
        fact.setLayer("CONTROL");
        fact.setProbeKind("RTSP_DESCRIBE");
        fact.setValue(value);
        fact.setReasonCode("RTSP_" + value);
        fact.setObservedAt(now.minusSeconds(3));
        fact.setReadAt(now.minusSeconds(3));
        fact.setValidUntil(now.plusMinutes(5));
        fact.setCheckRequestedAt(now.minusSeconds(4));
        fact.setCheckId(1L);
        fact.setConnectionKeyId("testkey");
        fact.setConnectionDigest(inspection.connectionDigest());
        fact.setUpdatedAt(now.minusSeconds(3));
        snapshots.insert(fact);
    }
}
