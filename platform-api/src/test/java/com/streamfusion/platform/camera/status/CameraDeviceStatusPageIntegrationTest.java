package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.support.CameraTestSupport;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/** Device paging selects one authorized target before filtering; no real device is contacted. */
class CameraDeviceStatusPageIntegrationTest extends CameraTestSupport {
    @MockitoBean private CameraStatusWorker worker;
    @Autowired private CameraChannelMapper channels;
    @Autowired private CameraProfileMapper profiles;
    @Autowired private CameraRtspTargetService targets;
    @Autowired private CameraStatusSnapshotMapper snapshots;

    @Test
    void sixteenChannelsAreOneDeviceAndDisablingOneChannelDoesNotDisableTheDevice()
            throws Exception {
        var auth = admin();
        var cameras = device(auth, 16);
        var first = cameras.getFirst();
        var second = cameras.get(1);
        var page = read("/camera-status/devices?layer=CONTROL", auth);
        assertThat(page.path("total").asInt()).isOne();
        assertThat(page.path("summary").path("total").asInt()).isOne();
        assertThat(page.path("items")).hasSize(1);
        assertThat(page.path("items").get(0).path("groupKey").asText()).isEqualTo("d7000");
        assertThat(page.path("items").get(0).path("deviceName").asText()).isEqualTo("十六路录像机");
        assertThat(page.path("items").get(0).path("cameraId")).isEqualTo(first.path("cameraId"));
        assertThat(read("/camera-status?layer=CONTROL", auth).path("total").asInt()).isEqualTo(16);

        jdbc.update(
                "UPDATE camera_channel SET lifecycle='DISABLED' WHERE id=?",
                first.path("cameraId").asLong());
        var after = read("/camera-status/devices?layer=CONTROL", auth);
        assertThat(after.path("total").asInt()).isOne();
        assertThat(after.path("summary").path("disabled").asInt()).isZero();
        assertThat(after.path("items").get(0).path("cameraId")).isEqualTo(second.path("cameraId"));
        var plan =
                write(
                        post("/camera-status/plans"),
                        auth,
                        Map.of(
                                "clientRequestId",
                                requestId(),
                                "groupKeys",
                                List.of("d7000"),
                                "intervalMinutes",
                                60,
                                "durationHours",
                                24),
                        201);
        assertThat(plan.path("targetCount").asInt()).isOne();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT camera_id FROM camera_status_plan_target WHERE plan_id=?",
                                Long.class,
                                plan.path("planId").asLong()))
                .isEqualTo(second.path("cameraId").asLong());
        jdbc.update("UPDATE camera_channel SET lifecycle='DISABLED' WHERE device_id=7000");
        assertThat(
                        read("/camera-status/devices?layer=CONTROL", auth)
                                .path("summary")
                                .path("disabled")
                                .asInt())
                .isOne();
        verifyNoInteractions(worker);
    }

    @Test
    void usableChannelWinsBeforeHistoryFilteringAndSearchDoesNotChangeItsIdentity()
            throws Exception {
        var auth = admin();
        var cameras = device(auth, 3);
        var first = cameras.getFirst();
        var second = cameras.get(1);
        long selected = second.path("cameraId").asLong();
        jdbc.update(
                "UPDATE camera_stream_profile SET enabled=FALSE WHERE channel_id=?",
                first.path("cameraId").asLong());
        long sub =
                jdbc.queryForObject(
                        "SELECT id FROM camera_stream_profile WHERE channel_id=? AND usage_hint='SUB'",
                        Long.class,
                        selected);
        addFact(selected, sub, "ACCESSIBLE");
        var page = read("/camera-status/devices?layer=CONTROL&observation=ACCESSIBLE&size=1", auth);
        assertThat(page.path("total").asInt()).isOne();
        assertThat(page.path("summary").path("accessible").asInt()).isOne();
        assertThat(page.path("items").get(0).path("cameraId").asLong()).isEqualTo(selected);
        assertThat(
                        page.path("items")
                                .get(0)
                                .path("profiles")
                                .get(0)
                                .path("streamProfileId")
                                .asLong())
                .isEqualTo(sub);
        assertThat(
                        read("/camera-status/devices?layer=CONTROL&observation=FAILED", auth)
                                .path("total")
                                .asInt())
                .isZero();
        assertThat(
                        read("/camera-status/devices?layer=CONTROL&observation=NEVER", auth)
                                .path("total")
                                .asInt())
                .isZero();
        assertThat(read("/camera-status/devices?layer=CONTROL&size=1&page=2", auth).path("items"))
                .isEmpty();
        var matchingOtherChannel = read("/camera-status/devices?layer=CONTROL&name=通道3", auth);
        assertThat(matchingOtherChannel.path("total").asInt()).isOne();
        assertThat(matchingOtherChannel.path("items").get(0).path("cameraId").asLong())
                .isEqualTo(selected);
        long otherGroup =
                write(post("/camera-groups"), auth, Map.of("name", "录像机另一个分组"), 201)
                        .path("groupId")
                        .asLong();
        jdbc.update(
                "UPDATE camera_channel SET group_id=? WHERE id=?",
                otherGroup,
                cameras.get(2).path("cameraId").asLong());
        var visibility =
                new com.streamfusion.platform.camera.service.CameraAccessService.Visibility(
                        true, 0, List.of());
        assertThat(
                        channels.statusGroupRepresentatives(List.of(otherGroup), visibility, 2001)
                                .getFirst()
                                .getId())
                .isEqualTo(selected);
        assertThat(
                        read("/camera-status/devices?layer=CONTROL&name=10.0.1.5", auth)
                                .path("total")
                                .asInt())
                .isOne();
        assertThat(
                        channels.statusDeviceRepresentatives(
                                        List.of("d7000"),
                                        new com.streamfusion.platform.camera.service
                                                .CameraAccessService.Visibility(true, 0, List.of()),
                                        2001)
                                .getFirst()
                                .getId())
                .isEqualTo(selected);
        verifyNoInteractions(worker);
    }

    @Test
    void devicePageAndManualChannelChoicesUseTheSameScopeWithoutExposingOtherDevices()
            throws Exception {
        var auth = admin();
        var cameras = device(auth, 2);
        var first = cameras.getFirst();
        var hidden = camera(auth, first.path("sourceId").asText(), "未授权独立相机");
        long group =
                jdbc.queryForObject(
                        "SELECT group_id FROM camera_channel WHERE id=?",
                        Long.class,
                        first.path("cameraId").asLong());
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                group,
                hidden.path("cameraId").asLong());
        String userId =
                write(post("/user"), auth, Map.of("username", "DeviceStatusViewer"), 201)
                        .path("id")
                        .asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8902,'DEVICE_STATUS_VIEW','设备状态查看','ENABLED',0)");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8902)", Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8902,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        write(
                put("/camera-scopes/users/" + userId),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupIds",
                        List.of(),
                        "cameraIds",
                        List.of(first.path("cameraId").asText())),
                200);
        var viewer = login("DeviceStatusViewer", "Initial1!");
        var page = read("/camera-status/devices?layer=CONTROL", viewer);
        assertThat(page.path("total").asInt()).isOne();
        assertThat(page.path("items").get(0).path("groupKey").asText()).isEqualTo("d7000");
        assertThat(read("/cameras/devices/d7000/channels?size=100", viewer).path("total").asInt())
                .isEqualTo(2);
        assertThat(
                        read("/camera-status/devices?layer=CONTROL&name=未授权", viewer)
                                .path("total")
                                .asInt())
                .isZero();
        jdbc.update(
                "UPDATE camera_channel SET group_id=NULL,lifecycle='PENDING_ASSIGNMENT' WHERE id=?",
                cameras.get(1).path("cameraId").asLong());
        assertThat(read("/cameras/devices/d7000/channels?size=100", viewer).path("total").asInt())
                .isOne();
        assertThat(read("/camera-status/devices?layer=CONTROL", viewer).path("total").asInt())
                .isOne();
        verifyNoInteractions(worker);
    }

    private List<JsonNode> device(Csrf auth, int count) throws Exception {
        String sourceId = source(auth).path("sourceId").asText();
        long group =
                write(post("/camera-groups"), auth, Map.of("name", "设备检查组"), 201)
                        .path("groupId")
                        .asLong();
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,device_type,local_name) VALUES(7000,?,'recorder','NVR','十六路录像机')",
                Long.valueOf(sourceId));
        var cameras = new ArrayList<JsonNode>();
        for (int index = 0; index < count; index++) {
            var camera = camera(auth, sourceId, "通道" + (index + 1));
            jdbc.update(
                    "UPDATE camera_channel SET device_id=7000,group_id=?,lifecycle='ENABLED' WHERE id=?",
                    group,
                    camera.path("cameraId").asLong());
            cameras.add(camera);
        }
        return cameras;
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
