package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.streamfusion.platform.support.CameraTestSupport;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

class CameraDeviceEditIntegrationTest extends CameraTestSupport {
    @Test
    void editsLocalDeviceInfoWithoutRenamingSharedSourceOrChannels() throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String channel = camera(auth, source, "通道一").path("cameraId").asText();
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,source_name,device_type) VALUES(701,?,'a','上游设备A','IPC'),(702,?,'b','上游设备B','IPC')",
                source,
                source);
        jdbc.update("UPDATE camera_channel SET device_id=701 WHERE id=?", channel);
        var before = read("/camera-devices/701", auth);
        var updated =
                write(
                        put("/camera-devices/701"),
                        auth,
                        Map.of("version", "0", "localName", "  入口相机  ", "remark", "北门"),
                        200);
        assertThat(updated.path("name").asText()).isEqualTo("入口相机");
        assertThat(updated.path("sourceName").asText()).isEqualTo("上游设备A");
        assertThat(updated.path("remark").asText()).isEqualTo("北门");
        assertThat(updated.path("version").asText()).isEqualTo("1");
        assertThat(
                        read("/cameras/devices/page?name=入口", auth)
                                .path("items")
                                .get(0)
                                .path("name")
                                .asText())
                .isEqualTo("入口相机");
        assertThat(read("/cameras/" + channel, auth).path("name").asText()).isEqualTo("通道一");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT local_name FROM camera_device WHERE id=702", String.class))
                .isNull();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT name FROM camera_source WHERE id=?", String.class, source))
                .isEqualTo("Test RTSP source");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='CAMERA_DEVICE_UPDATE'",
                                Integer.class))
                .isOne();

        write(put("/camera-devices/701"), auth, Map.of("version", "0", "localName", "过期改名"), 409);
        write(
                put("/camera-devices/701"),
                auth,
                Map.of("version", "1", "localName", "a".repeat(129)),
                400);
        write(
                put("/camera-devices/701"),
                auth,
                Map.of("version", "1", "remark", "a".repeat(501)),
                400);
        assertThat(
                        write(put("/camera-devices/701"), auth, Map.of("version", "1"), 200)
                                .path("version")
                                .asText())
                .isEqualTo("1");
        var clear = new LinkedHashMap<String, Object>();
        clear.put("version", "1");
        clear.put("localName", null);
        clear.put("remark", null);
        var restored = write(put("/camera-devices/701"), auth, clear, 200);
        assertThat(restored.path("name").asText()).isEqualTo(before.path("name").asText());
        assertThat(restored.path("remark").isNull()).isTrue();
        assertThat(restored.path("version").asText()).isEqualTo("2");
    }

    @Test
    void localNameTakesPrecedenceForDirectAndPlatformDevices() throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String channel = camera(auth, source, "通道").path("cameraId").asText();
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,source_name,device_type) VALUES(701,?,'a','上游设备','IPC')",
                source);
        jdbc.update("UPDATE camera_channel SET device_id=701 WHERE id=?", channel);
        jdbc.update("UPDATE camera_source SET connection_category='DEVICE' WHERE id=?", source);
        assertThat(read("/camera-devices/701", auth).path("name").asText())
                .isEqualTo("Test RTSP source");
        write(put("/camera-devices/701"), auth, Map.of("version", "0", "localName", "本地相机"), 200);
        for (String category : new String[] {"DEVICE", "PLATFORM"}) {
            jdbc.update(
                    "UPDATE camera_source SET connection_category=? WHERE id=?", category, source);
            assertThat(read("/camera-devices/701", auth).path("name").asText()).isEqualTo("本地相机");
            assertThat(
                            read("/cameras/devices/page", auth)
                                    .path("items")
                                    .get(0)
                                    .path("name")
                                    .asText())
                    .isEqualTo("本地相机");
        }
    }
}
