package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.delete;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.Map;
import org.junit.jupiter.api.Test;

class CameraDeviceDeleteIntegrationTest extends CameraTestSupport {
    private static final String DEVICE = "/cameras/devices/d701";

    private String device(Csrf auth, int count) throws Exception {
        String sourceId = source(auth).path("sourceId").asText();
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,source_name,device_type,version,created_at,updated_at) VALUES(701,?,'device-701','测试设备','IPC',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                sourceId);
        for (int i = 1; i <= count; i++)
            jdbc.update(
                    "INSERT INTO camera_channel(id,source_id,device_id,external_channel_key,name,lifecycle,mapping_origin,version,created_at,updated_at) VALUES(?,?,701,?,?,'PENDING_ASSIGNMENT','MANUAL',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                    800 + i,
                    sourceId,
                    "channel-" + i,
                    "通道" + i);
        return sourceId;
    }

    private JsonNode impact(Csrf auth) throws Exception {
        return read(DEVICE + "/delete-impact", auth);
    }

    private JsonNode deleteDevice(Csrf auth, JsonNode impact, int expected) throws Exception {
        return write(
                delete(DEVICE).header("If-Match", "\"" + impact.path("revision").asText() + "\""),
                auth,
                Map.of(),
                expected);
    }

    private int count(String sql, Object... args) {
        return jdbc.queryForObject(sql, Integer.class, args);
    }

    @Test
    void deletesEveryChannelAndExclusiveSourceIncludingConfiguredStreams() throws Exception {
        var auth = admin();
        String sourceId = device(auth, 100);
        String registered = camera(auth, sourceId, "有默认码流的通道").path("cameraId").asText();
        jdbc.update("UPDATE camera_channel SET device_id=701 WHERE id=?", registered);
        var impact = impact(auth);
        assertThat(impact.path("channelCount").asInt()).isEqualTo(101);
        assertThat(impact.path("removesSource").asBoolean()).isTrue();
        assertThat(deleteDevice(auth, impact, 200).path("removedSource").asBoolean()).isTrue();
        assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE source_id=?", sourceId))
                .isZero();
        assertThat(count("SELECT COUNT(*) FROM camera_stream_profile WHERE source_id=?", sourceId))
                .isZero();
        assertThat(count("SELECT COUNT(*) FROM camera_source_endpoint WHERE source_id=?", sourceId))
                .isZero();
        assertThat(count("SELECT COUNT(*) FROM camera_device WHERE id=701")).isZero();
        assertThat(count("SELECT COUNT(*) FROM camera_source WHERE id=?", sourceId)).isZero();
        assertThat(count("SELECT COUNT(*) FROM sys_operation_log WHERE action='CAMERA_DELETE'"))
                .isEqualTo(101);
    }

    @Test
    void leavesSharedConnectionAndOtherCameraInPlace() throws Exception {
        var auth = admin();
        String sourceId = device(auth, 2);
        String other = camera(auth, sourceId, "同来源的另一台").path("cameraId").asText();
        var impact = impact(auth);
        assertThat(impact.path("removesSource").asBoolean()).isFalse();
        assertThat(deleteDevice(auth, impact, 200).path("removedSource").asBoolean()).isFalse();
        assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE device_id=701")).isZero();
        assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE id=?", other)).isOne();
        assertThat(count("SELECT COUNT(*) FROM camera_source WHERE id=?", sourceId)).isOne();
    }

    @Test
    void leavesReusablePlatformConnectionEvenWhenAllItsChannelsAreDeleted() throws Exception {
        var auth = admin();
        String sourceId = device(auth, 2);
        jdbc.update("UPDATE camera_source SET connection_category='PLATFORM' WHERE id=?", sourceId);
        var impact = impact(auth);
        assertThat(impact.path("removesSource").asBoolean()).isFalse();
        assertThat(deleteDevice(auth, impact, 200).path("removedSource").asBoolean()).isFalse();
        assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE device_id=701")).isZero();
        assertThat(count("SELECT COUNT(*) FROM camera_source WHERE id=?", sourceId)).isOne();
    }

    @Test
    void requiresConfirmationAndRejectsChangedChannelOrSharingWithoutDeletingAnything()
            throws Exception {
        var auth = admin();
        String sourceId = device(auth, 2);
        write(delete(DEVICE), auth, Map.of(), 428);
        var original = impact(auth);
        jdbc.update("UPDATE camera_channel SET version=1 WHERE id=802");
        deleteDevice(auth, original, 412);
        assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE device_id=701")).isEqualTo(2);
        var revised = impact(auth);
        assertThat(revised.path("removesSource").asBoolean()).isTrue();
        camera(auth, sourceId, "确认后新增通道");
        deleteDevice(auth, revised, 412);
        assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE device_id=701")).isEqualTo(2);
    }

    @Test
    void directGrantsBlockPreviewAndCommitWithoutDeletingOtherChannels() throws Exception {
        var auth = admin();
        device(auth, 2);
        var original = impact(auth);
        var user =
                write(
                                org.springframework.test.web.servlet.request.MockMvcRequestBuilders
                                        .post("/user"),
                                auth,
                                Map.of("username", "DeleteViewer"),
                                201)
                        .path("id")
                        .asText();
        jdbc.update("INSERT INTO camera_user_scope(user_id) VALUES(?)", user);
        jdbc.update(
                "INSERT INTO camera_user_channel_grant(user_id,channel_id) VALUES(?,802)", user);
        assertBlocked(auth);
        deleteDevice(auth, original, 409);
        jdbc.update("DELETE FROM camera_user_channel_grant WHERE user_id=?", user);
        jdbc.update("INSERT INTO camera_user_device_grant(user_id,device_id) VALUES(?,701)", user);
        assertBlocked(auth);
        deleteDevice(auth, original, 409);
        assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE device_id=701")).isEqualTo(2);
    }

    @Test
    void rollsBackTheFirstChannelAndAuditWhenAWriteToTheSecondChannelFails() throws Exception {
        var auth = admin();
        String sourceId = device(auth, 2);
        var impact = impact(auth);
        jdbc.execute(
                "CREATE TABLE test_camera_delete_reference (channel_id BIGINT PRIMARY KEY REFERENCES camera_channel(id))");
        try {
            jdbc.update("INSERT INTO test_camera_delete_reference(channel_id) VALUES(802)");
            deleteDevice(auth, impact, 500);
            assertThat(count("SELECT COUNT(*) FROM camera_channel WHERE device_id=701"))
                    .isEqualTo(2);
            assertThat(count("SELECT COUNT(*) FROM camera_source WHERE id=?", sourceId)).isOne();
            assertThat(count("SELECT COUNT(*) FROM sys_operation_log WHERE action='CAMERA_DELETE'"))
                    .isZero();
        } finally {
            jdbc.execute("DROP TABLE test_camera_delete_reference");
        }
    }

    private void assertBlocked(Csrf auth) throws Exception {
        mvc.perform(get(DEVICE + "/delete-impact").session(auth.session()))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("CAMERA_REFERENCED"));
    }
}
